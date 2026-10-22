package handler

import (
	"log"
	"net/http"
	"strings"
	"time"

	"go-backend/internal/authstate"
	"go-backend/internal/clientip"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/ratelimit"
	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

// Plan 135 auth hardening: disabled/deleted users lose their tokens, password
// guessing is rate limited, passwords move from unsalted MD5 to bcrypt.

const (
	loginRefusedMsg = "登录尝试过于频繁，请稍后再试"
	loginRefusedRC  = 429
)

type authGuard struct {
	state *authstate.Cache
	// Every password check (login, subscription URL, change password) takes
	// one token from the caller's address and one from the account; a correct
	// password gives both back, so only failures count.
	byIP     ratelimit.Bucket
	byUser   ratelimit.Bucket
	refusals ratelimit.Refusals
}

func newAuthGuard(lookup authstate.Lookup) authGuard {
	return authGuard{
		state: authstate.New(lookup, 5*time.Second),
		// 20 failures at once, then one more every 30 s (~120/h) per address.
		byIP: ratelimit.Bucket{Burst: 20, Every: 30 * time.Second, MaxKeys: 10000},
		// 10 failures at once, then one per minute per account: a botnet gets
		// at most ~1440 guesses/day at one account, a person never notices.
		byUser:   ratelimit.Bucket{Burst: 10, Every: time.Minute, MaxKeys: 10000},
		refusals: ratelimit.Refusals{Name: "login rate limit"},
	}
}

// UserState is shared with the JWT middleware and the WebSocket server.
func (h *Handler) UserState() *authstate.Cache {
	if h == nil {
		return nil
	}
	return h.auth.state
}

func (h *Handler) invalidateUserAuth(ids ...int64) {
	for _, id := range ids {
		h.auth.state.Invalidate(id)
	}
}

func loginUserKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// passwordAttempt reserves one try for (address, account). ok=false means the
// caller must answer loginRefusedMsg without touching the database.
func (h *Handler) passwordAttempt(w http.ResponseWriter, r *http.Request, username string) (ipKey, userKey string, ok bool) {
	ipKey = clientip.LimitKey(r)
	userKey = loginUserKey(username)
	if !h.auth.byIP.Take(ipKey) {
		h.auth.refusals.Note("per_ip")
		middleware.SuppressRequestLog(w)
		return ipKey, userKey, false
	}
	if !h.auth.byUser.Take(userKey) {
		h.auth.byIP.Refund(ipKey)
		h.auth.refusals.Note("per_account")
		middleware.SuppressRequestLog(w)
		return ipKey, userKey, false
	}
	return ipKey, userKey, true
}

func (h *Handler) passwordAttemptSucceeded(ipKey, userKey string) {
	h.auth.byIP.Refund(ipKey)
	h.auth.byUser.Refund(userKey)
}

// verifyUserPassword checks password and transparently upgrades a legacy MD5
// hash to bcrypt on success.
func (h *Handler) verifyUserPassword(user *model.User, password string) bool {
	if user == nil {
		return false
	}
	ok, legacy := security.VerifyPassword(user.Pwd, password)
	if !ok {
		return false
	}
	if legacy {
		if hash, err := security.HashPassword(password); err == nil {
			if err := h.repo.UpgradeUserPasswordHash(user.ID, user.Pwd, hash); err != nil {
				log.Printf("password hash upgrade failed user=%d: %v", user.ID, err)
			} else {
				user.Pwd = hash
			}
		}
	}
	return true
}

func writeLoginRefused(w http.ResponseWriter) {
	response.WriteJSON(w, response.Err(loginRefusedRC, loginRefusedMsg))
}

// wsUserScope is the WebSocket counterpart of the monitor REST scope: a
// normal user sees the nodes of their tunnels (all nodes with a full-access
// monitor grant), nothing without a monitor grant.
func (h *Handler) wsUserScope(userID int64) (ws.NodeScope, error) {
	perm, err := h.repo.GetMonitorPermission(userID)
	if err != nil {
		return ws.NodeScope{}, err
	}
	if perm == nil {
		return ws.NodeScope{}, nil
	}
	if perm.FullAccess == 1 {
		return ws.NodeScope{All: true}, nil
	}
	scope := ws.NodeScope{Nodes: map[int64]struct{}{}}
	tunnelIDs, err := h.repo.ListUserTunnelIDSet(userID)
	if err != nil {
		return ws.NodeScope{}, err
	}
	if len(tunnelIDs) == 0 {
		return scope, nil
	}
	nodeIDs, err := h.repo.ListNodeIDsByTunnelIDs(userID, tunnelIDs)
	if err != nil {
		return ws.NodeScope{}, err
	}
	for _, id := range nodeIDs {
		scope.Nodes[id] = struct{}{}
	}
	return scope, nil
}

// ─── vite_config exposure (F02) ─────────────────────────────────────

// Keys the login / register / public status pages and the menu read before
// or without admin rights. Everything else in vite_config is admin data.
var publicConfigKeys = map[string]bool{
	"app_name":                 true,
	"app_logo":                 true,
	"app_favicon":              true,
	"app_bg_image":             true,
	"global_app_bg_image":      true,
	"registration_enabled":     true,
	"register_captcha_enabled": true,
	"captcha_enabled":          true,
	"captcha_type":             true,
	"cloudflare_site_key":      true,
	"login_monitor_link":       true,
	"payment_enabled":          true,
	"store_enabled":            true,
}

// Extra keys logged-in normal users read (rules page layout switch).
var userConfigKeys = map[string]bool{
	"forward_compact_mode": true,
}

// configVisibility: 0 anonymous, 1 logged-in user, 2 admin.
func (h *Handler) configVisibility(r *http.Request) int {
	claims, _, ok := h.optionalClaimsFromRequest(r)
	if !ok {
		return 0
	}
	if claims.RoleID == 0 {
		return 2
	}
	return 1
}

func configKeyVisible(name string, level int) bool {
	if level >= 2 {
		return true
	}
	if publicConfigKeys[name] {
		return true
	}
	return level >= 1 && userConfigKeys[name]
}
