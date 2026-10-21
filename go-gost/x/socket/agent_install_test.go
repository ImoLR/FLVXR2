package socket

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeAgentHost is a fake /etc + /etc/systemd/system with a chosen running binary.
type fakeAgentHost struct {
	t        *testing.T
	etc      string
	systemd  string
	scripts  []string
	progress []string
}

func newFakeAgentHost(t *testing.T) *fakeAgentHost {
	t.Helper()
	root := t.TempDir()
	h := &fakeAgentHost{t: t, etc: filepath.Join(root, "etc")}
	h.systemd = filepath.Join(h.etc, "systemd", "system")
	if err := os.MkdirAll(h.systemd, 0o755); err != nil {
		t.Fatal(err)
	}
	oldEtc, oldSystemd, oldExe, oldStart, oldObserver := agentEtcDir, agentSystemdDir, agentExecutable, startAgentScript, upgradeProgressObserver
	t.Cleanup(func() {
		agentEtcDir, agentSystemdDir, agentExecutable, startAgentScript, upgradeProgressObserver = oldEtc, oldSystemd, oldExe, oldStart, oldObserver
	})
	agentEtcDir, agentSystemdDir = h.etc, h.systemd
	agentExecutable = func() (string, error) { return "", fmt.Errorf("unknown") }
	startAgentScript = func(script string) error {
		h.scripts = append(h.scripts, script)
		return nil
	}
	upgradeProgressObserver = func(stage string, percent int, message string) {
		h.progress = append(h.progress, fmt.Sprintf("%s/%d/%s", stage, percent, message))
	}
	return h
}

// install lays out /etc/<name>/<name> (+ config.json, + unit) like install.sh.
func (h *fakeAgentHost) install(name, config string, unit bool) string {
	h.t.Helper()
	dir := filepath.Join(h.etc, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatal(err)
	}
	bin := filepath.Join(dir, name)
	writeTestFile(h.t, bin, "binary of "+name)
	if config != "" {
		writeTestFile(h.t, filepath.Join(dir, "config.json"), config)
	}
	if unit {
		writeTestFile(h.t, filepath.Join(h.systemd, name+".service"), "[Service]\nExecStart="+bin+"\n")
	}
	return bin
}

func (h *fakeAgentHost) runAs(exe string) {
	agentExecutable = func() (string, error) { return exe, nil }
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func newUpgradeServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "new agent binary")
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func upgradeCmd(url string) map[string]interface{} {
	return map[string]interface{}{"downloadUrls": []string{url + "/agent"}, "version": "3.0.27-fork.67"}
}

func TestResolveAgentInstallExplicitServiceNameUnchanged(t *testing.T) {
	// Production defaults: the paths the old getConfigDir-based code used.
	for _, name := range []string{"flvxx", "flvx_agent", "flvx_agent2", "flux_agent"} {
		in := resolveAgentInstall(name)
		dir := getConfigDir(name)
		if in.Name != name || in.Dir != dir || in.Binary != dir+"/"+name ||
			in.Unit != "/etc/systemd/system/"+name+".service" || in.Source != "service_name" {
			t.Fatalf("%s: %+v", name, in)
		}
	}

	// An explicit service_name wins over the running binary.
	h := newFakeAgentHost(t)
	h.runAs(h.install("flvxx", `{"addr":"x"}`, true))
	h.install("flvx_agent2", "", true)
	if in := resolveAgentInstall("flvx_agent2"); in.Name != "flvx_agent2" || in.Source != "service_name" {
		t.Fatalf("explicit service_name must win: %+v", in)
	}
}

func TestResolveAgentInstallEmptyServiceNameUsesExecutable(t *testing.T) {
	h := newFakeAgentHost(t)
	bin := h.install("flvxx", `{"addr":"x"}`, true)
	h.runAs(bin)
	in := resolveAgentInstall("")
	want := agentInstall{Name: "flvxx", Dir: filepath.Join(h.etc, "flvxx"), Binary: bin,
		Unit: filepath.Join(h.systemd, "flvxx.service"), Source: "executable"}
	if in != want {
		t.Fatalf("got %+v want %+v", in, want)
	}
	if err := in.check(); err != nil {
		t.Fatal(err)
	}

	// Started through a symlink: resolved to the real install.
	link := filepath.Join(t.TempDir(), "agent")
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}
	h.runAs(link)
	if in := resolveAgentInstall(""); in.Name != "flvxx" || in.Source != "executable" {
		t.Fatalf("symlinked binary: %+v", in)
	}

	// No unit for the derived name: today's default (and check refuses it).
	h.runAs(h.install("nounit", `{"addr":"x"}`, false))
	in = resolveAgentInstall("")
	if in.Name != defaultAgentName || in.Source != "default" {
		t.Fatalf("no unit must keep the default: %+v", in)
	}
	if err := in.check(); err == nil || !strings.Contains(err.Error(), "flvx_agent.service") {
		t.Fatalf("missing unit must be refused: %v", err)
	}

	// Binary outside /etc/<n>/<n>: default too.
	other := filepath.Join(h.etc, "flvxx", "gost")
	writeTestFile(t, other, "x")
	h.runAs(other)
	if in := resolveAgentInstall(""); in.Source != "default" {
		t.Fatalf("non-install layout: %+v", in)
	}
}

func TestResolveAgentInstallNeverPicksFluxAgentFromExecutable(t *testing.T) {
	h := newFakeAgentHost(t)
	h.runAs(h.install("flux_agent", `{"addr":"x"}`, true))
	in := resolveAgentInstall("")
	if in.Name != defaultAgentName || in.Source != "default" {
		t.Fatalf("a flux_agent binary must not be derived: %+v", in)
	}
	if err := in.check(); err == nil {
		t.Fatal("no flvx_agent.service: OTA must be refused")
	}
	// The legacy migration stays available for an explicit service_name.
	if in := resolveAgentInstall("flux_agent"); in.Name != "flux_agent" || in.check() != nil {
		t.Fatalf("explicit flux_agent: %+v %v", in, in.check())
	}
}

func TestAgentConfigPathsSkipAnotherPanelsAgent(t *testing.T) {
	h := newFakeAgentHost(t)
	h.install("flux_agent", `{"addr":"df.example:6365","service_name":"flux_agent"}`, true)
	h.install("flvx_agent", `{"addr":"other-panel","service_name":"flvx_agent"}`, true)
	h.runAs(h.install("flvxx", `{"addr":"x"}`, true))
	if got := agentConfigPaths(); strings.Join(got, ",") != "config.json" {
		t.Fatalf("our binary lives in /etc/flvxx, legacy configs must not be read: %v", got)
	}

	// Our own legacy installs still find their config there.
	h.runAs(filepath.Join(h.etc, "flux_agent", "flux_agent"))
	if got := agentConfigPaths(); len(got) != 2 || got[1] != filepath.Join(h.etc, "flux_agent", "config.json") {
		t.Fatalf("binary in /etc/flux_agent: %v", got)
	}
	h.runAs(filepath.Join(h.etc, "flvx_agent", "flvx_agent"))
	if got := agentConfigPaths(); len(got) != 2 || got[1] != filepath.Join(h.etc, "flvx_agent", "config.json") {
		t.Fatalf("binary in /etc/flvx_agent: %v", got)
	}

	// Unknown binary: the old list.
	agentExecutable = func() (string, error) { return "", fmt.Errorf("unknown") }
	if got := agentConfigPaths(); len(got) != 3 {
		t.Fatalf("unknown binary keeps the old list: %v", got)
	}
}

// Node 20 「Vmsilo TW」: /etc/flvxx/flvxx, config.json without service_name, and
// another panel's /etc/flux_agent agent on the same host.
func TestUpgradeAgentWithoutServiceNameTargetsRunningInstall(t *testing.T) {
	h := newFakeAgentHost(t)
	fluxBin := h.install("flux_agent", `{"addr":"df.example:6365","service_name":"flux_agent"}`, true)
	ours := h.install("flvxx", `{"addr":"x"}`, true)
	h.runAs(ours)
	srv, hits := newUpgradeServer(t)

	w := &WebSocketReporter{}
	if err := w.handleUpgradeAgent(upgradeCmd(srv.URL)); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 || len(h.scripts) != 1 {
		t.Fatalf("hits=%d scripts=%v", hits.Load(), h.scripts)
	}
	unit := filepath.Join(h.systemd, "flvxx.service")
	want := fmt.Sprintf("sleep 1 && systemctl stop flvxx && mv %[1]s.new %[1]s && sed -i 's/^StandardOutput=null$/StandardOutput=journal/' %[2]s && sed -i 's/^StandardError=null$/StandardError=journal/' %[2]s && systemctl daemon-reload && systemctl start flvxx", ours, unit)
	if h.scripts[0] != want {
		t.Fatalf("script:\n%s\nwant:\n%s", h.scripts[0], want)
	}
	if strings.Contains(h.scripts[0], "flux_agent") || strings.Contains(h.scripts[0], "flvx_agent") {
		t.Fatalf("script touches another agent: %s", h.scripts[0])
	}
	if got := readTestFile(t, ours+".new"); got != "new agent binary" {
		t.Fatalf("downloaded %q", got)
	}
	if got := readTestFile(t, ours+".old"); got != "binary of flvxx" {
		t.Fatalf("backup %q", got)
	}
	// The other panel's agent is untouched.
	entries, _ := os.ReadDir(filepath.Dir(fluxBin))
	if len(entries) != 2 || readTestFile(t, fluxBin) != "binary of flux_agent" {
		t.Fatalf("flux_agent dir changed: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(h.etc, "flvx_agent")); !os.IsNotExist(err) {
		t.Fatalf("must not create /etc/flvx_agent: %v", err)
	}
}

func TestUpgradeAgentExplicitServiceNameScriptUnchanged(t *testing.T) {
	h := newFakeAgentHost(t)
	ours := h.install("flvx_agent", `{"addr":"x","service_name":"flvx_agent"}`, true)
	h.runAs(filepath.Join(t.TempDir(), "elsewhere")) // explicit name ignores the binary
	srv, _ := newUpgradeServer(t)
	w := &WebSocketReporter{serviceName: "flvx_agent"}
	if err := w.handleUpgradeAgent(upgradeCmd(srv.URL)); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(h.systemd, "flvx_agent.service")
	want := fmt.Sprintf("sleep 1 && systemctl stop flvx_agent && mv %[1]s.new %[1]s && sed -i 's/^StandardOutput=null$/StandardOutput=journal/' %[2]s && sed -i 's/^StandardError=null$/StandardError=journal/' %[2]s && systemctl daemon-reload && systemctl start flvx_agent", ours, unit)
	if len(h.scripts) != 1 || h.scripts[0] != want {
		t.Fatalf("script %v", h.scripts)
	}

	// Explicit flux_agent keeps the legacy migration script.
	h.install("flux_agent", `{"addr":"x","service_name":"flux_agent"}`, true)
	w = &WebSocketReporter{serviceName: "flux_agent"}
	if err := w.handleUpgradeAgent(upgradeCmd(srv.URL)); err != nil {
		t.Fatal(err)
	}
	if len(h.scripts) != 2 || !strings.Contains(h.scripts[1], "systemctl disable flux_agent") ||
		!strings.Contains(h.scripts[1], "systemctl start flvx_agent") {
		t.Fatalf("migration script %v", h.scripts)
	}
}

func TestUpgradeAgentMissingUnitFailsLoudly(t *testing.T) {
	for _, tc := range []struct {
		name        string
		serviceName string
		setup       func(h *fakeAgentHost)
		wantErr     string
	}{
		// Node 20 before the fix, but without flvxx.service: nothing to restart.
		{"empty name, no unit anywhere", "", func(h *fakeAgentHost) {
			h.runAs(h.install("flvxx", `{"addr":"x"}`, false))
		}, "flvx_agent.service"},
		{"explicit name, no unit", "flvxx", func(h *fakeAgentHost) {
			h.install("flvxx", `{"addr":"x","service_name":"flvxx"}`, false)
		}, "flvxx.service"},
		// Empty name, binary not at /etc/<n>/<n>, flvx_agent.service of another agent.
		{"empty name, foreign flvx_agent", "", func(h *fakeAgentHost) {
			h.install("flvx_agent", `{"addr":"other-panel"}`, true)
			bin := filepath.Join(h.etc, "custom", "agent")
			_ = os.MkdirAll(filepath.Dir(bin), 0o755)
			writeTestFile(h.t, bin, "x")
			h.runAs(bin)
		}, "为免误动其他服务"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeAgentHost(t)
			tc.setup(h)
			srv, hits := newUpgradeServer(t)
			w := &WebSocketReporter{serviceName: tc.serviceName}
			err := w.handleUpgradeAgent(upgradeCmd(srv.URL))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v want %q", err, tc.wantErr)
			}
			if hits.Load() != 0 || len(h.scripts) != 0 {
				t.Fatalf("must fail before downloading/restarting: hits=%d scripts=%v", hits.Load(), h.scripts)
			}
			if len(h.progress) != 1 || !strings.HasPrefix(h.progress[0], "failed/100/升级失败：") {
				t.Fatalf("progress %v", h.progress)
			}
		})
	}
}

func TestUpgradeAgentDownloadFailureReportsFailed(t *testing.T) {
	h := newFakeAgentHost(t)
	h.runAs(h.install("flvxx", `{"addr":"x"}`, true))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	w := &WebSocketReporter{}
	if err := w.handleUpgradeAgent(upgradeCmd(srv.URL)); err == nil {
		t.Fatal("want error")
	}
	if len(h.scripts) != 0 || len(h.progress) == 0 || !strings.HasPrefix(h.progress[len(h.progress)-1], "failed/100/") {
		t.Fatalf("scripts=%v progress=%v", h.scripts, h.progress)
	}
}

func TestRollbackAgentWithoutServiceName(t *testing.T) {
	h := newFakeAgentHost(t)
	h.install("flux_agent", `{"service_name":"flux_agent"}`, true)
	ours := h.install("flvxx", `{"addr":"x"}`, true)
	writeTestFile(t, ours+".old", "previous")
	h.runAs(ours)
	w := &WebSocketReporter{}
	if err := w.handleRollbackAgent(nil); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(h.systemd, "flvxx.service")
	want := fmt.Sprintf("sleep 1 && systemctl stop flvxx && cp %[1]s.old %[1]s && sed -i 's/^StandardOutput=null$/StandardOutput=journal/' %[2]s && sed -i 's/^StandardError=null$/StandardError=journal/' %[2]s && systemctl daemon-reload && systemctl start flvxx", ours, unit)
	if len(h.scripts) != 1 || h.scripts[0] != want {
		t.Fatalf("script %v", h.scripts)
	}

	// Unit gone: refused, reported as failed.
	if err := os.Remove(unit); err != nil {
		t.Fatal(err)
	}
	h.progress = nil
	if err := w.handleRollbackAgent(nil); err == nil {
		t.Fatal("missing unit must fail")
	}
	if len(h.scripts) != 1 || len(h.progress) != 1 || !strings.HasPrefix(h.progress[0], "failed/100/回退失败：") {
		t.Fatalf("scripts=%v progress=%v", h.scripts, h.progress)
	}
}
