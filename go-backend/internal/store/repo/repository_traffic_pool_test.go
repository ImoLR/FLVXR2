package repo

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

func TestPoolFlowShareCountsOncePerPool(t *testing.T) {
	node := func(ids ...int64) map[ServerQuotaKey]bool {
		m := map[ServerQuotaKey]bool{}
		for _, id := range ids {
			m[ServerQuotaKey{UserID: 2, Kind: "node", RefID: id}] = true
		}
		return m
	}
	// entry 1 → relay 2 → exit tier {3,4} (load balanced).
	tiers := [][]int64{{1}, {2}, {3, 4}}
	flow := PoolFlow{UserID: 2, Tiers: tiers, In: 601, Out: 400}
	for _, tc := range []struct {
		name    string
		members map[ServerQuotaKey]bool
		in, out int64
	}{
		{"whole path once", node(1, 2, 3, 4), 601, 400},
		{"entry only", node(1), 601, 400},
		{"relay + entry once", node(1, 2), 601, 400},
		{"one exit of two = its share", node(3), 301, 200},
		{"other exit gets the remainder-free half", node(4), 300, 200},
		{"both exits = whole flow", node(3, 4), 601, 400},
		{"unrelated server", node(9), 0, 0},
		{"other user's key never matches", map[ServerQuotaKey]bool{{UserID: 3, Kind: "node", RefID: 1}: true}, 0, 0},
	} {
		got := poolFlowShare(flow, tc.members)
		if got.In != tc.in || got.Out != tc.out {
			t.Fatalf("%s: got %+v want (%d,%d)", tc.name, got, tc.in, tc.out)
		}
	}
	landing := ServerQuotaKey{UserID: 2, Kind: "node", RefID: 3}
	// Rule into the user's own landing on node 3: the pool holding node 3 gets
	// only the premium (the Core counter adds the landing bill).
	withLanding := PoolFlow{UserID: 2, Tiers: [][]int64{{1}, {3}}, In: 900, Out: 300, Landing: landing, LandingIn: 100, LandingOut: 50}
	if got := poolFlowShare(withLanding, node(1, 3)); got.In != 100 || got.Out != 50 {
		t.Fatalf("landing member: %+v", got)
	}
	if got := poolFlowShare(withLanding, node(1)); got.In != 900 || got.Out != 300 {
		t.Fatalf("entry only with landing: %+v", got)
	}
	counter := PoolFlow{UserID: 2, Landing: landing, LandingIn: 7, LandingOut: 9}
	if got := poolFlowShare(counter, node(3)); got.In != 7 || got.Out != 9 {
		t.Fatalf("counter: %+v", got)
	}
	if got := poolFlowShare(counter, node(1)); got.In != 0 || got.Out != 0 {
		t.Fatalf("counter outside pool: %+v", got)
	}
}

func poolRepoFixture(t *testing.T, dialect string) *Repository {
	t.Helper()
	r := orphanTestRepo(t, dialect)
	now := time.Now().UnixMilli()
	future := now + 86400000
	orphanSeed(t, r,
		&model.User{ID: 2, User: "alice", Pwd: "x", RoleID: 1, Status: 1, ExpTime: future, Flow: 99999, FlowResetTime: 1, CreatedTime: now},
		&model.User{ID: 3, User: "bob", Pwd: "x", RoleID: 1, Status: 1, ExpTime: future, Flow: 99999, FlowResetTime: 1, CreatedTime: now},
		&model.User{ID: 9, User: "root", Pwd: "x", RoleID: 0, Status: 1, Flow: 99999, CreatedTime: now},
	)
	for id, name := range map[int64]string{1: "HK-entry", 2: "SG-relay", 3: "JP-exit", 4: "TW-entry", 5: "US-landing", 6: "unrelated"} {
		orphanSeed(t, r, &model.Node{ID: id, Name: name, Secret: name, ServerIP: "127.0.0.1", Port: "1000-2000", CreatedTime: now, Status: 1})
	}
	return r
}

func TestTrafficPoolSaveRules(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			orphanSeed(t, r, &model.LandingServer{ID: 7, Source: "shared", RemoteServerID: 16, Name: "PQS-TW", Status: "active"})
			save := func(userID int64, pools []TrafficPoolInput) error {
				return r.SaveLandingUserSettingFull(userID, LandingMaxNodesFollowGlobal, nil, 0, nil, &pools)
			}
			srv := func(kind string, id int64) TrafficPoolServer { return TrafficPoolServer{ServerKind: kind, ServerRefID: id} }
			if err := save(2, []TrafficPoolInput{{Name: "亚洲", LimitBytes: 100 << 30, Members: []TrafficPoolServer{srv("node", 1), srv("node", 3), srv("landing", 7)}}}); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				pools []TrafficPoolInput
				err   string
			}{
				{[]TrafficPoolInput{{Name: "a", Members: []TrafficPoolServer{srv("node", 1)}}, {Name: "b", Members: []TrafficPoolServer{srv("node", 1)}}}, "只能加入该用户的一个流量池"},
				{[]TrafficPoolInput{{Name: "a", Members: []TrafficPoolServer{srv("node", 1)}}, {Name: "a", Members: []TrafficPoolServer{srv("node", 2)}}}, "名称重复"},
				{[]TrafficPoolInput{{Name: " ", Members: []TrafficPoolServer{srv("node", 1)}}}, "请填写流量池名称"},
				{[]TrafficPoolInput{{Name: "a"}}, "至少要选一台服务器"},
				{[]TrafficPoolInput{{Name: "a", Members: []TrafficPoolServer{srv("node", 99)}}}, "服务器不存在"},
				{[]TrafficPoolInput{{Name: "a", Members: []TrafficPoolServer{srv("landing", 99)}}}, "共享服务器不存在"},
				{[]TrafficPoolInput{{Name: "a", LimitBytes: -1, Members: []TrafficPoolServer{srv("node", 1)}}}, "限额无效"},
				{[]TrafficPoolInput{{ID: 12345, Name: "a", Members: []TrafficPoolServer{srv("node", 1)}}}, "流量池不存在"},
				{[]TrafficPoolInput{{Name: strings.Repeat("长", TrafficPoolNameMaxRunes+1), Members: []TrafficPoolServer{srv("node", 1)}}}, "最多"},
			} {
				if err := save(2, tc.pools); err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("want %q got %v", tc.err, err)
				}
			}
			pools, err := r.UserTrafficPools(2, true, time.Now())
			if err != nil || len(pools) != 1 || len(pools[0].Members) != 3 {
				t.Fatalf("failed saves must not change pools: %+v %v", pools, err)
			}
			// The DB itself refuses a second pool row for the same server.
			if err := r.db.Create(&model.UserTrafficPoolMember{PoolID: pools[0].ID + 1, UserID: 2, ServerKind: "node", ServerRefID: 1}).Error; err == nil {
				t.Fatal("unique index missing")
			}
			// Another user may pool the same server.
			if err := save(3, []TrafficPoolInput{{Name: "亚洲", Members: []TrafficPoolServer{srv("node", 1)}}}); err != nil {
				t.Fatal(err)
			}
			// Update keeps usage; members change; a dropped pool is deleted.
			r.db.Model(&model.UserTrafficPool{}).Where("id = ?", pools[0].ID).Updates(map[string]interface{}{"used_in": 5, "used_out": 6, "period_key": LandingPeriodKey(1, time.Now())})
			if err := save(2, []TrafficPoolInput{{ID: pools[0].ID, Name: "亚洲线路", LimitBytes: 1, Members: []TrafficPoolServer{srv("node", 3)}}, {Name: "台湾", Members: []TrafficPoolServer{srv("node", 1), srv("node", 4)}}}); err != nil {
				t.Fatal(err)
			}
			pools, _ = r.UserTrafficPools(2, true, time.Now())
			if len(pools) != 2 || pools[0].Name != "亚洲线路" || pools[0].UsedIn != 5 || pools[0].UsedOut != 6 || len(pools[0].Members) != 1 || pools[0].Members[0].ServerName != "JP-exit" || len(pools[1].Members) != 2 {
				t.Fatalf("update: %+v", pools)
			}
			// nil pools (older client) keeps them.
			if err := r.SaveLandingUserSettingFull(2, LandingMaxNodesFollowGlobal, nil, 0, nil, nil); err != nil {
				t.Fatal(err)
			}
			if pools, _ = r.UserTrafficPools(2, false, time.Now()); len(pools) != 2 {
				t.Fatalf("nil pools must keep: %+v", pools)
			}
			// An elapsed period reads 0 used.
			r.db.Model(&model.UserTrafficPool{}).Where("user_id = 2").Update("period_key", 20000101)
			if pools, _ = r.UserTrafficPools(2, false, time.Now()); pools[0].UsedIn != 0 || pools[0].Paused {
				t.Fatalf("old period: %+v", pools[0])
			}
			if err := save(2, []TrafficPoolInput{}); err != nil {
				t.Fatal(err)
			}
			var rows int64
			r.db.Model(&model.UserTrafficPoolMember{}).Where("user_id = 2").Count(&rows)
			if pools, _ = r.UserTrafficPools(2, true, time.Now()); len(pools) != 0 || rows != 0 {
				t.Fatalf("delete all: %+v members %d", pools, rows)
			}
			// Node delete removes it from pools; user delete removes the pools.
			if err := r.DeleteNodeCascade(1); err != nil {
				t.Fatal(err)
			}
			r.db.Model(&model.UserTrafficPoolMember{}).Where("user_id = 3").Count(&rows)
			if rows != 0 {
				t.Fatalf("node delete left %d members", rows)
			}
		})
	}
}

func TestUserRelatedServers(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			now := time.Now().UnixMilli()
			exec := func(sql string, args ...interface{}) {
				t.Helper()
				if err := r.db.Exec(sql, args...).Error; err != nil {
					t.Fatalf("%s: %v", sql, err)
				}
			}
			exec(`INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, inx) VALUES (1,'t1',1,2,'tls',1,?,?,1,0),(2,'t2',1,1,'tls',1,?,?,1,0)`, now, now, now, now)
			// t1: entry 1 → relay 2 → exit 3 (relay/exit are not candidates); t2: entry 4, not granted.
			exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx) VALUES (1,'1',1,0),(1,'2',2,1),(1,'3',3,0),(2,'1',4,0)`)
			exec(`INSERT INTO user_tunnel(id, user_id, tunnel_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status) VALUES (10,2,1,10,99999,0,0,1,?,1)`, now+86400000)
			orphanSeed(t, r,
				&model.LandingServer{ID: 5, NodeID: 5, Source: "native", Name: "US-native", Status: "active"},
				&model.LandingServer{ID: 7, Source: "shared", RemoteServerID: 16, Name: "PQS-TW", Status: "active"},
				&model.LandingServer{ID: 8, Source: "shared", RemoteServerID: 17, Name: "not-granted", Status: "active"},
				&model.LandingServer{ID: 9, NodeID: 3, Source: "native", Name: "chain-only", Status: "active", ChainOnly: true},
				&model.LandingServerUser{LandingServerID: 5, UserID: 2, CreatedTime: now},
			)
			// Shared 7 via a node the user owns (no grant).
			orphanSeed(t, r, &model.LandingNode{ID: 1, UserID: 2, LandingServerID: 7, Name: "n", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-1", Status: "active"})
			got, err := r.UserRelatedServers(2)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"node:1:entry:HK-entry", "node:5:landing:US-landing", "landing:7:landing:PQS-TW"}
			if len(got) != len(want) {
				t.Fatalf("related %+v", got)
			}
			for i, s := range got {
				role := "entry"
				if s.Landing {
					role = "landing"
				}
				if key := s.ServerKind + ":" + fmt.Sprint(s.ServerRefID) + ":" + role + ":" + s.ServerName; key != want[i] || (s.Entry && s.Landing) {
					t.Fatalf("related[%d] = %s %+v", i, key, s)
				}
			}
			// A rule's entry node counts as 入口 too (e.g. entry-bound rules).
			exec(`INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx) VALUES (20,2,'u','f',2,'1.1.1.1:443','fifo',0,0,?,?,1,0)`, now, now)
			exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES (20,4,11000)`)
			got, _ = r.UserRelatedServers(2)
			if len(got) != 4 || got[1].ServerRefID != 4 || !got[1].Entry {
				t.Fatalf("rule entry: %+v", got)
			}
			// Admin: every entry + every non-chain-only landing server.
			got, _ = r.UserRelatedServers(9)
			if len(got) != 5 {
				t.Fatalf("admin related %+v", got)
			}
		})
	}
}
