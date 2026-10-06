package handler

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"go-backend/internal/http/client"
	"go-backend/internal/store/repo"
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
	tunnel, err := h.getTunnelRecord(tunnelID)
	if err != nil {
		if errors.Is(err, errTunnelRuntimeInactive) || strings.Contains(err.Error(), "不存在") {
			return errTunnelRuntimeInactive
		}
		return err
	}
	if tunnel.Type != 2 || tunnel.Status != 1 {
		return errTunnelRuntimeInactive
	}
	state, err := h.reconstructTunnelState(tunnelID)
	if err != nil {
		return err
	}
	if _, ok := state.Nodes[nodeID]; !ok {
		return errTunnelRuntimeInactive
	}
	if err := h.restoreFederationRuntimePorts(state); err != nil {
		return err
	}
	return h.replaceTunnelRuntimeOnNode(state, nodeID)
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
	node := state.Nodes[nodeID]
	if node == nil {
		return fmt.Errorf("节点 %d 不存在", nodeID)
	}
	var runtimeNode tunnelRuntimeNode
	var targets []tunnelRuntimeNode
	role := ""
	for _, out := range state.OutNodes {
		if out.NodeID == nodeID {
			runtimeNode, role = out, "出口节点"
			break
		}
	}
	if role == "" {
		for i, hop := range state.ChainHops {
			for _, middle := range hop {
				if middle.NodeID == nodeID {
					runtimeNode, role = middle, "转发链节点"
					targets = state.OutNodes
					if i+1 < len(state.ChainHops) {
						targets = state.ChainHops[i+1]
					}
					break
				}
			}
			if role != "" {
				break
			}
		}
	}
	if role == "" {
		for _, entry := range state.InNodes {
			if entry.NodeID == nodeID {
				runtimeNode, role = entry, "入口节点"
				targets = state.OutNodes
				if len(state.ChainHops) > 0 {
					targets = state.ChainHops[0]
				}
				break
			}
		}
	}
	if role == "" {
		return errTunnelRuntimeInactive
	}
	failed := func(action string, err error) error {
		return fmt.Errorf("%s %s(%d) %s失败: %w", role, nodeDisplayName(node), nodeID, action, err)
	}
	if node.IsRemote == 1 && runtimeNode.ChainType != 1 {
		if err := h.replaceFederationRuntimeOnNode(state, nodeID); err != nil {
			return failed("运行时下发", err)
		}
		return nil
	}
	var chainData map[string]interface{}
	if runtimeNode.ChainType != 3 {
		var err error
		chainData, err = buildTunnelChainConfig(state.TunnelID, nodeID, targets, state.Nodes, state.IPPreference)
		if err != nil {
			return failed("生成转发链", err)
		}
	}
	var serviceData []map[string]interface{}
	if runtimeNode.ChainType != 1 {
		targetCount := len(targets)
		if runtimeNode.ChainType == 3 {
			targetCount = 1
		}
		serviceData = buildTunnelChainServiceConfig(state.TunnelID, runtimeNode, node, targetCount)
	}
	if chainData != nil {
		if _, err := h.sendNodeCommand(nodeID, "DeleteChains", map[string]interface{}{"chain": fmt.Sprintf("chains_%d", state.TunnelID)}, false, true); err != nil {
			return failed("清理转发链", err)
		}
	}
	if serviceData != nil {
		if _, err := h.sendNodeCommand(nodeID, "DeleteService", map[string]interface{}{"services": []string{fmt.Sprintf("%d_tls", state.TunnelID)}}, false, true); err != nil {
			return failed("清理服务", err)
		}
	}
	if chainData != nil {
		if _, err := h.sendNodeCommand(nodeID, "AddChains", chainData, true, false); err != nil {
			return failed("下发转发链", err)
		}
	}
	if serviceData != nil {
		if err := h.addTunnelServiceOnNode(nodeID, state.TunnelID, serviceData); err != nil {
			return failed("下发服务", err)
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
