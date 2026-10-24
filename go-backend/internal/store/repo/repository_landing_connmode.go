package repo

import (
	"sort"
	"strconv"
	"strings"

	"go-backend/internal/landingcustom"
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
// the server's.
func LandingNodeConnMode(n *model.LandingNode, s *model.LandingServer) string {
	if n != nil && n.ConnMode != nil {
		return NormalizeLandingConnMode(*n.ConnMode)
	}
	if s == nil {
		return LandingConnTotal
	}
	return NormalizeLandingConnMode(s.ConnMode)
}

// LandingConnModeSupported reports whether the server's runtime applies 仅入站:
// a shared server whose share advertises landing-conn-mode-v1, or a native
// server whose agent reports landing_conn_mode >= 1 (agentVersion).
func LandingConnModeSupported(s *model.LandingServer, agentVersion int) bool {
	if s == nil {
		return false
	}
	if s.Source == "shared" {
		return SharedLandingCaps(s).ConnModes.Supports(landingcustom.ConnModeVersion)
	}
	return agentVersion >= LandingConnModeVersion
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

// NativeLandingConnModeVersions maps native landing server id → the agent's
// landing_conn_mode capability, in one query (views and the monitor).
func (r *Repository) NativeLandingConnModeVersions() (map[int64]int, error) {
	var rows []struct {
		ID              int64
		LandingConnMode int
	}
	err := r.db.Model(&model.LandingServer{}).Select("landing_server.id AS id", "node.landing_conn_mode AS landing_conn_mode").
		Joins("JOIN node ON node.id = landing_server.node_id").Where("landing_server.source = ?", "native").Scan(&rows).Error
	out := make(map[int64]int, len(rows))
	for _, row := range rows {
		out[row.ID] = row.LandingConnMode
	}
	return out, err
}

// LandingConnSupport answers LandingConnModeSupported for any server from one
// NativeLandingConnModeVersions map.
type LandingConnSupport map[int64]int

func (m LandingConnSupport) Supported(s *model.LandingServer) bool {
	if s == nil {
		return false
	}
	return LandingConnModeSupported(s, m[s.ID])
}
