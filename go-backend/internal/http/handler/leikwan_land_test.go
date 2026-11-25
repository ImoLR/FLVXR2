package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 310 r3: the per-minute 利群 pass — 专线出口 → 落地 (exit from
// LandingPeerRtt ≤ every 10 min, ONE echo per (landing node, exit IP) a
// minute, ≈ connection rtt when the exit never answers) and 端内 = UDP 全程 −
// 入口段 per 专线 (header + 线路监测 「利群专线端内」 samples).

type lkLandLab struct {
	mu        sync.Mutex
	peers     map[int][]map[string]interface{} // landing port → LandingPeerRtt peers
	ping      map[string]float64               // exit IP → whole ms (absent = no answer)
	peerCalls map[int]int
	echoes    map[string]int
	offline   map[int64]bool
}

func newLKLandLab(t *testing.T) (*Handler, *repo.Repository, *lkLandLab) {
	t.Helper()
	r, err := repo.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	h := &Handler{repo: r}
	db := r.DB()
	now := time.Now().UnixMilli()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.Create(&model.NodeGroup{ID: 5, Name: "利群入口", CreatedTime: now}).Error)
	grp := sql.NullInt64{Int64: 5, Valid: true}
	for _, n := range []model.Node{{ID: 58, Name: "百度云广州", ServerIP: "106.13.17.23", GroupID: grp}, {ID: 60, Name: "阿里广州", ServerIP: "8.134.172.154", GroupID: grp},
		{ID: 62, Name: "Boil Hinet 马年", ServerIP: "203.0.113.62"}, {ID: 63, Name: "Fachost", ServerIP: "203.0.113.63"}, {ID: 47, Name: "AWS HK", ServerIP: "43.199.143.188"}} {
		n.Secret, n.Port, n.CreatedTime, n.Status = "s", "1-65535", now, 1
		must(db.Create(&n).Error)
	}
	must(db.Create(&model.LandingServer{ID: 14, Source: "native", NodeID: 62, Name: "马年", PublicHost: "203.0.113.62", Status: "active", CreatedTime: now, UpdatedTime: now}).Error)
	must(db.Create(&model.LandingServer{ID: 16, Source: "native", NodeID: 63, Name: "Fachost", PublicHost: "203.0.113.63", Status: "active", CreatedTime: now, UpdatedTime: now}).Error)
	must(db.Create(&model.LandingServer{ID: 17, Source: "shared", NodeID: 0, Name: "妙妙屋", PublicHost: "198.51.100.7", Status: "active", CreatedTime: now, UpdatedTime: now}).Error)
	for _, n := range []model.LandingNode{{ID: 226, LandingServerID: 14, Port: 10019}, {ID: 227, LandingServerID: 14, Port: 10020},
		{ID: 228, LandingServerID: 16, Port: 30001}, {ID: 229, LandingServerID: 17, Port: 40000}} {
		n.UserID, n.Name, n.Protocol, n.InboundTag, n.Status = 1, "ln", "ss2022", "flvxl", "active"
		must(db.Create(&n).Error)
	}
	must(db.Create(&model.LeiKwanSetting{ID: 1, AccountID: 1, EntryGroupID: 5}).Error)
	must(db.Create(&model.LeiKwanLine{ID: 3, AccountID: 1, RouteID: 29, Name: "利群·广台", RouteLabel: "CAN -> TPE", Status: 1}).Error)
	must(db.Create(&model.LeiKwanLine{ID: 4, AccountID: 1, RouteID: 30, Name: "利群·福台", RouteLabel: "FOC -> TPE", Status: 1}).Error)
	must(db.Create(&model.LeiKwanGrant{UserID: 1, MainEntryNodeID: 58, Status: 1}).Error)
	for _, x := range []model.LeiKwanRule{
		{ID: 4, LineID: 3, ForwardID: 280, Name: "利群广台 Hinet", ListenPort: 10020, LandingNodeID: 227},
		{ID: 6, LineID: 4, ForwardID: 282, Name: "利群福台 Hinet", ListenPort: 10019, LandingNodeID: 226},
		{ID: 8, LineID: 4, ForwardID: 615, Name: "利群福台 Seednet", ListenPort: 30000, LandingNodeID: 228},
		{ID: 9, LineID: 3, ForwardID: 281, Name: "利群广台 手动", ListenPort: 10021, TargetHost: "198.51.100.9", TargetPort: 1},
		{ID: 10, LineID: 3, ForwardID: 290, Name: "外部", ListenPort: 10022, ExternalNodeID: 5},
		{ID: 11, LineID: 3, ForwardID: 291, Name: "共享落地", ListenPort: 10023, LandingNodeID: 229},
	} {
		x.UserID, x.Status = 1, 1
		must(db.Create(&x).Error)
	}
	lab := &lkLandLab{peerCalls: map[int]int{}, echoes: map[string]int{}, offline: map[int64]bool{},
		peers: map[int][]map[string]interface{}{
			// 10020: the 广台 exit + the 备线 exit (AWS HK is an FLVX node → not the exit).
			10020: {{"ip": "43.199.143.188", "conns": 9, "rttMs": 30.0}, {"ip": "210.61.104.139", "conns": 3, "rttMs": 24.0, "minRttMs": 23.0}},
			10019: {{"ip": "154.206.93.221", "conns": 2, "rttMs": 11.0}},
			30001: {{"ip": "202.5.245.92", "conns": 1, "rttMs": 12.5, "minRttMs": 11.7}},
		},
		ping: map[string]float64{"210.61.104.139": 5, "154.206.93.221": 10}}
	h.nodeOnlineFn = func(id int64) bool {
		lab.mu.Lock()
		defer lab.mu.Unlock()
		return !lab.offline[id]
	}
	h.nodeCommandSender = func(node int64, cmd string, data interface{}, timeout time.Duration) (ws.CommandResult, error) {
		lab.mu.Lock()
		defer lab.mu.Unlock()
		m, _ := data.(map[string]interface{})
		switch cmd {
		case "LandingPeerRtt":
			port, _ := m["port"].(int)
			lab.peerCalls[port]++
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"v": 1.0, "port": float64(port), "peers": lab.peers[port]}}, nil
		case "ServiceMonitorCheck":
			ip, _ := m["target"].(string)
			if m["type"] != "icmp" {
				t.Errorf("echo type %v", m["type"])
			}
			lab.echoes[ip]++
			if v, ok := lab.ping[ip]; ok {
				return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": true, "latencyMs": v}}, nil
			}
			return ws.CommandResult{Success: true, Data: map[string]interface{}{"success": false, "latencyMs": 2000.0, "errorMessage": "timeout"}}, nil
		}
		return ws.CommandResult{}, errors.New("unknown command " + cmd)
	}
	return h, r, lab
}

func lkRule(t *testing.T, r *repo.Repository, id int64) model.LeiKwanRule {
	t.Helper()
	x, err := r.GetLeiKwanRule(id)
	if err != nil || x == nil {
		t.Fatalf("rule %d: %v", id, err)
	}
	return *x
}

func TestLeiKwanLandTick(t *testing.T) {
	h, r, lab := newLKLandLab(t)
	t0 := time.Now()
	h.leiKwanLandTick(context.Background(), t0)
	lab.mu.Lock()
	if lab.peerCalls[10020] != 1 || lab.peerCalls[10019] != 1 || lab.peerCalls[30001] != 1 || len(lab.peerCalls) != 3 {
		t.Fatalf("peer reads %v (shared / manual / external landings must not be read)", lab.peerCalls)
	}
	if lab.echoes["210.61.104.139"] != 1 || lab.echoes["154.206.93.221"] != 1 || lab.echoes["202.5.245.92"] != 1 || lab.echoes["43.199.143.188"] != 0 {
		t.Fatalf("echoes %v", lab.echoes)
	}
	lab.mu.Unlock()
	land := func(id int64, at time.Time) (*lkLandValue, string) { return h.lkLandFor(lkRule(t, r, id), at) }
	if v, why := land(4, t0); v == nil || v.Ms != 5.5 || v.Approx || why != "" {
		t.Fatalf("广台 %+v %q", v, why)
	}
	if v, _ := land(6, t0); v == nil || v.Ms != 10.5 {
		t.Fatalf("福台 %+v", v)
	}
	// The Fachost exit does not answer ping → the connection rtt, ≈.
	if v, _ := land(8, t0); v == nil || v.Ms != 11.7 || !v.Approx {
		t.Fatalf("Seednet %+v", v)
	}
	for id, want := range map[int64]string{9: lkLandManual, 10: lkLandExternal, 11: lkLandShared} {
		if v, why := land(id, t0); v != nil || why != want {
			t.Fatalf("rule %d: %+v %q", id, v, why)
		}
	}
	// Next minute: the exits are cached (no LandingPeerRtt), one echo per pair.
	h.leiKwanLandTick(context.Background(), t0.Add(time.Minute))
	lab.mu.Lock()
	if lab.peerCalls[10020] != 1 || lab.peerCalls[30001] != 1 || lab.echoes["210.61.104.139"] != 2 || lab.echoes["202.5.245.92"] != 2 {
		t.Fatalf("minute 2: peers %v echoes %v", lab.peerCalls, lab.echoes)
	}
	// Two landing ports with the same exit IP on one node share the echo.
	lab.peers[10019] = []map[string]interface{}{{"ip": "210.61.104.139", "conns": 1, "rttMs": 24.0}}
	lab.mu.Unlock()
	// Minute 3: the non-answering exit is re-read (2 min retry); the 广台
	// exit loses its echo → the last answer is kept (≤ 5 min).
	lab.mu.Lock()
	delete(lab.ping, "210.61.104.139")
	lab.mu.Unlock()
	h.leiKwanLandTick(context.Background(), t0.Add(2*time.Minute))
	lab.mu.Lock()
	if lab.peerCalls[30001] != 2 || lab.peerCalls[10020] != 1 {
		t.Fatalf("minute 3 peers %v", lab.peerCalls)
	}
	lab.mu.Unlock()
	if v, _ := land(4, t0.Add(2*time.Minute)); v == nil || v.Ms != 5.5 || v.Approx || v.AgeS != 60 {
		t.Fatalf("held echo %+v", v)
	}
	// No answer for > 5 min → the connection rtt (min_rtt), ≈; the port is
	// re-read meanwhile (the exit may have rotated) and the shared IP is
	// echoed once per minute.
	for m := 3; m <= 7; m++ {
		h.leiKwanLandTick(context.Background(), t0.Add(time.Duration(m)*time.Minute))
	}
	if v, _ := land(4, t0.Add(7*time.Minute)); v == nil || v.Ms != 23 || !v.Approx {
		t.Fatalf("no echo for 6 min %+v", v)
	}
	lab.mu.Lock()
	if lab.peerCalls[10020] < 2 {
		t.Fatalf("10020 not re-read after failed echoes: %v", lab.peerCalls)
	}
	echoes := lab.echoes["210.61.104.139"]
	lab.mu.Unlock()
	h.leiKwanLandTick(context.Background(), t0.Add(8*time.Minute))
	lab.mu.Lock()
	if got := lab.echoes["210.61.104.139"] - echoes; got != 1 {
		t.Fatalf("shared exit IP echoed %d times in one minute", got)
	}
	// The landing node goes offline: nothing sent to it, no value.
	lab.offline[62] = true
	before := lab.echoes["210.61.104.139"]
	lab.mu.Unlock()
	h.leiKwanLandTick(context.Background(), t0.Add(9*time.Minute))
	if v, why := land(4, t0.Add(9*time.Minute)); v != nil || why != lkLandOffline {
		t.Fatalf("offline %+v %q", v, why)
	}
	lab.mu.Lock()
	if lab.echoes["210.61.104.139"] != before {
		t.Fatal("echo sent to an offline landing")
	}
	lab.mu.Unlock()
}

// lkSegReport: a plan-310-r3 agent report of one 利群入口.
func lkSegReport(fw ...map[string]any) json.RawMessage {
	list := make([]any, 0, len(fw))
	for _, f := range fw {
		list = append(list, f)
	}
	raw, _ := json.Marshal(map[string]any{"v": 1, "at": time.Now().UnixMilli(), "state": map[string]any{"agentRunning": true, "rtt": 2, "forwards": list},
		"files": map[string]any{}, "manage": map[string]any{"manage": false}})
	return raw
}

func lkSegFw(id int64, udp, seg float64, udpAge int64, conns int) map[string]any {
	f := map[string]any{"forwardId": id, "listenPort": 10000, "state": "ready", "running": true,
		"rttUdp": map[string]any{"rttMs": udp, "sent": 30, "recv": 30, "ageS": udpAge}}
	if seg >= 0 {
		f["seg"] = map[string]any{"endpoint": "120.232.166.118:5438", "ms": seg, "minMs": seg - 0.3, "conns": conns}
	}
	return f
}

func TestLeiKwanEndIn(t *testing.T) {
	h, r, _ := newLKLandLab(t)
	for _, id := range []int64{58, 60} {
		host, err := r.EnsureLeiKwanHost(id)
		if err != nil {
			t.Fatal(err)
		}
		host.Traffic = 1
		if err := r.SaveLeiKwanHostConfig(host); err != nil {
			t.Fatal(err)
		}
	}
	// 百度云广州: 广台 280 (UDP 25, 入口段 5.9), 福台 282 (UDP 35, no connection
	// to its ingress → no 入口段), 615 TCP only.
	h.leiKwanHostIngest(58, lkSegReport(lkSegFw(280, 25, 5.9, 90, 2), lkSegFw(282, 35, 0, 30, 0),
		map[string]any{"forwardId": 615, "running": true, "rttTcp": map[string]any{"rttMs": 61, "ageS": 3}, "seg": map[string]any{"endpoint": "1.2.3.4:5438", "ms": 2.0, "conns": 1}}))
	// 阿里广州: 广台 280 measured more recently (UDP 23, 入口段 2.5).
	h.leiKwanHostIngest(60, lkSegReport(lkSegFw(280, 23, 2.5, 10, 10)))
	now := time.Now()
	h.leiKwanLandTick(context.Background(), now)
	v := h.lkLineEndIn(3, true, now)
	if v == nil || v.Ms != 20.5 || v.TotalMs != 23 || v.SegMs != 2.5 || v.NodeName != "阿里广州" || v.RuleName != "利群广台 Hinet" || v.Readings != 2 {
		t.Fatalf("广台 header %+v", v)
	}
	if u := h.lkLineEndIn(3, false, now); u == nil || u.RuleName != "" || u.ForwardID != 0 || u.Ingress != "" {
		t.Fatalf("user view %+v", u)
	}
	// 福台: no 入口段 (no connection) and a TCP-only 全程 → no 端内.
	if v := h.lkLineEndIn(4, true, now); v != nil {
		t.Fatalf("福台 %+v", v)
	}
	// Sticky: 百度云广州 now has the fresher 全程, the header keeps 阿里广州.
	h.leiKwanHostIngest(58, lkSegReport(lkSegFw(280, 26, 5.9, 1, 2)))
	h.leiKwanLandTick(context.Background(), now.Add(time.Second))
	if v := h.lkLineEndIn(3, true, now.Add(time.Second)); v == nil || v.NodeName != "阿里广州" {
		t.Fatalf("sticky %+v", v)
	}
	// An old agent (rtt 1) has no 入口段 → its readings disappear.
	raw, _ := json.Marshal(map[string]any{"v": 1, "at": time.Now().UnixMilli(), "state": map[string]any{"rtt": 1, "forwards": []any{lkSegFw(280, 23, 2.5, 10, 10)}},
		"files": map[string]any{}, "manage": map[string]any{}})
	h.leiKwanHostIngest(60, raw)
	h.leiKwanLandTick(context.Background(), now.Add(2*time.Second))
	if v := h.lkLineEndIn(3, true, now.Add(2*time.Second)); v == nil || v.NodeName != "百度云广州" || v.Ms != 20.1 || v.Readings != 1 {
		t.Fatalf("after 阿里广州 lost 入口段 %+v", v)
	}
	h.leiKwanHostIngest(60, lkSegReport(lkSegFw(280, 23, 2.5, 10, 10)))

	// 线路监测: an auto 广台 line and a 福台 line fixed on 阿里广州 (no reading).
	chart := &model.LineMonitorChart{Name: "利群专线端内"}
	lines := []model.LineMonitorLine{{Kind: model.LineKindLeiKwanEndIn, LeiKwanLineID: 3}, {Kind: model.LineKindLeiKwanEndIn, LeiKwanLineID: 4, SourceNodeID: 60}}
	if _, err := r.SaveLineMonitorChart(chart, lines, 40, nil); err != nil {
		t.Fatal(err)
	}
	saved, _ := r.ListLineMonitorChartLines(chart.ID)
	if len(saved) != 2 || saved[0].Kind != model.LineKindLeiKwanEndIn || saved[0].LeiKwanLineID != 3 || saved[1].SourceNodeID != 60 {
		t.Fatalf("saved %+v", saved)
	}
	// Passive lines never become TcpPing targets.
	if targets, _, err := h.lineMonitorTargets(); err != nil || len(targets) != 0 {
		t.Fatalf("targets %+v %v", targets, err)
	}
	base := time.Now().Truncate(time.Minute)
	for m := 0; m < 3; m++ {
		h.leiKwanLandTick(context.Background(), base.Add(time.Duration(m)*time.Minute+5*time.Second))
	}
	h.lineMonitorFlush(true)
	data, err := h.lineMonitorQuery(chart.ID, "1h", 0, 0, base.Add(3*time.Minute+10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if data.Step < 60 || data.Slot != 60 || len(data.Lines) != 2 {
		t.Fatalf("step %d slot %d lines %d", data.Step, data.Slot, len(data.Lines))
	}
	s := data.Lines[0]
	got := 0
	for i := range s.OK {
		if s.OK[i] == 0 {
			continue
		}
		got++
		if s.Avg[i] != 20.1 && s.Avg[i] != 20.5 || s.Total == nil || s.Seg == nil || s.Via[i] == 0 {
			t.Fatalf("bucket %d avg %v total %v seg %v via %v", i, s.Avg[i], s.Total, s.Seg, s.Via)
		}
		if s.Total[i]-s.Seg[i] != s.Avg[i] {
			t.Fatalf("parts %v − %v ≠ %v", s.Total[i], s.Seg[i], s.Avg[i])
		}
	}
	if got != 3 {
		t.Fatalf("%d samples, want 3: ok %v", got, s.OK)
	}
	if data.ViaNames[60] != "阿里广州" && data.ViaNames[58] != "百度云广州" {
		t.Fatalf("via names %v", data.ViaNames)
	}
	if st := data.Stats[0]; st.Down != 0 || st.DownEvents != 0 || st.Measured != 3 {
		t.Fatalf("stats %+v", st)
	}
	// The fixed 福台 line has no reading → no sample (无数据), a live reason.
	if n := len(data.Lines[1].OK); n == 0 {
		t.Fatal("no buckets")
	}
	for _, v := range data.Lines[1].OK {
		if v != 0 {
			t.Fatalf("福台 line got samples %v", data.Lines[1].OK)
		}
	}
	if lv, ok := data.Live[saved[1].ID]; !ok || lv.Status != 0 || lv.Reason == "" {
		t.Fatalf("福台 live %+v", lv)
	}
	// Deleting the chart removes the parts with the samples.
	if _, err := r.DeleteLineMonitorChart(chart.ID); err != nil {
		t.Fatal(err)
	}
	for _, l := range saved {
		if _, err := r.DeleteLineMonitorSamplesBefore(l.ID, 1<<62, 2000); err != nil {
			t.Fatal(err)
		}
		n := 0
		_ = r.ReadLineMonitorParts(l.ID, 0, 1<<32-1, func(repo.LineMonitorPartRow) { n++ })
		if n != 0 {
			t.Fatalf("line %d: %d parts left", l.ID, n)
		}
	}
}

// The rule view: 到落地 = UDP 全程 of the shown entry + 专线出口 → 落地.
func TestLeiKwanRuleLatencyLanding(t *testing.T) {
	h, r, _ := newLKLandLab(t)
	host, _ := r.EnsureLeiKwanHost(58)
	host.Traffic = 1
	_ = r.SaveLeiKwanHostConfig(host)
	h.leiKwanHostIngest(58, lkSegReport(lkSegFw(280, 19, 3, 40, 2), lkSegFw(282, 7.4, -1, 10, 0),
		map[string]any{"forwardId": 615, "running": true, "rttTcp": map[string]any{"rttMs": 61, "ageS": 3}}))
	h.leiKwanLandTick(context.Background(), time.Now())
	rules, _ := r.ListLeiKwanRules(0)
	c := h.leiKwanRuleContext(rules, true)
	by := map[int64]map[string]interface{}{}
	for _, x := range rules {
		by[x.ID] = h.leiKwanRuleView(x, c, true)
	}
	lat, _ := by[4]["latency"].(*lkRuleLatency)
	if lat == nil || lat.Ms != 24.5 || lat.TotalMs != 19 || lat.LandMs != 5.5 || lat.Src != "udp" || lat.Approx {
		t.Fatalf("广台 %+v %v", lat, by[4]["latencyNote"])
	}
	if land, _ := by[4]["landLatency"].(*lkLandValue); land == nil || land.Ms != 5.5 {
		t.Fatalf("landLatency %+v", by[4]["landLatency"])
	}
	if lat, _ := by[6]["latency"].(*lkRuleLatency); lat == nil || lat.Ms != 17.9 {
		t.Fatalf("福台 %+v", by[6]["latency"])
	}
	// TCP-only 全程 → no tag (latest_tcp already includes the landing), even with a landing value.
	if by[8]["latency"] != nil || by[8]["latencyNote"] != lkLatNoUDP || by[8]["landLatency"] == nil {
		t.Fatalf("Seednet %v %v %v", by[8]["latency"], by[8]["latencyNote"], by[8]["landLatency"])
	}
	for _, id := range []int64{9, 10, 11} {
		if by[id]["latency"] != nil || by[id]["landLatency"] != nil {
			t.Fatalf("rule %d %v", id, by[id])
		}
	}
}
