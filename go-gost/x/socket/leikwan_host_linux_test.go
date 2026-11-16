//go:build linux

package socket

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixtures: secret-free copies of the four 利群入口 hosts (11-17 05:5x TW):
// unit files, warm / ssfilter / coalesce / triggers / phone files as they
// are, sskeys.json with fake keys of the same shape, reduced forward configs
// and status files, cgroup markers for the active units.
var lkFixtureHosts = []string{"baidu", "volc-gz", "aliyun-gz", "volc-sh"}

func lkFixture(t *testing.T, host string) *lkHostManager {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join("testdata", "leikwan-host", host, "root")
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, "sskeys.json") {
			mode = 0o600
		}
		return os.WriteFile(target, data, mode)
	})
	if err != nil {
		t.Fatal(err)
	}
	m := newLKHostManager(filepath.Join(dst, "etc", "flvx_agent"))
	_ = os.MkdirAll(filepath.Join(dst, "etc", "flvx_agent"), 0o755)
	m.root = dst
	m.now = func() time.Time { return time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC) } // 14:00 TW, outside the window
	m.run = func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	return m
}

// lkDesiredFromHost: what the panel sends when it wants exactly what the host has.
func lkCopyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func lkDesiredFromHost(f LKFiles) *LKDesired {
	d := &LKDesired{}
	if f.Warm != nil {
		d.Warm = &LKWantTokens{Tokens: lkCopyMap(f.Warm.Tokens)}
	}
	if f.SSFilter != nil {
		d.SSFilter = &LKWantTokens{Tokens: lkCopyMap(f.SSFilter.Tokens)}
	}
	if f.SSKeys != nil {
		d.SSKeys = &LKWantKeys{}
	}
	if f.Triggers != nil {
		d.Triggers = &LKWantTokens{Tokens: lkCopyMap(f.Triggers.Tokens)}
	}
	if f.Logswitch != nil {
		d.Logswitch = &LKWantLogswitch{Lines: append([]string{}, f.Logswitch.Lines...), RecordOnly: append([]string{}, f.Logswitch.RecordOnly...)}
	}
	if f.Coalesce != nil {
		d.Coalesce = &LKWantValue{Value: f.Coalesce.Value}
	}
	if f.Phone != nil {
		d.Phone = &LKWantPhone{Rules: lkCopyMap(f.Phone.Rules), Comments: lkCopyMap(f.Phone.Comments)}
	}
	return d
}

func TestLeiKwanHostReadFixtures(t *testing.T) {
	type want struct {
		warm, ssfilter, keys, lines, record, phone int
		coalesce, front, logswitch, agent, core    string
		triggers                                   bool
	}
	cases := map[string]want{
		"baidu":     {warm: 21, ssfilter: 7, keys: 7, lines: 7, phone: 6, coalesce: "off", front: "test", logswitch: "v11", agent: "2026.11.15.1", core: "2026.11.13.1", triggers: true},
		"volc-gz":   {warm: 10, ssfilter: 6, keys: 6, lines: 6, phone: 1, coalesce: "on budget_ms=2 min=59", front: "test", logswitch: "v11", agent: "2026.10.12.1", core: "2026.10.12.1", triggers: true},
		"aliyun-gz": {warm: 10, ssfilter: 6, keys: 6, lines: 6, phone: 0, coalesce: "on budget_ms=2 min=59", front: "test", logswitch: "v11", agent: "2026.11.15.1", core: "2026.11.13.1", triggers: true},
		"volc-sh":   {warm: -1, ssfilter: -1, keys: -1, lines: 6, record: 9, phone: 0, coalesce: "on budget_ms=2 min=59", front: "stable", logswitch: "v9.1", agent: "2026.10.12.1", core: "2026.10.12.1"},
	}
	for _, host := range lkFixtureHosts {
		t.Run(host, func(t *testing.T) {
			m := lkFixture(t, host)
			files, st := m.read()
			w := cases[host]
			count := func(v *LKTokens) int {
				if v == nil {
					return -1
				}
				return len(v.Tokens)
			}
			if got := count(files.Warm); got != w.warm {
				t.Errorf("warm tokens = %d, want %d (%v)", got, w.warm, files.Warm)
			}
			if got := count(files.SSFilter); got != w.ssfilter {
				t.Errorf("ssfilter = %d, want %d", got, w.ssfilter)
			}
			keys := -1
			if files.SSKeys != nil {
				keys = len(files.SSKeys.FP)
				if files.SSKeys.Error != "" {
					t.Errorf("sskeys error %q", files.SSKeys.Error)
				}
			}
			if keys != w.keys {
				t.Errorf("keys = %d, want %d", keys, w.keys)
			}
			if files.Logswitch == nil || len(files.Logswitch.Lines) != w.lines || len(files.Logswitch.RecordOnly) != w.record {
				t.Fatalf("logswitch = %+v", files.Logswitch)
			}
			if (files.Triggers != nil) != w.triggers {
				t.Errorf("triggers present = %v", files.Triggers != nil)
			}
			phones := 0
			if files.Phone != nil {
				phones = len(files.Phone.Rules)
			}
			if phones != w.phone {
				t.Errorf("phone = %d, want %d", phones, w.phone)
			}
			if files.Coalesce == nil || files.Coalesce.Value != w.coalesce {
				t.Errorf("coalesce = %+v", files.Coalesce)
			}
			if st.FrontChannel != w.front || st.Logswitch != w.logswitch || st.AgentVersion != w.agent || st.CoreVersion != w.core {
				t.Errorf("state = front %q logswitch %q agent %q core %q", st.FrontChannel, st.Logswitch, st.AgentVersion, st.CoreVersion)
			}
			if !st.UpdateLock || !st.AgentRunning || st.NativeUDP != "all" || len(st.Forwards) != 8 {
				t.Errorf("state lock=%v agent=%v udp=%q forwards=%d", st.UpdateLock, st.AgentRunning, st.NativeUDP, len(st.Forwards))
			}
			for _, f := range st.Forwards {
				if f.State != "ready" || !f.Running || f.Target == "" {
					t.Errorf("forward %+v", f)
				}
			}
			raw, _ := json.Marshal(LeiKwanHostReport{State: st, Files: files})
			if bytes.Contains(raw, []byte("psk")) || bytes.Contains(raw, []byte("AAAA")) {
				t.Errorf("report leaks key material: %s", raw)
			}
		})
	}
	// Spot checks of today's per-host behaviour.
	m := lkFixture(t, "baidu")
	files, _ := m.read()
	if files.Warm.Tokens["540"] != "10" || files.Warm.Tokens["backup:540"] != "4" || files.Warm.Tokens["burst"] == "" {
		t.Errorf("baidu warm %v", files.Warm.Tokens)
	}
	if files.Triggers.Tokens["540:return_check"] != "on" || files.Triggers.Tokens["279:return_check"] != "" {
		t.Errorf("baidu triggers %v", files.Triggers.Tokens)
	}
	if files.Phone.Rules["19263"] != "163.53.52.189:35046" {
		t.Errorf("baidu phone %v", files.Phone.Rules)
	}
	m = lkFixture(t, "volc-gz")
	files, _ = m.read()
	if _, ok := files.Warm.Tokens["540"]; ok || files.SSFilter.Tokens["540"] != "" || files.Phone.Rules["19047"] != "163.53.52.189:35046" {
		t.Errorf("volc-gz 540 must stay cold, phone 19047: %v %v %v", files.Warm.Tokens, files.SSFilter.Tokens, files.Phone.Rules)
	}
	for _, l := range files.Logswitch.Lines {
		if strings.HasPrefix(l, "540:") {
			t.Errorf("volc-gz logswitch has 540: %v", files.Logswitch.Lines)
		}
	}
}

// Rendering what the host has, then reading it back, must give the same
// meaning for every file of every host (semantic diff 0).
func TestLeiKwanHostRenderRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC)
	for _, host := range lkFixtureHosts {
		t.Run(host, func(t *testing.T) {
			m := lkFixture(t, host)
			files, _ := m.read()
			d := lkDesiredFromHost(files)
			for _, f := range lkFileOrder {
				if lkWanted(f, d) && !lkSame(f, &files, d) {
					t.Fatalf("%s: host vs its own desired not same", f)
				}
			}
			// Write every file through the renderer (the logswitch as a drop-in
			// with the same values) and read again.
			backup := m.newBackupDir(now)
			for _, f := range lkFileOrder {
				if !lkWanted(f, d) {
					continue
				}
				if err := m.applyFile(context.Background(), f, files, d, 7, backup); err != nil && !strings.Contains(err.Error(), "file does not exist") {
					t.Fatalf("apply %s: %v", f, err)
				}
			}
			again, _ := m.read()
			for _, f := range lkFileOrder {
				if lkWanted(f, d) && !lkSame(f, &again, d) {
					t.Errorf("%s: rendered file reads back different", f)
				}
			}
			if again.Logswitch != nil && !m.exists("etc/systemd/system/leikwan-logswitch.service.d/60-flvx-panel.conf") {
				t.Errorf("logswitch drop-in missing")
			}
			if again.SSKeys != nil {
				info, _ := os.Stat(m.p(again.SSKeys.Path))
				if info.Mode().Perm() != 0o600 || again.SSKeys.Error != "" {
					t.Errorf("sskeys mode %v err %q", info.Mode().Perm(), again.SSKeys.Error)
				}
				if !lkMapsEqual(again.SSKeys.FP, files.SSKeys.FP) {
					t.Errorf("sskeys changed: %v vs %v", again.SSKeys.FP, files.SSKeys.FP)
				}
			}
			if _, err := os.Stat(filepath.Join(backup)); err != nil {
				t.Errorf("no backup dir: %v", err)
			}
		})
	}
}

func readAllFiles(t *testing.T, root string) map[string]string {
	out := map[string]string{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			raw, _ := os.ReadFile(path)
			rel, _ := filepath.Rel(root, path)
			out[rel] = string(raw) + "|" + info.ModTime().String()
		}
		return nil
	})
	return out
}

func TestLeiKwanHostManageNoWriteWhenSame(t *testing.T) {
	for _, host := range lkFixtureHosts {
		m := lkFixture(t, host)
		files, _ := m.read()
		before := readAllFiles(t, filepath.Join(m.root, "etc"))
		beforeOpt := readAllFiles(t, filepath.Join(m.root, "opt"))
		m.load()
		want := &LeiKwanHostSync{V: 1, Manage: true, Rev: 3, Desired: lkDesiredFromHost(files)}
		if m.manage(context.Background(), files, want) {
			t.Fatalf("%s: wrote although the host already matches", host)
		}
		after := readAllFiles(t, filepath.Join(m.root, "etc"))
		for k, v := range before {
			if after[k] != v && !strings.Contains(k, "flvx_agent") {
				t.Errorf("%s: %s changed", host, k)
			}
		}
		for k, v := range readAllFiles(t, filepath.Join(m.root, "opt")) {
			if beforeOpt[k] != v {
				t.Errorf("%s: %s changed", host, k)
			}
		}
		if len(m.status.Drift) != 0 || len(m.status.Pending) != 0 || m.status.Applied[lkFileWarm] != 3 && files.Warm != nil {
			t.Errorf("%s: status %+v", host, m.status)
		}
		if _, err := os.Stat(filepath.Join(m.root, "var/lib/flvx-leikwan/backup")); err == nil {
			t.Errorf("%s: backup made without a write", host)
		}
	}
}

func TestLeiKwanHostManageWriteDriftForceWindow(t *testing.T) {
	m := lkFixture(t, "baidu")
	files, _ := m.read()
	m.load()
	d := lkDesiredFromHost(files)
	d.Warm.Tokens["263"] = "8"
	d.SSKeys = &LKWantKeys{Set: map[string]LKKeyEntry{"540": {Method: "2022-blake3-aes-128-gcm", PSK: "MTIzNDU2Nzg5MDEyMzQ1Ng==", Users: []string{"YWJjZGVmZ2hpamtsbW5vcA=="}}}}
	d.Logswitch.Lines = append([]string{}, d.Logswitch.Lines...)
	d.Logswitch.Lines[len(d.Logswitch.Lines)-1] = "540:10021>163.53.52.189:12345"
	var ran []string
	m.run = func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	want := &LeiKwanHostSync{V: 1, Manage: true, Rev: 5, Desired: d, NightOnly: true}
	if !m.manage(context.Background(), files, want) {
		t.Fatal("expected a write")
	}
	again, _ := m.read()
	if again.Warm.Tokens["263"] != "8" || again.Warm.Tokens["540"] != "10" {
		t.Errorf("warm after write %v", again.Warm.Tokens)
	}
	if again.SSKeys.FP["540"] != LKKeyFingerprint(d.SSKeys.Set["540"]) || again.SSKeys.FP["263"] != files.SSKeys.FP["263"] {
		t.Errorf("sskeys after write %v", again.SSKeys.FP)
	}
	// 14:00 TW: the logswitch change waits for the window.
	if len(m.status.Pending) != 1 || m.status.Pending[0] != lkFileLogswitch || len(ran) != 0 {
		t.Errorf("pending %v ran %v", m.status.Pending, ran)
	}
	backups, _ := filepath.Glob(filepath.Join(m.root, "var/lib/flvx-leikwan/backup/*/opt/leikwan-front-test/warm"))
	if len(backups) != 1 {
		t.Errorf("warm backup %v", backups)
	}
	old, _ := os.ReadFile(backups[0])
	if !strings.Contains(string(old), "263=10") {
		t.Errorf("backup content %q", old)
	}
	// Hand edit, same rev: drift, no rewrite.
	warmPath := m.p(again.Warm.Path)
	if err := os.WriteFile(warmPath, []byte("263=3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, _ = m.read()
	if m.manage(context.Background(), files, want) {
		t.Fatal("rewrote a hand edit without a config change")
	}
	if len(m.status.Drift) != 1 || m.status.Drift[0] != lkFileWarm {
		t.Errorf("drift %v", m.status.Drift)
	}
	if raw, _ := os.ReadFile(warmPath); string(raw) != "263=3\n" {
		t.Errorf("hand edit overwritten: %q", raw)
	}
	// 按面板覆盖 (force + 1) rewrites.
	want.Force = 1
	if !m.manage(context.Background(), files, want) {
		t.Fatal("force did not rewrite")
	}
	files, _ = m.read()
	if files.Warm.Tokens["263"] != "8" {
		t.Errorf("after force %v", files.Warm.Tokens)
	}
	// Inside the window the logswitch drop-in is written and restarted.
	m.now = func() time.Time { return time.Date(2026, 11, 16, 18, 30, 0, 0, time.UTC) } // 02:30 TW
	if !m.manage(context.Background(), files, want) {
		t.Fatal("logswitch not written in the window")
	}
	if len(ran) != 2 || !strings.Contains(ran[0], "daemon-reload") || !strings.Contains(ran[1], "restart leikwan-logswitch.service") {
		t.Errorf("ran %v", ran)
	}
	files, _ = m.read()
	if !lkSame(lkFileLogswitch, &files, d) {
		t.Errorf("logswitch after drop-in %v", files.Logswitch.Lines)
	}
	// Persisted: a restarted agent keeps the applied revisions (no rewrite).
	m2 := newLKHostManager(filepath.Dir(m.stateFile))
	m2.root, m2.now, m2.run = m.root, m.now, m.run
	m2.load()
	if m2.persist.Applied[lkFileWarm] != 5 || m2.persist.Forced[lkFileWarm] != 1 {
		t.Errorf("persist %+v", m2.persist)
	}
	// 接管 off: read only.
	_ = os.WriteFile(warmPath, []byte("263=1\n"), 0o644)
	files, _ = m.read()
	off := &LeiKwanHostSync{V: 1, Manage: false, Rev: 9, Force: 9, Desired: d}
	if m.manage(context.Background(), files, off) {
		t.Fatal("wrote with 接管 off")
	}
}

func TestLeiKwanHostWindow(t *testing.T) {
	tw := func(h, mi int) time.Time { return time.Date(2026, 11, 17, h, mi, 0, 0, time.UTC).Add(-8 * time.Hour) }
	for _, c := range []struct {
		h, m int
		ok   bool
	}{{1, 59, false}, {2, 0, true}, {3, 44, true}, {3, 45, false}, {4, 19, false}, {4, 20, true}, {5, 59, true}, {6, 0, false}, {14, 0, false}} {
		if got := lkInWindow(tw(c.h, c.m)); got != c.ok {
			t.Errorf("%02d:%02d TW = %v", c.h, c.m, got)
		}
	}
}

func TestLeiKwanTrafficTable(t *testing.T) {
	script := lkTrafficScript([]int{10018, 10021}, []int{10022})
	for _, s := range []string{"counter u10018 { }", "counter d10021 { }", "flush chain inet flvx_leikwan pre",
		"pre iifname \"lo\" return", "ct original proto-dst { 10022 } drop", "ct direction original meta l4proto { tcp, udp } ct original proto-dst 10018 counter name \"u10018\"",
		"post ct direction reply meta l4proto { tcp, udp } ct original proto-dst 10021 counter name \"d10021\""} {
		if !strings.Contains(script, s) {
			t.Errorf("script lacks %q:\n%s", s, script)
		}
	}
	if strings.Index(script, "drop") > strings.Index(script, "counter name") {
		t.Errorf("block must come before counting")
	}
	listing := `{"nftables": [{"metainfo": {}}, {"table": {"family": "inet", "name": "flvx_leikwan"}},
	 {"counter": {"family": "inet", "name": "u10018", "table": "flvx_leikwan", "packets": 3, "bytes": 1200}},
	 {"counter": {"family": "inet", "name": "d10018", "table": "flvx_leikwan", "packets": 5, "bytes": 9000}}]}`
	got, ok := lkReadCounters([]byte(listing))
	if !ok || got["10018"] != [2]int64{1200, 9000} {
		t.Fatalf("counters %v %v", got, ok)
	}

	m := lkFixture(t, "baidu")
	m.load()
	var scripts []string
	table := false
	m.run = func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		switch {
		case cmd == "-j list table inet flvx_leikwan":
			if !table {
				return nil, os.ErrNotExist
			}
			return []byte(listing), nil
		case cmd == "-f -":
			scripts = append(scripts, string(stdin))
			table = true
		case cmd == "delete table inet flvx_leikwan":
			table = false
		}
		return nil, nil
	}
	want := &LeiKwanHostSync{V: 1, Traffic: true, Ports: []int{10018}, NightOnly: true}
	// 14:00 TW: counting waits for the window, nothing is created.
	if tr := m.trafficTick(context.Background(), want); tr == nil || !tr.Pending || len(scripts) != 0 {
		t.Fatalf("outside window: %+v %v", tr, scripts)
	}
	// A block is enforced at once, even outside the window (no counters yet).
	want.Block = []int{10021}
	tr := m.trafficTick(context.Background(), want)
	if len(scripts) != 1 || strings.Contains(scripts[0], "counter u10018") || !strings.Contains(scripts[0], "{ 10021 } drop") || tr.Epoch == "" {
		t.Fatalf("block outside window: %+v %v", tr, scripts)
	}
	epoch := tr.Epoch
	// In the window the counters are added; the epoch stays (table existed).
	m.now = func() time.Time { return time.Date(2026, 11, 16, 18, 30, 0, 0, time.UTC) }
	tr = m.trafficTick(context.Background(), want)
	if len(scripts) != 2 || !strings.Contains(scripts[1], "counter u10018") || tr.Epoch != epoch || tr.Ports["10018"] != [2]int64{1200, 9000} {
		t.Fatalf("window: %+v %v", tr, scripts)
	}
	// Same wanted state: no nft change.
	m.trafficTick(context.Background(), want)
	if len(scripts) != 2 {
		t.Fatalf("idempotent: %d scripts", len(scripts))
	}
	// Unblock outside the window: blocks go at once, counters stay.
	m.now = func() time.Time { return time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC) }
	want.Block = nil
	m.trafficTick(context.Background(), want)
	if len(scripts) != 3 || strings.Contains(scripts[2], "drop") || !strings.Contains(scripts[2], "counter u10018") {
		t.Fatalf("unblock: %v", scripts)
	}
}

func TestLeiKwanHostSyncAndReport(t *testing.T) {
	m := lkFixture(t, "volc-sh")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.runLoop(ctx)
	rep, err := m.handleSync(ctx, map[string]any{"v": 1, "manage": false, "rev": 1})
	if err != nil || rep == nil || rep.State.FrontChannel != "stable" || rep.Files.Warm != nil {
		t.Fatalf("sync report %+v %v", rep, err)
	}
	if r := m.takeReport(); r != nil {
		t.Errorf("report sent twice right after the sync answer")
	}
	if _, err := m.handleSync(ctx, map[string]any{"v": 2}); err == nil {
		t.Errorf("unknown version accepted")
	}
}

// LK_DUMP_DIR=… go test -run TestLeiKwanHostDumpReports writes the fixture
// hosts' reports (the panel's contract tests use them as testdata).
func TestLeiKwanHostDumpReports(t *testing.T) {
	dir := os.Getenv("LK_DUMP_DIR")
	if dir == "" {
		t.Skip("LK_DUMP_DIR not set")
	}
	for _, host := range lkFixtureHosts {
		m := lkFixture(t, host)
		files, st := m.read()
		raw, _ := json.MarshalIndent(LeiKwanHostReport{V: 1, At: 1794870000000, State: st, Files: files, Manage: LKManageStatus{}}, "", " ")
		if err := os.WriteFile(filepath.Join(dir, host+".json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The panel's own sync JSON (go-backend handler test dump) parses into the
// agent's types, and the key fingerprints agree (cross-module contract).
func TestLeiKwanHostPanelSyncJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "leikwan-host", "panel-sync.json"))
	if err != nil {
		t.Fatal(err)
	}
	fp, _ := os.ReadFile(filepath.Join("testdata", "leikwan-host", "panel-sync.json.fp"))
	var generic map[string]any
	_ = json.Unmarshal(raw, &generic)
	var s LeiKwanHostSync
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.V != 1 || !s.Manage || s.Rev != 7 || s.Force != 2 || !s.Traffic || len(s.Ports) != 2 || len(s.Block) != 1 || s.Desired == nil {
		t.Fatalf("sync %+v", s)
	}
	d := s.Desired
	if d.Warm.Tokens["backup:263"] != "4" || d.SSFilter.Tokens["263"] != "enforce" || d.Triggers.Tokens["263:return_check"] != "on" ||
		d.Logswitch.Lines[0] != "263:10018>163.53.52.189:35046" || d.Coalesce.Value != "off" || d.Phone.Rules["19263"] != "163.53.52.189:35046" {
		t.Fatalf("desired %+v", d)
	}
	if got := LKKeyFingerprint(d.SSKeys.Set["263"]); got != string(fp) {
		t.Fatalf("fingerprint agent %s panel %s", got, fp)
	}
	// Through the command path: handleSync decodes the same JSON (as map data).
	m := lkFixture(t, "baidu")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.runLoop(ctx)
	rep, err := m.handleSync(ctx, generic)
	if err != nil || rep == nil {
		t.Fatalf("handleSync: %v", err)
	}
	if rep.Files.SSKeys.FP["263"] != string(fp) || rep.Files.Warm.Tokens["263"] != "10" || rep.Manage.Rev != 7 {
		t.Fatalf("after sync: keys %v warm %v manage %+v", rep.Files.SSKeys.FP, rep.Files.Warm.Tokens, rep.Manage)
	}
}

// LK_NFT_DUMP=dir writes counting / blocking scripts for the netns lab.
func TestLeiKwanTrafficScriptDump(t *testing.T) {
	dir := os.Getenv("LK_NFT_DUMP")
	if dir == "" {
		t.Skip("LK_NFT_DUMP not set")
	}
	_ = os.WriteFile(filepath.Join(dir, "count.nft"), []byte(lkTrafficScript([]int{10018, 10015, 10021}, nil)), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "block.nft"), []byte(lkTrafficScript([]int{10018, 10015, 10021}, []int{10015})), 0o644)
}

// Default (11-17 user: the 利群入口 machines carry no business yet): no
// night window — the logswitch drop-in and the counter table apply at once.
func TestLeiKwanHostAnytime(t *testing.T) {
	m := lkFixture(t, "aliyun-gz")
	files, _ := m.read()
	m.load()
	var ran []string
	m.run = func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
		ran = append(ran, strings.Join(args, " "))
		if strings.Join(args, " ") == "-j list table inet flvx_leikwan" {
			return nil, os.ErrNotExist
		}
		return nil, nil
	}
	d := lkDesiredFromHost(files)
	d.Logswitch.Lines = append(d.Logswitch.Lines, "540:10021>163.53.52.189:59349")
	want := &LeiKwanHostSync{V: 1, Manage: true, Rev: 2, Desired: d, Traffic: true, Ports: []int{10018}}
	if !m.manage(context.Background(), files, want) || len(m.status.Pending) != 0 {
		t.Fatalf("pending %v", m.status.Pending)
	}
	if tr := m.trafficTick(context.Background(), want); tr == nil || tr.Pending || tr.Epoch == "" {
		t.Fatalf("traffic %+v", tr)
	}
	joined := strings.Join(ran, "|")
	if !strings.Contains(joined, "restart leikwan-logswitch.service") || !strings.Contains(joined, "-f -") {
		t.Fatalf("ran %v", ran)
	}
}
