package testapi

import (
	"bytes"
	"encoding/json"
	"go-backend/internal/leikwan"
	"net/http/httptest"
	"testing"
)

func TestFakeQuotaValidationAndIdempotency(t *testing.T) {
	f := New()
	call := func(key, raw string) int {
		r := httptest.NewRequest("POST", "/api/v1/user/services/39/forwards", bytes.NewBufferString(raw))
		r.Header.Set("Authorization", "Bearer "+f.Token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		return w.Code
	}
	body := `{"route_id":21,"listen_port":null,"transport":"tcp","target":{"source":"manual","host":"8.8.8.8","port":443}}`
	if got := call("unknown-field", `{"unknown":true}`); got != 422 {
		t.Fatalf("unknown fields %d", got)
	}
	for i := 0; i < 8; i++ {
		raw, _ := json.Marshal(i)
		if got := call("create-key-"+string(raw), body); got != 201 {
			t.Fatalf("create %d: %d", i, got)
		}
	}
	if got := call("create-key-8", body); got != 409 {
		t.Fatalf("quota accepted %d", got)
	}
	if got := call("create-key-0", body); got != 200 {
		t.Fatalf("retry full quota %d", got)
	}
	if got := call("create-key-0", `{"route_id":21}`); got != 409 {
		t.Fatalf("idempotency mismatch %d", got)
	}
	f.Forwards = map[int64]leikwan.Forward{}
	f.Stored = 512
	if got := call("storage-key-1", body); got != 409 {
		t.Fatalf("stored quota accepted %d", got)
	}
}
