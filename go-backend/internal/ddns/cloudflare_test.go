package ddns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCloudflareClientVerifyZonesAndErrors(t *testing.T) {
	const token = "secret-token-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":9109,"message":"Invalid access token"}]}`))
			return
		}
		switch r.URL.Path {
		case "/user/tokens/verify":
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"x","status":"active","expires_on":"2026-11-01T00:00:00Z"}}`))
		case "/zones":
			page := r.URL.Query().Get("page")
			zones := []Zone{{ID: "z" + page, Name: "zone" + page + ".test", Permissions: []string{"#zone:read"}}}
			raw, _ := json.Marshal(map[string]interface{}{"success": true, "result": zones, "result_info": map[string]int{"page": 1, "total_pages": 2}})
			_, _ = w.Write(raw)
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":81044,"message":"Record does not exist."}]}`))
		}
	}))
	defer srv.Close()
	c := NewCloudflare(token)
	c.BaseURL = srv.URL
	ctx := context.Background()
	info, err := c.VerifyToken(ctx)
	if err != nil || info.Status != "active" || !info.ExpiresOn.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("verify = %+v %v", info, err)
	}
	zones, err := c.ListZones(ctx)
	if err != nil || len(zones) != 2 || zones[0].CanEditDNS() {
		t.Fatalf("zones = %+v %v (read-only zone must not be editable)", zones, err)
	}
	if _, err := c.GetRecord(ctx, "z", "missing"); err == nil || !strings.Contains(err.Error(), "81044") {
		t.Fatalf("not found = %v", err)
	}
	bad := NewCloudflare("wrong")
	bad.BaseURL = srv.URL
	_, err = bad.VerifyToken(ctx)
	if !IsAuthError(err) || strings.Contains(err.Error(), "wrong") || strings.Contains(err.Error(), token) {
		t.Fatalf("auth error = %v", err)
	}
	down := NewCloudflare(token)
	down.BaseURL = "http://127.0.0.1:1"
	if _, err := down.ListZones(ctx); err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("network error = %v", err)
	}
}
