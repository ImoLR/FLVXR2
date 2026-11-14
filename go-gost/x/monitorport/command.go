package monitorport

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Request is the MonitorPort command of the panel.
//
//	{"action":"open","port":N} {"action":"close","port":N} {"action":"list"}
//	{"action":"sync","ports":[N,…]}   (full desired set; every other port closes)
type Request struct {
	Action string `json:"action"`
	Port   int    `json:"port,omitempty"`
	Ports  []int  `json:"ports,omitempty"`
}

// Response lists the per-port results and the ports open afterwards.
type Response struct {
	Results []Result `json:"results"`
	Open    []Info   `json:"open"`
	Max     int      `json:"max"`
}

// Handle runs one MonitorPort command.
func (m *Manager) Handle(data interface{}) (Response, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Response{}, err
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return Response{}, fmt.Errorf("解析 MonitorPort 请求失败: %v", err)
	}
	var results []Result
	switch req.Action {
	case "open":
		results = []Result{m.Open(req.Port)}
	case "close":
		results = []Result{m.Close(req.Port)}
	case "list":
	case "sync":
		if len(req.Ports) > MaxPorts*4 {
			return Response{}, errors.New("端口列表过长")
		}
		results = m.Sync(req.Ports)
	default:
		return Response{}, fmt.Errorf("未知操作 %q", req.Action)
	}
	if results == nil {
		results = []Result{}
	}
	return Response{Results: results, Open: m.List(), Max: MaxPorts}, nil
}
