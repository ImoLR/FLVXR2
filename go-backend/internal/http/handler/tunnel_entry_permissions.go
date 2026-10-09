package handler

import (
	"errors"
	"fmt"
	"log"
	"strings"
)

func (h *Handler) allowedTunnelEntries(userID, tunnelID int64) ([]int64, error) {
	entries, err := h.repo.EffectiveTunnelEntryNodeIDs(userID, tunnelID)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("你没有该隧道入口的权限")
	}
	return entries, nil
}

// Called after the entire grant mutation, never between removing and adding
// groups. Comparing stored ports with the final union also handles overlapping
// grants without disrupting entries that remain authorized.
func (h *Handler) reconcileTunnelEntryPermissions(userID, tunnelID int64) []string {
	pairs, err := h.repo.ListForwardUserTunnelPairs()
	if err != nil {
		return []string{err.Error()}
	}
	var warnings []string
	for _, pair := range pairs {
		if userID > 0 && pair.UserID != userID || tunnelID > 0 && pair.TunnelID != tunnelID {
			continue
		}
		entries, err := h.repo.EffectiveTunnelEntryNodeIDs(pair.UserID, pair.TunnelID)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if len(entries) == 0 {
			h.cleanupForwardsForUserTunnel(pair.UserID, pair.TunnelID)
			continue
		}
		forwards, err := h.repo.ListForwardsByUserAndTunnel(pair.UserID, pair.TunnelID)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		for i := range forwards {
			warnings = append(warnings, h.reconcileForwardEntries(&forwards[i], entries)...)
		}
	}
	for _, warning := range warnings {
		log.Printf("[tunnel-entries] %s", warning)
	}
	return warnings
}

func (h *Handler) reconcileForwardEntries(f *forwardRecord, allowed []int64) []string {
	old, err := h.listForwardPorts(f.ID)
	if err != nil {
		return []string{err.Error()}
	}
	oldIDs := forwardPortNodeIDs(old)
	removed, gained := diffInt64s(oldIDs, allowed), diffInt64s(allowed, oldIDs)
	if len(removed) == 0 && len(gained) == 0 {
		return nil
	}
	var warnings []string
	warn := func(err error) {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("转发 %s 入口同步失败: %v", f.Name, err))
		}
	}
	ports := make([]forwardPortRecord, 0, len(allowed))
	for _, fp := range old {
		if len(diffInt64s([]int64{fp.NodeID}, allowed)) == 0 {
			ports = append(ports, fp)
			continue
		}
		if strings.EqualFold(f.Mode, "nftables") {
			warn(h.deleteNftablesRules(f, []forwardPortRecord{fp}))
		} else {
			warn(h.deleteForwardServicesOnNode(f, fp.NodeID))
		}
	}
	added := make([]int64, 0, len(gained))
	for _, nodeID := range gained {
		port := h.resolvePortForNewEntryNode(nodeID, pickForwardPortFromRecords(old), f.ID)
		node, err := h.getNodeRecord(nodeID)
		if err == nil && port <= 0 {
			err = errors.New("无可用端口")
		}
		if err == nil {
			err = validateLocalNodePort(node, port)
		}
		if err == nil {
			err = validateRemoteNodePort(node, port)
		}
		if err == nil {
			err = h.validateForwardPortAvailability(node, port, f.ID)
		}
		if err != nil {
			warn(fmt.Errorf("节点 %d: %w", nodeID, err))
			continue
		}
		ports = append(ports, forwardPortRecord{NodeID: nodeID, Port: port})
		added = append(added, nodeID)
	}
	if err := h.repo.ReconcileForwardEntryPorts(f.ID, ports); err != nil {
		warn(err)
		return warnings
	}
	if len(ports) == 0 {
		// All old entries were revoked and allocation on the remaining allowed
		// entries failed. Use the existing revoked-rule deletion path.
		warn(h.deleteForwardByID(f.ID))
		return warnings
	}
	if f.Status == 1 {
		for _, nodeID := range added {
			ws, err := h.syncForwardServicesWithWarningsOnNode(f, "UpdateService", true, nodeID)
			warnings = append(warnings, ws...)
			warn(err)
		}
	}
	return warnings
}
