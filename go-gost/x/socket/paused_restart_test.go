package socket

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"testing"
	"time"

	corelogger "github.com/go-gost/core/logger"
	"github.com/go-gost/x/config"
	parser "github.com/go-gost/x/config/parsing/service"
	xlogger "github.com/go-gost/x/logger"
	"github.com/go-gost/x/registry"
)

// listening reports whether something accepts TCP connections on addr.
func listening(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func waitListening(t *testing.T, addr string, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if listening(addr) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s listening=%v, want %v", addr, !want, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// restartAgent does what a new agent process does with the saved gost.json:
// parse it, register every service the way loader.Load does (the loader
// itself is tested in config/loader; its logger setup cannot run here, other
// tests of this package store a different logger type), then Serve every
// registered service (program.Start).
func restartAgent(t *testing.T) {
	t.Helper()
	for name := range registry.ServiceRegistry().GetAll() {
		registry.ServiceRegistry().Unregister(name)
	}
	raw, err := os.ReadFile("gost.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	config.Set(&cfg)
	for _, svcCfg := range cfg.Services {
		svc, err := parser.ParseServiceOrPaused(svcCfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.ServiceRegistry().Register(svcCfg.Name, svc); err != nil {
			t.Fatal(err)
		}
	}
	for _, svc := range registry.ServiceRegistry().GetAll() {
		go svc.Serve()
	}
}

func storedPaused(name string) bool {
	for _, s := range config.Global().Services {
		if s != nil && s.Name == name {
			return parser.IsPaused(s)
		}
	}
	return false
}

// Plan 236: a paused rule must stay closed after an agent restart (the bug:
// the loader parsed and served every service of gost.json) and after an
// UpdateService for the paused rule; ResumeService must still work afterwards.
func TestPausedServiceStaysClosedAcrossRestartAndUpdate(t *testing.T) {
	if corelogger.Default() == nil {
		corelogger.SetDefault(xlogger.NewLogger(xlogger.LevelOption(corelogger.ErrorLevel), xlogger.OutputOption(io.Discard)))
	}
	chdirTemp(t)
	config.Set(&config.Config{})
	runtimeRevs.reset()
	t.Cleanup(runtimeRevs.reset)
	reporter, peer := routineTestReporter(t)
	addr := freeTCPAddr(t)
	other := freeTCPAddr(t)
	const name, otherName = "pr_svc_tcp", "pr_other_tcp"
	t.Cleanup(func() {
		for _, n := range []string{name, otherName} {
			registry.ServiceRegistry().Unregister(n)
		}
		config.Set(&config.Config{})
	})
	step := func(cmd CommandMessage, wantOK bool) {
		t.Helper()
		_ = captureRoutineOutput(t, func() { reporter.routeCommand(cmd) })
		if resp := readRoutineResponse(t, peer); resp.Success != wantOK {
			t.Fatalf("%s: success=%v (%s)", cmd.Type, resp.Success, resp.Message)
		}
	}

	step(genericCmd("AddService", []interface{}{revService(name, addr, ""), revService(otherName, other, "")}), true)
	waitListening(t, addr, true)
	step(genericCmd("PauseService", map[string]interface{}{"services": []string{name}}), true)
	waitListening(t, addr, false)

	// 1. Restart: the paused rule stays closed, the running one comes back.
	restartAgent(t)
	waitListening(t, other, true)
	time.Sleep(200 * time.Millisecond)
	if listening(addr) {
		t.Fatal("paused service listens again after an agent restart")
	}
	if svc := registry.ServiceRegistry().Get(name); !parser.IsPausedService(svc) {
		t.Fatalf("paused service registered as %T, want the placeholder", svc)
	}
	inv, err := reporter.listServices()
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Paused) != 1 || inv.Paused[0] != name {
		t.Fatalf("inventory paused=%v", inv.Paused)
	}

	// 2. An UpdateService that carries paused: true (panel, paused rule)
	// keeps it closed and keeps the flag in gost.json.
	upd := revService(name, addr, "r1")
	upd["metadata"] = map[string]interface{}{"paused": true}
	step(genericCmd("UpdateService", []interface{}{upd}), true)
	time.Sleep(200 * time.Millisecond)
	if listening(addr) || !storedPaused(name) {
		t.Fatalf("UpdateService(paused) listening=%v storedPaused=%v", listening(addr), storedPaused(name))
	}
	// PauseService on the placeholder (old panel follow-up) is fine.
	step(genericCmd("PauseService", map[string]interface{}{"services": []string{name}}), true)

	// 3. ResumeService after the restart serves it again.
	step(genericCmd("ResumeService", map[string]interface{}{"services": []string{name}}), true)
	waitListening(t, addr, true)
	if storedPaused(name) {
		t.Fatal("resumed service still marked paused")
	}
	// ...and a restart keeps it running.
	restartAgent(t)
	waitListening(t, addr, true)

	// 4. Pause again → restart → still closed; DeleteService removes it.
	step(genericCmd("PauseService", map[string]interface{}{"services": []string{name}}), true)
	waitListening(t, addr, false)
	restartAgent(t)
	time.Sleep(200 * time.Millisecond)
	if listening(addr) {
		t.Fatal("paused service listens after the second restart")
	}
	// AddService of a paused config (re-create) stays closed too.
	step(genericCmd("DeleteService", map[string]interface{}{"services": []string{name}}), true)
	add := revService(name, addr, "")
	add["metadata"] = map[string]interface{}{"paused": true}
	step(genericCmd("AddService", []interface{}{add}), true)
	time.Sleep(200 * time.Millisecond)
	if listening(addr) {
		t.Fatal("AddService(paused) listens")
	}
	step(genericCmd("ResumeService", map[string]interface{}{"services": []string{name}}), true)
	waitListening(t, addr, true)
}
