package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 157 共享流量池: real flow uploads through commitFlowItems /
// commitFlowWithLanding, on SQLite and (FLVX_POSTGRES_TEST_DSN) PostgreSQL.

func poolTestEnv(t *testing.T, dialect string) *flowTestEnv {
	t.Helper()
	r := landingMonitorRepo(t, dialect)
	return &flowTestEnv{t: t, r: r, h: &Handler{repo: r}, now: time.Now().UnixMilli()}
}

// addPoolUser works on PostgreSQL too ("user" is reserved there).
func (e *flowTestEnv) addPoolUser(id int64) {
	e.t.Helper()
	u := model.User{ID: id, User: fmt.Sprintf("pool-user-%d", id), Pwd: "x", RoleID: 1, Status: 1, ExpTime: e.now + 86400000, Flow: 99999, FlowResetTime: 1, Num: 10, CreatedTime: e.now}
	if err := e.r.DB().Create(&u).Error; err != nil {
		e.t.Fatal(err)
	}
}

func (e *flowTestEnv) savePools(userID int64, pools []repo.TrafficPoolInput) {
	e.t.Helper()
	if err := e.r.SaveLandingUserSettingFull(userID, repo.LandingMaxNodesFollowGlobal, nil, 0, nil, &pools); err != nil {
		e.t.Fatal(err)
	}
}

func (e *flowTestEnv) pools(userID int64) []repo.TrafficPoolView {
	e.t.Helper()
	v, err := e.r.UserTrafficPools(userID, true, time.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *flowTestEnv) serverQuotaUsed(userID int64, kind string, ref int64) int64 {
	e.t.Helper()
	var q model.UserServerQuota
	if err := e.r.DB().Where("user_id = ? AND server_kind = ? AND server_ref_id = ?", userID, kind, ref).Take(&q).Error; err != nil {
		e.t.Fatalf("quota %s:%d: %v", kind, ref, err)
	}
	return q.UsedIn + q.UsedOut
}

func poolNodes(ids ...int64) []repo.TrafficPoolServer {
	out := make([]repo.TrafficPoolServer, 0, len(ids))
	for _, id := range ids {
		out = append(out, repo.TrafficPoolServer{ServerKind: "node", ServerRefID: id})
	}
	return out
}

func TestTrafficPoolCountsPathOnce(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := poolTestEnv(t, dialect)
			e.addPoolUser(2)
			for id := int64(1); id <= 4; id++ {
				e.addNode(id, fmt.Sprintf("s%d", id))
			}
			e.addTunnel(1, 1, 1)
			e.addUserTunnel(10, 2, 1)
			e.addForward(20, 2, 1)
			// entry 1 → relay 2 → exit tier {3, 4}
			e.exec("INSERT INTO forward_port (forward_id,node_id,port) VALUES (20,1,11000)")
			e.exec("INSERT INTO chain_tunnel (tunnel_id,chain_type,node_id,inx) VALUES (1,'1',1,0),(1,'2',2,1),(1,'3',3,0),(1,'3',4,0)")
			e.savePools(2, []repo.TrafficPoolInput{{Name: "全线", LimitBytes: 1 << 40, Members: poolNodes(1, 2, 3)}, {Name: "备用出口", Members: poolNodes(4)}})
			for i := 0; i < 2; i++ {
				if _, err := e.h.commitFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 600, U: 400}}); err != nil {
					t.Fatal(err)
				}
			}
			p := e.pools(2)
			if p[0].UsedIn != 1200 || p[0].UsedOut != 800 {
				t.Fatalf("全线 must count each flow once: %+v", p[0])
			}
			if p[1].UsedIn != 600 || p[1].UsedOut != 400 {
				t.Fatalf("备用出口 = its half of the exit tier: %+v", p[1])
			}
			// Per-server quotas are unchanged by pools: every machine bills its part.
			sum := int64(0)
			for id, want := range map[int64]int64{1: 2000, 2: 2000, 3: 1000, 4: 1000} {
				got := e.serverQuotaUsed(2, "node", id)
				if got != want {
					t.Fatalf("server %d quota %d want %d", id, got, want)
				}
				if id <= 3 {
					sum += got
				}
			}
			if sum == p[0].UsedIn+p[0].UsedOut {
				t.Fatal("pool used must differ from the member sum here")
			}
			e.expectFlows("user", 2, 1200, 800)
		})
	}
}

// Same worked examples as TestLandingBillingWorkedExamples: entry node 1,
// landing server 1 on node 2 (native) or shared server 1.
func TestTrafficPoolLandingFlowCountedOnce(t *testing.T) {
	const gb int64 = 1 << 30
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, source := range []string{"native", "shared"} {
			for _, tc := range []struct {
				name           string
				tRatio, lRatio float64
				owner          int64
				rule           bool
				want           int64
			}{{"double ratio1 via rule", 1, 1, 2, true, 2 * gb}, {"direct", 1, 1, 2, false, 2 * gb}, {"premium tunnel", 3, 1, 2, true, 6 * gb}, {"premium landing", 1, 3, 2, true, 6 * gb}, {"other owner landing", 1, 1, 3, true, 2 * gb}} {
				for _, pool := range []string{"entry+landing", "entry", "landing"} {
					t.Run(dialect+"/"+source+"/"+tc.name+"/"+pool, func(t *testing.T) {
						if dialect == "postgres" && tc.name != "premium tunnel" && tc.name != "direct" {
							t.Skip("PostgreSQL: the two representative cases")
						}
						e := poolTestEnv(t, dialect)
						e.addPoolUser(2)
						if tc.owner != 2 {
							e.addPoolUser(tc.owner)
						}
						e.addNode(1, "entry-secret")
						e.addTunnel(1, 2, tc.tRatio)
						e.addUserTunnel(10, 2, 1)
						e.addForward(20, 2, 1)
						seedHandlerLanding(t, e, tc.owner, 2, tc.lRatio)
						landingKey := repo.TrafficPoolServer{ServerKind: "node", ServerRefID: 2}
						if source == "shared" {
							e.exec("UPDATE landing_server SET source='shared',node_id=0,remote_server_id=7 WHERE id=1")
							landingKey = repo.TrafficPoolServer{ServerKind: "landing", ServerRefID: 1}
						}
						e.exec("UPDATE forward SET landing_node_id=1 WHERE id=20")
						members := map[string][]repo.TrafficPoolServer{"entry+landing": {poolNodes(1)[0], landingKey}, "entry": poolNodes(1), "landing": {landingKey}}[pool]
						e.savePools(2, []repo.TrafficPoolInput{{Name: "p", Members: members}})
						var items []flowItem
						if tc.rule {
							items = []flowItem{{N: "20_2_10_tcp", D: gb / 4, U: gb * 3 / 4}}
						}
						counter := repo.LandingCounter{ID: 1, NodeID: 2, InboundTag: "flvxl-1", Epoch: "100", UpBytes: gb / 4, DownBytes: gb * 3 / 4}
						reporter := int64(2)
						if source == "shared" {
							reporter, counter.NodeID, counter.ServerID, counter.CollectedAt = 0, 0, 1, time.Now().UnixMilli()
						}
						if _, err := e.h.commitFlowItems(1, items); err != nil {
							t.Fatal(err)
						}
						if _, err := e.h.commitFlowWithLanding(reporter, nil, []repo.LandingCounter{counter}); err != nil {
							t.Fatal(err)
						}
						want := tc.want
						switch {
						case tc.owner != 2 && pool == "landing":
							want = 0 // the landing node is another user's
						case tc.owner == 2 && pool == "entry" && !tc.rule:
							want = 0 // direct landing use never touches the entry
						}
						p := e.pools(2)
						if got := p[0].UsedIn + p[0].UsedOut; got != want {
							t.Fatalf("pool used %d want %d (%+v)", got, want, p[0])
						}
						if tc.owner == 2 {
							if got := e.monthlyQuotaUsed(2); got != tc.want {
								t.Fatalf("user total %d want %d", got, tc.want)
							}
						}
					})
				}
			}
		}
	}
}

func TestTrafficPoolPausesLikeServerQuota(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := poolTestEnv(t, dialect)
			e.addPoolUser(2)
			e.addPoolUser(3)
			for id := int64(1); id <= 3; id++ {
				e.addNode(id, fmt.Sprintf("s%d", id))
			}
			e.addTunnel(1, 1, 1)
			e.addUserTunnel(10, 2, 1)
			e.addUserTunnel(11, 3, 1)
			e.addForward(20, 2, 1)
			e.addForward(21, 3, 1) // other user, same path: never paused by user 2's pool
			e.exec("INSERT INTO forward_port (forward_id,node_id,port) VALUES (20,1,11000),(21,1,11001)")
			e.exec("INSERT INTO chain_tunnel (tunnel_id,chain_type,node_id,inx) VALUES (1,'1',1,0),(1,'3',3,0)")
			// User 2's own native landing on node 3 (a pool member) and one on node 2 (not).
			for _, v := range []interface{}{
				&model.LandingServer{ID: 1, NodeID: 3, Source: "native", Name: "on-3", PublicHost: "203.0.113.3", FlowType: 1, TrafficRatio: 1, Status: "active"},
				&model.LandingServer{ID: 2, NodeID: 2, Source: "native", Name: "on-2", PublicHost: "203.0.113.2", FlowType: 1, TrafficRatio: 1, Status: "active"},
				&model.LandingNode{ID: 1, UserID: 2, LandingServerID: 1, Name: "a", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-1", Status: "active"},
				&model.LandingNode{ID: 2, UserID: 2, LandingServerID: 2, Name: "b", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-2", Status: "active"},
				&model.LandingServerUser{LandingServerID: 1, UserID: 2, CreatedTime: e.now},
				&model.LandingServerUser{LandingServerID: 2, UserID: 2, CreatedTime: e.now},
			} {
				if err := e.r.DB().Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}
			e.savePools(2, []repo.TrafficPoolInput{{Name: "日本", LimitBytes: 100, Members: poolNodes(3)}})
			landingStatus := func(id int64) string {
				n, err := e.r.GetLandingNode(id)
				if err != nil || n == nil {
					t.Fatal(err)
				}
				return n.Status
			}
			reconcile := func() repo.LandingReconcileResult {
				res, err := e.r.ReconcileLandingPolicy(time.Now())
				if err != nil {
					t.Fatal(err)
				}
				return res
			}
			if res := reconcile(); len(res.PauseForwardIDs) != 0 {
				t.Fatalf("nothing used yet: %+v", res)
			}
			if _, err := e.h.commitFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 150, U: 50}}); err != nil {
				t.Fatal(err)
			}
			if p := e.pools(2); !p[0].Paused || p[0].UsedIn+p[0].UsedOut != 200 {
				t.Fatalf("pool not exhausted: %+v", p[0])
			}
			res := reconcile()
			if len(res.PauseForwardIDs) != 1 || res.PauseForwardIDs[0] != 20 {
				t.Fatalf("pause %+v", res)
			}
			if e.forwardStatus(20) != 0 || e.forwardStatus(21) != 1 {
				t.Fatalf("status 20=%d 21=%d", e.forwardStatus(20), e.forwardStatus(21))
			}
			if landingStatus(1) != "paused_server_quota" || landingStatus(2) != "active" {
				t.Fatalf("landing on member %s, off member %s", landingStatus(1), landingStatus(2))
			}
			if err := e.r.LandingForwardBlockReason(20); err == nil || err.Error() != "共享流量池已用完" {
				t.Fatalf("block reason %v", err)
			}
			// The per-server quota of node 3 is not exhausted: the pool alone paused it.
			var q model.UserServerQuota
			e.r.DB().Where("user_id = 2 AND server_kind = 'node' AND server_ref_id = 3").Take(&q)
			if q.Paused {
				t.Fatalf("server quota paused: %+v", q)
			}
			// Raising the limit resumes.
			id := e.pools(2)[0].ID
			e.savePools(2, []repo.TrafficPoolInput{{ID: id, Name: "日本", LimitBytes: 1000, Members: poolNodes(3)}})
			res = reconcile()
			if len(res.ResumeForwardIDs) != 1 || res.ResumeForwardIDs[0] != 20 || landingStatus(1) != "active" {
				t.Fatalf("raise limit resume %+v landing %s", res, landingStatus(1))
			}
			if err := e.r.CompleteLandingForwardResume(20); err != nil {
				t.Fatal(err)
			}
			if err := e.r.LandingForwardBlockReason(20); err != nil {
				t.Fatal(err)
			}
			// Back under the old limit → paused again; a new period resumes.
			e.savePools(2, []repo.TrafficPoolInput{{ID: id, Name: "日本", LimitBytes: 100, Members: poolNodes(3)}})
			if res = reconcile(); len(res.PauseForwardIDs) != 1 {
				t.Fatalf("lowered limit %+v", res)
			}
			e.exec("UPDATE user_traffic_pool SET period_key = 20000101 WHERE id = ?", id)
			res = reconcile()
			if len(res.ResumeForwardIDs) != 1 || landingStatus(1) != "active" {
				t.Fatalf("period roll %+v", res)
			}
			if p := e.pools(2); p[0].Paused || p[0].UsedIn+p[0].UsedOut != 0 {
				t.Fatalf("period roll pool %+v", p[0])
			}
			if err := e.r.CompleteLandingForwardResume(20); err != nil {
				t.Fatal(err)
			}
			// Removing the member resumes too; limit 0 never pauses.
			if _, err := e.h.commitFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 150, U: 50}}); err != nil {
				t.Fatal(err)
			}
			if res = reconcile(); len(res.PauseForwardIDs) != 1 {
				t.Fatalf("pause again %+v", res)
			}
			e.savePools(2, []repo.TrafficPoolInput{{ID: id, Name: "日本", LimitBytes: 100, Members: poolNodes(2)}})
			if res = reconcile(); len(res.ResumeForwardIDs) != 1 || landingStatus(1) != "active" || landingStatus(2) != "paused_server_quota" {
				t.Fatalf("member moved %+v landing %s/%s", res, landingStatus(1), landingStatus(2))
			}
			e.savePools(2, []repo.TrafficPoolInput{{ID: id, Name: "日本", LimitBytes: 0, Members: poolNodes(2, 3)}})
			reconcile()
			if landingStatus(1) != "active" || landingStatus(2) != "active" {
				t.Fatal("limit 0 must not pause")
			}
		})
	}
}

func poolHTTP(t *testing.T, fn http.HandlerFunc, path, body string, userID int64, role int) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: fmt.Sprint(userID), RoleID: role}))
	w := httptest.NewRecorder()
	fn(w, req)
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s: %s", path, w.Body.String())
	}
	return out
}

func TestTrafficPoolHTTP(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := poolTestEnv(t, dialect)
			e.addPoolUser(2)
			// User 1 = the seeded admin of a fresh install.
			for id := int64(1); id <= 3; id++ {
				e.addNode(id, fmt.Sprintf("s%d", id))
			}
			e.addTunnel(1, 1, 1)
			e.addUserTunnel(10, 2, 1)
			e.exec("INSERT INTO chain_tunnel (tunnel_id,chain_type,node_id,inx) VALUES (1,'1',1,0),(1,'3',3,0)")
			if err := e.r.DB().Create(&model.LandingServer{ID: 1, NodeID: 2, Source: "native", Name: "landing-2", PublicHost: "203.0.113.2", Status: "active"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := e.r.DB().Create(&model.LandingServerUser{LandingServerID: 1, UserID: 2, CreatedTime: e.now}).Error; err != nil {
				t.Fatal(err)
			}
			get := func() map[string]interface{} {
				out := poolHTTP(t, e.h.landingUserGet, "/api/v1/landing/user/get", `{"userId":2}`, 1, 0)
				if out["code"].(float64) != 0 {
					t.Fatalf("get %+v", out)
				}
				return out["data"].(map[string]interface{})
			}
			data := get()
			var related []repo.RelatedServer
			raw, _ := json.Marshal(data["relatedServers"])
			_ = json.Unmarshal(raw, &related)
			if len(related) != 2 || related[0].ServerRefID != 1 || !related[0].Entry || related[0].Landing || related[1].ServerRefID != 2 || !related[1].Landing || related[1].Entry || related[1].ServerName != "n" {
				t.Fatalf("related %s", raw)
			}
			if pools, _ := json.Marshal(data["pools"]); string(pools) != "[]" {
				t.Fatalf("pools %s", pools)
			}
			save := func(body string) map[string]interface{} {
				return poolHTTP(t, e.h.landingUserSave, "/api/v1/landing/user/save", body, 1, 0)
			}
			if out := save(`{"userId":2,"maxNodesSource":"global","quotas":[],"pools":[{"name":"亚洲","limitBytes":1073741824,"members":[{"serverKind":"node","serverRefId":1},{"serverKind":"node","serverRefId":2}]},{"name":"b","members":[{"serverKind":"node","serverRefId":2}]}]}`); out["code"].(float64) == 0 || !strings.Contains(fmt.Sprint(out["msg"]), "一个流量池") {
				t.Fatalf("duplicate member accepted %+v", out)
			}
			if out := save(`{"userId":2,"maxNodesSource":"global","quotas":[],"pools":[{"name":"亚洲","limitBytes":1073741824,"members":[{"serverKind":"node","serverRefId":1},{"serverKind":"node","serverRefId":2}]}]}`); out["code"].(float64) != 0 {
				t.Fatalf("save %+v", out)
			}
			// Older clients omit pools: kept.
			if out := save(`{"userId":2,"maxNodesSource":"global","quotas":[]}`); out["code"].(float64) != 0 {
				t.Fatalf("save %+v", out)
			}
			var pools []repo.TrafficPoolView
			raw, _ = json.Marshal(get()["pools"])
			_ = json.Unmarshal(raw, &pools)
			if len(pools) != 1 || pools[0].Name != "亚洲" || pools[0].LimitBytes != 1<<30 || len(pools[0].Members) != 2 || pools[0].Members[1].ServerRefID != 2 || pools[0].Members[1].ServerName != "n" {
				t.Fatalf("pools %s", raw)
			}
			// Non-admins cannot read or write another user's pools.
			if out := poolHTTP(t, e.h.landingUserGet, "/api/v1/landing/user/get", `{"userId":2}`, 2, 1); out["code"].(float64) == 0 {
				t.Fatal("user read admin view")
			}
			// The user's own read-only view on 主页 (/user/package).
			out := poolHTTP(t, e.h.userPackage, "/api/v1/user/package", `{}`, 2, 1)
			own, _ := json.Marshal(out["data"].(map[string]interface{})["trafficPools"])
			if !strings.Contains(string(own), `"name":"亚洲"`) || !strings.Contains(string(own), `"limitBytes":1073741824`) || strings.Contains(string(own), "serverRefId") {
				t.Fatalf("user view %s", own)
			}
		})
	}
}
