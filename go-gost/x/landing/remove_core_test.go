package landing

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const fakeCoreStarted = "2026-11-13T00:00:00Z"

var fakeCoreEpoch = func() string {
	t, _ := time.Parse(time.RFC3339, fakeCoreStarted)
	return strconv.FormatInt(t.UnixNano(), 10)
}()

// fakeCoreControl answers the control socket like a running Core until
// stopped (the stop of the unit / the test process).
type fakeCoreControl struct {
	server  *http.Server
	stopped atomic.Bool
}

func startFakeCoreControl(t *testing.T, m *Manager) *fakeCoreControl {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(m.socket()), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", m.socket())
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCoreControl{}
	f.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.stopped.Load() {
			http.Error(w, "stopped", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"version":7,"started_at":"` + fakeCoreStarted + `","inbounds":[],"proxy_users":[]}`))
	})}
	go f.server.Serve(listener)
	t.Cleanup(func() { f.server.Close() })
	return f
}

type systemctlLog struct {
	mu    sync.Mutex
	calls []string
	on    func(args []string) error
}

func (l *systemctlLog) run(_ context.Context, args ...string) error {
	l.mu.Lock()
	l.calls = append(l.calls, strings.Join(args, " "))
	on := l.on
	l.mu.Unlock()
	if on != nil {
		return on(args)
	}
	return nil
}

func (l *systemctlLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// chainOnlyHost lays out a systemd-mode landing installation of a chain-only
// exit whose last chain copy was removed (desired revision 7, no nodes).
func chainOnlyHost(t *testing.T) (*Manager, string, string, *systemctlLog) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "etc-flvxx", "landing")
	unitDir := filepath.Join(root, "etc-systemd-system")
	controlDir := filepath.Join(root, "etc-systemd-system.control")
	for _, d := range []string{dir, unitDir, filepath.Join(controlDir, unitName+".d"), filepath.Join(dir, "run")} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	m := NewManager(dir, false)
	m.unitDir, m.controlDir = unitDir, controlDir
	log := &systemctlLog{}
	m.systemctl = log.run
	m.desired = Desired{Revision: 7, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 1000}
	unit, err := m.unit()
	if err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(m.unitPath(), unit)
	write(filepath.Join(controlDir, unitName+".d", "50-MemoryMax.conf"), "[Service]\nMemoryMax=268435456\n")
	// Other units in the same directories are never touched.
	write(filepath.Join(unitDir, "flvx_agent.service"), "[Service]\nExecStart=/etc/flvxx/flvx_agent\n")
	write(filepath.Join(unitDir, "flux_agent.service"), "[Service]\nExecStart=/etc/flux_agent/flux_agent\n")
	if err := os.MkdirAll(filepath.Join(controlDir, "flux_agent.service.d"), 0755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(controlDir, "flux_agent.service.d", "50-CPUQuota.conf"), "[Service]\nCPUQuota=50%\n")
	b, _ := json.Marshal(m.desired)
	write(filepath.Join(dir, "desired.json"), string(b))
	write(filepath.Join(dir, "applied.json"), `{"epoch":"1","inbounds":{}}`)
	for _, name := range []string{"flvx-landing-core", "config.json", "api.json", "scan.json", "geoip.dat", "geosite.dat", "flvx-landing-core.download", ".config.json.tmp-123", "run/control.sock.old"} {
		write(filepath.Join(dir, name), "x")
	}
	// Not the Core's: the entry 速率 tree (plan 168) and an unknown file.
	write(filepath.Join(dir, entryRatesFile), `{"version":1}`)
	write(filepath.Join(dir, "operator-notes.txt"), "keep me")
	m.loaded = true
	return m, unitDir, controlDir, log
}

func TestRemoveCoreSystemdMode(t *testing.T) {
	m, unitDir, controlDir, log := chainOnlyHost(t)
	core := startFakeCoreControl(t, m)
	log.on = func(args []string) error {
		if args[0] == "disable" {
			core.stopped.Store(true) // the unit stops the Core
		}
		return nil
	}
	m.limitsApplied = [2]int{256, 1000}
	res, err := m.RemoveCore(context.Background(), RemoveCoreRequest{Revision: 7})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"disable --now " + unitName, "daemon-reload", "reset-failed " + unitName}
	if got := log.list(); !reflect.DeepEqual(got, want) {
		t.Fatalf("systemctl calls = %q, want %q", got, want)
	}
	if !res.Removed || !res.WasRunning || !res.UnitRemoved || res.DirRemoved {
		t.Fatalf("result = %+v", res)
	}
	if !reflect.DeepEqual(res.Kept, []string{entryRatesFile, "operator-notes.txt"}) {
		t.Fatalf("kept = %v", res.Kept)
	}
	for _, gone := range []string{m.unitPath(), filepath.Join(controlDir, unitName+".d"), m.binary(), filepath.Join(m.dir, "config.json"), filepath.Join(m.dir, "desired.json"), filepath.Join(m.dir, "run"), filepath.Join(m.dir, "geoip.dat"), filepath.Join(m.dir, ".config.json.tmp-123"), filepath.Join(m.dir, "flvx-landing-core.download")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s survived: %v", gone, err)
		}
	}
	for _, kept := range []string{filepath.Join(m.dir, entryRatesFile), filepath.Join(m.dir, "operator-notes.txt"), filepath.Join(unitDir, "flvx_agent.service"), filepath.Join(unitDir, "flux_agent.service"), filepath.Join(controlDir, "flux_agent.service.d", "50-CPUQuota.conf")} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s removed: %v", kept, err)
		}
	}
	if m.loaded || m.desired.Revision != 0 || m.apiPort != 0 || m.limitsApplied != [2]int{} || m.applied.Inbounds != nil {
		t.Fatalf("state not reset: loaded=%v desired=%+v api=%d limits=%v", m.loaded, m.desired, m.apiPort, m.limitsApplied)
	}
	if st := m.Status(); st.State != "disabled" || st.Revision != 0 || len(st.Nodes) != 0 {
		t.Fatalf("status after removal = %+v", st)
	}
	// The panel lost the answer and asks again: a no-op success.
	log.calls = nil
	again, err := m.RemoveCore(context.Background(), RemoveCoreRequest{Revision: 7})
	if err != nil || !again.Removed || again.UnitRemoved || again.WasRunning || len(again.Files) != 0 {
		t.Fatalf("repeat = %+v %v", again, err)
	}
	if got := log.list(); !reflect.DeepEqual(got, []string{"daemon-reload", "reset-failed " + unitName}) {
		t.Fatalf("repeat systemctl calls = %q", got)
	}
	// The next desired state of a recreated chain-only server (new row,
	// revision 1) is accepted again.
	if m.desired.Revision != 0 {
		t.Fatal("revision kept")
	}
}

func TestRemoveCoreRemovesEmptyDirectory(t *testing.T) {
	m, _, _, _ := chainOnlyHost(t)
	for _, name := range []string{entryRatesFile, "operator-notes.txt"} {
		_ = os.Remove(filepath.Join(m.dir, name))
	}
	res, err := m.RemoveCore(context.Background(), RemoveCoreRequest{Revision: 9})
	if err != nil || !res.DirRemoved || len(res.Kept) != 0 {
		t.Fatalf("result = %+v %v", res, err)
	}
	if _, err := os.Stat(m.dir); !os.IsNotExist(err) {
		t.Fatalf("landing dir kept: %v", err)
	}
}

func TestRemoveCoreRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(m *Manager)
		rev   int64
		want  string
	}{
		{"nodes left", func(m *Manager) { m.desired.Nodes = []Node{chainNode(1, goldenChainUpstreams()[0])} }, 7, RemoveCoreHasNodes},
		{"newer desired", nil, 6, RemoveCoreStale},
		{"inbound still applied", func(m *Manager) {
			startFakeCoreControl(t, m)
			m.applied = appliedState{Epoch: fakeCoreEpoch, Inbounds: map[string]string{"flvxl-1099511627777": "{}"}}
		}, 7, RemoveCoreApplied},
		{"foreign unit", func(m *Manager) {
			_ = os.WriteFile(m.unitPath(), []byte("[Service]\nExecStart=\"/opt/other/flvx-landing-core\" run\n"), 0644)
		}, 7, "another installation"},
		{"bad revision", nil, 0, "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _, log := chainOnlyHost(t)
			if tc.setup != nil {
				tc.setup(m)
			}
			_, err := m.RemoveCore(context.Background(), RemoveCoreRequest{Revision: tc.rev})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if calls := log.list(); len(calls) != 0 {
				t.Fatalf("refusal ran systemctl: %q", calls)
			}
			if _, err := os.Stat(m.binary()); err != nil {
				t.Fatalf("refusal removed the Core binary: %v", err)
			}
			if !m.loaded {
				t.Fatal("refusal reset the state")
			}
		})
	}
}

// Applied entries of a Core that is no longer running (it died, or the
// entries are from an earlier epoch) do not block the removal.
func TestRemoveCoreStaleAppliedDoesNotBlock(t *testing.T) {
	m, _, _, _ := chainOnlyHost(t)
	m.applied = appliedState{Epoch: "123", Inbounds: map[string]string{"flvxl-1099511627777": "{}"}}
	if res, err := m.RemoveCore(context.Background(), RemoveCoreRequest{Revision: 7}); err != nil || !res.Removed || res.WasRunning {
		t.Fatalf("dead Core: %+v %v", res, err)
	}
	m2, _, _, log := chainOnlyHost(t)
	core := startFakeCoreControl(t, m2)
	log.on = func(args []string) error {
		if args[0] == "disable" {
			core.stopped.Store(true)
		}
		return nil
	}
	m2.applied = appliedState{Epoch: "123", Inbounds: map[string]string{"flvxl-1099511627777": "{}"}}
	if res, err := m2.RemoveCore(context.Background(), RemoveCoreRequest{Revision: 7}); err != nil || !res.Removed || !res.WasRunning {
		t.Fatalf("earlier epoch: %+v %v", res, err)
	}
}

// A Core that still answers after the stop keeps every file.
func TestRemoveCoreStopNotConfirmed(t *testing.T) {
	m, _, _, log := chainOnlyHost(t)
	startFakeCoreControl(t, m) // never stops
	_, err := m.RemoveCore(context.Background(), RemoveCoreRequest{Revision: 7})
	if err == nil || !strings.Contains(err.Error(), "still answers") {
		t.Fatalf("err = %v", err)
	}
	if calls := log.list(); len(calls) != 1 || !strings.HasPrefix(calls[0], "disable --now") {
		t.Fatalf("calls = %q", calls)
	}
	if _, err := os.Stat(m.binary()); err != nil {
		t.Fatal("binary removed although the Core still runs")
	}
	if _, err := os.Stat(m.unitPath()); err != nil {
		t.Fatal("unit removed although the Core still runs")
	}
}

func TestRemoveCoreFailedStopKeepsEverything(t *testing.T) {
	m, _, _, log := chainOnlyHost(t)
	log.on = func(args []string) error {
		if args[0] == "disable" {
			return errors.New("exit status 1")
		}
		return nil
	}
	if _, err := m.RemoveCore(context.Background(), RemoveCoreRequest{Revision: 7}); err == nil || !strings.Contains(err.Error(), "stop landing Core") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(m.unitPath()); err != nil {
		t.Fatal("unit removed after a failed stop")
	}
}

// A reconcile holding the lock (e.g. a first Core download) makes the
// command give up instead of running late.
func TestRemoveCoreBusyGivesUp(t *testing.T) {
	m, _, _, log := chainOnlyHost(t)
	old := removeCoreLockWait
	removeCoreLockWait = 200 * time.Millisecond
	defer func() { removeCoreLockWait = old }()
	m.mu.Lock()
	start := time.Now()
	_, err := m.RemoveCore(context.Background(), RemoveCoreRequest{Revision: 7})
	m.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "busy") || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	if len(log.list()) != 0 {
		t.Fatal("busy command still acted")
	}
}

// Test (direct) mode: the agent's own Core process is stopped; a Core it did
// not start is left alone.
func TestRemoveCoreDirectMode(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, true)
	m.desired = Desired{Revision: 3, Enabled: true}
	m.loaded = true
	if err := os.WriteFile(m.binary(), []byte("x"), 0700); err != nil {
		t.Fatal(err)
	}
	core := startFakeCoreControl(t, m)
	if _, err := m.RemoveCore(context.Background(), RemoveCoreRequest{Revision: 3}); err == nil || !strings.Contains(err.Error(), "not managed") {
		t.Fatalf("foreign test Core: %v", err)
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); core.stopped.Store(true); close(done) }()
	m.proc = cmd.Process
	res, err := m.RemoveCore(context.Background(), RemoveCoreRequest{Revision: 3})
	if err != nil || !res.Removed || !res.WasRunning || res.UnitRemoved {
		t.Fatalf("result = %+v %v", res, err)
	}
	<-done
	if _, err := os.Stat(m.binary()); !os.IsNotExist(err) {
		t.Fatal("binary kept")
	}
}

func TestCoreOwnName(t *testing.T) {
	own := map[string]bool{}
	for _, n := range coreOwnFiles {
		own[n] = true
	}
	for name, want := range map[string]bool{
		"flvx-landing-core": true, "run": true, "geosite.dat.download": true, ".applied.json.tmp-99": true,
		entryRatesFile: false, "." + entryRatesFile + ".tmp-1": false, "notes.txt": false, ".tmp-": false, "x.download": false,
	} {
		if got := coreOwnName(name, own); got != want {
			t.Errorf("coreOwnName(%q) = %v, want %v", name, got, want)
		}
	}
}
