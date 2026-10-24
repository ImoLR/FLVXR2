package repo

import (
	"fmt"
	"strings"
	"testing"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
)

// Plan 149: one-time migration of the 大陆 seeds (global marker 3) and of the
// 「屏蔽中国大陆」 preset (preset marker 2), and the exempt-source inputs.

const cnSeedRule = `{"id":"%s","name":"%s","enabled":%t,"domains":%s,"ips":%s,"ports":"","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block","preset":"block-cn"}`

func fork89Forced(ipOn, siteOn bool, extra string) string {
	list := []string{
		fmt.Sprintf(cnSeedRule, "seed-cn-ip", "屏蔽大陆 IP", ipOn, "[]", `["geoip:cn"]`),
		fmt.Sprintf(cnSeedRule, "seed-cn-site", "屏蔽大陆网站", siteOn, `["geosite:cn"]`, "[]"),
	}
	if extra != "" {
		list = append(list, extra)
	}
	return "[" + strings.Join(list, ",") + "]"
}

const mineRule = `{"id":"mine","name":"我的端口","enabled":true,"domains":[],"ips":[],"ports":"8443","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block"}`

// setFork89Row: markers 2 / 1 (fork.83–89) and the legacy block-cn preset.
func setFork89Row(t *testing.T, r *Repository, forced string) {
	t.Helper()
	if err := r.db.Exec("UPDATE landing_egress_setting SET global_seed_version = 2, preset_seed_version = 1, global_forced = ? WHERE id = 1", forced).Error; err != nil {
		t.Fatal(err)
	}
	legacy := `[{"id":"","name":"屏蔽大陆 IP","enabled":true,"domains":[],"ips":["geoip:cn"],"ports":"","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block","preset":"block-cn"},` +
		`{"id":"","name":"屏蔽大陆网站","enabled":true,"domains":["geosite:cn"],"ips":[],"ports":"","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block","preset":"block-cn"},` +
		`{"id":"","name":"禁止大陆直连","enabled":true,"domains":[],"ips":[],"ports":"","sourceIps":["geoip:cn"],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block","preset":"block-cn"}]`
	if err := r.db.Exec("UPDATE landing_egress_preset SET rules = ?, description = ? WHERE seed_key = 'block-cn'", legacy, landingegress.LegacyBlockCNDescription).Error; err != nil {
		t.Fatal(err)
	}
}

func blockCNPreset(t *testing.T, r *Repository) model.LandingEgressPreset {
	t.Helper()
	var p model.LandingEgressPreset
	if err := r.db.Where("seed_key = ?", "block-cn").First(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLandingCNSeedMigration149(t *testing.T) {
	edited := fmt.Sprintf(cnSeedRule, "seed-cn-ip", "屏蔽大陆 IP", false, "[]", `["geoip:cn","1.2.3.0/24"]`)
	site := fmt.Sprintf(cnSeedRule, "seed-cn-site", "屏蔽大陆网站", false, `["geosite:cn"]`, "[]")
	cases := []struct {
		name, forced, ids, enabled string
		resync                     bool
	}{
		// Prod (fork.89, 10-24): both on, nothing else → direct appended.
		{"prod", fork89Forced(true, true, ""), "[seed-cn-ip seed-cn-site seed-cn-direct]", "111", true},
		{"both off + own rule", fork89Forced(false, false, mineRule), "[seed-cn-ip seed-cn-site seed-cn-direct mine]", "1111", true},
		{"ip edited", "[" + edited + "," + site + "]", "[seed-cn-ip seed-cn-site seed-cn-direct]", "011", true},
		{"site deleted", "[" + mineRule + "," + fmt.Sprintf(cnSeedRule, "seed-cn-ip", "屏蔽大陆 IP", true, "[]", `["geoip:cn"]`) + "]", "[mine seed-cn-ip seed-cn-direct]", "111", true},
		{"both deleted", "[" + mineRule + "]", "[mine]", "1", false},
	}
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, c := range cases {
			t.Run(dialect+"/"+c.name, func(t *testing.T) {
				r := orphanTestRepo(t, dialect)
				setFork89Row(t, r, c.forced)
				before := settingRow(t, r)
				resync, err := seedLandingEgressDefaults(r.db)
				if err != nil || resync != c.resync {
					t.Fatalf("resync %t %v", resync, err)
				}
				row := settingRow(t, r)
				if row.GlobalSeedVersion != 3 || row.PresetSeedVersion != 2 {
					t.Fatalf("markers %d %d", row.GlobalSeedVersion, row.PresetSeedVersion)
				}
				if got := forcedIDs(t, row.GlobalForced); got != c.ids {
					t.Fatalf("forced %s", got)
				}
				g, _, err := r.GetLandingGlobal()
				if err != nil {
					t.Fatal(err)
				}
				on := ""
				for _, rule := range g.Forced {
					on += map[bool]string{true: "1", false: "0"}[rule.Enabled]
					if rule.ID == landingegress.SeedCNDirect && (strings.Join(rule.InboundProtocols, ",") != "ss2022" || strings.Join(rule.SourceIPs, ",") != "geoip:cn") {
						t.Fatalf("direct %+v", rule)
					}
				}
				if on != c.enabled {
					t.Fatalf("enabled %s", on)
				}
				// Everything else untouched.
				if row.GlobalDefault != before.GlobalDefault || row.GlobalAction != before.GlobalAction || row.SourceWhitelist != before.SourceWhitelist ||
					rowSwitches(row) != rowSwitches(before) || row.GlobalUpdatedTime != before.GlobalUpdatedTime {
					t.Fatalf("other columns changed: %+v", row)
				}
				// Preset: legacy rules → scoped; description replaced.
				p := PresetFromModel(blockCNPreset(t, r))
				if len(p.Rules) != 3 || strings.Join(p.Rules[2].InboundProtocols, ",") != "ss2022" || p.Description != landingegress.BlockCNDescription {
					t.Fatalf("preset %+v", p)
				}
				// Idempotent, and a deleted 禁止大陆直连 never comes back.
				g.Forced = g.Forced[:0]
				if _, err := r.SaveLandingGlobal(&g); err != nil {
					t.Fatal(err)
				}
				mid := settingRow(t, r)
				for i := 0; i < 2; i++ {
					if resync, err := seedLandingEgressDefaults(r.db); err != nil || resync {
						t.Fatalf("second run %t %v", resync, err)
					}
				}
				if again := settingRow(t, r); rowString(again) != rowString(mid) {
					t.Fatalf("migration re-ran:\n%+v\n%+v", mid, again)
				}
			})
		}
	}
}

func rowSwitches(row model.LandingEgressSetting) string {
	v := func(p *bool) string {
		if p == nil {
			return "nil"
		}
		return fmt.Sprint(*p)
	}
	return v(row.BuiltinSMTP) + v(row.BuiltinBT) + v(row.BuiltinMailPorts) + v(row.BuiltinTelnetSMB)
}

// An edited 「屏蔽中国大陆」 preset is kept as the admin left it.
func TestLandingBlockCNPresetEdited(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			setFork89Row(t, r, fork89Forced(true, true, ""))
			custom := `[{"id":"","name":"只屏蔽大陆 IP","enabled":true,"domains":[],"ips":["geoip:cn"],"ports":"","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block","preset":"block-cn"}]`
			if err := r.db.Exec("UPDATE landing_egress_preset SET rules = ?, description = ? WHERE seed_key = 'block-cn'", custom, "我的说明").Error; err != nil {
				t.Fatal(err)
			}
			before := blockCNPreset(t, r)
			if _, err := seedLandingEgressDefaults(r.db); err != nil {
				t.Fatal(err)
			}
			after := blockCNPreset(t, r)
			if after.Rules != before.Rules || after.Description != before.Description || after.UpdatedTime != before.UpdatedTime {
				t.Fatalf("edited preset changed: %+v", after)
			}
			// Same rules, own description: rules updated, description kept.
			setFork89Row(t, r, fork89Forced(true, true, ""))
			if err := r.db.Exec("UPDATE landing_egress_preset SET description = ? WHERE seed_key = 'block-cn'", "我的说明").Error; err != nil {
				t.Fatal(err)
			}
			if _, err := seedLandingEgressDefaults(r.db); err != nil {
				t.Fatal(err)
			}
			p := PresetFromModel(blockCNPreset(t, r))
			if p.Description != "我的说明" || strings.Join(p.Rules[2].InboundProtocols, ",") != "ss2022" {
				t.Fatalf("description / rules: %+v", p)
			}
		})
	}
}

// Exempt-source inputs: agent-reported egress (written only when changed,
// DDNS nodes too) and DDNS records' current addresses.
func TestLandingExemptHosts149(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			orphanSeed(t, r,
				&model.Node{ID: 720, Name: "nat", ServerIP: "10.7.177.91", ServerIPV4: ns("10.7.177.91"), ServerIPV6: ns("2408:8748:a100:59::1"), Port: "10000-20000", Status: 1},
				&model.Node{ID: 748, Name: "ddns", ServerIP: "leikwan-hutai-ct.imgamer.top", ServerIPV6: ns("leikwan-hutai-ct.imgamer.top"), DDNSEnabled: true, Port: "10000-20000", Status: 1},
			)
			if err := r.db.Create(&model.NodeDDNSRecord{NodeID: 748, Domain: "leikwan-hutai-ct.imgamer.top", RecordType: "AAAA", Role: "main", CurrentIP: "240e:b8f:72d:5c04:ca:8aff:fec0:2813"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.db.Create(&model.NodeDDNSRecord{NodeID: 748, Domain: "leikwan-hutai-cu.imgamer.top", RecordType: "AAAA", Role: "extra"}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := r.FillNodePublicIPs(720, "112.64.1.9", "2408:8748:a100:59:aaaa::7"); err != nil {
				t.Fatal(err)
			}
			if _, err := r.FillNodePublicIPs(748, "101.80.2.3", "bogus"); err != nil {
				t.Fatal(err)
			}
			var n model.Node
			if err := r.db.First(&n, 720).Error; err != nil || n.ReportedIPV4 != "112.64.1.9" || n.ReportedIPV6 != "2408:8748:a100:59:aaaa::7" ||
				n.ServerIP != "10.7.177.91" || n.ServerIPV4.String != "10.7.177.91" {
				t.Fatalf("nat node %+v %v", n, err)
			}
			var d model.Node
			if err := r.db.First(&d, 748).Error; err != nil || d.ReportedIPV4 != "101.80.2.3" || d.ReportedIPV6 != "" || d.ServerIP != "leikwan-hutai-ct.imgamer.top" {
				t.Fatalf("ddns node %+v %v", d, err)
			}
			// Invalid / private reports never clear a stored value.
			if _, err := r.FillNodePublicIPs(720, "10.0.0.1", ""); err != nil {
				t.Fatal(err)
			}
			if err := r.db.First(&n, 720).Error; err != nil || n.ReportedIPV4 != "112.64.1.9" {
				t.Fatalf("private report overwrote %+v", n)
			}
			hosts, err := r.LandingExemptHosts()
			if err != nil {
				t.Fatal(err)
			}
			joined := "," + strings.Join(hosts, ",") + ","
			for _, want := range []string{"112.64.1.9", "2408:8748:a100:59:aaaa::7", "101.80.2.3", "240e:b8f:72d:5c04:ca:8aff:fec0:2813", "leikwan-hutai-ct.imgamer.top", "10.7.177.91"} {
				if !strings.Contains(joined, ","+want+",") {
					t.Fatalf("%s missing from %v", want, hosts)
				}
			}
			exempt := strings.Join(landingegress.ExemptSources(hosts, nil, nil), ",")
			for _, want := range []string{"112.64.1.9/32", "2408:8748:a100:59::/64", "240e:b8f:72d:5c04::/64", "101.80.2.3/32"} {
				if !strings.Contains(exempt, want) {
					t.Fatalf("%s missing from exempt %s", want, exempt)
				}
			}
		})
	}
}
