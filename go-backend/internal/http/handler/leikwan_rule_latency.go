package handler

// Plan 310: the 全程 latency tag of a 利群 rule row (入口机 → 利群专线出口,
// the LeiKwan client's own measurement) instead of 「利群 #id」.
//
// Source: the latest host report the entry's agent already sends (stored in
// leikwan_host.report on every ingest) — the rule list only reads it; no new
// writes, dials or agent calls. Agents ≥ plan 310 put the client's
// latest_tcp / active_udp of each forward into the report; the pick (UDP vs
// TCP + the 2000 ms sanity cap) is lkClientPick, the same as 诊断.
//
// Round 2: no age cap on the client's value. active_udp is measured ONCE when
// the client selects a path (selected_at can stay unchanged for hours while
// the path is kept) — an old UDP value is still the client's current path, so
// it is shown with its real age. latest_tcp (TCP fallback) is the last user
// connection; an old one is likewise the client's latest measurement and also
// carries its age. The only freshness rule is the host report's (3 / 7 min).
//
// Round 3 (user 11-26): the row shows 到落地 = 全程 (UDP only: latest_tcp is a
// user connection that already includes the landing) + 专线出口 → 落地 (the
// landing's ping of the exit, leikwan_land.go). No landing value (手动落地 /
// 外部节点 / landing offline / no exit known) or no UDP 全程 → no tag at all
// (latencyNote still says why, for the API only). landLatency (出口 → 落地
// alone) also goes out for the App's 手机本地 rows (phone 全程 + this).

import (
	"encoding/json"
	"time"
)

// lkRuleLatency is the tag value of one rule row (round 3: 到落地).
type lkRuleLatency struct {
	Ms       float64 `json:"ms"`       // 到落地 = TotalMs + LandMs
	Src      string  `json:"src"`      // "udp"
	AgeS     int64   `json:"ageS"`     // seconds since the client measured 全程 (as of this list)
	TotalMs  float64 `json:"totalMs"`  // 全程: 入口机 → 利群专线出口
	LandMs   float64 `json:"landMs"`   // 专线出口 → 落地
	LandAgeS int64   `json:"landAgeS"` // age of the landing value
	Approx   bool    `json:"approx,omitempty"`
}

// Report cadence of a 利群入口 (agent lkHostManager.tick, see plan 310):
// traffic counting on → every 2 min (1 min tick, ≥ 1 min since the last send),
// off → every 6 min (5 min). A report older than this is not current.
const (
	lkLatencyFreshTraffic = 3 * time.Minute
	lkLatencyFreshIdle    = 7 * time.Minute
)

// Notes the UI shows in the grey 「— ms」 tag.
const (
	lkLatNoEntry   = "授权没有主入口"
	lkLatOffline   = "入口机离线"
	lkLatNoReport  = "还没有数据（入口机最近没有上报）"
	lkLatOldAgent  = "入口机 agent 需要升级"
	lkLatNoForward = "入口机上没有这条利群转发"
	lkLatNoClient  = "利群客户端还没有数据"
	lkLatNoUDP     = "利群客户端没有 UDP 全程"
)

// lkHostRtt: what the rule list needs from one entry's latest report.
type lkHostRtt struct {
	reportedAt int64 // ms
	fresh      time.Duration
	rtt        int
	forwards   map[int64]*lkForwardView
}

// leiKwanHostRtts parses the latest report of the given entry nodes (one
// query for all 利群入口 rows; reports are a few KB each).
func (h *Handler) leiKwanHostRtts(nodes map[int64]bool) map[int64]*lkHostRtt {
	out := map[int64]*lkHostRtt{}
	if len(nodes) == 0 || h == nil || h.repo == nil {
		return out
	}
	hosts, err := h.repo.ListLeiKwanHosts()
	if err != nil {
		return out
	}
	for _, hs := range hosts {
		if !nodes[hs.NodeID] || hs.Report == "" || hs.ReportedAt <= 0 {
			continue
		}
		var rep lkHostReport
		if json.Unmarshal([]byte(hs.Report), &rep) != nil {
			continue
		}
		hr := &lkHostRtt{reportedAt: hs.ReportedAt, fresh: lkLatencyFreshIdle, rtt: rep.State.Rtt, forwards: map[int64]*lkForwardView{}}
		if hs.Traffic == 1 {
			hr.fresh = lkLatencyFreshTraffic
		}
		for i := range rep.State.Forwards {
			f := &rep.State.Forwards[i]
			hr.forwards[f.ForwardID] = f
		}
		out[hs.NodeID] = hr
	}
	return out
}

// lkRuleLatencyFor: the tag value (到落地), or the reason there is none.
// land / landWhy = 专线出口 → 落地 of the rule (lkLandFor).
func lkRuleLatencyFor(e *leiKwanEntryNode, hr *lkHostRtt, forwardID int64, land *lkLandValue, landWhy string, now time.Time) (*lkRuleLatency, string) {
	switch {
	case e == nil:
		return nil, lkLatNoEntry
	case !e.Online:
		return nil, lkLatOffline
	case hr == nil || now.Sub(time.UnixMilli(hr.reportedAt)) > hr.fresh:
		return nil, lkLatNoReport
	case hr.rtt < 1:
		return nil, lkLatOldAgent
	}
	f := hr.forwards[forwardID]
	if forwardID <= 0 || f == nil {
		return nil, lkLatNoForward
	}
	src, c := lkClientPick(f.RttUDP, f.RttTCP)
	if c == nil {
		return nil, lkLatNoClient
	}
	if src != "udp" {
		return nil, lkLatNoUDP
	}
	if land == nil {
		if landWhy == "" {
			landWhy = lkLandPending
		}
		return nil, landWhy
	}
	age := now.Sub(time.UnixMilli(hr.reportedAt)).Milliseconds() / 1000
	if age < 0 {
		age = 0
	}
	if c.AgeS > 0 {
		age += c.AgeS
	}
	total := lkRound(c.RttMs)
	return &lkRuleLatency{Ms: lkRound(total + land.Ms), Src: src, AgeS: age, TotalMs: total, LandMs: land.Ms, LandAgeS: land.AgeS, Approx: land.Approx}, ""
}
