package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/http/middleware"
	"go-backend/internal/sublink"
)

// subRuleSet serves GET /s/rs/<name>.srs without login (plan 226): the rule
// sets the 国内直连 sing-box config downloads DIRECT from the panel. Public
// data, served from memory with an ETag; nothing is logged per request.
func (h *Handler) subRuleSet(w http.ResponseWriter, r *http.Request) {
	middleware.SuppressRequestLog(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		subPlain(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, sublink.RuleSetPath), ".srs")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !h.subLinkEnabled() {
		subPlain(w, http.StatusForbidden, "订阅功能已关闭")
		return
	}
	body, etag, ok := h.subState().ruleSets.Get(name, time.Now())
	if !ok {
		http.NotFound(w, r)
		return
	}
	hd := w.Header()
	hd.Set("ETag", etag)
	hd.Set("Cache-Control", "public, max-age=3600")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	hd.Set("Content-Type", "application/octet-stream")
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}
