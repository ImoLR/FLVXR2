package machineprotect

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeGuard struct {
	mu        sync.Mutex
	supported bool
	present   bool
	blocking  bool
	ports     PortSet
	dropped   int64
	ensureErr error
	ensures   int
	removes   int
}

func (g *fakeGuard) Ensure(ports PortSet, blocking bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ensures++
	if g.ensureErr != nil {
		return g.ensureErr
	}
	g.present, g.ports, g.blocking = true, ports, blocking
	return nil
}
func (g *fakeGuard) Remove() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.removes++
	g.present, g.blocking, g.ports = false, false, PortSet{}
	return nil
}
func (g *fakeGuard) Dropped() (int64, error) { g.mu.Lock(); defer g.mu.Unlock(); return g.dropped, nil }
func (g *fakeGuard) Blocking() bool          { g.mu.Lock(); defer g.mu.Unlock(); return g.present && g.blocking }
func (g *fakeGuard) Supported() bool         { return g.supported }

type lab struct {
	t        *testing.T
	root     string
	run      string
	state    string
	now      time.Time
	helper   bool
	ports    PortSet
	g        *fakeGuard
	p        *Protector
	cw       []int64
	logs     []string
	tcpTotal int
}

func newLab(t *testing.T) *lab {
	t.Helper()
	base := t.TempDir()
	l := &lab{t: t, root: filepath.Join(base, "root"), run: filepath.Join(base, "run"), state: filepath.Join(base, "etc"),
		now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), g: &fakeGuard{supported: true},
		ports: PortSet{TCP: []int{10001, 10002}, UDP: []int{10001}}}
	for _, d := range []string{l.run, l.state, filepath.Join(l.root, "proc/sys/net/ipv4")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	l.setTW(262144)
	l.setConns(100, 10)
	l.p = New(Options{ProcRoot: l.root, RunDir: l.run, StateDir: l.state,
		HelperActive: func(context.Context) bool { return l.helper },
		Ports:        func() PortSet { return l.ports },
		CloseWait:    func(v int64) { l.cw = append(l.cw, v) },
		Now:          func() time.Time { return l.now },
		Logf:         func(s string) { l.logs = append(l.logs, s) },
		guard:        l.g,
	})
	return l
}

func (l *lab) setConns(tcp, udp int) {
	var b, u strings.Builder
	b.WriteString(tcpHeader)
	for i := 0; i < tcp; i++ {
		st := "01"
		if i%2 == 1 {
			st = "06"
		}
		b.WriteString(tcpRow(i, st))
	}
	u.WriteString(udpHeader)
	for i := 0; i < udp; i++ {
		u.WriteString(udpRow(i))
	}
	writeProcNet(l.t, l.root, map[string]string{"tcp": b.String(), "udp": u.String()})
}

func (l *lab) setTW(v int64) {
	if err := os.WriteFile(filepath.Join(l.root, twSysctlRel), []byte(itoa(v)+"\n"), 0o644); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) tw() int64 {
	b, err := os.ReadFile(filepath.Join(l.root, twSysctlRel))
	if err != nil {
		l.t.Fatal(err)
	}
	var v int64
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &v); err != nil {
		l.t.Fatal(err)
	}
	return v
}

func itoa(v int64) string { b, _ := json.Marshal(v); return string(b) }

func (l *lab) tick() *Snapshot {
	l.p.tick(context.Background())
	l.now = l.now.Add(SampleInterval)
	return l.p.Snapshot()
}

func (l *lab) writeMmwx(doc map[string]any) {
	path := filepath.Join(l.run, MmwxStatusRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		l.t.Fatal(err)
	}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		l.t.Fatal(err)
	}
}

func TestNextBlockingHysteresis(t *testing.T) {
	steps := []struct {
		total int64
		want  bool
	}{{1000, false}, {1499, false}, {1500, true}, {1600, true}, {1430, true}, {1425, true}, {1424, false}, {1499, false}, {1500, true}}
	blocking := false
	for _, s := range steps {
		blocking = NextBlocking(blocking, s.total, 1500)
		if blocking != s.want {
			t.Fatalf("total %d: blocking=%t want %t", s.total, blocking, s.want)
		}
	}
	if ReleaseLevel(1500) != 1425 || NextBlocking(true, 5000, 0) {
		t.Fatal("release level / no cap")
	}
}

func TestSettingsValidate(t *testing.T) {
	ok := []Settings{{}, DefaultSettings(), {Enabled: true, MaxTotal: 100, TWBuckets: 100, CloseWaitSeconds: 1}, {Enabled: true, MaxTotal: 1_000_000, TWBuckets: 2_000_000, CloseWaitSeconds: 3600}}
	for _, s := range ok {
		if err := s.Validate(); err != nil {
			t.Fatalf("%+v: %v", s, err)
		}
	}
	bad := []Settings{{MaxTotal: 99}, {MaxTotal: -1}, {TWBuckets: 50}, {CloseWaitSeconds: 3601}, {CloseWaitSeconds: -5}}
	for _, s := range bad {
		if s.Validate() == nil {
			t.Fatalf("%+v accepted", s)
		}
	}
	d := DefaultSettings()
	if !d.Enabled || d.MaxTotal != 1500 || d.TWBuckets != 500 || d.CloseWaitSeconds != 10 {
		t.Fatalf("defaults %+v", d)
	}
}

func TestUnconfiguredDoesNothingButReports(t *testing.T) {
	l := newLab(t)
	s := l.tick()
	if s == nil || s.Conns == nil || s.Conns.Total != 110 || s.Protect.Owner != OwnerFLVX || s.Protect.Enabled {
		t.Fatalf("snapshot %+v", s)
	}
	if l.g.present || l.tw() != 262144 || len(l.cw) != 1 || l.cw[0] != 0 {
		t.Fatalf("unconfigured agent touched the kernel: guard=%+v tw=%d cw=%v", l.g, l.tw(), l.cw)
	}
	// The business-ports file is written on every machine.
	b, err := os.ReadFile(filepath.Join(l.run, BusinessPortsRelPath))
	if err != nil {
		t.Fatal(err)
	}
	var doc businessPortsFile
	if err := json.Unmarshal(b, &doc); err != nil || doc.Version != 1 || len(doc.TCP) != 2 || len(doc.UDP) != 1 || doc.UpdatedAt == "" {
		t.Fatalf("ports file %s", b)
	}
}

func TestCapBlocksAndReleasesWithHysteresis(t *testing.T) {
	l := newLab(t)
	l.p.Configure(Settings{Enabled: true, MaxTotal: 200, TWBuckets: 500, CloseWaitSeconds: 10})
	s := l.tick()
	if !l.g.present || l.g.blocking || s.Protect.Blocking || !s.Protect.Enabled || *s.Protect.MaxTotal != 200 || s.Protect.Ports != 3 {
		t.Fatalf("idle state guard=%+v snap=%+v", l.g, s.Protect)
	}
	if l.tw() != 500 || l.cw[len(l.cw)-1] != 10 {
		t.Fatalf("tw=%d cw=%v", l.tw(), l.cw)
	}
	l.setConns(190, 10) // 200 >= cap
	l.g.dropped = 7
	if s = l.tick(); !s.Protect.Blocking || !l.g.blocking || s.Protect.DroppedTotal != 7 {
		t.Fatalf("not blocking at cap: %+v", s.Protect)
	}
	l.setConns(180, 10) // 190 >= 95 % of 200: still blocking
	if s = l.tick(); !s.Protect.Blocking {
		t.Fatal("released above the release level")
	}
	l.setConns(179, 10) // 189 < 190: release
	if s = l.tick(); s.Protect.Blocking || l.g.blocking {
		t.Fatal("not released below the release level")
	}
	joined := strings.Join(l.logs, "\n")
	if !strings.Contains(joined, "开始丢弃") || !strings.Contains(joined, "解除新连接保护") {
		t.Fatalf("transitions not logged: %s", joined)
	}
}

func TestNoBusinessPortsMeansNoTable(t *testing.T) {
	l := newLab(t)
	l.ports = PortSet{}
	l.p.Configure(DefaultSettings())
	l.setConns(2000, 0)
	if s := l.tick(); s.Protect.Blocking || l.g.present {
		t.Fatalf("table without business ports: %+v", l.g)
	}
}

func TestDisableRestoresKernelState(t *testing.T) {
	l := newLab(t)
	l.p.Configure(DefaultSettings())
	l.tick()
	if l.tw() != 500 || !l.g.present {
		t.Fatal("not applied")
	}
	if _, err := os.Stat(filepath.Join(l.state, StateFileName)); err != nil {
		t.Fatal("original not saved before the change")
	}
	l.p.Configure(Settings{Enabled: false, MaxTotal: 1500, TWBuckets: 500, CloseWaitSeconds: 10})
	s := l.tick()
	if l.tw() != 262144 || l.g.present || s.Protect.Enabled || l.cw[len(l.cw)-1] != 0 {
		t.Fatalf("disable left state: tw=%d guard=%+v cw=%v", l.tw(), l.g, l.cw)
	}
	if _, err := os.Stat(filepath.Join(l.state, StateFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state file kept after restore")
	}
}

func TestSysctlRestoreKeepsForeignValue(t *testing.T) {
	l := newLab(t)
	l.p.Configure(DefaultSettings())
	l.tick()
	l.setTW(4096) // an admin changed it after us
	l.p.Configure(Settings{})
	l.tick()
	if l.tw() != 4096 {
		t.Fatalf("restore clobbered a foreign value: %d", l.tw())
	}
}

func TestSysctlSurvivesRestart(t *testing.T) {
	l := newLab(t)
	l.p.Configure(DefaultSettings())
	l.tick()
	// A new agent process (same state dir) is told to disable.
	l2 := *l
	l2.p = New(Options{ProcRoot: l.root, RunDir: l.run, StateDir: l.state, HelperActive: func(context.Context) bool { return false },
		Ports: func() PortSet { return l.ports }, Now: func() time.Time { return l.now }, Logf: func(string) {}, guard: &fakeGuard{supported: true}})
	l2.p.Configure(Settings{})
	l2.p.tick(context.Background())
	if l.tw() != 262144 {
		t.Fatalf("original not restored by the next process: %d", l.tw())
	}
}

func TestMmwxOwnedHandsOver(t *testing.T) {
	l := newLab(t)
	l.p.Configure(DefaultSettings())
	l.setConns(1600, 0)
	if s := l.tick(); !s.Protect.Blocking || l.tw() != 500 {
		t.Fatal("setup")
	}
	// A new Helper starts and writes its status file.
	l.writeMmwx(map[string]any{"version": 1, "owner": "mmwx", "updated_at": l.now.Format(time.RFC3339), "enabled": true,
		"max_total": 1500, "tw_buckets": 500, "close_wait_seconds": 10, "total": 1600, "tcp": 1600, "udp": 0, "blocking": true, "dropped_total": 42})
	s := l.tick()
	if l.g.present || l.tw() != 262144 {
		t.Fatalf("FLVX kept its table/sysctl on an mmwx machine: guard=%+v tw=%d", l.g, l.tw())
	}
	p := s.Protect
	if p.Owner != OwnerMmwx || p.MmwxStatus != MmwxStatusOK || !p.Enabled || *p.MaxTotal != 1500 || *p.TWBuckets != 500 || *p.CloseWaitSeconds != 10 || !p.Blocking || p.DroppedTotal != 42 {
		t.Fatalf("mirror %+v", p)
	}
	if s.Conns == nil || s.Conns.Total != 1600 || l.cw[len(l.cw)-1] != 10 {
		t.Fatalf("count / close-wait on mmwx machine: %+v %v", s.Conns, l.cw)
	}
	if _, err := os.Stat(filepath.Join(l.run, BusinessPortsRelPath)); err != nil {
		t.Fatal("ports file not written on an mmwx machine")
	}
	// The status file goes stale while the Helper still runs: still mmwx-owned.
	l.helper = true
	l.now = l.now.Add(2 * time.Minute)
	s = l.tick()
	if s.Protect.Owner != OwnerMmwx || s.Protect.MmwxStatus != MmwxStatusStale || l.g.present || l.cw[len(l.cw)-1] != 0 {
		t.Fatalf("stale %+v cw=%v", s.Protect, l.cw)
	}
}

func TestOldHelperIsOwnerWithoutValues(t *testing.T) {
	l := newLab(t)
	l.helper = true
	l.p.Configure(DefaultSettings())
	s := l.tick()
	if s.Protect.Owner != OwnerMmwx || s.Protect.MmwxStatus != MmwxStatusMissing || s.Protect.Enabled || l.g.present || l.tw() != 262144 || l.g.ensures != 0 {
		t.Fatalf("old helper: %+v guard=%+v", s.Protect, l.g)
	}
	if l.cw[len(l.cw)-1] != 0 {
		t.Fatal("close-wait applied on an mmwx machine without mmwx values")
	}
}

func TestOwnershipIsSticky(t *testing.T) {
	l := newLab(t)
	l.helper = true
	l.p.Configure(DefaultSettings())
	l.tick()
	l.helper = false // restart loop / short stop
	l.now = l.now.Add(helperCheckEvery)
	if s := l.tick(); s.Protect.Owner != OwnerMmwx || l.g.present {
		t.Fatal("ownership flapped on a short Helper stop")
	}
	l.now = l.now.Add(mmwxOwnerHold)
	if s := l.tick(); s.Protect.Owner != OwnerFLVX || !l.g.present {
		t.Fatalf("FLVX did not take over after the hold: %+v", s.Protect)
	}
}

func TestMalformedOrStaleStatusIgnored(t *testing.T) {
	l := newLab(t)
	l.p.Configure(DefaultSettings())
	for _, doc := range []map[string]any{
		{"version": 2, "owner": "mmwx", "updated_at": l.now.Format(time.RFC3339)},
		{"version": 1, "owner": "other", "updated_at": l.now.Format(time.RFC3339)},
		{"version": 1, "owner": "mmwx", "updated_at": l.now.Add(-61 * time.Second).Format(time.RFC3339)},
		{"version": 1, "owner": "mmwx", "updated_at": "yesterday"},
	} {
		l.writeMmwx(doc)
		if s := l.tick(); s.Protect.Owner != OwnerFLVX {
			t.Fatalf("%v made the machine mmwx-owned", doc)
		}
	}
}

func TestHelperCheckIsCached(t *testing.T) {
	l := newLab(t)
	calls := 0
	l.p.owner.helperActive = func(context.Context) bool { calls++; return false }
	for i := 0; i < 12; i++ { // 12 × 5 s = 60 s
		l.tick()
	}
	if calls != 2 {
		t.Fatalf("systemctl ran %d times in 60 s, want 2", calls)
	}
}

func TestGuardErrorReported(t *testing.T) {
	l := newLab(t)
	l.g.ensureErr = errors.New("netlink: operation not permitted")
	l.p.Configure(DefaultSettings())
	s := l.tick()
	if !strings.Contains(s.Protect.Error, "防护规则更新失败") || s.Protect.Blocking {
		t.Fatalf("error not reported: %+v", s.Protect)
	}
	l.g.supported = false
	l2 := newLab(t)
	l2.g.supported = false
	l2.p = New(Options{ProcRoot: l2.root, RunDir: l2.run, StateDir: l2.state, HelperActive: func(context.Context) bool { return false },
		Ports: func() PortSet { return l2.ports }, Now: func() time.Time { return l2.now }, Logf: func(string) {}, guard: l2.g})
	l2.p.Configure(DefaultSettings())
	if s := l2.tick(); !strings.Contains(s.Protect.Error, "nftables 不可用") || l2.tw() != 500 {
		t.Fatalf("unsupported nft: %+v tw=%d", s.Protect, l2.tw())
	}
}

func TestCloseWaitHookOnlyOnChange(t *testing.T) {
	l := newLab(t)
	l.p.Configure(DefaultSettings())
	for i := 0; i < 5; i++ {
		l.tick()
	}
	if len(l.cw) != 1 || l.cw[0] != 10 {
		t.Fatalf("hook calls %v", l.cw)
	}
	l.p.Configure(Settings{Enabled: true, MaxTotal: 1500, CloseWaitSeconds: 0})
	l.tick()
	if len(l.cw) != 2 || l.cw[1] != 0 || l.tw() != 262144 {
		t.Fatalf("hook calls %v tw=%d", l.cw, l.tw())
	}
}

func TestPortsGraceAndFileRefresh(t *testing.T) {
	l := newLab(t)
	path := filepath.Join(l.run, BusinessPortsRelPath)
	l.tick()
	st1, _ := os.Stat(path)
	l.ports = PortSet{} // reconnect window: the panel is re-sending rules
	l.tick()
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "10002") {
		t.Fatalf("port dropped inside the grace window: %s", b)
	}
	l.now = l.now.Add(portRemovalGrace)
	l.tick()
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "10002") {
		t.Fatalf("port kept after the grace window: %s", b)
	}
	info, _ := os.Stat(path)
	dir, _ := os.Stat(filepath.Dir(path))
	if info.Mode().Perm() != 0o644 || dir.Mode().Perm() != 0o755 {
		t.Fatalf("modes file %v dir %v", info.Mode(), dir.Mode())
	}
	_ = st1
	// Unchanged set: no rewrite until the refresh interval.
	before, _ := os.ReadFile(path)
	l.tick()
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("unchanged set rewritten")
	}
	l.now = l.now.Add(portsFileRefresh)
	l.tick()
	refreshed, _ := os.ReadFile(path)
	if string(refreshed) == string(before) {
		t.Fatal("not refreshed after 4 min")
	}
	// A deleted file comes back on the next sample.
	_ = os.Remove(path)
	l.tick()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("vanished file not rewritten")
	}
}

func TestRunOnceAndShutdownRemovesDropChain(t *testing.T) {
	l := newLab(t)
	l.p.opts.Interval = 10 * time.Millisecond
	l.p.Configure(DefaultSettings())
	l.setConns(1600, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.p.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for !l.g.Blocking() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !l.g.Blocking() {
		t.Fatal("loop never blocked")
	}
	// A second Run (e.g. from a reconnect) returns at once.
	second := make(chan struct{})
	go func() { l.p.Run(ctx); close(second) }()
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("second Run started another loop")
	}
	if n := l.p.runs.Load(); n != 1 {
		t.Fatalf("%d loops running", n)
	}
	cancel()
	<-done
	if l.g.Blocking() {
		t.Fatal("drop chain left behind a stopped agent")
	}
}
