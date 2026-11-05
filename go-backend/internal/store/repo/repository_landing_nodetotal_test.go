package repo

import (
	"encoding/json"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 211: what one node's runtime gets for its 入站 limit + 节点总连接数.
func TestLandingSendLimits(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		inbound, total       int
		inboundMode, capable bool
		want                 LandingSentLimits
	}{
		{"no total: unchanged (inbound runtime)", 3, 0, true, false, LandingSentLimits{MaxConnections: 3, Inbound: true}},
		{"no total: unchanged (old runtime)", 3, 0, false, false, LandingSentLimits{MaxConnections: 3}},
		{"no total, no inbound: unchanged", 0, 0, true, true, LandingSentLimits{Inbound: true}},
		{"capable runtime gets both", 3, 400, true, true, LandingSentLimits{MaxConnections: 3, Inbound: true, MaxTotal: 400}},
		{"capable runtime, total only", 0, 400, true, true, LandingSentLimits{Inbound: true, MaxTotal: 400}},
		{"inbound runtime, total only → old field", 0, 400, true, false, LandingSentLimits{MaxConnections: 400}},
		{"inbound runtime, both → inbound, total pending", 3, 400, true, false, LandingSentLimits{MaxConnections: 3, Inbound: true, TotalPending: true}},
		{"old runtime counts in+out anyway → smaller", 500, 400, false, false, LandingSentLimits{MaxConnections: 400}},
		{"old runtime, total only", 0, 400, false, false, LandingSentLimits{MaxConnections: 400}},
		{"negative values are 0", -1, -5, true, false, LandingSentLimits{Inbound: true}},
	} {
		if got := LandingSendLimits(tt.inbound, tt.total, tt.inboundMode, tt.capable); got != tt.want {
			t.Fatalf("%s: got %+v want %+v", tt.name, got, tt.want)
		}
	}
}

// The one-shot migration (column missing): 400 everywhere, server totals 0,
// shared copies of the share cap in node / default 入站 limits → 0; anything
// else kept. A second start changes nothing.
func TestLandingNodeTotalMigration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			caps, _ := json.Marshal(landingcustom.Share{ID: 16, Status: "active", PortMin: 30000, PortMax: 30999, MaxConnections: 800, Protocols: []string{"ss2022"}})
			now := time.Now().UnixMilli()
			servers := []model.LandingServer{
				{ID: 1, Source: "native", NodeID: 21, Name: "Breadcloud JP", PublicHost: "87.76.190.35", MaxConnections: 0, DefaultMaxConnections: 0, Status: "active", CreatedTime: now, UpdatedTime: now},
				{ID: 6, Source: "shared", RemoteServerID: 16, Name: "PQS TW", PublicHost: "pqstw.example", MaxConnections: 800, DefaultMaxConnections: 800, SharedCaps: string(caps), Status: "active", CreatedTime: now, UpdatedTime: now},
				{ID: 8, Source: "native", NodeID: 22, Name: "Own total", PublicHost: "203.0.113.8", MaxConnections: 300, DefaultMaxConnections: 50, Status: "active", CreatedTime: now, UpdatedTime: now},
			}
			for i := range servers {
				if err := r.db.Create(&servers[i]).Error; err != nil {
					t.Fatal(err)
				}
			}
			nodes := []model.LandingNode{
				{ID: 101, UserID: 1, LandingServerID: 1, Name: "jp", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-101", Status: "active", MaxConnections: 7},
				{ID: 102, UserID: 1, LandingServerID: 6, Name: "tw copy", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-102", Status: "active", MaxConnections: 800},
				{ID: 103, UserID: 1, LandingServerID: 6, Name: "tw own", Protocol: "ss2022", Port: 30003, InboundTag: "flvxl-103", Status: "active", MaxConnections: 50},
				{ID: 104, UserID: 1, LandingServerID: 8, Name: "own", Protocol: "ss2022", Port: 30004, InboundTag: "flvxl-104", Status: "active", MaxConnections: 800},
			}
			for i := range nodes {
				if err := r.db.Create(&nodes[i]).Error; err != nil {
					t.Fatal(err)
				}
			}
			// An older panel's schema: no node total column, no server default.
			if err := r.db.Migrator().DropColumn(&model.LandingNode{}, "max_total_connections"); err != nil {
				t.Fatal(err)
			}
			if err := r.db.Migrator().DropColumn(&model.LandingServer{}, "default_max_total_connections"); err != nil {
				t.Fatal(err)
			}
			if err := autoMigrateAll(r.db); err != nil {
				t.Fatal(err)
			}
			got := map[int64]model.LandingServer{}
			var ss []model.LandingServer
			r.db.Find(&ss)
			for _, s := range ss {
				got[s.ID] = s
			}
			for _, id := range []int64{1, 6, 8} {
				if s := got[id]; s.MaxConnections != 0 || s.DefaultMaxTotalConnections != LandingDefaultNodeTotal {
					t.Fatalf("server %d: max %d default total %d", id, s.MaxConnections, s.DefaultMaxTotalConnections)
				}
			}
			if got[6].DefaultMaxConnections != 0 || got[8].DefaultMaxConnections != 50 || got[1].DefaultMaxConnections != 0 {
				t.Fatalf("default 入站: shared copy %d, native own %d", got[6].DefaultMaxConnections, got[8].DefaultMaxConnections)
			}
			wantInbound := map[int64]int{101: 7, 102: 0, 103: 50, 104: 800}
			var ns []model.LandingNode
			r.db.Order("id").Find(&ns)
			for _, n := range ns {
				if n.MaxTotalConnections != LandingDefaultNodeTotal || n.MaxConnections != wantInbound[n.ID] {
					t.Fatalf("node %d: inbound %d (want %d) total %d", n.ID, n.MaxConnections, wantInbound[n.ID], n.MaxTotalConnections)
				}
			}
			// Second start: nothing runs again.
			r.db.Model(&model.LandingNode{}).Where("id = 101").Update("max_total_connections", 123)
			r.db.Model(&model.LandingServer{}).Where("id = 8").Updates(map[string]any{"max_connections": 9, "default_max_total_connections": 0})
			if err := autoMigrateAll(r.db); err != nil {
				t.Fatal(err)
			}
			var n101 model.LandingNode
			var s8 model.LandingServer
			r.db.First(&n101, 101)
			r.db.First(&s8, 8)
			if n101.MaxTotalConnections != 123 || s8.MaxConnections != 9 || s8.DefaultMaxTotalConnections != 0 {
				t.Fatalf("migration ran twice: node %d server %d/%d", n101.MaxTotalConnections, s8.MaxConnections, s8.DefaultMaxTotalConnections)
			}
		})
	}
}

// Shared servers: the share cap is no longer copied (server total, node
// defaults, node limits); new shares default to 400; the desired payload
// carries the total the old way, both limits only with node_total v1.
func TestSharedLandingNodeTotal(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := extraProtocolFixture(t, dialect)
			if s.MaxConnections != 0 || s.DefaultMaxConnections != 0 || s.DefaultMaxTotalConnections != LandingDefaultNodeTotal || SharedLandingCaps(s).MaxConnections != 1000 {
				t.Fatalf("fresh share: total %d default 入站 %d default 总 %d caps %d", s.MaxConnections, s.DefaultMaxConnections, s.DefaultMaxTotalConnections, SharedLandingCaps(s).MaxConnections)
			}
			a, err := r.CreateLandingNode(501, s.ID, "a", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			b, err := r.CreateLandingNode(501, s.ID, "b", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			if a.MaxConnections != 0 || a.MaxTotalConnections != LandingDefaultNodeTotal {
				t.Fatalf("new shared node: 入站 %d 总 %d", a.MaxConnections, a.MaxTotalConnections)
			}
			b.MaxConnections = 5
			if err := r.UpdateLandingNode(b); err != nil {
				t.Fatal(err)
			}
			sync := func(share landingcustom.Share) map[string]landingcustom.Node {
				t.Helper()
				if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
					t.Fatal(err)
				}
				d, err := r.SharedLandingDesired(s.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				return desiredByID(d)
			}
			id := func(n *model.LandingNode) string { return jsonID(n.ID) }
			// Plain share (no user limits): every limit counts 入站+出站 → smaller one.
			plain := sync(extraProtocolShare(true))
			if n := plain[id(a)]; n.MaxConnections != 400 || n.ConnMode != "" || n.MaxTotalConnections != 0 {
				t.Fatalf("plain share, total only: %+v", n)
			}
			if n := plain[id(b)]; n.MaxConnections != 5 || n.ConnMode != "" || n.MaxTotalConnections != 0 {
				t.Fatalf("plain share, both: %+v", n)
			}
			if cur, _ := r.GetLandingNode(a.ID); cur.MaxConnections != 0 {
				t.Fatalf("sync copied the share cap into the node: %d", cur.MaxConnections)
			}
			// User limits (入站 nodes) without node_total: total travels the old way
			// when there is no 入站 limit, else it waits.
			ul := extraProtocolShare(true)
			ul.UserLimits = &landingcustom.ShareUserLimits{Versions: []int{1, 2}}
			inbound := sync(ul)
			if n := inbound[id(a)]; n.MaxConnections != 400 || n.ConnMode != "" || n.MaxTotalConnections != 0 {
				t.Fatalf("user-limits share, total only: %+v", n)
			}
			if n := inbound[id(b)]; n.MaxConnections != 5 || n.ConnMode != landingcustom.ConnModeInbound || n.MaxTotalConnections != 0 {
				t.Fatalf("user-limits share, both: %+v", n)
			}
			cur := reloadServer(t, r, s.ID)
			if !LandingSendLimits(5, 400, true, SharedNodeTotalSupported(cur)).TotalPending {
				t.Fatal("total not pending without node_total")
			}
			// node_total v1: both limits.
			nt := ul
			nt.NodeTotal = &landingcustom.ShareNodeTotal{Versions: []int{1}}
			both := sync(nt)
			if n := both[id(b)]; n.MaxConnections != 5 || n.ConnMode != landingcustom.ConnModeInbound || n.MaxTotalConnections != 400 {
				t.Fatalf("node_total share, both: %+v", n)
			}
			if n := both[id(a)]; n.MaxConnections != 0 || n.MaxTotalConnections != 400 {
				t.Fatalf("node_total share, total only: %+v", n)
			}
			if !SharedNodeTotalSupported(reloadServer(t, r, s.ID)) {
				t.Fatal("node_total capability not stored")
			}
			// Server save: the hidden server total is forced to 0; the new default
			// can be applied to every existing node.
			srv := reloadServer(t, r, s.ID)
			srv.MaxConnections, srv.DefaultMaxTotalConnections = 777, 250
			if err := r.SaveLandingServerOpts(srv, nil, LandingServerSaveOptions{ApplyNodeTotalToNodes: true}); err != nil {
				t.Fatal(err)
			}
			if saved := reloadServer(t, r, s.ID); saved.MaxConnections != 0 || saved.DefaultMaxTotalConnections != 250 {
				t.Fatalf("saved server: total %d default 总 %d", saved.MaxConnections, saved.DefaultMaxTotalConnections)
			}
			for _, n := range []*model.LandingNode{a, b} {
				if cur, _ := r.GetLandingNode(n.ID); cur.MaxTotalConnections != 250 {
					t.Fatalf("node %d not applied: %d", n.ID, cur.MaxTotalConnections)
				}
			}
			srv = reloadServer(t, r, s.ID)
			srv.DefaultMaxTotalConnections = 100
			if err := r.SaveLandingServerOpts(srv, nil, LandingServerSaveOptions{}); err != nil {
				t.Fatal(err)
			}
			if cur, _ := r.GetLandingNode(a.ID); cur.MaxTotalConnections != 250 {
				t.Fatalf("existing node changed without 应用到现有节点: %d", cur.MaxTotalConnections)
			}
			srv.DefaultMaxTotalConnections = -1
			if err := r.SaveLandingServerOpts(srv, nil, LandingServerSaveOptions{}); err == nil {
				t.Fatal("negative default accepted")
			}
		})
	}
}

func jsonID(id int64) string {
	raw, _ := json.Marshal(id)
	return string(raw)
}
