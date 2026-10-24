package repo

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

func ns(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }

// Plan 147 fill-empty rule: a report never overwrites, never touches DDNS
// nodes and only fills a family field that merely records the main address.
func TestNodePublicIPFillRule(t *testing.T) {
	for _, c := range []struct {
		name       string
		node       model.Node
		ipv4, ipv6 string
		want       map[string]interface{}
	}{
		{"main literal v4, v4 empty → record it", model.Node{ServerIP: "203.0.114.10"}, "203.0.114.10", "2400:cb00::1", map[string]interface{}{"server_ip_v4": "203.0.114.10"}},
		{"all set → nothing", model.Node{ServerIP: "203.0.114.10", ServerIPV4: ns("203.0.114.10")}, "203.0.114.10", "2400:cb00::1", nil},
		{"v6 empty, main is v4 → never adds a family", model.Node{ServerIP: "203.0.114.10", ServerIPV4: ns("203.0.114.10")}, "", "2400:cb00::1", nil},
		{"main is a domain → nothing", model.Node{ServerIP: "hk.example.net"}, "203.0.114.10", "2400:cb00::1", nil},
		{"main differs (NAT / admin IP) → nothing", model.Node{ServerIP: "198.18.0.1"}, "203.0.114.10", "", nil},
		{"never overwrites a set v4", model.Node{ServerIP: "203.0.114.10", ServerIPV4: ns("203.0.114.99")}, "203.0.114.10", "", nil},
		{"main v6 (node 50 style), v4 empty → nothing", model.Node{ServerIP: "240e:390::1", ServerIPV6: ns("240e:390::1")}, "203.0.114.10", "240e:390::2", nil},
		{"main v6 literal, v6 empty → record it", model.Node{ServerIP: "[2400:cb00::1]"}, "", "2400:cb00:0::1", map[string]interface{}{"server_ip_v6": "2400:cb00::1"}},
		{"DDNS node (node 48) → never", model.Node{ServerIP: "", DDNSEnabled: true}, "203.0.114.10", "240e::1", nil},
		{"no address at all → fill main + family", model.Node{ServerIP: ""}, "203.0.114.10", "2400:cb00::1", map[string]interface{}{"server_ip": "203.0.114.10", "server_ip_v4": "203.0.114.10"}},
		{"no address, v6 only → fill main v6", model.Node{ServerIP: " "}, "", "2400:cb00::1", map[string]interface{}{"server_ip": "2400:cb00::1", "server_ip_v6": "2400:cb00::1"}},
		{"private report ignored", model.Node{ServerIP: ""}, "10.7.1.2", "fd00::1", nil},
		{"CGNAT / loopback / link-local ignored", model.Node{ServerIP: ""}, "100.64.1.1", "fe80::1", nil},
		{"wrong family ignored", model.Node{ServerIP: ""}, "2400:cb00::1", "203.0.114.10", nil},
	} {
		n := c.node
		if got := NodePublicIPFill(&n, c.ipv4, c.ipv6); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	for raw, want := range map[string]string{"203.0.114.10": "203.0.114.10", " 8.8.8.8 ": "8.8.8.8", "192.168.1.1": "", "0.1.2.3": "", "198.51.100.1": "", "::ffff:8.8.8.8": "8.8.8.8", "x": ""} {
		if got := PublicIPReportAddr(raw, "v4"); got != want {
			t.Errorf("v4 %q: %q want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]string{"2400:cb00::1": "2400:cb00::1", "[2400:cb00::1]": "2400:cb00::1", "2001:db8::1": "", "fe80::1%eth0": "", "::1": "", "fc00::1": ""} {
		if got := PublicIPReportAddr(raw, "v6"); got != want {
			t.Errorf("v6 %q: %q want %q", raw, got, want)
		}
	}
}

func TestFillNodePublicIPs(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			orphanSeed(t, r,
				&model.Node{ID: 701, Name: "literal", ServerIP: "203.0.114.10", Port: "10000-20000", Status: 1},
				&model.Node{ID: 702, Name: "domain", ServerIP: "hk.example.net", ServerIPV4: ns("hk.example.net"), Port: "10000-20000", Status: 1},
				&model.Node{ID: 748, Name: "ddns", ServerIP: "leikwan-hutai-ct.imgamer.top", ServerIPV6: ns("leikwan-hutai-ct.imgamer.top"), DDNSEnabled: true, Port: "10000-20000", Status: 1},
			)
			load := func(id int64) model.Node {
				t.Helper()
				var n model.Node
				if err := r.db.First(&n, id).Error; err != nil {
					t.Fatal(err)
				}
				return n
			}
			filled, err := r.FillNodePublicIPs(701, "203.0.114.10", "2400:cb00::1")
			if err != nil || !reflect.DeepEqual(filled, []string{"server_ip_v4"}) {
				t.Fatalf("literal: %v %v", filled, err)
			}
			if n := load(701); n.ServerIP != "203.0.114.10" || n.ServerIPV4.String != "203.0.114.10" || n.ServerIPV6.Valid && n.ServerIPV6.String != "" {
				t.Fatalf("literal node: %+v", n)
			}
			// A second, different report never overwrites the recorded value.
			if filled, err := r.FillNodePublicIPs(701, "203.0.114.11", ""); err != nil || len(filled) != 0 || load(701).ServerIPV4.String != "203.0.114.10" {
				t.Fatalf("overwrite: %v %v", filled, err)
			}
			for _, id := range []int64{702, 748} {
				before := load(id)
				if filled, err := r.FillNodePublicIPs(id, "203.0.114.12", "240e:390::9"); err != nil || len(filled) != 0 {
					t.Fatalf("node %d filled %v %v", id, filled, err)
				}
				if after := load(id); after.ServerIP != before.ServerIP || after.ServerIPV4 != before.ServerIPV4 || after.ServerIPV6 != before.ServerIPV6 {
					t.Fatalf("node %d changed: %+v", id, after)
				}
			}
		})
	}
}

func TestPublicIPReportGate(t *testing.T) {
	g := NewPublicIPReportGate()
	now := time.Unix(1_700_000_000, 0)
	if !g.Allow(1, "a", "b", now) {
		t.Fatal("first report dropped")
	}
	if g.Allow(1, "c", "d", now.Add(30*time.Second)) {
		t.Fatal("second report within a minute applied")
	}
	if g.Allow(1, "a", "b", now.Add(10*time.Minute)) {
		t.Fatal("same report within 30 min applied")
	}
	if !g.Allow(1, "c", "d", now.Add(10*time.Minute)) {
		t.Fatal("changed report after a minute dropped")
	}
	if !g.Allow(2, "a", "b", now) {
		t.Fatal("other node gated")
	}
	if !g.Allow(1, "c", "d", now.Add(41*time.Minute)) {
		t.Fatal("same report after 30 min dropped")
	}
}
