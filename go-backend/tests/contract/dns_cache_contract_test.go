package contract_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go-backend/internal/auth"
	"go-backend/internal/security"
)

// Plan 240: the agent's dns_cache report in the admin node list, 「修复 DNS 缓存」 (DNSCacheEnsure with the
// panel's zones, single run + cooldown, old agents refused) and the ensure run when a node becomes a native
// landing.

type fakeDNSAgent struct {
	mu      sync.Mutex
	ensures []agentCmd
	reply   map[string]interface{}
}

// serve answers every command; DNSCacheEnsure gets f.reply as data.
func (f *fakeDNSAgent) serve(t *testing.T, c *websocket.Conn, secret string) {
	aes, err := security.NewAESCrypto(secret)
	if err != nil {
		t.Error(err)
		return
	}
	for {
		_, payload, err := c.ReadMessage()
		if err != nil {
			return
		}
		raw := payload
		var wrap struct {
			Encrypted bool   `json:"encrypted"`
			Data      string `json:"data"`
		}
		if json.Unmarshal(payload, &wrap) == nil && wrap.Encrypted {
			if raw, err = aes.Decrypt(wrap.Data); err != nil {
				continue
			}
		}
		var cmd agentCmd
		if json.Unmarshal(raw, &cmd) != nil || cmd.Type == "" {
			continue
		}
		resp := map[string]interface{}{"type": cmd.Type + "Response", "requestId": cmd.RequestID, "success": true, "message": "OK"}
		if cmd.Type == "DNSCacheEnsure" {
			f.mu.Lock()
			f.ensures = append(f.ensures, cmd)
			resp["data"] = f.reply
			f.mu.Unlock()
		}
		_ = c.WriteJSON(resp)
	}
}

func (f *fakeDNSAgent) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ensures)
}

func dialDNSAgent(t *testing.T, base, secret, caps string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(base+"/system-info?type=1&secret="+secret+"&version=test"+caps, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestDNSCacheReportEnsureAndLandingContract(t *testing.T) {
	const secret = "dns-cache-contract"
	router, r := setupContractRouter(t, secret)
	admin := mustAdminToken(t, secret)
	srv := httptest.NewServer(router)
	defer srv.Close()
	base := "ws" + strings.TrimPrefix(srv.URL, "http")
	for _, n := range []map[string]interface{}{
		{"name": "new-agent", "serverIpV4": "198.51.100.31"},
		{"name": "old-agent", "serverIpV4": "198.51.100.32"},
		{"name": "landing-agent", "serverIpV4": "198.51.100.33"},
	} {
		if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", n); out.Code != 0 {
			t.Fatalf("create = %+v", out)
		}
	}
	listItem := func(id int64) map[string]interface{} {
		out := requestContractEnvelope(t, router, admin, "/api/v1/node/list", map[string]interface{}{})
		for _, it := range out.Data.([]interface{}) {
			if m := it.(map[string]interface{}); m["id"] == float64(id) {
				return m
			}
		}
		t.Fatalf("node %d not listed", id)
		return nil
	}
	// Offline: no dnsCache key (UI: 未知).
	if v, ok := listItem(1)["dnsCache"]; ok {
		t.Fatalf("offline node dnsCache = %v", v)
	}

	n1, n2, n3 := nodeRow(t, r, 1), nodeRow(t, r, 2), nodeRow(t, r, 3)
	agent := &fakeDNSAgent{reply: map[string]interface{}{
		"dns_cache": map[string]interface{}{"mode": "dnsmasq", "ok": true, "checked_at": 1700000000000},
		"summary":   "DNS 缓存：本机 dnsmasq（127.0.0.1，上游 1.1.1.1 8.8.4.4）已配置",
		"output":    "[flvx-dns-cache] cache running\nRESULT mode=dnsmasq changed=1",
	}}
	c1 := dialDNSAgent(t, base, n1.Secret, "&dnsCache=1")
	go agent.serve(t, c1, n1.Secret)
	c2 := dialDNSAgent(t, base, n2.Secret, "") // agent without plan 240
	go (&fakeDNSAgent{}).serve(t, c2, n2.Secret)

	// The metric's dns_cache lands in the admin list (memory only).
	metric := `{"type":"metric","data":{"uptime":5,"cpu_usage":1,"memory_usage":1,"dns_cache":{"mode":"none","ok":false,"reason":"未启用本机 DNS 缓存（只有 1 个解析器）","checked_at":1700000000000}}}`
	if err := c1.WriteMessage(websocket.TextMessage, []byte(metric)); err != nil {
		t.Fatal(err)
	}
	var dc map[string]interface{}
	for i := 0; i < 100; i++ {
		dc, _ = listItem(n1.ID)["dnsCache"].(map[string]interface{})
		if dc != nil && dc["mode"] == "none" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if dc == nil || dc["supported"] != true || dc["mode"] != "none" || dc["ok"] != false || !strings.Contains(dc["reason"].(string), "1 个解析器") || dc["checkedAt"] != float64(1700000000000) {
		t.Fatalf("dnsCache after metric = %v", dc)
	}
	var old map[string]interface{}
	for i := 0; i < 100 && old == nil; i++ {
		old, _ = listItem(n2.ID)["dnsCache"].(map[string]interface{})
		time.Sleep(10 * time.Millisecond)
	}
	if old == nil || old["supported"] != false || old["mode"] != nil {
		t.Fatalf("old agent dnsCache = %v", old)
	}

	// 「修复 DNS 缓存」: DNSCacheEnsure with the panel's zones, fresh state in the reply and in the list.
	out := requestContractEnvelope(t, router, admin, "/api/v1/node/dns-cache/ensure", map[string]interface{}{"id": n1.ID})
	if out.Code != 0 {
		t.Fatalf("ensure = %+v", out)
	}
	res := out.Data.(map[string]interface{})
	if got := res["dnsCache"].(map[string]interface{}); got["mode"] != "dnsmasq" || got["ok"] != true || !strings.HasPrefix(res["summary"].(string), "DNS 缓存：本机 dnsmasq") {
		t.Fatalf("ensure result = %v", res)
	}
	if agent.count() != 1 {
		t.Fatalf("ensure commands = %d", agent.count())
	}
	zones, _ := agent.ensures[0].Data["zones"].([]interface{})
	if len(zones) == 0 {
		t.Fatalf("zones = %v", agent.ensures[0].Data)
	}
	for _, z := range zones {
		if s := z.(string); s == "" || strings.ContainsAny(s, " :/") {
			t.Fatalf("bad zone %q in %v", s, zones)
		}
	}
	if dc = listItem(n1.ID)["dnsCache"].(map[string]interface{}); dc["mode"] != "dnsmasq" || dc["ok"] != true {
		t.Fatalf("list after ensure = %v", dc)
	}
	// Cooldown: a second click within a minute is refused without reaching the agent.
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/dns-cache/ensure", map[string]interface{}{"id": n1.ID}); out.Code == 0 || !strings.Contains(out.Msg, "秒后再试") {
		t.Fatalf("second ensure = %+v", out)
	}
	if agent.count() != 1 {
		t.Fatalf("cooldown leaked a command: %d", agent.count())
	}
	// Old agent / offline / bad id / non-admin.
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/dns-cache/ensure", map[string]interface{}{"id": n2.ID}); out.Code == 0 || !strings.Contains(out.Msg, "版本过旧") {
		t.Fatalf("old agent ensure = %+v", out)
	}
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/dns-cache/ensure", map[string]interface{}{"id": n3.ID}); out.Code == 0 || !strings.Contains(out.Msg, "不在线") {
		t.Fatalf("offline ensure = %+v", out)
	}
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/dns-cache/ensure", map[string]interface{}{"id": 0}); out.Code == 0 {
		t.Fatalf("bad id ensure = %+v", out)
	}
	ensureContractUser(t, r, 2, 1)
	user, _ := auth.GenerateToken(2, "user", 1, secret)
	if out := requestContractEnvelope(t, router, user, "/api/v1/node/dns-cache/ensure", map[string]interface{}{"id": n1.ID}); out.Code != 403 {
		t.Fatalf("user ensure = %+v", out)
	}

	// Native landing conversion: the new landing node gets one ensure run in the background.
	landingAgent := &fakeDNSAgent{reply: map[string]interface{}{"dns_cache": map[string]interface{}{"mode": "resolved", "ok": true}}}
	c3 := dialDNSAgent(t, base, n3.Secret, "&dnsCache=1")
	go landingAgent.serve(t, c3, n3.Secret)
	if err := c3.WriteMessage(websocket.TextMessage, []byte(`{"type":"metric","data":{"uptime":5,"landing_capable":true}}`)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100 && !nodeRow(t, r, n3.ID).LandingCapable; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if out := requestContractEnvelope(t, router, admin, "/api/v1/landing/server/enable", map[string]interface{}{"nodeId": n3.ID}); out.Code != 0 {
		t.Fatalf("landing enable = %+v", out)
	}
	for i := 0; i < 200 && landingAgent.count() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if landingAgent.count() != 1 {
		t.Fatalf("landing conversion ensure runs = %d", landingAgent.count())
	}
	for i := 0; i < 100; i++ {
		if dc, _ = listItem(n3.ID)["dnsCache"].(map[string]interface{}); dc != nil && dc["mode"] == "resolved" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if dc == nil || dc["mode"] != "resolved" || dc["ok"] != true {
		t.Fatalf("landing node dnsCache = %v", dc)
	}
}
