package handler

// Plan 310: the 全程 latency tag of a 利群 rule row (入口机 → 利群专线出口,
// the LeiKwan client's own measurement) instead of 「利群 #id」.
//
// Source: the latest host report the entry's agent already sends (stored in
// leikwan_host.report on every ingest) — the rule list only reads it; no new
// writes, dials or agent calls. Agents ≥ plan 310 put the client's
// latest_tcp / active_udp of each forward into the report; the pick (UDP vs
// TCP + the 2000 ms sanity cap) is lkClientPick, the same as 诊断.

import (
	"encoding/json"
	"time"
)

// lkRuleLatency is the tag value of one rule row.
type lkRuleLatency struct {
	Ms   float64 `json:"ms"`
	Src  string  `json:"src"`  // "udp" | "tcp"
	AgeS int64   `json:"ageS"` // seconds since the client measured it (as of this list)
}

// Report cadence of a 利群入口 (agent lkHostManager.tick, see plan 310):
// traffic counting on → every 2 min (1 min tick, ≥ 1 min since the last send),
// off → every 6 min (5 min). A report older than this is not current.
const (
	lkLatencyFreshTraffic = 3 * time.Minute
	lkLatencyFreshIdle    = 7 * time.Minute
	// A client value older than this (forward stopped / client stuck) is not
	// shown as the current latency.
	lkLatencyMaxAgeS = 600
)

// Notes the UI shows in the grey 「— ms」 tag.
const (
	lkLatNoEntry   = "授权没有主入口"
	lkLatOffline   = "入口机离线"
	lkLatNoReport  = "还没有数据（入口机最近没有上报）"
	lkLatOldAgent  = "入口机 agent 需要升级"
	lkLatNoForward = "入口机上没有这条利群转发"
	lkLatNoClient  = "利群客户端还没有数据"
	lkLatStale     = "利群客户端很久没有新数据"
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

// lkRuleLatencyFor: the tag value, or the reason there is none.
func lkRuleLatencyFor(e *leiKwanEntryNode, hr *lkHostRtt, forwardID int64, now time.Time) (*lkRuleLatency, string) {
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
	age := now.Sub(time.UnixMilli(hr.reportedAt)).Milliseconds() / 1000
	if age < 0 {
		age = 0
	}
	if c.AgeS > 0 {
		age += c.AgeS
	}
	if age > lkLatencyMaxAgeS {
		return nil, lkLatStale
	}
	return &lkRuleLatency{Ms: lkRound(c.RttMs), Src: src, AgeS: age}, ""
}
