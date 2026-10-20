package landing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Fixed key material keeps the chain golden file stable.
var goldenChainPrivate = base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

const goldenPin = "6d5f0e1c9a8b7c6d5e4f30211203f4e5d6c7b8a9908172635445362718090a1b"

func chainNode(id int64, upstream EgressOutbound) Node {
	upstream.Tag = chainOutboundTag(id)
	if upstream.IPVersion == "" {
		upstream.IPVersion = "system"
	}
	return Node{ID: id, InboundTag: "flvxl-" + strconv.FormatInt(id, 10), Protocol: "vless_reality_vision", Port: 40000 + int(id%1000), Enabled: true, Listen: ChainListen,
		VLESS: &VLESS{UUID: "8a8e3c4d-1f2b-4c3d-9e8f-0a1b2c3d4e5f", Flow: "xtls-rprx-vision", Reality: Reality{Dest: "www.lovelive-anime.jp:443", ServerNames: []string{"www.lovelive-anime.jp"}, PrivateKey: goldenChainPrivate, ShortIDs: []string{"a1b2c3d4"}}},
		Chain: &upstream}
}

// goldenChainUpstreams covers every upstream a pasted share link can become.
func goldenChainUpstreams() []EgressOutbound {
	uid := "d77c8b43-40ed-42cd-9150-f3f47d0f534d"
	return []EgressOutbound{
		{Type: "shadowsocks", Server: "203.0.113.10", Port: 8388, Method: "2022-blake3-aes-128-gcm", Password: goldenSSKey},
		{Type: "shadowsocks", Server: "ss.example.com", Port: 8389, Method: "chacha20-ietf-poly1305", Password: "p@ss:word"},
		{Type: "vless", Server: "198.51.100.7", Port: 443, UUID: uid, Flow: "xtls-rprx-vision", Security: "reality", Reality: &EgressReality{ServerName: "www.example.com", PublicKey: goldenPBK, ShortID: "0a1b", Fingerprint: "chrome"}},
		{Type: "vless", Server: "vl.example.com", Port: 443, UUID: uid, Flow: "xtls-rprx-vision", Security: "tls", TLS: &EgressTLS{ServerName: "vl.example.com", Fingerprint: "chrome", ALPN: []string{"h2", "http/1.1"}}},
		{Type: "vless", Server: "ws.example.com", Port: 443, UUID: uid, Network: "ws", Security: "tls", WS: &EgressWS{Path: "/ray?ed=2048", Host: "cdn.example.com"}, TLS: &EgressTLS{ServerName: "cdn.example.com"}},
		{Type: "vless", Server: "grpc.example.com", Port: 443, UUID: uid, Network: "grpc", Security: "tls", GRPC: &EgressGRPC{ServiceName: "grpcsvc", MultiMode: true}},
		{Type: "vless", Server: "198.51.100.8", Port: 8080, UUID: uid, Security: "none"},
		{Type: "vmess", Server: "vm.example.com", Port: 443, UUID: uid, Cipher: "auto", Network: "ws", Security: "tls", WS: &EgressWS{Path: "/vm"}, TLS: &EgressTLS{ServerName: "vm.example.com", PinnedSHA256: goldenPin}},
		{Type: "vmess", Server: "198.51.100.9", Port: 10086, UUID: uid, Cipher: "aes-128-gcm", Security: "none"},
		{Type: "trojan", Server: "tj.example.com", Port: 443, Password: "trojan-pass", Security: "tls", TLS: &EgressTLS{ServerName: "tj.example.com"}},
		{Type: "trojan", Server: "tjg.example.com", Port: 443, Password: "trojan-pass", Network: "grpc", Security: "tls", GRPC: &EgressGRPC{ServiceName: "tj"}},
		{Type: "socks", Server: "203.0.113.11", Port: 1080, Username: "u", Password: "p"},
		{Type: "http", Server: "203.0.113.12", Port: 3128},
		{Type: "http", Server: "proxy.example.com", Port: 443, Username: "u", Password: "p", Security: "tls", TLS: &EgressTLS{ServerName: "proxy.example.com"}},
		{Type: "hysteria2", Server: "hy.example.com", Port: 443, Password: "hy2-auth", TLS: &EgressTLS{ServerName: "hy.example.com"}},
		{Type: "hysteria2", Server: "203.0.113.13", Port: 8443, Password: "hy2-auth", Obfs: "salamander-pass", TLS: &EgressTLS{ServerName: "hy.example.com", PinnedSHA256: goldenPin}},
	}
}

func goldenChainDesired() Desired {
	d := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50}
	for i, o := range goldenChainUpstreams() {
		d.Nodes = append(d.Nodes, chainNode(int64(1<<40+i+1), o))
	}
	return d
}

func TestChainNodeValidation(t *testing.T) {
	d := goldenChainDesired()
	if err := d.validate(); err != nil {
		t.Fatalf("golden chain desired rejected: %v", err)
	}
	ok := chainNode(7, goldenChainUpstreams()[0])
	inbound, err := ok.inbound()
	if err != nil || inbound["listen"] != ChainListen {
		t.Fatalf("chain inbound must listen on loopback only: %v %v", inbound["listen"], err)
	}
	public := testNode(t, 8)
	if in, _ := public.inbound(); in["listen"] != "0.0.0.0" {
		t.Fatalf("public node listen changed: %v", in["listen"])
	}
	mutate := map[string]func(n *Node){
		"public listen on chain": func(n *Node) { n.Listen = "" },
		"other listen address":   func(n *Node) { n.Listen = "0.0.0.0" },
		"loopback without chain": func(n *Node) { n.Chain = nil },
		"chain on ss2022":        func(n *Node) { n.Protocol = "ss2022"; n.SS2022 = public.SS2022; n.VLESS = nil },
		"foreign chain tag":      func(n *Node) { n.Chain.Tag = "flvxc-99" },
		"plan tag on chain":      func(n *Node) { n.Chain.Tag = "flvxo-1" },
		"direct chain": func(n *Node) {
			n.Chain.Type = "direct"
			n.Chain.Server = ""
			n.Chain.Port = 0
			n.Chain.Method = ""
			n.Chain.Password = ""
		},
		"private upstream":         func(n *Node) { n.Chain.Server = "10.0.0.5" },
		"loopback upstream":        func(n *Node) { n.Chain.Server = "127.0.0.1" },
		"metadata upstream":        func(n *Node) { n.Chain.Server = "169.254.169.254" },
		"ula upstream":             func(n *Node) { n.Chain.Server = "fd00:ec2::254" },
		"bad port":                 func(n *Node) { n.Chain.Port = 0 },
		"transport on shadowsocks": func(n *Node) { n.Chain.Network = "ws" },
	}
	for name, fn := range mutate {
		n := chainNode(7, goldenChainUpstreams()[0])
		c := *n.Chain
		n.Chain = &c
		fn(&n)
		if err := (&Desired{Revision: 1, Enabled: true, Nodes: []Node{n}}).validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	bad := map[string]EgressOutbound{
		"vless flow over ws":        {Type: "vless", Server: "a.example.com", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Flow: "xtls-rprx-vision", Network: "ws", Security: "tls"},
		"vless flow without tls":    {Type: "vless", Server: "a.example.com", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Flow: "xtls-rprx-vision", Security: "none"},
		"vless bad uuid":            {Type: "vless", Server: "a.example.com", Port: 443, UUID: "nope", Security: "none"},
		"vless reality over ws":     {Type: "vless", Server: "a.example.com", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Network: "ws", Security: "reality", Reality: &EgressReality{ServerName: "a.example.com", PublicKey: goldenPBK}},
		"grpc without service":      {Type: "vless", Server: "a.example.com", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Network: "grpc", Security: "tls"},
		"ws bad path":               {Type: "vmess", Server: "a.example.com", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Network: "ws", Security: "tls", WS: &EgressWS{Path: "no-slash"}},
		"vmess bad cipher":          {Type: "vmess", Server: "a.example.com", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Cipher: "rc4", Security: "none"},
		"vmess reality":             {Type: "vmess", Server: "a.example.com", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Security: "reality", Reality: &EgressReality{ServerName: "a.example.com", PublicKey: goldenPBK}},
		"trojan without tls":        {Type: "trojan", Server: "a.example.com", Port: 443, Password: "x", Security: "none"},
		"trojan empty password":     {Type: "trojan", Server: "a.example.com", Port: 443, Security: "tls"},
		"trojan newline password":   {Type: "trojan", Server: "a.example.com", Port: 443, Password: "a\nb", Security: "tls"},
		"tls bad alpn":              {Type: "trojan", Server: "a.example.com", Port: 443, Password: "x", Security: "tls", TLS: &EgressTLS{ALPN: []string{"spdy"}}},
		"tls bad fingerprint":       {Type: "trojan", Server: "a.example.com", Port: 443, Password: "x", Security: "tls", TLS: &EgressTLS{Fingerprint: "netscape"}},
		"tls bad pin":               {Type: "trojan", Server: "a.example.com", Port: 443, Password: "x", Security: "tls", TLS: &EgressTLS{PinnedSHA256: "abc"}},
		"hysteria2 no auth":         {Type: "hysteria2", Server: "a.example.com", Port: 443},
		"hysteria2 over ws":         {Type: "hysteria2", Server: "a.example.com", Port: 443, Password: "x", Network: "ws"},
		"http proxy with ws":        {Type: "http", Server: "a.example.com", Port: 443, Network: "ws", Security: "tls"},
		"socks with tls":            {Type: "socks", Server: "a.example.com", Port: 443, Security: "tls"},
		"ss2022 bad key":            {Type: "shadowsocks", Server: "a.example.com", Port: 443, Method: "2022-blake3-aes-128-gcm", Password: "short"},
		"unknown type":              {Type: "tuic", Server: "a.example.com", Port: 443, Password: "x"},
		"single label host":         {Type: "socks", Server: "localhost", Port: 1080},
		"reality short id odd size": {Type: "vless", Server: "a.example.com", Port: 443, UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Security: "reality", Reality: &EgressReality{ServerName: "a.example.com", PublicKey: goldenPBK, ShortID: "abc"}},
	}
	for name, o := range bad {
		o.Tag, o.IPVersion = "flvxc-1", "system"
		if err := o.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// renderedChain renders what the Core receives for the golden chain desired:
// loopback inbounds, the upstream outbounds and the rule order.
func renderedChain(t *testing.T) map[string]any {
	t.Helper()
	d := goldenChainDesired()
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	m := &Manager{desired: d}
	plan := m.effectiveEgress()
	var inbounds, outbounds []any
	for _, n := range d.Nodes {
		in, err := n.inbound()
		if err != nil {
			t.Fatal(err)
		}
		inbounds = append(inbounds, in)
	}
	for _, o := range plan.Outbounds {
		rendered, warn := o.render(goldenLocal)
		if warn != "" {
			t.Fatalf("unexpected warning for %s: %s", o.Tag, warn)
		}
		outbounds = append(outbounds, rendered)
	}
	rules, err := renderRules(plan, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"inbounds": inbounds, "outbounds": outbounds, "rules": rules}
}

func TestChainRenderGolden(t *testing.T) {
	got := renderedChain(t)
	b, _ := json.MarshalIndent(got, "", "  ")
	path := filepath.Join("testdata", "chain_golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var a, w any
	_ = json.Unmarshal(b, &a)
	_ = json.Unmarshal(want, &w)
	if !reflect.DeepEqual(a, w) {
		t.Fatalf("chain render changed; rerun with UPDATE_GOLDEN=1 after review\n%s", b)
	}
}

// Rule order: safety first, then one rule per chain node, then the server
// plan; chain nodes are never routed by the plan or the default outbound.
func TestChainEffectiveEgressOrder(t *testing.T) {
	chain := chainNode(5, goldenChainUpstreams()[11])
	public := testNode(t, 1)
	plan := &Egress{Version: 1, Outbounds: []EgressOutbound{{Tag: "flvxo-a", Type: "direct", IPVersion: "force_ipv4"}}, Rules: []EgressRule{{InboundTags: []string{public.InboundTag}, Outbound: "flvxo-a"}}}
	m := &Manager{desired: Desired{Revision: 1, Enabled: true, Nodes: []Node{public, chain}, Egress: plan}}
	if err := m.desired.validate(); err != nil {
		t.Fatal(err)
	}
	e := m.effectiveEgress()
	rules, err := renderRules(e, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("rules = %v", rules)
	}
	first, second, third := rules[0].(map[string]any), rules[1].(map[string]any), rules[2].(map[string]any)
	if first["ruleTag"] != "flvx-safety" || first["outboundTag"] != "block" {
		t.Fatalf("safety rule not first: %v", first)
	}
	if !reflect.DeepEqual(second["inboundTag"], []string{chain.InboundTag}) || second["outboundTag"] != "flvxc-5" || len(second) != 4 {
		t.Fatalf("chain rule must match only its inbound and catch everything: %v", second)
	}
	if !reflect.DeepEqual(third["inboundTag"], []string{public.InboundTag}) {
		t.Fatalf("plan rule moved: %v", third)
	}
	if !e.coveredTags()[chain.InboundTag] || e.coveredTags()[public.InboundTag] == false {
		t.Fatal("chain and plan nodes must both depend on the applied rules")
	}
	// No plan at all: the chain still gets its rule; a disabled server none.
	m.desired.Egress = nil
	if got := m.effectiveEgress(); len(got.Rules) != 1 || len(got.Outbounds) != 1 {
		t.Fatalf("chain without plan = %+v", got)
	}
	m.desired.Enabled = false
	if m.effectiveEgress() != nil {
		t.Fatal("disabled server keeps chain rules")
	}
}

// Hot apply with the fake Core: the chain upstream is added before the rule
// set that references it; removing the chain removes its outbound; a failed
// apply withholds the chain node instead of serving it through direct.
func TestApplyChainEgressFakeCore(t *testing.T) {
	dir := t.TempDir()
	log := fakeCore(t, dir, true)
	m := NewManager(dir, true)
	m.apiPort, m.epoch = 1, "e1"
	m.applied = appliedState{Epoch: "e1", Inbounds: map[string]string{}}
	chain := chainNode(3, goldenChainUpstreams()[11])
	m.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{chain}}
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	var order []string
	for _, l := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		order = append(order, strings.Fields(l)[0])
	}
	if strings.Join(order, ",") != "lsrules,lso,ado,adrules,rmo" {
		t.Fatalf("chain apply order = %v", order)
	}
	added, _ := os.ReadFile(filepath.Join(dir, "last-ado.json"))
	if !strings.Contains(string(added), `"tag":"flvxc-3-`) || !strings.Contains(string(added), `"protocol":"socks"`) {
		t.Fatalf("chain upstream not added: %s", added)
	}
	rulesRaw, _ := os.ReadFile(filepath.Join(dir, "last-rules.json"))
	var rules struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(rulesRaw, &rules); err != nil || len(rules.Routing.Rules) != 2 || rules.Routing.Rules[0]["ruleTag"] != "flvx-safety" || !strings.HasPrefix(rules.Routing.Rules[1]["outboundTag"].(string), "flvxc-3-") {
		t.Fatalf("chain rules = %s", rulesRaw)
	}
	if m.egress.State != "applied" || chainCoreTagPrefix(m.applied.Outbounds, 3) == "" {
		t.Fatalf("chain apply state %+v %+v", m.egress, m.applied)
	}
	if len(m.withheldTags()) != 0 {
		t.Fatal("applied chain node withheld")
	}
	// Removing the chain node: its outbound becomes stale and is removed.
	coreTag := chainCoreTagPrefix(m.applied.Outbounds, 3)
	b, _ := json.Marshal(map[string]any{"outbounds": []any{map[string]any{"tag": "direct"}, map[string]any{"tag": "block"}, map[string]any{"tag": coreTag}}})
	_ = os.WriteFile(filepath.Join(dir, "lso.json"), b, 0644)
	_ = os.Remove(log)
	m.desired = Desired{Revision: 2, Enabled: true}
	m.egressCache = egressCache{}
	if _, err := m.applyEgress(context.Background(), coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	calls, _ = os.ReadFile(log)
	removed := false
	for _, l := range strings.Split(string(calls), "\n") {
		removed = removed || (strings.HasPrefix(l, "rmo ") && strings.HasSuffix(strings.TrimSpace(l), coreTag))
	}
	if !removed {
		t.Fatalf("stale chain outbound kept: %s", calls)
	}
	// A failed apply (old Core without RoutingService) withholds the chain node.
	dir2 := t.TempDir()
	fakeCore(t, dir2, false)
	m2 := NewManager(dir2, true)
	m2.apiPort, m2.epoch = 1, "e1"
	m2.applied = appliedState{Epoch: "e1", Inbounds: map[string]string{}}
	m2.desired = Desired{Revision: 1, Enabled: true, Nodes: []Node{chain, testNode(t, 1)}}
	if _, err := m2.applyEgress(context.Background(), coreSnapshot{}); err != errRestartRequired {
		t.Fatalf("old Core: %v", err)
	}
	if w := m2.withheldTags(); !w[chain.InboundTag] || w["flvxl-1"] {
		t.Fatalf("withheld = %v; the chain node must stay down, the public node up", w)
	}
}

func TestInstallRefusesLowResources(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, true)
	m.resourceCheck = func(string) error { return errLowResources }
	called := false
	m.coreDownload = func(ctx context.Context, direct, path, expected string, limit int64, mode os.FileMode) error {
		called = true
		return nil
	}
	if err := m.install(context.Background()); err != errLowResources || called {
		t.Fatalf("install with low resources: err=%v downloaded=%v", err, called)
	}
	if err := checkInstallResources(dir); err != nil {
		t.Logf("this machine is below the install minimum: %v", err)
	}
}

var errLowResources = errorString("落地程序安装所需磁盘空间不足")

type errorString string

func (e errorString) Error() string { return string(e) }

// The real Core accepts the loopback chain inbounds and every upstream type.
func TestChainRealCoreAcceptsConfig(t *testing.T) {
	dir := t.TempDir()
	core := realCore(t, dir)
	r := renderedChain(t)
	cfg := baseConfig(freePort(t))
	cfg["inbounds"] = r["inbounds"]
	cfg["outbounds"] = append(cfg["outbounds"].([]any), r["outbounds"].([]any)...)
	cfg["routing"].(map[string]any)["rules"] = r["rules"]
	b, _ := json.MarshalIndent(cfg, "", "  ")
	path := filepath.Join(dir, "chain-test.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(core, "run", "-test", "-config", path).CombinedOutput(); err != nil {
		t.Fatalf("real Core rejected chain config: %v\n%s", err, out)
	}
	t.Logf("real Core accepted %d chain inbounds/outbounds", len(r["outbounds"].([]any)))
}

// Hot add/change/remove of chain nodes keeps the Core epoch (no restart) and
// the loopback inbound is reachable on 127.0.0.1 only.
func TestChainRealCoreHotApply(t *testing.T) {
	dir := t.TempDir()
	realCore(t, dir)
	m := NewManager(dir, true)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	defer func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	}()
	public := testNode(t, 1)
	public.Port = freePort(t)
	c1 := chainNode(2, goldenChainUpstreams()[11])
	c1.Port = freePort(t)
	d := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, Nodes: []Node{public, c1}}
	status, err := m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	epoch := status.Epoch
	if status.Egress == nil || status.Egress.State != "applied" {
		t.Fatalf("egress %+v", status.Egress)
	}
	if rules := apiTags(t, m, "lsrules", "rules", "ruleTag"); strings.Join(rules, ",") != "flvx-safety,flvxr-1" {
		t.Fatalf("rules = %v", rules)
	}
	if outs := apiTags(t, m, "lso", "outbounds", "tag"); countPrefix(outs, "flvxc-2-") != 1 {
		t.Fatalf("outbounds = %v", outs)
	}
	listing := string(mustAPI(t, m, "lsi"))
	if !strings.Contains(listing, c1.InboundTag) || !strings.Contains(listing, public.InboundTag) {
		t.Fatalf("inbounds = %s", listing)
	}
	if !listensOnlyOnLoopback(t, c1.Port) {
		t.Fatal("chain inbound reachable outside loopback")
	}
	// Second chain node + new upstream for the first: hot, same epoch.
	c2 := chainNode(3, goldenChainUpstreams()[12])
	c2.Port = freePort(t)
	c1.Chain = &EgressOutbound{Tag: c1.Chain.Tag, Type: "socks", IPVersion: "system", Server: "203.0.113.99", Port: 1081}
	d.Revision++
	d.Nodes = []Node{public, c1, c2}
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != epoch {
		t.Fatalf("chain change restarted the Core or failed: %v %s/%s", err, status.Epoch, epoch)
	}
	outs := apiTags(t, m, "lso", "outbounds", "tag")
	if countPrefix(outs, "flvxc-2-") != 1 || countPrefix(outs, "flvxc-3-") != 1 {
		t.Fatalf("outbounds after change = %v", outs)
	}
	// Remove both chain nodes: outbounds, rules and inbounds are gone, the
	// public node keeps running in the same Core process.
	d.Revision++
	d.Nodes = []Node{public}
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != epoch {
		t.Fatalf("chain removal: %v", err)
	}
	if outs = apiTags(t, m, "lso", "outbounds", "tag"); countPrefix(outs, "flvxc-") != 0 {
		t.Fatalf("chain outbounds left: %v", outs)
	}
	if rules := apiTags(t, m, "lsrules", "rules", "ruleTag"); strings.Join(rules, ",") != "flvx-safety" {
		t.Fatalf("rules after removal = %v", rules)
	}
	listing = string(mustAPI(t, m, "lsi"))
	if strings.Contains(listing, c1.InboundTag) || strings.Contains(listing, c2.InboundTag) || !strings.Contains(listing, public.InboundTag) {
		t.Fatalf("inbounds after removal = %s", listing)
	}
	t.Logf("chain add/change/remove kept epoch %s", epoch)
}

// listensOnlyOnLoopback dials the port on loopback (must work) and on every
// non-loopback local address (must be refused).
func listensOnlyOnLoopback(t *testing.T, port int) bool {
	t.Helper()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	var conn interface{ Close() error }
	for i := 0; i < 50; i++ {
		c, err := dialTimeout(addr)
		if err == nil {
			conn = c
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if conn == nil {
		t.Fatalf("chain inbound not reachable on %s", addr)
	}
	conn.Close()
	for _, ip := range localNonLoopback() {
		if c, err := dialTimeout(joinHostPort(ip, port)); err == nil {
			c.Close()
			return false
		}
	}
	return true
}

func dialTimeout(addr string) (net.Conn, error) { return net.DialTimeout("tcp", addr, time.Second) }

func joinHostPort(ip string, port int) string { return net.JoinHostPort(ip, strconv.Itoa(port)) }

func localNonLoopback() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipNet, ok := a.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && !ipNet.IP.IsLinkLocalUnicast() {
			out = append(out, ipNet.IP.String())
		}
	}
	return out
}
