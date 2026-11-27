package handler

import (
	"fmt"
	"net/http"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/linemon"
	"go-backend/internal/store/model"
)

// Plan 321 (user 11-28): 线路监测 is its own page, and normal users see it
// read-only — the charts the admin leaves 「用户可见」 (default all), their
// lines by name, latency / 丢包 / 断流. Only what the user view needs leaves
// the panel: no node IPs, no DDNS / target host names, no ports, no probe
// error texts (they quote host:port), no node ids, no engine numbers. Every
// mutating endpoint stays under /api/v1/node/line-monitor/ (admins only, the
// JWT middleware answers 403 for anyone else).

type lineMonitorUserLive struct {
	TS     int64         `json:"ts"`
	Status int           `json:"status"`
	Ms     linemon.Float `json:"ms"`
}

type lineMonitorUserLine struct {
	ID         int64  `json:"id"`
	ChartID    int64  `json:"chartId"`
	Kind       string `json:"kind,omitempty"`
	Label      string `json:"label"`
	Color      int    `json:"color"`
	Paused     int    `json:"paused"`
	Sort       int    `json:"sort"`
	SourceName string `json:"sourceName"`
	// SourceOnline: the source node is connected (else 无数据 is expected).
	SourceOnline bool `json:"sourceOnline"`
	// TargetName: the target node's name, the 「自定义目标」 placeholder for a
	// custom host (never the host itself).
	TargetName   string `json:"targetName"`
	TargetCustom bool   `json:"targetCustom,omitempty"`
	// 端内 lines: the 专线 name and whether the 利群入口 is fixed (its name is
	// SourceName) or picked automatically.
	LeiKwanLineName string               `json:"leikwanLineName,omitempty"`
	FixedEntry      bool                 `json:"fixedEntry,omitempty"`
	Live            *lineMonitorUserLive `json:"live,omitempty"`
	CreatedTime     int64                `json:"createdTime"`
}

type lineMonitorUserChart struct {
	ID    int64                 `json:"id"`
	Name  string                `json:"name"`
	Sort  int                   `json:"sort"`
	Lines []lineMonitorUserLine `json:"lines"`
}

func lineMonitorUserAccess(w http.ResponseWriter, r *http.Request) bool {
	if _, _, err := userRoleFromRequest(r); err != nil {
		response.WriteJSON(w, response.Err(401, "无效的token或token已过期"))
		return false
	}
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return false
	}
	return true
}

func userLive(v lineMonitorLive) *lineMonitorUserLive {
	return &lineMonitorUserLive{TS: v.TS, Status: v.Status, Ms: v.Ms}
}

// lineMonitorUserCharts: the visible charts with their lines (user view).
func (h *Handler) lineMonitorUserCharts() ([]lineMonitorUserChart, error) {
	charts, err := h.repo.ListLineMonitorCharts()
	if err != nil {
		return nil, err
	}
	visible := map[int64]bool{}
	out := make([]lineMonitorUserChart, 0, len(charts))
	for _, c := range charts {
		if c.UserHidden {
			continue
		}
		visible[c.ID] = true
		out = append(out, lineMonitorUserChart{ID: c.ID, Name: c.Name, Sort: c.Sort, Lines: []lineMonitorUserLine{}})
	}
	if len(out) == 0 {
		return out, nil
	}
	lines, err := h.repo.ListLineMonitorLines()
	if err != nil {
		return nil, err
	}
	var nodes map[int64]*nodeRecord
	if len(lines) > 0 {
		list, err := h.repo.ListLineMonitorNodeRecords()
		if err != nil {
			return nil, err
		}
		nodes = nodeRecordMap(list)
	}
	rt := h.lineMon()
	rt.mu.Lock()
	live := make(map[int64]lineMonitorLive, len(rt.live))
	for k, v := range rt.live {
		live[k] = v
	}
	rt.mu.Unlock()
	idx := map[int64]int{}
	for i, c := range out {
		idx[c.ID] = i
	}
	var lkLines map[int64]model.LeiKwanLine
	var lkEntries map[int64]leiKwanEntryNode
	for _, l := range lines {
		i, ok := idx[l.ChartID]
		if !ok || !visible[l.ChartID] {
			continue
		}
		v := lineMonitorUserLine{ID: l.ID, ChartID: l.ChartID, Kind: l.Kind, Label: l.Label, Color: l.Color, Paused: l.Paused, Sort: l.Sort, CreatedTime: l.CreatedTime}
		if lv, ok := live[l.ID]; ok {
			v.Live = userLive(lv)
		}
		if l.Kind == model.LineKindLeiKwanEndIn {
			if lkLines == nil {
				lkLines, lkEntries = h.lineMonitorEndInRefs()
			}
			v.LeiKwanLineName, v.SourceOnline = lkLines[l.LeiKwanLineID].Name, true
			if v.LeiKwanLineName == "" {
				v.LeiKwanLineName = fmt.Sprintf("专线 #%d（已删除）", l.LeiKwanLineID)
			}
			if l.SourceNodeID > 0 {
				e := lkEntries[l.SourceNodeID]
				v.SourceName, v.SourceOnline, v.FixedEntry = e.Name, e.Online, true
			}
			out[i].Lines = append(out[i].Lines, v)
			continue
		}
		if src := nodes[l.SourceNodeID]; src != nil {
			v.SourceName = src.Name
			v.SourceOnline = src.IsRemote == 0 && h.lineMonitorSourceOnline(src.ID)
		}
		if l.TargetNodeID > 0 {
			if t := nodes[l.TargetNodeID]; t != nil {
				v.TargetName = t.Name
			}
		} else {
			v.TargetName, v.TargetCustom = "自定义目标", true
		}
		out[i].Lines = append(out[i].Lines, v)
	}
	return out, nil
}

// lineMonitorUserList: POST /api/v1/line-monitor/list (any logged-in user).
func (h *Handler) lineMonitorUserList(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorUserAccess(w, r) {
		return
	}
	charts, err := h.lineMonitorUserCharts()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{
		"charts":       charts,
		"slotSeconds":  linemon.SlotSeconds,
		"retentionDay": linemon.RetentionSeconds / 86400,
	}))
}

type lineMonitorUserData struct {
	ChartID  int64                          `json:"chartId"`
	From     int64                          `json:"from"`
	To       int64                          `json:"to"`
	Step     int64                          `json:"step"`
	Now      int64                          `json:"now"`
	Slot     int64                          `json:"slot"`
	Lines    []linemon.Series               `json:"lines"`
	Stats    []linemon.LineStats            `json:"stats"`
	Events   []linemon.Event                `json:"events"`
	Live     map[int64]*lineMonitorUserLive `json:"live"`
	ViaNames map[int64]string               `json:"viaNames,omitempty"`
}

// lineMonitorUserDataHandler: POST /api/v1/line-monitor/data — the same
// series / stats / events as the admin chart, for a visible chart only, the
// live probe reasons left out.
func (h *Handler) lineMonitorUserDataHandler(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorUserAccess(w, r) {
		return
	}
	var req struct {
		ChartID int64  `json:"chartId"`
		Range   string `json:"range"`
		From    int64  `json:"from"`
		To      int64  `json:"to"`
	}
	if err := lineMonitorDecode(r, &req); err != nil || req.ChartID <= 0 {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	chart, err := h.repo.GetLineMonitorChart(req.ChartID)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	if chart == nil || chart.UserHidden {
		response.WriteJSON(w, response.ErrDefault("监测图不存在"))
		return
	}
	data, err := h.lineMonitorQuery(req.ChartID, req.Range, req.From, req.To, time.Now())
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	out := lineMonitorUserData{ChartID: data.ChartID, From: data.From, To: data.To, Step: data.Step, Now: data.Now, Slot: data.Slot,
		Lines: data.Lines, Stats: data.Stats, Events: data.Events, ViaNames: data.ViaNames, Live: map[int64]*lineMonitorUserLive{}}
	for id, v := range data.Live {
		out.Live[id] = userLive(v)
	}
	response.WriteJSON(w, response.OK(out))
}

// lineMonitorChartUserVisible: POST /api/v1/node/line-monitor/chart/user-visible
// {id, visible} — the admin's per-chart 「用户可见」 switch.
func (h *Handler) lineMonitorChartUserVisible(w http.ResponseWriter, r *http.Request) {
	if !lineMonitorAdmin(w, r) {
		return
	}
	var req struct {
		ID      int64 `json:"id"`
		Visible *bool `json:"visible"`
	}
	if err := lineMonitorDecode(r, &req); err != nil || req.ID <= 0 || req.Visible == nil {
		response.WriteJSON(w, response.ErrDefault("参数错误"))
		return
	}
	ok, err := h.repo.SetLineMonitorChartUserHidden(req.ID, !*req.Visible)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	if !ok {
		response.WriteJSON(w, response.ErrDefault("监测图不存在"))
		return
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"id": req.ID, "userHidden": !*req.Visible}))
}
