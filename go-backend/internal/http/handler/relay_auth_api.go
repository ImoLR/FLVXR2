package handler

import (
	"fmt"
	"net/http"
	"time"

	"go-backend/internal/http/response"
)

// Ordered relay auth rollout (plan 225).
//
// POST /api/v1/tunnel/relay-auth (admin)
//
//	{"tunnelIds":[1,2]}                 status only (flag, enforceable, effective)
//	{"tunnelIds":[1,2],"enforce":true}  switch on, tunnel by tunnel
//	{"all":true,"enforce":false}        switch off (rollback path)
//
// Switching ON a running tunnel, under its runtime lock:
//  1. every entry/middle node (the connectors) must be online and run the
//     credentialed chain — pushed now if its revision differs (UpdateChains
//     swaps without a gap); any failure leaves the tunnel unchanged;
//  2. tunnel.relay_auth = 1;
//  3. middle + exit relays are pushed with the credential required (exits
//     also with the target guard). An offline exit gets it on reconnect.
//
// Switching OFF stores 0 and pushes the middle/exit relays without auth;
// connectors keep sending the credential (ignored by a handler without auth),
// so an older panel can take over afterwards.
//
// The response never contains the credential.

type relayAuthNodeResult struct {
	NodeID  int64  `json:"nodeId"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Pushed  bool   `json:"pushed"`
	Message string `json:"message,omitempty"`
}

type relayAuthTunnelResult struct {
	TunnelID    int64                 `json:"tunnelId"`
	Name        string                `json:"name"`
	Type        int                   `json:"type"`
	Status      int                   `json:"status"`
	Flag        int                   `json:"relayAuth"`
	Enforceable bool                  `json:"enforceable"`
	Effective   bool                  `json:"effective"`
	Reason      string                `json:"reason,omitempty"`
	Changed     bool                  `json:"changed"`
	Skipped     string                `json:"skipped,omitempty"`
	Error       string                `json:"error,omitempty"`
	Nodes       []relayAuthNodeResult `json:"nodes,omitempty"`
	ElapsedMs   int64                 `json:"elapsedMs"`
}

func (h *Handler) tunnelRelayAuth(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		TunnelIDs []int64 `json:"tunnelIds"`
		All       bool    `json:"all"`
		Enforce   *bool   `json:"enforce"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	if !req.All && len(req.TunnelIDs) == 0 {
		response.WriteJSON(w, response.ErrDefault("请指定隧道"))
		return
	}
	ids := req.TunnelIDs
	if req.All {
		ids = nil
	}
	rows, err := h.repo.ListTunnelRelayAuth(ids)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	out := make([]relayAuthTunnelResult, 0, len(rows))
	for _, row := range rows {
		var res relayAuthTunnelResult
		if req.Enforce == nil {
			res = h.relayAuthStatus(row.ID)
		} else {
			res = h.setTunnelRelayAuth(row.ID, *req.Enforce)
		}
		out = append(out, res)
	}
	response.WriteJSON(w, response.OK(out))
}

func (h *Handler) relayAuthStatus(tunnelID int64) relayAuthTunnelResult {
	res := relayAuthTunnelResult{TunnelID: tunnelID}
	tunnel, err := h.getTunnelRecord(tunnelID)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Type, res.Status, res.Flag = tunnel.Type, tunnel.Status, tunnel.RelayAuth
	res.Name = h.tunnelNameOf(tunnelID)
	if tunnel.Type != 2 {
		res.Reason = "端口转发隧道没有中转服务"
		return res
	}
	state, err := h.reconstructTunnelState(tunnelID, true)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Enforceable, res.Reason = h.relayAuthEnforceable(state)
	res.Effective = res.Enforceable && tunnel.RelayAuth == 1
	return res
}

func (h *Handler) tunnelNameOf(tunnelID int64) string {
	rows, err := h.repo.ListTunnelRelayAuth([]int64{tunnelID})
	if err != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].Name
}

// relayAuthUpstreamNodes are the nodes whose connectors dial the tunnel's
// relays (entries + middle hops), relayAuthRelayNodes the nodes that run a
// relay service (middle hops + exits). Both deduplicated, in push order.
func relayAuthUpstreamNodes(state *tunnelCreateState) []int64 {
	var ids []int64
	seen := map[int64]bool{}
	add := func(list []tunnelRuntimeNode) {
		for _, n := range list {
			if !seen[n.NodeID] {
				seen[n.NodeID] = true
				ids = append(ids, n.NodeID)
			}
		}
	}
	add(state.InNodes)
	for _, hop := range state.ChainHops {
		add(hop)
	}
	return ids
}

func relayAuthRelayNodes(state *tunnelCreateState) []int64 {
	var ids []int64
	seen := map[int64]bool{}
	add := func(list []tunnelRuntimeNode) {
		for _, n := range list {
			if !seen[n.NodeID] {
				seen[n.NodeID] = true
				ids = append(ids, n.NodeID)
			}
		}
	}
	add(state.OutNodes)
	for i := len(state.ChainHops) - 1; i >= 0; i-- {
		add(state.ChainHops[i])
	}
	return ids
}

func (h *Handler) setTunnelRelayAuth(tunnelID int64, enforce bool) relayAuthTunnelResult {
	start := time.Now()
	res := relayAuthTunnelResult{TunnelID: tunnelID}
	defer func() { res.ElapsedMs = time.Since(start).Milliseconds() }()
	flag := 0
	if enforce {
		flag = 1
	}

	unlock := h.lockTunnelRuntime(tunnelID)
	defer unlock()

	tunnel, err := h.getTunnelRecord(tunnelID)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Type, res.Status, res.Flag = tunnel.Type, tunnel.Status, tunnel.RelayAuth
	res.Name = h.tunnelNameOf(tunnelID)
	if tunnel.Type != 2 || tunnel.Status != 1 {
		// No running relay: only the flag (used when the tunnel deploys).
		if tunnel.RelayAuth != flag {
			if err := h.repo.SetTunnelRelayAuth(tunnelID, flag); err != nil {
				res.Error = err.Error()
				return res
			}
			res.Changed = true
		}
		res.Flag = flag
		if tunnel.Type != 2 {
			res.Reason = "端口转发隧道没有中转服务"
		} else {
			res.Reason = "隧道已停用，启用时按新设置下发"
		}
		return res
	}
	state, err := h.reconstructTunnelState(tunnelID)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Enforceable, res.Reason = h.relayAuthEnforceable(state)
	if enforce && !res.Enforceable {
		res.Skipped = res.Reason
		return res
	}

	push := func(nodeID int64, role string) (relayAuthNodeResult, error) {
		item := relayAuthNodeResult{NodeID: nodeID, Role: role, Name: nodeDisplayName(state.Nodes[nodeID])}
		if !h.nodeAgentOnline(nodeID) {
			item.Message = "节点不在线"
			return item, fmt.Errorf("%s %s 不在线", role, item.Name)
		}
		inv, _ := h.reconnectInventory(nodeID)
		changed, err := h.redeployTunnelRuntimeOnNodeDiffLocked(tunnelID, nodeID, inv)
		item.Pushed = changed
		if err != nil {
			item.Message = err.Error()
		}
		return item, err
	}

	if enforce {
		// 1. Connectors first: nothing may require auth before every node
		//    that dials it sends the credential.
		for _, id := range relayAuthUpstreamNodes(state) {
			if n := state.Nodes[id]; n != nil && n.IsRemote == 1 {
				continue
			}
			item, err := push(id, "上游")
			res.Nodes = append(res.Nodes, item)
			if err != nil {
				res.Skipped = "上游节点未就绪：" + err.Error()
				return res
			}
		}
	}

	// 2. The flag.
	if tunnel.RelayAuth != flag {
		if err := h.repo.SetTunnelRelayAuth(tunnelID, flag); err != nil {
			res.Error = err.Error()
			return res
		}
		res.Changed = true
	}
	res.Flag = flag

	// 3. Relays (middle + exit). An offline relay node gets the new service
	//    on its reconnect (diff); its connectors already send the credential.
	for _, id := range relayAuthRelayNodes(state) {
		if n := state.Nodes[id]; n != nil && n.IsRemote == 1 {
			continue
		}
		item, err := push(id, "中转/出口")
		res.Nodes = append(res.Nodes, item)
		if err != nil && res.Error == "" {
			res.Error = err.Error()
		}
	}
	res.Effective = enforce && res.Enforceable
	return res
}
