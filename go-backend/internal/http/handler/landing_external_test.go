package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"go-backend/internal/cnlanding"
	"go-backend/internal/landingcustom"
	"go-backend/internal/landingegress"
	"go-backend/internal/landingsub"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 177 「外部节点」 / 「链式代理」 handler tests.

const (
	extReality = "vless://4b2f1c84-1f7e-4f3b-9d4c-2f0f6b6a1c11@198.51.124.2:443?type=tcp&security=reality&sni=www.example.com&pbk=8eDxL1uR4ob8BdjX1sY7k2Xz4XlG0bVvUj3sZ0kq8Uo&sid=ab12&fp=chrome&flow=xtls-rprx-vision#HK-Home"
	extVMess   = "vmess://eyJ2IjoiMiIsInBzIjoiU0cgVk1lc3MiLCJhZGQiOiIxOTguNTEuMTI0LjMiLCJwb3J0Ijo0NDMsImlkIjoiNGIyZjFjODQtMWY3ZS00ZjNiLTlkNGMtMmYwZjZiNmExYzEyIiwiYWlkIjowLCJuZXQiOiJ0Y3AiLCJ0bHMiOiJ0bHMiLCJzbmkiOiJ2bS5leGFtcGxlLm5ldCJ9"
)

type probeCall struct {
	node int64
	ip   string
	port int
}

func extEnv(t *testing.T) (*flowTestEnv, *layerAgent, *[]probeCall) {
	t.Helper()
	e, agent := layersEnv(t)
	// The per-user mutation rate limit is tested on its own; these tests
	// drive many adds of one user.
	e.h.SetMutationRateLimitEnabled(false)
	oldResolver, oldProbe, oldFetcher := chainResolver, chainProbeNative, subFetcher
	resolver := fakeChainResolver{"home.example.net": {"198.51.124.9"}, "inside.example.net": {"198.51.124.9", "10.0.0.7"}}
	chainResolver = resolver
	e.h.cnLandingChecker = cnlanding.New(resolver)
	var mu sync.Mutex
	calls := &[]probeCall{}
	probeOK := true
	chainProbeNative = func(_ *Handler, nodeID int64, ip string, port int) (bool, int, string) {
		mu.Lock()
		defer mu.Unlock()
		*calls = append(*calls, probeCall{nodeID, ip, port})
		if !probeOK || port == 9999 {
			return false, 0, "connection refused"
		}
		return true, 38, ""
	}
	t.Cleanup(func() { chainResolver, chainProbeNative, subFetcher = oldResolver, oldProbe, oldFetcher })
	return e, agent, calls
}

func extCreate(t *testing.T, e *flowTestEnv, uid int64, role int, link string, extra map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{"link": link}
	for k, v := range extra {
		body[k] = v
	}
	out := layerCall(t, e.h, "/api/v1/landing/external/create", body, uid, role)
	if out.Code != 0 {
		t.Fatalf("create %s: %+v", link, out)
	}
	return dataMap(t, out)
}

func socksLink(port int) string { return fmt.Sprintf("socks5://u:p@198.51.124.4:%d#S%d", port, port) }

// Ownership: users see and use only their own external nodes; admins act
// for the node owner; secrets never leave the panel.
func TestExternalOwnershipAndSecrets(t *testing.T) {
	e, _, _ := extEnv(t)
	item := extCreate(t, e, 2, 1, extReality, nil)
	id := int64(item["id"].(float64))
	if item["name"] != "HK-Home" || item["summary"] != "VLESS Reality · 198.51.124.2:443" || item["sharedOk"] != true {
		t.Fatalf("item %+v", item)
	}
	list := layerCall(t, e.h, "/api/v1/landing/external/list", map[string]any{}, 2, 1)
	raw, _ := json.Marshal(list)
	for _, secret := range []string{"4b2f1c84", "8eDxL1uR4ob8", "ab12", "upstream"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("secret %q in list response: %s", secret, raw)
		}
	}
	if other := dataMap(t, layerCall(t, e.h, "/api/v1/landing/external/list", map[string]any{"userId": 2}, 3, 1)); other["count"].(float64) != 0 {
		t.Fatalf("user 3 sees user 2's nodes: %+v", other)
	}
	for _, c := range []struct {
		path string
		body map[string]any
		uid  int64
		want string
	}{
		{"external/rename", map[string]any{"id": id, "name": "x"}, 3, "外部节点不存在"},
		{"external/delete", map[string]any{"id": id}, 3, "外部节点不存在"},
		{"external/rename", map[string]any{"id": id, "name": "x", "userId": 2}, 3, "外部节点不存在"},
		{"node/chain/save", map[string]any{"id": 2, "externalId": id}, 3, "外部节点不存在"},
		{"node/chain/probe", map[string]any{"id": 2, "externalId": id}, 3, "外部节点不存在"},
		{"node/chain/save", map[string]any{"id": 1, "externalId": id}, 3, "无权访问"},
		{"node/chain/save", map[string]any{"id": 2, "externalId": id}, 1, "外部节点不存在"}, // admin: the ext must be the node owner's
	} {
		role := 1
		if c.uid == 1 {
			role = 0
		}
		out := layerCall(t, e.h, "/api/v1/landing/"+c.path, c.body, c.uid, role)
		if out.Code == 0 || !strings.Contains(out.Msg, c.want) {
			t.Fatalf("%s as %d: %+v (want %q)", c.path, c.uid, out, c.want)
		}
	}
	if n, _ := e.r.GetLandingNode(2); n.ChainExternalID != 0 {
		t.Fatal("foreign chain stored")
	}
	// Admin acting for user 2: sees the list, may chain user 2's node.
	if adm := dataMap(t, layerCall(t, e.h, "/api/v1/landing/external/list", map[string]any{"userId": 2}, 1, 0)); adm["count"].(float64) != 1 {
		t.Fatalf("admin view %+v", adm)
	}
	if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 1, "externalId": id}, 1, 0); out.Code != 0 {
		t.Fatalf("admin chain save: %+v", out)
	}
}

func TestExternalCapDuplicatesAndGuard(t *testing.T) {
	e, _, _ := extEnv(t)
	for p := 1; p <= model.MaxExternalNodesPerUser; p++ {
		extCreate(t, e, 2, 1, socksLink(1000+p), nil)
	}
	if out := layerCall(t, e.h, "/api/v1/landing/external/create", map[string]any{"link": socksLink(2000)}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "最多 20 个") {
		t.Fatalf("cap: %+v", out)
	}
	if out := layerCall(t, e.h, "/api/v1/landing/external/create", map[string]any{"link": socksLink(1001)}, 3, 1); out.Code != 0 {
		t.Fatalf("other user blocked by user 2's cap: %+v", out)
	}
	if out := layerCall(t, e.h, "/api/v1/landing/external/create", map[string]any{"link": strings.Replace(socksLink(1001), "#S1001", "#again", 1)}, 3, 1); out.Code == 0 || !strings.Contains(out.Msg, "已经添加过") {
		t.Fatalf("duplicate: %+v", out)
	}
	// Admins are not capped.
	for p := 1; p <= model.MaxExternalNodesPerUser+1; p++ {
		extCreate(t, e, 1, 0, socksLink(3000+p), nil)
	}
	// Parallel adds never pass the cap (user row locked in the tx).
	e.addUser(5)
	var wg sync.WaitGroup
	for p := 0; p < 30; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			layerCall(t, e.h, "/api/v1/landing/external/create", map[string]any{"link": socksLink(4000 + p)}, 5, 1)
		}(p)
	}
	wg.Wait()
	if n, _ := e.r.ListLandingExternalNodes(5); len(n) != model.MaxExternalNodesPerUser {
		t.Fatalf("parallel adds stored %d", len(n))
	}
	// Address guard: private / metadata / CGNAT / our landing / FLVX node /
	// a domain with any internal answer.
	e.exec("UPDATE node SET server_ip = '198.51.124.200' WHERE id = 2")
	for link, want := range map[string]string{
		"socks5://10.0.0.1:1080":                       "内网",
		"socks5://127.0.0.1:1080":                      "内网",
		"socks5://169.254.169.254:80":                  "内网",
		"socks5://100.64.1.1:1080":                     "内网",
		"socks5://203.0.113.2:1080":                    "FLVX 落地服务器",
		"socks5://198.51.124.200:1080":                 "FLVX 节点",
		"socks5://inside.example.net:1080":             "",
		"tuic://u:p@198.51.124.5:443":                  "TUIC",
		"trojan://pw@198.51.124.5:443?allowInsecure=1": "跳过证书验证",
	} {
		out := layerCall(t, e.h, "/api/v1/landing/external/create", map[string]any{"link": link}, 3, 1)
		if out.Code == 0 || (want != "" && !strings.Contains(out.Msg, want)) {
			t.Fatalf("%s: %+v (want %q)", link, out, want)
		}
		if out := layerCall(t, e.h, "/api/v1/landing/external/parse", map[string]any{"link": link}, 3, 1); out.Code == 0 {
			t.Fatalf("parse accepted %s", link)
		}
	}
	if out := layerCall(t, e.h, "/api/v1/landing/external/parse", map[string]any{"link": "socks5://home.example.net:1080#Home"}, 3, 1); out.Code != 0 || dataMap(t, out)["resolved"] != "198.51.124.9" {
		t.Fatalf("parse domain: %+v", out)
	}
}

// Saving a chain sends one chain outbound + a catch-all rule after the block
// rules of that node; switching back to 直连 removes it hot (same sync path);
// an unreachable external node is refused before anything changes.
func TestChainSaveAppliesPlanAndSwitchesBack(t *testing.T) {
	e, agent, probes := extEnv(t)
	item := extCreate(t, e, 2, 1, extVMess, nil)
	id := int64(item["id"].(float64))
	bad := extCreate(t, e, 2, 1, "socks5://198.51.124.4:9999#down", nil)
	out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 1, "externalId": int64(bad["id"].(float64))}, 2, 1)
	if out.Code == 0 || !strings.Contains(out.Msg, "连不上外部节点") {
		t.Fatalf("unreachable accepted: %+v", out)
	}
	if n, _ := e.r.GetLandingNode(1); n.ChainExternalID != 0 {
		t.Fatal("failed probe still saved the chain")
	}
	out = layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 1, "externalId": id}, 2, 1)
	if out.Code != 0 {
		t.Fatalf("save: %+v", out)
	}
	data := dataMap(t, out)
	node := data["node"].(map[string]any)
	chain, _ := node["chain"].(map[string]any)
	if chain == nil || chain["name"] != "SG VMess" || chain["protocolLabel"] != "VMess TLS" || chain["state"] != "applied" || node["egressLabel"] != landingegress.ChainRuleName {
		t.Fatalf("node view %+v", node)
	}
	if p := data["probe"].(map[string]any); p["ok"] != true || p["latencyMs"].(float64) != 38 {
		t.Fatalf("probe %+v", p)
	}
	if last := (*probes)[len(*probes)-1]; last.node != 2 || last.ip != "198.51.124.3" || last.port != 443 {
		t.Fatalf("probe call %+v", last)
	}
	plan := agent.lastOf(2)
	if plan == nil {
		t.Fatal("no plan sent")
	}
	var chainTag string
	for _, o := range plan.Outbounds {
		if strings.HasPrefix(o.Tag, "flvxo-x") {
			chainTag = o.Tag
			if o.Type != "vmess" || o.Security != "tls" || o.TLS == nil || o.TLS.ServerName != "vm.example.net" || o.UUID == "" {
				t.Fatalf("chain outbound %+v", o)
			}
		}
	}
	last := plan.Rules[len(plan.Rules)-1]
	if chainTag == "" || last.Outbound != chainTag || strings.Join(last.InboundTags, ",") != "flvxl-1" || last.Builtin != "" {
		t.Fatalf("chain rule %+v outbounds %+v", last, plan.Outbounds)
	}
	for _, r := range plan.Rules {
		for _, tag := range r.InboundTags {
			if tag == "flvxl-1" && r.Outbound != landingegress.ActionBlock && r.Outbound != chainTag {
				t.Fatalf("non-block rule %+v covers the chained node", r)
			}
		}
	}
	// The other user's node is untouched.
	for _, r := range plan.Rules {
		if r.Outbound == chainTag && strings.Contains(strings.Join(r.InboundTags, ","), "flvxl-2") {
			t.Fatal("chain leaked to node 2")
		}
	}
	// Transfer is refused while chained.
	if _, err := e.r.TransferLandingNode(1, 3); err == nil || !strings.Contains(err.Error(), "链式代理") {
		t.Fatalf("transfer: %v", err)
	}
	// Delete in use → refused with the node name; back to 直连 → plan has no
	// chain outbound and the delete goes through.
	if out := layerCall(t, e.h, "/api/v1/landing/external/delete", map[string]any{"id": id}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "「node」") {
		t.Fatalf("delete in use: %+v", out)
	}
	if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 1, "externalId": 0}, 2, 1); out.Code != 0 {
		t.Fatalf("back to direct: %+v", out)
	}
	if p := agent.lastOf(2); p != nil {
		for _, o := range p.Outbounds {
			if strings.HasPrefix(o.Tag, "flvxo-x") {
				t.Fatalf("chain outbound left after 直连: %+v", p.Outbounds)
			}
		}
	}
	if out := layerCall(t, e.h, "/api/v1/landing/external/delete", map[string]any{"id": id}, 2, 1); out.Code != 0 {
		t.Fatalf("delete unused: %+v", out)
	}
	// Old agent (no egress plans): chain refused; an already chained node is
	// sent disabled rather than direct.
	item = extCreate(t, e, 2, 1, extReality, nil)
	if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 1, "externalId": item["id"]}, 2, 1); out.Code != 0 {
		t.Fatalf("save reality: %+v", out)
	}
	e.exec("UPDATE node SET landing_egress = 0 WHERE id = 2")
	if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 2, "externalId": 0}, 3, 1); out.Code != 0 {
		t.Fatal(out)
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	agent.mu.Lock()
	payload := agent.lastFor[2]
	agent.mu.Unlock()
	for _, n := range payload["nodes"].([]map[string]interface{}) {
		if n["id"].(int64) == 1 && n["enabled"] != false {
			t.Fatalf("chained node enabled on an old agent: %+v", n)
		}
		if n["id"].(int64) == 2 && n["enabled"] != true {
			t.Fatalf("plain node disabled: %+v", n)
		}
	}
	list := layerCall(t, e.h, "/api/v1/landing/node/list", map[string]any{}, 2, 1)
	var nodes []map[string]any
	b, _ := json.Marshal(list.Data)
	_ = json.Unmarshal(b, &nodes)
	if len(nodes) != 1 || nodes[0]["chain"].(map[string]any)["state"] != "error" || nodes[0]["chainSupport"].(map[string]any)["supported"] != false {
		t.Fatalf("old agent chain view %+v", nodes)
	}
}

// Deleting a user deletes their external nodes.
func TestExternalUserDeleteCleanup(t *testing.T) {
	e, _, _ := extEnv(t)
	item := extCreate(t, e, 2, 1, extReality, nil)
	if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 1, "externalId": item["id"]}, 2, 1); out.Code != 0 {
		t.Fatal(out)
	}
	extCreate(t, e, 3, 1, extReality, nil)
	if err := e.r.DeleteUserCascade(2); err != nil {
		t.Fatal(err)
	}
	var count int64
	e.r.DB().Model(&model.LandingExternalNode{}).Where("user_id = ?", 2).Count(&count)
	if count != 0 {
		t.Fatalf("%d external nodes left", count)
	}
	e.r.DB().Model(&model.LandingExternalNode{}).Count(&count)
	if count != 1 {
		t.Fatalf("other users' nodes touched: %d", count)
	}
}

// Shared servers: only the Helper whitelist; gated on the share's egress
// capability; a withdrawn capability turns chained nodes off.
func TestChainSharedGating(t *testing.T) {
	e, _, probes := extEnv(t)
	caps := landingcustom.Share{ID: 7, Name: "shared", Status: "active", PortMin: 30000, PortMax: 30010, Protocols: []string{"ss2022"}, Hosts: landingcustom.Hosts{IPv4: "203.0.113.70"}}
	raw, _ := json.Marshal(caps)
	for _, v := range []interface{}{&model.LandingServer{ID: 5, Source: "shared", RemoteServerID: 7, Name: "妙妙屋", PublicHost: "203.0.113.70", PortMin: 30000, PortMax: 30010, FlowType: 2, TrafficRatio: 1, Status: "active", SharedCaps: string(raw)}, &model.LandingNode{ID: 5, UserID: 2, LandingServerID: 5, Name: "shared-node", Protocol: "ss2022", Port: 30005, InboundTag: "flvxl-5", Status: "active"}} {
		if err := e.r.DB().Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	reality := extCreate(t, e, 2, 1, extReality, nil)
	vmess := extCreate(t, e, 2, 1, extVMess, nil)
	if vmess["sharedOk"] != false || !strings.Contains(vmess["sharedReason"].(string), "不支持") {
		t.Fatalf("vmess shared flag %+v", vmess)
	}
	if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 5, "externalId": reality["id"]}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "暂不支持自定义出站") {
		t.Fatalf("share without egress: %+v", out)
	}
	caps.Egress = &landingcustom.ShareEgress{Versions: []int{1}, Allowed: false}
	raw, _ = json.Marshal(caps)
	e.exec("UPDATE landing_server SET shared_caps=? WHERE id=5", string(raw))
	if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 5, "externalId": reality["id"]}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "未允许") {
		t.Fatalf("disallowed share: %+v", out)
	}
	caps.Egress.Allowed = true
	raw, _ = json.Marshal(caps)
	e.exec("UPDATE landing_server SET shared_caps=? WHERE id=5", string(raw))
	if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 5, "externalId": vmess["id"]}, 2, 1); out.Code == 0 || !strings.Contains(out.Msg, "此服务器不支持该协议") {
		t.Fatalf("vmess on shared: %+v", out)
	}
	before := len(*probes)
	probe := layerCall(t, e.h, "/api/v1/landing/node/chain/probe", map[string]any{"id": 5, "externalId": reality["id"]}, 2, 1)
	if probe.Code != 0 || dataMap(t, probe)["probed"] != false || !strings.Contains(dataMap(t, probe)["message"].(string), "无法检测") || len(*probes) != before {
		t.Fatalf("shared probe %+v", probe)
	}
	if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 5, "externalId": reality["id"]}, 2, 1); out.Code != 0 {
		t.Fatalf("reality on shared: %+v", out)
	}
	plan, _, err := e.h.compileLandingEgress(5)
	if err != nil || plan == nil {
		t.Fatalf("shared plan %v %v", plan, err)
	}
	var out *landingegress.PlanOutbound
	for i := range plan.Outbounds {
		if strings.HasPrefix(plan.Outbounds[i].Tag, "flvxo-x") {
			out = &plan.Outbounds[i]
		}
	}
	// The Helper decodes the plan-107 form only: no transport fields.
	if out == nil || out.Type != "vless" || out.Reality == nil || out.Network != "" || out.Security != "" || out.TLS != nil {
		t.Fatalf("shared chain outbound %+v", out)
	}
	b, _ := json.Marshal(out)
	for _, k := range []string{"network", "security", "tls", "ws", "grpc", "cipher", "obfs"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Fatalf("shared outbound carries %q: %s", k, b)
		}
	}
	writeHelperGolden(t, plan)
	desired := func() landingcustom.Node {
		d, err := e.r.SharedLandingDesired(5, plan)
		if err != nil {
			t.Fatal(err)
		}
		return d.Nodes[0]
	}
	if !desired().Enabled {
		t.Fatal("chained shared node disabled while supported")
	}
	caps.Egress.Allowed = false
	raw, _ = json.Marshal(caps)
	e.exec("UPDATE landing_server SET shared_caps=? WHERE id=5", string(raw))
	if desired().Enabled {
		t.Fatal("chained shared node stays enabled after the capability was withdrawn (would leave directly)")
	}
}

// writeHelperGolden stores the shared plan for the Helper-validator harness
// (plan 177 lab; FLVX_HELPER_GOLDEN=path).
func writeHelperGolden(t *testing.T, plan *landingegress.Plan) {
	t.Helper()
	if path := os.Getenv("FLVX_HELPER_GOLDEN"); path != "" {
		b, _ := json.MarshalIndent(plan, "", "  ")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Subscription import: fetch once (cache by fetch id, per user, bounded),
// import picked indexes, duplicates / cap / unsupported reported; another
// user cannot use the fetch id; fetches are rate limited.
func TestExternalSubscriptionImport(t *testing.T) {
	e, _, _ := extEnv(t)
	lines := []string{extReality, extVMess, "ss://YWVzLTI1Ni1nY206eA@1.1.1.1:1#剩余流量 100G", "tuic://u:p@198.51.124.5:443#TUIC", "socks5://10.0.0.5:1080#lan", "socks5://198.51.124.200:1080#our-node"}
	for p := 0; p < 25; p++ {
		lines = append(lines, socksLink(5000+p))
	}
	body := base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	defer srv.Close()
	subFetcher = func(h *Handler) *landingsub.Fetcher {
		f := &landingsub.Fetcher{Resolver: fakeChainResolver{"sub.example.net": {"198.51.124.30"}}, Forbidden: h.chainForbidden()}
		f.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, srv.Listener.Addr().String())
		}
		return f
	}
	extCreate(t, e, 2, 1, extReality, nil) // already there → duplicate
	e.exec("UPDATE node SET server_ip = '198.51.124.200' WHERE id = 2")
	url := "http://sub.example.net/api/sub?token=x"
	out := layerCall(t, e.h, "/api/v1/landing/external/sub/fetch", map[string]any{"url": url}, 2, 1)
	if out.Code != 0 {
		t.Fatalf("fetch: %+v", out)
	}
	data := dataMap(t, out)
	items := data["items"].([]any)
	// 剩余流量 and the LAN address are dropped as info / placeholder entries.
	if data["info"].(float64) != 2 || len(items) != 29 {
		t.Fatalf("fetch result info=%v items=%d", data["info"], len(items))
	}
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "4b2f1c84") || strings.Contains(string(raw), "token=x") {
		t.Fatalf("secret or url echoed: %s", raw)
	}
	byName := map[string]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if byName["HK-Home"]["duplicate"] != "HK-Home" || byName["TUIC"]["supported"] != false || byName["our-node"]["supported"] != false || byName["SG VMess"]["sharedOk"] != false {
		t.Fatalf("flags %+v", byName)
	}
	fetchID := data["fetchId"].(string)
	all := []int{}
	for i := range items {
		all = append(all, i)
	}
	if out := layerCall(t, e.h, "/api/v1/landing/external/sub/import", map[string]any{"fetchId": fetchID, "indexes": all}, 3, 1); out.Code == 0 || !strings.Contains(out.Msg, "过期") {
		t.Fatalf("other user used the fetch id: %+v", out)
	}
	out = layerCall(t, e.h, "/api/v1/landing/external/sub/import", map[string]any{"fetchId": fetchID, "indexes": all}, 2, 1)
	if out.Code != 0 {
		t.Fatalf("import: %+v", out)
	}
	res := dataMap(t, out)
	if res["imported"].(float64) != 19 || len(res["duplicates"].([]any)) != 1 || res["overCap"].(float64) == 0 {
		t.Fatalf("import result %+v", res)
	}
	if n, _ := e.r.ListLandingExternalNodes(2); len(n) != model.MaxExternalNodesPerUser {
		t.Fatalf("stored %d", len(n))
	}
	// Re-using a fetch after a successful import is refused (cache dropped).
	if out := layerCall(t, e.h, "/api/v1/landing/external/sub/import", map[string]any{"fetchId": fetchID, "indexes": []int{0}}, 2, 1); out.Code == 0 {
		t.Fatal("fetch id reused")
	}
	// Rate limit: 5 fetches a minute per user (one used above).
	limited := false
	for i := 0; i < 6; i++ {
		if out := layerCall(t, e.h, "/api/v1/landing/external/sub/fetch", map[string]any{"url": url}, 2, 1); out.Code != 0 && strings.Contains(out.Msg, "太频繁") {
			limited = true
		}
	}
	if !limited {
		t.Fatal("fetch not rate limited")
	}
	// Bounded cache: a user keeps at most 3 fetches.
	e.h.external.mu.Lock()
	mine := 0
	for _, f := range e.h.external.fetches {
		if f.actor == 2 {
			mine++
		}
	}
	e.h.external.mu.Unlock()
	if mine > subCachePerUser {
		t.Fatalf("user holds %d cached fetches", mine)
	}
	// SSRF guard is wired: a private subscription address is refused.
	subFetcher = func(h *Handler) *landingsub.Fetcher {
		return &landingsub.Fetcher{Resolver: chainResolver, Forbidden: h.chainForbidden()}
	}
	e.h.external.fetchRL.Refund("3")
	if out := layerCall(t, e.h, "/api/v1/landing/external/sub/fetch", map[string]any{"url": "http://10.0.0.1/sub"}, 3, 1); out.Code == 0 || !strings.Contains(out.Msg, "内网") {
		t.Fatalf("private sub url: %+v", out)
	}
}

// Overflow: more distinct external nodes than the server's outbound room is
// refused at save time without touching other nodes.
func TestChainSaveRefusesWhenServerFull(t *testing.T) {
	e, _, _ := extEnv(t)
	for i := 0; i < landingegress.MaxPlanOutbounds; i++ {
		id := int64(100 + i)
		if err := e.r.DB().Create(&model.LandingNode{ID: id, UserID: 2, LandingServerID: 1, Name: fmt.Sprintf("n%d", i), Protocol: "ss2022", Port: 31000 + i, InboundTag: fmt.Sprintf("flvxl-%d", id), Status: "active"}).Error; err != nil {
			t.Fatal(err)
		}
		item := extCreate(t, e, 2, 1, socksLink(6000+i), nil)
		out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": id, "externalId": item["id"]}, 2, 1)
		if out.Code != 0 {
			t.Fatalf("chain %d: %+v", i, out)
		}
	}
	item := extCreate(t, e, 2, 1, socksLink(7000), nil)
	out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 1, "externalId": item["id"]}, 2, 1)
	if out.Code == 0 || !strings.Contains(out.Msg, "出站已满") {
		t.Fatalf("overflow accepted: %+v", out)
	}
	// Re-using an external node already on the server needs no room.
	used, _ := e.r.ListLandingExternalNodes(2)
	if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": 1, "externalId": used[0].ID}, 2, 1); out.Code != 0 {
		t.Fatalf("shared outbound refused: %+v", out)
	}
}

// Every protocol the Helper takes, chained on one shared server, compiles to
// the plan-107 field form; FLVX_HELPER_GOLDEN writes the plan for the
// Helper-validator harness (the mmwx validator itself lives in mmwx-custom).
func TestChainSharedHelperGolden(t *testing.T) {
	e, _, _ := extEnv(t)
	caps := landingcustom.Share{ID: 7, Name: "shared", Status: "active", PortMin: 30000, PortMax: 30010, Protocols: []string{"ss2022"}, Hosts: landingcustom.Hosts{IPv4: "203.0.113.70"}, Egress: &landingcustom.ShareEgress{Versions: []int{1}, Allowed: true}}
	raw, _ := json.Marshal(caps)
	if err := e.r.DB().Create(&model.LandingServer{ID: 5, Source: "shared", RemoteServerID: 7, Name: "妙妙屋", PublicHost: "203.0.113.70", PortMin: 30000, PortMax: 30010, FlowType: 2, TrafficRatio: 1, Status: "active", SharedCaps: string(raw)}).Error; err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	links := []string{
		"socks5://u:p@198.51.124.4:1080#socks",
		"http://u:p@198.51.124.4:3128#http",
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw")) + "@198.51.124.3:8388#ss",
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("2022-blake3-aes-128-gcm:"+key)) + "@198.51.124.3:8389#ss2022",
		extReality,
	}
	tags := []string{}
	for i, link := range links {
		id := int64(50 + i)
		if err := e.r.DB().Create(&model.LandingNode{ID: id, UserID: 2, LandingServerID: 5, Name: fmt.Sprintf("s%d", i), Protocol: "ss2022", Port: 30000 + i, InboundTag: fmt.Sprintf("flvxl-%d", id), Status: "active"}).Error; err != nil {
			t.Fatal(err)
		}
		item := extCreate(t, e, 2, 1, link, nil)
		if item["sharedOk"] != true {
			t.Fatalf("%s not shared-ok: %+v", link, item)
		}
		if out := layerCall(t, e.h, "/api/v1/landing/node/chain/save", map[string]any{"id": id, "externalId": item["id"]}, 2, 1); out.Code != 0 {
			t.Fatalf("%s: %+v", link, out)
		}
		tags = append(tags, fmt.Sprintf("flvxl-%d", id))
	}
	plan, _, err := e.h.compileLandingEgress(5)
	if err != nil || plan == nil {
		t.Fatal(plan, err)
	}
	chains := 0
	for _, o := range plan.Outbounds {
		if strings.HasPrefix(o.Tag, "flvxo-x") {
			chains++
			if o.Network != "" || o.Security != "" || o.TLS != nil || o.WS != nil || o.GRPC != nil || o.Cipher != "" || o.Obfs != "" {
				t.Fatalf("transport fields on a shared outbound: %+v", o)
			}
		}
	}
	if chains != len(links) {
		t.Fatalf("%d chain outbounds", chains)
	}
	if path := os.Getenv("FLVX_HELPER_GOLDEN"); path != "" {
		b, _ := json.MarshalIndent(map[string]any{"plan": plan, "node_tags": tags}, "", "  ")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Repository behaviour on both dialects: migration, cap under parallel adds
// (user row lock), duplicates, chain set / in-use delete refusal, transfer
// refusal, user delete cleanup, egress data loading.
func TestExternalNodesBothDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := sharedLandingTestRepo(t, dialect)
			now := int64(1793440000000)
			for _, id := range []int64{20, 30} {
				if err := r.DB().Create(&model.User{ID: id, User: fmt.Sprintf("u%d", id), Pwd: "x", RoleID: 1, ExpTime: now + 86400000, Flow: 100, FlowResetTime: 1, Num: 10, CreatedTime: now, Status: 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := r.DB().Create(&model.LandingServer{ID: 9, Source: "native", NodeID: 0, Name: "s", PublicHost: "203.0.113.9", Status: "active"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.DB().Create(&model.LandingNode{ID: 90, UserID: 20, LandingServerID: 9, Name: "n90", Protocol: "ss2022", Port: 30090, InboundTag: "flvxl-90", Status: "active", CreatedTime: now, UpdatedTime: now}).Error; err != nil {
				t.Fatal(err)
			}
			item := func(i int) model.LandingExternalNode {
				return model.LandingExternalNode{Name: fmt.Sprintf("e%d", i), Protocol: "socks", Host: "198.51.124.4", Port: 1000 + i, Summary: "SOCKS5", Upstream: `{"type":"socks"}`, UpstreamHash: fmt.Sprintf("h%d", i)}
			}
			var wg sync.WaitGroup
			for i := 0; i < 30; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, _ = r.CreateLandingExternalNodes(20, []model.LandingExternalNode{item(i)}, model.MaxExternalNodesPerUser)
				}(i)
			}
			wg.Wait()
			list, err := r.ListLandingExternalNodes(20)
			if err != nil || len(list) != model.MaxExternalNodesPerUser {
				t.Fatalf("parallel adds: %d %v", len(list), err)
			}
			res, err := r.CreateLandingExternalNodes(30, []model.LandingExternalNode{item(1), item(1), item(2)}, 0)
			if err != nil || len(res.Created) != 2 || len(res.Duplicates) != 1 {
				t.Fatalf("duplicates in batch: %+v %v", res, err)
			}
			if _, err := r.SetLandingNodeChain(90, res.Created[0].ID); err == nil {
				t.Fatal("chained another user's external node")
			}
			if _, err := r.SetLandingNodeChain(90, list[0].ID); err != nil {
				t.Fatal(err)
			}
			uses, err := r.DeleteLandingExternalNode(list[0].ID, 20)
			if err != nil || len(uses) != 1 || uses[0].Name != "n90" {
				t.Fatalf("in-use delete: %+v %v", uses, err)
			}
			if _, err := r.DeleteLandingExternalNode(list[0].ID, 30); err != repo.ErrExternalNodeNotFound {
				t.Fatalf("foreign delete: %v", err)
			}
			if m, _ := r.LandingExternalUses(20); len(m[list[0].ID]) != 1 {
				t.Fatalf("uses %+v", m)
			}
			d, err := r.LandingEgressData(9)
			if err != nil || d.Externals[list[0].ID].Name != list[0].Name {
				t.Fatalf("egress data externals %+v %v", d, err)
			}
			if _, err := r.TransferLandingNode(90, 30); err == nil || !strings.Contains(err.Error(), "链式代理") {
				t.Fatalf("transfer: %v", err)
			}
			if err := r.DeleteUserCascade(20); err != nil {
				t.Fatal(err)
			}
			var left int64
			r.DB().Model(&model.LandingExternalNode{}).Where("user_id = ?", 20).Count(&left)
			if left != 0 {
				t.Fatalf("%d external nodes left after user delete", left)
			}
			if rest, _ := r.ListLandingExternalNodes(30); len(rest) != 2 {
				t.Fatalf("other user's nodes touched: %d", len(rest))
			}
		})
	}
}
