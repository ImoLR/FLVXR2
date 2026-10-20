package repo

import (
	"database/sql"
	"net"
	"net/url"
	"testing"

	"go-backend/internal/store/model"
)

func TestCopyLinksUsePickedExtraEntryIPs(t *testing.T) {
	for _, picked := range []string{"2001:db8::11", "2001:db8::10,2001:db8::11", "192.0.2.10,2001:db8::11"} {
		t.Run(picked, func(t *testing.T) {
			r, s := landingRepoFixture(t, "sqlite")
			n, err := r.CreateLandingNode(501, s.ID, "landing", "vless_reality_vision", false)
			if err != nil {
				t.Fatal(err)
			}
			orphanSeed(t, r,
				&model.Node{ID: 602, Name: "dual IPv6", ServerIP: "192.0.2.10", ServerIPV6: sql.NullString{String: "2001:db8::10", Valid: true}, ExtraIPs: sql.NullString{String: `["2001:db8::11"]`, Valid: true}},
				&model.Node{ID: 603, Name: "other entry", ServerIP: "192.0.2.20", ExtraIPs: sql.NullString{String: `["2001:db8::20"]`, Valid: true}},
				&model.Tunnel{ID: 801, Name: "entries", InIP: sql.NullString{String: picked + ",2001:db8::20", Valid: true}},
				&model.ChainTunnel{TunnelID: 801, NodeID: 602, ChainType: "1"},
				&model.ChainTunnel{TunnelID: 801, NodeID: 603, ChainType: "1"},
				&model.Forward{ID: 901, UserID: 501, TunnelID: 801, LandingNodeID: n.ID, Name: "rule"},
				&model.ForwardPort{ForwardID: 901, NodeID: 602, Port: 15000})
			// This user's rule has only entry 602. Entry 603's extra IP must
			// not appear in either type of copy link.
			want := landingEntryHosts(picked)
			entries, err := r.ForwardEntryAddresses(901)
			if err != nil || len(entries) != len(want) {
				t.Fatalf("chain entries=%+v err=%v", entries, err)
			}
			links, err := r.LandingNodeLinks(*n)
			if err != nil || len(links) != len(want)+1 {
				t.Fatalf("landing links=%+v err=%v", links, err)
			}
			chain := model.ForwardChain{UUID: n.UUID, PublicKey: n.PublicKey, ShortID: n.ShortID, ServerNames: n.ServerNames}
			for i, host := range want {
				if entries[i].NodeID != 602 || entries[i].Host != host || entries[i].Port != 15000 {
					t.Fatalf("wrong chain entry: %+v", entries[i])
				}
				for _, raw := range []string{ChainLinkURL(chain, entries[i].Host, entries[i].Port, "chain"), links[i+1].URL} {
					u, err := url.Parse(raw)
					if err != nil || u.Host != net.JoinHostPort(host, "15000") || u.User.Username() != n.UUID || u.Query().Get("pbk") != n.PublicKey {
						t.Fatalf("copy link=%q err=%v", raw, err)
					}
				}
			}
		})
	}
}
