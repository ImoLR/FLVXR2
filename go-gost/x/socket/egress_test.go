package socket

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

func TestDetectEgress(t *testing.T) {
	for _, want := range []string{"v4", "v6", "dual", ""} {
		t.Run(want, func(t *testing.T) {
			calls := map[string]int{}
			var peers []net.Conn
			defer func() {
				for _, peer := range peers {
					peer.Close()
				}
			}()
			got := detectEgress(context.Background(), func(ctx context.Context, network, addr string) (net.Conn, error) {
				calls[network]++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 3*time.Second {
					t.Fatal("probe must have a 3s deadline")
				}
				host, port, err := net.SplitHostPort(addr)
				ip := net.ParseIP(host)
				if err != nil || ip == nil || port != "443" || (network == "tcp4") != (ip.To4() != nil) {
					t.Fatalf("invalid probe %s %s", network, addr)
				}
				// First target fails; a later success must still establish capability.
				if calls[network] == 2 && (want == "dual" || (want == "v4" && network == "tcp4") || (want == "v6" && network == "tcp6")) {
					a, b := net.Pipe()
					peers = append(peers, b)
					return a, nil
				}
				return nil, errors.New("unreachable")
			})
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
			data, err := json.Marshal(SystemInfo{EgressIPFamily: got})
			if err != nil {
				t.Fatal(err)
			}
			var info map[string]interface{}
			json.Unmarshal(data, &info)
			if want == "" {
				if _, ok := info["egress_ip_family"]; ok {
					t.Fatal("unknown must not be reported")
				}
			} else if info["egress_ip_family"] != want {
				t.Fatalf("missing family: %s", data)
			}
		})
	}
}

func TestEgressDetectorDoesNotBlockReporter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, done := make(chan struct{}), make(chan struct{})
	d := egressDetector{last: "dual"}
	go func() {
		defer close(done)
		d.run(ctx, func(ctx context.Context, _, _ string) (net.Conn, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
	}()
	<-started
	if got := d.family(); got != "dual" {
		t.Fatalf("lost cached result: %s", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("detector did not stop")
	}
}
