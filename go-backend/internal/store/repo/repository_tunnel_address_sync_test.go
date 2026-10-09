package repo

import (
	"database/sql"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

func TestTunnelEntryInIPTx(t *testing.T) {
	r, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ns := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	for _, row := range []interface{}{
		&model.Node{ID: 1, ServerIP: "old.example", ServerIPV4: ns("192.0.2.1"), ServerIPV6: ns("2001:db8::A")},
		&model.Node{ID: 2, ServerIP: "shared.example", ServerIPV4: ns("192.0.2.2")},
		&model.Node{ID: 3, ServerIP: "SHARED.example", ServerIPV4: ns("192.0.2.3")},
		&model.Tunnel{ID: 8, InIP: ns("custom.example,192.0.2.1")},
		&model.ChainTunnel{TunnelID: 8, NodeID: 1, ChainType: "1"},
		&model.ChainTunnel{TunnelID: 8, NodeID: 2, ChainType: "1"},
		&model.ChainTunnel{TunnelID: 8, NodeID: 3, ChainType: "3"},
	} {
		if err := r.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Verify the repository passes ordered entries and preference to the shared builder.
	build := func(nodes []model.Node, pref string) string {
		var result []string
		for _, n := range nodes {
			if pref == "v6" && n.ServerIPV6.String != "" {
				result = append(result, n.ServerIPV6.String)
			}
			if n.ServerIPV4.String != "" {
				result = append(result, n.ServerIPV4.String)
			}
			if pref != "v6" && n.ServerIPV6.String != "" {
				result = append(result, n.ServerIPV6.String)
			}
			if n.ServerIPV4.String == "" && n.ServerIPV6.String == "" {
				result = append(result, n.ServerIP)
			}
		}
		return strings.Join(result, ",")
	}
	for _, tc := range []struct {
		name, value string
		absent      bool
		entries     []int64
		pref, want  string
	}{
		{name: "append and custom", value: " custom.example\n192.0.2.1 ", entries: []int64{1, 2, 3}, want: "custom.example,192.0.2.1,192.0.2.3"},
		{name: "remove normalized", value: "custom.example,OLD.EXAMPLE,[2001:DB8::a],192.0.2.1,192.0.2.2", entries: []int64{2}, want: "custom.example,192.0.2.2"},
		{name: "shared removed and kept", value: "sHaReD.example,custom.example", entries: []int64{1, 3}, want: "sHaReD.example,custom.example,192.0.2.3"},
		{name: "same set preserves bytes", value: " Custom.example, \n192.0.2.1 ", entries: []int64{2, 1}, want: " Custom.example, \n192.0.2.1 "},
		{name: "unchanged empty rebuild", entries: []int64{2, 1}, pref: "v6", want: "192.0.2.2,2001:db8::A,192.0.2.1"},
		{name: "empty result rebuild", value: "192.0.2.1", entries: []int64{2}, want: "192.0.2.2"},
		{name: "absent uses stored", absent: true, entries: []int64{1, 2, 3}, want: "custom.example,192.0.2.1,192.0.2.3"},
		{name: "append dedup", value: "192.0.2.3,custom.example,custom.example", entries: []int64{1, 2, 3}, want: "192.0.2.3,custom.example,custom.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value *string
			if !tc.absent {
				value = &tc.value
			}
			got, err := r.TunnelEntryInIPTx(r.db, 8, value, tc.entries, tc.pref, build)
			if err != nil || got != tc.want {
				t.Fatalf("got=%q want=%q err=%v", got, tc.want, err)
			}
		})
	}
	t.Run("shared removed and retained", func(t *testing.T) {
		if err := r.db.Model(&model.Node{}).Where("id = 1").Update("server_ip", "shared.example").Error; err != nil {
			t.Fatal(err)
		}
		value := "SHARED.example,custom.example"
		got, err := r.TunnelEntryInIPTx(r.db, 8, &value, []int64{1}, "", build)
		if err != nil || got != value {
			t.Fatalf("shared retained address=%q err=%v", got, err)
		}
	})
	var tunnel model.Tunnel
	r.db.First(&tunnel, 8)
	if tunnel.InIP.String != "custom.example,192.0.2.1" {
		t.Fatal("address calculation wrote data")
	}
}
