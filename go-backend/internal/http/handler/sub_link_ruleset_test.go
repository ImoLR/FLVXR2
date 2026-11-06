package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Plan 226: the 国内直连 sing-box config downloads its rule sets DIRECT from
// the panel the subscription came from, and the panel serves them.
func TestSubSingBoxRuleSetsFromPanel(t *testing.T) {
	e := subEnv(t)
	seedSubPanel(t, e)
	e.h.subState().ruleSets.Fetch = func(context.Context, string) ([]byte, error) { return nil, context.DeadlineExceeded }
	sub := dataMap(t, subCall(t, e.h, "create", map[string]any{"name": "s", "items": []any{map[string]any{"kind": "landing_direct", "landingId": 22}}}, 2, 1))
	token := sub["token"].(string)
	do := func(method, path, host string, hdr map[string]string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		e.h.registerSubLink(mux)
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "203.0.113.21:5555"
		req.Host = host
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	sfa := map[string]string{"User-Agent": "SFA/1.12.0 (Android 14; sing-box 1.12.0)", "X-Forwarded-Proto": "https"}
	a := do("GET", "/s/"+token, "a.example.com", sfa).Body.String()
	b := do("GET", "/s/"+token, "b.example.com:8443", sfa).Body.String()
	for host, body := range map[string]string{"a.example.com": a, "b.example.com:8443": b} {
		if !strings.Contains(body, `"url": "https://`+host+`/s/rs/geosite-cn.srs"`) || !strings.Contains(body, `"url": "https://`+host+`/s/rs/geoip-cn.srs"`) ||
			strings.Count(body, `"download_detour": "direct"`) != 2 || strings.Contains(body, "githubusercontent") {
			t.Fatalf("%s: rule sets not from the panel:\n%s", host, body)
		}
	}
	if n, _ := e.h.subState().cache.stats(); n != 2 {
		t.Fatalf("cache entries %d, want 2 (one per origin)", n)
	}

	w := do("GET", "/s/rs/geosite-cn.srs", "a.example.com", nil)
	etag := w.Header().Get("ETag")
	if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("SRS")) || etag == "" || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("geosite-cn: %d %q %q", w.Code, etag, w.Header().Get("Content-Type"))
	}
	if w := do("GET", "/s/rs/geosite-cn.srs", "a.example.com", map[string]string{"If-None-Match": etag}); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("If-None-Match: %d", w.Code)
	}
	if w := do("HEAD", "/s/rs/geoip-cn.srs", "a.example.com", nil); w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD: %d %d", w.Code, w.Body.Len())
	}
	for _, p := range []string{"/s/rs/geosite-cn", "/s/rs/geosite-us.srs", "/s/rs/", "/s/rs/../rs/geoip-cn.srs.srs"} {
		if w := do("GET", p, "a.example.com", nil); w.Code != http.StatusNotFound && w.Code != http.StatusMovedPermanently {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	if w := do("POST", "/s/rs/geoip-cn.srs", "a.example.com", nil); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", w.Code)
	}
	e.exec("INSERT INTO vite_config(name, value, time) VALUES(?, ?, ?)", subConfigKey, "false", time.Now().UnixMilli())
	if w := do("GET", "/s/rs/geoip-cn.srs", "a.example.com", nil); w.Code != http.StatusForbidden {
		t.Fatalf("sub link off: %d", w.Code)
	}
}
