package handler

import (
	"context"
	"encoding/json"
	"log"
	"time"
)

func (h *Handler) runLandingReconcileLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			h.reconcileLandingPolicy(time.Now())
			// Full re-push repairs agent reconnects and interrupted hot applies. Identical
			// revisions are idempotent and do not restart the Core.
			servers, err := h.repo.ListLandingServers(0, true)
			if err == nil {
				for _, s := range servers {
					select {
					case <-ctx.Done():
						return
					default:
					}
					h.syncLandingServer(s.ID)
				}
			}
			timer.Reset(time.Minute)
		}
	}
}

func (h *Handler) reconcileLandingPolicy(now time.Time) {
	if h == nil || h.repo == nil {
		return
	}
	h.landingPolicyMu.Lock()
	defer h.landingPolicyMu.Unlock()
	result, err := h.repo.ReconcileLandingPolicy(now)
	if err != nil {
		log.Printf("landing policy reconciliation failed: %v", err)
		return
	}
	for _, id := range result.PauseForwardIDs {
		f, err := h.getForwardRecord(id)
		if err != nil || f == nil {
			continue
		}
		if err = h.pauseForwardRuntimeForCNBlock(f); err != nil {
			log.Printf("landing policy pause forward=%d failed: %v", id, err)
		}
	}
	for _, id := range result.ResumeForwardIDs {
		f, err := h.getForwardRecord(id)
		if err != nil || f == nil {
			continue
		}
		if err = h.ensureUserTunnelForwardAllowed(f.UserID, f.TunnelID, now.UnixMilli()); err != nil {
			continue
		}
		if f.CNBlocked || (f.ExpiryTime.Valid && f.ExpiryTime.Int64 > 0 && f.ExpiryTime.Int64 <= now.UnixMilli()) || (f.TrafficLimit > 0 && f.InFlow+f.OutFlow >= f.TrafficLimit*bytesPerGB) {
			continue
		}
		if err = h.checkForwardRecordLanding(context.Background(), f); err != nil {
			continue
		}
		// Rebuild also restores nftables and previously offline agents.
		f.Status = 1
		if err = h.syncForwardServices(f, "UpdateService", true); err != nil {
			continue
		}
		if err = h.repo.CompleteLandingForwardResume(id); err != nil {
			log.Printf("landing policy resume forward=%d failed: %v", id, err)
		}
	}
	for _, id := range result.ServerIDs {
		h.syncLandingServer(id)
	}
}

// syncLandingServer sends full desired state, including disabled nodes so credentials
// and ports remain reserved during the renewal grace period. No secrets are logged.
func (h *Handler) syncLandingServer(serverID int64) {
	if h == nil || h.repo == nil {
		return
	}
	h.landingSyncMu.Lock()
	defer h.landingSyncMu.Unlock()
	s, err := h.repo.GetLandingServer(serverID)
	if err != nil || s == nil || s.Source != "native" || s.NodeID == 36 {
		return
	}
	node, err := h.repo.GetNodeByID(s.NodeID)
	if err != nil || node == nil || !node.LandingCapable {
		return
	}
	nodes, err := h.repo.ListLandingNodes(0, true)
	if err != nil {
		return
	}
	desiredNodes := []map[string]interface{}{}
	for _, n := range nodes {
		if n.LandingServerID != s.ID {
			continue
		}
		desired := map[string]interface{}{"id": n.ID, "inbound_tag": n.InboundTag, "protocol": n.Protocol, "port": n.Port, "enabled": n.Status == "active", "max_connections": n.MaxConnections, "max_online_ips": n.MaxOnlineIPs}
		if n.Protocol == "ss2022" {
			desired["ss2022"] = map[string]interface{}{"method": n.Method, "server_key": n.ServerKey, "user_key": n.UserKey}
		}
		if n.Protocol == "vless_reality_vision" {
			var names []string
			_ = json.Unmarshal([]byte(n.ServerNames), &names)
			desired["vless"] = map[string]interface{}{"uuid": n.UUID, "flow": n.Flow, "reality": map[string]interface{}{"dest": n.Dest, "server_names": names, "private_key": n.PrivateKey, "short_ids": []string{n.ShortID}}}
		}
		desiredNodes = append(desiredNodes, desired)
	}
	payload := map[string]interface{}{"revision": s.Revision, "enabled": s.Status == "active", "max_connections": s.MaxConnections, "core_memory_mb": s.CoreMemoryMB, "core_cpu_percent": s.CoreCPUPercent, "nodes": desiredNodes}
	result, err := h.sendNodeCommandWithTimeout(s.NodeID, "ApplyLandingDesired", payload, 30*time.Second, false, false)
	if err != nil || !result.Success {
		log.Printf("landing desired sync pending server=%d node=%d", s.ID, s.NodeID)
	}
}
