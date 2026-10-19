package landing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-gost/x/logsafe"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func quietManager(t *testing.T) (*Manager, *fakeClock, *[]string) {
	t.Helper()
	m := NewManager(t.TempDir(), true)
	clock := &fakeClock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	m.now = clock.now
	var lines []string
	m.logs = logsafe.NewRoutineLogger(func(s string) { lines = append(lines, s) })
	return m, clock, &lines
}

// The reconcile tick runs every 10 s; the 30 MiB Core must be hashed once per
// file change, not once per tick.
func TestCoreHashedOncePerFileChange(t *testing.T) {
	m, _, _ := quietManager(t)
	good := []byte("pinned core v1")
	saved := coreExpected
	coreExpected = func() (string, bool) { return sha(good), true }
	defer func() { coreExpected = saved }()
	hashes := 0
	m.hashFile = func(path string) (string, error) { hashes++; return fileHash(path) }
	m.coreDownload = func(context.Context, string, string, string, int64, os.FileMode) error {
		t.Fatal("verified Core must not be downloaded")
		return nil
	}
	if err := os.WriteFile(m.binary(), good, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err := m.install(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if hashes != 1 {
		t.Fatalf("Core hashed %d times over 6 ticks; want 1", hashes)
	}
	// Replacing the file (same bytes, new inode) forces exactly one new hash.
	tmp := m.binary() + ".new"
	if err := os.WriteFile(tmp, good, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, m.binary()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := m.install(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if hashes != 2 {
		t.Fatalf("replaced Core hashed %d times in total; want 2", hashes)
	}
}

// A failing download waits 1, 2, 4 … 30 min instead of retrying every tick,
// logs once (plus 10-min summaries), and recovers on success.
func TestCoreDownloadBacksOff(t *testing.T) {
	m, clock, lines := quietManager(t)
	good := []byte("pinned core v2")
	saved := coreExpected
	coreExpected = func() (string, bool) { return sha(good), true }
	defer func() { coreExpected = saved }()
	attempts, healthy := 0, false
	m.coreDownload = func(_ context.Context, _, path, _ string, _ int64, mode os.FileMode) error {
		attempts++
		if !healthy {
			return fmt.Errorf("all mirrors failed")
		}
		return os.WriteFile(path, good, mode)
	}
	tick := func() error { return m.install(context.Background()) }
	lastTry := clock.now()
	if err := tick(); err == nil || !strings.Contains(err.Error(), "后重试") || attempts != 1 {
		t.Fatalf("first failure: attempts=%d err=%v", attempts, err)
	}
	// 10 s ticks inside the first 1 min window never touch the network.
	for i := 0; i < 5; i++ {
		clock.advance(10 * time.Second)
		if err := tick(); err == nil {
			t.Fatal("backoff window reported success")
		}
	}
	if attempts != 1 {
		t.Fatalf("attempts=%d during the 1 min backoff; want 1", attempts)
	}
	wantGaps := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute}
	for i, gap := range wantGaps {
		clock.t = lastTry.Add(gap - 10*time.Second) // one tick short of the next try
		_ = tick()
		if attempts != i+1 {
			t.Fatalf("retry %d came before its %s gap", i+1, gap)
		}
		clock.t = lastTry.Add(gap)
		_ = tick()
		if attempts != i+2 {
			t.Fatalf("retry %d did not happen after %s (attempts=%d)", i+1, gap, attempts)
		}
		lastTry = clock.t
	}
	// 9 failures in ~92 min: one first-failure line plus at most one summary per 10 min.
	if len(*lines) == 0 || len(*lines) > 10 || !strings.Contains((*lines)[0], "首次失败") {
		t.Fatalf("log lines = %q", *lines)
	}
	healthy = true
	clock.advance(31 * time.Minute)
	if err := tick(); err != nil {
		t.Fatalf("recovered download: %v", err)
	}
	if !strings.Contains((*lines)[len(*lines)-1], "已恢复") {
		t.Fatalf("no recovery line: %q", *lines)
	}
	// After a success the next failure starts from the short delay again.
	if m.downloads["core"] != nil {
		t.Fatal("backoff state kept after success")
	}
}

func TestGeoDownloadBacksOffPerFile(t *testing.T) {
	m, clock, _ := quietManager(t)
	attempts := map[string]int{}
	m.geoDownload = func(_ context.Context, _, path, _ string, _ int64, _ os.FileMode) error {
		attempts[filepath.Base(path)]++
		return fmt.Errorf("offline")
	}
	for i := 0; i < 6; i++ { // one render per 10 s tick for a minute
		if err := m.ensureGeoData(context.Background(), []string{"geosite.dat"}); err == nil {
			t.Fatal("missing geo data reported as present")
		}
		clock.advance(10 * time.Second)
	}
	if attempts["geosite.dat"] != 1 {
		t.Fatalf("geosite.dat attempts=%d within the first minute; want 1", attempts["geosite.dat"])
	}
	clock.advance(time.Minute)
	_ = m.ensureGeoData(context.Background(), []string{"geosite.dat"})
	if attempts["geosite.dat"] != 2 {
		t.Fatalf("geosite.dat not retried after its backoff: %d", attempts["geosite.dat"])
	}
}

func TestResourceLimitsSetOnlyOnChange(t *testing.T) {
	m, _, _ := quietManager(t)
	var calls []string
	m.systemctl = func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	m.desired = Desired{Revision: 1, CoreMemoryMB: 256, CoreCPUPercent: 50}
	for i := 0; i < 5; i++ {
		if err := m.applyResourceLimits(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	m.desired.CoreMemoryMB = 384
	_ = m.applyResourceLimits(context.Background())
	_ = m.applyResourceLimits(context.Background())
	if len(calls) != 2 || !strings.Contains(calls[0], "MemoryMax=256M CPUQuota=50%") || !strings.Contains(calls[1], "MemoryMax=384M") {
		t.Fatalf("systemctl calls = %q; want one per change", calls)
	}
	// A failed call is retried on the next tick.
	m.desired.CoreCPUPercent = 80
	m.systemctl = func(context.Context, ...string) error { return fmt.Errorf("busy") }
	if err := m.applyResourceLimits(context.Background()); err == nil {
		t.Fatal("failure swallowed")
	}
	m.systemctl = func(_ context.Context, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	_ = m.applyResourceLimits(context.Background())
	if len(calls) != 3 {
		t.Fatalf("failed set-property not retried: %q", calls)
	}
}

// The periodic re-render must not re-read geoip.dat when neither the source
// lists nor the exemption changed.
func TestSourceExpansionReusedAcrossRenders(t *testing.T) {
	reads := 0
	read := func(code string) ([]netip.Prefix, error) {
		reads++
		return []netip.Prefix{netip.MustParsePrefix("1.0.0.0/8"), netip.MustParsePrefix("240e::/20")}, nil
	}
	plan := &Egress{Version: 1, ExemptSources: []string{"1.2.3.4"}, Rules: []EgressRule{
		{InboundTags: []string{"flvxl-1"}, SourceIP: []string{"geoip:cn"}, Outbound: "block"},
		{InboundTags: []string{"flvxl-2"}, SourceIP: []string{"geoip:cn"}, Port: "443", Outbound: "block"},
	}}
	memo := &sourceMemo{}
	render := func() []any {
		rules, err := renderRules(plan, read, memo)
		if err != nil {
			t.Fatal(err)
		}
		memo.rotate()
		return rules
	}
	first := render()
	if reads != 1 {
		t.Fatalf("identical source lists expanded %d times in one render; want 1", reads)
	}
	second := render()
	if reads != 1 || canonical(first) != canonical(second) {
		t.Fatalf("re-render re-read geo data (%d reads) or changed output", reads)
	}
	plan.ExemptSources = []string{"1.2.3.5"}
	third := render()
	if reads != 2 || canonical(third) == canonical(second) {
		t.Fatalf("exemption change not re-expanded (reads=%d)", reads)
	}
	if len(memo.prev) != 1 {
		t.Fatalf("memo keeps %d entries; want only the current one", len(memo.prev))
	}
}
