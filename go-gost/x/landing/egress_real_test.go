package landing

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// realCore copies the pinned, SHA256-verified Core into dir. Tests run with
// FLVX_LANDING_TEST_BINARY; geo rules additionally need FLVX_LANDING_GEODIR
// holding the pinned geoip.dat/geosite.dat.
func realCore(t *testing.T, dir string) string {
	t.Helper()
	binary := os.Getenv("FLVX_LANDING_TEST_BINARY")
	if binary == "" {
		t.Skip("pinned Core integration binary not supplied")
	}
	if hash, err := fileHash(binary); err != nil || hash != CoreSHA256["amd64"] {
		t.Fatal("pinned binary checksum mismatch", err)
	}
	b, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "flvx-landing-core")
	if err := os.WriteFile(path, b, 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func copyGeo(t *testing.T, dir string) bool {
	t.Helper()
	src := os.Getenv("FLVX_LANDING_GEODIR")
	if src == "" {
		return false
	}
	for name, sum := range GeoDataSHA256 {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0644); err != nil {
			t.Fatal(err)
		}
		if h, _ := fileHash(filepath.Join(dir, name)); h != sum {
			t.Fatalf("%s is not the pinned release", name)
		}
	}
	return true
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// The Core must accept every rendered outbound type and rule matcher.
func TestEgressRealCoreAcceptsRenderedConfig(t *testing.T) {
	dir := t.TempDir()
	core := realCore(t, dir)
	geo := copyGeo(t, dir)
	plan := goldenPlan()
	if geo {
		plan.Rules = append(plan.Rules,
			EgressRule{InboundTags: []string{"flvxl-1"}, IP: []string{"geoip:cn", "geoip:!us"}, Outbound: "block"},
			EgressRule{InboundTags: []string{"flvxl-1"}, Domain: []string{"geosite:cn", "geosite:netflix", "geosite:category-ads"}, Outbound: "block"},
			EgressRule{InboundTags: []string{"flvxl-1"}, SourceIP: []string{"geoip:cn"}, Outbound: "block"},
			EgressRule{InboundTags: []string{"flvxl-1"}, Protocol: []string{"http", "tls", "quic", "bittorrent"}, Network: "tcp,udp", Outbound: "block"},
		)
		plan.ExemptSources = []string{"1.0.1.7", "240e::1"}
	}
	port := freePort(t)
	cfg := baseConfig(port)
	n1, n2 := testNode(t, 1), testNode(t, 2)
	n1.Port, n2.Port = freePort(t), freePort(t)
	in1, _ := n1.inbound()
	in2, _ := n2.inbound()
	cfg["inbounds"] = []any{in1, in2}
	outbounds := cfg["outbounds"].([]any)
	for _, o := range plan.Outbounds {
		rendered, _ := o.render(goldenLocal)
		outbounds = append(outbounds, rendered)
	}
	cfg["outbounds"] = outbounds
	rules, err := renderRules(plan, func(code string) ([]netip.Prefix, error) { return readGeoIP(filepath.Join(dir, "geoip.dat"), code) }, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg["routing"].(map[string]any)["rules"] = rules
	b, _ := json.MarshalIndent(cfg, "", "  ")
	path := filepath.Join(dir, "egress-test.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(core, "run", "-test", "-config", path)
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("real Core rejected rendered egress: %v\n%s", err, out)
	}
	t.Logf("real Core accepted %d outbounds and %d rules (geo=%v)", len(plan.Outbounds), len(rules), geo)
}

// Hot apply: outbound add/remove and atomic rule replacement keep the epoch;
// the default plan returns to the safety rule only.
func TestEgressRealCoreHotApply(t *testing.T) {
	dir := t.TempDir()
	realCore(t, dir)
	m := NewManager(dir, true)
	m.detectLocal = func() localAddrs { return localAddrs{v4: netip.MustParseAddr("127.0.0.1")} }
	n := testNode(t, 1)
	n.Port = freePort(t)
	plan := &Egress{Version: 1, Outbounds: []EgressOutbound{{Tag: "flvxo-a", Type: "direct", IPVersion: "prefer_ipv4"}, {Tag: "flvxo-b", Type: "socks", IPVersion: "system", Server: "203.0.113.5", Port: 1080}}, Rules: []EgressRule{
		{InboundTags: []string{n.InboundTag}, Port: "25", Outbound: "block"},
		{InboundTags: []string{n.InboundTag}, Network: "udp", Outbound: "flvxo-b"},
		{InboundTags: []string{n.InboundTag}, Outbound: "flvxo-a"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, Nodes: []Node{n}, Egress: plan}
	status, err := m.Apply(ctx, d)
	defer func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	}()
	if err != nil {
		t.Fatal(err)
	}
	if status.Egress == nil || status.Egress.State != "applied" || !status.Egress.Ready {
		t.Fatalf("egress status %+v", status.Egress)
	}
	epoch := status.Epoch
	rules := apiTags(t, m, "lsrules", "rules", "ruleTag")
	if strings.Join(rules, ",") != "flvx-safety,flvxr-1,flvxr-2,flvxr-3" {
		t.Fatalf("rules = %v", rules)
	}
	outs := apiTags(t, m, "lso", "outbounds", "tag")
	if countPrefix(outs, "flvxo-a-") != 1 || countPrefix(outs, "flvxo-b-") != 1 {
		t.Fatalf("outbounds = %v", outs)
	}
	if !strings.Contains(string(mustAPI(t, m, "lsi")), n.InboundTag) {
		t.Fatal("inbound missing after egress apply")
	}
	// Change one outbound and drop a rule: hot swap, same epoch.
	d.Revision++
	d.Egress = &Egress{Version: 1, Outbounds: []EgressOutbound{{Tag: "flvxo-a", Type: "direct", IPVersion: "force_ipv4"}}, Rules: []EgressRule{{InboundTags: []string{n.InboundTag}, Outbound: "flvxo-a"}}}
	status, err = m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if status.Epoch != epoch {
		t.Fatal("plan change restarted the Core")
	}
	outs = apiTags(t, m, "lso", "outbounds", "tag")
	if countPrefix(outs, "flvxo-a-") != 1 || countPrefix(outs, "flvxo-b-") != 0 {
		t.Fatalf("outbounds after swap = %v", outs)
	}
	if rules = apiTags(t, m, "lsrules", "rules", "ruleTag"); strings.Join(rules, ",") != "flvx-safety,flvxr-1" {
		t.Fatalf("rules after swap = %v", rules)
	}
	// Back to the built-in default: safety rule only, no FLVX outbounds.
	d.Revision++
	d.Egress = nil
	status, err = m.Apply(ctx, d)
	if err != nil || status.Epoch != epoch || status.Egress.State != "none" {
		t.Fatalf("default plan: %v %+v", err, status.Egress)
	}
	if rules = apiTags(t, m, "lsrules", "rules", "ruleTag"); strings.Join(rules, ",") != "flvx-safety" {
		t.Fatalf("rules after reset = %v", rules)
	}
	if outs = apiTags(t, m, "lso", "outbounds", "tag"); countPrefix(outs, "flvxo-") != 0 {
		t.Fatalf("outbounds after reset = %v", outs)
	}
	t.Logf("hot apply kept epoch %s across add, swap and reset", epoch)
}

// A Core started with the pre-egress config (no RoutingService) keeps serving
// existing nodes, withholds nodes of an unconfirmed plan and restarts exactly
// once when the admin confirmed it.
func TestEgressRealCoreOneTimeRestart(t *testing.T) {
	dir := t.TempDir()
	core := realCore(t, dir)
	m := NewManager(dir, true)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	n1, n2 := testNode(t, 1), testNode(t, 2)
	n1.Port, n2.Port = freePort(t), freePort(t)
	// Simulate an upgraded agent next to a Core that still runs the old config.
	m.apiPort = freePort(t)
	if err := m.writeJSON("api.json", map[string]int{"port": m.apiPort}); err != nil {
		t.Fatal(err)
	}
	old := baseConfig(m.apiPort)
	old["api"].(map[string]any)["services"] = []string{"HandlerService", "StatsService"}
	old["routing"].(map[string]any)["domainStrategy"] = "IPIfNonMatch"
	if err := m.writeJSON("old-config.json", old); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(core, "run", "-config", filepath.Join(dir, "old-config.json"))
	cmd.Env = append(os.Environ(), "MMWXC_CORE_CONTROL_SOCKET="+m.socket())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	m.proc = cmd.Process
	go func() { _ = cmd.Wait() }()
	defer func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	}()
	for i := 0; i < 100; i++ {
		if _, err := m.snapshot(ctx); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	d := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, Nodes: []Node{n1, n2}}
	status, err := m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	epoch := status.Epoch
	if status.Egress.Ready {
		t.Fatal("old Core reported the routing API")
	}
	// Plan for node 2 without confirmation: node 1 keeps working, node 2 stays
	// down instead of leaving through the default outbound.
	d.Revision++
	d.Egress = &Egress{Version: 1, Rules: []EgressRule{{InboundTags: []string{n2.InboundTag}, Port: "25", Outbound: "block"}}}
	status, err = m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if status.Epoch != epoch || status.Egress.State != "restart_required" || len(status.Egress.Withheld) != 1 || status.Egress.Withheld[0] != n2.InboundTag {
		t.Fatalf("unconfirmed plan status %+v", status.Egress)
	}
	listed := string(mustAPI(t, m, "lsi"))
	if !strings.Contains(listed, n1.InboundTag) || strings.Contains(listed, n2.InboundTag) {
		t.Fatalf("inbounds during restart_required: %s", listed)
	}
	// Confirmed: exactly one restart onto the new base config, then hot apply.
	d.Revision++
	d.Egress.AllowCoreRestart = true
	status, err = m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if status.Epoch == epoch || status.Egress.State != "applied" || !status.Egress.Ready || len(status.Egress.Withheld) != 0 {
		t.Fatalf("confirmed restart status %+v epoch %s→%s", status.Egress, epoch, status.Epoch)
	}
	listed = string(mustAPI(t, m, "lsi"))
	if !strings.Contains(listed, n1.InboundTag) || !strings.Contains(listed, n2.InboundTag) {
		t.Fatalf("inbounds after restart: %s", listed)
	}
	restarted := status.Epoch
	d.Revision++
	d.Egress.Rules = append(d.Egress.Rules, EgressRule{InboundTags: []string{n1.InboundTag}, Port: "587", Outbound: "block"})
	status, err = m.Apply(ctx, d)
	if err != nil || status.Epoch != restarted {
		t.Fatalf("later plan change restarted again: %v", err)
	}
	t.Logf("old Core epoch %s → one restart %s, later changes hot", epoch, restarted)
}

func apiTags(t *testing.T, m *Manager, command, list, field string) []string {
	t.Helper()
	var out map[string][]map[string]any
	if err := json.Unmarshal(mustAPI(t, m, command), &out); err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, item := range out[list] {
		if v, ok := item[field].(string); ok {
			tags = append(tags, v)
		}
	}
	return tags
}

func mustAPI(t *testing.T, m *Manager, command string, args ...string) []byte {
	t.Helper()
	raw, err := m.api(context.Background(), command, args...)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func countPrefix(items []string, prefix string) int {
	n := 0
	for _, s := range items {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}

// A steady reconcile tick (nothing to add or remove) runs the Core API CLI
// twice: lsi and one statsquery. Removing an inbound still samples the
// counters right before the removal and once after.
func TestLandingSteadyTickCLICalls(t *testing.T) {
	dir := t.TempDir()
	core := realCore(t, dir)
	calls := filepath.Join(dir, "calls.log")
	wrapper := filepath.Join(dir, "core-counting")
	script := "#!/bin/sh\nif [ \"$1\" = api ]; then echo \"$2\" >> " + calls + "; fi\nexec " + core + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, true)
	m.execPath = wrapper
	defer func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	}()
	n1, n2 := testNode(t, 1), testNode(t, 2)
	n1.Port, n2.Port = freePort(t), freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, Nodes: []Node{n1, n2}, Egress: &Egress{Version: 1, Rules: []EgressRule{{InboundTags: []string{n1.InboundTag}, Port: "25", Outbound: "block"}}}}
	if _, err := m.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}
	count := func() map[string]int {
		b, _ := os.ReadFile(calls)
		out := map[string]int{}
		for _, l := range strings.Fields(string(b)) {
			out[l]++
		}
		_ = os.Remove(calls)
		return out
	}
	count()
	for i := 0; i < 3; i++ {
		m.mu.Lock()
		err := m.reconcile(ctx)
		m.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := count(); got["lsi"] != 3 || got["statsquery"] != 3 || len(got) != 2 {
		t.Fatalf("3 steady ticks ran %v; want lsi×3 and statsquery×3 only", got)
	}
	d.Revision++
	d.Nodes[1].Enabled = false
	if _, err := m.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got := count(); got["rmi"] != 1 || got["statsquery"] != 3 {
		t.Fatalf("removal ran %v; want rmi×1 and statsquery×3 (before controls, before removal, after)", got)
	}
}
