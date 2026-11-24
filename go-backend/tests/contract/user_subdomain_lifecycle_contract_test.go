package contract_test

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
)

// Plan 303 round 2 (user 11-24): the 到期 / 停用 lifecycle end to end, only
// through the admin API and the reconcile loop (kicker → debounced pass, as
// in production; no direct reconcile call), SQLite + PostgreSQL, fake CF:
//   - 到期 → CF record gone, name kept + bound to the user and node, tagged
//     「已过期」 in the node list, the user list and the forward address; the
//     subscription content keeps the name (not the main domain, not the IP)
//   - 续期 → the SAME name recreated within seconds; the subscription is
//     byte-identical to before
//   - 停用 / 启用 → the same, tag 「已停用」
//   - deleting the user → record deleted, name retired for good.
func TestUserSubdomainLifecycleContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			db := r.DB()
			const mainDomain, v6 = "vmsilo-huri.imgamer.top", "240e:96c:7100:1fe:1c8:c8aa:4ecc:e4"
			key := "AQEBAQEBAQEBAQEBAQEBAQ=="
			for _, row := range []interface{}{
				&model.Node{ID: 51, Name: "vmsilo 沪日首发", ServerIP: mainDomain, ServerIPV6: sql.NullString{String: mainDomain, Valid: true}, Port: "10000-65535", Secret: "n51", DDNSEnabled: true, DDNSProviderID: 1},
				&model.Node{ID: 61, Name: "Bestvm JP B", ServerIP: "203.0.113.61", Port: "10000-65535", Secret: "n61"},
				&model.User{ID: 801, User: "fanscool", Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 100, FlowResetTime: 1, Num: 10, Status: 1},
				&model.User{ID: 802, User: "ns-robot", Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 100, FlowResetTime: 1, Num: 10, Status: 1},
				&model.LandingServer{ID: 11, Source: "native", NodeID: 61, Name: "JP B", PublicHost: "203.0.113.61", FlowType: 2, TrafficRatio: 1, Status: "active"},
				&model.LandingNode{ID: 22, UserID: 801, LandingServerID: 11, Name: "游戏加速", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-22", Status: "active", Method: "2022-blake3-aes-128-gcm", ServerKey: key, UserKey: key},
				&model.NodeDDNSRecord{NodeID: 51, Domain: mainDomain, RecordType: "AAAA", Role: "main", Source: "ct", ZoneID: "z1", RecordID: "rec001", CurrentIP: v6, TTL: 120, Applied: true, Status: "synced", LastSyncAt: now},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatalf("seed %T: %v", row, err)
				}
			}
			tunnel := model.Tunnel{Name: "沪日首发 -> JP", TrafficRatio: 1, Type: 2, Protocol: "tls", Flow: 2, CreatedTime: now, UpdatedTime: now, Status: 1,
				InIP: sql.NullString{String: mainDomain, Valid: true}}
			if err := db.Create(&tunnel).Error; err != nil {
				t.Fatal(err)
			}
			db.Create(&model.ChainTunnel{TunnelID: tunnel.ID, NodeID: 51, ChainType: "1"})
			fanFwd := model.Forward{UserID: 801, UserName: "fanscool", Name: "游戏加速", TunnelID: tunnel.ID, RemoteAddr: "203.0.113.61:30002", Strategy: "fifo",
				CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost", LandingNodeID: 22}
			robotFwd := model.Forward{UserID: 802, UserName: "ns-robot", Name: "robot", TunnelID: tunnel.ID, RemoteAddr: "127.0.0.1:1", Strategy: "fifo",
				CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost"}
			db.Create(&fanFwd)
			db.Create(&robotFwd)
			db.Create(&model.ForwardPort{ForwardID: fanFwd.ID, NodeID: 51, Port: 52168})
			db.Create(&model.ForwardPort{ForwardID: robotFwd.ID, NodeID: 51, Port: 60099})

			fake := newUSFakeCF()
			api := httptest.NewServer(fake)
			defer api.Close()
			const secret = "user-subdomain-lifecycle"
			h := handler.New(r, secret, "test")
			h.SetMutationRateLimitEnabled(false)
			h.SetDDNSAPIBase(api.URL)
			router := httpserver.NewRouter(h, secret, r)
			stop := h.StartUserSubdomainLoop(50 * time.Millisecond)
			defer stop()
			admin, _ := auth.GenerateToken(1, "admin", 0, secret)
			fanTok, _ := auth.GenerateToken(801, "fanscool", 1, secret)
			call := func(token, path string, body interface{}) (int, string) {
				t.Helper()
				out := requestContractEnvelope(t, router, token, "/api/v1/"+path, body)
				raw, _ := json.Marshal(out)
				return out.Code, string(raw)
			}
			waitFor := func(what string, cond func() bool) time.Duration {
				t.Helper()
				start := time.Now()
				for time.Since(start) < 8*time.Second {
					if cond() {
						return time.Since(start)
					}
					time.Sleep(25 * time.Millisecond)
				}
				t.Fatalf("%s: not within 8 s", what)
				return 0
			}
			nameOf := func(uid int64) model.UserSubdomain {
				var rows []model.UserSubdomain
				db.Where("user_id = ? AND node_id = 51", uid).Order("id").Find(&rows)
				if len(rows) != 1 {
					t.Fatalf("user %d: want exactly one name row, got %+v", uid, rows)
				}
				return rows[0]
			}
			hasRecord := func(domain string) bool { return len(fake.byName(domain)) == 1 }
			userSave := func(uid int64, user string, exp int64, status int) {
				t.Helper()
				if c, raw := call(admin, "user/update", map[string]interface{}{"id": uid, "user": user, "flow": 100, "num": 10,
					"expTime": exp, "flowResetTime": 1, "status": status}); c != 0 {
					t.Fatalf("user/update: %s", raw)
				}
			}

			if c, raw := call(admin, "node/ddns/provider/create", map[string]interface{}{"name": "Cloudflare imgamer.top", "providerType": "cloudflare", "token": "cf-token-303-abcdef"}); c != 0 {
				t.Fatalf("provider: %s", raw)
			}
			// Toggle on through the node editor → the kick creates both names.
			if c, raw := call(admin, "node/update", map[string]interface{}{"id": 51, "name": "vmsilo 沪日首发",
				"serverIpV4": "", "serverIpV6": mainDomain, "port": "10000-65535", "tcpListenAddr": "[::]", "udpListenAddr": "[::]",
				"ddns": map[string]interface{}{"enabled": true, "providerId": 1, "userSubdomain": true, "records": []interface{}{
					map[string]interface{}{"domain": mainDomain, "type": "AAAA", "role": "main", "source": "ct"},
				}}}); c != 0 {
				t.Fatalf("toggle on: %s", raw)
			}
			waitFor("names created", func() bool {
				var n int64
				db.Model(&model.UserSubdomain{}).Where("state = ? AND record_id <> ''", "active").Count(&n)
				return n == 2
			})
			fan := nameOf(801)
			if !hasRecord(fan.Domain) || fake.userCNAMEs()[fan.Domain] != mainDomain {
				t.Fatalf("fan record: %+v", fake.userCNAMEs())
			}

			// Subscription: the name for NekoBox (no IP, no main domain), IPv6 on.
			c, raw := call(fanTok, "sub/create", map[string]interface{}{"name": "s", "items": []interface{}{
				map[string]interface{}{"kind": "forward_link", "forwardId": fanFwd.ID, "entry": "51|" + mainDomain},
			}})
			if c != 0 {
				t.Fatalf("sub/create: %s", raw)
			}
			var created struct {
				Data struct {
					Token string `json:"token"`
				} `json:"data"`
			}
			_ = json.Unmarshal([]byte(raw), &created)
			pull := func() (int, string) {
				req := httptest.NewRequest("GET", "/s/"+created.Data.Token, nil)
				req.RemoteAddr = "203.0.113.70:5555"
				req.Header.Set("User-Agent", "NekoBox/Android/1.4.2")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w.Code, w.Body.String()
			}
			code0, liveBody := pull()
			if code0 != 200 || !strings.Contains(liveBody, "server: "+fan.Domain) || strings.Contains(liveBody, mainDomain) || strings.Contains(liveBody, v6) || !strings.Contains(liveBody, "  ipv6: true\n") {
				t.Fatalf("live sub (%d):\n%s", code0, liveBody)
			}
			subServer := func() string {
				d, err := r.SubPanelLinks(801, nil, nil, false)
				if err != nil {
					t.Fatal(err)
				}
				for _, l := range d.Links {
					if l.Ref.ForwardID == fanFwd.ID {
						return l.Node.Server()
					}
				}
				t.Fatalf("sub links: %+v", d.Links)
				return ""
			}

			stage := func(what, tag string, wantRecord bool) {
				t.Helper()
				waitFor(what+": CF record", func() bool { return hasRecord(fan.Domain) == wantRecord })
				got := nameOf(801)
				if got.Label != fan.Label || got.Domain != fan.Domain || got.UserID != 801 || got.NodeID != 51 {
					t.Fatalf("%s: name not kept: %+v", what, got)
				}
				if s := subServer(); s != fan.Domain {
					t.Fatalf("%s: subscription server %q, want the name", what, s)
				}
				_, list := call(admin, "forward/list", map[string]interface{}{})
				if !strings.Contains(list, fan.Domain+":52168") || strings.Contains(list, mainDomain+":52168") {
					t.Fatalf("%s: forward address: %.800s", what, list)
				}
				if tagged := strings.Contains(list, `"entryNameTags":{"`+fan.Domain+`":"`+tag+`"}`); tagged != (tag != "") {
					t.Fatalf("%s: forward tag %q: %.800s", what, tag, list)
				}
				_, nodeList := call(admin, "node/user-subdomain/list", map[string]interface{}{"nodeId": 51})
				_, userList := call(admin, "node/user-subdomain/list", map[string]interface{}{"userId": 801})
				for _, l := range []string{nodeList, userList} {
					var parsed struct {
						Data struct {
							Items []struct {
								Domain, State, Reason string
							} `json:"items"`
						} `json:"data"`
					}
					_ = json.Unmarshal([]byte(l), &parsed)
					wantState := "active"
					if tag != "" {
						wantState = "paused"
					}
					found := false
					for _, it := range parsed.Data.Items {
						if it.Domain == fan.Domain {
							found = it.State == wantState && it.Reason == tag
						}
					}
					if !found {
						t.Fatalf("%s: dialog list lacks %s %s/%s: %.900s", what, fan.Domain, wantState, tag, l)
					}
				}
			}

			// 到期 → record deleted, name kept + tagged; the account gate refuses pulls.
			userSave(801, "fanscool", now-1000, 1)
			stage("到期", "expired", false)
			if c, _ := pull(); c != 403 {
				t.Fatalf("expired pull: %d", c)
			}
			// 续期 → the same name back within seconds, sub byte-identical.
			userSave(801, "fanscool", now+60*86400000, 1)
			took := waitFor("续期", func() bool { return hasRecord(fan.Domain) && nameOf(801).Live() })
			stage("续期", "", true)
			t.Logf("续期 → %s back in %v (debounce 50 ms)", fan.Domain, took.Round(time.Millisecond))
			if c, body := pull(); c != 200 || body != liveBody {
				t.Fatalf("renewed sub differs (%d):\n%s", c, body)
			}

			// 停用 / 启用.
			userSave(801, "fanscool", now+60*86400000, 0)
			stage("停用", "disabled", false)
			userSave(801, "fanscool", now+60*86400000, 1)
			took = waitFor("启用", func() bool { return hasRecord(fan.Domain) && nameOf(801).Live() })
			stage("启用", "", true)
			t.Logf("启用 → %s back in %v", fan.Domain, took.Round(time.Millisecond))
			if c, body := pull(); c != 200 || body != liveBody {
				t.Fatalf("re-enabled sub differs (%d):\n%s", c, body)
			}

			// Deleting the user → record deleted, name retired for good.
			robot := nameOf(802)
			if c, raw := call(admin, "user/delete", map[string]interface{}{"id": 802}); c != 0 {
				t.Fatalf("user delete: %s", raw)
			}
			waitFor("user deleted", func() bool { return !hasRecord(robot.Domain) && nameOf(802).State == model.UserSubdomainRetired })
			if got := nameOf(802); got.Reason != model.UserSubdomainReasonUserDeleted || got.Label != robot.Label {
				t.Fatalf("user deleted: %+v", got)
			}
			// fanscool's name and the foreign records are untouched.
			if !hasRecord(fan.Domain) || len(fake.byName(mainDomain)) != 1 || fake.byName(mainDomain)[0]["comment"] == "flvx-user" {
				t.Fatal("other records touched")
			}
		})
	}
}
