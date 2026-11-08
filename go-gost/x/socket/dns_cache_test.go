package socket

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gost/x/dnscache"
)

func TestDNSCacheCapInHandshake(t *testing.T) {
	if q := buildWebSocketQuery("s", "v", 0, 0, 0, 0); !strings.Contains(q, "&dnsCache=1") {
		t.Fatalf("handshake lacks dnsCache cap: %s", q)
	}
}

func TestDNSCacheDetectorReportsCopy(t *testing.T) {
	var calls atomic.Int32
	d := &dnsCacheDetector{check: func() dnscache.Status {
		calls.Add(1)
		return dnscache.Status{Mode: "dnsmasq", OK: true, CheckedAt: 1}
	}}
	if d.result() != nil {
		t.Fatal("result before the first check must be nil (old-agent shape: no dns_cache key)")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for d.result() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	r := d.result()
	if r == nil || r.Mode != "dnsmasq" || !r.OK {
		t.Fatalf("result = %+v", r)
	}
	r.Mode = "changed"
	if d.result().Mode != "dnsmasq" {
		t.Fatal("result must be a copy")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("one check at start, then every %v; got %d checks", dnsCacheInterval, n)
	}
}

func TestDNSCacheEnsureSingleFlightAndGap(t *testing.T) {
	release := make(chan struct{})
	var runs atomic.Int32
	var gotZones []string
	d := &dnsCacheDetector{
		check: func() dnscache.Status { return dnscache.Status{Mode: "dnsmasq", OK: true} },
		ensure: func(ctx context.Context, zones []string) (string, error) {
			runs.Add(1)
			gotZones = zones
			<-release
			return "[flvx-dns-cache] x\nDNS 缓存：本机 dnsmasq（127.0.0.1，上游 1.1.1.1）已配置\nRESULT mode=dnsmasq", nil
		},
	}
	var wg sync.WaitGroup
	wg.Add(1)
	var first map[string]interface{}
	var firstErr error
	go func() {
		defer wg.Done()
		first, firstErr = d.handleEnsure(context.Background(), map[string]interface{}{"zones": []string{"imgamer.top"}})
	}()
	for runs.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if _, err := d.handleEnsure(context.Background(), nil); !errors.Is(err, dnscache.ErrBusy) {
		t.Fatalf("second concurrent run: err = %v, want ErrBusy", err)
	}
	close(release)
	wg.Wait()
	if firstErr != nil || first["summary"] != "DNS 缓存：本机 dnsmasq（127.0.0.1，上游 1.1.1.1）已配置" {
		t.Fatalf("first = %v %v", first, firstErr)
	}
	if st, _ := first["dns_cache"].(*dnscache.Status); st == nil || !st.OK {
		t.Fatalf("dns_cache after ensure = %v", first["dns_cache"])
	}
	if len(gotZones) != 1 || gotZones[0] != "imgamer.top" {
		t.Fatalf("zones = %v", gotZones)
	}
	if _, err := d.handleEnsure(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "20 秒") {
		t.Fatalf("run inside the min gap: err = %v", err)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs = %d", runs.Load())
	}
}
