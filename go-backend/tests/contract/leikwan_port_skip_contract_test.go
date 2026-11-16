package contract_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

// Plan 265: the panel never hands out a port the node's LeiKwan client
// listens on (also stopped forwards), refuses typed / changed ports there, and
// keeps saving rows that already hold one.
func TestLeiKwanPortSkipContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			const secret = "lk-port-contract"
			now := time.Now().UnixMilli()
			lk := `[{"forwardId":264,"listenPort":10015,"running":true},{"forwardId":281,"listenPort":10016,"running":false},{"forwardId":282,"listenPort":10017,"running":true},{"forwardId":263,"listenPort":10018,"running":true},{"forwardId":279,"listenPort":10019,"running":true},{"forwardId":280,"listenPort":10020,"running":true},{"forwardId":540,"listenPort":10021,"running":true},{"forwardId":318,"listenPort":10044,"running":true}]`
			str := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
			num := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
			for _, v := range []interface{}{
				&model.Node{ID: 960, Name: "阿里广州", Secret: "lk-960", ServerIP: "198.51.100.60", Port: "10015-10023", Status: 1, CreatedTime: now, LeiKwanStatus: "running", LeiKwanForwards: lk},
				&model.Node{ID: 961, Name: "火山上海", Secret: "lk-961", ServerIP: "198.51.100.61", Port: "10015-10022", Status: 1, CreatedTime: now, LeiKwanStatus: "running", LeiKwanForwards: lk},
				&model.Node{ID: 912, Name: "Bestvm TW", Secret: "lk-912", ServerIP: "198.51.100.12", Port: "10015-10023", Status: 1, CreatedTime: now},
				&model.Tunnel{ID: 801, Name: "lk-entry", Type: 1, Protocol: "tls", Flow: 99999, TrafficRatio: 1, CreatedTime: now, Status: 1},
				&model.ChainTunnel{TunnelID: 801, ChainType: "1", NodeID: 960, Strategy: str("round"), Inx: num(1), Protocol: str("tls")},
				&model.Tunnel{ID: 802, Name: "plain-entry", Type: 1, Protocol: "tls", Flow: 99999, TrafficRatio: 1, CreatedTime: now, Status: 1},
				&model.ChainTunnel{TunnelID: 802, ChainType: "1", NodeID: 912, Strategy: str("round"), Inx: num(1), Protocol: str("tls")},
				// Pre-existing rule already on a LeiKwan port: must keep saving.
				&model.Forward{ID: 701, UserID: 1, UserName: "admin", Name: "old-on-10018", TunnelID: 801, RemoteAddr: "1.1.1.1:443", Strategy: "fifo", CreatedTime: now, Status: 1},
				&model.ForwardPort{ForwardID: 701, NodeID: 960, Port: 10018},
				// Pre-existing tunnel whose exit already listens on a LeiKwan port.
				&model.Tunnel{ID: 803, Name: "old-exit-10020", Type: 2, Protocol: "tls", Flow: 99999, TrafficRatio: 1, CreatedTime: now, Status: 1},
				&model.ChainTunnel{TunnelID: 803, ChainType: "1", NodeID: 912, Strategy: str("round"), Inx: num(1), Protocol: str("tls")},
				&model.ChainTunnel{TunnelID: 803, ChainType: "3", NodeID: 961, Port: num(10020), Strategy: str("round"), Inx: num(1), Protocol: str("tls")},
			} {
				if err := r.DB().Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}
			h := handler.New(r, secret, "test")
			router := httpserver.NewRouter(h, secret, r)
			server := httptest.NewServer(router)
			defer server.Close()
			for _, s := range []string{"lk-960", "lk-961", "lk-912"} {
				stop := startMockNodeSession(t, server.URL, s)
				defer stop()
			}
			admin, _ := auth.GenerateToken(1, "admin", 0, secret)
			call := func(path string, body map[string]interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
				req.Header.Set("Authorization", admin)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				var out response.R
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
					t.Fatalf("%s: %s", path, rec.Body.String())
				}
				return out
			}
			taken := func(out response.R, node string, port string) {
				t.Helper()
				want := "节点 " + node + " 端口 " + port + " 已被本机利群转发占用，请换一个"
				if out.Code == 0 || !strings.Contains(out.Msg, want) {
					t.Fatalf("want %q, got %d %q", want, out.Code, out.Msg)
				}
			}
			portOf := func(name string, node int64) int {
				t.Helper()
				var fp model.ForwardPort
				if err := r.DB().Joins("JOIN forward ON forward.id = forward_port.forward_id").Where("forward.name = ? AND forward_port.node_id = ?", name, node).First(&fp).Error; err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				return fp.Port
			}

			// 1. Auto ports skip 10015–10021 (10016 is a stopped LeiKwan forward) and 10018 (rule 701).
			seen := map[int]bool{}
			for _, name := range []string{"auto-a", "auto-b"} {
				if out := call("/api/v1/forward/create", map[string]interface{}{"name": name, "tunnelId": 801, "remoteAddr": "8.8.8.8:53", "strategy": "fifo"}); out.Code != 0 {
					t.Fatalf("auto create %s: %d %s", name, out.Code, out.Msg)
				}
				p := portOf(name, 960)
				if (p != 10022 && p != 10023) || seen[p] {
					t.Fatalf("auto port %d", p)
				}
				seen[p] = true
			}
			// 2. Typed port on a LeiKwan port (stopped one) is refused.
			taken(call("/api/v1/forward/create", map[string]interface{}{"name": "typed", "tunnelId": 801, "remoteAddr": "8.8.8.8:53", "strategy": "fifo", "inPort": 10016}), "阿里广州", "10016")
			// 3. Editing a rule onto a LeiKwan port is refused; the rule keeps its port.
			var autoA model.Forward
			r.DB().Where("name = ?", "auto-a").First(&autoA)
			before := portOf("auto-a", 960)
			taken(call("/api/v1/forward/update", map[string]interface{}{"id": autoA.ID, "name": "auto-a", "tunnelId": 801, "remoteAddr": "8.8.8.8:53", "strategy": "fifo", "inPort": 10017}), "阿里广州", "10017")
			if portOf("auto-a", 960) != before {
				t.Fatal("refused edit moved the rule")
			}
			// 4. The old rule already on 10018 still saves (same port typed, or port omitted).
			for _, body := range []map[string]interface{}{
				{"id": 701, "name": "old-on-10018-renamed", "tunnelId": 801, "remoteAddr": "1.1.1.1:443", "strategy": "fifo", "inPort": 10018},
				{"id": 701, "name": "old-on-10018", "tunnelId": 801, "remoteAddr": "1.1.1.2:443", "strategy": "fifo"},
			} {
				if out := call("/api/v1/forward/update", body); out.Code != 0 {
					t.Fatalf("grandfathered edit: %d %s", out.Code, out.Msg)
				}
			}
			if portOf("old-on-10018", 960) != 10018 {
				t.Fatal("grandfathered rule moved")
			}
			// 5. A node without a LeiKwan client is unchanged.
			if out := call("/api/v1/forward/create", map[string]interface{}{"name": "plain", "tunnelId": 802, "remoteAddr": "8.8.8.8:53", "strategy": "fifo", "inPort": 10015}); out.Code != 0 {
				t.Fatalf("plain node typed port: %d %s", out.Code, out.Msg)
			}
			// 6. Tunnel exit: typed LeiKwan port refused, auto pick skips them (only 10022 is free on 火山上海).
			tunnel := func(name string, out map[string]interface{}) map[string]interface{} {
				return map[string]interface{}{"name": name, "type": 2, "flow": 1, "trafficRatio": 1.0, "status": 1,
					"inNodeId": []map[string]interface{}{{"nodeId": 912, "protocol": "tls"}}, "outNodeId": []map[string]interface{}{out}}
			}
			taken(call("/api/v1/tunnel/create", tunnel("typed-exit", map[string]interface{}{"nodeId": 961, "protocol": "tls", "port": 10019})), "火山上海", "10019")
			if out := call("/api/v1/tunnel/create", tunnel("auto-exit", map[string]interface{}{"nodeId": 961, "protocol": "tls"})); out.Code != 0 {
				t.Fatalf("auto exit: %d %s", out.Code, out.Msg)
			}
			var autoExit model.Tunnel
			r.DB().Where("name = ?", "auto-exit").First(&autoExit)
			var exit model.ChainTunnel
			r.DB().Where("tunnel_id = ? AND chain_type = '3'", autoExit.ID).First(&exit)
			if exit.Port.Int64 != 10022 {
				t.Fatalf("auto exit port %d", exit.Port.Int64)
			}
			// 7. The old tunnel on 10020 keeps saving; moving it onto 10021 is refused.
			edit := tunnel("old-exit-10020", map[string]interface{}{"nodeId": 961, "protocol": "tls", "port": 10020})
			edit["id"] = 803
			if out := call("/api/v1/tunnel/update", edit); out.Code != 0 {
				t.Fatalf("grandfathered tunnel edit: %d %s", out.Code, out.Msg)
			}
			edit["outNodeId"] = []map[string]interface{}{{"nodeId": 961, "protocol": "tls", "port": 10021}}
			taken(call("/api/v1/tunnel/update", edit), "火山上海", "10021")
			var kept model.ChainTunnel
			r.DB().Where("tunnel_id = 803 AND chain_type = '3'").First(&kept)
			if kept.Port.Int64 != 10020 {
				t.Fatalf("refused tunnel edit changed port to %d", kept.Port.Int64)
			}
			// LeiKwan status and list untouched.
			var n model.Node
			r.DB().First(&n, 960)
			if n.LeiKwanStatus != "running" || n.LeiKwanForwards != lk {
				t.Fatalf("leikwan row changed: %s %s", n.LeiKwanStatus, n.LeiKwanForwards)
			}
		})
	}
}
