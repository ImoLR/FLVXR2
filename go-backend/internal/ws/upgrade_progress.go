package ws

import (
	"encoding/json"
	"time"
)

// UpgradeProgress is one OTA progress report of an agent (plan 248):
// {"type":"UpgradeProgress","message":…,"data":{"stage":…,"percent":…}}.
type UpgradeProgress struct {
	Stage   string
	Percent int
	Message string
}

// SetUpgradeProgressHook receives every UpgradeProgress report (the admin
// broadcast is unchanged). The hook must not block.
func (s *Server) SetUpgradeProgressHook(fn func(nodeID int64, p UpgradeProgress)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onUpgradeProgress = fn
	s.mu.Unlock()
}

func (s *Server) reportUpgradeProgress(nodeID int64, msg string) {
	s.mu.RLock()
	hook := s.onUpgradeProgress
	s.mu.RUnlock()
	if hook == nil {
		return
	}
	var parsed struct {
		Message string `json:"message"`
		Data    struct {
			Stage   string `json:"stage"`
			Percent int    `json:"percent"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(msg), &parsed) != nil {
		return
	}
	hook(nodeID, UpgradeProgress{Stage: parsed.Data.Stage, Percent: parsed.Data.Percent, Message: parsed.Message})
}

// BroadcastUpgradeProgress sends a panel-side OTA state to admins in the
// agent's own message shape (the node page shows it like an agent report).
func (s *Server) BroadcastUpgradeProgress(nodeID int64, p UpgradeProgress) {
	if s == nil {
		return
	}
	raw, err := json.Marshal(map[string]interface{}{
		"type":    "UpgradeProgress",
		"success": p.Stage != "failed",
		"message": p.Message,
		"data":    map[string]interface{}{"stage": p.Stage, "percent": p.Percent},
	})
	if err != nil {
		return
	}
	s.broadcastAdminTyped(nodeID, "upgrade_progress", string(raw))
}

// NodeSessionVersion is the agent version the node's current session reported
// at its handshake, and when that session began (ok=false: not connected).
func (s *Server) NodeSessionVersion(nodeID int64) (version string, startedAt time.Time, ok bool) {
	if s == nil {
		return "", time.Time{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := s.nodes[nodeID]
	if ns == nil {
		return "", time.Time{}, false
	}
	return ns.version, ns.startedAt, true
}
