package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 158 (contract landing-user-limits-v1): a share that advertises
// user_limits gets 入站 node limits (conn_mode), per-user budgets through
// PUT /shares/{id}/user-limits and its reported usage counts in the user
// totals; without the capability nothing new reaches Custom.
func TestSharedUserLimitsContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newSharedContract(t, dialect)
			s := e.servers[0]
			id := e.node(t, "ss2022")
			e.ok(t, "node/update", e.admin, map[string]any{"id": id, "maxConnections": 50})
			if err := e.r.DB().Model(&model.User{}).Where("id = ?", 501).Updates(map[string]any{"max_connections": 10, "max_client_ips": 3}).Error; err != nil {
				t.Fatal(err)
			}
			sync := func() { e.ok(t, "integration/sync", e.admin, map[string]any{}) }
			calls := func() []landingcustom.UserLimitsRequest {
				e.f.mu.Lock()
				defer e.f.mu.Unlock()
				return append([]landingcustom.UserLimitsRequest(nil), e.f.userLimits[7]...)
			}
			desiredJSON := func() string {
				e.f.mu.Lock()
				defer e.f.mu.Unlock()
				raw, _ := json.Marshal(e.f.desired[7].Nodes)
				return string(raw)
			}
			post := func(path string, body any) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
				req.Header.Set("Authorization", e.admin)
				w := httptest.NewRecorder()
				e.router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Code != 0 {
					t.Fatalf("%s: %v %s", path, err, w.Body.String())
				}
				return out
			}
			liveUsage := func() (float64, float64) {
				for _, item := range post("/api/v1/user/list", map[string]any{}).Data.([]any) {
					u := item.(map[string]any)
					if u["id"].(float64) == 501 {
						conns, _ := u["activeConnections"].(float64)
						ips, _ := u["activeClientIps"].(float64)
						return conns, ips
					}
				}
				t.Fatal("user 501 missing")
				return 0, 0
			}
			until := func(what string, ok func() bool) {
				t.Helper()
				for i := 0; i < 60; i++ {
					sync()
					if ok() {
						return
					}
					time.Sleep(100 * time.Millisecond)
				}
				t.Fatalf("timed out waiting for %s (calls=%+v)", what, calls())
			}

			// 1. No capability: no conn_mode, no user-limits call, not counted.
			sync()
			sync()
			if n := len(calls()); n != 0 || strings.Contains(desiredJSON(), "conn_mode") {
				t.Fatalf("without the capability: %d user-limits calls, desired %s", n, desiredJSON())
			}

			// 2. Capability: node limits count 入站, budgets go out, usage counts.
			e.f.mu.Lock()
			e.f.shares[0].UserLimits = &landingcustom.ShareUserLimits{Versions: []int{1}}
			e.f.userUsage = map[int64][]landingcustom.UserLimitUsage{7: {{UserID: "501", InboundConnections: 3, RelayConnections: 2, OnlineIPs: []string{"198.51.100.7", "::ffff:198.51.100.8"}}}}
			e.f.mu.Unlock()
			until("first user-limits exchange", func() bool { return len(calls()) > 0 })
			if !strings.Contains(desiredJSON(), `"conn_mode":"inbound"`) {
				t.Fatalf("node limit not sent as 入站: %s", desiredJSON())
			}
			first := calls()[0]
			if first.Version != 1 || len(first.Users) != 1 || first.Users[0].UserID != "501" || first.Users[0].MaxInboundConnections == nil || *first.Users[0].MaxInboundConnections != 10 ||
				first.Users[0].ServerMaxInboundConnections != nil || first.Users[0].MaxInboundOnlineIPs == nil || *first.Users[0].MaxInboundOnlineIPs != 3 {
				raw, _ := json.Marshal(first)
				t.Fatalf("first budgets %s", raw)
			}
			until("shared usage in the user total", func() bool { c, ips := liveUsage(); return c == 3 && ips == 2 })

			// 3. A per-server 入站连接数 on the share → server budget (relays included).
			e.ok(t, "user/save", e.admin, map[string]any{"userId": 501, "quotas": []map[string]any{{"serverKind": "landing", "serverRefId": s.ID, "limitBytes": 1 << 30, "maxInboundConnections": 4}}})
			until("server budget", func() bool {
				c := calls()
				last := c[len(c)-1]
				return len(last.Users) == 1 && last.Users[0].ServerMaxInboundConnections != nil && *last.Users[0].ServerMaxInboundConnections == 4
			})
			c := calls()
			for i := 1; i < len(c); i++ {
				if c[i].Sequence <= c[i-1].Sequence {
					t.Fatalf("sequence not increasing: %d then %d", c[i-1].Sequence, c[i].Sequence)
				}
			}

			// 4. The user loses every limit → one empty authoritative list, then nothing.
			if err := e.r.DB().Model(&model.User{}).Where("id = ?", 501).Updates(map[string]any{"max_connections": 0, "max_client_ips": 0}).Error; err != nil {
				t.Fatal(err)
			}
			e.ok(t, "user/save", e.admin, map[string]any{"userId": 501, "quotas": []map[string]any{{"serverKind": "landing", "serverRefId": s.ID, "limitBytes": 1 << 30, "maxInboundConnections": 0}}})
			until("empty list", func() bool { c := calls(); return len(c[len(c)-1].Users) == 0 })
			n := len(calls())
			sync()
			sync()
			if len(calls()) != n {
				t.Fatalf("exchange went on without limited users: %d → %d", n, len(calls()))
			}

			// 5. Capability withdrawn → conn_mode gone again (byte-identical node list).
			e.f.mu.Lock()
			e.f.shares[0].UserLimits = nil
			e.f.mu.Unlock()
			until("conn_mode removed", func() bool { return !strings.Contains(desiredJSON(), "conn_mode") })
		})
	}
}
