package handler

import (
	"encoding/json"
	"net/http"

	"go-backend/internal/http/response"
)

// StaleFrontendKickPath is only requested by frontends built before fork.16
// (the commercial-license endpoint was removed there). Such clients can be
// pinned to an old build by a service worker that never re-fetches /sw.js.
const StaleFrontendKickPath = "/api/v1/license/info"

// staleFrontendKick answers the old license endpoint with HTTP 401 and
// Clear-Site-Data "storage". The browser unregisters the stale service worker
// and clears CacheStorage/localStorage before the old JS sees the 401; the old
// JS then logs out and navigates to "/", which now loads the fresh index.html.
// Constant time, no DB access, no auth: it must work with any (or no) token.
func (h *Handler) staleFrontendKick(w http.ResponseWriter, _ *http.Request) {
	header := w.Header()
	header.Set("Clear-Site-Data", `"storage"`)
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(response.Err(401, "未登录或token已过期"))
}
