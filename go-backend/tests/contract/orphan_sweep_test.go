package contract_test

import (
	"bytes"
	"go-backend/internal/auth"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrphanSweepAdminOnly(t *testing.T) {
	const secret = "orphan-sweep-jwt"
	router, _ := setupContractRouter(t, secret)
	token, err := auth.GenerateToken(2, "normal_user", 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/node/orphan-sweep", bytes.NewBufferString(`{"execute":true}`))
	req.Header.Set("Authorization", token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("HTTP status: %d %s", rec.Code, rec.Body.String())
	}
	for _, execute := range []bool{false, true} {
		out := requestContractEnvelope(t, router, token, "/api/v1/node/orphan-sweep", map[string]interface{}{"execute": execute})
		if out.Code != 403 {
			t.Fatalf("non-admin execute=%v: want 403, got %d %s", execute, out.Code, out.Msg)
		}
	}
	out := requestContractEnvelope(t, router, mustAdminToken(t, secret), "/api/v1/node/orphan-sweep", map[string]interface{}{})
	if out.Code != 0 {
		t.Fatalf("admin dry run: %d %s", out.Code, out.Msg)
	}
}
