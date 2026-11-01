package handler

import "testing"

// Plan 184 r3: the UI never shows a ms range, so targets that share one path
// but differ in (shown, whole) ms keep their items; equal ones collapse.
func TestSummarizeRulePickKeepsItemsWhenMsDiffer(t *testing.T) {
	hops := []rulePickHop{{Label: "中转", Name: "hk"}, {Label: "出口", Name: "tw"}}
	mk := func(ms ...float64) *rulePick {
		p := &rulePick{}
		for i, v := range ms {
			p.Items = append(p.Items, rulePickItem{Target: string(rune('a'+i)) + ".example:443", Status: bestPathStatusApplied, Hops: hops, Ms: v, WithTarget: true})
		}
		summarizeRulePick(p)
		return p
	}
	if p := mk(28, 128); !p.Multi || p.MinMs != 28 || p.MaxMs != 128 || p.Layers[1].Name != "tw" {
		t.Fatalf("same path, 28 vs 128 ms: %+v", p)
	}
	if p := mk(28, 28); p.Multi || p.MaxMs != 0 {
		t.Fatalf("same path, same ms: %+v", p)
	}
	// 28.1 vs 28.3 both show 「28 ms」: one line.
	if p := mk(28.1, 28.3); p.Multi {
		t.Fatalf("same shown ms: %+v", p)
	}
	if p := mk(42); p.Multi {
		t.Fatalf("single target: %+v", p)
	}
}
