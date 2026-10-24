package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
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
			// Missing WARP devices: one batched pass at most every 5 minutes.
			h.maybeEnsureLandingWarp()
			// Drop monitor samples of deleted landing nodes (every delete path).
			if ids, err := h.repo.ListLandingNodeIDs(); err == nil {
				h.landingRateTracker().retain(ids, time.Now().UnixMilli())
			}
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
					if s.Source == "native" {
						h.syncLandingServer(s.ID)
					}
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
type landingEgressReport struct {
	State    string   `json:"state"`
	Error    string   `json:"error"`
	Ready    bool     `json:"ready"`
	Warnings []string `json:"warnings"`
	Withheld []string `json:"withheld"`
	// Reality guard (plan 127): "" | on | restart_required | error.
	Guard      string `json:"guard"`
	GuardNodes int    `json:"guard_nodes"`
}

type landingSyncSnapshot struct {
	NodeID   int64                 `json:"-"`
	Revision int64                 `json:"revision"`
	Epoch    string                `json:"epoch"`
	State    string                `json:"state"`
	Error    string                `json:"error"`
	Nodes    []repo.LandingCounter `json:"nodes"`
	Egress   *landingEgressReport  `json:"egress"`
	// Plan 129: "restart_required" = the running Core is older than the
	// pinned binary the agent downloaded (absent on older agents).
	CoreUpdate string `json:"core_update"`
	// Plan 147: inbounds the agent could not add (port busy, Core refused);
	// absent on older agents, which fail the whole apply instead.
	NodeErrors []repo.LandingNodeReport `json:"node_errors"`
}

func (h *Handler) syncLandingServer(serverID int64) (*landingSyncSnapshot, error) {
	if h == nil || h.repo == nil {
		return nil, errors.New("落地服务器状态不可用")
	}
	if server, err := h.repo.GetLandingServer(serverID); err == nil && server != nil && server.Source == "shared" {
		return nil, h.syncSharedLandingServer(serverID)
	}
	// Plan 147: a never-used node whose port is busy on the machine moves to a
	// free port; push again right away (each node moves at most 3 times, so
	// this loop is bounded too).
	for attempt := 0; ; attempt++ {
		snapshot, moved, err := h.syncNativeLandingServer(serverID)
		if err != nil || !moved || attempt >= 3 {
			return snapshot, err
		}
	}
}

func (h *Handler) syncNativeLandingServer(serverID int64) (*landingSyncSnapshot, bool, error) {
	snapshot, err := h.syncNativeLandingServerOnce(serverID)
	if err != nil || snapshot == nil {
		return snapshot, false, err
	}
	moved, err := h.repo.ApplyNativeLandingNodeErrors(serverID, snapshot.NodeErrors, time.Now())
	if err != nil {
		log.Printf("landing node errors not recorded server=%d: %v", serverID, err)
		return snapshot, false, nil
	}
	if len(moved) > 0 {
		log.Printf("landing server=%d moved never-used nodes %v off ports busy on the machine", serverID, moved)
	}
	return snapshot, len(moved) > 0, nil
}

func (h *Handler) syncNativeLandingServerOnce(serverID int64) (*landingSyncSnapshot, error) {
	h.landingSyncMu.Lock()
	defer h.landingSyncMu.Unlock()
	s, err := h.repo.GetLandingServer(serverID)
	if err != nil || s == nil || s.Source != "native" || s.NodeID == 36 {
		return nil, errors.New("落地服务器状态不可用")
	}
	node, err := h.repo.GetNodeByID(s.NodeID)
	if err != nil || node == nil || !node.LandingCapable {
		return nil, errors.New("落地服务器状态不可用")
	}
	if node.Status != 1 {
		return nil, errors.New("节点离线，请在节点恢复在线后重试")
	}
	if h.nodeCommandSender == nil && h.wsServer == nil {
		return nil, errors.New("节点连接不可用，请稍后重试")
	}
	// Compile the outbound/routing plan; a content change advances the revision
	// so the agent never sees one revision with two different plans.
	plan, egressWarnings, egressErr := h.compileLandingEgress(s.ID)
	if node.LandingEgress < 1 {
		plan = nil
	}
	nodes, err := h.repo.ListLandingNodes(0, true)
	if err != nil {
		return nil, errors.New("落地服务器状态不可用")
	}
	// Plan 141: 仅入站 only for agents that apply it (landing_conn_mode >= 1);
	// older agents keep today's payload byte for byte (入站+出站). The
	// signature joins the revision so an agent upgrade or a mode change never
	// reuses a revision with new content.
	connSupported := repo.LandingConnModeSupported(s, node.LandingConnMode)
	inboundNodes := map[int64]bool{}
	inboundIDs := []int64{}
	if connSupported {
		for i := range nodes {
			n := &nodes[i]
			if n.LandingServerID == s.ID && !landingcustom.IsExtraProtocol(n.Protocol) && repo.LandingNodeConnMode(n, s) == repo.LandingConnInbound {
				inboundNodes[n.ID] = true
				inboundIDs = append(inboundIDs, n.ID)
			}
		}
	}
	globalInbound := s.MaxConnections > 0 && repo.LandingServerTotalConnMode(s, connSupported) == repo.LandingConnInbound
	connSig := repo.NativeLandingConnSig(inboundIDs, globalInbound)
	// Plan 129: the confirmed one-time Core upgrade restart joins the payload
	// (and the signature, so the revision advances) only while the agent
	// reports an outdated Core; it is never sent to agents that do not.
	upgradeCore := s.CoreUpgradeOK && s.CoreStaleEpoch != ""
	if upgradeCore {
		connSig += ";upgrade_core=" + s.CoreStaleEpoch
	}
	revision, err := h.repo.TouchLandingRevision(s.ID, planHash(plan), &connSig)
	if err != nil {
		return nil, errors.New("落地服务器状态不可用")
	}
	s.Revision = revision
	// Reality guard ports (plan 127) only for agents that apply them; older
	// agents keep today's payload byte for byte.
	guards := map[string]int{}
	if node.LandingGuard >= 1 {
		var guardWarnings []string
		if guards, guardWarnings, err = h.repo.EnsureLandingGuardPorts(s.ID); err != nil {
			return nil, errors.New("落地服务器状态不可用")
		}
		egressWarnings = append(egressWarnings, guardWarnings...)
	}
	desiredNodes := []map[string]interface{}{}
	for _, n := range nodes {
		// The native agent renders SS2022 / VLESS only (plan 121 protocols are
		// refused on native servers; never hand the agent one it cannot apply).
		if n.LandingServerID != s.ID || landingcustom.IsExtraProtocol(n.Protocol) {
			continue
		}
		desired := map[string]interface{}{"id": n.ID, "inbound_tag": n.InboundTag, "protocol": n.Protocol, "port": n.Port, "enabled": n.Status == "active", "max_connections": n.MaxConnections, "max_online_ips": n.MaxOnlineIPs}
		if inboundNodes[n.ID] {
			desired["conn_mode"] = repo.LandingConnInbound
		}
		if n.Protocol == "ss2022" {
			desired["ss2022"] = map[string]interface{}{"method": n.Method, "server_key": n.ServerKey, "user_key": n.UserKey}
		}
		if n.Protocol == "vless_reality_vision" {
			var names []string
			_ = json.Unmarshal([]byte(n.ServerNames), &names)
			desired["vless"] = map[string]interface{}{"uuid": n.UUID, "flow": n.Flow, "reality": map[string]interface{}{"dest": n.Dest, "server_names": names, "private_key": n.PrivateKey, "short_ids": []string{n.ShortID}}}
			if port := guards[n.InboundTag]; port > 0 {
				desired["guard_port"] = port
			}
		}
		desiredNodes = append(desiredNodes, desired)
	}
	// Chain nodes (plan 108) only go to agents that apply listen + chain;
	// an older agent would expose them publicly with a direct outbound.
	if node.LandingChain >= 1 {
		copies, err := h.repo.ChainCopiesForServer(s.ID)
		if err != nil {
			return nil, errors.New("落地服务器状态不可用")
		}
		for _, c := range copies {
			desired := chainDesiredNode(c)
			if port := guards[desired["inbound_tag"].(string)]; port > 0 {
				desired["guard_port"] = port
			}
			desiredNodes = append(desiredNodes, desired)
		}
	}
	payload := map[string]interface{}{"revision": s.Revision, "enabled": s.Status == "active", "max_connections": s.MaxConnections, "core_memory_mb": s.CoreMemoryMB, "core_cpu_percent": s.CoreCPUPercent, "nodes": desiredNodes}
	if globalInbound {
		payload["max_conn_mode"] = repo.LandingConnInbound
	}
	if plan != nil {
		payload["egress"] = plan
	}
	if upgradeCore {
		payload["upgrade_core_epoch"] = s.CoreStaleEpoch
	}
	result, err := h.sendNodeCommandWithTimeout(s.NodeID, "ApplyLandingDesired", payload, 30*time.Second, false, false)
	if err != nil || !result.Success {
		// Same single line as before, now with the agent's short reason (e.g.
		// "landing gRPC adi failed" when a port is taken by another program).
		reason := result.Message
		if err != nil {
			reason = err.Error()
		}
		if r := []rune(reason); len(r) > 120 {
			reason = string(r[:120])
		}
		log.Printf("landing desired sync pending server=%d node=%d reason=%q", s.ID, s.NodeID, reason)
		return nil, errors.New("节点未确认落地状态，请稍后重试")
	}
	raw, err := json.Marshal(result.Data)
	if err != nil {
		return nil, errors.New("节点落地状态格式错误")
	}
	var snapshot landingSyncSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, errors.New("节点落地状态格式错误")
	}
	if snapshot.Revision != s.Revision || snapshot.State == "error" || snapshot.Error != "" {
		return nil, fmt.Errorf("节点尚未应用落地配置 revision=%d", s.Revision)
	}
	snapshot.NodeID = s.NodeID
	for i := range snapshot.Nodes {
		snapshot.Nodes[i].NodeID = s.NodeID
	}
	h.recordNativeEgress(s.ID, snapshot.Egress, egressWarnings, egressErr)
	staleEpoch := ""
	if snapshot.CoreUpdate == "restart_required" {
		staleEpoch = snapshot.Epoch
	}
	if staleEpoch != s.CoreStaleEpoch {
		_ = h.repo.RecordLandingCoreUpdate(s.ID, staleEpoch)
	}
	if snapshot.Egress != nil && snapshot.Egress.Ready && !s.EgressReady && !s.EgressRestartOK && node.LandingEgress >= 1 && h.landingServerReadyNow(s.ID) {
		// First report of a Core with the routing API (e.g. a newly enabled
		// landing): push again right away so the default and global rules
		// (plan 134) and queued node rules apply now, not a minute later.
		h.landingEgressSaveAsync([]int64{s.ID})
	}
	return &snapshot, nil
}

// landingServerReadyNow re-reads the stored readiness (a failed status write
// must not turn the re-push above into a loop).
func (h *Handler) landingServerReadyNow(id int64) bool {
	cur, err := h.repo.GetLandingServer(id)
	return err == nil && cur != nil && cur.EgressReady
}

// recordNativeEgress stores the agent's egress report plus panel-side
// compile warnings/errors (e.g. a WARP device still registering).
func (h *Handler) recordNativeEgress(serverID int64, report *landingEgressReport, compileWarnings []string, compileErr error) {
	if report == nil {
		return
	}
	state, errText := report.State, report.Error
	warnings := append(append([]string{}, compileWarnings...), report.Warnings...)
	if len(report.Withheld) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d 个节点因出站与路由未生效已暂停", len(report.Withheld)))
	}
	if compileErr != nil {
		state, errText = "error", compileErr.Error()
	}
	_ = h.repo.RecordLandingEgressStatus(serverID, state, errText, report.Ready, warnings)
	_ = h.repo.RecordLandingGuardState(serverID, report.Guard, report.GuardNodes)
}

// chainDesiredNode renders one chain copy for the agent: loopback VLESS
// Reality Vision whose only route is the user's node. No limits here: the
// rule's entry service enforces the user's connection and IP caps.
func chainDesiredNode(c repo.ChainCopy) map[string]interface{} {
	id := model.ChainAgentIDBase + c.Node.ID
	var upstream map[string]interface{}
	_ = json.Unmarshal([]byte(c.Chain.Upstream), &upstream)
	if upstream == nil {
		upstream = map[string]interface{}{}
	}
	upstream["tag"] = fmt.Sprintf("flvxc-%d", id)
	upstream["ip_version"] = "system"
	return map[string]interface{}{
		"id": id, "inbound_tag": fmt.Sprintf("flvxl-%d", id), "protocol": "vless_reality_vision", "port": c.Node.Port, "enabled": true,
		"max_connections": 0, "max_online_ips": 0, "listen": "127.0.0.1",
		"vless": map[string]interface{}{"uuid": c.Chain.UUID, "flow": "xtls-rprx-vision", "reality": map[string]interface{}{"dest": c.Chain.Dest, "server_names": repo.ChainServerNames(c.Chain), "private_key": c.Chain.PrivateKey, "short_ids": []string{c.Chain.ShortID}}},
		"chain": upstream,
	}
}
