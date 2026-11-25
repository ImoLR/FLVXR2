package handler

import (
	"encoding/json"
	"testing"
	"time"
)

// Plan 310: the rule latency tag — same UDP/TCP pick + 2000 ms cap as 诊断,
// fresh reports only, a reason when there is no value.
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
	cases := []struct {
		name string
		e    *leiKwanEntryNode
		hr   *lkHostRtt
		fid  int64
		ms   float64
		src  string
		note string
	}{
		{"udp with probes wins", online, host(30*time.Second, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 263, 11, "udp", ""},
		{"bogus udp 60000 → tcp", online, host(30*time.Second, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 60000}, &lkAgentClient{RttMs: 64})), 263, 64, "tcp", ""},
		{"bogus udp 3018 with probes → tcp", online, host(0, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 3018, Sent: 30, Recv: 1}, &lkAgentClient{RttMs: 64})), 263, 64, "tcp", ""},
		{"udp without probes when no tcp", online, host(0, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 36}, nil)), 263, 36, "udp", ""},
		{"only bogus values", online, host(0, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 60000}, nil)), 263, 0, "", lkLatNoClient},
		{"no entry", nil, nil, 263, 0, "", lkLatNoEntry},
		{"entry offline", &leiKwanEntryNode{ID: 58}, host(0, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 263, 0, "", lkLatOffline},
		{"no report", online, nil, 263, 0, "", lkLatNoReport},
		{"stale (traffic on, 4 min)", online, host(4*time.Minute, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 263, 0, "", lkLatNoReport},
		{"traffic off, 6 min is still current", online, host(6*time.Minute, lkLatencyFreshIdle, 1, fw(udp, tcp)), 263, 11, "udp", ""},
		{"old agent", online, host(0, lkLatencyFreshTraffic, 0, fw(nil, nil)), 263, 0, "", lkLatOldAgent},
		{"forward not on the host", online, host(0, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 264, 0, "", lkLatNoForward},
		{"rule without forward", online, host(0, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 0, 0, "", lkLatNoForward},
		{"client value too old", online, host(0, lkLatencyFreshTraffic, 1, fw(&lkAgentClient{RttMs: 40, Sent: 3, Recv: 3, AgeS: 900}, nil)), 263, 0, "", lkLatStale},
	}
	for _, c := range cases {
		lat, note := lkRuleLatencyFor(c.e, c.hr, c.fid, now)
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
	// Age = report age + the client's own age.
	lat, _ := lkRuleLatencyFor(online, host(40*time.Second, lkLatencyFreshTraffic, 1, fw(udp, tcp)), 263, now)
	if lat == nil || lat.AgeS < 44 || lat.AgeS > 46 {
		t.Fatalf("age %+v", lat)
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
	want := map[int64][2]any{1: {11.0, "udp"}, 2: {64.0, "tcp"}}
	for _, x := range rules {
		v := h.leiKwanRuleView(x, c, true)
		if w, ok := want[x.ID]; ok {
			lat, _ := v["latency"].(*lkRuleLatency)
			if lat == nil || lat.Ms != w[0] || lat.Src != w[1] || v["latencyNote"] != nil {
				t.Fatalf("rule %d: %+v %v", x.ID, lat, v["latencyNote"])
			}
			continue
		}
		if v["latencyNote"] != lkLatNoClient {
			t.Fatalf("rule %d note %v", x.ID, v["latencyNote"])
		}
	}
	// The owner's view: same tag, no forward number.
	uc := h.leiKwanRuleContext(rules, false)
	v := h.leiKwanRuleView(rules[0], uc, false)
	if lat, _ := v["latency"].(*lkRuleLatency); lat == nil || lat.Ms != 11 || v["forwardId"] != nil {
		t.Fatalf("user view %v %v", v["latency"], v["forwardId"])
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
