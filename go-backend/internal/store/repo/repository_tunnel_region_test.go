package repo

import (
	"reflect"
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
