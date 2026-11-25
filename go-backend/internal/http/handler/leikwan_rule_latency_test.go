package handler

import (
	"encoding/json"
	"testing"
	"time"
)

// Plan 310: the rule latency tag — same UDP/TCP pick + 2000 ms cap as 诊断,
// fresh reports only (no cap on the client value's own age, round 2), a
// reason when there is no value. Round 3: the tag is 到落地 = UDP 全程 + 专线出口
// → 落地; a TCP-only 全程 or no landing value → no tag.
func TestLeiKwanRuleLatencyFor(t *testing.T) {
	now := time.Now()
	online := &leiKwanEntryNode{ID: 58, Name: "百度云广州", Online: true}
	fw := func(udp, tcp *lkAgentClient) map[int64]*lkForwardView {
		return map[int64]*lkForwardView{263: {ForwardID: 263, RttUDP: udp, RttTCP: tcp}}
	}
	host := func(ago time.Duration, fresh time.Duration, rtt int, f map[int64]*lkForwardView) *lkHostRtt {
		return &lkHostRtt{reportedAt: now.Add(-ago).UnixMilli(), fresh: fresh, rtt: rtt, forwards: f}
	}
	udp := &lkAgentClient{RttMs: 11.04, Sent: 30, Recv: 30, AgeS: 5}
	tcp := &lkAgentClient{RttMs: 107, AgeS: 2}
	land := &lkLandValue{Ms: 3.5, AgeS: 20}
	cases := []struct {
		name string
		e    *leiKwanEntryNode
		hr   *lkHostRtt
		fid  int64
		ms   float64
		src  string
		note string
	}{
		{"udp with probes wins", online, host(30*time.Second, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 263, 14.5, "udp", ""},
		{"bogus udp 60000 → tcp → no tag", online, host(30*time.Second, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 60000}, &lkAgentClient{RttMs: 64})), 263, 0, "", lkLatNoUDP},
		{"bogus udp 3018 with probes → tcp → no tag", online, host(0, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 3018, Sent: 30, Recv: 1}, &lkAgentClient{RttMs: 64})), 263, 0, "", lkLatNoUDP},
		{"udp without probes when no tcp", online, host(0, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 36}, nil)), 263, 39.5, "udp", ""},
		{"only bogus values", online, host(0, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 60000}, nil)), 263, 0, "", lkLatNoClient},
		{"no entry", nil, nil, 263, 0, "", lkLatNoEntry},
		{"entry offline", &leiKwanEntryNode{ID: 58}, host(0, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 263, 0, "", lkLatOffline},
		{"no report", online, nil, 263, 0, "", lkLatNoReport},
		{"stale (traffic on, 4 min)", online, host(4*time.Minute, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 263, 0, "", lkLatNoReport},
		{"traffic off, 6 min is still current", online, host(6*time.Minute, lkLatencyFreshIdle, 1, fw(udp, tcp)), 263, 14.5, "udp", ""},
		{"old agent", online, host(0, lkLatencyFreshTraffic, 0, fw(nil, nil)), 263, 0, "", lkLatOldAgent},
		{"forward not on the host", online, host(0, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 264, 0, "", lkLatNoForward},
		{"rule without forward", online, host(0, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 0, 0, "", lkLatNoForward},
		{"udp measured 3 h ago, fresh report → shown", online, host(30*time.Second, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 40, Sent: 30, Recv: 30, AgeS: 3 * 3600}, tcp)), 263, 43.5, "udp", ""},
		{"udp 3 h old but stale report → none", online, host(4*time.Minute, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 40, Sent: 30, Recv: 30, AgeS: 3 * 3600}, nil)), 263, 0, "", lkLatNoReport},
	}
	for _, c := range cases {
		lat, note := lkRuleLatencyFor(c.e, c.hr, c.fid, land, "", now)
		if c.note != "" {
			if lat != nil || note != c.note {
				t.Errorf("%s: got %+v %q, want note %q", c.name, lat, note, c.note)
			}
			continue
		}
		if lat == nil || lat.Ms != c.ms || lat.Src != c.src || note != "" {
			t.Errorf("%s: got %+v %q, want %v %s", c.name, lat, note, c.ms, c.src)
		}
	}
	// Age = report age + the client's own age; the parts are in the value.
	lat, _ := lkRuleLatencyFor(online, host(40*time.Second, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 263, land, "", now)
	if lat == nil || lat.AgeS < 44 || lat.AgeS > 46 || lat.TotalMs != 11 || lat.LandMs != 3.5 || lat.LandAgeS != 20 || lat.Approx {
		t.Fatalf("age %+v", lat)
	}
	// No landing value → no tag, the landing's reason; ≈ passes through.
	if lat, note := lkRuleLatencyFor(online, host(0, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 263, nil, lkLandManual, now); lat != nil || note != lkLandManual {
		t.Fatalf("manual landing %+v %q", lat, note)
	}
	if lat, _ := lkRuleLatencyFor(online, host(0, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 263, &lkLandValue{Ms: 24, Approx: true}, "", now); lat == nil || lat.Ms != 35 || !lat.Approx {
		t.Fatalf("approx %+v", lat)
	}
	// Round 2: an old UDP pick keeps its real age (selected_at + report age).
	old := &lkAgentClient{RttMs: 11, Sent: 30, Recv: 30, AgeS: 3 * 3600}
	lat, _ = lkRuleLatencyFor(online, host(60*time.Second, lkLatencyFreshTraffic, 1, fw(old, tcp)), 263, land, "", now)
	if lat == nil || lat.Src != "udp" || lat.AgeS < 3*3600+59 || lat.AgeS > 3*3600+61 {
		t.Fatalf("old udp age %+v", lat)
	}
}

// The report the panel stores keeps the plan-310 fields, and the rule view
// (admin + owner) carries latency / latencyNote; users never get forwardId.
func TestLeiKwanRuleLatencyView(t *testing.T) {
	h, r := lkCapsFixture(t)
	rules, _ := r.ListLeiKwanRules(0)
	// Fixture reports are from old agents (no rtt).
	c := h.leiKwanRuleContext(rules, true)
	if v := h.leiKwanRuleView(rules[0], c, true); v["latency"] != nil || v["latencyNote"] != lkLatOldAgent {
		t.Fatalf("old agent: %v %v", v["latency"], v["latencyNote"])
	}
	host, _ := r.GetLeiKwanHost(58)
	host.Traffic = 1
	if err := r.SaveLeiKwanHostConfig(host); err != nil {
		t.Fatal(err)
	}
	// A plan-310 agent report goes through the normal ingest.
	rep := map[string]any{"v": 1, "at": time.Now().UnixMilli(), "state": map[string]any{"agentRunning": true, "rtt": 1, "forwards": []any{
		map[string]any{"forwardId": 263, "listenPort": 10018, "state": "ready", "running": true,
			"rttUdp": map[string]any{"rttMs": 11, "p95Ms": 12, "sent": 30, "recv": 30, "ageS": 4},
			"rttTcp": map[string]any{"rttMs": 107, "ageS": 30}},
		map[string]any{"forwardId": 264, "listenPort": 10015, "state": "ready", "running": true,
			"rttUdp": map[string]any{"rttMs": 60000, "ageS": 1}, "rttTcp": map[string]any{"rttMs": 64, "ageS": 9}},
		map[string]any{"forwardId": 540, "listenPort": 10021, "state": "ready", "running": true},
	}}, "files": map[string]any{}, "manage": map[string]any{"manage": true}}
	raw, _ := json.Marshal(rep)
	h.leiKwanHostIngest(58, raw)
	if stored, _ := r.GetLeiKwanHost(58); stored == nil || stored.ReportedAt == 0 {
		t.Fatal("report not stored")
	}
	c = h.leiKwanRuleContext(rules, true)
	// Round 3: these fixture rules have manual landings → no tag (the reason
	// stays in latencyNote); a TCP-only 全程 → no tag either.
	want := map[int64]string{1: lkLandManual, 2: lkLatNoUDP, 7: lkLatNoClient}
	for _, x := range rules {
		v := h.leiKwanRuleView(x, c, true)
		if v["latency"] != nil || v["latencyNote"] != want[x.ID] || v["landLatency"] != nil {
			t.Fatalf("rule %d: %v %v", x.ID, v["latency"], v["latencyNote"])
		}
	}
	// The owner's view: no forward number.
	uc := h.leiKwanRuleContext(rules, false)
	v := h.leiKwanRuleView(rules[0], uc, false)
	if v["latencyNote"] != lkLandManual || v["forwardId"] != nil {
		t.Fatalf("user view %v %v", v["latencyNote"], v["forwardId"])
	}
	// Entry offline → reason instead of a stale value.
	if err := r.UpdateNodeStatus(58, 0); err != nil {
		t.Fatal(err)
	}
	c = h.leiKwanRuleContext(rules, true)
	if v := h.leiKwanRuleView(rules[0], c, true); v["latency"] != nil || v["latencyNote"] != lkLatOffline {
		t.Fatalf("offline: %v %v", v["latency"], v["latencyNote"])
	}
}
