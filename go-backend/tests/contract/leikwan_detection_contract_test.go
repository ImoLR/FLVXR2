package contract_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLeiKwanDetectionAdminOnlyContract(t *testing.T) {
	e := newLeiKwanEnv(t)
	if err := e.repository.UpdateNodeLeiKwan(100, "running", `[{"forwardId":263,"listenPort":10018,"running":true}]`, 1234); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token string
		admin bool
	}{{e.admin, true}, {e.user, false}} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/node/list", bytes.NewBufferString(`{}`))
		req.Header.Set("Authorization", tc.token)
		w := httptest.NewRecorder()
		e.router.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		for _, key := range []string{"leikwanStatus", "leikwanForwards", "leikwanCheckedAt"} {
			if bytes.Contains(w.Body.Bytes(), []byte(key)) != tc.admin {
				t.Fatalf("admin=%v key=%s body=%s", tc.admin, key, w.Body.String())
			}
		}
		if tc.admin && !bytes.Contains(w.Body.Bytes(), []byte(`"listenPort":10018`)) {
			t.Fatal(w.Body.String())
		}
	}
}
