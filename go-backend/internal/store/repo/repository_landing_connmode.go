package repo

import (
	"sort"
	"strconv"
	"strings"

	"go-backend/internal/store/model"
)

// Plan 141: a landing connection limit counts either every TCP socket of the
// node's port in any state (入站+出站, Core max_total_connections, the only
// behaviour before) or only accepted client connections (仅入站, Core
// max_inbound_connections, the same meaning as a rule's 连接数).
const (
	LandingConnTotal   = "total"
	LandingConnInbound = "inbound"
	// LandingConnModeVersion is the agent capability (landing_conn_mode) and
	// the 妙妙屋 contract version (landing-conn-mode-v1) that apply 仅入站.
	LandingConnModeVersion = 1
)

// NormalizeLandingConnMode maps anything but "inbound" (including the empty
// value of rows written before plan 141) to "total".
func NormalizeLandingConnMode(mode string) string {
	if mode == LandingConnInbound {
		return LandingConnInbound
	}
	return LandingConnTotal
}

func validLandingConnMode(mode string) bool {
	return mode == "" || mode == LandingConnTotal || mode == LandingConnInbound
}

// LandingNodeConnMode is the node's configured 计算方式: its own override, else
// the server's. Plan 147: a shared (妙妙屋) server always counts 入站+出站 —
// the setting is FLVX-native only (user 10-24: 妙妙屋侧不动), whatever a row
// may hold.
func LandingNodeConnMode(n *model.LandingNode, s *model.LandingServer) string {
	if s != nil && s.Source == "shared" {
		return LandingConnTotal
	}
	if n != nil && n.ConnMode != nil {
		return NormalizeLandingConnMode(*n.ConnMode)
	}
	if s == nil {
		return LandingConnTotal
	}
	return NormalizeLandingConnMode(s.ConnMode)
}

// LandingConnModeSupported reports whether the server's runtime applies 仅入站:
// a native server whose agent reports landing_conn_mode >= 1 (agentVersion).
// Plan 147: never a shared server — FLVX does not offer the setting there and
// never sends conn_mode to Custom, even if a share advertised
// landing-conn-mode-v1 (contract 暂缓).
func LandingConnModeSupported(s *model.LandingServer, agentVersion int) bool {
	if s == nil || s.Source == "shared" {
		return false
	}
	return agentVersion >= LandingConnModeVersion
}

// LandingConnModeFixed: the server has no 计算方式 choice (shared = 妙妙屋,
// fixed 入站+出站, plan 147).
func LandingConnModeFixed(s *model.LandingServer) bool {
	return s != nil && s.Source == "shared"
}

// LandingEffectiveConnMode is the mode the runtime actually applies: 仅入站 only
// where supported, otherwise 入站+出站 exactly as before (old agent / Helper).
func LandingEffectiveConnMode(n *model.LandingNode, s *model.LandingServer, supported bool) string {
	if supported && LandingNodeConnMode(n, s) == LandingConnInbound {
		return LandingConnInbound
	}
	return LandingConnTotal
}

// LandingServerTotalConnMode is the effective mode of the server total: only a
// native server's total can be 仅入站 (a shared total belongs to 妙妙屋).
func LandingServerTotalConnMode(s *model.LandingServer, supported bool) string {
	if s != nil && s.Source != "shared" && supported && NormalizeLandingConnMode(s.MaxConnMode) == LandingConnInbound {
		return LandingConnInbound
	}
	return LandingConnTotal
}

// LandingModeConnections is the node's live connection count in mode: the
// Core's inbound (client) connections for 仅入站, all port sockets otherwise.
// A node the 妙妙屋 side reports removed counts 0 (LandingLiveConnections).
func LandingModeConnections(n *model.LandingNode, mode string) int {
	if n.RemoteState == "removed" {
		return 0
	}
	if mode == LandingConnInbound {
		return n.InboundConnections
	}
	return n.Connections
}

// NativeLandingConnSig is the 仅入站 signature of a native payload: the sorted
// ids of nodes sent as inbound plus "g" for an inbound server total. Empty
// when nothing is inbound, so servers never switched keep their revision.
func NativeLandingConnSig(inboundNodeIDs []int64, globalInbound bool) string {
	if len(inboundNodeIDs) == 0 && !globalInbound {
		return ""
	}
	ids := append([]int64(nil), inboundNodeIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, 0, len(ids)+1)
	if globalInbound {
		parts = append(parts, "g")
	}
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

// LandingConnSupport maps native landing server id → the agent's
// landing_conn_mode (filled by list queries) and answers
// LandingConnModeSupported for any server.
type LandingConnSupport map[int64]int

func (m LandingConnSupport) Supported(s *model.LandingServer) bool {
	if s == nil {
		return false
	}
	return LandingConnModeSupported(s, m[s.ID])
}
