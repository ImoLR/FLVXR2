package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/response"
)

// Plan 150: nftables (kernel DNAT) cannot translate between IPv4 and IPv6.
// Node 48 「沪台」 is reachable only over IPv6, so an nftables rule to an IPv4
// target there forwarded nothing while gost worked. The panel now saves such a
// rule as gost with a note, keeps nftables where a family matches, and warns
// when only part of a dual-stack entry can be served.
func TestNftablesRuleAddressFamilyContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, repo := setupContractRouter(t, secret)
	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	now := time.Now().UnixMilli()

	addEntry := func(label, inIP, serverIP, v4, v6, extra, nodeSecret string) int64 {
		t.Helper()
		var in interface{}
		if inIP != "" {
			in = inIP
		}
		if err := repo.DB().Exec(`
			INSERT INTO tunnel(name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
			VALUES(?, 1.0, 1, 'tls', 99999, ?, ?, 1, ?, 0)
		`, label, now, now, in).Error; err != nil {
			t.Fatalf("insert tunnel %s: %v", label, err)
		}
		tunnelID := mustLastInsertID(t, repo, label)
		if err := repo.DB().Exec(`
			INSERT INTO node(name, secret, server_ip, server_ip_v4, server_ip_v6, extra_ips, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx)
			VALUES(?, ?, ?, ?, ?, ?, '47000-47100', '', 'v1', 1, 1, 1, ?, ?, 1, '[::]', '[::]', 0)
		`, label+"-node", nodeSecret, serverIP, v4, v6, extra, now, now).Error; err != nil {
			t.Fatalf("insert node %s: %v", label, err)
		}
		nodeID := mustLastInsertID(t, repo, label+"-node")
		if err := repo.DB().Exec(`
			INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol)
			VALUES(?, 1, ?, NULL, 'round', 1, 'tls')
		`, tunnelID, nodeID).Error; err != nil {
			t.Fatalf("insert chain_tunnel %s: %v", label, err)
		}
		return tunnelID
	}
	// Literal addresses keep the test free of DNS.
	v6Only := addEntry("nft-hutai-v6only", "240e:b8f:72d:5c04:ca:8aff:fec0:2813,2408:820c:750b:dc64:ca:8aff:fec0:2813",
		"240e:b8f:72d:5c04:ca:8aff:fec0:2813", "", "240e:b8f:72d:5c04:ca:8aff:fec0:2813", `["2408:820c:750b:dc64:ca:8aff:fec0:2813"]`, "nft-v6-secret")
	v4Only := addEntry("nft-v4only", "", "10.40.0.1", "10.40.0.1", "", "", "nft-v4-secret")
	dual := addEntry("nft-dual", "10.41.0.1,2001:db8:41::1", "10.41.0.1", "10.41.0.1", "", "", "nft-dual-secret")

	type command struct {
		node, typ string
		data      json.RawMessage
	}
	var mu sync.Mutex
	var commands []command
	recorder := func(node string) func(string, json.RawMessage) (bool, string) {
		return func(typ string, data json.RawMessage) (bool, string) {
			mu.Lock()
			commands = append(commands, command{node: node, typ: typ, data: append(json.RawMessage(nil), data...)})
			mu.Unlock()
			return false, ""
		}
	}
	server := httptest.NewServer(router)
	defer server.Close()
	for node, nodeSecret := range map[string]string{"v6": "nft-v6-secret", "v4": "nft-v4-secret", "dual": "nft-dual-secret"} {
		stop := startMockNodeSessionWithCommandRecorder(t, server.URL, nodeSecret, recorder(node))
		defer stop()
	}
	takeCommands := func() []command {
		mu.Lock()
		defer mu.Unlock()
		out := commands
		commands = nil
		return out
	}
	types := func(cs []command) string {
		var out []string
		for _, c := range cs {
			if c.typ != "SetQuotaGroups" {
				out = append(out, c.node+":"+c.typ)
			}
		}
		return strings.Join(out, ",")
	}
	nftTargets := func(cs []command) []string {
		var out []string
		for _, c := range cs {
			if c.typ != "AddNftablesRules" {
				continue
			}
			var req struct {
				Rules []struct {
					Protocol string `json:"protocol"`
					Target   string `json:"target"`
				} `json:"rules"`
			}
			if err := json.Unmarshal(c.data, &req); err != nil {
				t.Fatalf("decode AddNftablesRules: %v", err)
			}
			for _, r := range req.Rules {
				out = append(out, r.Protocol+">"+r.Target)
			}
		}
		return out
	}
	post := func(path string, body map[string]interface{}) (int, string, map[string]interface{}) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", adminToken)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		var out response.R
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		data, _ := out.Data.(map[string]interface{})
		return out.Code, out.Msg, data
	}
	warnings := func(data map[string]interface{}) string {
		list, _ := data["warnings"].([]interface{})
		var out []string
		for _, w := range list {
			out = append(out, w.(string))
		}
		return strings.Join(out, " | ")
	}
	modeOf := func(name string) string {
		return mustQueryString(t, repo, `SELECT mode FROM forward WHERE name = ?`, name)
	}
	idOf := func(name string) int64 {
		return mustQueryInt64(t, repo, `SELECT id FROM forward WHERE name = ?`, name)
	}

	// 1. The user's rule: IPv6-only entry, IPv4 target, nftables -> saved and deployed as gost.
	takeCommands()
	code, msg, data := post("/api/v1/forward/create", map[string]interface{}{
		"name": "zz150-v4-target", "tunnelId": v6Only, "remoteAddr": "203.0.113.10:443", "mode": "nftables", "inPort": 47001, "strategy": "fifo",
	})
	if code != 0 {
		t.Fatalf("create v4 target: %d %s", code, msg)
	}
	if w := warnings(data); !strings.Contains(w, "只有 IPv6 地址，目标只有 IPv4 地址") || !strings.Contains(w, "已自动改用 gost 转发") || data["mode"] != "gost" {
		t.Fatalf("create v4 target: warnings %q mode %v", w, data["mode"])
	}
	if got := modeOf("zz150-v4-target"); got != "gost" {
		t.Fatalf("rule saved as %q, want gost", got)
	}
	if got := types(takeCommands()); strings.Contains(got, "AddNftablesRules") || !strings.Contains(got, "v6:UpdateService") {
		t.Fatalf("v4 target deploy commands %q: want gost services, no nftables rule", got)
	}

	// 2. IPv6 target on the same entry: nftables works (prod rule 158) and stays.
	code, msg, data = post("/api/v1/forward/create", map[string]interface{}{
		"name": "zz150-v6-target", "tunnelId": v6Only, "remoteAddr": "[2001:db8::10]:443", "mode": "nftables", "inPort": 47002, "strategy": "fifo",
	})
	if code != 0 || warnings(data) != "" {
		t.Fatalf("create v6 target: %d %s warnings %q", code, msg, warnings(data))
	}
	if got := modeOf("zz150-v6-target"); got != "nftables" {
		t.Fatalf("v6 target saved as %q", got)
	}
	if got := strings.Join(nftTargets(takeCommands()), ","); got != "tcp>[2001:db8::10]:443,udp>[2001:db8::10]:443" {
		t.Fatalf("v6 target nftables payload %q", got)
	}

	// 3. Editing that rule to an IPv4 target switches it to gost and removes the DNAT.
	code, msg, data = post("/api/v1/forward/update", map[string]interface{}{
		"id": idOf("zz150-v6-target"), "name": "zz150-v6-target", "tunnelId": v6Only, "remoteAddr": "203.0.113.11:443", "mode": "nftables", "inPort": 47002, "strategy": "fifo",
	})
	if code != 0 || !strings.Contains(warnings(data), "已自动改用 gost 转发") {
		t.Fatalf("update to v4 target: %d %s warnings %q", code, msg, warnings(data))
	}
	if got := modeOf("zz150-v6-target"); got != "gost" {
		t.Fatalf("updated rule saved as %q, want gost", got)
	}
	if got := types(takeCommands()); !strings.Contains(got, "v6:DeleteNftablesRules") || !strings.Contains(got, "v6:UpdateService") || strings.Contains(got, "AddNftablesRules") {
		t.Fatalf("update commands %q", got)
	}

	// 4. IPv4-only entry to an IPv6 target (the old form refused it): gost.
	code, msg, data = post("/api/v1/forward/create", map[string]interface{}{
		"name": "zz150-v4entry-v6target", "tunnelId": v4Only, "remoteAddr": "[2001:db8::20]:443", "mode": "nftables", "inPort": 47003, "strategy": "fifo",
	})
	if code != 0 || !strings.Contains(warnings(data), "只有 IPv4 地址，目标只有 IPv6 地址") || modeOf("zz150-v4entry-v6target") != "gost" {
		t.Fatalf("v4 entry to v6 target: %d %s warnings %q mode %s", code, msg, warnings(data), modeOf("zz150-v4entry-v6target"))
	}
	takeCommands()

	// 5. Dual-stack entry, IPv4 target: nftables kept for IPv4, the IPv6 entry is flagged.
	code, msg, data = post("/api/v1/forward/create", map[string]interface{}{
		"name": "zz150-dual", "tunnelId": dual, "remoteAddr": "203.0.113.12:443", "mode": "nftables", "inPort": 47004, "strategy": "fifo",
	})
	if code != 0 || !strings.Contains(warnings(data), "的 IPv6 地址不能用 nftables 转发到 IPv4 目标") || modeOf("zz150-dual") != "nftables" {
		t.Fatalf("dual entry: %d %s warnings %q mode %s", code, msg, warnings(data), modeOf("zz150-dual"))
	}
	if got := strings.Join(nftTargets(takeCommands()), ","); got != "tcp>203.0.113.12:443,udp>203.0.113.12:443" {
		t.Fatalf("dual entry nftables payload %q", got)
	}

	// 6. gost rules are untouched by the check.
	code, msg, data = post("/api/v1/forward/create", map[string]interface{}{
		"name": "zz150-gost", "tunnelId": v6Only, "remoteAddr": "203.0.113.13:443", "mode": "gost", "inPort": 47005, "strategy": "fifo",
	})
	if code != 0 || warnings(data) != "" || modeOf("zz150-gost") != "gost" {
		t.Fatalf("gost rule: %d %s warnings %q", code, msg, warnings(data))
	}

	// 7. A batch move cannot strand an nftables rule on entries it cannot use:
	// that rule stays where it works, with the reason.
	code, msg, _ = post("/api/v1/forward/create", map[string]interface{}{
		"name": "zz150-v4-nft", "tunnelId": v4Only, "remoteAddr": "203.0.113.14:443", "mode": "nftables", "inPort": 47006, "strategy": "fifo",
	})
	if code != 0 || modeOf("zz150-v4-nft") != "nftables" {
		t.Fatalf("v4 nft rule: %d %s mode %s", code, msg, modeOf("zz150-v4-nft"))
	}
	code, msg, data = post("/api/v1/forward/batch-change-tunnel", map[string]interface{}{
		"forwardIds": []int64{idOf("zz150-v4-nft")}, "targetTunnelId": v6Only,
	})
	failures, _ := data["failures"].([]interface{})
	if code != 0 || len(failures) != 1 || !strings.Contains(failures[0].(map[string]interface{})["reason"].(string), "只有 IPv6 地址，目标只有 IPv4 地址") {
		t.Fatalf("batch move: %d %s %+v", code, msg, data)
	}
	if got := mustQueryInt64(t, repo, `SELECT tunnel_id FROM forward WHERE name = ?`, "zz150-v4-nft"); got != v4Only || modeOf("zz150-v4-nft") != "nftables" {
		t.Fatalf("batch-refused rule moved to tunnel %d mode %s", got, modeOf("zz150-v4-nft"))
	}
}
