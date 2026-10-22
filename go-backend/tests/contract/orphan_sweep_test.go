package contract_test

import (
	"bytes"
	"encoding/json"
	"go-backend/internal/auth"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrphanSweepAdminOnly(t *testing.T) {
	const secret = "orphan-sweep-jwt"
	router, sweepRepo := setupContractRouter(t, secret)
	ensureContractUser(t, sweepRepo, 2, 1)
	token, err := auth.GenerateToken(2, "normal_user", 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	for _, execute := range []bool{false, true} {
		raw, _ := json.Marshal(map[string]interface{}{"execute": execute})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/node/orphan-sweep", bytes.NewReader(raw))
		req.Header.Set("Authorization", token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var result struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusForbidden || result.Code != 403 {
			t.Fatalf("non-admin execute=%v: HTTP %d %s", execute, rec.Code, rec.Body.String())
		}
	}
	out := requestContractEnvelope(t, router, mustAdminToken(t, secret), "/api/v1/node/orphan-sweep", map[string]interface{}{})
	if out.Code != 0 {
		t.Fatalf("admin dry run: %d %s", out.Code, out.Msg)
	}
}
