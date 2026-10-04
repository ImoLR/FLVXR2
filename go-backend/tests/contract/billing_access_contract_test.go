package contract_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go-backend/internal/auth"
)

func TestBillingAccessWithoutCommercialConfiguration(t *testing.T) {
	const secret = "billing-access-jwt"
	router, _ := setupContractRouter(t, secret)
	adminToken := mustAdminToken(t, secret)
	userToken, err := auth.GenerateToken(2, "normal_user", 1, secret)
	if err != nil {
		t.Fatalf("generate user token: %v", err)
	}

	created := requestContractEnvelope(t, router, adminToken, "/api/v1/package/create", map[string]interface{}{
		"name": "Test package", "enabled": 1, "shopVisible": 1, "stock": -1,
	})
	if created.Code != 0 {
		t.Fatalf("create package: code=%d msg=%q", created.Code, created.Msg)
	}

	for _, path := range []string{
		"/api/v1/package/list",
		"/api/v1/billing/redeem/list",
		"/api/v1/billing/discount/list",
		"/api/v1/billing/balance-log/list",
		"/api/v1/payment/stats",
		"/api/v1/payment/config/admin/list",
		"/api/v1/order/admin/list",
	} {
		t.Run(path, func(t *testing.T) {
			out := requestContractEnvelope(t, router, adminToken, path, nil)
			if out.Code != 0 {
				t.Fatalf("admin endpoint: code=%d msg=%q", out.Code, out.Msg)
			}
		})
	}

	for _, enabled := range []bool{false, true} {
		out := requestContractEnvelope(t, router, adminToken, "/api/v1/package/store-status/save", map[string]interface{}{"enabled": enabled})
		if out.Code != 0 {
			t.Fatalf("save store status: code=%d msg=%q", out.Code, out.Msg)
		}
		for _, actor := range []struct {
			token string
			admin bool
		}{{adminToken, true}, {userToken, false}} {
			out := requestContractEnvelope(t, router, actor.token, "/api/v1/package/list", nil)
			if out.Code != 0 {
				t.Fatalf("list packages: code=%d msg=%q", out.Code, out.Msg)
			}
			want := 0
			if enabled || actor.admin {
				want = 1
			}
			if items := mustContractSlice(t, out.Data, "packages"); len(items) != want {
				t.Fatalf("store enabled=%v admin=%v: want %d packages, got %d", enabled, actor.admin, want, len(items))
			}
		}
	}

	for _, path := range []string{"/api/v1/package/create", "/api/v1/package/store-status/save", "/api/v1/config/update"} {
		out := requestContractEnvelope(t, router, userToken, path, nil)
		if out.Code != 403 {
			t.Fatalf("non-admin %s: expected 403, got %d", path, out.Code)
		}
	}
	out := requestContractEnvelope(t, router, "", "/api/v1/package/list", nil)
	if out.Code != 401 {
		t.Fatalf("anonymous package list: expected 401, got %d", out.Code)
	}
}

func TestRemovedCommercialRoutes(t *testing.T) {
	const secret = "removed-routes-jwt"
	router, _ := setupContractRouter(t, secret)
	token := mustAdminToken(t, secret)
	for _, path := range []string{"/api/v1/license/info", "/api/v1/license/config", "/api/v1/license/transfer"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", token)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("removed route %s: expected HTTP 404, got %d", path, res.Code)
		}
	}
}
