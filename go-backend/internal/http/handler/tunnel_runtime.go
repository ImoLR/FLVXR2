package handler

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"go-backend/internal/http/client"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

var errTunnelRuntimeInactive = errors.New("隧道已删除、停用或不再包含该节点")

func (h *Handler) lockTunnelRuntime(tunnelID int64) func() {
	value, _ := h.tunnelRuntimeLocks.LoadOrStore(tunnelID, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// A newly inserted tunnel still owns the database transaction. Never wait for
// a runtime holder that may itself be waiting for that transaction to finish.
func (h *Handler) tryLockTunnelRuntime(tunnelID int64) (func(), bool) {
	value, _ := h.tunnelRuntimeLocks.LoadOrStore(tunnelID, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

func tunnelRuntimeNodeOrder(state *tunnelCreateState) []int64 {
	var ids []int64
	seen := make(map[int64]struct{})
	appendNodes := func(nodes []tunnelRuntimeNode) {
		for _, node := range nodes {
			if _, ok := seen[node.NodeID]; !ok {
				seen[node.NodeID] = struct{}{}
				ids = append(ids, node.NodeID)
			}
		}
	}
	appendNodes(state.OutNodes)
	for i := len(state.ChainHops) - 1; i >= 0; i-- {
		appendNodes(state.ChainHops[i])
	}
	appendNodes(state.InNodes)
	return ids
}

func (h *Handler) redeployTunnelRuntimeOnNode(tunnelID, nodeID int64) error {
	unlock := h.lockTunnelRuntime(tunnelID)
	defer unlock()
	return h.redeployTunnelRuntimeOnNodeLocked(tunnelID, nodeID)
}

// The caller holds the tunnel lock through any following forward-service sync.
// In particular, a local reconnect must not release or rebind federation peers.
func (h *Handler) redeployTunnelRuntimeOnNodeLocked(tunnelID, nodeID int64) error {
	_, err := h.redeployTunnelRuntimeOnNodeDiffLocked(tunnelID, nodeID, nil)
	return err
}

// redeployTunnelRuntimeOnNodeDiffLocked is redeployTunnelRuntimeOnNodeLocked
// for a reconnect: with the agent's runtime inventory it sends nothing when
// the agent already runs exactly this tunnel's objects (changed=false). A nil
// inventory (older agent, failed listing) always redeploys.
func (h *Handler) redeployTunnelRuntimeOnNodeDiffLocked(tunnelID, nodeID int64, inv *agentRuntimeInventory) (bool, error) {
	tunnel, err := h.getTunnelRecord(tunnelID)
	if err != nil {
		if errors.Is(err, errTunnelRuntimeInactive) || strings.Contains(err.Error(), "不存在") {
			return false, errTunnelRuntimeInactive
		}
		return false, err
	}
	if tunnel.Type != 2 || tunnel.Status != 1 {
		return false, errTunnelRuntimeInactive
	}
	state, err := h.reconstructTunnelState(tunnelID)
	if err != nil {
		return true, err
	}
	if _, ok := state.Nodes[nodeID]; !ok {
		return false, errTunnelRuntimeInactive
	}
	if err := h.restoreFederationRuntimePorts(state); err != nil {
		return true, err
	}
	plan, err := h.buildTunnelRuntimePlan(state, nodeID)
	if err != nil {
		return true, err
	}
	if inv != nil && !plan.federation && inv.matches(plan.expectedRevs()) {
		return false, nil
	}
	// Plan 165: only the chain differs (e.g. the agent still runs a 最优 order
	// from before a panel restart) on an agent that swaps chains without a
	// gap → send just the chain; the relay service keeps listening.
	if inv != nil && !plan.federation && plan.chainData != nil && h.chainSwapCapable(nodeID) &&
		(plan.serviceData == nil || inv.matches(ws.ExpectedRuntimeRevs("AddService", plan.serviceData))) {
		if _, err := h.sendNodeCommand(nodeID, "UpdateChains", map[string]interface{}{"chain": fmt.Sprintf("chains_%d", state.TunnelID), "data": plan.chainData}, false, false); err != nil {
			return true, plan.failed("下发转发链", err)
		}
		return true, nil
	}
	return true, h.applyTunnelRuntimePlan(state, plan)
}

// A remote reservation may have changed its port during an earlier full
// redeploy. Reconnects reuse that binding without contacting federation peers.
func (h *Handler) restoreFederationRuntimePorts(state *tunnelCreateState) error {
	hasRemote := false
	for _, node := range state.Nodes {
		if node != nil && node.IsRemote == 1 {
			hasRemote = true
			break
		}
	}
	if !hasRemote {
		return nil
	}
	bindings, err := h.repo.ListActiveFederationTunnelBindingsByTunnel(state.TunnelID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		node := state.Nodes[binding.NodeID]
		if node == nil || node.IsRemote != 1 || binding.AllocatedPort <= 0 {
			continue
		}
		if binding.ChainType == 3 && binding.HopInx == 0 {
			for i := range state.OutNodes {
				if state.OutNodes[i].NodeID == binding.NodeID {
					state.OutNodes[i].Port = binding.AllocatedPort
				}
			}
		}
		if binding.ChainType == 2 && binding.HopInx > 0 && binding.HopInx <= len(state.ChainHops) {
			for i := range state.ChainHops[binding.HopInx-1] {
				if state.ChainHops[binding.HopInx-1][i].NodeID == binding.NodeID {
					state.ChainHops[binding.HopInx-1][i].Port = binding.AllocatedPort
				}
			}
		}
	}
	return nil
}

// replaceTunnelRuntimeOnNode must be called with the tunnel lock held. Build
// payloads before deleting anything, and finish this node before touching another.
func (h *Handler) replaceTunnelRuntimeOnNode(state *tunnelCreateState, nodeID int64) error {
	plan, err := h.buildTunnelRuntimePlan(state, nodeID)
	if err != nil {
		return err
	}
	return h.applyTunnelRuntimePlan(state, plan)
}

// tunnelRuntimePlan is what one node runs for one tunnel: the chain to the
// next hop (entry/middle) and the relay service (middle/exit).
type tunnelRuntimePlan struct {
	nodeID      int64
	node        *nodeRecord
	role        string
	runtimeNode tunnelRuntimeNode
	federation  bool // remote role: federation protocol, never diffed
	chainData   map[string]interface{}
	serviceData []map[string]interface{}
	targets     []tunnelRuntimeNode // next layer in DB order (chain owners)
}

func (p *tunnelRuntimePlan) failed(action string, err error) error {
	return fmt.Errorf("%s %s(%d) %s失败: %w", p.role, nodeDisplayName(p.node), p.nodeID, action, err)
}

// expectedRevs are the runtime revisions an agent holds after the plan was
// applied (see ws.ExpectedRuntimeRevs).
func (p *tunnelRuntimePlan) expectedRevs() map[string]string {
	out := map[string]string{}
	if p.chainData != nil {
		for k, v := range ws.ExpectedRuntimeRevs("AddChains", p.chainData) {
			out[k] = v
		}
	}
	if p.serviceData != nil {
		for k, v := range ws.ExpectedRuntimeRevs("AddService", p.serviceData) {
			out[k] = v
		}
	}
	return out
}

func (h *Handler) buildTunnelRuntimePlan(state *tunnelCreateState, nodeID int64) (*tunnelRuntimePlan, error) {
	node := state.Nodes[nodeID]
	if node == nil {
		return nil, fmt.Errorf("节点 %d 不存在", nodeID)
	}
	plan := &tunnelRuntimePlan{nodeID: nodeID, node: node}
	var targets []tunnelRuntimeNode
	for _, out := range state.OutNodes {
		if out.NodeID == nodeID {
			plan.runtimeNode, plan.role = out, "出口节点"
			break
		}
	}
	if plan.role == "" {
		for i, hop := range state.ChainHops {
			for _, middle := range hop {
				if middle.NodeID == nodeID {
					plan.runtimeNode, plan.role = middle, "转发链节点"
					targets = state.OutNodes
					if i+1 < len(state.ChainHops) {
						targets = state.ChainHops[i+1]
					}
					break
				}
			}
			if plan.role != "" {
				break
			}
		}
	}
	if plan.role == "" {
		for _, entry := range state.InNodes {
			if entry.NodeID == nodeID {
				plan.runtimeNode, plan.role = entry, "入口节点"
				var err error
				targets, err = leiKwanEntryTargets(state)
				if err != nil {
					return nil, err
				}
				break
			}
		}
	}
	if plan.role == "" {
		return nil, errTunnelRuntimeInactive
	}
	if node.IsRemote == 1 && plan.runtimeNode.ChainType != 1 {
		plan.federation = true
		return plan, nil
	}
	plan.targets = targets
	if plan.runtimeNode.ChainType != 3 {
		chainData, err := h.tunnelChainConfigFor(state, nodeID, targets)
		if err != nil {
			return nil, plan.failed("生成转发链", err)
		}
		plan.chainData = chainData
	}
	if plan.runtimeNode.ChainType != 1 {
		targetCount := len(targets)
		if plan.runtimeNode.ChainType == 3 {
			targetCount = 1
		}
		plan.serviceData = buildTunnelChainServiceConfig(state.TunnelID, plan.runtimeNode, node, targetCount)
	}
	return plan, nil
}

func (h *Handler) applyTunnelRuntimePlan(state *tunnelCreateState, plan *tunnelRuntimePlan) error {
	nodeID := plan.nodeID
	if plan.federation {
		if err := h.replaceFederationRuntimeOnNode(state, nodeID); err != nil {
			return plan.failed("运行时下发", err)
		}
		return nil
	}
	// Plan 165: agents that swap chains without a gap get one UpdateChains
	// (no moment without the chain, in which dials would go direct); older
	// agents keep DeleteChains + AddChains.
	chainName := fmt.Sprintf("chains_%d", state.TunnelID)
	swap := plan.chainData != nil && h.chainSwapCapable(nodeID)
	if plan.chainData != nil && !swap {
		if _, err := h.sendNodeCommand(nodeID, "DeleteChains", map[string]interface{}{"chain": chainName}, false, true); err != nil {
			return plan.failed("清理转发链", err)
		}
	}
	if plan.serviceData != nil {
		if _, err := h.sendNodeCommand(nodeID, "DeleteService", map[string]interface{}{"services": []string{fmt.Sprintf("%d_tls", state.TunnelID)}}, false, true); err != nil {
			return plan.failed("清理服务", err)
		}
	}
	if swap {
		if _, err := h.sendNodeCommand(nodeID, "UpdateChains", map[string]interface{}{"chain": chainName, "data": plan.chainData}, false, false); err != nil {
			return plan.failed("下发转发链", err)
		}
	} else if plan.chainData != nil {
		if _, err := h.sendNodeCommand(nodeID, "AddChains", plan.chainData, true, false); err != nil {
			return plan.failed("下发转发链", err)
		}
	}
	if plan.serviceData != nil {
		if err := h.addTunnelServiceOnNode(nodeID, state.TunnelID, plan.serviceData); err != nil {
			return plan.failed("下发服务", err)
		}
	}
	return nil
}

// Remote roles use the existing federation release/reserve/apply protocol, but
// only for this node. A local failure never releases successful remote roles.
func (h *Handler) replaceFederationRuntimeOnNode(state *tunnelCreateState, nodeID int64) error {
	bindings, err := h.repo.ListActiveFederationTunnelBindingsByTunnel(state.TunnelID)
	if err != nil {
		return err
	}
	node := state.Nodes[nodeID]
	if strings.TrimSpace(node.RemoteURL) == "" || strings.TrimSpace(node.RemoteToken) == "" {
		return errors.New("远程节点缺少共享配置")
	}
	kept := make([]repo.FederationTunnelBinding, 0, len(bindings))
	fc := client.NewFederationClient()
	localDomain := h.federationLocalDomain()
	for _, binding := range bindings {
		if binding.NodeID != nodeID {
			kept = append(kept, binding)
			continue
		}
		if err := fc.ReleaseRole(node.RemoteURL, node.RemoteToken, localDomain, client.RuntimeReleaseRoleRequest{
			BindingID:   strings.TrimSpace(binding.RemoteBindingID),
			ResourceKey: strings.TrimSpace(binding.ResourceKey),
		}); err != nil {
			return err
		}
	}
	applied, refs, err := h.applyFederationRuntime(state, localDomain, nodeID)
	if err != nil {
		return err
	}
	kept = append(kept, applied...)
	tx := h.repo.BeginTx()
	if tx.Error != nil {
		h.releaseFederationRuntimeRefs(refs)
		return tx.Error
	}
	if err := h.repo.ReplaceFederationTunnelBindingsTx(tx, state.TunnelID, kept); err != nil {
		tx.Rollback()
		h.releaseFederationRuntimeRefs(refs)
		return err
	}
	if err := tx.Commit().Error; err != nil {
		h.releaseFederationRuntimeRefs(refs)
		return err
	}
	return nil
}
