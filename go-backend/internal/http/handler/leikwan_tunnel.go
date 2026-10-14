package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

// LeiKwan's listen port belongs to the entry's client, while Port on the
// landing chain row remains the actual FLVX service port. Only entry dialing
// receives this override; persisted ordinary chain rows keep their meaning.
func leiKwanEntryTargets(state *tunnelCreateState) ([]tunnelRuntimeNode, error) {
	targets := state.OutNodes
	if len(state.ChainHops) > 0 {
		targets = state.ChainHops[0]
	}
	if state.LeiKwanPipe == nil {
		return targets, nil
	}
	pipe := state.LeiKwanPipe
	if len(targets) != 1 || targets[0].NodeID != pipe.LandingNodeID || targets[0].Port != pipe.LandingPort {
		return nil, errors.New("利群管道落地与隧道配置不一致，请编辑隧道重新同步")
	}
	if pipe.ListenPort < 1 || pipe.ListenPort > 65535 {
		return nil, errors.New("利群管道监听端口无效")
	}
	targets = append([]tunnelRuntimeNode(nil), targets...)
	targets[0].LeiKwanPipeHop = true
	targets[0].ConnectIP = "127.0.0.1"
	targets[0].Port = pipe.ListenPort
	return targets, nil
}

type leiKwanTunnelChange struct {
	pipe      *model.LeiKwanPipe
	account   *model.LeiKwanAccount
	updated   *model.LeiKwanPipe
	tx        *gorm.DB
	committed bool
	unlock    func()
}

func (h *Handler) prepareLeiKwanTunnelChange(req map[string]interface{}, tunnelID int64) (*leiKwanTunnelChange, error) {
	var pipe *model.LeiKwanPipe
	var err error
	if tunnelID > 0 {
		pipe, err = h.repo.GetLeiKwanPipeByTunnel(tunnelID)
		if err != nil {
			return nil, err
		}
	}
	requested := asInt64(req["leikwanPipeId"], 0)
	if pipe != nil && requested > 0 && requested != pipe.ID {
		return nil, errors.New("已绑定的利群管道不能替换，请新建隧道")
	}
	if pipe == nil && requested > 0 {
		pipe, err = h.repo.GetLeiKwanPipe(requested)
		if err != nil {
			return nil, err
		}
		if pipe == nil {
			return nil, errors.New("利群管道不存在")
		}
	}
	if pipe == nil {
		return nil, nil
	}
	unlock := h.lockLeiKwanPipe(pipe.ID)
	pipe, err = h.repo.GetLeiKwanPipe(pipe.ID)
	if err != nil || pipe == nil {
		unlock()
		return nil, errors.New("利群管道不存在")
	}
	if pipe.TunnelID != nil && *pipe.TunnelID != tunnelID {
		unlock()
		return nil, errors.New("管道已被其他隧道使用")
	}
	account, err := h.repo.GetLeiKwanAccount(pipe.AccountID)
	if err != nil || account == nil {
		unlock()
		return nil, errors.New("利群账号不存在")
	}
	return &leiKwanTunnelChange{pipe: pipe, account: account, unlock: unlock}, nil
}

func (change *leiKwanTunnelChange) finish(h *Handler) {
	if change == nil {
		return
	}
	defer change.unlock()
	if change.committed || change.updated == nil {
		return
	}
	// Release SQLite's single connection before compensating an aborted edit.
	if change.tx != nil {
		change.tx.Rollback()
	}
	if change.updated.TargetHost == change.pipe.TargetHost && change.updated.LandingPort == change.pipe.LandingPort {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	oldNode := &nodeRecord{ID: change.pipe.LandingNodeID, ServerIP: change.pipe.TargetHost, ServerIPv4: change.pipe.TargetHost, ServerIPv6: change.pipe.TargetHost}
	restored, err := h.patchLeiKwanPipe(ctx, change.account, change.updated, oldNode, change.pipe.LandingPort)
	if err != nil {
		// Preserve the actual last known remote target for a safe, conditional retry.
		pending := *change.updated
		pending.TunnelID = change.pipe.TunnelID
		_ = h.repo.SaveLeiKwanPipe(&pending)
		log.Printf("leikwan: pipe %d rollback failed; target saved for explicit retry: %v", change.pipe.ID, err)
		return
	}
	restored.TunnelID = change.pipe.TunnelID
	_ = h.repo.SaveLeiKwanPipe(restored)
}

func (h *Handler) syncLeiKwanTunnelChange(ctx context.Context, tx *gorm.DB, change *leiKwanTunnelChange, state *tunnelCreateState) error {
	if change == nil {
		return nil
	}
	change.tx = tx
	if state.Type != 2 {
		return errors.New("利群管道必须使用隧道转发")
	}
	targets := state.OutNodes
	if len(state.ChainHops) > 0 {
		targets = state.ChainHops[0]
	}
	if len(targets) != 1 {
		return errors.New("利群管道的第一落地必须恰好选择一个节点")
	}
	for _, entry := range state.InNodes {
		if n := state.Nodes[entry.NodeID]; n == nil || n.IsRemote == 1 {
			return errors.New("利群入口必须是本面板的节点")
		}
	}
	landing := targets[0]
	node := state.Nodes[landing.NodeID]
	if node == nil || node.IsRemote == 1 {
		return errors.New("利群落地必须是本面板的节点")
	}
	if landing.Port < 1 || landing.Port > 65535 {
		return errors.New("利群落地服务端口无效")
	}
	updated, err := h.patchLeiKwanPipe(ctx, change.account, change.pipe, node, landing.Port)
	// A failed PATCH may have reached LeiKwan. Keep its attempted target so
	// the rollback path can inspect and compensate an ambiguous response.
	change.updated = updated
	if err != nil {
		return err
	}
	if err := h.repo.SaveLeiKwanTunnelPipeTx(tx, updated, state.TunnelID); err != nil {
		return err
	}
	state.LeiKwanPipe = updated
	_, err = leiKwanEntryTargets(state)
	return err
}

// A pipe probe uses the entry's deployed chain transport and reads an FLVX
// relay response beyond the opaque pipe. TcpPing alone only proves that the
// local LeiKwan process accepted a TCP socket.
func (h *Handler) probeLeiKwanPipe(nodeID, tunnelID int64, port int, timeout time.Duration) (map[string]interface{}, error) {
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	res, err := h.sendNodeCommandWithTimeout(nodeID, "PipePing", map[string]interface{}{
		"tunnelId": tunnelID, "port": port, "timeout": int(timeout / time.Millisecond),
	}, timeout+time.Second, false, false)
	if err != nil {
		return nil, fmt.Errorf("管道诊断失败（入口需 fork.45 或更新代理）: %w", err)
	}
	if res.Data == nil {
		return nil, errors.New("入口未返回管道诊断数据，请升级代理")
	}
	return res.Data, nil
}

func (h *Handler) leikwanPipeCheck(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		PipeID int64 `json:"pipeId"`
	}
	if err := decodeJSON(r.Body, &req); err != nil || req.PipeID <= 0 {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	pipe, err := h.repo.GetLeiKwanPipe(req.PipeID)
	if err != nil || pipe == nil {
		response.WriteJSON(w, response.ErrDefault("利群管道不存在"))
		return
	}
	entries := []map[string]interface{}{}
	result := map[string]interface{}{"platformSaved": true, "entries": entries, "endToEnd": map[string]interface{}{"success": false, "message": "绑定隧道后检查入口与落地"}}
	if pipe.TunnelID == nil {
		response.WriteJSON(w, response.OK(result))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	state, err := h.reconstructTunnelState(*pipe.TunnelID)
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	allOK := len(state.InNodes) > 0
	endResults := []map[string]interface{}{}
	for _, entry := range state.InNodes {
		if ctx.Err() != nil {
			allOK = false
			break
		}
		options := diagnosisExecOptions{pingCount: 1, pingTimeoutMS: 1000, commandTimeout: 2 * time.Second}
		ping, pingErr := h.tcpPingViaNode(entry.NodeID, "127.0.0.1", pipe.ListenPort, options)
		reachable := pingErr == nil && asBool(ping["success"], false)
		message := "本地监听端口可连接"
		if !reachable {
			message = "本地监听端口不可连接"
			if pingErr != nil {
				message = pingErr.Error()
			}
		}
		entries = append(entries, map[string]interface{}{"nodeId": entry.NodeID, "nodeName": state.Nodes[entry.NodeID].Name, "reachable": reachable, "message": message})
		remaining := 5 * time.Second
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < remaining {
			remaining = time.Until(deadline)
		}
		if !reachable || remaining <= 0 || ctx.Err() != nil {
			allOK = false
			continue
		}
		probe, probeErr := h.probeLeiKwanPipe(entry.NodeID, state.TunnelID, pipe.ListenPort, remaining)
		ok := probeErr == nil && asBool(probe["success"], false)
		if !ok {
			allOK = false
		}
		if probe == nil {
			probe = map[string]interface{}{}
		}
		probe["nodeId"] = entry.NodeID
		if probeErr != nil {
			probe["message"] = probeErr.Error()
		}
		endResults = append(endResults, probe)
	}
	result["entries"] = entries
	message := "通过管道的落地服务已响应"
	if !allOK {
		message = "管道尚未就绪，请查看各入口诊断"
	}
	result["endToEnd"] = map[string]interface{}{"success": allOK, "message": message, "results": endResults}
	response.WriteJSON(w, response.OK(result))
}

func (h *Handler) cleanupLeiKwanOldRuntime(state *tunnelCreateState) {
	if state == nil {
		return
	}
	chains, services := []int64{}, []int64{}
	for _, entry := range state.InNodes {
		chains = append(chains, entry.NodeID)
	}
	for _, hop := range state.ChainHops {
		for _, node := range hop {
			if state.Nodes[node.NodeID].IsRemote != 1 {
				chains = append(chains, node.NodeID)
				services = append(services, node.NodeID)
			}
		}
	}
	for _, node := range state.OutNodes {
		if state.Nodes[node.NodeID].IsRemote != 1 {
			services = append(services, node.NodeID)
		}
	}
	h.rollbackTunnelRuntime(chains, services, state.TunnelID)
}

func (h *Handler) applyLeiKwanDiagnosis(tunnelID int64, items []diagnosisWorkItem) error {
	pipe, err := h.repo.GetLeiKwanPipeByTunnel(tunnelID)
	if err != nil || pipe == nil {
		return err
	}
	for i := range items {
		item := &items[i]
		if !item.hasChainHop || asInt(item.metadata["fromChainType"], 0) != 1 {
			continue
		}
		if item.toNode.NodeID != pipe.LandingNodeID || item.toNode.Port != pipe.LandingPort {
			return errors.New("利群管道落地与隧道配置不一致")
		}
		item.hasChainHop = false
		item.targetIP = "127.0.0.1"
		item.targetPort = pipe.ListenPort
		item.pipeTunnelID = tunnelID
		item.description += "（利群管道）"
		item.metadata["leikwanPipeId"] = pipe.ID
	}
	return nil
}

func (h *Handler) executeLeiKwanDiagnosis(item diagnosisWorkItem, options diagnosisExecOptions) map[string]interface{} {
	result := newDiagnosisResultItem(item.fromNodeID, "127.0.0.1", item.targetPort, item.description, item.metadata)
	if node, err := h.getNodeRecord(item.fromNodeID); err == nil {
		result["nodeName"] = node.Name
	}
	data, err := h.probeLeiKwanPipe(item.fromNodeID, item.pipeTunnelID, item.targetPort, options.commandTimeout)
	if err != nil {
		result["message"] = err.Error()
		return result
	}
	result["success"] = asBool(data["success"], false)
	result["averageTime"] = asFloat(data["averageTime"], 0)
	result["packetLoss"] = asFloat(data["packetLoss"], 100)
	result["message"] = asString(data["message"])
	if result["message"] == "" {
		result["message"] = asString(data["errorMessage"])
	}
	return result
}

// The tunnel has already been removed. A concurrent create may have claimed
// its retained pipe; never delete that new tunnel's forward.
func (h *Handler) deleteDetachedLeiKwanPipe(ctx context.Context, pipeID int64) error {
	unlock := h.lockLeiKwanPipe(pipeID)
	defer unlock()
	pipe, err := h.repo.GetLeiKwanPipe(pipeID)
	if err != nil || pipe == nil {
		return err
	}
	if pipe.TunnelID != nil {
		return errors.New("管道已被另一个隧道使用，已保留利群转发")
	}
	return h.deleteLeiKwanPipe(ctx, pipeID)
}
