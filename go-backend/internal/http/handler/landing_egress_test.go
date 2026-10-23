package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

func egressCall(t *testing.T, h *Handler, path string, body any, role int) response.R {
	t.Helper()
	mux := http.NewServeMux()
	h.registerLanding(mux)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: role}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var out response.R
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %v %s", path, err, w.Body.String())
	}
	return out
}

func dataMap(t *testing.T, r response.R) map[string]any {
	t.Helper()
	b, _ := json.Marshal(r.Data)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

// offlineDNS makes hostname checks deterministic.
func offlineDNS(t *testing.T, answers map[string]string) {
	t.Helper()
	old := egressLookup
	egressLookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		if a, ok := answers[host]; ok {
			return []netip.Addr{netip.MustParseAddr(a)}, nil
		}
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() { egressLookup = old })
}

type capturedApply struct {
	payloads []map[string]interface{}
}

// egressAgent answers ApplyLandingDesired like an egress-capable agent.
func egressAgent(t *testing.T, e *flowTestEnv, ready bool) *capturedApply {
	c := &capturedApply{}
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		if method != "ApplyLandingDesired" {
			t.Fatalf("unexpected command %s", method)
		}
		payload := data.(map[string]interface{})
		c.payloads = append(c.payloads, payload)
		state := "none"
		if _, ok := payload["egress"]; ok {
			state = "applied"
		}
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"revision": payload["revision"], "epoch": "1", "state": "running", "nodes": []repo.LandingCounter{}, "egress": map[string]any{"state": state, "ready": ready}}}, nil
	}
	return c
}

func serverSaveBody(t *testing.T, e *flowTestEnv, profileID int64, confirm bool) map[string]any {
	t.Helper()
	s, err := e.r.GetLandingServer(1)
	if err != nil || s == nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s)
	body := map[string]any{}
	_ = json.Unmarshal(b, &body)
	body["protocols"] = []string{"ss2022", "vless_reality_vision"}
	body["realityServerNames"] = []string{"www.lovelive-anime.jp"}
	body["userGroupIds"] = []int64{}
	body["routeProfileId"] = profileID
	body["egressRestartConfirmed"] = confirm
	return body
}

func TestLandingEgressAdminOnlyAndValidation(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	offlineDNS(t, map[string]string{"evil.example.com": "10.1.2.3", "good.example.com": "203.0.113.30"})
	for _, path := range []string{"overview", "outbound/save", "outbound/delete", "outbound/parse", "profile/save", "profile/delete", "whitelist/save", "warp/register"} {
		if out := egressCall(t, e.h, "/api/v1/landing/egress/"+path, map[string]any{}, 1); out.Code != 403 {
			t.Fatalf("%s for a normal user: %+v", path, out)
		}
	}
	for name, settings := range map[string]map[string]any{
		"private literal":  {"protocol": "socks", "server": "192.168.1.1", "port": 1080},
		"metadata literal": {"protocol": "http", "server": "169.254.169.254", "port": 80},
		"private dns":      {"protocol": "socks", "server": "evil.example.com", "port": 1080},
	} {
		out := egressCall(t, e.h, "/api/v1/landing/egress/outbound/save", map[string]any{"name": name, "type": "proxy", "settings": settings}, 0)
		if out.Code == 0 {
			t.Fatalf("%s upstream accepted", name)
		}
	}
	out := egressCall(t, e.h, "/api/v1/landing/egress/outbound/save", map[string]any{"name": "hk", "type": "proxy", "ipVersion": "force_ipv4", "settings": map[string]any{"protocol": "socks", "server": "good.example.com", "port": 1080}}, 0)
	if out.Code != 0 {
		t.Fatalf("public upstream rejected: %+v", out)
	}
	proxyID := int64(dataMap(t, out)["id"].(float64))
	out = egressCall(t, e.h, "/api/v1/landing/egress/outbound/parse", map[string]any{"link": "ss://2022-blake3-aes-128-gcm:AAAAAAAAAAAAAAAAAAAAAA%3D%3D@203.0.113.40:8388#imported"}, 0)
	if out.Code != 0 || dataMap(t, out)["name"] != "imported" {
		t.Fatalf("link import: %+v", out)
	}
	out = egressCall(t, e.h, "/api/v1/landing/egress/profile/save", map[string]any{"name": "via proxy", "defaultAction": "outbound:" + strconv.FormatInt(proxyID, 10), "rules": []any{map[string]any{"name": "BT", "enabled": true, "protocols": []string{"bittorrent"}, "action": "block"}}}, 0)
	if out.Code != 0 {
		t.Fatalf("profile save: %+v", out)
	}
	profileID := int64(dataMap(t, out)["id"].(float64))
	if out := egressCall(t, e.h, "/api/v1/landing/egress/outbound/delete", map[string]any{"id": proxyID}, 0); out.Code == 0 {
		t.Fatal("outbound used by a profile deleted")
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/whitelist/save", map[string]any{"whitelist": []string{"0.0.0.0/0"}}, 0); out.Code == 0 {
		t.Fatal("whole-internet whitelist accepted")
	}

	// Old agent: no egress capability → assignment refused, prod unchanged.
	e.exec("UPDATE node SET landing_capable=?, landing_egress=0 WHERE id=2", true)
	agent := egressAgent(t, e, false)
	out = egressCall(t, e.h, "/api/v1/landing/server/save", serverSaveBody(t, e, profileID, true), 0)
	if out.Code == 0 || !strings.Contains(out.Msg, "节点程序版本过旧") {
		t.Fatalf("old agent accepted a profile: %+v", out)
	}
	// Default profile keeps today's payload: no egress key, no revision churn.
	s, _ := e.r.GetLandingServer(1)
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := agent.payloads[len(agent.payloads)-1]["egress"]; ok {
		t.Fatal("default profile sent an egress plan")
	}
	if after, _ := e.r.GetLandingServer(1); after.Revision != s.Revision {
		t.Fatalf("default plan bumped revision %d→%d", s.Revision, after.Revision)
	}

	// Capable agent whose Core still runs the old config: confirmation needed.
	e.exec("UPDATE node SET landing_egress=1 WHERE id=2")
	out = egressCall(t, e.h, "/api/v1/landing/server/save", serverSaveBody(t, e, profileID, false), 0)
	if out.Code == 0 || !strings.Contains(out.Msg, "重启") {
		t.Fatalf("restart not confirmed but saved: %+v", out)
	}
	before, _ := e.r.GetLandingServer(1)
	out = egressCall(t, e.h, "/api/v1/landing/server/save", serverSaveBody(t, e, profileID, true), 0)
	if out.Code != 0 {
		t.Fatalf("confirmed save: %+v", out)
	}
	view := dataMap(t, out)
	egressView := view["egress"].(map[string]any)
	if egressView["profileId"].(float64) != float64(profileID) || egressView["label"] != "代理" || egressView["state"] != "applied" {
		// Plan 139: a block-only rule (BT) filters; allowed traffic leaves via the proxy default.
		t.Fatalf("server egress view %+v", egressView)
	}
	payload := agent.payloads[len(agent.payloads)-1]
	plan, ok := payload["egress"].(*landingegress.Plan)
	// Plan 127: default rules first; the profile's own BT rule only repeated the
	// default BT rule and is not sent twice.
	if !ok || !plan.AllowCoreRestart || len(plan.Rules) != 3 || plan.Rules[0].Port != "25" || plan.Rules[1].Protocol[0] != "bittorrent" || plan.Rules[2].Outbound != landingegress.OutboundTag(proxyID) || strings.Join(plan.Rules[0].InboundTags, ",") != "flvxl-1" {
		t.Fatalf("plan sent to the agent: %+v", payload["egress"])
	}
	if len(plan.Outbounds) != 1 || plan.Outbounds[0].Type != "socks" || plan.Outbounds[0].IPVersion != "force_ipv4" {
		t.Fatalf("outbounds = %+v", plan.Outbounds)
	}
	after, _ := e.r.GetLandingServer(1)
	if after.Revision <= before.Revision || payload["revision"].(int64) != after.Revision {
		t.Fatalf("revision not advanced with the plan: %d→%d payload %v", before.Revision, after.Revision, payload["revision"])
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/profile/delete", map[string]any{"id": profileID}, 0); out.Code == 0 {
		t.Fatal("profile in use deleted")
	}
	// An older frontend that does not send routeProfileId keeps the assignment.
	legacy := serverSaveBody(t, e, profileID, false)
	delete(legacy, "routeProfileId")
	legacy["name"] = "renamed"
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", legacy, 0); out.Code != 0 {
		t.Fatalf("legacy save: %+v", out)
	}
	if kept, _ := e.r.GetLandingServer(1); kept.RouteProfileID != profileID || kept.Name != "renamed" {
		t.Fatalf("legacy save reset the profile: %+v", kept)
	}

	// Users see the summary only.
	req := httptest.NewRequest("POST", "/api/v1/landing/node/list", strings.NewReader(`{}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "2", RoleID: 1}))
	w := httptest.NewRecorder()
	e.h.landingNodeList(w, req)
	var list response.R
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	nodes := list.Data.([]any)
	node := nodes[0].(map[string]any)
	if node["egressLabel"] != "代理" { // block-only rules do not count as 分流 (plan 139)
		t.Fatalf("user node label %v", node["egressLabel"])
	}
	for _, hidden := range []string{"routeProfileId", "effectiveRouteProfileId", "effectiveRouteProfileName"} {
		if _, ok := node[hidden]; ok {
			t.Fatalf("user sees %s", hidden)
		}
	}

	// Node override back to the built-in default removes its rules.
	out = egressCall(t, e.h, "/api/v1/landing/node/update", map[string]any{"id": 1, "routeProfileId": 0}, 0)
	if out.Code != 0 {
		t.Fatalf("node override: %+v", out)
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	// Plan 127: the built-in default now carries only the two default rules.
	optedOut, ok := agent.payloads[len(agent.payloads)-1]["egress"].(*landingegress.Plan)
	if !ok || len(optedOut.Rules) != 2 || len(optedOut.Outbounds) != 0 || optedOut.Rules[0].Name != "默认：屏蔽 SMTP 25" {
		t.Fatalf("node opted out of the profile: %+v", agent.payloads[len(agent.payloads)-1]["egress"])
	}
	e.h.egressSyncWG.Wait()
}

func TestLandingEgressWarpAutoRegistration(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1 WHERE id=2", true)
	e.exec("UPDATE landing_server SET egress_ready=? WHERE id=1", true)
	registrations := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/reg" {
			registrations++
			_, _ = w.Write([]byte(`{"id":"dev-` + strconv.Itoa(registrations) + `","token":"tok","account":{"account_type":"free"},"config":{"client_id":"AQID","peers":[{"public_key":"bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=","endpoint":{"v4":"162.159.192.9:0","v6":"[2606:4700:d0::a29f:c009]:0"}}],"interface":{"addresses":{"v4":"172.16.0.2","v6":"2606:4700:110:1::2"}}}}`))
			return
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	old := landingegress.WarpAPIBase
	landingegress.WarpAPIBase = srv.URL
	defer func() { landingegress.WarpAPIBase = old }()
	agent := egressAgent(t, e, true)
	out := egressCall(t, e.h, "/api/v1/landing/egress/outbound/save", map[string]any{"name": "WARP", "type": "warp", "ipVersion": "force_ipv6"}, 0)
	if out.Code != 0 {
		t.Fatal(out)
	}
	warpID := int64(dataMap(t, out)["id"].(float64))
	out = egressCall(t, e.h, "/api/v1/landing/egress/profile/save", map[string]any{"name": "防止送中", "defaultAction": "direct", "rules": []any{map[string]any{"name": "Google 走 WARP", "enabled": true, "domains": []string{"geosite:google"}, "action": "outbound:" + strconv.FormatInt(warpID, 10)}}}, 0)
	if out.Code != 0 {
		t.Fatal(out)
	}
	profileID := int64(dataMap(t, out)["id"].(float64))
	// Ready Core: no restart confirmation needed.
	out = egressCall(t, e.h, "/api/v1/landing/server/save", serverSaveBody(t, e, profileID, false), 0)
	if out.Code != 0 {
		t.Fatalf("assign: %+v", out)
	}
	if registrations != 1 {
		t.Fatalf("WARP registered %d times", registrations)
	}
	a, _ := e.r.GetLandingWarpAccount(1, warpID)
	if a == nil || a.Status != "ok" || a.Reserved != "1,2,3" || a.EndpointV4 != "162.159.192.9:2408" {
		t.Fatalf("account = %+v", a)
	}
	plan := agent.payloads[len(agent.payloads)-1]["egress"].(*landingegress.Plan)
	wg := plan.Outbounds[0].WireGuard
	if plan.AllowCoreRestart || plan.Outbounds[0].Type != "wireguard" || wg == nil || wg.PrivateKey != a.PrivateKey || wg.AddressV6 != "2606:4700:110:1::2" || len(wg.Reserved) != 3 {
		t.Fatalf("WARP outbound = %+v", plan.Outbounds)
	}
	// Steady state: no second registration, same revision.
	rev, _ := e.r.GetLandingServer(1)
	e.h.ensureLandingWarp(nil, false, time.Second)
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.r.GetLandingServer(1); registrations != 1 || again.Revision != rev.Revision {
		t.Fatalf("steady state re-registered (%d) or bumped revision", registrations)
	}
	// Re-register: new device, new keys, revision advances.
	out = egressCall(t, e.h, "/api/v1/landing/egress/warp/register", map[string]any{"serverId": 1, "outboundId": warpID}, 0)
	if out.Code != 0 || registrations != 2 {
		t.Fatalf("re-register: %+v (%d)", out, registrations)
	}
	b, _ := e.r.GetLandingWarpAccount(1, warpID)
	if b.DeviceID != "dev-2" || b.PrivateKey == a.PrivateKey {
		t.Fatal("re-register kept the old device")
	}
	// Registration failures fail closed and back off.
	landingegress.WarpAPIBase = srv.URL + "/down"
	e.exec("DELETE FROM landing_warp_account")
	e.h.ensureLandingWarp([]int64{1}, false, time.Second)
	f, _ := e.r.GetLandingWarpAccount(1, warpID)
	if f == nil || f.Status != "error" || f.NextAttemptAt == 0 {
		t.Fatalf("failure not recorded: %+v", f)
	}
	plan2, warnings, err := e.h.compileLandingEgress(1)
	if err != nil || len(warnings) == 0 || plan2.Rules[0].Outbound != landingegress.ActionBlock || len(plan2.Outbounds) != 0 {
		t.Fatalf("unregistered WARP must route to block: %+v %v %v", plan2, warnings, err)
	}
	e.h.egressSyncWG.Wait()
}

func TestLandingEgressSharedCapabilityGate(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	caps := landingcustom.Share{ID: 7, Name: "shared", Status: "active", PortMin: 30000, PortMax: 30010, Protocols: []string{"ss2022"}, Hosts: landingcustom.Hosts{IPv4: "203.0.113.70"}}
	raw, _ := json.Marshal(caps)
	for _, v := range []interface{}{&model.LandingServer{ID: 1, Source: "shared", RemoteServerID: 7, Name: "shared", PublicHost: "203.0.113.70", PortMin: 30000, PortMax: 30010, FlowType: 2, TrafficRatio: 1, Status: "active", SharedCaps: string(raw)}, &model.LandingNode{ID: 1, UserID: 2, LandingServerID: 1, Name: "n", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-1", Status: "active"}} {
		if err := e.r.DB().Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	p := landingegress.Profile{Name: "block bt", DefaultAction: "direct", Rules: []landingegress.Rule{{Enabled: true, Protocols: []string{"bittorrent"}, Action: "block"}, {Enabled: true, Domains: []string{"geosite:cn"}, Action: "block"}}}
	saved, _, err := e.r.SaveLandingRouteProfile(&p)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := e.r.GetLandingServer(1)
	if _, err := e.h.validateServerEgressAssignment(*s, saved.ID, false); err == nil || !strings.Contains(err.Error(), "暂不支持自定义出站") {
		t.Fatalf("old Custom accepted a profile: %v", err)
	}
	// Supported but the owner disabled it.
	caps.Egress = &landingcustom.ShareEgress{Versions: []int{1}, Allowed: false, GeoIP: true, GeoSite: false}
	raw, _ = json.Marshal(caps)
	e.exec("UPDATE landing_server SET shared_caps=? WHERE id=1", string(raw))
	s, _ = e.r.GetLandingServer(1)
	if _, err := e.h.validateServerEgressAssignment(*s, saved.ID, false); err == nil || !strings.Contains(err.Error(), "未允许") {
		t.Fatalf("disallowed share accepted: %v", err)
	}
	// Allowed but no geosite data: geosite rules are rejected with a clear message.
	caps.Egress.Allowed = true
	raw, _ = json.Marshal(caps)
	e.exec("UPDATE landing_server SET shared_caps=? WHERE id=1", string(raw))
	s, _ = e.r.GetLandingServer(1)
	if _, err := e.h.validateServerEgressAssignment(*s, saved.ID, false); err == nil || !strings.Contains(err.Error(), "geosite") {
		t.Fatalf("geosite without data accepted: %v", err)
	}
	caps.Egress.GeoSite = true
	raw, _ = json.Marshal(caps)
	e.exec("UPDATE landing_server SET shared_caps=? WHERE id=1", string(raw))
	s, _ = e.r.GetLandingServer(1)
	if needRestart, err := e.h.validateServerEgressAssignment(*s, saved.ID, false); err != nil || needRestart {
		t.Fatalf("supported share: %v restart=%v", err, needRestart)
	}
	if err := e.r.SetLandingServerEgress(1, saved.ID, false); err != nil {
		t.Fatal(err)
	}
	plan, _, err := e.h.compileLandingEgress(1)
	// Plan 127: defaults (SMTP 25, BT) + geosite:cn; the profile's BT rule is a
	// duplicate of the default one.
	if err != nil || plan == nil || len(plan.Rules) != 3 || plan.Rules[2].Domain[0] != "geosite:cn" {
		t.Fatalf("shared plan: %+v %v", plan, err)
	}
	desired, err := e.r.SharedLandingDesired(1, plan)
	if err != nil || desired.Egress == nil || desired.Revision < 2 {
		t.Fatalf("desired with egress: %+v %v", desired, err)
	}
	b, _ := json.Marshal(desired)
	if !strings.Contains(string(b), `"egress":{"version":1`) || !strings.Contains(string(b), `"inbound_tags":["flvxl-1"]`) || strings.Contains(string(b), "builtin") {
		t.Fatalf("contract payload: %s", b)
	}
	// Without egress the payload stays exactly the v1 shape.
	plain, _ := e.r.SharedLandingDesired(1, nil)
	pb, _ := json.Marshal(plain)
	if strings.Contains(string(pb), "egress") {
		t.Fatalf("plain payload carries egress: %s", pb)
	}
}

func TestLandingEgressLoopRejected(t *testing.T) {
	if err := checkLandingLoops(map[int64][]int64{1: {2}, 2: {3}}); err != nil {
		t.Fatal(err)
	}
	if err := checkLandingLoops(map[int64][]int64{1: {2}, 2: {1}}); err == nil {
		t.Fatal("two-server loop accepted")
	}
	if err := checkLandingLoops(map[int64][]int64{4: {4}}); err == nil {
		t.Fatal("self loop accepted")
	}
}

// A failing node hostname must not cost a 2 s lookup on every compile or
// overview load; the last good answer is kept.
func TestLandingEgressResolveCachesFailures(t *testing.T) {
	h := &Handler{}
	calls, fail := 0, false
	old := egressLookup
	egressLookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		calls++
		if fail {
			return nil, context.DeadlineExceeded
		}
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
	}
	t.Cleanup(func() { egressLookup = old })
	for i := 0; i < 3; i++ {
		if got := h.egressResolve("node.example.com"); len(got) != 1 {
			t.Fatalf("lookup %d = %v", i, got)
		}
	}
	if calls != 1 {
		t.Fatalf("successful answer looked up %d times; want 1 (10 min cache)", calls)
	}
	// Expire the good answer, then fail: the old answer is kept and the failure
	// is not retried on every call.
	h.egressDNS["node.example.com"] = egressDNSEntry{addrs: h.egressDNS["node.example.com"].addrs, at: time.Now().Add(-11 * time.Minute)}
	fail = true
	for i := 0; i < 3; i++ {
		if got := h.egressResolve("node.example.com"); len(got) != 1 {
			t.Fatalf("failed lookup dropped the last good answer: %v", got)
		}
	}
	if calls != 2 {
		t.Fatalf("failing lookup retried %d times within a minute; want 1", calls-1)
	}
	for i := 0; i < 3; i++ {
		_ = h.egressResolve("missing.example.com")
	}
	if calls != 3 {
		t.Fatalf("unknown failing host looked up %d times; want 1", calls-2)
	}
}

// A user creating a node gets the same view as the node list: the read-only
// egressLabel, never route profile ids or names (admins still get them).
func TestLandingNodeCreateHidesProfileInternalsForUsers(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1 WHERE id=2", true)
	egressAgent(t, e, true)
	if err := e.r.DB().Create(&model.LandingServerUser{LandingServerID: 1, UserID: 2, CreatedTime: 1}).Error; err != nil {
		t.Fatal(err)
	}
	create := func(sub string, role int, body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest("POST", "/api/v1/landing/node/create", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: sub, RoleID: role}))
		w := httptest.NewRecorder()
		e.h.landingNodeCreate(w, req)
		var out response.R
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Code != 0 {
			t.Fatalf("create as %s: %v %s", sub, err, w.Body.String())
		}
		return dataMap(t, out)
	}
	user := create("2", 1, `{"landingServerId":1,"name":"mine","protocol":"ss2022"}`)
	for _, hidden := range []string{"routeProfileId", "effectiveRouteProfileId", "effectiveRouteProfileName"} {
		if _, ok := user[hidden]; ok {
			t.Fatalf("user create response exposes %s: %v", hidden, user[hidden])
		}
	}
	if user["egressLabel"] != "直连" {
		t.Fatalf("user create response label %v", user["egressLabel"])
	}
	admin := create("1", 0, `{"landingServerId":1,"userId":2,"name":"for-user","protocol":"ss2022"}`)
	if _, ok := admin["effectiveRouteProfileName"]; !ok {
		t.Fatal("admin create response lost the profile name")
	}
}
