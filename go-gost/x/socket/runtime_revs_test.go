package socket

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"

	corelogger "github.com/go-gost/core/logger"
	"github.com/go-gost/x/landing"
	xlogger "github.com/go-gost/x/logger"
	"github.com/go-gost/x/registry"
)

func chdirTemp(t *testing.T) {
	t.Helper()
	cwd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func revService(name, addr, rev string) map[string]interface{} {
	m := map[string]interface{}{
		"name": name, "addr": addr,
		"handler":   map[string]interface{}{"type": "tcp"},
		"listener":  map[string]interface{}{"type": "tcp"},
		"forwarder": map[string]interface{}{"nodes": []interface{}{map[string]interface{}{"name": "n1", "addr": "127.0.0.1:9"}}},
	}
	if rev != "" {
		m[runtimeRevKey] = rev
	}
	return m
}

// genericCmd round-trips data like a decoded panel command.
func genericCmd(typ string, data interface{}) CommandMessage {
	raw, _ := json.Marshal(data)
	var g interface{}
	_ = json.Unmarshal(raw, &g)
	return CommandMessage{Type: typ, Data: g, RequestId: typ}
}

func listRevs(t *testing.T, w *WebSocketReporter) (serviceInventory, map[string]string) {
	t.Helper()
	inv, err := w.listServices()
	if err != nil {
		t.Fatal(err)
	}
	return inv, inv.Revs
}

func TestRuntimeRevsFollowServiceCommands(t *testing.T) {
	if corelogger.Default() == nil {
		corelogger.SetDefault(xlogger.NewLogger(xlogger.LevelOption(corelogger.ErrorLevel), xlogger.OutputOption(io.Discard)))
	}
	chdirTemp(t)
	runtimeRevs.reset()
	t.Cleanup(runtimeRevs.reset)
	reporter, peer := routineTestReporter(t)
	addr := freeTCPAddr(t)
	t.Cleanup(func() {
		if svc := registry.ServiceRegistry().Get("rt_svc"); svc != nil {
			registry.ServiceRegistry().Unregister("rt_svc")
			svc.Close()
		}
	})
	step := func(cmd CommandMessage, wantOK bool) {
		t.Helper()
		_ = captureRoutineOutput(t, func() { reporter.routeCommand(cmd) })
		if resp := readRoutineResponse(t, peer); resp.Success != wantOK {
			t.Fatalf("%s: success=%v (%s)", cmd.Type, resp.Success, resp.Message)
		}
	}
	step(genericCmd("AddService", []interface{}{revService("rt_svc", addr, "r1")}), true)
	if _, revs := listRevs(t, reporter); revs["service:rt_svc"] != "r1" {
		t.Fatalf("applied revision not reported: %v", revs)
	}
	inv, _ := listRevs(t, reporter)
	if inv.Boot == "" || inv.Boot != agentBootID {
		t.Fatalf("boot id %q", inv.Boot)
	}
	// An apply without revision (older panel) forgets the revision.
	step(genericCmd("UpdateService", []interface{}{revService("rt_svc", addr, "")}), true)
	if got := runtimeRevs.get("service", "rt_svc"); got != "" {
		t.Fatalf("revision kept after an untagged update: %q", got)
	}
	step(genericCmd("UpdateService", []interface{}{revService("rt_svc", addr, "r2")}), true)
	if got := runtimeRevs.get("service", "rt_svc"); got != "r2" {
		t.Fatalf("update revision %q", got)
	}
	// A failed apply (already exists) forgets it as well.
	step(genericCmd("AddService", []interface{}{revService("rt_svc", addr, "r9")}), false)
	if got := runtimeRevs.get("service", "rt_svc"); got != "" {
		t.Fatalf("revision kept after a failed apply: %q", got)
	}
	step(genericCmd("UpdateService", []interface{}{revService("rt_svc", addr, "r3")}), true)
	step(genericCmd("PauseService", map[string]interface{}{"services": []string{"rt_svc"}}), true)
	if got := runtimeRevs.get("service", "rt_svc"); got != "" {
		t.Fatalf("paused service kept its revision: %q", got)
	}
	step(genericCmd("UpdateService", []interface{}{revService("rt_svc", addr, "r4")}), true)
	step(genericCmd("DeleteService", map[string]interface{}{"services": []string{"rt_svc"}}), true)
	if _, revs := listRevs(t, reporter); len(revs) != 0 {
		t.Fatalf("deleted service still reported: %v", revs)
	}
}

func TestRecordRuntimeRevsChainsLimitersNft(t *testing.T) {
	runtimeRevs.reset()
	t.Cleanup(runtimeRevs.reset)
	chain := map[string]interface{}{"name": "chains_10", runtimeRevKey: "c1"}
	recordRuntimeRevs("AddChains", chain, nil)
	if runtimeRevs.get("chain", "chains_10") != "c1" {
		t.Fatal("chain revision")
	}
	recordRuntimeRevs("UpdateChains", map[string]interface{}{"chain": "chains_10", "data": map[string]interface{}{runtimeRevKey: "c2"}}, nil)
	if runtimeRevs.get("chain", "chains_10") != "c2" {
		t.Fatal("chain update envelope revision")
	}
	recordRuntimeRevs("DeleteChains", map[string]interface{}{"chain": "chains_10"}, nil)
	if runtimeRevs.get("chain", "chains_10") != "" {
		t.Fatal("deleted chain kept revision")
	}
	recordRuntimeRevs("AddLimiters", map[string]interface{}{"name": "forward_1_speed", "limits": []interface{}{"$ 1MB 1MB"}, runtimeRevKey: "l1"}, nil)
	recordRuntimeRevs("UpdateLimiters", map[string]interface{}{"limiter": "forward_1_speed", "data": map[string]interface{}{"name": "forward_1_speed", runtimeRevKey: "l2"}}, errors.New("boom"))
	if runtimeRevs.get("limiter", "forward_1_speed") != "" {
		t.Fatal("failed limiter update kept revision")
	}
	one := map[string]interface{}{"rules": []interface{}{map[string]interface{}{"forward_id": float64(101)}, map[string]interface{}{"forward_id": float64(101)}}, runtimeRevKey: "n1"}
	recordRuntimeRevs("AddNftablesRules", one, nil)
	if runtimeRevs.get("nft", "101_nft") != "n1" {
		t.Fatal("nft revision")
	}
	two := map[string]interface{}{"rules": []interface{}{map[string]interface{}{"forward_id": float64(101)}, map[string]interface{}{"forward_id": float64(102)}}, runtimeRevKey: "n2"}
	recordRuntimeRevs("UpdateNftablesRules", two, nil)
	if runtimeRevs.get("nft", "101_nft") != "" || runtimeRevs.get("nft", "102_nft") != "" {
		t.Fatal("multi-forward nft request must forget revisions")
	}
	recordRuntimeRevs("AddNftablesRules", one, nil)
	recordRuntimeRevs("DeleteNftablesRules", map[string]interface{}{"forward_ids": []interface{}{float64(101)}}, nil)
	if runtimeRevs.get("nft", "101_nft") != "" {
		t.Fatal("deleted nft forward kept revision")
	}
	// Only present objects are reported.
	recordRuntimeRevs("AddChains", map[string]interface{}{"name": "chains_11", runtimeRevKey: "x"}, nil)
	recordRuntimeRevs("AddService", []interface{}{map[string]interface{}{"name": "gone", runtimeRevKey: "y"}}, nil)
	snap := runtimeRevs.snapshot(serviceInventory{Chains: []string{"chains_11"}})
	if fmt.Sprint(snap) != "map[chain:chains_11:x]" {
		t.Fatalf("snapshot %v", snap)
	}
}

func TestHandshakeAdvertisesRuntimeRevs(t *testing.T) {
	q := buildWebSocketQuery("s", "v", 0, 0, 0, 0)
	if want := fmt.Sprintf("&runtimeRevs=%d", runtimeRevsVersion); !containsStr(q, want) {
		t.Fatalf("query %s lacks %s", q, want)
	}
}

// Plan 159: the panel only adds metadata.userLimiter for agents that say so.
func TestHandshakeAdvertisesUserRate(t *testing.T) {
	q := buildWebSocketQuery("s", "v", 0, 0, 0, 0)
	if want := fmt.Sprintf("&userRate=%d", userRateVersion); !containsStr(q, want) {
		t.Fatalf("query %s lacks %s", q, want)
	}
}

// Plan 161: the panel only sends user_rates / rate_group to agents that say so.
func TestHandshakeAdvertisesLandingRate(t *testing.T) {
	q := buildWebSocketQuery("s", "v", 0, 0, 0, 0)
	if want := fmt.Sprintf("&landingRate=%d", landing.LandingRateVersion); !containsStr(q, want) {
		t.Fatalf("query %s lacks %s", q, want)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
