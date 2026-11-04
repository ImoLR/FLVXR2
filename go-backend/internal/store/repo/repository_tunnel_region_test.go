package repo

import (
	"reflect"
	"strings"
	"testing"
)

func TestDeriveTunnelRegions(t *testing.T) {
	rows := []tunnelRegionRow{
		{TunnelID: 1, TunnelType: 1, ChainType: "1", NodeID: 10, Region: "CN"},
		{TunnelID: 2, TunnelType: 2, ChainType: "1", NodeID: 10, Region: "CN"},
		{TunnelID: 2, TunnelType: 2, ChainType: "3", NodeID: 11, Region: "JP"},
		{TunnelID: 3, TunnelType: 2, ChainType: "1", NodeID: 10, Region: "CN"},
		{TunnelID: 3, TunnelType: 2, ChainType: "1", NodeID: 12, Region: "HK"},
		{TunnelID: 3, TunnelType: 2, ChainType: "1", NodeID: 12, Region: "HK"},
		{TunnelID: 3, TunnelType: 2, ChainType: "3", NodeID: 13, Region: "US"},
		{TunnelID: 3, TunnelType: 2, ChainType: "3", NodeID: 14, Region: "TW"},
		{TunnelID: 3, TunnelType: 2, ChainType: "3", NodeID: 15, Region: "TW"},
		{TunnelID: 4, TunnelType: 2, ChainType: "1", NodeID: 10, Region: "CN"},
		{TunnelID: 4, TunnelType: 2, ChainType: "3", NodeID: 16, Region: ""},
		{TunnelID: 6, TunnelType: 2, ChainType: "3", NodeID: 16, Region: ""},
		{TunnelID: 6, TunnelType: 2, ChainType: "3", NodeID: 11, Region: "JP"},
	}
	got := deriveTunnelRegions([]int64{1, 2, 3, 4, 5, 6}, rows)
	for _, tc := range []struct {
		id      int64
		want    []string
		entries int
	}{{1, []string{"CN"}, 1}, {2, []string{"JP"}, 1}, {3, []string{"TW", "US"}, 2}, {4, []string{""}, 1}, {5, []string{""}, 0}, {6, []string{"JP", ""}, 0}} {
		if !reflect.DeepEqual(got[tc.id].ExitRegions, tc.want) || len(got[tc.id].EntryNodes) != tc.entries {
			t.Fatalf("tunnel %d = %+v, want exits %v / %d entries", tc.id, got[tc.id], tc.want, tc.entries)
		}
	}
}

func TestTunnelEntryGroupsPrivateLabels(t *testing.T) {
	info := tunnelRegionInfo{EntryNodes: []tunnelEntryNode{
		{ID: 1001, Name: "secret entry", Region: "CN", RegionCity: "深圳"},
		{ID: 1002, Name: "secret HK", Region: "HK"},
		{ID: 1003, Name: "secret unknown"},
	}}
	public := info.entryGroups(false)
	for i, label := range []string{"深圳", "香港", "默认入口"} {
		if public[i].Label != label || public[i].Key == info.entryGroups(true)[i].Key {
			t.Fatalf("private group %d = %+v", i, public[i])
		}
	}
	// The public identity cannot be used to recover a node ID or name.
	info.EntryNodes[0].ID = 5000
	info.EntryNodes[0].Name = "renamed secret"
	if info.entryGroups(false)[0].Key != public[0].Key {
		t.Fatal("public key depends on node identity")
	}
	if got := regionName("FI"); got != "芬兰" {
		t.Fatalf("uncommon region name = %q", got)
	}
}

// Plan 184 「显示地区」: the override replaces the exit regions everyone sees
// (one region); the real ones stay in AutoExitRegions for the admin marker.
func TestDeriveTunnelRegionsOverride(t *testing.T) {
	rows := []tunnelRegionRow{
		// 端口转发 from a mainland entry, shown under 香港.
		{TunnelID: 1, TunnelType: 1, RegionOverride: "HK", ChainType: "1", NodeID: 10, Region: "CN"},
		// HK exit that exists for Taiwan (HKIX → Hinet), shown under 台湾.
		{TunnelID: 2, TunnelType: 2, RegionOverride: "TW", ChainType: "1", NodeID: 10, Region: "CN"},
		{TunnelID: 2, TunnelType: 2, RegionOverride: "TW", ChainType: "3", NodeID: 11, Region: "HK"},
		// several exits collapse into the one chosen region
		{TunnelID: 3, TunnelType: 2, RegionOverride: "JP", ChainType: "1", NodeID: 10, Region: "CN"},
		{TunnelID: 3, TunnelType: 2, RegionOverride: "JP", ChainType: "3", NodeID: 13, Region: "US"},
		{TunnelID: 3, TunnelType: 2, RegionOverride: "JP", ChainType: "3", NodeID: 14, Region: "TW"},
		// no override = automatic, unchanged
		{TunnelID: 4, TunnelType: 2, ChainType: "1", NodeID: 10, Region: "CN"},
		{TunnelID: 4, TunnelType: 2, ChainType: "3", NodeID: 11, Region: "HK"},
		// a tunnel without entry/exit rows has no row at all: automatic
		// a broken stored value counts as automatic; lower case is accepted
		{TunnelID: 6, TunnelType: 2, RegionOverride: "Z9", ChainType: "3", NodeID: 11, Region: "HK"},
		{TunnelID: 7, TunnelType: 1, RegionOverride: "tw", ChainType: "1", NodeID: 10, Region: "CN"},
	}
	got := deriveTunnelRegions([]int64{1, 2, 3, 4, 5, 6, 7}, rows)
	for _, tc := range []struct {
		id       int64
		exits    []string
		auto     []string
		override string
		entries  int
	}{
		{1, []string{"HK"}, []string{"CN"}, "HK", 1},
		{2, []string{"TW"}, []string{"HK"}, "TW", 1},
		{3, []string{"JP"}, []string{"TW", "US"}, "JP", 1},
		{4, []string{"HK"}, []string{"HK"}, "", 1},
		{5, []string{""}, []string{""}, "", 0},
		{6, []string{"HK"}, []string{"HK"}, "", 0},
		{7, []string{"TW"}, []string{"CN"}, "TW", 1},
	} {
		info := got[tc.id]
		if !reflect.DeepEqual(info.ExitRegions, tc.exits) || !reflect.DeepEqual(info.AutoExitRegions, tc.auto) || info.RegionOverride != tc.override || len(info.EntryNodes) != tc.entries {
			t.Fatalf("tunnel %d = %+v, want exits %v auto %v override %q / %d entries", tc.id, info, tc.exits, tc.auto, tc.override, tc.entries)
		}
	}
}

func TestNormalizeTunnelRegionOverride(t *testing.T) {
	// Every code the frontend region list (utils/region.ts REGION_CODES, the
	// node region editor's list) offers is accepted.
	for _, code := range strings.Fields("AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW") {
		got, err := NormalizeTunnelRegionOverride(strings.ToLower(code))
		if err != nil || got != code {
			t.Fatalf("code %s = %q, %v", code, got, err)
		}
	}
	if got, err := NormalizeTunnelRegionOverride("  "); err != nil || got != "" {
		t.Fatalf("blank = %q, %v", got, err)
	}
	for _, bad := range []string{"ZZ", "EU", "T", "TWN", "1A", "台湾", "auto"} {
		if got, err := NormalizeTunnelRegionOverride(bad); err == nil {
			t.Fatalf("%q accepted as %q", bad, got)
		}
	}
}

// Plan 207: one table — full names unchanged, every short form set and
// unique (a 「日美最优」 label must never be ambiguous).
func TestRegionShortTable(t *testing.T) {
	seen := map[string]string{}
	for code, r := range regionNames {
		if r.full == "" || r.short == "" {
			t.Fatalf("%s: empty name %+v", code, r)
		}
		if other, dup := seen[r.short]; dup {
			t.Fatalf("short form %q used by %s and %s", r.short, other, code)
		}
		seen[r.short] = code
	}
	for code, want := range map[string]string{"HK": "港", "JP": "日", "US": "美", "TW": "台", "SG": "新", "KR": "韩", "CN": "中", "DE": "德", "GB": "英", "FR": "法", "NL": "荷"} {
		if got, ok := RegionShort(code); !ok || got != want {
			t.Fatalf("RegionShort(%s) = %q %v, want %q", code, got, ok, want)
		}
	}
	if got, ok := RegionShort(" jp "); !ok || got != "日" {
		t.Fatalf("RegionShort normalises: %q %v", got, ok)
	}
	for _, code := range []string{"", "XX", "IT"} {
		if _, ok := RegionShort(code); ok {
			t.Fatalf("RegionShort(%q) should be unknown", code)
		}
	}
	if regionName("HK") != "香港" || RegionName("jp") != "日本" || regionName("IT") != "意大利" || regionName("") != "" {
		t.Fatalf("regionName changed: %q %q %q", regionName("HK"), RegionName("jp"), regionName("IT"))
	}
}
