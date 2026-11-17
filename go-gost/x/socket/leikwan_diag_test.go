package socket

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type lkTimeoutErr struct{}

func (lkTimeoutErr) Error() string   { return "i/o timeout" }
func (lkTimeoutErr) Timeout() bool   { return true }
func (lkTimeoutErr) Temporary() bool { return true }

func lkDiagFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Shapes as read on 火山上海 11-17 (refs shortened, no secrets in these files).
	write("run/leikwan-fast-failover/forward-263.offered.json", `{"at": 1794906719.43, "offered": [
		{"endpoint": "113.45.231.245:5438", "ref": "M72"}, {"endpoint": "175.178.27.71:5438", "ref": "YAM"},
		{"endpoint": "125.94.47.216:5438", "ref": "vIG"}, {"endpoint": "121.37.28.219:5438", "ref": "B21"}]}`)
	write("run/leikwan-fast-failover/forward-263.lines.json", `{"H0y": {"endpoint": "114.132.73.125:5438", "seen": 1794902662.99},
		"B21": {"endpoint": "121.37.28.219:5438", "seen": 1794906719.43}, "bad": {"endpoint": "relay.example:5438"}}`)
	write("run/ultra/forward-263.status.json", `{"state": "ready",
		"latest_tcp": {"selection_ref": "H0y", "rtt_ms": 62, "selected_at": "2026-11-17T04:39:47Z"},
		"active_udp": {"selection_ref": "B21", "rtt_ms": 36, "p95_rtt_ms": 37, "probe_sent": 30, "probe_received": 29, "selected_at": "2026-11-17T09:07:26Z"}}`)
	return root
}

func TestLeiKwanDiagFixture(t *testing.T) {
	root := lkDiagFixture(t)
	var mu sync.Mutex
	dials := map[string]int{}
	old := lkDiagDial
	defer func() { lkDiagDial = old }()
	lkDiagDial = func(ctx context.Context, ep string, timeout time.Duration) (time.Duration, error) {
		mu.Lock()
		dials[ep]++
		mu.Unlock()
		switch ep {
		case "125.94.47.216:5438":
			return 0, &net.OpError{Op: "dial", Net: "tcp", Err: lkTimeoutErr{}}
		case "175.178.27.71:5438":
			if dials[ep] == 2 {
				return 0, errors.New("connect: connection refused")
			}
		}
		return 30 * time.Millisecond, nil
	}
	now := time.Date(2026, 11, 17, 9, 17, 26, 0, time.UTC)
	res, err := lkDiagRun(context.Background(), root, LeiKwanDiagRequest{ForwardID: 263, Count: 9, TimeoutMs: 50}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found || res.State != "ready" {
		t.Fatalf("found/state: %+v", res)
	}
	// 4 offered + the TCP in-use ingress that is no longer offered.
	if len(res.Ingresses) != 5 {
		t.Fatalf("ingresses: %+v", res.Ingresses)
	}
	byEP := map[string]LKDiagIngress{}
	for _, in := range res.Ingresses {
		byEP[in.Endpoint] = in
	}
	if in := byEP["121.37.28.219:5438"]; in.InUse != "udp" || !in.Offered || !in.OK || in.AvgMs != 30 || in.Loss != 0 || in.Sent != 5 {
		t.Fatalf("udp in-use ingress: %+v", in)
	}
	if in := byEP["114.132.73.125:5438"]; in.InUse != "tcp" || in.Offered {
		t.Fatalf("tcp in-use ingress (old offer): %+v", in)
	}
	// A timeout stops the target after the first try (no hammering), all lost.
	if in := byEP["125.94.47.216:5438"]; in.OK || in.Loss != 100 || in.Sent != 1 || in.Error != "连接超时" || dials["125.94.47.216:5438"] != 1 {
		t.Fatalf("dead ingress: %+v dials=%d", in, dials["125.94.47.216:5438"])
	}
	// count is capped at 5; one refused try = 20 % loss, still OK.
	if in := byEP["175.178.27.71:5438"]; !in.OK || in.Loss != 20 || in.Sent != 5 || dials["175.178.27.71:5438"] != 5 {
		t.Fatalf("lossy ingress: %+v", in)
	}
	if res.UDP == nil || res.UDP.RttMs != 36 || res.UDP.Sent != 30 || res.UDP.Recv != 29 || res.UDP.AgeS != 600 {
		t.Fatalf("udp client: %+v", res.UDP)
	}
	if res.TCP == nil || res.TCP.RttMs != 62 || res.TCP.AgeS != 16659 {
		t.Fatalf("tcp client: %+v", res.TCP)
	}
	if res.OfferAgeS < 0 {
		t.Fatalf("offer age: %d", res.OfferAgeS)
	}
}

func TestLeiKwanDiagMissingForward(t *testing.T) {
	root := lkDiagFixture(t)
	res, err := lkDiagRun(context.Background(), root, LeiKwanDiagRequest{ForwardID: 999}, time.Now())
	if err != nil || res.Found || len(res.Ingresses) != 0 {
		t.Fatalf("missing forward: %+v %v", res, err)
	}
	if _, err := lkDiagRun(context.Background(), root, LeiKwanDiagRequest{}, time.Now()); err == nil {
		t.Fatal("forward id 0 accepted")
	}
}

func TestLeiKwanDiagRealDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener")
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	root := t.TempDir()
	dir := filepath.Join(root, "run/leikwan-fast-failover")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "forward-7.offered.json"), []byte(`{"at":1,"offered":[{"endpoint":"`+ln.Addr().String()+`","ref":"a"}]}`), 0o600)
	res, err := handleLeiKwanDiag(context.Background(), root, map[string]interface{}{"forwardId": 7, "count": 2})
	if err != nil || len(res.Ingresses) != 1 || !res.Ingresses[0].OK || res.Ingresses[0].Sent != 2 {
		t.Fatalf("real dial: %+v %v", res, err)
	}
	if strings.Contains(res.Ingresses[0].Error, "timeout") {
		t.Fatal(res.Ingresses[0].Error)
	}
}
