package socket

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-gost/x/logsafe"
)

func isRoutineCommand(command string) bool {
	switch command {
	// ApplyLandingDesired: the panel re-sends the desired landing state every
	// minute; failures are still logged (first + summary + recovery), and the
	// landing runtime logs its own reconcile errors (plan 137).
	// SetQuotaGroups: the panel's quota coordinator re-sends live budgets
	// whenever a user's counts move on other nodes (plan 167: it was one
	// 「收到命令」 line per push, ~1350/h on node 32); failures as above.
	case "TcpPing", "PipePing", "ServiceMonitorCheck", "GetServiceClientIPs", "ListServices", "ApplyLandingDesired", "SetQuotaGroups":
		return true
	default:
		return false
	}
}

// Only extract target identifiers; never log command bodies or request IDs.
func routineCommandTarget(cmd CommandMessage) string {
	var req struct {
		IP         string  `json:"ip"`
		Port       int     `json:"port"`
		TunnelID   int64   `json:"tunnelId"`
		Type       string  `json:"type"`
		Target     string  `json:"target"`
		ForwardIDs []int64 `json:"forwardIds"`
	}
	if err := decodeCommandData(cmd.Data, &req); err != nil {
		return "invalid request"
	}
	switch cmd.Type {
	case "TcpPing":
		return net.JoinHostPort(req.IP, strconv.Itoa(req.Port))
	case "PipePing":
		return fmt.Sprintf("tunnel=%d/127.0.0.1:%d", req.TunnelID, req.Port)
	case "ServiceMonitorCheck":
		return strings.ToLower(req.Type) + ":" + strings.TrimSpace(req.Target)
	case "GetServiceClientIPs":
		sort.Slice(req.ForwardIDs, func(i, j int) bool { return req.ForwardIDs[i] < req.ForwardIDs[j] })
		return fmt.Sprint(req.ForwardIDs)
	default:
		return "node"
	}
}

// pingLossLogs: partial TcpPing losses of all targets — the first at once,
// then one count per hour (plan 167).
var pingLossLogs = logsafe.NewEventSummaryEvery(nil, time.Hour)

func (w *WebSocketReporter) logRoutineResult(cmd CommandMessage, response CommandResponse, sendErr error) {
	failure := ""
	if !response.Success {
		failure = response.Message
	} else {
		switch result := response.Data.(type) {
		case TcpPingResponse:
			if !result.Success {
				failure = "探测失败: " + result.ErrorMessage
			} else if result.PacketLoss > 0 {
				// Plan 167: a partial loss is not an outage (the target answered
				// and the panel shows the loss). It used to open and close a
				// failure episode — a 首次失败/已恢复 pair, ~30/h on some nodes —
				// so it is counted in one hourly line per node instead.
				pingLossLogs.Record(cmd.Type, fmt.Sprintf("⚠️ %s 部分丢包 target=%q 丢包 %.1f%%", cmd.Type, logsafe.Text(routineCommandTarget(cmd), w.secret), result.PacketLoss))
			}
		case ServiceMonitorCheckResult:
			if !result.Success {
				failure = "检查失败: " + result.ErrorMessage
			}
		}
	}
	if sendErr != nil {
		failure = "发送响应失败: " + logsafe.Error(sendErr)
	}
	w.routineLogs.Record(cmd.Type, logsafe.Text(routineCommandTarget(cmd), w.secret), logsafe.Text(failure, w.secret))
}
