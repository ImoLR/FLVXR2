package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// Plan 212 over HTTP: real encrypted agent uploads (entry flow + landing Core
// counter) → the user's 「服务器流量」 card shows exactly the user_server_quota
// numbers, only for the caller; the day rollup equals the user's counters; the
// ranking and its drill-down are admin-only.
func TestTrafficStatsContract(t *testing.T) {
	const gb = int64(1 << 30)
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			rows := []interface{}{
				&model.User{ID: 501, User: "stats-user", Pwd: "x", RoleID: 1, Status: 1, Flow: 99999, Num: 10, ExpTime: now + 86400000, FlowResetTime: 1},
				&model.User{ID: 502, User: "stats-other", Pwd: "x", RoleID: 1, Status: 1, Flow: 99999, Num: 10, ExpTime: now + 86400000, FlowResetTime: 1},
				&model.Node{ID: 601, Name: "JP-landing", Secret: "stats-landing-secret", ServerIP: "203.0.113.1", Port: "10000-19999"},
				&model.Node{ID: 602, Name: "HK-entry", Secret: "stats-entry-secret", ServerIP: "203.0.113.2", Port: "10000-19999", DefaultUserQuotaBytes: 3 * gb},
				&model.Tunnel{ID: 801, Name: "HK-JP", Status: 1, Type: 2, Flow: 2, TrafficRatio: 1},
				&model.UserTunnel{ID: 701, UserID: 501, TunnelID: 801, Status: 1, Flow: 99999, ExpTime: now + 86400000},
				&model.Forward{ID: 901, UserID: 501, UserName: "stats-user", Name: "rule", TunnelID: 801, Status: 1, LandingNodeID: 1, RemoteAddr: "203.0.113.1:30001"},
				&model.ForwardPort{ForwardID: 901, NodeID: 602, Port: 12000},
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
			h := handler.New(r, "stats-contract", "test")
			router := httpserver.NewRouter(h, "stats-contract", r)
			admin, _ := auth.GenerateToken(1, "admin", 0, "stats-contract")
			user, _ := auth.GenerateToken(501, "stats-user", 1, "stats-contract")
			other, _ := auth.GenerateToken(502, "stats-other", 1, "stats-contract")
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
			decode := func(v interface{}, into interface{}) {
				t.Helper()
				raw, _ := json.Marshal(v)
				if err := json.Unmarshal(raw, into); err != nil {
					t.Fatal(err)
				}
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
			counter := map[string]interface{}{"id": 1, "inbound_tag": "flvxl-1", "epoch": "100", "up_bytes": gb / 4, "down_bytes": 3 * gb / 4}
			upload("stats-entry-secret", []map[string]interface{}{{"n": "901_501_701_tcp", "d": gb / 4, "u": 3 * gb / 4}})
			upload("stats-landing-secret", map[string]interface{}{"flows": []interface{}{}, "landing_counters": []interface{}{counter}})
			counter["up_bytes"], counter["down_bytes"] = gb/2, gb
			upload("stats-landing-secret", map[string]interface{}{"flows": []interface{}{}, "landing_counters": []interface{}{counter}})

			// 1) the user's card = user_server_quota, own servers only.
			res := call("traffic/servers", user, map[string]interface{}{})
			var card repo.ServerTrafficView
			decode(res.Data, &card)
			if res.Code != 0 || len(card.Servers) != 2 || card.ResetDay != 1 || card.PeriodStart == "" {
				t.Fatalf("card %+v %+v", res, card)
			}
			for _, s := range card.Servers {
				var q model.UserServerQuota
				if err := r.DB().First(&q, "user_id = 501 AND server_kind = ? AND server_ref_id = ?", s.ServerKind, s.ServerRefID).Error; err != nil {
					t.Fatal(err)
				}
				if s.UsedIn != q.UsedIn || s.UsedOut != q.UsedOut || s.LimitBytes != q.LimitBytes || q.UsedIn+q.UsedOut == 0 {
					t.Fatalf("card row %+v != quota %+v", s, q)
				}
				if s.ServerRefID == 602 && (!s.Entry || s.LimitBytes != 3*gb || s.ServerName != "HK-entry") || s.ServerRefID == 601 && !s.Landing {
					t.Fatalf("row %+v", s)
				}
			}
			// Another user (even naming user 501) only gets their own (none).
			res = call("traffic/servers", other, map[string]interface{}{"userId": 501})
			decode(res.Data, &card)
			if res.Code != 0 || len(card.Servers) != 0 {
				t.Fatalf("other user saw %+v", card)
			}

			// 2) the day rollup = the user's billed counters.
			u, _ := r.GetUserByID(501)
			var day struct{ In, Out int64 }
			r.DB().Model(&model.TrafficDaily{}).Select(`COALESCE(SUM(in_bytes),0) AS "in", COALESCE(SUM(out_bytes),0) AS "out"`).
				Where("user_id = 501 AND server_kind = 'total'").Scan(&day)
			if u.InFlow == 0 || day.In != u.InFlow || day.Out != u.OutFlow {
				t.Fatalf("rollup %+v != user %d/%d", day, u.InFlow, u.OutFlow)
			}

			// 3) ranking: admin only.
			for _, path := range []string{"traffic/ranking", "traffic/ranking/user"} {
				if res := call(path, user, map[string]interface{}{"userId": 501, "window": "today"}); res.Code != 403 {
					t.Fatalf("user on %s: %+v", path, res)
				}
			}
			res = call("traffic/ranking", admin, map[string]interface{}{})
			var ranking repo.TrafficRankingView
			decode(res.Data, &ranking)
			if res.Code != 0 || len(ranking.Windows) != 3 || ranking.Since <= 0 {
				t.Fatalf("ranking %+v", res)
			}
			today := ranking.Windows[0]
			if today.Key != "today" || len(today.Users) != 1 || today.Users[0].UserID != 501 || today.Users[0].In != u.InFlow || today.Users[0].Out != u.OutFlow || len(today.Servers) != 2 {
				t.Fatalf("today %+v", today)
			}
			res = call("traffic/ranking/user", admin, map[string]interface{}{"userId": 501, "window": "month"})
			var split repo.UserTrafficSplitView
			decode(res.Data, &split)
			if res.Code != 0 || split.In != u.InFlow || len(split.Servers) != 2 {
				t.Fatalf("split %+v", res)
			}
			if res := call("traffic/ranking/user", admin, map[string]interface{}{"userId": 501, "window": "year"}); res.Code == 0 {
				t.Fatal("bad window accepted")
			}
			if res := call("traffic/ranking/user", admin, map[string]interface{}{"userId": 501, "window": "today", "x": 1}); res.Code == 0 {
				t.Fatal("unknown field accepted")
			}
		})
	}
}
