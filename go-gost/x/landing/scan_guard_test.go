package landing

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// scanCore is a fake Core control socket with /v1/features and /v1/scan.
type scanCore struct {
	mu       sync.Mutex
	capable  bool
	report   string // GET /v1/scan body
	scanURLs []string
	bodies   [][]byte
}

func newScanCore(t *testing.T, m *Manager, capable bool) *scanCore {
	t.Helper()
	c := &scanCore{capable: capable, report: `{"version":1,"hits":[],"blocked":[]}`}
	if err := os.MkdirAll(filepath.Dir(m.socket()), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", m.socket())
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		switch {
		case r.URL.Path == "/v1/features":
			features := `["anytls.downlink_stats","flvx.reality_guard_hop_uncounted"]`
			if c.capable {
				features = `["anytls.downlink_stats","flvx.reality_guard_hop_uncounted","scan_guard.v1"]`
			}
			_, _ = w.Write([]byte(`{"features":` + features + `}`))
		case r.URL.Path == "/v1/scan":
			if !c.capable {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			c.scanURLs = append(c.scanURLs, r.URL.String())
			_, _ = w.Write([]byte(c.report))
		case r.Method == http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			c.bodies = append(c.bodies, b)
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })
	return c
}

func (c *scanCore) set(report string) { c.mu.Lock(); c.report = report; c.mu.Unlock() }
func (c *scanCore) last(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		t.Fatal("no config pushed")
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(c.bodies[len(c.bodies)-1], &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// tick runs the scan part of one reconcile: features + /v1/scan, then the
// limits push.
func scanTick(t *testing.T, m *Manager, epoch string) {
	t.Helper()
	m.epoch = epoch
	m.readScan(context.Background())
	if err := m.applyLimits(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
}

func blockedTags(t *testing.T, cfg map[string]json.RawMessage) map[string]bool {
	t.Helper()
	var ids []map[string]string
	if err := json.Unmarshal(cfg["blocked_identities"], &ids); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, id := range ids {
		out[id["inbound_tag"]+"/"+id["user"]] = true
	}
	return out
}

const scanHitJSON = `{"identity":"flvxl-1","type":"horizontal","count":100,"port":22,"host":"","window_s":60,"at":"2026-11-03T12:30:00Z","seq":1793709225987084}`

func TestScanGuardOldCoreSendsNothing(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	core := newScanCore(t, m, false)
	m.desired = Desired{Enabled: true, Nodes: []Node{testNode(t, 1)}, ScanGuard: &ScanGuard{Enabled: true, Identities: []string{"flvxl-1"}}}
	scanTick(t, m, "e1")
	cfg := core.last(t)
	if cfg["scan_guard"] != nil || cfg["scan_unblock"] != nil {
		t.Fatalf("old Core got scan keys: %s", core.bodies[0])
	}
	if len(core.scanURLs) != 0 {
		t.Fatal("old Core asked for /v1/scan")
	}
	if st := m.scanStatus(); st.State != "core_update" || len(st.Hits) != 0 {
		t.Fatalf("status %+v", st)
	}
	// Guard off on an old Core: state off, still nothing sent.
	m.desired.ScanGuard = nil
	scanTick(t, m, "e1")
	if cfg := core.last(t); cfg["scan_guard"] != nil || m.scanStatus().State != "off" {
		t.Fatal("guard off must stay off")
	}
}

func TestScanGuardHitHoldRecordUnblock(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, true)
	core := newScanCore(t, m, true)
	n1, n2 := testNode(t, 1), testNode(t, 2)
	guard := func(seen int64) *ScanGuard {
		return &ScanGuard{Enabled: true, Identities: []string{"flvxl-1", "flvxl-2"}, Horizontal: 100, HorizontalWeb: 500, WebPorts: []int{80, 443}, WindowS: 60, Vertical: 30, Total: 1000, TotalWindowS: 300, Seen: seen}
	}
	m.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{n1, n2}, ScanGuard: guard(0)}
	if err := m.desired.validate(); err != nil {
		t.Fatal(err)
	}
	scanTick(t, m, "e1")
	cfg := core.last(t)
	var sg map[string]any
	if err := json.Unmarshal(cfg["scan_guard"], &sg); err != nil {
		t.Fatal(err)
	}
	if sg["enabled"] != true || len(sg["identities"].([]any)) != 2 || sg["horizontal"].(float64) != 100 || sg["total_window_s"].(float64) != 300 {
		t.Fatalf("scan_guard %v", sg)
	}
	if cfg["scan_unblock"] != nil || m.scanStatus().State != "on" || m.ScanSeq() != 0 {
		t.Fatal("idle guard must not unblock or report")
	}

	// The Core blocked node 1: the hit is pending, held by the agent too, and
	// node 1 leaves the guard list (counting stops while blocked).
	core.set(`{"version":1,"hits":[` + scanHitJSON + `],"blocked":[` + scanHitJSON + `]}`)
	scanTick(t, m, "e1")
	cfg = core.last(t)
	st := m.scanStatus()
	if len(st.Hits) != 1 || st.Hits[0].ID != 1 || st.Hits[0].Port != 22 || st.Hits[0].Type != "horizontal" || st.Hits[0].Seq != 1793709225987084 {
		t.Fatalf("pending %+v", st.Hits)
	}
	if b := blockedTags(t, cfg); !b["flvxl-1/flvxl-1"] || !b["flvxl-1/"] || b["flvxl-2/flvxl-2"] {
		t.Fatalf("held node not blocked: %v", b)
	}
	if cfg["scan_unblock"] != nil || m.ScanSeq() != 1793709225987084 || st.Blocked[0] != "flvxl-1" {
		t.Fatal("unrecorded hit must stay blocked")
	}
	if err := json.Unmarshal(cfg["scan_guard"], &sg); err != nil || len(sg["identities"].([]any)) != 1 {
		t.Fatalf("blocked node still counted: %v", sg)
	}
	if _, err := os.Stat(filepath.Join(dir, "scan.json")); err != nil {
		t.Fatal("pending hit not persisted")
	}
	// The next read starts after the last seq.
	scanTick(t, m, "e1")
	if u := core.scanURLs[len(core.scanURLs)-1]; !strings.Contains(u, "after=1793709225987084") {
		t.Fatalf("scan cursor not used: %s", u)
	}

	// A restarted agent (same dir) on a restarted Core keeps holding it.
	m2 := NewManager(dir, true)
	core.mu.Lock()
	core.report = `{"version":1,"hits":[],"blocked":[]}`
	core.mu.Unlock()
	m2.desired = m.desired
	scanTick(t, m2, "e2")
	if b := blockedTags(t, core.last(t)); !b["flvxl-1/flvxl-1"] || len(m2.scanStatus().Hits) != 1 {
		t.Fatal("persisted hit not held after restart")
	}

	// The panel recorded it (seen) and disabled node 1: nothing pending, the
	// Core block stays (node disabled), scan.json removed.
	core.set(`{"version":1,"hits":[],"blocked":[` + scanHitJSON + `]}`)
	n1.Enabled = false
	m.desired = Desired{Enabled: true, Nodes: []Node{n1, n2}, ScanGuard: guard(1793709225987084)}
	scanTick(t, m, "e1")
	cfg = core.last(t)
	if len(m.scanStatus().Hits) != 0 || m.ScanSeq() != 0 || cfg["scan_unblock"] != nil {
		t.Fatalf("recorded hit still pending or lifted: %s", cfg["scan_unblock"])
	}
	if _, err := os.Stat(filepath.Join(dir, "scan.json")); !os.IsNotExist(err) {
		t.Fatal("scan.json kept after the panel recorded the hit")
	}

	// 解除禁用: node 1 enabled again -> the Core block is lifted with its seq.
	n1.Enabled = true
	m.desired = Desired{Enabled: true, Nodes: []Node{n1, n2}, ScanGuard: guard(1793709225987084)}
	scanTick(t, m, "e1")
	cfg = core.last(t)
	var unblock []map[string]any
	if err := json.Unmarshal(cfg["scan_unblock"], &unblock); err != nil || len(unblock) != 1 || unblock[0]["identity"] != "flvxl-1" || unblock[0]["seq"].(float64) != 1793709225987084 {
		t.Fatalf("unblock %s", cfg["scan_unblock"])
	}
	if b := blockedTags(t, cfg); b["flvxl-1/flvxl-1"] {
		t.Fatal("re-enabled node still blocked by the agent")
	}

	// An older panel (no scan_guard) lifts every Core block and sends no guard.
	m.desired = Desired{Enabled: true, Nodes: []Node{n1, n2}}
	scanTick(t, m, "e1")
	cfg = core.last(t)
	if cfg["scan_guard"] != nil || cfg["scan_unblock"] == nil {
		t.Fatalf("rollback: guard %s unblock %s", cfg["scan_guard"], cfg["scan_unblock"])
	}
}

func TestScanGuardValidate(t *testing.T) {
	n := testNode(t, 1)
	ok := Desired{Revision: 1, Enabled: true, Nodes: []Node{n}, ScanGuard: &ScanGuard{Enabled: true, Identities: []string{"flvxl-1"}, Horizontal: 100, WebPorts: []int{80, 443}}}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for name, g := range map[string]ScanGuard{
		"unknown identity": {Enabled: true, Identities: []string{"flvxl-9"}},
		"duplicate":        {Enabled: true, Identities: []string{"flvxl-1", "flvxl-1"}},
		"threshold":        {Enabled: true, Identities: []string{"flvxl-1"}, Horizontal: 100001},
		"window":           {Enabled: true, Identities: []string{"flvxl-1"}, WindowS: 5},
		"web port":         {Enabled: true, Identities: []string{"flvxl-1"}, WebPorts: []int{80, 80}},
		"seen":             {Seen: -1},
	} {
		d := ok
		g := g
		d.ScanGuard = &g
		if err := d.validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A disabled guard with half-edited numbers never breaks the push.
	d := ok
	d.ScanGuard = &ScanGuard{Identities: []string{"flvxl-1"}, Horizontal: 999999}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
}
