package handler

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/http/middleware"
	"go-backend/internal/sublink"
)

// subRuleSet serves GET /s/rs/<name>.<ext> without login: the rule sets the
// subscription configs download DIRECT from the panel — plan 226 geosite-cn /
// geoip-cn for sing-box, plan 256 the 「分流」 catalog sets (.mrs mihomo,
// .srs sing-box, .txt Stash, .list Surge). Public data, served from memory
// with an ETag; nothing is logged per request.
func (h *Handler) subRuleSet(w http.ResponseWriter, r *http.Request) {
	middleware.SuppressRequestLog(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		subPlain(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	file := strings.TrimPrefix(r.URL.Path, sublink.RuleSetPath)
	i := strings.LastIndexByte(file, '.')
	if i <= 0 || strings.Contains(file, "/") {
		http.NotFound(w, r)
		return
	}
	name, ext := file[:i], file[i+1:]
	st := h.subState()
	if !st.ruleSets.Known(name, ext) {
		http.NotFound(w, r)
		return
	}
	if !h.subLinkEnabled() {
		subPlain(w, http.StatusForbidden, "订阅功能已关闭")
		return
	}
	body, etag, ok := st.ruleSets.Get(name, ext, time.Now())
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
	ctype := "application/octet-stream"
	if ext == sublink.ExtText || ext == sublink.ExtList {
		ctype = "text/plain; charset=utf-8"
	}
	hd.Set("Content-Type", ctype)
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}
