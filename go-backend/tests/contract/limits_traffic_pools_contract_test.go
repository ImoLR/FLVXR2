package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 157 over HTTP: admin saves a 共享流量池 (entry + landing of one rule),
// encrypted agent uploads count the rule + its landing once in the pool, the
// exhausted pool pauses the rule (resume refused), the user sees the pool on
// /user/package, and a raised limit lifts the block.
func TestTrafficPoolContract(t *testing.T) {
	const gb = int64(1 << 30)
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			rows := []interface{}{
				&model.User{ID: 501, User: "pool-user", Pwd: "x", RoleID: 1, Status: 1, Flow: 99999, Num: 10, ExpTime: now + 86400000, FlowResetTime: 1},
				&model.Node{ID: 601, Name: "JP-landing", Secret: "pool-landing-secret", ServerIP: "203.0.113.1", Port: "10000-19999"},
				&model.Node{ID: 602, Name: "HK-entry", Secret: "pool-entry-secret", ServerIP: "203.0.113.2", Port: "10000-19999"},
				&model.Tunnel{ID: 801, Name: "HK-JP", Status: 1, Type: 2, Flow: 2, TrafficRatio: 1},
				&model.UserTunnel{ID: 701, UserID: 501, TunnelID: 801, Status: 1, Flow: 99999, ExpTime: now + 86400000},
				&model.Forward{ID: 901, UserID: 501, UserName: "pool-user", Name: "rule", TunnelID: 801, Status: 1, LandingNodeID: 1, RemoteAddr: "203.0.113.1:30001"},
				&model.ForwardPort{ForwardID: 901, NodeID: 602, Port: 12000},
				// A plain rule on the same path (no landing node).
				&model.Forward{ID: 902, UserID: 501, UserName: "pool-user", Name: "plain", TunnelID: 801, Status: 1, RemoteAddr: "198.51.100.9:443"},
				&model.ForwardPort{ForwardID: 902, NodeID: 602, Port: 12001},
				&model.ChainTunnel{TunnelID: 801, NodeID: 602, ChainType: "1"},
				&model.ChainTunnel{TunnelID: 801, NodeID: 601, ChainType: "3"},
				&model.LandingServer{ID: 1, Source: "native", NodeID: 601, Name: "JP", PublicHost: "203.0.113.1", FlowType: 2, TrafficRatio: 1, Status: "active"},
				&model.LandingNode{ID: 1, UserID: 501, LandingServerID: 1, Name: "jp", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-1", Status: "active"},
				&model.LandingServerUser{LandingServerID: 1, UserID: 501, CreatedTime: now},
			}
			for _, row := range rows {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			h := handler.New(r, "pool-contract", "test")
			router := httpserver.NewRouter(h, "pool-contract", r)
			admin, _ := auth.GenerateToken(1, "admin", 0, "pool-contract")
			user, _ := auth.GenerateToken(501, "pool-user", 1, "pool-contract")
			call := func(path, tok string, body interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/"+path, bytes.NewReader(raw))
				req.Header.Set("Authorization", tok)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err, w.Body.String())
				}
				return out
			}
			upload := func(secret string, body interface{}) {
				t.Helper()
				plain, _ := json.Marshal(body)
				cipher, err := security.NewAESCrypto(secret)
				if err != nil {
					t.Fatal(err)
				}
				encrypted, err := cipher.Encrypt(plain)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(map[string]interface{}{"encrypted": true, "data": encrypted})
				req := httptest.NewRequest(http.MethodPost, "/flow/upload?secret="+secret, bytes.NewReader(raw))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != 200 || w.Body.String() != "ok" {
					t.Fatalf("flow upload %d %s", w.Code, w.Body.String())
				}
			}
			decode := func(v interface{}, into interface{}) {
				raw, _ := json.Marshal(v)
				if err := json.Unmarshal(raw, into); err != nil {
					t.Fatal(err)
				}
			}

			got := call("landing/user/get", admin, map[string]int64{"userId": 501})
			var view struct {
				RelatedServers []repo.RelatedServer   `json:"relatedServers"`
				Pools          []repo.TrafficPoolView `json:"pools"`
			}
			decode(got.Data, &view)
			if got.Code != 0 || len(view.RelatedServers) != 2 || !view.RelatedServers[0].Landing || view.RelatedServers[0].ServerRefID != 601 || !view.RelatedServers[1].Entry || view.RelatedServers[1].ServerRefID != 602 {
				t.Fatalf("related servers %+v %+v", got, view.RelatedServers)
			}
			pool := map[string]interface{}{"name": "日本线", "limitBytes": 3 * gb, "members": []map[string]interface{}{{"serverKind": "node", "serverRefId": 602}, {"serverKind": "node", "serverRefId": 601}}}
			if res := call("landing/user/save", admin, map[string]interface{}{"userId": 501, "maxNodesSource": "global", "quotas": []interface{}{}, "pools": []interface{}{pool}}); res.Code != 0 {
				t.Fatalf("save %+v", res)
			}
			if res := call("landing/user/save", user, map[string]interface{}{"userId": 501, "pools": []interface{}{}}); res.Code == 0 {
				t.Fatal("a user saved pools")
			}

			// Rule 2 GB billed at the entry + the same bytes at the landing Core.
			counter := map[string]interface{}{"id": 1, "inbound_tag": "flvxl-1", "epoch": "100", "up_bytes": gb / 4, "down_bytes": 3 * gb / 4}
			upload("pool-entry-secret", []map[string]interface{}{{"n": "901_501_701_tcp", "d": gb / 4, "u": 3 * gb / 4}})
			upload("pool-landing-secret", map[string]interface{}{"flows": []interface{}{}, "landing_counters": []interface{}{counter}})
			pools, err := r.UserTrafficPools(501, false, time.Now())
			if err != nil || len(pools) != 1 || pools[0].UsedIn+pools[0].UsedOut != 2*gb || pools[0].Paused {
				t.Fatalf("pool after one flow: %+v %v (want 2 GiB, once)", pools, err)
			}
			for _, id := range []int64{601, 602} {
				var q model.UserServerQuota
				if err := r.DB().First(&q, "user_id = ? AND server_kind = 'node' AND server_ref_id = ?", 501, id).Error; err != nil || q.UsedIn+q.UsedOut != 2*gb {
					t.Fatalf("server %d quota %+v %v", id, q, err)
				}
			}

			// A second flow exhausts the 3 GiB pool → the rule is paused.
			counter["up_bytes"], counter["down_bytes"] = gb/2, 3*gb/2
			upload("pool-entry-secret", []map[string]interface{}{{"n": "901_501_701_tcp", "d": gb / 4, "u": 3 * gb / 4}})
			upload("pool-landing-secret", map[string]interface{}{"flows": []interface{}{}, "landing_counters": []interface{}{counter}})
			deadline := time.Now().Add(10 * time.Second)
			for _, id := range []int64{901, 902} {
				for {
					var f model.Forward
					if err := r.DB().First(&f, id).Error; err != nil {
						t.Fatal(err)
					}
					if f.Status == 0 && f.LandingAutoPaused {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("rule %d not paused: %+v", id, f)
					}
					time.Sleep(50 * time.Millisecond)
				}
			}
			var ln model.LandingNode
			if err := r.DB().First(&ln, 1).Error; err != nil || ln.Status != "paused_server_quota" {
				t.Fatalf("landing node on a pool member: %+v %v", ln, err)
			}
			// The landing rule is refused by its paused landing node first (as with a
			// per-server quota); the plain rule names the pool.
			if res := call("forward/resume", user, map[string]int64{"id": 901}); res.Code == 0 || !strings.Contains(res.Msg, "落地节点已停用") {
				t.Fatalf("resume landing rule %+v", res)
			}
			if res := call("forward/resume", user, map[string]int64{"id": 902}); res.Code == 0 || !strings.Contains(res.Msg, "共享流量池已用完") {
				t.Fatalf("resume plain rule %+v", res)
			}
			own := call("user/package", user, map[string]interface{}{})
			var pkg struct {
				TrafficPools []repo.TrafficPoolView `json:"trafficPools"`
			}
			decode(own.Data, &pkg)
			if own.Code != 0 || len(pkg.TrafficPools) != 1 || !pkg.TrafficPools[0].Paused || pkg.TrafficPools[0].UsedIn+pkg.TrafficPools[0].UsedOut != 4*gb || len(pkg.TrafficPools[0].Members) != 0 {
				t.Fatalf("user view %+v", pkg)
			}

			// Raised limit: the pool is open again and resume is no longer refused for it.
			got = call("landing/user/get", admin, map[string]int64{"userId": 501})
			decode(got.Data, &view)
			pool["id"], pool["limitBytes"] = view.Pools[0].ID, 10*gb
			if res := call("landing/user/save", admin, map[string]interface{}{"userId": 501, "maxNodesSource": "global", "quotas": []interface{}{}, "pools": []interface{}{pool}}); res.Code != 0 {
				t.Fatalf("raise %+v", res)
			}
			if pools, _ = r.UserTrafficPools(501, false, time.Now()); pools[0].Paused || pools[0].UsedIn+pools[0].UsedOut != 4*gb {
				t.Fatalf("raised pool %+v", pools)
			}
			h.WaitFlowEnforce()
			if err := r.DB().First(&ln, 1).Error; err != nil || ln.Status != "active" {
				t.Fatalf("landing node after raise: %+v %v", ln, err)
			}
			for _, id := range []int64{901, 902} {
				if err := r.LandingForwardBlockReason(id); err != nil {
					t.Fatalf("rule %d still blocked: %v", id, err)
				}
			}
			h.WaitFlowEnforce()
		})
	}
}
