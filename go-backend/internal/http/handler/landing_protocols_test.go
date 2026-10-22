package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 121 handler coverage: server defaults survive older frontends, node
// views expose only non-secret options, links carry the client configs, and a
// withdrawn capability shows the withheld reason.
func protocolsCall(t *testing.T, h *Handler, sub int64, role int, path string, body any) response.R {
	t.Helper()
	mux := http.NewServeMux()
	h.registerLanding(mux)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: strconv.FormatInt(sub, 10), RoleID: role}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var out response.R
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %v %s", path, err, w.Body.String())
	}
	return out
}

func protocolsShare(caps bool) landingcustom.Share {
	s := landingcustom.Share{ID: 10, Name: "Dmit T1", Hosts: landingcustom.Hosts{IPv4: "154.3.39.138"}, Status: "active", PortMin: 30000, PortMax: 30010, ResetDay: 1,
		Core: landingcustom.Core{State: "running"}, Protocols: []string{"ss2022", "vless_reality_vision"}, ServerMode: "embedded", BusyPorts: []int{30000}}
	if caps {
		s.ProtocolCaps = &landingcustom.ProtocolCaps{Versions: []int{1}, Protocols: map[string]landingcustom.ProtocolCap{"anytls": {UDP: true}, "snell": {UDP: false}, "mieru": {UDP: true}}}
	}
	return s
}

func TestLandingExtraProtocolsHandlers(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	if err := e.r.SyncSharedLandings([]landingcustom.Share{protocolsShare(true)}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var s model.LandingServer
	if err := e.r.DB().Where("remote_server_id = 10").First(&s).Error; err != nil {
		t.Fatal(err)
	}
	e.r.DB().Create(&model.LandingServerUser{LandingServerID: s.ID, UserID: 2, CreatedTime: 1})
	body := func(extra map[string]any) map[string]any {
		current, _ := e.r.GetLandingServer(s.ID)
		raw, _ := json.Marshal(current)
		out := map[string]any{}
		_ = json.Unmarshal(raw, &out)
		// An older frontend sends exactly these keys (no plan-121 defaults).
		for _, k := range []string{"anytlsServerName", "snellVersion", "snellObfs", "snellObfsHost", "mieruTransport"} {
			delete(out, k)
		}
		out["protocols"] = []string{"ss2022", "vless_reality_vision", "anytls", "snell", "mieru"}
		out["realityServerNames"] = []string{"www.lovelive-anime.jp"}
		out["userGroupIds"] = []int64{}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	if out := protocolsCall(t, e.h, 1, 0, "/api/v1/landing/server/save", body(map[string]any{"snellVersion": 5, "snellObfs": "tls", "snellObfsHost": "www.bing.com", "mieruTransport": "UDP", "anytlsServerName": "cdn.example.com"})); out.Code != 0 {
		t.Fatalf("save: %+v", out)
	}
	// Legacy save keeps the defaults.
	if out := protocolsCall(t, e.h, 1, 0, "/api/v1/landing/server/save", body(nil)); out.Code != 0 {
		t.Fatalf("legacy save: %+v", out)
	}
	saved, _ := e.r.GetLandingServer(s.ID)
	if saved.SnellVersion != 5 || saved.SnellObfs != "tls" || saved.SnellObfsHost != "www.bing.com" || saved.MieruTransport != "UDP" || saved.AnyTLSServerName != "cdn.example.com" {
		t.Fatalf("legacy save reset defaults: %+v", saved)
	}
	// Admin server list: caps without the busy-port list, mode passed through.
	list := protocolsCall(t, e.h, 1, 0, "/api/v1/landing/server/list", map[string]any{})
	raw, _ := json.Marshal(list.Data)
	if !strings.Contains(string(raw), `"server_mode":"embedded"`) || strings.Contains(string(raw), "busy_ports") || !strings.Contains(string(raw), `"busyPortCount":1`) || !strings.Contains(string(raw), `"protocol_caps"`) {
		t.Fatalf("admin server view %s", raw)
	}
	// User server list (plan 132): only the protocol caps the create dialog needs.
	list = protocolsCall(t, e.h, 2, 1, "/api/v1/landing/server/list", map[string]any{})
	raw, _ = json.Marshal(list.Data)
	if strings.Contains(string(raw), "server_mode") || strings.Contains(string(raw), "busy_ports") || strings.Contains(string(raw), "busyPortCount") || !strings.Contains(string(raw), `"protocol_caps"`) {
		t.Fatalf("user server view %s", raw)
	}
	ids := map[string]int64{}
	for _, p := range []string{"anytls", "snell", "mieru"} {
		out := protocolsCall(t, e.h, 2, 1, "/api/v1/landing/node/create", map[string]any{"landingServerId": s.ID, "name": "n-" + p, "protocol": p})
		if out.Code != 0 {
			t.Fatalf("create %s: %+v", p, out)
		}
		view := dataMap(t, out)
		ids[p] = int64(view["id"].(float64))
		rawView, _ := json.Marshal(view)
		for _, secret := range []string{`"password"`, `"username"`, `"tlsKeyPem"`, "PRIVATE KEY", `"psk"`} {
			if strings.Contains(string(rawView), secret) {
				t.Fatalf("%s view leaks %s: %s", p, secret, rawView)
			}
		}
		if view["protocolOpts"] == nil {
			t.Fatalf("%s view without options: %s", p, rawView)
		}
	}
	links := protocolsCall(t, e.h, 2, 1, "/api/v1/landing/node/links", map[string]any{"id": ids["snell"]})
	var ls []repo.LandingLink
	b, _ := json.Marshal(links.Data)
	_ = json.Unmarshal(b, &ls)
	if len(ls) != 1 || ls[0].URL != "" || !strings.Contains(ls[0].Surge, "version=5, obfs=tls, obfs-host=www.bing.com") || !strings.Contains(ls[0].Clash, "udp: false") {
		t.Fatalf("snell links %+v", ls)
	}
	links = protocolsCall(t, e.h, 2, 1, "/api/v1/landing/node/links", map[string]any{"id": ids["anytls"]})
	b, _ = json.Marshal(links.Data)
	_ = json.Unmarshal(b, &ls)
	if !strings.Contains(ls[0].URL, "sni=cdn.example.com") || !strings.Contains(ls[0].Clash, `sni: "cdn.example.com"`) {
		t.Fatalf("anytls links %+v", ls)
	}
	// Capability withdrawn: the node list explains why the nodes stopped.
	if err := e.r.SyncSharedLandings([]landingcustom.Share{protocolsShare(false)}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	nodes := protocolsCall(t, e.h, 2, 1, "/api/v1/landing/node/list", map[string]any{})
	b, _ = json.Marshal(nodes.Data)
	var views []map[string]any
	_ = json.Unmarshal(b, &views)
	if len(views) != 3 {
		t.Fatalf("nodes %s", b)
	}
	for _, v := range views {
		if v["status"] != "error" || !strings.Contains(v["syncError"].(string), "Helper 需升级") {
			t.Fatalf("withheld view %+v", v)
		}
	}
	// The egress plan input never names a withheld node's inbound tag.
	d, err := e.r.LandingEgressData(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if in := landingEgressInput(d); len(in.Nodes) != 0 {
		t.Fatalf("withheld nodes in egress input: %+v", in.Nodes)
	}
}
