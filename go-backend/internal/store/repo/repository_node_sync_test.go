package repo

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

func TestRewriteNodeAddressTokens(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		replacements map[string]string
		want         string
	}{
		{"custom domain", "entry.example.com, 192.0.2.1", map[string]string{"192.0.2.1": "192.0.2.2"}, "entry.example.com,192.0.2.2"},
		{"other entry node", "192.0.2.1,198.51.100.1", map[string]string{"192.0.2.1": "192.0.2.2"}, "192.0.2.2,198.51.100.1"},
		{"v4 and v6", "192.0.2.1,2001:db8::1", map[string]string{"192.0.2.1": "192.0.2.2", "2001:db8::1": "2001:db8::2"}, "192.0.2.2,2001:db8::2"},
		{"cleared family", "192.0.2.1,2001:db8::1", map[string]string{"2001:db8::1": ""}, "192.0.2.1"},
		{"all cleared", "2001:db8::1", map[string]string{"2001:db8::1": ""}, ""},
		{"dedup keeps order", "entry.example.com,192.0.2.1,192.0.2.2,entry.example.com,198.51.100.1", map[string]string{"192.0.2.1": "192.0.2.2"}, "entry.example.com,192.0.2.2,198.51.100.1"},
		{"trim and empty tokens", " , 192.0.2.1, ,entry.example.com, ", map[string]string{"192.0.2.1": "192.0.2.2"}, "192.0.2.2,entry.example.com"},
		{"no partial match", "192.0.2.10,192.0.2.1.example.com", map[string]string{"192.0.2.1": "192.0.2.2"}, "192.0.2.10,192.0.2.1.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rewriteNodeAddressTokens(tt.value, tt.replacements); got != tt.want {
				t.Fatalf("rewrite = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRewriteNodeEntryAddresses(t *testing.T) {
	r, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	textValue := func(value string) sql.NullString { return sql.NullString{String: value, Valid: true} }
	oldNode := model.Node{ID: 1, Name: "entry", ServerIP: "192.0.2.1", ServerIPV4: textValue("192.0.2.1"), ServerIPV6: textValue("2001:db8::1")}
	newNode := oldNode
	newNode.ServerIP = "192.0.2.2"
	newNode.ServerIPV4 = textValue("192.0.2.2")
	newNode.ServerIPV6 = sql.NullString{}
	otherNode := model.Node{ID: 2, Name: "other entry", ServerIP: "198.51.100.1", ServerIPV4: textValue("198.51.100.1")}
	for _, row := range []interface{}{
		&newNode, &otherNode,
		&model.Tunnel{ID: 1, Name: "multiple entries", InIP: textValue("192.0.2.1,198.51.100.1,2001:db8::1")},
		&model.Tunnel{ID: 2, Name: "custom domain", InIP: textValue("entry.example.com")},
		&model.Tunnel{ID: 3, Name: "rebuild", InIP: textValue("2001:db8::1"), IPPreference: "v6"},
		&model.Tunnel{ID: 4, Name: "exit only", InIP: textValue("192.0.2.1")},
		&model.ChainTunnel{TunnelID: 1, NodeID: 1, ChainType: "1", Inx: sql.NullInt64{Int64: 0, Valid: true}},
		&model.ChainTunnel{TunnelID: 1, NodeID: 2, ChainType: "1", Inx: sql.NullInt64{Int64: 1, Valid: true}},
		&model.ChainTunnel{TunnelID: 2, NodeID: 1, ChainType: "1"},
		&model.ChainTunnel{TunnelID: 3, NodeID: 1, ChainType: "1", Inx: sql.NullInt64{Int64: 1, Valid: true}},
		&model.ChainTunnel{TunnelID: 3, NodeID: 2, ChainType: "1", Inx: sql.NullInt64{Int64: 0, Valid: true}},
		&model.ChainTunnel{TunnelID: 4, NodeID: 1, ChainType: "3"},
		&model.Forward{ID: 1, TunnelID: 1},
		&model.Forward{ID: 2, TunnelID: 1},
		&model.Forward{ID: 3, TunnelID: 2},
		&model.Forward{ID: 4, TunnelID: 1},
		&model.ForwardPort{ID: 1, ForwardID: 1, NodeID: 1, Port: 10001},
		&model.ForwardPort{ID: 2, ForwardID: 2, NodeID: 1, Port: 10002, InIP: textValue("custom.example.com")},
		&model.ForwardPort{ID: 3, ForwardID: 3, NodeID: 1, Port: 10003, InIP: textValue("192.0.2.1")},
		&model.ForwardPort{ID: 4, ForwardID: 4, NodeID: 2, Port: 10004, InIP: textValue("192.0.2.1")},
		&model.ForwardPort{ID: 5, ForwardID: 3, NodeID: 1, Port: 10005, InIP: textValue("2001:db8::1")},
	} {
		if err := r.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	buildCalls := 0
	result, err := r.RewriteNodeEntryAddresses(&oldNode, &newNode, func(entries []model.Node, preference string) string {
		buildCalls++
		if preference != "v6" || len(entries) != 2 || entries[0].ID != 2 || entries[1].ServerIPV4.String != "192.0.2.2" || entries[1].ServerIPV6.Valid {
			t.Errorf("rebuild did not receive current ordered entries: preference=%q entries=%+v", preference, entries)
		}
		var addresses []string
		for _, entry := range entries {
			addresses = append(addresses, entry.ServerIPV4.String)
		}
		return strings.Join(addresses, ",")
	})
	if err != nil {
		t.Fatal(err)
	}
	if buildCalls != 1 || !reflect.DeepEqual(result.TunnelIDs, []int64{1, 3}) || !reflect.DeepEqual(result.ForwardIDs, []int64{1, 3}) || result.EntryAddressesUpdated != 4 {
		t.Fatalf("unexpected sync result: %+v rebuilds=%d", result, buildCalls)
	}
	for id, want := range map[int64]string{1: "192.0.2.2,198.51.100.1", 2: "entry.example.com", 3: "198.51.100.1,192.0.2.2", 4: "192.0.2.1"} {
		var tunnel model.Tunnel
		if err := r.db.First(&tunnel, id).Error; err != nil {
			t.Fatal(err)
		}
		if tunnel.InIP.String != want {
			t.Errorf("tunnel %d in_ip=%q, want %q", id, tunnel.InIP.String, want)
		}
	}
	for id, want := range map[int64]string{1: "", 2: "custom.example.com", 3: "192.0.2.2", 4: "192.0.2.1", 5: ""} {
		var port model.ForwardPort
		if err := r.db.First(&port, id).Error; err != nil {
			t.Fatal(err)
		}
		if port.InIP.String != want {
			t.Errorf("forward_port %d in_ip=%q, want %q", id, port.InIP.String, want)
		}
		if id == 1 && port.InIP.Valid {
			t.Error("inherited forward_port unexpectedly gained a saved address")
		}
	}
}

func TestRewriteNodeEntryAddressesUnchanged(t *testing.T) {
	r, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	oldNode := model.Node{ID: 1, Name: "old name", ServerIP: "192.0.2.1"}
	newNode := oldNode
	newNode.Name = "new name"
	for _, row := range []interface{}{
		&model.Tunnel{ID: 1, InIP: sql.NullString{String: " 192.0.2.1 ,192.0.2.1", Valid: true}},
		&model.ChainTunnel{TunnelID: 1, NodeID: 1, ChainType: "1"},
	} {
		if err := r.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, err := r.RewriteNodeEntryAddresses(&oldNode, &newNode, func([]model.Node, string) string {
		t.Fatal("unchanged addresses must not be rebuilt")
		return ""
	})
	if err != nil || result.EntryAddressesUpdated != 0 || len(result.TunnelIDs) != 0 || len(result.ForwardIDs) != 0 {
		t.Fatalf("unexpected unchanged result: %+v, error=%v", result, err)
	}
	var tunnel model.Tunnel
	if err := r.db.First(&tunnel, 1).Error; err != nil {
		t.Fatal(err)
	}
	if tunnel.InIP.String != " 192.0.2.1 ,192.0.2.1" {
		t.Fatalf("name-only edit changed saved ingress: %q", tunnel.InIP.String)
	}
}

func TestRewriteNodeEntryAddressesRollback(t *testing.T) {
	r, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	oldNode := model.Node{ID: 1, ServerIP: "192.0.2.1"}
	newNode := model.Node{ID: 1, ServerIP: "192.0.2.2"}
	for _, row := range []interface{}{
		&newNode,
		&model.Tunnel{ID: 1, InIP: sql.NullString{String: "192.0.2.1", Valid: true}},
		&model.ChainTunnel{TunnelID: 1, NodeID: 1, ChainType: "1"},
		&model.ForwardPort{ID: 1, ForwardID: 1, NodeID: 1, Port: 10001, InIP: sql.NullString{String: "192.0.2.1", Valid: true}},
	} {
		if err := r.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := r.db.Exec("CREATE TRIGGER fail_node_address_sync BEFORE UPDATE ON forward_port BEGIN SELECT RAISE(ABORT, 'injected address sync failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	result, err := r.RewriteNodeEntryAddresses(&oldNode, &newNode, nil)
	if err == nil || !strings.Contains(err.Error(), "injected address sync failure") || result.EntryAddressesUpdated != 0 || len(result.TunnelIDs) != 0 || len(result.ForwardIDs) != 0 {
		t.Fatalf("expected rolled-back sync: result=%+v error=%v", result, err)
	}
	var tunnel model.Tunnel
	var port model.ForwardPort
	if err := r.db.First(&tunnel, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.First(&port, 1).Error; err != nil {
		t.Fatal(err)
	}
	if tunnel.InIP.String != "192.0.2.1" || port.InIP.String != "192.0.2.1" {
		t.Fatalf("partial address changes committed: tunnel=%q port=%q", tunnel.InIP.String, port.InIP.String)
	}
}
