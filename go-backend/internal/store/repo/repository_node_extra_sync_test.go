package repo

import (
	"database/sql"
	"reflect"
	"testing"

	"go-backend/internal/store/model"
)

func extraSyncFixture(t *testing.T) (*Repository, model.Node) {
	t.Helper()
	r, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	text := func(value string) sql.NullString { return sql.NullString{String: value, Valid: true} }
	node := model.Node{ID: 1, Name: "two v6", ServerIP: "192.0.2.1", ServerIPV4: text("192.0.2.1"), ServerIPV6: text("2001:db8::1"),
		ExtraIPs: text(`["2001:db8::2","2001:db8::3"]`)}
	other := model.Node{ID: 2, Name: "other", ServerIP: "198.51.100.1", ServerIPV4: text("198.51.100.1")}
	rows := []interface{}{&node, &other,
		&model.Tunnel{ID: 1, Name: "auto", InIP: text("192.0.2.1")},
		&model.Tunnel{ID: 2, Name: "one extra", InIP: text("2001:db8::2")},
		&model.Tunnel{ID: 3, Name: "two extras", InIP: text("2001:db8::2,2001:db8::3")},
		&model.Tunnel{ID: 4, Name: "only removed", InIP: text("2001:DB8::3")},
		&model.Tunnel{ID: 5, Name: "multi entry", InIP: text("2001:db8::3,198.51.100.1")},
		&model.Tunnel{ID: 6, Name: "primary and extra", InIP: text("2001:db8::1,2001:db8::2")},
		&model.Forward{ID: 1, TunnelID: 2}, &model.Forward{ID: 2, TunnelID: 2}, &model.Forward{ID: 3, TunnelID: 1},
		&model.ForwardPort{ID: 1, ForwardID: 1, NodeID: 1, Port: 10001, InIP: text("2001:db8::3")},
		&model.ForwardPort{ID: 2, ForwardID: 2, NodeID: 1, Port: 10002, InIP: text("2001:db8::2")},
		&model.ForwardPort{ID: 3, ForwardID: 3, NodeID: 1, Port: 10003},
	}
	for id := int64(1); id <= 6; id++ {
		rows = append(rows, &model.ChainTunnel{TunnelID: id, NodeID: 1, ChainType: "1", Inx: sql.NullInt64{Valid: true}})
	}
	rows = append(rows, &model.ChainTunnel{TunnelID: 5, NodeID: 2, ChainType: "1", Inx: sql.NullInt64{Int64: 1, Valid: true}})
	for _, row := range rows {
		if err := r.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return r, node
}

func expectEntryAddresses(t *testing.T, r *Repository, tunnels map[int64]string, ports map[int64]string) {
	t.Helper()
	for id, want := range tunnels {
		var tunnel model.Tunnel
		if err := r.db.First(&tunnel, id).Error; err != nil {
			t.Fatal(err)
		}
		if tunnel.InIP.String != want {
			t.Errorf("tunnel %d in_ip = %q, want %q", id, tunnel.InIP.String, want)
		}
	}
	for id, want := range ports {
		var port model.ForwardPort
		if err := r.db.First(&port, id).Error; err != nil {
			t.Fatal(err)
		}
		if port.InIP.String != want {
			t.Errorf("forward_port %d in_ip = %q, want %q", id, port.InIP.String, want)
		}
	}
}

func autoBuild(entries []model.Node, _ string) string {
	out := ""
	for i, entry := range entries {
		if i > 0 {
			out += ","
		}
		out += entry.ServerIPV4.String
	}
	return out
}

func TestRewriteNodeEntryAddressesDropsRemovedExtraIP(t *testing.T) {
	r, oldNode := extraSyncFixture(t)
	newNode := oldNode
	newNode.ExtraIPs = sql.NullString{String: `["2001:db8::2"]`, Valid: true}
	if err := r.db.Save(&newNode).Error; err != nil {
		t.Fatal(err)
	}
	result, err := r.RewriteNodeEntryAddresses(&oldNode, &newNode, autoBuild)
	if err != nil {
		t.Fatal(err)
	}
	expectEntryAddresses(t, r, map[int64]string{
		1: "192.0.2.1",               // auto default untouched
		2: "2001:db8::2",             // kept pick
		3: "2001:db8::2",             // removed IP dropped
		4: "192.0.2.1",               // nothing left: auto rebuild
		5: "198.51.100.1",            // other entry node kept
		6: "2001:db8::1,2001:db8::2", // untouched
	}, map[int64]string{1: "", 2: "2001:db8::2", 3: ""})
	if !reflect.DeepEqual(result.TunnelIDs, []int64{3, 4, 5}) || !reflect.DeepEqual(result.BindForwardIDs, []int64{1}) {
		t.Fatalf("unexpected result %+v", result)
	}
}

func TestRewriteNodeEntryAddressesReplacesEditedExtraIP(t *testing.T) {
	r, oldNode := extraSyncFixture(t)
	newNode := oldNode
	newNode.ExtraIPs = sql.NullString{String: `["2001:db8::2","2001:db8::9"]`, Valid: true}
	if err := r.db.Save(&newNode).Error; err != nil {
		t.Fatal(err)
	}
	result, err := r.RewriteNodeEntryAddresses(&oldNode, &newNode, autoBuild)
	if err != nil {
		t.Fatal(err)
	}
	expectEntryAddresses(t, r, map[int64]string{
		1: "192.0.2.1", 2: "2001:db8::2", 3: "2001:db8::2,2001:db8::9", 4: "2001:db8::9", 5: "2001:db8::9,198.51.100.1",
	}, map[int64]string{1: "2001:db8::9", 2: "2001:db8::2"})
	if !reflect.DeepEqual(result.BindForwardIDs, []int64{1}) {
		t.Fatalf("unexpected result %+v", result)
	}
}

func TestRewriteNodeEntryAddressesAddedExtraOrPrimaryChangeKeepsPicks(t *testing.T) {
	r, oldNode := extraSyncFixture(t)
	added := oldNode
	added.ExtraIPs = sql.NullString{String: `["2001:db8::2","2001:db8::3","2001:db8::5"]`, Valid: true}
	result, err := r.RewriteNodeEntryAddresses(&oldNode, &added, autoBuild)
	if err != nil || result.EntryAddressesUpdated != 0 || len(result.TunnelIDs) != 0 {
		t.Fatalf("adding an extra IP rewrote addresses: %+v %v", result, err)
	}
	// Legacy comma storage of the same set is not a change either.
	legacy := oldNode
	legacy.ExtraIPs = sql.NullString{String: "2001:db8::2, 2001:db8::3", Valid: true}
	if got := nodeExtraIPReplacements(&legacy, &oldNode); len(got) != 0 {
		t.Fatalf("legacy spelling treated as removal: %v", got)
	}

	primary := oldNode
	primary.ServerIPV6 = sql.NullString{String: "2001:db8::10", Valid: true}
	if err := r.db.Save(&primary).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.RewriteNodeEntryAddresses(&oldNode, &primary, autoBuild); err != nil {
		t.Fatal(err)
	}
	expectEntryAddresses(t, r, map[int64]string{
		1: "192.0.2.1", 2: "2001:db8::2", 3: "2001:db8::2,2001:db8::3", 6: "2001:db8::10,2001:db8::2",
	}, map[int64]string{1: "2001:db8::3", 2: "2001:db8::2"})
}

func TestRewriteNodeEntryAddressesKeepsExtraPromotedToPrimary(t *testing.T) {
	r, oldNode := extraSyncFixture(t)
	newNode := oldNode
	// 2001:db8::3 is promoted to the v6 field and the old v6 becomes an extra
	// IP. Both stay on the node, so nothing counts as removed.
	newNode.ServerIPV6 = sql.NullString{String: "2001:db8::3", Valid: true}
	newNode.ExtraIPs = sql.NullString{String: `["2001:db8::2","2001:db8::1"]`, Valid: true}
	if got := nodeExtraIPReplacements(&oldNode, &newNode); len(got) != 0 {
		t.Fatalf("swapped addresses treated as removed: %v", got)
	}
	if err := r.db.Save(&newNode).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.RewriteNodeEntryAddresses(&oldNode, &newNode, autoBuild); err != nil {
		t.Fatal(err)
	}
	// Primary slots keep today's rule: the old v6 token follows the v6 field.
	expectEntryAddresses(t, r, map[int64]string{3: "2001:db8::2,2001:db8::3", 4: "2001:DB8::3", 6: "2001:db8::3,2001:db8::2"}, nil)
}

func TestTunnelEntryInIPTxDropsExtraIPsOfRemovedEntryNode(t *testing.T) {
	r, _ := extraSyncFixture(t)
	submitted := "2001:db8::2,198.51.100.1,custom.example"
	tx := r.db.Begin()
	defer tx.Rollback()
	got, err := r.TunnelEntryInIPTx(tx, 5, &submitted, []int64{2}, "", autoBuild)
	if err != nil {
		t.Fatal(err)
	}
	if got != "198.51.100.1,custom.example" {
		t.Fatalf("in_ip = %q", got)
	}
	// A picked extra IP of a node that stays an entry is kept.
	submitted = "2001:db8::3"
	if got, err = r.TunnelEntryInIPTx(tx, 5, &submitted, []int64{1}, "", autoBuild); err != nil || got != "2001:db8::3" {
		t.Fatalf("kept pick = %q (%v)", got, err)
	}
}
