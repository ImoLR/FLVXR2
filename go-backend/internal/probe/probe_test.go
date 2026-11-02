package probe

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyerOpaqueStable(t *testing.T) {
	a, b := NewKeyer("secret-a"), NewKeyer("secret-b")
	k1 := a.Key(1)
	if len(k1) != 10 || k1 != a.Key(1) || k1 != NewKeyer("secret-a").Key(1) {
		t.Fatalf("key not stable / wrong length: %q", k1)
	}
	if a.Key(2) == k1 || b.Key(1) == k1 {
		t.Fatal("keys must differ per node and per secret")
	}
	seen := map[int64]bool{}
	for id := int64(1); id <= 500; id++ {
		v := a.PublicID(id)
		if v <= 0 || v >= 1<<53 || v == id || seen[v] {
			t.Fatalf("public id %d for %d: not opaque / unique / JS-safe", v, id)
		}
		seen[v] = true
	}
}

func TestParseOSArch(t *testing.T) {
	cases := map[string][2]string{
		"3.0.27-fork.103 (debian/amd64)": {"debian", "amd64"},
		"v1 (Ubuntu/arm64)":              {"ubuntu", "arm64"},
		"3.0.27":                         {"", ""},
		"x (evil<script>/a b c)":         {"evilscript", "abc"},
		"x (only)":                       {"only", ""},
		"x (broken":                      {"", ""},
	}
	for in, want := range cases {
		o, a := ParseOSArch(in)
		if o != want[0] || a != want[1] {
			t.Errorf("%q → %q/%q, want %v", in, o, a, want)
		}
	}
	if CleanRegion("jp") != "JP" || CleanRegion("JPN") != "" || CleanRegion("1a") != "" {
		t.Fatal("CleanRegion")
	}
}

func keysOf(t *testing.T, v interface{}) []string {
	t.Helper()
	raw, _ := json.Marshal(v)
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func demoInput(now time.Time) BuildInput {
	k := NewKeyer("s")
	return BuildInput{
		Title: "探针", LatencyOn: true, Now: now,
		Nodes: []NodeInput{
			{ID: 11, Name: "东京", Region: "jp", Version: "3.0.27-fork.104 (debian/amd64)"},
			{ID: 12, Name: "香港", Region: "HK", Version: "3.0.27-fork.104 (ubuntu/arm64)", LastSeen: now.Add(-time.Hour).UnixMilli()},
			{ID: 13, Name: "新加坡", Region: "", Version: ""},
		},
		Samples: map[int64]Sample{
			11: {At: now.Add(-time.Second).UnixMilli(), CPU: 12.345, Mem: 50.04, Disk: 30, Load1: 0.123, NetIn: 100, NetOut: 200, RxTotal: 1 << 30, TxTotal: 2 << 30, Uptime: 3600, TCP: 42},
			12: {At: now.Add(-time.Hour).UnixMilli(), CPU: 99},
			13: {At: now.Add(-time.Minute).UnixMilli(), CPU: 77}, // online but stale
		},
		Online: func(id int64) bool { return id != 12 },
		Edges: map[int64][]Edge{
			11: {{From: 11, To: 99, MS: 5, OK: true}, {From: 11, To: 13, MS: 70.06, OK: true}, {From: 11, To: 12, OK: false}},
		},
		Key: k.Key,
	}
}

func TestBuildSnapshotWhitelist(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	in := demoInput(now)
	snap := BuildSnapshot(in)
	if got := strings.Join(keysOf(t, snap), ","); got != "latency_on,nodes,title,ts" {
		t.Fatalf("snapshot keys %s", got)
	}
	want := "arch,cpu,disk,k,latency,load1,mem,name,net_in,net_out,online,os,region,seen,tcp,traffic_in,traffic_out,uptime"
	if got := strings.Join(keysOf(t, snap.Nodes[0]), ","); got != want {
		t.Fatalf("node keys\n got %s\nwant %s", got, want)
	}
	if got := strings.Join(keysOf(t, snap.Nodes[0].Latency[0]), ","); got != "ms,ok,to" {
		t.Fatalf("latency keys %s", got)
	}
	n := snap.Nodes[0]
	if n.CPU != 12.3 || n.Mem != 50 || n.Load1 != 0.12 || n.OS != "debian" || n.Arch != "amd64" || n.Region != "JP" || !n.Online || n.TCP != 42 {
		t.Fatalf("node 0 = %+v", n)
	}
	// Latency: unknown target 99 dropped; order follows the shown nodes.
	if len(n.Latency) != 2 || n.Latency[0].To != in.Key(12) || n.Latency[0].OK || n.Latency[1].To != in.Key(13) || n.Latency[1].MS != 70.1 {
		t.Fatalf("latency rows %+v", n.Latency)
	}
	off := snap.Nodes[1]
	if off.Online || off.CPU != 0 || off.Seen != now.Add(-time.Hour).UnixMilli() {
		t.Fatalf("offline node shows data: %+v", off)
	}
	if stale := snap.Nodes[2]; !stale.Online || stale.CPU != 0 {
		t.Fatalf("stale sample shown: %+v", stale)
	}
	raw, _ := json.Marshal(snap)
	for _, bad := range []string{`"id"`, `"version"`, "fork.104", `"11"`, "secret"} {
		if bytes.Contains(raw, []byte(bad)) {
			t.Fatalf("snapshot contains %s: %s", bad, raw)
		}
	}
	in.LatencyOn = false
	if s := BuildSnapshot(in); s.Nodes[0].Latency != nil || s.LatencyOn {
		t.Fatal("latency shown while off")
	}
}

// Plan 189: custom groups — order kept, multi-membership, hidden / deleted
// members and empty groups dropped (indexes renumbered), whitelist keys.
func TestBuildSnapshotGroups(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	in := demoInput(now)
	in.Groups = []GroupInput{
		{Name: "隐藏组", Nodes: []int64{99, 77}}, // only hidden / deleted members → not published
		{Name: "IXP", Nodes: []int64{12, 11, 11}},
		{Name: "空组"},
		{Name: "三网优化", Nodes: []int64{11, 99}},
	}
	in.GroupMode = GroupModeGroup
	snap := BuildSnapshot(in)
	if got := strings.Join(keysOf(t, snap), ","); got != "group_mode,groups,latency_on,nodes,title,ts" {
		t.Fatalf("snapshot keys %s", got)
	}
	if len(snap.Groups) != 2 || snap.Groups[0].Name != "IXP" || snap.Groups[1].Name != "三网优化" || snap.GroupMode != GroupModeGroup {
		t.Fatalf("groups %+v mode %q", snap.Groups, snap.GroupMode)
	}
	if got := strings.Join(keysOf(t, snap.Groups[0]), ","); got != "name" {
		t.Fatalf("group keys %s", got)
	}
	if g := snap.Nodes[0].G; len(g) != 2 || g[0] != 0 || g[1] != 1 {
		t.Fatalf("node 11 in both groups once each: %v", g)
	}
	if g := snap.Nodes[1].G; len(g) != 1 || g[0] != 0 {
		t.Fatalf("node 12 groups %v", g)
	}
	if snap.Nodes[2].G != nil {
		t.Fatalf("node 13 is in no group: %v", snap.Nodes[2].G)
	}
	want := "arch,cpu,disk,g,k,latency,load1,mem,name,net_in,net_out,online,os,region,seen,tcp,traffic_in,traffic_out,uptime"
	if got := strings.Join(keysOf(t, snap.Nodes[0]), ","); got != want {
		t.Fatalf("node keys\n got %s\nwant %s", got, want)
	}
	raw, _ := json.Marshal(snap)
	for _, bad := range []string{"隐藏组", "空组", `"id"`, `"nodes":[1`, `"99"`, `"11"`} {
		if bytes.Contains(raw, []byte(bad)) {
			t.Fatalf("snapshot contains %s: %s", bad, raw)
		}
	}
	// Hide node 11 and 12 → no group left at all, fields omitted.
	in.Nodes = in.Nodes[2:]
	if s := BuildSnapshot(in); s.Groups != nil || s.GroupMode != "" || s.Nodes[0].G != nil {
		t.Fatalf("groups of hidden nodes published: %+v", s)
	}
	// Default mode is region; unknown modes fall back to it.
	in = demoInput(now)
	in.Groups = []GroupInput{{Name: "IXP", Nodes: []int64{13}}}
	in.GroupMode = "bogus"
	if s := BuildSnapshot(in); s.GroupMode != GroupModeRegion || len(s.Groups) != 1 {
		t.Fatalf("mode %q groups %+v", s.GroupMode, s.Groups)
	}
}

func TestBuildSnapshotHiddenTargetDropped(t *testing.T) {
	now := time.Now()
	in := demoInput(now)
	in.Nodes = in.Nodes[:1] // 12 and 13 hidden by the caller
	snap := BuildSnapshot(in)
	if len(snap.Nodes) != 1 || len(snap.Nodes[0].Latency) != 0 {
		t.Fatalf("hidden nodes leak as latency targets: %+v", snap.Nodes)
	}
	raw, _ := json.Marshal(snap)
	if bytes.Contains(raw, []byte(in.Key(12))) || bytes.Contains(raw, []byte(in.Key(13))) {
		t.Fatal("hidden node key in snapshot")
	}
}

func TestLatencyBook(t *testing.T) {
	var b LatencyBook
	t0 := time.Unix(1_800_000_000, 0)
	b.Record([]Edge{{From: 1, To: 2, MS: 50, OK: true}, {From: 1, To: 2, MS: 40, OK: true}, {From: 1, To: 2, OK: false},
		{From: 1, To: 3, OK: false}, {From: 2, To: 1, MS: 9, OK: true}, {From: 4, To: 4, MS: 1, OK: true}}, t0)
	latest := b.Latest(t0, time.Minute)
	if len(latest[1]) != 2 || latest[1][0].MS != 40 || !latest[1][0].OK || latest[1][1].OK || len(latest[2]) != 1 || len(latest[4]) != 0 {
		t.Fatalf("dedupe: %+v", latest)
	}
	for i := 1; i <= 70; i++ {
		b.Record([]Edge{{From: 1, To: 2, MS: float64(i), OK: true}}, t0.Add(time.Duration(i)*time.Minute))
	}
	h := b.History(1)
	if len(h[2]) != LatencyRounds || h[2][LatencyRounds-1][1] != 70 || h[2][0][1] != 11 {
		t.Fatalf("ring: n=%d last=%v first=%v", len(h[2]), h[2][len(h[2])-1], h[2][0])
	}
	if got := b.Latest(t0.Add(70*time.Minute), time.Minute); len(got[1]) != 1 || len(got[2]) != 0 {
		t.Fatalf("old pairs must age out of Latest: %+v", got)
	}
	b.Record(nil, t0.Add(5*time.Hour))
	if b.Pairs() != 0 {
		t.Fatalf("pairs not pruned: %d", b.Pairs())
	}
}

func TestClampPointsAndStep(t *testing.T) {
	for in, want := range map[int]int{0: 360, -5: 360, 1: 60, 61: 120, 719: 720, 100000: 720} {
		if got := ClampPoints(in); got != want {
			t.Errorf("ClampPoints(%d) = %d", in, got)
		}
	}
	if StepFor(time.Hour, 720) != 5000 || StepFor(24*time.Hour, 720) != 120_000 || StepFor(6*time.Hour, 720) != 30_000 {
		t.Fatal("StepFor")
	}
	if _, ok := ParseRange("7d"); ok {
		t.Fatal("unknown range accepted")
	}
}

type fakeClock struct{ t atomic.Int64 }

func (c *fakeClock) now() time.Time      { return time.Unix(0, c.t.Load()) }
func (c *fakeClock) add(d time.Duration) { c.t.Add(int64(d)) }
func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.t.Store(time.Unix(1_800_000_000, 0).UnixNano())
	return c
}
func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(zr)
	return out
}

func TestHubBuildsOncePerIntervalAndShares(t *testing.T) {
	clock := newFakeClock()
	var builds atomic.Int64
	hub := NewHub(HubConfig{Interval: time.Hour, Now: clock.now, Build: func(time.Time) ([]byte, error) {
		builds.Add(1)
		return []byte(`{"code":0,"data":{"nodes":[]}}`), nil
	}})
	f1, _ := hub.Frame()
	f2, _ := hub.Frame()
	if builds.Load() != 1 || f1 != f2 {
		t.Fatalf("builds=%d", builds.Load())
	}
	if !bytes.Equal(gunzip(t, f1.Gzip), f1.Raw) {
		t.Fatal("gzip differs from raw")
	}
	a, _ := hub.Join("1.1.1.1", true)
	b, _ := hub.Join("2.2.2.2", false)
	hub.Offer(a, f1)
	hub.Offer(b, f1)
	fa, fb := <-a.Send, <-b.Send
	if &fa.Gzip[0] != &fb.Gzip[0] || &fa.Raw[0] != &fb.Raw[0] {
		t.Fatal("viewers must get the same byte slices")
	}
	clock.add(2 * time.Hour)
	f3, _ := hub.Frame()
	if builds.Load() != 2 || f3.Seq != f1.Seq+1 || f3.ETag == f1.ETag {
		t.Fatalf("rebuild after interval: builds=%d", builds.Load())
	}
	hub.Leave(a)
	hub.Leave(b)
}

func TestHubCapsAndSlowClient(t *testing.T) {
	hub := NewHub(HubConfig{Interval: time.Hour, MaxPerIP: 2, MaxTotal: 3, Queue: 1, Build: func(time.Time) ([]byte, error) { return []byte(`{}`), nil }})
	c1, _ := hub.Join("a", true)
	c2, _ := hub.Join("a", true)
	if _, err := hub.Join("a", true); !errors.Is(err, ErrPerIP) {
		t.Fatalf("per-IP cap: %v", err)
	}
	c3, _ := hub.Join("b", true)
	if _, err := hub.Join("c", true); !errors.Is(err, ErrTotal) {
		t.Fatalf("total cap: %v", err)
	}
	f, _ := hub.Frame()
	hub.Offer(c1, f)
	hub.Offer(c1, f) // queue 1 → slow → dropped
	select {
	case <-c1.Done:
	default:
		t.Fatal("slow client not dropped")
	}
	if c1.Reason() != KickSlow || hub.Stats().Slow != 1 {
		t.Fatalf("reason %q stats %+v", c1.Reason(), hub.Stats())
	}
	hub.Leave(c1)
	hub.Leave(c1) // idempotent
	if _, err := hub.Join("a", true); err != nil {
		t.Fatalf("slot not freed: %v", err)
	}
	hub.Leave(c2)
	hub.Leave(c3)
}

func TestHubLoopOnlyWithViewersAndSwitchOff(t *testing.T) {
	var builds atomic.Int64
	var on atomic.Bool
	on.Store(true)
	hub := NewHub(HubConfig{Interval: 20 * time.Millisecond, Enabled: on.Load, Build: func(time.Time) ([]byte, error) {
		builds.Add(1)
		return []byte(`{}`), nil
	}})
	time.Sleep(80 * time.Millisecond)
	if builds.Load() != 0 || hub.Running() {
		t.Fatal("hub works with nobody watching")
	}
	c, _ := hub.Join("x", false)
	select {
	case <-c.Send:
	case <-time.After(2 * time.Second):
		t.Fatal("no frame pushed")
	}
	on.Store(false)
	select {
	case <-c.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("switch off did not close the stream")
	}
	if c.Reason() != KickOff {
		t.Fatalf("reason %q", c.Reason())
	}
	hub.Leave(c)
	deadline := time.Now().Add(2 * time.Second)
	for hub.Running() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.Running() {
		t.Fatal("loop still running without viewers")
	}
	n := builds.Load()
	time.Sleep(80 * time.Millisecond)
	if builds.Load() != n {
		t.Fatal("builds continue without viewers")
	}
}

// TestLatencyBookMemory: the rings stay small (resource budget, plan 187).
func TestLatencyBookMemory(t *testing.T) {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var b LatencyBook
	t0 := time.Unix(1_800_000_000, 0)
	const pairs = 200
	for round := 0; round < LatencyRounds; round++ {
		edges := make([]Edge, 0, pairs)
		for i := 0; i < pairs; i++ {
			edges = append(edges, Edge{From: int64(i/20 + 1), To: int64(i%20 + 100), MS: float64(i), OK: true})
		}
		b.Record(edges, t0.Add(time.Duration(round)*time.Minute))
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	per := float64(after.HeapAlloc-before.HeapAlloc) / pairs
	t.Logf("latency rings: %d pairs × %d rounds ≈ %.0f B per pair (%.1f KB total)", b.Pairs(), LatencyRounds, per, per*pairs/1024)
	if b.Pairs() != pairs || per > 4096 {
		t.Fatalf("pairs %d, %.0f B per pair", b.Pairs(), per)
	}
}

// TestKeyerSourceQualified: plan 195 — a 救命版 machine (negative id) gets a
// key of its own source ("old:<id>"), never the FLVX key of the same number;
// FLVX keys are unchanged.
func TestKeyerSourceQualified(t *testing.T) {
	k := NewKeyer("secret")
	if k.Key(-35) == k.Key(35) || len(k.Key(-35)) != 10 || k.Key(-35) != NewKeyer("secret").Key(-35) {
		t.Fatalf("old key %q vs flvx %q", k.Key(-35), k.Key(35))
	}
	if k.Key(-35) == NewKeyer("other").Key(-35) {
		t.Fatal("key does not depend on the secret")
	}
}

// TestBuildSnapshotSampleMaxAge: 救命版 rows arrive in batches; a 40 s old
// sample is live data for such a machine but not for an FLVX node.
func TestBuildSnapshotSampleMaxAge(t *testing.T) {
	now := time.Now()
	old := Sample{At: now.Add(-40 * time.Second).UnixMilli(), CPU: 12}
	snap := BuildSnapshot(BuildInput{
		Now:     now,
		Nodes:   []NodeInput{{ID: 1, Name: "a"}, {ID: -2, Name: "b", SampleMaxAge: 45 * time.Second}},
		Samples: map[int64]Sample{1: old, -2: old},
		Online:  func(int64) bool { return true },
		Key:     NewKeyer("s").Key,
	})
	if snap.Nodes[0].CPU != 0 || snap.Nodes[1].CPU != 12 {
		t.Fatalf("cpu %v / %v", snap.Nodes[0].CPU, snap.Nodes[1].CPU)
	}
}
