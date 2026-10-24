package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Admin server-list fields before plan 132 (fork.71): the admin view must keep
// exactly this field set.
// Plan 141 added connMode / maxConnMode / connModeSupported / connectionsMode /
// maxConnectionsMode (admin only; users get none of them); plan 147
// connModeFixed (true on shared servers: no 计算方式 choice).
var landingAdminServerKeysNative = []string{"anytlsServerName", "availableUntil", "billedBytes", "chainNodeCount", "chainOnly", "connModeFixed", "connModeSupported", "connMode", "connections", "connectionsMode", "coreCpuPercent", "coreMemoryMb", "createdTime", "defaultExpiresAt", "defaultExpiryDays", "defaultExpiryMode", "defaultMaxConnections", "defaultMaxOnlineIps", "defaultTrafficLimitBytes", "defaultUserQuotaBytes", "directUserCount", "egress", "egressCheckedAt", "egressError", "egressLabel", "egressReady", "egressRestartOk", "egressState", "flowType", "id", "landingSupported", "maxConnMode", "maxConnections", "maxConnectionsMode", "maxNodesPerUser", "mieruTransport", "name", "nodeCount", "nodeId", "online", "portMax", "portMin", "protocols", "publicHost", "realityDest", "realityServerNames", "remoteServerId", "revision", "routeProfileId", "sharedAppliedRevision", "sharedLastSyncAt", "sharedPushedRevision", "sharedSyncError", "snellObfs", "snellObfsHost", "snellVersion", "source", "status", "trafficRatio", "updatedTime", "usedBytes", "userGroupIds"}
var landingAdminServerKeysShared = append([]string{"busyPortCount", "sharedAvailable", "sharedCaps", "sharedNotice"}, landingAdminServerKeysNative...)

// Plan 132: a non-admin's server list shows only the caller's own node count
// and how many more they can create (both caps, counted like node creation);
// server-wide aggregates, admin limits and 妙妙屋 sync internals are absent.
// Reproduces prod: shared server per-user cap 3, user total cap 2, the admin
// owns a node there and the user one → 节点数 1, 还可创建 1.
func TestLandingUserServerViewContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			future := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
			for _, v := range []interface{}{
				&model.User{ID: 526, User: "leekao", Status: 1, RoleID: 1, ExpTime: future},
				&model.User{ID: 527, User: "other", Status: 1, RoleID: 1, ExpTime: future},
				&model.Node{ID: 601, Name: "native", ServerIP: "203.0.113.1", Port: "10000-19999", LandingCapable: true},
			} {
				if err := r.DB().Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}
			h := handler.New(r, "landing-user-view", "test")
			router := httpserver.NewRouter(h, "landing-user-view", r)
			token := func(id int64, name string, role int) string {
				v, _ := auth.GenerateToken(id, name, role, "landing-user-view")
				return v
			}
			admin, leekao, other := token(1, "admin", 0), token(526, "leekao", 1), token(527, "other", 1)
			call := func(path, tok string, body interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/landing/"+path, bytes.NewReader(raw))
				req.Header.Set("Authorization", tok)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err, w.Body.String())
				}
				return out
			}
			list := func(tok string) map[int64]map[string]interface{} {
				t.Helper()
				out := call("server/list", tok, map[string]interface{}{})
				if out.Code != 0 {
					t.Fatalf("list %+v", out)
				}
				m := map[int64]map[string]interface{}{}
				for _, item := range out.Data.([]interface{}) {
					v := item.(map[string]interface{})
					m[int64(v["id"].(float64))] = v
				}
				return m
			}
			mustCreate := func(tok string, serverID int64, name string) {
				t.Helper()
				if out := call("node/create", tok, map[string]interface{}{"landingServerId": serverID, "name": name, "protocol": "ss2022"}); out.Code != 0 {
					t.Fatalf("create %s %+v", name, out)
				}
			}

			if out := call("server/enable", admin, map[string]interface{}{"nodeId": 601}); out.Code != 0 {
				t.Fatalf("enable %+v", out)
			}
			native, _ := r.GetLandingServerForNode(601)
			caps := &landingcustom.ProtocolCaps{Versions: []int{1}, Protocols: map[string]landingcustom.ProtocolCap{"anytls": {UDP: true}}}
			if err := r.SyncSharedLandings([]landingcustom.Share{{ID: 3, Name: "Boil Hinet 马年", Hosts: landingcustom.Hosts{IPv4: "114.41.205.96"}, Status: "active", PortMin: 30000, PortMax: 30999, MaxConnections: 500, TrafficLimit: 1 << 40, TrafficUsed: 1 << 30, Core: landingcustom.Core{State: "running"}, Protocols: []string{"ss2022", "vless_reality_vision"}, ProtocolCaps: caps, ServerMode: "embedded"}}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			var shared model.LandingServer
			if err := r.DB().Where("source = 'shared'").First(&shared).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.DB().Model(&model.LandingServer{}).Where("id IN ?", []int64{shared.ID, native.ID}).Updates(map[string]interface{}{"max_nodes_per_user": 3, "max_connections": 800, "default_max_connections": 50, "default_max_online_ips": 3}).Error; err != nil {
				t.Fatal(err)
			}
			// leekao: total cap 2, both servers; other: total cap 10, shared only.
			if out := call("user/save", admin, map[string]interface{}{"userId": 526, "maxNodes": 2, "quotas": []interface{}{}, "serverIds": []int64{shared.ID, native.ID}}); out.Code != 0 {
				t.Fatalf("grant leekao %+v", out)
			}
			if out := call("user/save", admin, map[string]interface{}{"userId": 527, "maxNodes": 10, "quotas": []interface{}{}, "serverIds": []int64{shared.ID}}); out.Code != 0 {
				t.Fatalf("grant other %+v", out)
			}
			mustCreate(admin, shared.ID, "keekao ss2022")
			mustCreate(leekao, shared.ID, "test")
			mustCreate(other, shared.ID, "other-1")
			if err := r.DB().Model(&model.LandingNode{}).Where("user_id <> ?", 526).Updates(map[string]interface{}{"connections": 7, "billed_in": int64(5) << 30}).Error; err != nil {
				t.Fatal(err)
			}

			userForbidden := []string{"connections", "billedBytes", "usedBytes", "maxConnections", "defaultMaxConnections", "defaultMaxOnlineIps", "defaultTrafficLimitBytes", "defaultUserQuotaBytes", "busyPortCount", "sharedSyncError", "sharedNotice", "sharedLastSyncAt", "sharedPushedRevision", "sharedAppliedRevision", "coreMemoryMb", "coreCpuPercent", "remoteServerId", "userGroupIds", "directUserCount", "chainNodeCount", "chainOnly", "egress", "routeProfileId", "egressState", "egressError"}
			userKept := []string{"id", "name", "source", "status", "online", "protocols", "flowType", "trafficRatio", "availableUntil", "egressLabel", "nodeCount", "myRemaining", "maxNodesPerUser"}
			checkUser := func(v map[string]interface{}, nodeCount, remaining float64) {
				t.Helper()
				for _, k := range userForbidden {
					if _, ok := v[k]; ok {
						t.Fatalf("user view leaks %q: %v", k, v[k])
					}
				}
				for _, k := range userKept {
					if _, ok := v[k]; !ok {
						t.Fatalf("user view lacks %q", k)
					}
				}
				if v["nodeCount"].(float64) != nodeCount || v["myRemaining"].(float64) != remaining {
					t.Fatalf("server %v nodeCount %v myRemaining %v, want %v / %v", v["id"], v["nodeCount"], v["myRemaining"], nodeCount, remaining)
				}
				if v["source"] == "shared" {
					sc := v["sharedCaps"].(map[string]interface{})
					if len(sc) != 1 || sc["protocol_caps"] == nil {
						t.Fatalf("user sharedCaps %+v", sc)
					}
					pc := sc["protocol_caps"].(map[string]interface{})["protocols"].(map[string]interface{})["anytls"].(map[string]interface{})
					if pc["udp"] != true {
						t.Fatalf("protocol caps lost %+v", pc)
					}
					if v["sharedAvailable"] != true {
						t.Fatalf("shared availability %+v", v)
					}
				}
				raw, _ := json.Marshal(v)
				for _, leak := range []string{"\"hosts\"", "max_connections", "traffic_used_bytes", "server_mode"} {
					if strings.Contains(string(raw), leak) {
						t.Fatalf("user view leaks %s: %s", leak, raw)
					}
				}
			}

			// prod repro: admin + other own nodes on the same server; not counted.
			views := list(leekao)
			checkUser(views[shared.ID], 1, 1)       // per-server 3-1=2, total 2-1=1
			checkUser(views[native.ID], 0, 1)       // per-server 3, total 2-1=1
			checkUser(list(other)[shared.ID], 1, 2) // per-server 3-1=2, total 10-1=9

			// The count agrees with node creation: one more fits, then none.
			mustCreate(leekao, native.ID, "second")
			views = list(leekao)
			checkUser(views[shared.ID], 1, 0)
			checkUser(views[native.ID], 1, 0)
			if out := call("node/create", leekao, map[string]interface{}{"landingServerId": shared.ID, "name": "third", "protocol": "ss2022"}); out.Code == 0 || !strings.Contains(out.Msg, "上限") {
				t.Fatalf("create past remaining 0 %+v", out)
			}
			// per-server cap: other reaches 3 on shared → 0 left there.
			mustCreate(other, shared.ID, "other-2")
			mustCreate(other, shared.ID, "other-3")
			checkUser(list(other)[shared.ID], 3, 0)
			if out := call("node/create", other, map[string]interface{}{"landingServerId": shared.ID, "name": "other-4", "protocol": "ss2022"}); out.Code == 0 || !strings.Contains(out.Msg, "上限") {
				t.Fatalf("create past per-server cap %+v", out)
			}

			// Admin view: same field set as before, server-wide aggregates.
			adminViews := list(admin)
			for id, want := range map[int64][]string{native.ID: landingAdminServerKeysNative, shared.ID: landingAdminServerKeysShared} {
				got := []string{}
				for k := range adminViews[id] {
					got = append(got, k)
				}
				sort.Strings(got)
				w := append([]string(nil), want...)
				sort.Strings(w)
				if strings.Join(got, ",") != strings.Join(w, ",") {
					t.Fatalf("admin field set changed for %d:\n got %v\nwant %v", id, got, w)
				}
			}
			sv := adminViews[shared.ID]
			// shared: admin 1 + leekao 1 + other 3 = 5 nodes; admin's and other-1 carry 7 connections / 5 GiB each.
			if sv["nodeCount"].(float64) != 5 || sv["connections"].(float64) != 14 || sv["billedBytes"].(float64) != float64(int64(10)<<30) || sv["usedBytes"] != sv["billedBytes"] || sv["maxConnections"].(float64) != 800 {
				t.Fatalf("admin aggregates %+v", sv)
			}
			if sc := sv["sharedCaps"].(map[string]interface{}); sc["max_connections"].(float64) != 500 || sc["server_mode"] != "embedded" {
				t.Fatalf("admin sharedCaps %+v", sc)
			}
			if adminViews[native.ID]["nodeCount"].(float64) != 1 {
				t.Fatalf("admin native nodeCount %+v", adminViews[native.ID]["nodeCount"])
			}
		})
	}
}
