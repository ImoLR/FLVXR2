package contract_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

// 20 parallel landing-node creates of one user never overshoot the per-server
// cap (MaxNodesPerUser) or the per-user total (MaxNodes), on both databases.
func TestLandingCapsConcurrentCreates(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			for _, v := range []interface{}{&model.User{ID: 511, User: "cap-a", Status: 1, RoleID: 1}, &model.Node{ID: 611, Name: "native-cap", ServerIP: "203.0.113.11", Port: "10000-19999", LandingCapable: true}, &model.UserGroup{ID: 711, Name: "cap-granted", Status: 1}, &model.UserGroupUser{UserGroupID: 711, UserID: 511}} {
				if err := r.DB().Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}
			const secret = "landing-cap-contract"
			h := handler.New(r, secret, "test")
			router := httpserver.NewRouter(h, secret, r)
			admin, _ := auth.GenerateToken(1, "admin", 0, secret)
			user, _ := auth.GenerateToken(511, "cap-a", 1, secret)
			if out := requestContractEnvelope(t, router, admin, "/api/v1/landing/server/enable", map[string]interface{}{"nodeId": 611}); out.Code != 0 {
				t.Fatalf("enable %+v", out)
			}
			s, err := r.GetLandingServerForNode(611)
			if err != nil || s == nil {
				t.Fatal(err)
			}
			if err := r.SaveLandingServer(s, []int64{711}); err != nil {
				t.Fatal(err)
			}
			nodes := func() int64 {
				var n int64
				if err := r.DB().Model(&model.LandingNode{}).Where("user_id = ?", 511).Count(&n).Error; err != nil {
					t.Fatal(err)
				}
				return n
			}
			burst := func(round int, router http.Handler) (ok int) {
				var wg sync.WaitGroup
				results := make([]response.R, 20)
				for i := range results {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						results[i] = requestContractEnvelope(t, router, user, "/api/v1/landing/node/create", map[string]interface{}{"landingServerId": s.ID, "name": fmt.Sprintf("cap-%d-%d", round, i), "protocol": "ss2022"})
					}(i)
				}
				wg.Wait()
				for _, out := range results {
					if out.Code == 0 {
						ok++
					} else if !strings.Contains(out.Msg, "上限") && !strings.Contains(out.Msg, "最多") && out.Msg != "操作太频繁，请稍后再试" {
						t.Fatalf("unexpected refusal: %+v", out)
					}
				}
				return ok
			}

			// Round 1: per-server cap (default 3), database check only.
			h.SetMutationRateLimitEnabled(false)
			if ok := burst(1, router); ok != 3 || nodes() != 3 {
				t.Fatalf("per-server cap: ok=%d rows=%d, want 3", ok, nodes())
			}
			// Round 2: raise the server cap; the user's total (5) now binds.
			s.MaxNodesPerUser = 10
			if err := r.SaveLandingServer(s, []int64{711}); err != nil {
				t.Fatal(err)
			}
			if err := r.DB().Save(&model.LandingUserSetting{UserID: 511, MaxNodes: 5}).Error; err != nil {
				t.Fatal(err)
			}
			if ok := burst(2, router); ok != 2 || nodes() != 5 {
				t.Fatalf("per-user total: ok=%d rows=%d, want 2 more / 5 total", ok, nodes())
			}
			// Round 3: shipped configuration (rate limit on), caps still hold.
			h.SetMutationRateLimitEnabled(true)
			if err := r.DB().Save(&model.LandingUserSetting{UserID: 511, MaxNodes: 8}).Error; err != nil {
				t.Fatal(err)
			}
			burst(3, router)
			if n := nodes(); n > 8 {
				t.Fatalf("rate-limited round overshot the total: %d", n)
			}
		})
	}
}
