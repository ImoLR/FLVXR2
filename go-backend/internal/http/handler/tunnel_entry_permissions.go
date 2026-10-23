package handler

import (
	"errors"
	"fmt"
	"log"
	"strings"
)

func (h *Handler) allowedTunnelEntries(userID, tunnelID int64, admin ...bool) ([]int64, error) {
	entries, restricted, err := h.repo.TunnelEntryRestriction(userID, tunnelID, len(admin) > 0 && admin[0])
	if err != nil {
		return nil, err
	}
	if !restricted {
		entries, _ = h.tunnelEntryNodeIDs(tunnelID)
		return entries, nil
	}
	if len(entries) == 0 {
		return nil, errors.New("你没有该隧道入口的权限")
	}
	return entries, nil
}

func forwardOwnerLabel(f *forwardRecord) string {
	if f == nil {
		return "未知用户"
	}
	if name := strings.TrimSpace(f.UserName); name != "" {
		return name
	}
	return fmt.Sprintf("#%d", f.UserID)
}

// ensureForwardOwnerTunnelAccess: an admin moving someone else's rule (tunnel
// delete migration, batch change tunnel) must not put it on a tunnel its owner
// is not permitted on — the owner could no longer edit it. Admin-owned rules
// are unrestricted. Returns the entries the owner may use.
func (h *Handler) ensureForwardOwnerTunnelAccess(f *forwardRecord, tunnelID int64) ([]int64, error) {
	if f == nil {
		return nil, errors.New("转发不存在")
	}
	owner, err := h.repo.GetUserByID(f.UserID)
	if err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, fmt.Errorf("规则所属用户「%s」不存在，无法迁移", forwardOwnerLabel(f))
	}
	if owner.RoleID != 0 {
		ok, err := h.repo.UserTunnelExistsByUserAndTunnel(owner.ID, tunnelID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("用户「%s」没有目标隧道的权限", forwardOwnerLabel(f))
		}
	}
	entries, err := h.allowedTunnelEntries(f.UserID, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("用户「%s」：%s", forwardOwnerLabel(f), err.Error())
	}
	return entries, nil
}

// tunnelMigrationTargets lists enabled tunnels (outside exclude) that every
// owner of the given rules may use: tunnel grant plus at least one permitted
// entry. Admin-only, computed once when the delete dialog opens.
func (h *Handler) tunnelMigrationTargets(forwards []forwardRecord, exclude map[int64]bool) ([]int64, error) {
	out := make([]int64, 0)
	candidates, err := h.repo.ListEnabledTunnelIDs()
	if err != nil {
		return out, err
	}
	ownerSet := map[int64]bool{}
	owners := make([]int64, 0)
	for _, f := range forwards {
		if !ownerSet[f.UserID] {
			ownerSet[f.UserID] = true
			owners = append(owners, f.UserID)
		}
	}
	roles, grants, err := h.repo.UserTunnelGrantSets(owners)
	if err != nil {
		return out, err
	}
	restricted := make([]int64, 0, len(owners))
	for _, uid := range owners {
		if role, ok := roles[uid]; !ok {
			return out, nil // a rule of a deleted user cannot be migrated anywhere
		} else if role != 0 {
			restricted = append(restricted, uid)
		}
	}
next:
	for _, tid := range candidates {
		if exclude[tid] {
			continue
		}
		for _, uid := range restricted {
			if !grants[uid][tid] {
				continue next
			}
		}
		for _, uid := range restricted {
			if entries, err := h.allowedTunnelEntries(uid, tid); err != nil || len(entries) == 0 {
				continue next
			}
		}
		out = append(out, tid)
	}
	return out, nil
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
		entries, restricted, err := h.repo.TunnelEntryRestriction(pair.UserID, pair.TunnelID, false)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if !restricted {
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
