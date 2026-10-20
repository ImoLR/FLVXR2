package handler

import (
	"errors"
	"fmt"
	"log"
	"strings"
)

// cleanupForwardRuntime is shared by single and cascading deletes. Keep the
// forward, ports and user grants until all nodes have been attempted, including
// paused rules. One failed node must not prevent cleanup on the others.
func (h *Handler) cleanupForwardRuntime(forward *forwardRecord) error {
	ports, err := h.listForwardPorts(forward.ID)
	if err != nil {
		return err
	}
	var errs []error
	record := func(nodeID int64, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("node %d: %w", nodeID, err))
		}
	}
	switch {
	case strings.EqualFold(forward.Mode, "wg_path"):
		nodes, err := h.wgPathNodeOrder(forward.WGPathID)
		if err != nil {
			errs = append(errs, err)
		} else if len(nodes) < 2 {
			errs = append(errs, errors.New("WG 隧道链路不完整"))
		} else {
			for _, nodeID := range uniqueInt64s([]int64{nodes[0], nodes[len(nodes)-1]}) {
				record(nodeID, h.syncWGForwardRuleOnNode(forward, ports, "DeleteService", nodeID))
			}
		}
	case strings.EqualFold(forward.Mode, "nftables"):
		if err := h.deleteNftablesRules(forward, ports); err != nil {
			errs = append(errs, err)
		}
		// Retain the single-delete fallback for leftover services after a mode change.
		bases, err := h.forwardServiceBaseCandidates(forward)
		if err != nil {
			errs = append(errs, err)
		} else {
			for _, nodeID := range forwardPortNodeIDs(ports) {
				record(nodeID, h.deleteForwardServiceBasesOnNode(nodeID, bases))
			}
		}
	default:
		for _, nodeID := range forwardPortNodeIDs(ports) {
			record(nodeID, h.deleteForwardServicesOnNodeBatch(forward, nodeID))
		}
	}
	h.deleteForwardDynamicLimiter(forward)
	return errors.Join(errs...)
}

func (h *Handler) cleanupForwardRuntimeBestEffort(forward *forwardRecord) {
	if err := h.cleanupForwardRuntime(forward); err != nil {
		log.Printf("[forward-delete] forward %d runtime cleanup: %v", forward.ID, err)
	}
}

func (h *Handler) deleteUserAndCleanup(userID int64) error {
	ids, err := h.repo.ListForwardIDsByUser(userID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		forward, err := h.getForwardRecord(id)
		if err != nil {
			return err
		}
		h.cleanupForwardRuntimeBestEffort(forward)
	}
	landings, err := h.repo.ListLandingNodes(userID, false)
	if err != nil {
		return err
	}
	chainServers, _ := h.repo.ChainServerIDsByUser(userID)
	if err = h.repo.DeleteUserCascade(userID); err != nil {
		return err
	}
	h.syncChainServersAsync(chainServers)
	seen := map[int64]bool{}
	for _, n := range landings {
		if !seen[n.LandingServerID] {
			h.syncLandingServer(n.LandingServerID)
			seen[n.LandingServerID] = true
		}
	}
	return nil
}
