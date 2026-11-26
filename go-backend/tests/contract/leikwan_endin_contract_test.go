package contract_test

// Plan 310 r3 on SQLite and PostgreSQL: 端内 (= the client's UDP 全程 − 入口段,
// both from the 利群入口 report of a plan-310-r3 agent) on the 专线 header of
// the admin line list, and the 线路监测 line kind 「利群专线端内」: options list
// the 专线 / 利群入口, chart save validates the kind, the per-minute pass
// stores one sample + its parts (new line_monitor_part table), the data
// endpoint returns 全程 / 入口段 / 入口机 per bucket; a passive line is never
// TcpPinged and a minute without a reading is 无数据.

import (
	"strconv"
	"testing"

	"go-backend/internal/store/model"
)

func TestLeiKwanEndInContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { leiKwanEndInFlow(t, dialect) })
	}
}

func lkWithSeg(fw map[int64]map[string]interface{}) func(map[string]interface{}) {
	return func(m map[string]interface{}) {
		st := m["state"].(map[string]interface{})
		st["rtt"] = 2
		for _, it := range st["forwards"].([]interface{}) {
			f := it.(map[string]interface{})
			for k, v := range fw[num(f["forwardId"])] {
				f[k] = v
			}
		}
	}
}

func leiKwanEndInFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	var r263, r264 model.LeiKwanRule
	e.must(e.r.DB().Where("forward_id = ?", 263).First(&r263).Error)
	e.must(e.r.DB().Where("forward_id = ?", 264).First(&r264).Error)
	udp := func(ms float64) map[string]interface{} {
		return map[string]interface{}{"rttMs": ms, "p95Ms": ms + 1, "sent": 30, "recv": 30, "ageS": 40}
	}
	e.h.IngestLeiKwanHostReport(58, lkFixtureReport(t, "baidu", lkWithSeg(map[int64]map[string]interface{}{
		263: {"rttUdp": udp(11), "seg": map[string]interface{}{"endpoint": "43.139.2.249:5438", "ms": 2.47, "minMs": 1.87, "conns": 10}},
		// 264: the UDP ingress has no connection on this host → no 端内.
		264: {"rttUdp": udp(60), "seg": map[string]interface{}{"endpoint": "1.2.3.4:5438", "conns": 0}},
	})))
	e.h.RunLeiKwanLandPass()
	lines := e.ok("/api/v1/leikwan/line/list", e.admin, map[string]interface{}{})["lines"].([]interface{})
	byID := map[int64]map[string]interface{}{}
	for _, it := range lines {
		m := it.(map[string]interface{})
		byID[num(m["id"])] = m
	}
	endIn, _ := byID[r263.LineID]["endIn"].(map[string]interface{})
	if endIn == nil || endIn["ms"] != 8.5 || endIn["totalMs"] != 11.0 || endIn["segMs"] != 2.5 || endIn["nodeName"] != "百度云广州" || num(endIn["forwardId"]) != 263 {
		t.Fatalf("广港 endIn %v", byID[r263.LineID]["endIn"])
	}
	if byID[r264.LineID]["endIn"] != nil {
		t.Fatalf("沪日 endIn %v", byID[r264.LineID]["endIn"])
	}

	// 线路监测 options: the 专线 and the 利群入口.
	opts := e.ok("/api/v1/node/line-monitor/options", e.admin, map[string]interface{}{})
	if l, _ := opts["leikwanLines"].([]interface{}); len(l) < 2 {
		t.Fatalf("leikwanLines %v", opts["leikwanLines"])
	}
	entries := map[int64]bool{}
	for _, it := range opts["leikwanEntries"].([]interface{}) {
		entries[num(it.(map[string]interface{})["id"])] = true
	}
	if !entries[58] || !entries[60] || entries[2] {
		t.Fatalf("leikwanEntries %v", opts["leikwanEntries"])
	}
	save := func(lines ...map[string]interface{}) (int, string, map[string]interface{}) {
		list := make([]interface{}, 0, len(lines))
		for _, l := range lines {
			list = append(list, l)
		}
		out, data := e.call("/api/v1/node/line-monitor/chart/save", e.admin, map[string]interface{}{"name": "利群专线端内", "lines": list})
		return out.Code, out.Msg, data
	}
	lk := func(line, src int64) map[string]interface{} {
		return map[string]interface{}{"kind": "lk_endin", "leikwanLineId": line, "sourceNodeId": src}
	}
	for _, bad := range [][]map[string]interface{}{
		{lk(999, 0)},                             // unknown 专线
		{lk(r263.LineID, 2)},                     // not a 利群入口
		{lk(r263.LineID, 0), lk(r263.LineID, 0)}, // duplicate
		{{"kind": "nope", "leikwanLineId": r263.LineID}},
	} {
		if code, msg, _ := save(bad...); code == 0 {
			t.Fatalf("accepted %v", bad)
		} else if msg == "" {
			t.Fatalf("no message for %v", bad)
		}
	}
	code, msg, data := save(lk(r263.LineID, 0), lk(r264.LineID, 60))
	if code != 0 {
		t.Fatalf("save: %d %s", code, msg)
	}
	chartID := num(data["id"])
	list := e.ok("/api/v1/node/line-monitor/list", e.admin, map[string]interface{}{})
	var saved []map[string]interface{}
	for _, c := range list["charts"].([]interface{}) {
		cm := c.(map[string]interface{})
		if num(cm["id"]) == chartID {
			for _, l := range cm["lines"].([]interface{}) {
				saved = append(saved, l.(map[string]interface{}))
			}
		}
	}
	if len(saved) != 2 || saved[0]["kind"] != "lk_endin" || saved[0]["leikwanLineName"] == "" || saved[1]["sourceName"] != "阿里广州" {
		t.Fatalf("saved lines %v", saved)
	}
	// One pass = one sample of the auto line (written: first flush), none for
	// the 沪日 line fixed on 阿里广州 (no reading there).
	e.h.RunLeiKwanLandPass()
	var n int64
	e.must(e.r.DB().Model(&model.LineMonitorPart{}).Count(&n).Error)
	if n != 1 {
		t.Fatalf("parts stored: %d", n)
	}
	d := e.ok("/api/v1/node/line-monitor/data", e.admin, map[string]interface{}{"chartId": chartID, "range": "1h"})
	if num(d["slot"]) != 60 || num(d["step"]) < 60 {
		t.Fatalf("slot %v step %v", d["slot"], d["step"])
	}
	series := d["lines"].([]interface{})
	s0 := series[0].(map[string]interface{})
	found := false
	for i, v := range s0["ok"].([]interface{}) {
		if num(v) == 0 {
			continue
		}
		found = true
		if s0["avg"].([]interface{})[i] != 8.5 || s0["total"].([]interface{})[i] != 11.0 || s0["seg"].([]interface{})[i] != 2.5 || num(s0["via"].([]interface{})[i]) != 58 {
			t.Fatalf("bucket %d: avg %v total %v seg %v via %v", i, s0["avg"].([]interface{})[i], s0["total"], s0["seg"], s0["via"])
		}
	}
	if !found {
		t.Fatalf("no sample: %v", s0["ok"])
	}
	if names, _ := d["viaNames"].(map[string]interface{}); names["58"] != "百度云广州" {
		t.Fatalf("viaNames %v", d["viaNames"])
	}
	if s1 := series[1].(map[string]interface{}); s1["total"] != nil {
		t.Fatalf("沪日 line has parts %v", s1)
	}
	live, _ := d["live"].(map[string]interface{})
	if l1, _ := live[strconv.FormatInt(num(saved[1]["id"]), 10)].(map[string]interface{}); l1 == nil || num(l1["status"]) != 0 || l1["reason"] == "" {
		t.Fatalf("沪日 live %v", live)
	}
}
