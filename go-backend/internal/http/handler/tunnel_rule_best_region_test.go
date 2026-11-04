package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Plan 207: the rule line names the region interconnect of the pick.
func TestRuleRegionLabel(t *testing.T) {
	cases := []struct {
		name        string
		relays      []string
		exit        string
		label, hint string
		ok          bool
	}{
		{"JP relay → US exit", []string{"JP"}, "US", "日美", "经日本中转、美国出口", true},
		{"HK relay → JP exit", []string{"HK"}, "JP", "港日", "经香港中转、日本出口", true},
		{"three regions", []string{"HK", "JP"}, "US", "港日美", "经香港、日本中转、美国出口", true},
		{"中转2 same region as exit collapses", []string{"JP", "TW"}, "TW", "日台", "经日本、台湾中转、台湾出口", true},
		{"two relays same region", []string{"JP", "JP"}, "US", "日美", "经日本中转、美国出口", true},
		{"one region, direct exit", nil, "HK", "香港", "香港出口", true},
		{"one region via relay", []string{"HK"}, "HK", "香港", "经香港中转、香港出口", true},
		{"only non-consecutive duplicates stay", []string{"HK", "JP"}, "HK", "港日港", "经香港、日本中转、香港出口", true},
		{"codes are normalised", []string{" jp"}, "us ", "日美", "经日本中转、美国出口", true},
		{"short forms that avoid a clash", []string{"MO"}, "AU", "澳澳洲", "经澳门中转、澳大利亚出口", true},
		{"exit without region", []string{"JP"}, "", "", "", false},
		{"relay without region", []string{""}, "US", "", "", false},
		{"unknown code", []string{"XX"}, "US", "", "", false},
		{"valid ISO code the table does not know", []string{"JP"}, "IT", "", "", false},
	}
	for _, c := range cases {
		label, hint, ok := ruleRegionLabel(c.relays, c.exit)
		if label != c.label || hint != c.hint || ok != c.ok {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q, %v)", c.name, label, hint, ok, c.label, c.hint, c.ok)
		}
	}
}

// One label for the line only when every applied item has the same one.
func TestSummarizeRulePickRegionLabel(t *testing.T) {
	item := func(target, label, hint string, ms float64, regions ...string) rulePickItem {
		hops := []rulePickHop{{Label: "中转", Name: "jp-" + regions[0], Region: regions[0]}, {Label: "出口", Name: "x-" + regions[1], Region: regions[1]}}
		return rulePickItem{Target: target, Status: bestPathStatusApplied, Hops: hops, Ms: ms, RegionLabel: label, RegionHint: hint}
	}
	p := &rulePick{Items: []rulePickItem{item("a", "日美", "经日本中转、美国出口", 120, "JP", "US"), item("b", "日美", "经日本中转、美国出口", 140, "JP", "US")}}
	summarizeRulePick(p)
	if !p.Multi || p.RegionLabel != "日美" || p.RegionHint != "经日本中转、美国出口" || p.Layers[0].Region != "JP" || p.Layers[1].Region != "US" {
		t.Fatalf("uniform labels: %+v", p)
	}
	p = &rulePick{Items: []rulePickItem{item("a", "日美", "经日本中转、美国出口", 120, "JP", "US"), item("b", "日台", "经日本中转、台湾出口", 60, "JP", "TW")}}
	summarizeRulePick(p)
	if !p.Multi || p.RegionLabel != "" || p.RegionHint != "" || p.Layers[0].Region != "JP" || p.Layers[1].Region != "" {
		t.Fatalf("mixed labels: %+v", p)
	}
	// One item without a label (unknown region) ⇒ no line label.
	p = &rulePick{Items: []rulePickItem{item("a", "日美", "经日本中转、美国出口", 120, "JP", "US"), item("b", "", "", 120, "JP", "")}}
	summarizeRulePick(p)
	if p.RegionLabel != "" {
		t.Fatalf("one item without label: %+v", p)
	}
	// Non-applied items do not take part.
	p = &rulePick{Items: []rulePickItem{item("a", "港日", "经香港中转、日本出口", 44, "HK", "JP"), {Target: "b", Status: bestPathStatusUnreachable}}}
	summarizeRulePick(p)
	if p.RegionLabel != "港日" {
		t.Fatalf("applied + unreachable: %+v", p)
	}
}

// End to end through the round's own chain query: node.region reaches the
// pick, fixed layers count, the entry does not; the JSON is additive.
func TestRuleBestPickRegionLabelFromRound(t *testing.T) {
	e, _ := newRuleBestEnv(t)
	for id, region := range map[int64]string{1: "CN", 2: "HK", 3: "jp", 4: "US", 5: "US"} {
		e.exec(`UPDATE node SET region = ? WHERE id = ?`, region, id)
	}
	// Tunnel 21 (prod tunnel 30 shape): 最优 中转 [2 HK, 3 JP] → 最优 中转2
	// [6 TW, 7 TW] → one fixed 出口 8 TW.
	for id, region := range map[int64]string{6: "TW", 7: "TW", 8: "TW"} {
		e.addNode(id, "secret-"+region+string(rune('0'+id)))
		e.exec(`UPDATE node SET name = ?, region = ?, server_ip = ?, server_ip_v4 = ?, tcp_listen_addr = '0.0.0.0', udp_listen_addr = '0.0.0.0' WHERE id = ?`,
			"node-"+string(rune('0'+id)), region, "192.0.2."+string(rune('0'+id)), "192.0.2."+string(rune('0'+id)), id)
	}
	e.addTunnel(21, 1, 1)
	e.exec(`UPDATE tunnel SET type = 2, name = 'jp-tw' WHERE id = 21`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, port, strategy, inx, protocol)
		VALUES(21, '1', 1, NULL, 'round', 1, 'tls'),
		      (21, '2', 2, 2302, 'best', 1, 'tls'),
		      (21, '2', 3, 2303, 'best', 1, 'tls'),
		      (21, '2', 6, 2306, 'best', 2, 'tls'),
		      (21, '2', 7, 2307, 'best', 2, 'tls'),
		      (21, '3', 8, 2308, 'round', 1, 'tls')`)
	e.addForward(41, 2, 21)
	e.exec(`UPDATE forward SET remote_addr = 'e.example:443', mode = 'gost', name = 'jp-tw-rule' WHERE id = 41`)
	e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(41, 1, 30041)`)

	edges := rbEnvEdges()
	for k, v := range map[[2]int64]float64{{2, 6}: 200, {2, 7}: 210, {3, 6}: 20, {3, 7}: 25, {6, 8}: 5, {7, 8}: 6} {
		edges[k] = v
	}
	now := time.Now()
	rbEnvRound(t, e, edges, rbEnvDests(), now.Add(-time.Minute))
	rbEnvRound(t, e, edges, rbEnvDests(), now)

	items := rbItems(31, 32)
	items = append(items, map[string]interface{}{"id": int64(41), "tunnelId": int64(21), "remoteAddr": "e.example:443", "mode": "gost"})
	entries := map[int64][]int64{31: {1}, 32: {1}, 41: {1}}
	e.h.attachRuleBestPicks(items, entries, now)

	// Rule 32: 中转 node-3 (JP) → 出口 node-5 (US); the CN entry is not part.
	p := items[1]["bestPick"].(*rulePick)
	if p.Summary != "中转 node-3 → 出口 node-5" || p.RegionLabel != "日美" || p.RegionHint != "经日本中转、美国出口" {
		t.Fatalf("rule 32 %+v", p)
	}
	if p.Layers[0].Region != "JP" || p.Layers[1].Region != "US" {
		t.Fatalf("rule 32 layer regions %+v", p.Layers)
	}
	// Rule 31: two targets, two US exits ⇒ rows, one shared label.
	p = items[0]["bestPick"].(*rulePick)
	if !p.Multi || p.RegionLabel != "日美" || len(p.Items) != 2 || p.Items[0].RegionLabel != "日美" || p.Items[1].RegionLabel != "日美" {
		t.Fatalf("rule 31 %+v", p)
	}
	// Rule 41: JP 中转 → TW 中转2 → fixed TW 出口 ⇒ 日台 (fixed layer counted,
	// duplicates collapsed); hops stay the 最优 layers only.
	p = items[2]["bestPick"].(*rulePick)
	if p.Status != bestPathStatusApplied || p.RegionLabel != "日台" || p.RegionHint != "经日本、台湾中转、台湾出口" {
		t.Fatalf("rule 41 %+v", p)
	}
	if len(p.Layers) != 2 || p.Layers[0].Label != "中转1" || p.Layers[0].Name != "node-3" || p.Layers[1].Label != "中转2" || p.Layers[1].Region != "TW" {
		t.Fatalf("rule 41 layers %+v", p.Layers)
	}

	// Mixed: exit 4 moves to TW ⇒ target a (exit 4) 日台, b (exit 5) 日美.
	e.exec(`UPDATE node SET region = 'TW' WHERE id = 4`)
	rbEnvRound(t, e, edges, rbEnvDests(), now)
	items = rbItems(31)
	e.h.attachRuleBestPicks(items, map[int64][]int64{31: {1}}, now)
	p = items[0]["bestPick"].(*rulePick)
	labels := map[string]string{}
	for _, it := range p.Items {
		labels[it.Target] = it.RegionLabel
	}
	if p.RegionLabel != "" || labels["a.example:443"] != "日台" || labels["b.example:443"] != "日美" {
		t.Fatalf("mixed labels %+v / %v", p, labels)
	}

	// A node without region ⇒ no label anywhere on that line (node names stay).
	e.exec(`UPDATE node SET region = '' WHERE id = 5`)
	rbEnvRound(t, e, edges, rbEnvDests(), now)
	items = rbItems(32)
	e.h.attachRuleBestPicks(items, map[int64][]int64{32: {1}}, now)
	p = items[0]["bestPick"].(*rulePick)
	if p.Summary != "中转 node-3 → 出口 node-5" || p.RegionLabel != "" || p.RegionHint != "" {
		t.Fatalf("no-region fallback %+v", p)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "regionLabel") {
		t.Fatalf("empty label must be omitted: %s", raw)
	}
}
