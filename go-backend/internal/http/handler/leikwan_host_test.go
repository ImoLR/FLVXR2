package handler

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 269: the LeiKwanHostSync payload — every 利群 rule port is counted,
// blocked rules' ports are in Block, the desired content goes only to a
// managed host, and the revision moves only when the content changes.
func TestLeiKwanHostSyncPayload(t *testing.T) {
	r, err := repo.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{repo: r}
	db := r.DB()
	now := time.Now().UnixMilli()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.Create(&model.Node{ID: 2, Name: "ixp1", Secret: "s", ServerIP: "163.53.52.189", Port: "1-65535", CreatedTime: now}).Error)
	must(db.Create(&model.Forward{ID: 148, UserID: 1, Name: "備線·263", TunnelID: 90, RemoteAddr: "x:1", CreatedTime: now, UpdatedTime: now, Status: 1}).Error)
	must(db.Create(&model.ForwardPort{ForwardID: 148, NodeID: 2, Port: 35046}).Error)
	must(db.Create(&model.LeiKwanLine{ID: 1, AccountID: 1, RouteID: 21, Name: "利群·广港", Accel: `{"warm":{"main":10,"backup":4},"ssFilter":"enforce","logswitch":true,"returnCheck":true}`, Status: 1}).Error)
	must(db.Create(&model.LeiKwanRule{UserID: 1, LineID: 1, ForwardID: 263, Name: "263", ListenPort: 10018, BackupForwardID: 148, BackupMode: "linked", Status: 1}).Error)
	must(db.Create(&model.LeiKwanRule{UserID: 2, LineID: 1, ForwardID: 540, Name: "540", ListenPort: 10021, BlockReason: "总流量已用完", Status: 1}).Error)
	must(db.Create(&model.LeiKwanSetting{ID: 1, AccountID: 1, Global: `{"coalesce":"on budget_ms=2 min=59"}`}).Error)
	host, err := r.EnsureLeiKwanHost(58)
	must(err)

	g, err := h.newLKGen()
	must(err)
	s, d, err := h.leiKwanHostSyncPayload(host, g)
	must(err)
	if d != nil || s.Desired != nil || s.Manage || s.Rev != 1 {
		t.Fatalf("unlearned host: %+v", s)
	}
	if len(s.Ports) != 2 || s.Ports[0] != 10018 || s.Ports[1] != 10021 || len(s.Block) != 1 || s.Block[0] != 10021 {
		t.Fatalf("ports %v block %v", s.Ports, s.Block)
	}
	// Learned + 接管: the desired content goes out; same content = same rev.
	host.Profile = `{"files":{"warm":true,"ssfilter":true,"triggers":true,"logswitch":true,"coalesce":true},"warmExtra":{"pace_ms":"100"},"trigGlobal":{"return_check":"off"}}`
	host.Manage = 1
	must(r.SaveLeiKwanHostConfig(host))
	s, d, err = h.leiKwanHostSyncPayload(host, g)
	must(err)
	if !s.Manage || s.Desired == nil || s.Rev != 2 {
		t.Fatalf("managed host: %+v", s)
	}
	if d.Warm.Tokens["263"] != "10" || d.Warm.Tokens["backup:263"] != "4" || d.Warm.Tokens["pace_ms"] != "100" {
		t.Fatalf("warm %v", d.Warm.Tokens)
	}
	if _, ok := d.Warm.Tokens["540"]; !ok {
		t.Fatalf("540 has no backup → no logswitch but still a main pool: %v", d.Warm.Tokens)
	}
	if _, ok := d.Warm.Tokens["backup:540"]; ok {
		t.Fatalf("540 without 备线 got a backup pool: %v", d.Warm.Tokens)
	}
	if len(d.Logswitch.Lines) != 1 || d.Logswitch.Lines[0] != "263:10018>163.53.52.189:35046" {
		t.Fatalf("lines %v", d.Logswitch.Lines)
	}
	if d.Triggers.Tokens["263:return_check"] != "on" || d.Coalesce.Value != "on budget_ms=2 min=59" {
		t.Fatalf("triggers %v coalesce %v", d.Triggers.Tokens, d.Coalesce)
	}
	s2, _, _ := h.leiKwanHostSyncPayload(host, g)
	if s2.Rev != 2 {
		t.Fatalf("rev moved without a change: %d", s2.Rev)
	}
	// A line change → new content → rev 3.
	must(db.Model(&model.LeiKwanLine{}).Where("id = 1").Update("accel", `{"warm":{"main":8,"backup":4},"ssFilter":"enforce","logswitch":true,"returnCheck":true}`).Error)
	g, _ = h.newLKGen()
	s3, d3, _ := h.leiKwanHostSyncPayload(host, g)
	if s3.Rev != 3 || d3.Warm.Tokens["263"] != "8" {
		t.Fatalf("after line change rev %d warm %v", s3.Rev, d3.Warm.Tokens)
	}
	// A VLESS landing turns the SS2022 filter off for that rule.
	must(db.Create(&model.LandingServer{ID: 11, Source: "shared", Name: "99u", PublicHost: "h", Status: "active"}).Error)
	must(db.Create(&model.LandingNode{ID: 7, UserID: 2, LandingServerID: 11, Name: "v", Protocol: "vless_reality_vision", Port: 30001, InboundTag: "v7", Status: "active"}).Error)
	must(db.Model(&model.LeiKwanRule{}).Where("forward_id = 540").Update("landing_node_id", 7).Error)
	g, _ = h.newLKGen()
	_, d4, _ := h.leiKwanHostSyncPayload(host, g)
	if d4.SSFilter.Tokens["540"] != "" || d4.SSFilter.Tokens["263"] != "enforce" {
		t.Fatalf("ssfilter %v", d4.SSFilter.Tokens)
	}
	// An SS2022 panel landing sets the rule's key (server key + user key).
	must(db.Create(&model.LandingNode{ID: 8, UserID: 1, LandingServerID: 11, Name: "s", Protocol: "ss2022", Port: 30002, InboundTag: "s8", Method: "2022-blake3-aes-128-gcm", ServerKey: "c2Vy", UserKey: "dXNy", Status: "active"}).Error)
	must(db.Model(&model.LeiKwanRule{}).Where("forward_id = 263").Update("landing_node_id", 8).Error)
	host.Profile = `{"files":{"ssfilter":true,"sskeys":true}}`
	g, _ = h.newLKGen()
	_, d5, _ := h.leiKwanHostSyncPayload(host, g)
	if k := d5.SSKeys.Set["263"]; k.PSK != "c2Vy" || len(k.Users) != 1 || k.Users[0] != "dXNy" || len(d5.SSKeys.Set) != 1 {
		t.Fatalf("keys %+v", d5.SSKeys)
	}
}

func TestLeiKwanCounterDeltas(t *testing.T) {
	prev := map[string][2]int64{"10018": {100, 200}}
	if d := lkCounterDeltas("e1", prev, "e1", map[string][2]int64{"10018": {150, 260}, "10021": {5, 0}}); d["10018"] != [2]int64{50, 60} || d["10021"] != [2]int64{5, 0} {
		t.Fatalf("same epoch: %v", d)
	}
	if d := lkCounterDeltas("e1", prev, "e2", map[string][2]int64{"10018": {10, 20}}); d["10018"] != [2]int64{10, 20} {
		t.Fatalf("new epoch: %v", d)
	}
	if d := lkCounterDeltas("e1", prev, "e1", map[string][2]int64{"10018": {100, 200}}); len(d) != 0 {
		t.Fatalf("no growth: %v", d)
	}
	if d := lkCounterDeltas("e1", prev, "e1", map[string][2]int64{"10018": {40, 300}}); d["10018"] != [2]int64{40, 100} {
		t.Fatalf("counter went down (reset): %v", d)
	}
}

func TestLeiKwanBlockReason(t *testing.T) {
	now := time.Now().UnixMilli()
	ok := model.User{Status: 1, ExpTime: now + 1000, Flow: 10, InFlow: 1}
	if r := leiKwanBlockReason(ok, true, nil, nil, now); r != "" {
		t.Fatalf("ok user blocked: %q", r)
	}
	if r := leiKwanBlockReason(model.User{Status: 0, Flow: 10}, true, nil, nil, now); r != "账号已禁用" {
		t.Fatal(r)
	}
	if r := leiKwanBlockReason(model.User{Status: 1, ExpTime: now - 1, Flow: 10}, true, nil, nil, now); r != "账号已到期" {
		t.Fatal(r)
	}
	if r := leiKwanBlockReason(model.User{Status: 1, Flow: 1, InFlow: 2 << 30}, true, nil, nil, now); r != "总流量已用完" {
		t.Fatal(r)
	}
	if r := leiKwanBlockReason(ok, true, []int64{58}, map[int64]string{58: "百度云广州"}, now); r != "单服务器流量已用完（百度云广州）" {
		t.Fatal(r)
	}
}

// LK_SYNC_DUMP=path writes a managed host's sync payload (the agent's tests
// parse it: cross-module JSON contract).
func TestLeiKwanHostSyncDump(t *testing.T) {
	path := os.Getenv("LK_SYNC_DUMP")
	if path == "" {
		t.Skip("LK_SYNC_DUMP not set")
	}
	s := lkSync{V: 1, Manage: true, Rev: 7, Force: 2, Traffic: true, Ports: []int{10018, 10021}, Block: []int{10021},
		Desired: &lkDesired{
			Warm:      &lkWantTokens{Tokens: map[string]string{"263": "10", "backup:263": "4", "pace_ms": "100"}},
			SSFilter:  &lkWantTokens{Tokens: map[string]string{"263": "enforce"}},
			SSKeys:    &lkWantKeys{Set: map[string]lkKeyEntry{"263": {Method: "2022-blake3-aes-128-gcm", PSK: "c2VydmVyMTIzNDU2Nzg5MA==", Users: []string{"dXNlcjEyMzQ1Njc4OTAxMg=="}}}},
			Triggers:  &lkWantTokens{Tokens: map[string]string{"timeout": "switch", "263:return_check": "on"}},
			Logswitch: &lkWantLogswitch{Lines: []string{"263:10018>163.53.52.189:35046"}, RecordOnly: []string{}},
			Coalesce:  &lkWantValue{Value: "off"},
			Phone:     &lkWantPhone{Rules: map[string]string{"19263": "163.53.52.189:35046"}, Comments: map[string]string{"19263": "263 广港"}},
		}}
	raw, _ := json.MarshalIndent(s, "", " ")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if fp := lkKeyFingerprint(s.Desired.SSKeys.Set["263"]); fp == "" {
		t.Fatal("no fingerprint")
	}
	_ = os.WriteFile(path+".fp", []byte(lkKeyFingerprint(s.Desired.SSKeys.Set["263"])), 0o644)
}
