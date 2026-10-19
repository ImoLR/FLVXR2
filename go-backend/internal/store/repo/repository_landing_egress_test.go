package repo

import (
	"strconv"
	"strings"
	"testing"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
)

func TestLandingEgressRepository(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			for _, table := range []interface{}{&model.LandingOutbound{}, &model.LandingRouteProfile{}, &model.LandingWarpAccount{}, &model.LandingEgressSetting{}} {
				if !r.db.Migrator().HasTable(table) {
					t.Fatalf("missing table %T", table)
				}
			}
			for _, col := range []string{"route_profile_id", "egress_hash", "egress_restart_ok", "egress_state", "egress_ready"} {
				if !r.db.Migrator().HasColumn(&model.LandingServer{}, col) {
					t.Fatalf("landing_server.%s missing", col)
				}
			}
			if !r.db.Migrator().HasColumn(&model.LandingNode{}, "route_profile_id") || !r.db.Migrator().HasColumn(&model.Node{}, "landing_egress") {
				t.Fatal("node/landing_node egress columns missing")
			}
			// Existing servers stay on the built-in default after migration.
			if s.RouteProfileID != 0 || s.EgressHash != "" {
				t.Fatalf("migration changed defaults: %+v", s)
			}
			n, err := r.CreateLandingNode(501, s.ID, "egress", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			if n.RouteProfileID != nil {
				t.Fatal("new node does not follow its server")
			}

			warp, err := r.SaveLandingOutbound(&landingegress.Outbound{Name: "WARP", Type: landingegress.TypeWarp})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.SaveLandingOutbound(&landingegress.Outbound{Name: "WARP", Type: landingegress.TypeDirect}); err == nil {
				t.Fatal("duplicate outbound name accepted")
			}
			if _, err := r.SaveLandingOutbound(&landingegress.Outbound{ID: warp.ID, Name: "WARP", Type: landingegress.TypeDirect}); err == nil {
				t.Fatal("outbound type change accepted")
			}
			if _, err := r.SaveLandingOutbound(&landingegress.Outbound{Name: "loop", Type: landingegress.TypeLandingNode, Settings: landingegress.Settings{LandingNodeID: 99999}}); err == nil {
				t.Fatal("missing upstream node accepted")
			}
			if _, err := r.SaveLandingOutbound(&landingegress.Outbound{Name: "via", Type: landingegress.TypeLandingNode, Settings: landingegress.Settings{LandingNodeID: n.ID, ViaForwardID: 777}}); err == nil {
				t.Fatal("unrelated rule accepted as upstream path")
			}
			up, err := r.SaveLandingOutbound(&landingegress.Outbound{Name: "upstream", Type: landingegress.TypeLandingNode, Settings: landingegress.Settings{LandingNodeID: n.ID}})
			if err != nil {
				t.Fatal(err)
			}
			p := landingegress.Profile{Name: "unlock", DefaultAction: "outbound:" + itoa(warp.ID), Rules: []landingegress.Rule{
				{Name: "BT", Enabled: true, Protocols: []string{"bittorrent"}, Action: "block"},
				{Name: "only this node", Enabled: true, Ports: "25", LandingNodeIDs: []int64{n.ID}, Action: "outbound:" + itoa(up.ID)},
			}}
			saved, err := r.SaveLandingRouteProfile(&p)
			if err != nil {
				t.Fatal(err)
			}
			bad := landingegress.Profile{Name: "ghost", Rules: []landingegress.Rule{{Enabled: true, Ports: "25", LandingNodeIDs: []int64{424242}, Action: "block"}}}
			if _, err := r.SaveLandingRouteProfile(&bad); err == nil {
				t.Fatal("deleted landing node scope accepted")
			}
			outs, profiles, err := r.LandingEgressCatalog()
			if err != nil || len(outs) != 2 || profiles[saved.ID].Rules[1].LandingNodeIDs[0] != n.ID {
				t.Fatalf("catalog: %v %+v", err, profiles)
			}
			if err := r.DeleteLandingOutbound(warp.ID); err == nil || !strings.Contains(err.Error(), "unlock") {
				t.Fatalf("outbound in use deleted: %v", err)
			}

			// Assignment and the admin confirmation flag.
			if err := r.SetLandingServerEgress(s.ID, saved.ID, true); err != nil {
				t.Fatal(err)
			}
			if err := r.DeleteLandingRouteProfile(saved.ID); err == nil {
				t.Fatal("profile in use by a server deleted")
			}
			// SaveLandingServer (the settings modal) must not touch egress fields.
			current, _ := r.GetLandingServer(s.ID)
			current.RouteProfileID, current.EgressState, current.EgressRestartOK = 0, "forged", false
			if err := r.SaveLandingServer(current, []int64{701}); err != nil {
				t.Fatal(err)
			}
			after, _ := r.GetLandingServer(s.ID)
			if after.RouteProfileID != saved.ID || after.EgressState == "forged" || !after.EgressRestartOK {
				t.Fatalf("settings save changed egress state: %+v", after)
			}
			usage, err := r.LandingProfileUsage()
			if err != nil || usage[saved.ID][0] != 1 {
				t.Fatalf("usage %v %v", usage, err)
			}
			servers, err := r.LandingServersUsingProfiles([]int64{saved.ID})
			if err != nil || len(servers) != 1 || servers[0] != s.ID {
				t.Fatalf("servers using profile: %v %v", servers, err)
			}

			// Node override, then the server back to default: the node keeps it.
			if err := r.SetLandingServerEgress(s.ID, 0, false); err != nil {
				t.Fatal(err)
			}
			pid := saved.ID
			if err := r.SetLandingNodeRouteProfile(n.ID, &pid); err != nil {
				t.Fatal(err)
			}
			if err := r.DeleteLandingRouteProfile(saved.ID); err == nil {
				t.Fatal("profile in use by a node deleted")
			}
			d, err := r.LandingEgressData(s.ID)
			if err != nil || len(d.Nodes) != 1 || d.Nodes[0].RouteProfileID == nil || *d.Nodes[0].RouteProfileID != saved.ID || d.NodeEgress != 0 {
				t.Fatalf("egress data: %+v %v", d, err)
			}
			if _, ok := d.Upstreams[up.ID]; !ok {
				t.Fatal("upstream used by the node override not resolved")
			}
			if u := d.Upstreams[up.ID]; u.ServerID != s.ID || u.Port != n.Port {
				t.Fatalf("upstream = %+v", u)
			}

			// Revision bumps exactly when the compiled plan changes.
			before, _ := r.GetLandingServer(s.ID)
			rev, err := r.TouchLandingEgress(s.ID, "")
			if err != nil || rev != before.Revision {
				t.Fatalf("unchanged empty plan bumped revision: %d→%d %v", before.Revision, rev, err)
			}
			rev, _ = r.TouchLandingEgress(s.ID, "abc")
			again, _ := r.TouchLandingEgress(s.ID, "abc")
			if rev != before.Revision+1 || again != rev {
				t.Fatalf("plan hash revisions %d %d (base %d)", rev, again, before.Revision)
			}
			if err := r.RecordLandingEgressStatus(s.ID, "applied", "", true, []string{"w"}); err != nil {
				t.Fatal(err)
			}
			st, _ := r.GetLandingServer(s.ID)
			if st.EgressState != "applied" || !st.EgressReady || st.EgressWarnings != `["w"]` {
				t.Fatalf("status = %+v", st)
			}

			// Capability report: one conditional statement.
			if err := r.SetNodeLandingCapabilities(601, true, 1); err != nil {
				t.Fatal(err)
			}
			node, _ := r.GetNodeByID(601)
			if node.LandingEgress != 1 || !node.LandingCapable {
				t.Fatalf("capabilities = %+v", node)
			}

			// WARP account upsert and backoff.
			if err := r.RecordLandingWarpFailure(s.ID, warp.ID, "unreachable"); err != nil {
				t.Fatal(err)
			}
			a, _ := r.GetLandingWarpAccount(s.ID, warp.ID)
			if a == nil || a.Status != "error" || a.NextAttemptAt == 0 || a.Failures != 1 {
				t.Fatalf("failure = %+v", a)
			}
			ok := model.LandingWarpAccount{ID: a.ID, LandingServerID: s.ID, OutboundID: warp.ID, DeviceID: "d", Token: "t", PrivateKey: "k", PeerPublicKey: "p", AddressV4: "172.16.0.2", Status: "ok", CreatedTime: a.CreatedTime}
			if err := r.SaveLandingWarpAccount(&ok); err != nil {
				t.Fatal(err)
			}
			a, _ = r.GetLandingWarpAccount(s.ID, warp.ID)
			if a.Status != "ok" || a.PrivateKey != "k" || a.Failures != 0 {
				t.Fatalf("upsert = %+v", a)
			}
			all, _ := r.ListLandingWarpAccounts()
			if len(all) != 1 {
				t.Fatalf("duplicate WARP rows: %d", len(all))
			}

			// Whitelist and exemption sources.
			list, err := r.SaveLandingSourceWhitelist([]string{"192.0.2.9", "192.0.2.0/24"})
			if err != nil || len(list) != 2 {
				t.Fatalf("whitelist %v %v", list, err)
			}
			got, _ := r.GetLandingSourceWhitelist()
			if strings.Join(got, ",") != "192.0.2.0/24,192.0.2.9/32" {
				t.Fatalf("stored whitelist %v", got)
			}
			hosts, err := r.LandingExemptHosts()
			if err != nil || !containsString(hosts, "203.0.113.1") || !containsString(hosts, "2001:db8::1") || !containsString(hosts, "203.0.113.2") {
				t.Fatalf("exempt hosts %v %v", hosts, err)
			}

			// Upstream loop graph: a node override chaining its own server.
			edges, err := r.LandingUpstreamEdges(0, nil, nil, nil, nil)
			if err != nil || len(edges[s.ID]) != 1 || edges[s.ID][0] != s.ID {
				t.Fatalf("edges %v %v", edges, err)
			}
			if err := r.SetLandingNodeRouteProfile(n.ID, nil); err != nil {
				t.Fatal(err)
			}
			if err := r.DeleteLandingRouteProfile(saved.ID); err != nil {
				t.Fatal(err)
			}
			if err := r.DeleteLandingOutbound(warp.ID); err != nil {
				t.Fatal(err)
			}
			if a, _ := r.GetLandingWarpAccount(s.ID, warp.ID); a != nil {
				t.Fatal("WARP account survived its template")
			}
		})
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
