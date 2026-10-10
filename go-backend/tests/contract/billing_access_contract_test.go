package contract_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/store/model"
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

func TestBillingAdminPermissions(t *testing.T) {
	const secret = "billing-admin-permissions-jwt"
	router, repository := setupContractRouter(t, secret)
	adminToken := mustAdminToken(t, secret)
	userToken, err := auth.GenerateToken(2, "normal_user", 1, secret)
	if err != nil {
		t.Fatalf("generate user token: %v", err)
	}

	amount := int64(500)
	redeem := &model.RedeemCode{Code: "SEEDED-REDEEM", Type: "balance", AmountCents: &amount, IsActive: 1}
	if err := repository.CreateRedeemCodes([]*model.RedeemCode{redeem}); err != nil {
		t.Fatalf("seed redeem code: %v", err)
	}
	discount := &model.DiscountCode{Code: "SEEDED-DISCOUNT", Type: "percent", Value: 10, IsActive: 1}
	if err := repository.CreateDiscountCode(discount); err != nil {
		t.Fatalf("seed discount code: %v", err)
	}
	if err := repository.SavePaymentConfig(&model.PaymentConfig{Channel: "USDT", Config: `{"key":"test-secret"}`, Enabled: 1}); err != nil {
		t.Fatalf("seed payment config: %v", err)
	}
	for _, key := range []string{"billing_redemption_enabled", "billing_discount_enabled"} {
		if err := repository.SetSystemSetting(key, "1"); err != nil {
			t.Fatalf("seed feature setting %s: %v", key, err)
		}
	}

	// Compare every row and column in the tables these write routes can modify.
	snapshot := func(t *testing.T) string {
		t.Helper()
		state := make(map[string][]map[string]interface{})
		for _, table := range []string{"redeem_code", "discount_code", "payment_config", "system_setting"} {
			var rows []map[string]interface{}
			if err := repository.DB().Table(table).Order("1").Find(&rows).Error; err != nil {
				t.Fatalf("snapshot %s: %v", table, err)
			}
			state[table] = rows
		}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("marshal billing snapshot: %v", err)
		}
		return string(data)
	}

	for _, tc := range []struct {
		name  string
		path  string
		body  map[string]interface{}
		write bool
	}{
		{
			name: "redeem create", path: "/api/v1/billing/redeem/create", write: true,
			body: map[string]interface{}{"code": "NEW-REDEEM", "type": "balance", "amountCents": 1000, "count": 1},
		},
		{name: "redeem list", path: "/api/v1/billing/redeem/list"},
		{
			name: "redeem delete", path: "/api/v1/billing/redeem/delete", write: true,
			body: map[string]interface{}{"id": redeem.ID},
		},
		{
			name: "discount create", path: "/api/v1/billing/discount/create", write: true,
			body: map[string]interface{}{"code": "NEW-DISCOUNT", "type": "percent", "value": 20, "maxUses": 5},
		},
		{name: "discount list", path: "/api/v1/billing/discount/list"},
		{
			name: "discount delete", path: "/api/v1/billing/discount/delete", write: true,
			body: map[string]interface{}{"id": discount.ID},
		},
		{
			name: "balance logs for another user", path: "/api/v1/billing/balance-log/list",
			body: map[string]interface{}{"userId": 1, "page": 1, "size": 50},
		},
		{
			name: "feature status save", path: "/api/v1/billing/feature-status/save", write: true,
			body: map[string]interface{}{"redemptionEnabled": 0, "discountEnabled": 0},
		},
		{name: "payment stats", path: "/api/v1/payment/stats"},
		{name: "payment config admin list", path: "/api/v1/payment/config/admin/list"},
		{
			name: "payment config save new", path: "/api/v1/payment/config/save", write: true,
			body: map[string]interface{}{"channel": "YIPAY", "config": `{"key":"new-test-secret"}`, "enabled": 1},
		},
		{
			name: "payment config save existing", path: "/api/v1/payment/config/save", write: true,
			body: map[string]interface{}{"channel": "USDT", "config": `{"key":"updated-test-secret"}`, "enabled": 0},
		},
		{
			name: "payment config delete", path: "/api/v1/payment/config/delete", write: true,
			body: map[string]interface{}{"channel": "USDT"},
		},
		{name: "all orders", path: "/api/v1/order/admin/list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshot(t)
			out := requestContractEnvelope(t, router, userToken, tc.path, tc.body)
			if out.Code != 403 {
				t.Errorf("non-admin %s: expected 403, got %d (%q)", tc.path, out.Code, out.Msg)
			}
			if snapshot(t) != before {
				t.Fatalf("non-admin %s changed billing data", tc.path)
			}

			out = requestContractEnvelope(t, router, adminToken, tc.path, tc.body)
			if out.Code != 0 {
				t.Fatalf("admin %s: expected 0, got %d (%q)", tc.path, out.Code, out.Msg)
			}
			if changed := snapshot(t) != before; changed != tc.write {
				t.Fatalf("admin %s: billing data changed=%v, want %v", tc.path, changed, tc.write)
			}
		})
	}
}

func TestRemovedCommercialRoutes(t *testing.T) {
	const secret = "removed-routes-jwt"
	router, _ := setupContractRouter(t, secret)
	token := mustAdminToken(t, secret)
	// /api/v1/license/info is answered by the stale-frontend kill switch
	// (401 + Clear-Site-Data), see stale_client_kick_contract_test.go.
	for _, path := range []string{"/api/v1/license/config", "/api/v1/license/transfer"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", token)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("removed route %s: expected HTTP 404, got %d", path, res.Code)
		}
	}
}
