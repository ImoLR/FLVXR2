package repo

import (
	"testing"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
)

// Plan 140: seeds run once (markers), deleted seeds never come back, preset
// CRUD / reorder / restore, global switches.
func TestLandingSeedsAndPresets(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			for _, col := range []string{"builtin_smtp", "builtin_bt", "builtin_mail_ports", "builtin_telnet_smb", "global_seed_version", "preset_seed_version"} {
				if !r.db.Migrator().HasColumn(&model.LandingEgressSetting{}, col) {
					t.Fatalf("landing_egress_setting.%s missing", col)
				}
			}
			if !r.db.Migrator().HasTable(&model.LandingEgressPreset{}) {
				t.Fatal("landing_egress_preset missing")
			}
			g, _, err := r.GetLandingGlobal()
			if err != nil {
				t.Fatal(err)
			}
			// Plan 143: fresh installs seed only the 大陆 rules into 强制.
			// Plan 149: three of them, enabled (禁止大陆直连 on SS nodes).
			if len(g.Forced) != 3 || len(g.Default) != 0 || !g.Active() {
				t.Fatalf("seeded global: %+v", g)
			}
			for _, rule := range g.Forced {
				if !rule.Enabled {
					t.Fatalf("seed disabled: %+v", rule)
				}
			}
			if g.Forced[2].ID != landingegress.SeedCNDirect || len(g.Forced[2].InboundProtocols) != 1 || g.Forced[2].InboundProtocols[0] != "ss2022" {
				t.Fatalf("direct seed: %+v", g.Forced[2])
			}
			presets, err := r.ListLandingEgressPresets()
			if err != nil || len(presets) != 10 {
				t.Fatalf("presets %d %v", len(presets), err)
			}
			if presets[0].SeedKey != "block-cn" || !presets[0].InsertTop || len(presets[0].Rules) != 3 || presets[7].SeedKey != "block-private" ||
				len(presets[0].Rules[2].InboundProtocols) != 1 || presets[0].Description != landingegress.BlockCNDescription {
				t.Fatalf("seed order: %+v", presets[:2])
			}
			if presets[1].Rules[0].Action != landingegress.ActionAsk || !presets[1].AskOutbound {
				t.Fatalf("ask preset: %+v", presets[1])
			}
			sw, err := r.GetLandingAbuseSwitches()
			if err != nil || sw != (landingegress.AbuseSwitches{}) {
				t.Fatalf("switches default on: %+v %v", sw, err)
			}

			// The admin deletes the seeds; a restart never brings them back.
			g.Forced = []landingegress.Rule{}
			if _, err := r.SaveLandingGlobal(&g); err != nil {
				t.Fatal(err)
			}
			if err := r.DeleteLandingEgressPreset(presets[6].ID); err != nil { // 屏蔽 BT
				t.Fatal(err)
			}
			if resync, err := seedLandingEgressDefaults(r.db); err != nil || resync {
				t.Fatal(resync, err)
			}
			if g2, _, _ := r.GetLandingGlobal(); len(g2.Forced) != 0 {
				t.Fatalf("seeds came back: %+v", g2.Forced)
			}
			if p2, _ := r.ListLandingEgressPresets(); len(p2) != 9 {
				t.Fatalf("deleted preset came back: %d", len(p2))
			}

			// Switches: only their columns change.
			if err := r.SaveLandingAbuseSwitches(landingegress.AbuseSwitches{SMTPOff: true, TelnetSMBOn: true}); err != nil {
				t.Fatal(err)
			}
			if sw, _ := r.GetLandingAbuseSwitches(); !sw.SMTPOff || sw.BTOff || sw.MailOn || !sw.TelnetSMBOn {
				t.Fatalf("switches %+v", sw)
			}
			if g3, _, _ := r.GetLandingGlobal(); len(g3.Forced) != 0 {
				t.Fatal("switch save touched the global group")
			}
			d, err := r.LandingEgressData(func() int64 {
				orphanSeed(t, r, &model.Node{ID: 611, Name: "n", ServerIP: "203.0.113.11", Port: "10000-19999", LandingCapable: true, Status: 1})
				s, err := r.EnableLandingServer(611)
				if err != nil {
					t.Fatal(err)
				}
				return s.ID
			}())
			if err != nil || !d.Abuse.SMTPOff || d.Abuse.BTOff {
				t.Fatalf("compile data switches: %+v %v", d, err)
			}

			// CRUD: create (appended), update, reorder, restore.
			created, err := r.SaveLandingEgressPreset(&landingegress.Preset{Name: "我的预设", Description: "d", AskOutbound: true, Rules: []landingegress.Rule{{ID: "r-1", Name: "x", Enabled: true, Domains: []string{"geosite:openai"}, Action: landingegress.ActionAsk, LandingNodeIDs: []int64{5}}}})
			if err != nil {
				t.Fatal(err)
			}
			if created.ID == 0 || created.SeedKey != "" || created.Rules[0].ID != "" || len(created.Rules[0].LandingNodeIDs) != 0 {
				t.Fatalf("created %+v", created)
			}
			list, _ := r.ListLandingEgressPresets()
			if list[len(list)-1].ID != created.ID {
				t.Fatal("new preset not appended")
			}
			created.Name, created.AskOutbound = "改名", false
			created.Rules[0].Action = landingegress.ActionBlock
			updated, err := r.SaveLandingEgressPreset(created)
			if err != nil || updated.Name != "改名" || updated.AskOutbound || updated.SortOrder != list[len(list)-1].SortOrder {
				t.Fatalf("update %+v %v", updated, err)
			}
			if _, err := r.SaveLandingEgressPreset(&landingegress.Preset{ID: 999999, Name: "x", Rules: []landingegress.Rule{{Name: "a", Enabled: true, Ports: "1", Action: "block"}}}); err != ErrPresetNotFound {
				t.Fatalf("update unknown: %v", err)
			}
			ids := []int64{created.ID}
			for _, p := range list[:len(list)-1] {
				ids = append(ids, p.ID)
			}
			if err := r.ReorderLandingEgressPresets(ids[1:]); err == nil {
				t.Fatal("partial reorder accepted")
			}
			if err := r.ReorderLandingEgressPresets(append(ids, ids[0])); err == nil {
				t.Fatal("duplicate reorder accepted")
			}
			if err := r.ReorderLandingEgressPresets(ids); err != nil {
				t.Fatal(err)
			}
			list, _ = r.ListLandingEgressPresets()
			if list[0].ID != created.ID {
				t.Fatalf("reorder: first %d", list[0].ID)
			}
			added, err := r.RestoreLandingEgressPresets()
			if err != nil || added != 1 {
				t.Fatalf("restore added %d %v", added, err)
			}
			if again, _ := r.RestoreLandingEgressPresets(); again != 0 {
				t.Fatalf("restore twice added %d", again)
			}
			list, _ = r.ListLandingEgressPresets()
			if len(list) != 11 || list[len(list)-1].SeedKey != "block-bt" {
				t.Fatalf("restored list: %d last %q", len(list), list[len(list)-1].SeedKey)
			}
			if err := r.DeleteLandingEgressPreset(created.ID); err != nil {
				t.Fatal(err)
			}
			if err := r.DeleteLandingEgressPreset(created.ID); err != ErrPresetNotFound {
				t.Fatalf("delete twice: %v", err)
			}
		})
	}
}

// An upgraded database: the global group already has rules (one equal to a
// seed) and no markers → seeds are appended after them, without duplicates,
// and a missing settings row is created with the markers set.
func TestLandingSeedsOnUpgrade(t *testing.T) {
	r := orphanTestRepo(t, "sqlite")
	if err := r.db.Exec("UPDATE landing_egress_setting SET global_seed_version = 0, preset_seed_version = 0, global_forced = ? WHERE id = 1",
		`[{"id":"mine","name":"我的强制","enabled":true,"domains":[],"ips":["geoip:cn"],"ports":"","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block"}]`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := seedLandingEgressDefaults(r.db); err != nil {
		t.Fatal(err)
	}
	g, _, _ := r.GetLandingGlobal()
	var names []string
	for _, rule := range g.Forced {
		names = append(names, rule.Name)
	}
	// 「mine」 equals the 大陆 IP seed, so only 大陆网站 and 禁止大陆直连 are
	// appended (plan 149: enabled).
	if len(g.Forced) != 3 || g.Forced[0].ID != "mine" || !g.Forced[0].Enabled || !g.Forced[1].Enabled || g.Forced[2].ID != landingegress.SeedCNDirect {
		t.Fatalf("upgrade seeds: %v", names)
	}
	if p, _ := r.ListLandingEgressPresets(); len(p) != 10 {
		t.Fatalf("presets duplicated on re-seed: %d", len(p))
	}
	if err := r.db.Exec("DELETE FROM landing_egress_setting").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := seedLandingEgressDefaults(r.db); err != nil {
		t.Fatal(err)
	}
	var row model.LandingEgressSetting
	if err := r.db.First(&row, 1).Error; err != nil || row.GlobalSeedVersion != 3 || row.PresetSeedVersion != 2 || row.BuiltinSMTP != nil ||
		row.BuiltinMailPorts == nil || *row.BuiltinMailPorts || row.BuiltinTelnetSMB == nil || *row.BuiltinTelnetSMB {
		t.Fatalf("recreated row %+v %v", row, err)
	}
}
