package repo

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

// Plan 290: Cloudflare-hosted Reality candidates.

func cfTestRepo(t *testing.T) *Repository {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "cf.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func cfRow(t *testing.T, r *Repository, domain string) model.LandingRealityDomain {
	t.Helper()
	var d model.LandingRealityDomain
	if err := r.DB().Where("domain = ?", domain).First(&d).Error; err != nil {
		t.Fatalf("row %s: %v", domain, err)
	}
	return d
}

func TestRealityCFVerdictsBlockLiftKeepManual(t *testing.T) {
	r := cfTestRepo(t)
	if _, err := r.AddLandingRealityDomain("cf.example.net"); err != nil {
		t.Fatal(err)
	}
	// Hosted → marked, effective block, not a probe target, unblock refused.
	ch, err := r.ApplyRealityCFVerdicts(map[string]RealityCFVerdict{
		"cf.example.net":     {Hosted: true, Known: true, Evidence: "104.16.1.1（面板解析）"},
		"www.tesla.com":      {Known: true},
		"www.microsoft.com":  {Known: true}, // risk-rule block stays
		"www.cloudflare.com": {Hosted: true, Known: true, Evidence: "104.16.123.96（面板解析）"},
		"www.nvidia.com":     {Hosted: true}, // unknown: ignored
	})
	if err != nil || len(ch) != 2 || ch[0].Domain != "cf.example.net" || !ch[0].Hosted || ch[1].Domain != "www.cloudflare.com" {
		t.Fatalf("changes %+v %v", ch, err)
	}
	cf := cfRow(t, r, "cf.example.net")
	if !cf.CFHosted || cf.Blocked || !cf.RealityBlocked() || !strings.Contains(cf.CFEvidence, "104.16.1.1") {
		t.Fatalf("cf row %+v", cf)
	}
	if cfRow(t, r, "www.nvidia.com").CFHosted {
		t.Fatal("unknown verdict applied")
	}
	if ms := cfRow(t, r, "www.microsoft.com"); !ms.Blocked || ms.CFHosted {
		t.Fatalf("manual block touched: %+v", ms)
	}
	if !r.RealityDomainBlocked("cf.example.net") {
		t.Fatal("RealityDomainBlocked misses cf_hosted")
	}
	if ok, ev := r.RealityDomainCFHosted("CF.example.net"); !ok || ev == "" {
		t.Fatalf("RealityDomainCFHosted = %v %q", ok, ev)
	}
	targets, _ := r.RealityProbeTargets()
	if strings.Contains(strings.Join(targets, ","), "cf.example.net") {
		t.Fatalf("cf domain probed: %v", targets)
	}
	info, _ := r.realityDomainSets()
	if !info.Blocked["cf.example.net"] {
		t.Fatal("pick would use the cf domain")
	}
	if _, err := r.SetLandingRealityDomainBlocked(cf.ID, false); err != ErrRealityCFUnblock {
		t.Fatalf("unblock: %v", err)
	}
	// The admin may still block it (kept when it leaves Cloudflare).
	if _, err := r.SetLandingRealityDomainBlocked(cf.ID, true); err != nil {
		t.Fatal(err)
	}
	// Same verdict again: no change reported, no write needed.
	if ch, _ := r.ApplyRealityCFVerdicts(map[string]RealityCFVerdict{"cf.example.net": {Hosted: true, Known: true, Evidence: "104.16.1.1（面板解析）"}}); len(ch) != 0 {
		t.Fatalf("repeat changes %+v", ch)
	}
	// Leaves Cloudflare → lifted; the admin's own block stays.
	ch, _ = r.ApplyRealityCFVerdicts(map[string]RealityCFVerdict{"cf.example.net": {Known: true}})
	if len(ch) != 1 || ch[0].Hosted {
		t.Fatalf("lift changes %+v", ch)
	}
	if cf = cfRow(t, r, "cf.example.net"); cf.CFHosted || cf.CFEvidence != "" || !cf.Blocked {
		t.Fatalf("after lift %+v", cf)
	}
	if _, err := r.SetLandingRealityDomainBlocked(cf.ID, false); err != nil {
		t.Fatalf("unblock after lift: %v", err)
	}
	if r.RealityDomainBlocked("cf.example.net") {
		t.Fatal("still blocked")
	}
}

func TestRealityServerCFHostsEvidenceAge(t *testing.T) {
	now := time.Now()
	pins, _ := json.Marshal([]RealityPin{{Host: "Pinned.example", Port: 443, PinnedIP: "172.67.1.2"}, {Host: "ok.example", PinnedIP: "23.1.2.3"}})
	probe, _ := json.Marshal([]RealityProbeResult{
		{Target: "probed.example:443", BestIP: "23.4.4.4", IPs: []RealityProbeIP{{IP: "23.4.4.4"}, {IP: "2606:4700::1"}}},
		{Target: "clean.example:443", BestIP: "23.4.4.5", IPs: []RealityProbeIP{{IP: "23.4.4.5"}}},
	})
	s := &model.LandingServer{Name: "Lab", RealityPins: string(pins), RealityProbe: string(probe), RealityProbedAt: now.Add(-time.Hour).UnixMilli()}
	got := RealityServerCFHosts(s, now)
	if len(got) != 2 || !strings.Contains(got["pinned.example"], "172.67.1.2") || !strings.Contains(got["pinned.example"], "落地 Lab") || !strings.Contains(got["probed.example"], "2606:4700::1") {
		t.Fatalf("evidence %v", got)
	}
	// A 测速 table older than 48 h no longer counts; pins always do.
	s.RealityProbedAt = now.Add(-RealityCFEvidenceMaxAge - time.Minute).UnixMilli()
	if got := RealityServerCFHosts(s, now); len(got) != 1 || got["pinned.example"] == "" {
		t.Fatalf("stale table counted: %v", got)
	}
}

func TestRealityProbeRecordExcludesCF(t *testing.T) {
	r := cfTestRepo(t)
	now := time.Now().UnixMilli()
	s := model.LandingServer{ID: 1, Source: "native", NodeID: 9, Name: "Lab", PublicHost: "203.0.113.9", Status: "active", RealityMode: RealityModeAuto,
		RealityDest: "www.lovelive-anime.jp:443", RealityServerNames: `["www.lovelive-anime.jp"]`, CreatedTime: now, UpdatedTime: now}
	if err := r.DB().Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	row := func(target string, ms int, ip string) RealityProbeResult {
		return RealityProbeResult{Target: target, Feasible: true, LatencyMs: ms, BestIP: ip, IPs: []RealityProbeIP{{IP: ip, TCPMs: 1}}, TLS13: true, X25519: true, H2: true, CertValid: true}
	}
	// tesla is fastest but this machine resolves it to Cloudflare.
	rec, err := r.RecordLandingRealityProbe(1, []RealityProbeResult{row("www.tesla.com:443", 3, "104.18.7.7"), row("www.nvidia.com:443", 20, "23.9.9.9")}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Picked != "www.nvidia.com:443" || len(rec.CFHosts) != 1 || !strings.Contains(rec.CFHosts["www.tesla.com"], "104.18.7.7") {
		t.Fatalf("rec %+v", rec)
	}
	if tesla := cfRow(t, r, "www.tesla.com"); !tesla.CFHosted || tesla.Blocked {
		t.Fatalf("tesla row %+v", tesla)
	}
	// The stored table is evidence for the periodic check too.
	ev, err := r.RealityLandingCFEvidence(time.Now())
	if err != nil || !strings.Contains(ev["www.tesla.com"], "落地 Lab 测速解析") {
		t.Fatalf("landing evidence %v %v", ev, err)
	}
	// Margin re-apply on the stored table never moves to the Cloudflare row.
	e2 := r.DB().Model(&model.LandingRealityDomain{}).Where("domain = ?", "www.tesla.com").Update("cf_hosted", false).Error
	if e2 != nil {
		t.Fatal(e2)
	}
	if rec, err := r.ReapplyRealityDomainMargin(1, 0); err != nil || rec.Picked != "" {
		t.Fatalf("reapply moved: %+v %v", rec, err)
	}
}

func TestRealityListenersAndCheckHosts(t *testing.T) {
	r := cfTestRepo(t)
	now := time.Now().UnixMilli()
	for _, v := range []any{
		&model.LandingServer{ID: 1, Source: "native", NodeID: 9, Name: "A", PublicHost: "203.0.113.9", Status: "active", RealityDest: "def.example:443", CreatedTime: now, UpdatedTime: now},
		&model.LandingServer{ID: 2, Source: "shared", RemoteServerID: 5, Name: "B", PublicHost: "203.0.113.10", Status: "active", RealityDest: "www.lovelive-anime.jp:443", CreatedTime: now, UpdatedTime: now},
		&model.LandingNode{ID: 1, UserID: 2, LandingServerID: 1, Name: "r1", Protocol: "vless_reality_vision", Port: 30001, InboundTag: "flvxl-1", Status: "active", Dest: "Node.example:443"},
		&model.LandingNode{ID: 2, UserID: 2, LandingServerID: 1, Name: "r2", Protocol: "vless_reality_vision", Port: 30002, InboundTag: "flvxl-2", Status: "disabled", Dest: "off.example:443"},
		&model.LandingNode{ID: 3, UserID: 2, LandingServerID: 1, Name: "r3", Protocol: "vless_reality_vision", Port: 30003, InboundTag: "flvxl-3", Status: "active", Dest: "gone.example:443", DeletePending: true},
		&model.LandingNode{ID: 4, UserID: 2, LandingServerID: 1, Name: "ss", Protocol: "ss2022", Port: 30004, InboundTag: "flvxl-4", Status: "active", Dest: "ss.example:443"},
		&model.LandingNode{ID: 5, UserID: 2, LandingServerID: 2, Name: "r5", Protocol: "vless_reality_vision", Port: 30005, InboundTag: "flvxl-5", Status: "active", Dest: "node.example:443"},
		&model.ForwardChain{ID: 1, UserID: 2, State: model.ChainStateActive, Port: 30006, Dest: "chain.example:443", CreatedTime: now, UpdatedTime: now},
		&model.ForwardChainNode{ID: 1, ChainID: 1, LandingServerID: 1, NodeID: 9, Port: 30006, State: "active", CreatedTime: now},
		&model.ForwardChain{ID: 2, UserID: 2, State: model.ChainStateActive, Port: 30007, Dest: "anytls.example:443", InboundProtocol: model.ChainInboundAnyTLS, CreatedTime: now, UpdatedTime: now, IdemKey: "k2"},
		&model.ForwardChainNode{ID: 2, ChainID: 2, LandingServerID: 1, NodeID: 9, Port: 30007, State: "active", CreatedTime: now},
	} {
		if err := r.DB().Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	l, err := r.RealityListenersOnServer(1)
	if err != nil || l.Count != 2 || l.Dests["node.example"] != 1 || l.Dests["chain.example"] != 1 {
		t.Fatalf("server 1 listeners %+v %v", l, err)
	}
	if l, _ := r.RealityListenersOnServer(3); l.Count != 0 || l.Dests == nil {
		t.Fatalf("empty server %+v", l)
	}
	hosts, err := r.RealityCFCheckHosts()
	if err != nil {
		t.Fatal(err)
	}
	joined := "," + strings.Join(hosts, ",") + ","
	for _, want := range []string{"def.example", "node.example", "chain.example", "www.lovelive-anime.jp", "www.cloudflare.com"} {
		if !strings.Contains(joined, ","+want+",") {
			t.Fatalf("hosts miss %s: %v", want, hosts)
		}
	}
	for _, not := range []string{"off.example", "gone.example", "ss.example", "anytls.example"} {
		if strings.Contains(joined, ","+not+",") {
			t.Fatalf("hosts include %s", not)
		}
	}
}
