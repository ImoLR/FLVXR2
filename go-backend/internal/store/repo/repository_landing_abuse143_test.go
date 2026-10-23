package repo

import (
	"encoding/json"
	"fmt"
	"testing"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
)

// Plan 143: one-time move of the 防滥用 seeds from 全局·强制 into 内置.

const plan140Forced = `[` +
	`{"id":"seed-mail-ports","name":"屏蔽发信端口","enabled":%t,"domains":[],"ips":[],"ports":"465,587,2525","sourceIps":[],"sourcePorts":"","network":"tcp","protocols":[],"landingNodeIds":[],"action":"block","preset":"anti-abuse"},` +
	`{"id":"seed-telnet-smb","name":"屏蔽 Telnet/SMB","enabled":%t,"domains":[],"ips":[],"ports":"23,135-139,445","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block","preset":"anti-abuse"},` +
	`{"id":"seed-cn-ip","name":"屏蔽大陆 IP","enabled":true,"domains":[],"ips":["geoip:cn"],"ports":"","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block","preset":"block-cn"},` +
	`{"id":"seed-cn-site","name":"屏蔽大陆网站","enabled":true,"domains":["geosite:cn"],"ips":[],"ports":"","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block","preset":"block-cn"}]`

const globalDefaultJSON = `[{"id":"d1","name":"AI","enabled":true,"domains":["geosite:openai"],"ips":[],"ports":"","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block"}]`

// setPlan140Row puts row 1 in the fork.78–82 state: marker 1, the given
// 全局·强制 list, NULL switches, a non-empty 全局·默认.
func setPlan140Row(t *testing.T, r *Repository, forced string) {
	t.Helper()
	if err := r.db.Exec("UPDATE landing_egress_setting SET global_seed_version = 1, global_forced = ?, global_default = ?, builtin_smtp = NULL, builtin_bt = NULL, builtin_mail_ports = NULL, builtin_telnet_smb = NULL WHERE id = 1",
		forced, globalDefaultJSON).Error; err != nil {
		t.Fatal(err)
	}
}

func settingRow(t *testing.T, r *Repository) model.LandingEgressSetting {
	t.Helper()
	var row model.LandingEgressSetting
	if err := r.db.First(&row, 1).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

// rowString prints the row with the switch values (not pointers).
func rowString(row model.LandingEgressSetting) string {
	b, _ := json.Marshal(row)
	v := func(p *bool) string {
		if p == nil {
			return "nil"
		}
		return fmt.Sprint(*p)
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%d|%d|%s|%s|%s", b, v(row.BuiltinSMTP), v(row.BuiltinBT), v(row.BuiltinMailPorts), v(row.BuiltinTelnetSMB),
		row.GlobalSeedVersion, row.PresetSeedVersion, row.GlobalForced, row.GlobalDefault, row.GlobalAction)
}

func forcedIDs(t *testing.T, raw string) string {
	t.Helper()
	var rules []landingegress.Rule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, x := range rules {
		ids = append(ids, x.ID)
	}
	return fmt.Sprint(ids)
}

func TestLandingAbuseSeedMove(t *testing.T) {
	renamed := `[{"id":"k3j","name":"我的发信","enabled":true,"domains":[],"ips":[],"ports":"465,587,2525","sourceIps":[],"sourcePorts":"","network":"tcp","protocols":[],"landingNodeIds":[],"action":"block"},` +
		`{"id":"seed-telnet-smb","name":"屏蔽 Telnet/SMB","enabled":true,"domains":[],"ips":[],"ports":"23,445","sourceIps":[],"sourcePorts":"","network":"","protocols":[],"landingNodeIds":[],"action":"block","preset":"anti-abuse"}]`
	cases := []struct {
		name         string
		forced       string
		mail, telnet bool
		rest         string
		resync       bool
	}{
		{"prod: both enabled", fmt.Sprintf(plan140Forced, true, true), true, true, "[seed-cn-ip seed-cn-site]", true},
		{"mail only", fmt.Sprintf(plan140Forced, true, false), true, false, "[seed-cn-ip seed-cn-site]", true},
		{"both disabled", fmt.Sprintf(plan140Forced, false, false), false, false, "[seed-cn-ip seed-cn-site]", false},
		{"deleted", `[]`, false, false, "[]", false},
		{"renamed + edited", renamed, true, false, "[seed-telnet-smb]", true},
	}
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, c := range cases {
			t.Run(dialect+"/"+c.name, func(t *testing.T) {
				r := orphanTestRepo(t, dialect)
				setPlan140Row(t, r, c.forced)
				before := settingRow(t, r)
				resync, err := seedLandingEgressDefaults(r.db)
				if err != nil || resync != c.resync {
					t.Fatalf("resync %t %v", resync, err)
				}
				row := settingRow(t, r)
				if row.GlobalSeedVersion != 2 || row.BuiltinMailPorts == nil || *row.BuiltinMailPorts != c.mail || row.BuiltinTelnetSMB == nil || *row.BuiltinTelnetSMB != c.telnet {
					t.Fatalf("row %+v", row)
				}
				if got := forcedIDs(t, row.GlobalForced); got != c.rest {
					t.Fatalf("forced %s", got)
				}
				// 全局·默认, 默认出站, SMTP/BT, whitelist untouched.
				if row.GlobalDefault != before.GlobalDefault || row.GlobalAction != before.GlobalAction || row.BuiltinSMTP != nil || row.BuiltinBT != nil ||
					row.SourceWhitelist != before.SourceWhitelist || row.GlobalUpdatedTime != before.GlobalUpdatedTime {
					t.Fatalf("other columns changed: %+v", row)
				}
				sw, _ := r.GetLandingAbuseSwitches()
				if sw.On(landingegress.AbuseSMTP) != true || sw.On(landingegress.AbuseBT) != true || sw.MailOn != c.mail || sw.TelnetSMBOn != c.telnet {
					t.Fatalf("switches %+v", sw)
				}
				// The 大陆 rules are byte-identical to before.
				var b, a []json.RawMessage
				_ = json.Unmarshal([]byte(before.GlobalForced), &b)
				_ = json.Unmarshal([]byte(row.GlobalForced), &a)
				for _, x := range a {
					found := false
					for _, y := range b {
						found = found || string(x) == string(y)
					}
					if !found {
						t.Fatalf("remaining rule changed: %s", x)
					}
				}
				// Idempotent: a restart changes nothing, even after the admin
				// re-adds the 防滥用 preset into 强制 or flips the switches.
				if err := r.db.Exec("UPDATE landing_egress_setting SET global_forced = ? WHERE id = 1", fmt.Sprintf(plan140Forced, true, true)).Error; err != nil {
					t.Fatal(err)
				}
				if err := r.SaveLandingAbuseSwitches(landingegress.AbuseSwitches{MailOn: !c.mail}); err != nil {
					t.Fatal(err)
				}
				mid := settingRow(t, r)
				for i := 0; i < 2; i++ {
					if resync, err := seedLandingEgressDefaults(r.db); err != nil || resync {
						t.Fatalf("second run resync %t %v", resync, err)
					}
				}
				if again := settingRow(t, r); rowString(again) != rowString(mid) {
					t.Fatalf("migration re-ran:\n%+v\n%+v", mid, again)
				}
			})
		}
	}
}

// Open() reports the resync once (the handler pushes every server).
func TestLandingAbuseMoveResyncFlag(t *testing.T) {
	path := t.TempDir() + "/m.db"
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.TakeLandingStartupResync() {
		t.Fatal("fresh install asked for a resync")
	}
	setPlan140Row(t, r, fmt.Sprintf(plan140Forced, true, false))
	_ = r.Close()
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !r.TakeLandingStartupResync() || r.TakeLandingStartupResync() {
		t.Fatal("resync flag must be reported exactly once")
	}
	_ = r.Close()
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.TakeLandingStartupResync() {
		t.Fatal("second start asked for a resync")
	}
	sw, _ := r.GetLandingAbuseSwitches()
	if !sw.MailOn || sw.TelnetSMBOn {
		t.Fatalf("switches %+v", sw)
	}
}
