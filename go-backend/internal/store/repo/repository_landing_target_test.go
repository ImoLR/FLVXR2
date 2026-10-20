package repo

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

func TestLandingTargetReuseOrCreate(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, state := range []string{"missing", "active", "other owner", "other protocol", "disabled", "expired", "quota", "delete pending", "chain internal"} {
				t.Run(state, func(t *testing.T) {
					r, s := landingRepoFixture(t, dialect)
					var old *model.LandingNode
					if state == "chain internal" {
						orphanSeed(t, r, &model.ForwardChain{ID: 51, UserID: 501, State: model.ChainStateActive},
							&model.ForwardChainNode{ID: 61, ChainID: 51, LandingServerID: s.ID, NodeID: s.NodeID, Port: s.PortMin, State: model.ChainNodeActive})
					} else if state != "missing" {
						var err error
						old, err = r.CreateLandingNode(501, s.ID, "original", "ss2022", false)
						if err != nil {
							t.Fatal(err)
						}
						changes := map[string]interface{}{}
						switch state {
						case "other owner":
							changes["user_id"] = 502
						case "other protocol":
							changes["protocol"] = "vless_reality_vision"
						case "disabled":
							changes["status"] = "disabled"
						case "expired":
							changes["expiry_mode"], changes["expires_at"] = "fixed", time.Now().Add(-time.Hour).UnixMilli()
						case "quota":
							changes["traffic_limit_bytes"], changes["billed_in"] = 10, 10
						case "delete pending":
							changes["delete_pending"] = true
						}
						if len(changes) > 0 {
							if err := r.db.Model(old).Updates(changes).Error; err != nil {
								t.Fatal(err)
							}
						}
					}
					n, created, err := r.ReuseOrCreateLandingNode(501, s.ID, "rule", "ss2022", false)
					if err != nil {
						t.Fatal(err)
					}
					wantCreated := state != "active"
					if created != wantCreated || n.UserID != 501 || n.Protocol != "ss2022" || (old != nil && (n.ID == old.ID) == wantCreated) {
						t.Fatalf("created=%v, old=%+v, result=%+v", created, old, n)
					}
					if state == "chain internal" && n.Port == s.PortMin {
						t.Fatal("reused the hidden chain listener")
					}
					if !r.db.Migrator().HasIndex(&model.LandingNode{}, "idx_landing_reuse") {
						t.Fatal("reuse lookup lacks its composite index")
					}
				})
			}
		})
	}
}

func TestLandingTargetReusesOldestAtCapAndChecksPermission(t *testing.T) {
	r, s := landingRepoFixture(t, "sqlite")
	var nodes []*model.LandingNode
	for i := 0; i < 2; i++ {
		n, err := r.CreateLandingNode(501, s.ID, fmt.Sprint("node", i), "ss2022", false)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	// Creation time, then ID, defines "oldest", rather than current list order.
	if err := r.db.Model(nodes[1]).Update("created_time", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.SaveLandingUserSetting(501, 2, nil, 0); err != nil {
		t.Fatal(err)
	}
	n, created, err := r.ReuseOrCreateLandingNode(501, s.ID, "rule", "ss2022", false)
	if err != nil || created || n.ID != nodes[1].ID {
		t.Fatalf("reuse at cap: %+v %v %v", n, created, err)
	}
	if _, _, err := r.ReuseOrCreateLandingNode(501, s.ID, "new protocol", "vless_reality_vision", false); err == nil {
		t.Fatal("new creation bypassed cap")
	}
	if err := r.db.Where("landing_server_id = ?", s.ID).Delete(&model.LandingServerUserGroup{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ReuseOrCreateLandingNode(501, s.ID, "revoked", "ss2022", false); err == nil {
		t.Fatal("reuse bypassed permission")
	}
	if err := r.db.Model(&model.User{}).Where("id = ?", 501).Update("role_id", 1).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ReuseOrCreateLandingNode(501, s.ID, "admin for user", "ss2022", true); err == nil {
		t.Fatal("admin reuse bypassed owner's permission")
	}
}

func TestLandingTargetConcurrentReuseOrCreate(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			type result struct {
				node    *model.LandingNode
				created bool
				err     error
			}
			results := make(chan result, 2)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					n, created, err := r.ReuseOrCreateLandingNode(501, s.ID, "concurrent", "ss2022", false)
					results <- result{n, created, err}
				}()
			}
			close(start)
			wg.Wait()
			close(results)
			created, id := 0, int64(0)
			for result := range results {
				if result.err != nil {
					t.Fatal(result.err)
				}
				if id != 0 && result.node.ID != id {
					t.Fatal("concurrent calls returned different nodes")
				}
				id = result.node.ID
				if result.created {
					created++
				}
			}
			nodes, err := r.ListLandingNodes(501, false)
			if err != nil || len(nodes) != 1 || created != 1 {
				t.Fatalf("nodes=%+v created=%d err=%v", nodes, created, err)
			}
		})
	}
}

func TestLandingTargetGroupedForwardCountAndDeleteNames(t *testing.T) {
	r, s := landingRepoFixture(t, "sqlite")
	n, err := r.CreateLandingNode(501, s.ID, "shared", "ss2022", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.CreateLandingNode(501, s.ID, "unused", "ss2022", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 7; i++ {
		orphanSeed(t, r, &model.Forward{ID: int64(i), UserID: 501, LandingNodeID: n.ID, Name: fmt.Sprintf("rule-%d", i)})
	}
	queries, grouped := 0, 0
	count := func(tx *gorm.DB) {
		queries++
		if strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "group by") {
			grouped++
		}
	}
	if err := r.db.Callback().Query().After("gorm:query").Register("landing-target-count", count); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Callback().Row().After("gorm:row").Register("landing-target-count", count); err != nil {
		t.Fatal(err)
	}
	data, err := r.LandingNodeListData([]model.LandingNode{*n, *other})
	if err != nil || data.ForwardCounts[n.ID] != 7 || data.ForwardCounts[other.ID] != 0 || queries != 4 || grouped != 1 {
		t.Fatalf("list data=%+v queries=%d grouped=%d err=%v", data, queries, grouped, err)
	}
	for _, remove := range []func() error{
		func() error { _, err := r.PrepareLandingNodeDeletion(n.ID); return err },
		func() error { return r.DeleteLandingNode(n.ID) },
	} {
		err := remove()
		if err == nil || !strings.Contains(err.Error(), "rule-1") || !strings.Contains(err.Error(), "rule-5") || !strings.Contains(err.Error(), "等 7 条") || strings.Contains(err.Error(), "rule-6") {
			t.Fatalf("unbounded or missing referencing rule names: %v", err)
		}
	}
}

func TestLandingTargetLinksUseEveryEntryIPWithSameCredentials(t *testing.T) {
	for _, protocol := range []string{"ss2022", "vless_reality_vision"} {
		for _, addresses := range []string{`["192.0.2.10","2001:db8::10"]`, "192.0.2.10,2001:db8::10,192.0.2.10"} {
			for _, override := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/override=%v", protocol, addresses, override), func(t *testing.T) {
					r, s := landingRepoFixture(t, "sqlite")
					n, err := r.CreateLandingNode(501, s.ID, "original credentials", protocol, false)
					if err != nil {
						t.Fatal(err)
					}
					tunnel := &model.Tunnel{ID: 801, Name: "entry", InIP: sql.NullString{String: addresses, Valid: true}}
					port := &model.ForwardPort{ForwardID: 901, NodeID: 601, Port: 15000}
					if override {
						tunnel.InIP.String = "192.0.2.99"
						port.InIP = sql.NullString{String: addresses, Valid: true}
					}
					orphanSeed(t, r, tunnel, &model.Forward{ID: 901, UserID: 501, TunnelID: 801, LandingNodeID: n.ID, Name: "rule"}, port)
					links, err := r.LandingNodeLinks(*n)
					if err != nil || len(links) != 3 {
						t.Fatalf("links=%+v err=%v", links, err)
					}
					direct, err := url.Parse(links[0].URL)
					if err != nil {
						t.Fatal(err)
					}
					for i, host := range []string{"192.0.2.10", "2001:db8::10"} {
						link := links[i+1]
						u, err := url.Parse(link.URL)
						if err != nil || u.Hostname() != host || u.Port() != "15000" || u.User.String() != direct.User.String() || u.RawQuery != direct.RawQuery || link.ForwardID != 901 {
							t.Fatalf("entry variant=%+v err=%v", link, err)
						}
					}
					// Callers without expansion keep their original single-entry behavior.
					chainEntries, err := r.forwardEntries(model.Forward{ID: 901, TunnelID: 801})
					if err != nil || len(chainEntries) != 1 {
						t.Fatalf("chain entry behavior changed: %+v %v", chainEntries, err)
					}
				})
			}
		}
	}
}

func TestLandingTargetMultipleEntriesHaveUniqueMatchingLinks(t *testing.T) {
	r, s := landingRepoFixture(t, "sqlite")
	n, err := r.CreateLandingNode(501, s.ID, "original", "ss2022", false)
	if err != nil {
		t.Fatal(err)
	}
	orphanSeed(t, r,
		&model.Node{ID: 602, Name: "second entry", ServerIP: "192.0.2.2"},
		&model.Tunnel{ID: 801, Name: "entries", InIP: sql.NullString{String: "203.0.113.1,192.0.2.2,2001:db8::1", Valid: true}},
		&model.Forward{ID: 901, UserID: 501, TunnelID: 801, LandingNodeID: n.ID, Name: "rule"},
		&model.ForwardPort{ForwardID: 901, NodeID: 601, Port: 15000},
		&model.ForwardPort{ForwardID: 901, NodeID: 602, Port: 16000})
	links, err := r.LandingNodeLinks(*n)
	if err != nil || len(links) != 4 {
		t.Fatalf("links=%+v err=%v", links, err)
	}
	for _, link := range links[1:] {
		u, _ := url.Parse(link.URL)
		wantPort := "15000"
		if u.Hostname() == "192.0.2.2" {
			wantPort = "16000"
		}
		if u.Port() != wantPort {
			t.Fatalf("entry address used another entry's port: %+v", link)
		}
	}
	// A custom tunnel alias with the same rule port must not duplicate URLs.
	if err := r.db.Model(&model.Tunnel{}).Where("id = ?", 801).Update("in_ip", "alias.example.com,other.example.com").Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Model(&model.ForwardPort{}).Where("forward_id = ?", 901).Update("port", 15000).Error; err != nil {
		t.Fatal(err)
	}
	links, err = r.LandingNodeLinks(*n)
	if err != nil || len(links) != 3 {
		t.Fatalf("duplicate custom entry links: %+v %v", links, err)
	}
}

func TestLandingTargetLinksKeepMixedCustomEntryAddresses(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		t.Run(fmt.Sprintf("multiple=%v", multiple), func(t *testing.T) {
			r, s := landingRepoFixture(t, "sqlite")
			n, err := r.CreateLandingNode(501, s.ID, "mixed entry addresses", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			addresses := "203.0.113.1,192.0.2.99,custom.example.com"
			if multiple {
				addresses += ",192.0.2.2,192.0.2.3"
			}
			orphanSeed(t, r,
				&model.Tunnel{ID: 801, Name: "entries", InIP: sql.NullString{String: addresses, Valid: true}},
				&model.Forward{ID: 901, UserID: 501, TunnelID: 801, LandingNodeID: n.ID, Name: "rule"},
				&model.ForwardPort{ForwardID: 901, NodeID: 601, Port: 15000})
			if multiple {
				orphanSeed(t, r,
					&model.Node{ID: 602, Name: "second entry", ServerIP: "192.0.2.2"},
					&model.Node{ID: 603, Name: "unassigned entry", ServerIP: "192.0.2.3"},
					&model.ForwardPort{ForwardID: 901, NodeID: 602, Port: 16000},
					&model.ChainTunnel{TunnelID: 801, NodeID: 603, ChainType: "1"})
			}
			links, err := r.LandingNodeLinks(*n)
			want := map[string]bool{"203.0.113.1:15000": true, "192.0.2.99:15000": true, "custom.example.com:15000": true}
			if multiple {
				want["192.0.2.2:16000"], want["192.0.2.99:16000"], want["custom.example.com:16000"] = true, true, true
			}
			if err != nil || len(links) != len(want)+1 {
				t.Fatalf("links=%+v err=%v", links, err)
			}
			for _, link := range links[1:] {
				u, err := url.Parse(link.URL)
				if err != nil || !want[u.Host] {
					t.Fatalf("unexpected entry variant: %+v %v", link, err)
				}
				delete(want, u.Host)
			}
			if len(want) != 0 {
				t.Fatalf("missing custom entry variants: %+v", want)
			}
		})
	}
}
