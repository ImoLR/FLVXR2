package handler

import (
	"math"
	"net/http"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// The UI labels data older than this with its age (「数据 N 分钟前」).
const landingMonitorAgeHintMs = int64(30 * 1000)

type landingMonitorNodeView struct {
	ID                int64    `json:"id"`
	Name              string   `json:"name"`
	UserID            int64    `json:"userId,omitempty"`
	UserName          string   `json:"userName,omitempty"`
	Protocol          string   `json:"protocol"`
	Port              int      `json:"port"`
	Status            string   `json:"status"`
	PendingReason     string   `json:"pendingReason,omitempty"`
	SyncError         string   `json:"syncError,omitempty"`
	DeletePending     bool     `json:"deletePending,omitempty"`
	Connections       int      `json:"connections"`
	MaxConnections    int      `json:"maxConnections"`
	MaxOnlineIPs      int      `json:"maxOnlineIps"`
	OnlineIPs         *int     `json:"onlineIps"`
	UpRate            *float64 `json:"upRate"`
	DownRate          *float64 `json:"downRate"`
	SampleAt          int64    `json:"sampleAt"`
	BilledBytes       int64    `json:"billedBytes"`
	TrafficLimitBytes int64    `json:"trafficLimitBytes"`
}

type landingMonitorServerView struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Source          string   `json:"source"`
	Status          string   `json:"status"`
	Online          bool     `json:"online"`
	CoreState       string   `json:"coreState,omitempty"`
	Notice          string   `json:"notice,omitempty"`
	SyncError       string   `json:"syncError,omitempty"`
	NodeCount       int      `json:"nodeCount"`
	ActiveNodeCount int      `json:"activeNodeCount"`
	Connections     int      `json:"connections"`
	MaxConnections  int      `json:"maxConnections"`
	UpRate          *float64 `json:"upRate"`
	DownRate        *float64 `json:"downRate"`
	SampleAt        int64    `json:"sampleAt"`
	BilledBytes     int64    `json:"billedBytes"`
	// Admin, shared servers: the mmwx share's raw traffic used / limit.
	ShareUsedBytes  int64 `json:"shareUsedBytes,omitempty"`
	ShareLimitBytes int64 `json:"shareLimitBytes,omitempty"`
	// Normal users: their own per-server quota (用户 × 服务器 限额) this period.
	QuotaUsedBytes  int64                    `json:"quotaUsedBytes,omitempty"`
	QuotaLimitBytes int64                    `json:"quotaLimitBytes,omitempty"`
	QuotaPaused     bool                     `json:"quotaPaused,omitempty"`
	Nodes           []landingMonitorNodeView `json:"nodes"`
}

type landingMonitorView struct {
	GeneratedAt  int64                      `json:"generatedAt"`
	Admin        bool                       `json:"admin"`
	AgeHintMs    int64                      `json:"ageHintMs"`
	StaleAfterMs int64                      `json:"staleAfterMs"`
	Servers      []landingMonitorServerView `json:"servers"`
}

type landingMonitorIPsView struct {
	OnlineIPs *int                     `json:"onlineIps"`
	IPs       []landingcustom.OnlineIP `json:"ips"`
	SampleAt  int64                    `json:"sampleAt"`
}

func (h *Handler) landingMonitorIPs(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.resolveMonitorScope(w, r)
	if !ok {
		return
	}
	var req struct {
		NodeID int64 `json:"nodeId"`
	}
	if err := landingDecode(r, &req); err != nil || req.NodeID <= 0 {
		response.WriteJSON(w, response.Err(-1, "落地节点参数无效"))
		return
	}
	visible, err := h.repo.CanViewLandingMonitorNode(scope.userID, req.NodeID, scope.roleID == 0)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, "落地监控数据读取失败"))
		return
	}
	if !visible {
		response.WriteJSON(w, response.Err(403, "无权访问该落地节点"))
		return
	}
	response.WriteJSON(w, response.OK(h.landingRateTracker().onlineIPs(req.NodeID, time.Now().UnixMilli())))
}

// landingMonitor serves the 监控 「落地」 tab: one request, a fixed number of
// queries, rates from memory. Same access gate as the other monitor endpoints;
// a normal user (同步 or 全部 mode alike) only ever gets their own nodes.
func (h *Handler) landingMonitor(w http.ResponseWriter, r *http.Request) {
	scope, ok := h.resolveMonitorScope(w, r)
	if !ok {
		return
	}
	admin := scope.roleID == 0
	now := time.Now()
	data, err := h.repo.LandingMonitorSnapshot(scope.userID, admin, now)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, "落地监控数据读取失败"))
		return
	}
	response.WriteJSON(w, response.OK(buildLandingMonitorView(data, admin, h.landingRateTracker(), now)))
}

// landingNodeDisplayStatus is the status the 落地 page shows: shared nodes that
// Custom has not confirmed are 等待同步 (with the not-ready reason) or 节点异常.
func landingNodeDisplayStatus(n *model.LandingNode, s *model.LandingServer) (string, string) {
	if s == nil || s.Source != "shared" || n.Status != "active" {
		return n.Status, ""
	}
	if n.SyncError != "" {
		return "error", ""
	}
	if n.RemoteState != "active" {
		return "pending", sharedCoreNotice(s, true)
	}
	return n.Status, ""
}

// Whole bytes per second are precise enough and keep large responses small.
func ratePtr(v float64) *float64 {
	v = math.Round(v)
	return &v
}

func buildLandingMonitorView(data *repo.LandingMonitorData, admin bool, tracker *landingRateTracker, now time.Time) landingMonitorView {
	nowMs := now.UnixMilli()
	out := landingMonitorView{GeneratedAt: nowMs, Admin: admin, AgeHintMs: landingMonitorAgeHintMs, StaleAfterMs: landingRateStaleAfterMs, Servers: []landingMonitorServerView{}}
	if data == nil {
		return out
	}
	ids := make([]int64, 0, len(data.Nodes))
	byServer := make(map[int64][]model.LandingNode, len(data.Servers))
	for _, n := range data.Nodes {
		ids = append(ids, n.ID)
		byServer[n.LandingServerID] = append(byServer[n.LandingServerID], n)
	}
	rates := tracker.snapshotMany(ids, nowMs)
	for i := range data.Servers {
		s := &data.Servers[i]
		view := landingMonitorServerView{ID: s.ID, Name: s.Name, Source: s.Source, Status: s.Status, Nodes: []landingMonitorNodeView{}}
		nodes := byServer[s.ID]
		var upSum, downSum float64
		rated, enabled := false, false
		for j := range nodes {
			n := &nodes[j]
			status, reason := landingNodeDisplayStatus(n, s)
			nv := landingMonitorNodeView{ID: n.ID, Name: n.Name, Protocol: n.Protocol, Port: n.Port, Status: status, PendingReason: reason, SyncError: n.SyncError, DeletePending: n.DeletePending, Connections: n.Connections, MaxConnections: n.MaxConnections, MaxOnlineIPs: n.MaxOnlineIPs, BilledBytes: n.BilledIn + n.BilledOut, TrafficLimitBytes: n.TrafficLimitBytes}
			if admin {
				nv.UserID = n.UserID
				nv.UserName = data.OwnerNames[n.UserID]
			}
			if rate, ok := rates[n.ID]; ok {
				nv.SampleAt = rate.SampleAt
				nv.OnlineIPs = rate.OnlineIPs
				if rate.RateOK {
					nv.UpRate, nv.DownRate = ratePtr(rate.UpRate), ratePtr(rate.DownRate)
					upSum += rate.UpRate
					downSum += rate.DownRate
					rated = true
				}
			}
			if nv.SampleAt > view.SampleAt {
				view.SampleAt = nv.SampleAt
			}
			view.Connections += n.Connections
			view.BilledBytes += nv.BilledBytes
			view.NodeCount++
			if n.Status == "active" && !n.DeletePending {
				view.ActiveNodeCount++
				enabled = true
			}
			view.Nodes = append(view.Nodes, nv)
		}
		if rated {
			view.UpRate, view.DownRate = ratePtr(upSum), ratePtr(downSum)
		}
		if s.Source == "shared" {
			caps := repo.SharedLandingCaps(s)
			syncErr := s.SharedSyncError
			if data.IntegrationSyncError != "" {
				syncErr = data.IntegrationSyncError
			}
			view.CoreState = caps.Core.State
			view.SyncError = syncErr
			view.Notice = sharedCoreNotice(s, enabled)
			view.Online = caps.Core.State == "running" && syncErr == "" && s.SharedLastSyncAt > now.Add(-2*time.Minute).UnixMilli()
			if s.SharedCollectedAt > view.SampleAt {
				view.SampleAt = s.SharedCollectedAt
			}
			if admin {
				view.MaxConnections = minPositive(s.MaxConnections, caps.MaxConnections)
				view.ShareUsedBytes = caps.TrafficUsed
				view.ShareLimitBytes = caps.TrafficLimit
			}
		} else {
			view.Online = data.NativeOnline[s.NodeID]
			if admin {
				view.MaxConnections = s.MaxConnections
			}
		}
		if !admin {
			q := data.Quotas[s.ID]
			view.QuotaUsedBytes, view.QuotaLimitBytes, view.QuotaPaused = q.UsedBytes, q.LimitBytes, q.Paused
		}
		out.Servers = append(out.Servers, view)
	}
	return out
}

func minPositive(a, b int) int {
	switch {
	case a <= 0:
		return max(b, 0)
	case b <= 0:
		return a
	default:
		return min(a, b)
	}
}
