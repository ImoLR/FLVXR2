package contract_test

import (
	"encoding/json"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 159 (contract landing-user-limits §7): shares advertising user_limits
// version 2 also get the users' 速率 (max_rate_mbps, version 2 bodies); a
// v1-only share never sees the field, users with only a rate are listed too.
func TestSharedUserRateContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newSharedContract(t, dialect)
			s := e.servers[0]
			e.node(t, "ss2022")
			sync := func() { e.ok(t, "integration/sync", e.admin, map[string]any{}) }
			calls := func() []landingcustom.UserLimitsRequest {
				e.f.mu.Lock()
				defer e.f.mu.Unlock()
				return append([]landingcustom.UserLimitsRequest(nil), e.f.userLimits[7]...)
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
			last := func() landingcustom.UserLimitsRequest { c := calls(); return c[len(c)-1] }
			if err := e.r.DB().Model(&model.User{}).Where("id = ?", 501).Update("server_rate_mbps", 20).Error; err != nil {
				t.Fatal(err)
			}

			// 1. v1-only share: a rate alone causes no exchange, no field.
			e.f.mu.Lock()
			e.f.shares[0].UserLimits = &landingcustom.ShareUserLimits{Versions: []int{1}}
			e.f.mu.Unlock()
			sync()
			sync()
			if n := len(calls()); n != 0 {
				raw, _ := json.Marshal(calls())
				t.Fatalf("v1 share got %d calls for a rate-only user: %s", n, raw)
			}

			// 2. Version 2: the user's default rate goes out (conn fields null).
			e.f.mu.Lock()
			e.f.shares[0].UserLimits = &landingcustom.ShareUserLimits{Versions: []int{1, 2}}
			e.f.mu.Unlock()
			until("rate exchange", func() bool { return len(calls()) > 0 })
			first := calls()[0]
			if first.Version != 2 || len(first.Users) != 1 || first.Users[0].UserID != "501" || first.Users[0].MaxRateMbps == nil || *first.Users[0].MaxRateMbps != 20 ||
				first.Users[0].MaxInboundConnections != nil || first.Users[0].MaxInboundOnlineIPs != nil {
				raw, _ := json.Marshal(first)
				t.Fatalf("first v2 body %s", raw)
			}

			// 3. The per-server row on the share overrides the default.
			e.ok(t, "user/save", e.admin, map[string]any{"userId": 501, "quotas": []map[string]any{{"serverKind": "landing", "serverRefId": s.ID, "limitBytes": 1 << 30, "rateOverride": true, "rateMbps": 35}}})
			until("row rate", func() bool { l := last(); return len(l.Users) == 1 && l.Users[0].MaxRateMbps != nil && *l.Users[0].MaxRateMbps == 35 })

			// 4. Conn limits and rate travel in the same entry.
			if err := e.r.DB().Model(&model.User{}).Where("id = ?", 501).Updates(map[string]any{"max_connections": 10}).Error; err != nil {
				t.Fatal(err)
			}
			until("budget + rate", func() bool {
				l := last()
				return len(l.Users) == 1 && l.Users[0].MaxRateMbps != nil && *l.Users[0].MaxRateMbps == 35 && l.Users[0].MaxInboundConnections != nil && *l.Users[0].MaxInboundConnections == 10
			})

			// 5. Everything removed → one empty authoritative list, then nothing.
			if err := e.r.DB().Model(&model.User{}).Where("id = ?", 501).Updates(map[string]any{"max_connections": 0, "server_rate_mbps": 0}).Error; err != nil {
				t.Fatal(err)
			}
			e.ok(t, "user/save", e.admin, map[string]any{"userId": 501, "quotas": []map[string]any{{"serverKind": "landing", "serverRefId": s.ID, "limitBytes": 1 << 30, "rateOverride": false, "rateMbps": 0}}})
			until("empty list", func() bool { return len(last().Users) == 0 })
			n := len(calls())
			sync()
			sync()
			if len(calls()) != n {
				t.Fatalf("exchange went on without limits: %d → %d", n, len(calls()))
			}
		})
	}
}
