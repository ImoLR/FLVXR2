package contract_test

import (
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 234 「自动切换门槛」 on SQLite and PostgreSQL: admin-only API through the
// real router, values stored in vite_config (defaults until saved), the
// generic config endpoint validates them, counts persist, and a share gets
// reality_pin_margin_ms only with reality_pin v2 and a non-default value.
func TestSwitchMarginsContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			const secret = "switch-margins-contract"
			if err := r.DB().Create(&model.User{ID: 77, User: "carol", Pwd: "x", RoleID: 1, Status: 1, Flow: -1}).Error; err != nil {
				t.Fatal(err)
			}
			h := handler.New(r, secret, "test")
			h.SetMutationRateLimitEnabled(false)
			router := httpserver.NewRouter(h, secret, r)
			admin, _ := auth.GenerateToken(1, "admin", 0, secret)
			user, _ := auth.GenerateToken(77, "carol", 1, secret)

			if out := requestContractEnvelope(t, router, user, "/api/v1/config/switch-margins", map[string]any{}); out.Code == 0 {
				t.Fatalf("non-admin read: %+v", out)
			}
			if out := requestContractEnvelope(t, router, user, "/api/v1/config/switch-margins/save", map[string]any{"bestMs": 1, "realityDomainMs": 1, "realityIpMs": 1}); out.Code == 0 {
				t.Fatalf("non-admin save: %+v", out)
			}
			out := requestContractEnvelope(t, router, admin, "/api/v1/config/switch-margins", map[string]any{})
			if out.Code != 0 {
				t.Fatalf("admin read: %+v", out)
			}
			if m := r.SwitchMargins(); m != repo.DefaultSwitchMargins() {
				t.Fatalf("defaults %+v", m)
			}
			if out := requestContractEnvelope(t, router, admin, "/api/v1/config/switch-margins/save", map[string]any{"bestMs": 300, "realityDomainMs": 5, "realityIpMs": 20}); out.Code == 0 {
				t.Fatal("300 ms accepted")
			}
			if out := requestContractEnvelope(t, router, admin, "/api/v1/config/switch-margins/save", map[string]any{"bestMs": 12, "realityDomainMs": 0, "realityIpMs": 45}); out.Code != 0 {
				t.Fatalf("save: %+v", out)
			}
			if m := r.SwitchMargins(); m.BestMs != 12 || m.RealityDomainMs != 0 || m.RealityIPMs != 45 {
				t.Fatalf("saved %+v", m)
			}
			// Saved twice (upsert, no duplicate rows).
			if out := requestContractEnvelope(t, router, admin, "/api/v1/config/switch-margins/save", map[string]any{"bestMs": 12, "realityDomainMs": 0, "realityIpMs": 45}); out.Code != 0 {
				t.Fatalf("re-save: %+v", out)
			}
			if n := mustQueryInt(t, r, "SELECT COUNT(*) FROM vite_config WHERE name IN ('switch_margin_best_ms','switch_margin_reality_domain_ms','switch_margin_reality_ip_ms')"); n != 3 {
				t.Fatalf("%d margin rows", n)
			}
			// Generic config endpoint: validated, cache invalidated, counts read-only.
			if out := requestContractEnvelope(t, router, admin, "/api/v1/config/update-single", map[string]any{"name": repo.SwitchMarginBestKey, "value": "500"}); out.Code == 0 {
				t.Fatal("500 accepted by update-single")
			}
			if out := requestContractEnvelope(t, router, admin, "/api/v1/config/update-single", map[string]any{"name": repo.SwitchMarginBestKey, "value": "9"}); out.Code != 0 || r.SwitchMargins().BestMs != 9 {
				t.Fatalf("update-single 9: %+v best=%d", out, r.SwitchMargins().BestMs)
			}
			if out := requestContractEnvelope(t, router, admin, "/api/v1/config/update", map[string]any{repo.AutoSwitchCountsKey: "{}"}); out.Code == 0 {
				t.Fatal("counts writable")
			}

			// Counts persist in vite_config (one row).
			if err := r.StartAutoSwitchCounts(); err != nil {
				t.Fatal(err)
			}
			if err := r.RecordAutoSwitch(repo.AutoSwitchEvent{Kind: repo.AutoSwitchBest, Unit: 4}, repo.AutoSwitchEvent{Kind: repo.AutoSwitchRealityIP, Unit: 5, Failover: true}); err != nil {
				t.Fatal(err)
			}
			if err := r.RecordAutoSwitch(repo.AutoSwitchEvent{Kind: repo.AutoSwitchBest, Unit: 4}); err != nil {
				t.Fatal(err)
			}
			if n := mustQueryInt(t, r, "SELECT COUNT(*) FROM vite_config WHERE name = 'auto_switch_counts'"); n != 1 {
				t.Fatalf("%d counts rows", n)
			}
			if s := r.AutoSwitchSummaries(time.Now()); s[repo.AutoSwitchBest].Total != 2 || s[repo.AutoSwitchRealityIP].Failover != 1 {
				t.Fatalf("summaries %+v", s)
			}

			// Shared desired: reality_pin v1 → no field; v2 → the margin, new revision; 20 → gone.
			caps := `{"reality_pin":{"versions":[1]}}`
			if err := r.DB().Create(&model.LandingServer{ID: 5, Source: "shared", RemoteServerID: 7, Name: "Dmit T1", PublicHost: "203.0.113.7", PortMin: 30000, PortMax: 30010, FlowType: 2, TrafficRatio: 1, Status: "active",
				Protocols: `["ss2022"]`, SharedCaps: caps, MaxNodesPerUser: 8, Revision: 3, CreatedTime: 1, UpdatedTime: 1}).Error; err != nil {
				t.Fatal(err)
			}
			d, err := r.SharedLandingDesired(5, nil)
			if err != nil || d.RealityPinMarginMs != nil {
				t.Fatalf("v1 share got the margin %v %v", d.RealityPinMarginMs, err)
			}
			rev := d.Revision
			if err := r.DB().Model(&model.LandingServer{}).Where("id = 5").Update("shared_caps", `{"reality_pin":{"versions":[1,2]}}`).Error; err != nil {
				t.Fatal(err)
			}
			d, err = r.SharedLandingDesired(5, nil)
			if err != nil || d.RealityPinMarginMs == nil || *d.RealityPinMarginMs != 45 || d.Revision != rev+1 {
				t.Fatalf("v2 share %v rev %d→%d %v", d.RealityPinMarginMs, rev, d.Revision, err)
			}
			if again, _ := r.SharedLandingDesired(5, nil); again.Revision != d.Revision {
				t.Fatal("unchanged margin moved the revision")
			}
			if err := r.SetSwitchMargins(repo.SwitchMargins{BestMs: 9, RealityDomainMs: 0, RealityIPMs: 20}); err != nil {
				t.Fatal(err)
			}
			if d2, _ := r.SharedLandingDesired(5, nil); d2.RealityPinMarginMs != nil || d2.Revision != d.Revision+1 {
				t.Fatalf("default 20: %v rev %d", d2.RealityPinMarginMs, d2.Revision)
			}
		})
	}
}
