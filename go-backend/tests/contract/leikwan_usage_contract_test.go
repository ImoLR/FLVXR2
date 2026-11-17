package contract_test

// Plan 273 利群自己的流量: the 套餐流量 card / devices are admin only
// (/api/v1/leikwan/usage); every 利群 rule row carries vendorFlow (the
// vendor's bytes of that forward in the cycle) beside — not instead of — our
// own inFlow/outFlow; a granted user gets vendorFlow only on their own rules
// and without share / extra / error; reading the page never calls the vendor.

import (
	"context"
	"testing"
	"time"
)

func TestLeiKwanUsageVisibilityContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { leiKwanUsageVisibilityFlow(t, dialect) })
	}
}

func leiKwanUsageVisibilityFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})

	// Before the first poll: the card says 「还没读到」, rules have no chip.
	v := e.ok("/api/v1/leikwan/usage", e.admin, map[string]interface{}{})
	if v["accountReady"] != true || v["usage"] != nil || v["usageStatus"].(map[string]interface{})["pending"] != true {
		t.Fatalf("usage before the first poll: %v", v)
	}
	for _, r := range e.ruleList(e.admin)["rules"].([]interface{}) {
		if _, ok := r.(map[string]interface{})["vendorFlow"]; ok {
			t.Fatal("vendorFlow before the first poll")
		}
	}

	// alice: granted, one own rule (fake quota raised).
	e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": false})
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 10
	e.fake.Mu.Unlock()
	e.fakeLimitRefresh()
	lines := e.ruleList(e.user)["lines"].([]interface{})
	created := e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": num(lines[0].(map[string]interface{})["id"]), "name": "我的广港", "landingNodeId": 501, "requestId": "req-usage-1"})
	ruleID := num(created["rule"].(map[string]interface{})["id"])

	// Two poll rounds: usage + devices, then service + previous.
	now := time.Now()
	if n := e.h.LeiKwanUsageRound(context.Background(), now, 0); n != 2 {
		t.Fatalf("round 1 reads %d", n)
	}
	if n := e.h.LeiKwanUsageRound(context.Background(), now.Add(150*time.Second), 0); n != 2 {
		t.Fatalf("round 2 reads %d", n)
	}
	reads, writes := e.fake.Counts()

	// Admin: the card, per-forward rows, devices.
	v = e.ok("/api/v1/leikwan/usage", e.admin, map[string]interface{}{})
	u, _ := v["usage"].(map[string]interface{})
	if u == nil || num(u["includedBytes"]) != 1000<<30 || num(u["usedBytes"]) <= 0 || u["cycleStart"] == nil || len(u["forwards"].([]interface{})) == 0 {
		t.Fatalf("admin usage: %v", v)
	}
	if s, _ := v["service"].(map[string]interface{}); s == nil || s["expiresAt"] == nil || s["planName"] == "" {
		t.Fatalf("admin service: %v", v["service"])
	}
	if d, _ := v["devices"].(map[string]interface{}); d == nil || num(d["limit"]) != 5 || len(d["items"].([]interface{})) != 2 {
		t.Fatalf("admin devices: %v", v["devices"])
	} else if first := d["items"].([]interface{})[0].(map[string]interface{}); first["nodeName"] != "阿里广州" {
		t.Fatalf("device IP → node: %v", first)
	}
	if v["previous"] == nil {
		t.Fatalf("admin previous: %v", v)
	}
	admRules := e.ruleList(e.admin)["rules"].([]interface{})
	with := 0
	for _, x := range admRules {
		m := x.(map[string]interface{})
		vf, ok := m["vendorFlow"].(map[string]interface{})
		if !ok {
			continue
		}
		with++
		if vf["share"] == nil || vf["total"] == nil || num(vf["total"]) != num(vf["in"])+num(vf["out"]) || vf["updatedAt"] == nil {
			t.Fatalf("admin vendorFlow: %v", vf)
		}
		// Our own nft-counted 利群流量 stays its own field.
		if _, ok := m["inFlow"]; !ok {
			t.Fatalf("inFlow gone next to vendorFlow: %v", m)
		}
	}
	if with != len(admRules) || with != 8 {
		t.Fatalf("admin rules with vendorFlow %d of %d", with, len(admRules))
	}

	// alice: her rule only, bytes + times, no share / extra / error / totals.
	mine := e.ruleList(e.user)
	if len(mine["rules"].([]interface{})) != 1 {
		t.Fatalf("alice rules: %v", mine["rules"])
	}
	r0 := mine["rules"].([]interface{})[0].(map[string]interface{})
	vf, ok := r0["vendorFlow"].(map[string]interface{})
	if num(r0["id"]) != ruleID || !ok || vf["total"] == nil || vf["cycleStart"] == nil || vf["updatedAt"] == nil {
		t.Fatalf("alice vendorFlow: %v", r0)
	}
	for _, k := range []string{"share", "extra", "state", "error"} {
		if _, leak := vf[k]; leak {
			t.Fatalf("user vendorFlow leaks %s: %v", k, vf)
		}
	}
	for _, k := range []string{"usage", "devices", "service", "packageFlow"} {
		if _, leak := mine[k]; leak {
			t.Fatalf("user rule list leaks %s", k)
		}
	}
	// The admin endpoint is closed to users (granted or not).
	for _, tok := range []string{e.user, e.other} {
		if out, data := e.call("/api/v1/leikwan/usage", tok, map[string]interface{}{}); out.Code == 0 || data != nil {
			t.Fatalf("user reached /leikwan/usage: %+v %v", out, data)
		}
	}
	// bob (no grant): nothing at all.
	if bob := e.ruleList(e.other); len(bob["rules"].([]interface{})) != 0 {
		t.Fatalf("bob rules: %v", bob)
	}
	// Reading never called the vendor.
	if r2, w2 := e.fake.Counts(); r2 != reads || w2 != writes {
		t.Fatalf("page reads hit the vendor: %d/%d -> %d/%d", reads, writes, r2, w2)
	}

	// Vendor down: last good numbers stay, marked stale with the error (admin).
	e.fake.Mu.Lock()
	e.fake.FailReads = 503
	e.fake.Mu.Unlock()
	e.h.LeiKwanUsageRound(context.Background(), now.Add(time.Hour), 0)
	v = e.ok("/api/v1/leikwan/usage", e.admin, map[string]interface{}{})
	st := v["usageStatus"].(map[string]interface{})
	if v["usage"] == nil || st["stale"] != true || st["error"] == nil {
		t.Fatalf("stale usage: %v", st)
	}
	r0 = e.ruleList(e.user)["rules"].([]interface{})[0].(map[string]interface{})
	if vf := r0["vendorFlow"].(map[string]interface{}); vf["stale"] != true || vf["error"] != nil {
		t.Fatalf("user stale chip: %v", vf)
	}
}
