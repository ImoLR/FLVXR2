package ws

import "strings"

// Plan 168: entry speed limits in the agent's kernel (tc).

// EntryRateReport is the agent's entry shaper status (sent only on change).
type EntryRateReport struct {
	State   string `json:"state"` // off | on | error
	Error   string `json:"error,omitempty"`
	Classes int    `json:"classes"`
	Ports   int    `json:"ports"`
}

// Entry rate modes stored on the node (node.entry_rate_tc).
const (
	EntryRateModeOld    = 0 // agent without the capability: gost limiter / nftables policer
	EntryRateModeTc     = 1 // limits in the kernel (tc)
	EntryRateModeKernel = 2 // agent capable, kernel cannot (LXC / OpenVZ / module): old path
)

func entryRateMode(ns *nodeSession) int {
	switch {
	case ns == nil || ns.entryRateTc <= 0:
		return EntryRateModeOld
	case ns.entryRateTcErr != "":
		return EntryRateModeKernel
	}
	return EntryRateModeTc
}

// EntryRateTcCapable: the node's current agent session takes SetEntryRates and
// its kernel can shape (otherwise the panel keeps the gost / nft path).
func (s *Server) EntryRateTcCapable(nodeID int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return entryRateMode(s.nodes[nodeID]) == EntryRateModeTc
}

// recordEntryRate stores a runtime error of the shaper ("" clears) when the
// reported status changed (read loop only; one conditional write).
func (s *Server) recordEntryRate(ns *nodeSession, r *EntryRateReport) {
	if r == nil || ns == nil || entryRateMode(ns) != EntryRateModeTc {
		return
	}
	text := ""
	if r.State == "error" {
		text = strings.TrimSpace(r.Error)
		if text == "" {
			text = "限速未完全生效"
		}
	}
	if ns.entryRateKey == "k:"+text {
		return
	}
	if s.repo != nil && s.repo.SetNodeEntryRateCapability(ns.nodeID, EntryRateModeTc, text) == nil {
		ns.entryRateKey = "k:" + text
	}
}
