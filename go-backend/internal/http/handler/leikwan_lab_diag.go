//go:build leikwanlab

package handler

import (
	"fmt"
	"hash/fnv"
	"os"
	"strings"

	"go-backend/internal/ws"
)

// Plan 272 lab only (never in release builds): FLVX_LEIKWAN_LAB_DIAG=1 makes
// the panel answer LeiKwanDiag / TcpPing itself (deterministic values per
// node / target) so the 诊断 screens render without agents.
// FLVX_LEIKWAN_LAB_DIAG_FAIL=host:port,… → TcpPing to those targets times out;
// FLVX_LEIKWAN_LAB_DIAG_OLD=nodeId,… → those entry hosts answer like an old agent.
func init() {
	if os.Getenv("FLVX_LEIKWAN_LAB_DIAG") != "1" {
		return
	}
	fail := map[string]bool{}
	for _, f := range strings.Split(os.Getenv("FLVX_LEIKWAN_LAB_DIAG_FAIL"), ",") {
		if f = strings.TrimSpace(f); f != "" {
			fail[f] = true
		}
	}
	old := map[string]bool{}
	for _, f := range strings.Split(os.Getenv("FLVX_LEIKWAN_LAB_DIAG_OLD"), ",") {
		old[strings.TrimSpace(f)] = true
	}
	hash := func(s string) int {
		h := fnv.New32a()
		h.Write([]byte(s))
		return int(h.Sum32())
	}
	ingress := []string{"125.94.47.233:5438", "106.75.132.155:5438", "43.139.9.158:5438", "121.37.28.219:5438"}
	leiKwanLabCommand = func(nodeID int64, cmd string, data interface{}) (ws.CommandResult, bool) {
		m, _ := data.(map[string]interface{})
		switch cmd {
		case "LeiKwanDiag":
			if old[fmt.Sprint(nodeID)] {
				return ws.CommandResult{Success: false, Message: "未知命令: LeiKwanDiag"}, true
			}
			list := []interface{}{}
			base := 3 + int(nodeID%5)
			for i, ep := range ingress {
				ms := float64(base + i*3 + i*i*2)
				in := map[string]interface{}{"endpoint": ep, "offered": true, "ok": true, "avgMs": ms, "loss": 0.0}
				if i == 0 {
					in["inUse"] = "tcp+udp"
				}
				list = append(list, in)
			}
			fid := fmt.Sprint(m["forwardId"])
			total := float64(base + 9 + hash(fid)%8)
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"v": 1, "forwardId": m["forwardId"], "found": true, "state": "ready", "ingresses": list,
				"udp": map[string]interface{}{"rttMs": total, "p95Ms": total + 1, "sent": 30, "recv": 30, "ageS": 42}}}, true
		case "TcpPing":
			target := fmt.Sprintf("%v:%v", m["ip"], m["port"])
			if fail[target] || fail[fmt.Sprint(m["ip"])] {
				return ws.CommandResult{Success: true, Data: map[string]interface{}{"ip": m["ip"], "port": m["port"], "success": false, "averageTime": 0.0, "packetLoss": 100.0, "errorMessage": "连接超时"}}, true
			}
			ms := float64(2 + hash(target)%22)
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"ip": m["ip"], "port": m["port"], "success": true, "averageTime": ms, "packetLoss": 0.0}}, true
		}
		return ws.CommandResult{}, false
	}
}
