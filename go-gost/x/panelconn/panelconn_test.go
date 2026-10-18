package panelconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestDialModes(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		fail4 bool
		want  []string
	}{
		{Auto, false, []string{"tcp"}}, {PreferIPv4, false, []string{"tcp4"}},
		{PreferIPv4, true, []string{"tcp4", "tcp6"}}, {IPv4Only, true, []string{"tcp4"}}, {IPv6Only, false, []string{"tcp6"}},
	} {
		t.Run(fmt.Sprintf("%s/%t", tc.mode, tc.fail4), func(t *testing.T) {
			var calls []string
			_, err := dialMode(context.Background(), "tcp", "panel.test:443", tc.mode, func(ctx context.Context, network, address string) (net.Conn, error) {
				calls = append(calls, network)
				if address != "panel.test:443" {
					t.Fatal(address)
				}
				if tc.fail4 && network == "tcp4" {
					return nil, errors.New("IPv4 unavailable")
				}
				return nil, nil
			})
			if !reflect.DeepEqual(calls, tc.want) || (err != nil) != (tc.mode == IPv4Only && tc.fail4) {
				t.Fatalf("calls=%v err=%v", calls, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	_, _ = dialMode(ctx, "tcp", "panel:443", PreferIPv4, func(ctx context.Context, _, _ string) (net.Conn, error) { calls++; return nil, ctx.Err() })
	if calls != 1 {
		t.Fatal("canceled dial retried")
	}
}

func TestSelfHealWindowExpiryAndExplicitModes(t *testing.T) {
	p := New()
	defer p.CloseIdleConnections()
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }
	var logs []string
	p.logf = func(f string, args ...interface{}) { logs = append(logs, fmt.Sprintf(f, args...)) }
	p.IPv6Failure()
	now = now.Add(10*time.Minute + time.Second)
	p.IPv6Failure()
	if p.effective() != Auto {
		t.Fatal("expired failure counted")
	}
	now = now.Add(time.Minute)
	p.IPv6Failure()
	if p.effective() != PreferIPv4 || p.Mode() != Auto || len(logs) != 1 {
		t.Fatal("heal not activated exactly once")
	}
	p.IPv6Failure()
	_, _ = p.SetMode("") // reloading unchanged config must preserve self-heal
	now = now.Add(time.Hour - time.Second)
	if p.effective() != PreferIPv4 || len(logs) != 1 {
		t.Fatal("early expiry or repeated log")
	}
	now = now.Add(time.Second)
	if p.effective() != Auto || len(logs) != 2 {
		t.Fatal("heal did not expire")
	}
	_ = p.effective()
	if len(logs) != 2 {
		t.Fatal("repeated expiry log")
	}
	for _, mode := range []string{IPv4Only, IPv6Only, PreferIPv4} {
		_, _ = p.SetMode(mode)
		p.IPv6Failure()
		p.IPv6Failure()
		if p.effective() != mode || len(logs) != 2 {
			t.Fatal("explicit policy overridden")
		}
	}
	if _, err := p.SetMode("invalid"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestHTTPFamilyAndPoolChange(t *testing.T) {
	v4, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer v4.Close()
	port := v4.Addr().(*net.TCPAddr).Port
	v6, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer v6.Close()
	s4 := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "IPv4") })}
	s6 := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "IPv6") })}
	defer s4.Close()
	defer s6.Close()
	go s4.Serve(v4)
	go s6.Serve(v6)
	p := New()
	defer p.CloseIdleConnections()
	// Do not depend on the host having an ::1 entry for localhost.
	for mode, tr := range p.transports {
		mode := mode
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialMode(ctx, network, "panel.test", mode, func(ctx context.Context, network, _ string) (net.Conn, error) {
				addr := v4.Addr().String()
				if network == "tcp6" {
					addr = v6.Addr().String()
				}
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			})
		}
	}
	c := &http.Client{Transport: p, Timeout: 2 * time.Second}
	for _, tc := range []struct{ mode, family string }{{IPv4Only, "IPv4"}, {IPv6Only, "IPv6"}, {PreferIPv4, "IPv4"}, {IPv6Only, "IPv6"}} {
		_, _ = p.SetMode(tc.mode)
		resp, err := c.Get(fmt.Sprintf("http://localhost:%d/flow/config", port))
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(b) != tc.family {
			t.Fatalf("%s: %s, %v", tc.mode, b, err)
		}
	}
}
