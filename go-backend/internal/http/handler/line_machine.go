package handler

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// 线路机 rules (plan 108). A tunnel whose entry is a 线路机 (public-internet
// line, e.g. DMIT) carries the client's landing protocol across the GFW, so
// its rules may only target VLESS Reality Vision landing nodes or a chain
// (「我自己的节点」). Enforced here for every path that sets a rule target,
// for admins too; the UI only mirrors it.

const (
	lineMachineManualMsg   = "线路机隧道不能手动填写目标地址：请选择 VLESS Reality Vision 落地节点、落地服务器或「我自己的节点」"
	lineMachineRealityMsg  = "线路机隧道只能转发到 VLESS Reality Vision 落地节点"
	lineMachineProtocolMsg = "线路机隧道上自动创建的落地节点只能使用 VLESS Reality Vision 协议"
)

// isLineMachineTunnel reports whether any entry node of the tunnel is a 线路机.
func (h *Handler) isLineMachineTunnel(tunnelID int64) bool {
	if h == nil || h.repo == nil || tunnelID <= 0 {
		return false
	}
	info, err := h.repo.TunnelLineInfo(tunnelID)
	return err == nil && info != nil && info.LineMachine
}

// lineMachinePreCheck runs before a landing node could be auto-created: a
// 落地服务器 target on a 线路机 tunnel must use VLESS Reality Vision.
func (h *Handler) lineMachinePreCheck(tunnelID int64, req map[string]interface{}) error {
	if req == nil || asInt64(req["landingServerId"], 0) <= 0 || !h.isLineMachineTunnel(tunnelID) {
		return nil
	}
	if defaultString(asString(req["landingProtocol"]), "ss2022") != "vless_reality_vision" {
		return errors.New(lineMachineProtocolMsg)
	}
	return nil
}

// lineMachineTargetError validates a resolved rule target. explicit is true
// when the request chose a landing node/server (not a typed address).
func (h *Handler) lineMachineTargetError(tunnelID int64, mode string, landingID int64, explicit bool) error {
	if strings.EqualFold(mode, "wg_path") || !h.isLineMachineTunnel(tunnelID) {
		return nil
	}
	if landingID <= 0 || !explicit {
		return errors.New(lineMachineManualMsg)
	}
	n, err := h.repo.GetLandingNode(landingID)
	if err != nil || n == nil {
		return errors.New("落地节点不存在")
	}
	if n.Protocol != "vless_reality_vision" {
		return errors.New(lineMachineRealityMsg)
	}
	return nil
}

func explicitLandingTarget(req map[string]interface{}) bool {
	return req != nil && (asInt64(req["landingNodeId"], 0) > 0 || asInt64(req["landingServerId"], 0) > 0)
}

// forwardLineCompliant: on a 线路机 tunnel a rule is compliant when it is a
// chain rule or targets a VLESS Reality Vision landing node.
func forwardLineCompliant(f model.Forward, chained bool, landingProtocol map[int64]string) bool {
	if chained {
		return true
	}
	return f.LandingNodeID > 0 && landingProtocol[f.LandingNodeID] == "vless_reality_vision"
}

// loopbackTargetError: users must never type loopback/unspecified targets
// (they would reach node-local services such as the landing Core API).
// Chain rules use 127.0.0.1 only through the system-generated target.
func loopbackTargetError(remoteAddr string) error {
	for _, part := range strings.FieldsFunc(remoteAddr, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		host := strings.TrimSpace(part)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return errors.New("目标地址不能是本机（localhost）")
		}
		if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
			return errors.New("目标地址不能是本机回环地址（127.0.0.1、::1）或 0.0.0.0")
		}
	}
	return nil
}

// tunnelChainAvailability: the chain option needs a 线路机 tunnel with an exit
// tier whose nodes are FLVX nodes (no 线路机, chain-capable agents).
func tunnelChainAvailability(info *repo.TunnelLineInfo) (bool, string) {
	if info == nil || !info.LineMachine {
		return false, "仅线路机隧道可用"
	}
	if info.Type != 2 || len(info.Exits) == 0 {
		return false, "该隧道没有出口节点，只能转发到 Reality 落地节点"
	}
	for _, e := range info.Exits {
		switch {
		case e.LineMachine:
			return false, "出口节点「" + e.Name + "」是线路机，不能承载「我自己的节点」"
		case e.NodeID == 36:
			return false, "出口节点「" + e.Name + "」不能承载落地"
		case e.IsRemote:
			return false, "出口节点「" + e.Name + "」是远程节点，不能承载「我自己的节点」"
		case !e.LandingCapable || e.LandingChain < 1:
			return false, "出口节点「" + e.Name + "」的节点程序版本过低，需要升级后才能使用「我自己的节点」"
		}
	}
	return true, ""
}

// decorateTunnelLineInfo adds lineMachine / chainAvailable to tunnel list
// items (the rule dialog mirrors the backend rules with them).
func (h *Handler) decorateTunnelLineInfo(items []map[string]interface{}) {
	if h == nil || h.repo == nil || len(items) == 0 {
		return
	}
	lines, err := h.repo.LineMachineTunnelIDs()
	if err != nil {
		return
	}
	ids := []int64{}
	for id := range lines {
		ids = append(ids, id)
	}
	infos, err := h.repo.TunnelLineInfos(ids)
	if err != nil {
		return
	}
	for _, item := range items {
		id := asInt64(item["id"], 0)
		item["lineMachine"] = lines[id]
		if !lines[id] {
			item["chainAvailable"] = false
			continue
		}
		info := infos[id]
		if info == nil {
			continue
		}
		ok, reason := tunnelChainAvailability(info)
		item["chainAvailable"] = ok
		if !ok {
			item["chainUnavailableReason"] = reason
		}
	}
}

// decorateForwardChains adds chain info and the 不合规 mark to rule list
// items with three batched queries; it returns the non-compliant count.
func (h *Handler) decorateForwardChains(items []map[string]interface{}) int {
	if h == nil || h.repo == nil || len(items) == 0 {
		return 0
	}
	ids := make([]int64, 0, len(items))
	landingIDs := []int64{}
	for _, item := range items {
		ids = append(ids, asInt64(item["id"], 0))
		if l := asInt64(item["landingNodeId"], 0); l > 0 {
			landingIDs = append(landingIDs, l)
		}
	}
	chains, err := h.repo.ForwardChainsByForwardIDs(ids)
	if err != nil {
		return 0
	}
	lines, err := h.repo.LineMachineTunnelIDs()
	if err != nil {
		return 0
	}
	protocols, _ := h.repo.LandingNodeProtocols(landingIDs)
	count := 0
	for _, item := range items {
		id := asInt64(item["id"], 0)
		c, chained := chains[id]
		if chained {
			item["chain"] = map[string]interface{}{
				"state": c.State, "op": c.Op, "opStartedAt": c.OpStartedAt, "lastOpAt": c.LastOpAt, "error": c.LastError,
				"upstreamProtocol": c.UpstreamProtocol, "upstreamHost": c.UpstreamHost, "upstreamPort": c.UpstreamPort,
				"summary": c.UpstreamProtocol + " · " + net.JoinHostPort(c.UpstreamHost, strconv.Itoa(c.UpstreamPort)),
			}
		}
		tunnelID := asInt64(item["tunnelId"], 0)
		if !lines[tunnelID] || strings.EqualFold(asString(item["mode"]), "wg_path") {
			continue
		}
		item["lineMachine"] = true
		f := model.Forward{LandingNodeID: asInt64(item["landingNodeId"], 0)}
		if !forwardLineCompliant(f, chained && c.State == model.ChainStateActive, protocols) {
			item["lineNonCompliant"] = true
			count++
		}
	}
	return count
}
