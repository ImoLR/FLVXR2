package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/ws"
)

type forwardClientIP struct {
	IP          string   `json:"ip"`
	Connections int      `json:"connections"`
	Nodes       []string `json:"nodes"`
}

type forwardClientIPNodeError struct {
	NodeName string `json:"nodeName"`
	Reason   string `json:"reason"`
}

type forwardClientIPResult struct {
	IPs             []forwardClientIP          `json:"ips"`
	IPCount         int                        `json:"ipCount"`
	ConnectionCount int                        `json:"connectionCount"`
	Truncated       bool                       `json:"truncated"`
	NodeErrors      []forwardClientIPNodeError `json:"nodeErrors"`
}

type forwardIPNode struct {
	ID   int64
	Name string
}

type nodeServiceIPSnapshot struct {
	IPs []struct {
		IP          string `json:"ip"`
		Connections int    `json:"connections"`
	} `json:"ips"`
	IPCount         int  `json:"ipCount"`
	ConnectionCount int  `json:"connectionCount"`
	Truncated       bool `json:"truncated"`
}

func (h *Handler) forwardClientIPs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	_, roleID, err := userRoleFromRequest(r)
	if err != nil {
		response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
		return
	}
	if roleID != 0 {
		response.WriteJSON(w, response.Err(403, "权限不足，仅管理员可操作"))
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := decodeJSON(r.Body, &req); err != nil || req.ID <= 0 {
		response.WriteJSON(w, response.ErrDefault("无效的规则 ID"))
		return
	}
	forward, err := h.repo.GetForwardRecord(req.ID)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	if forward == nil {
		response.WriteJSON(w, response.ErrDefault("规则不存在"))
		return
	}
	ports, err := h.repo.ListForwardPorts(req.ID)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	nodes := make([]forwardIPNode, 0, len(ports))
	seen := make(map[int64]struct{}, len(ports))
	for _, port := range ports {
		if _, ok := seen[port.NodeID]; ok {
			continue
		}
		seen[port.NodeID] = struct{}{}
		name, err := h.repo.GetNodeName(port.NodeID)
		if err != nil || name == "" {
			name = "节点 " + asString(port.NodeID)
		}
		nodes = append(nodes, forwardIPNode{ID: port.NodeID, Name: name})
	}
	send := func(nodeID int64, forwardID int64) (ws.CommandResult, error) {
		if h.wsServer == nil {
			return ws.CommandResult{}, errors.New("节点不在线")
		}
		return h.wsServer.SendCommand(nodeID, "GetServiceClientIPs", map[string]interface{}{"forwardIds": []int64{forwardID}}, 3*time.Second)
	}
	response.WriteJSON(w, response.OK(queryForwardClientIPs(req.ID, nodes, send)))
}

func queryForwardClientIPs(forwardID int64, nodes []forwardIPNode, send func(int64, int64) (ws.CommandResult, error)) forwardClientIPResult {
	result := forwardClientIPResult{IPs: []forwardClientIP{}, NodeErrors: []forwardClientIPNodeError{}}
	type nodeResult struct {
		node forwardIPNode
		cmd  ws.CommandResult
		err  error
	}
	results := make(chan nodeResult, len(nodes))
	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(node forwardIPNode) {
			defer wg.Done()
			cmd, err := send(node.ID, forwardID)
			results <- nodeResult{node: node, cmd: cmd, err: err}
		}(node)
	}
	wg.Wait()
	close(results)
	byIP := make(map[string]*forwardClientIP)
	for item := range results {
		if item.err != nil {
			reason := item.err.Error()
			if item.cmd.Type == "UnknownCommandResponse" || strings.Contains(reason, "未知命令类型") {
				reason = "节点 agent 版本过旧，需 fork.13+"
			}
			result.NodeErrors = append(result.NodeErrors, forwardClientIPNodeError{NodeName: item.node.Name, Reason: reason})
			continue
		}
		var payload struct {
			Services map[string]nodeServiceIPSnapshot `json:"services"`
		}
		raw, err := json.Marshal(item.cmd.Data)
		if err == nil {
			err = json.Unmarshal(raw, &payload)
		}
		if err != nil || payload.Services == nil {
			result.NodeErrors = append(result.NodeErrors, forwardClientIPNodeError{NodeName: item.node.Name, Reason: "节点响应格式无效"})
			continue
		}
		for _, svc := range payload.Services {
			result.ConnectionCount += svc.ConnectionCount
			if svc.Truncated {
				result.Truncated = true
			}
			if svc.IPCount > result.IPCount {
				result.IPCount = svc.IPCount
			}
			for _, ip := range svc.IPs {
				entry := byIP[ip.IP]
				if entry == nil {
					entry = &forwardClientIP{IP: ip.IP}
					byIP[ip.IP] = entry
				}
				entry.Connections += ip.Connections
				if !containsString(entry.Nodes, item.node.Name) {
					entry.Nodes = append(entry.Nodes, item.node.Name)
				}
			}
		}
	}
	for _, ip := range byIP {
		sort.Strings(ip.Nodes)
		result.IPs = append(result.IPs, *ip)
	}
	sort.Slice(result.IPs, func(i, j int) bool { return result.IPs[i].IP < result.IPs[j].IP })
	if len(result.IPs) > result.IPCount {
		result.IPCount = len(result.IPs)
	}
	if len(result.IPs) > 500 {
		result.IPs = result.IPs[:500]
		result.Truncated = true
	}
	sort.Slice(result.NodeErrors, func(i, j int) bool { return result.NodeErrors[i].NodeName < result.NodeErrors[j].NodeName })
	return result
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
