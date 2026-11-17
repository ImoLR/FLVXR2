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
)

// Plan 271 利群备线流量 over HTTP (agent flow uploads + rule/grant APIs):
//   - the 备线 forward (admin-owned) of alice's 利群 rule bills alice's 总流量
//     and per-server quota, not the admin's; its counters stay on the forward;
//   - every rule using the 备线 gets its bytes in backup_in/out_flow; a 备线
//     shared by two of alice's rules shows the full bytes on both and counts
//     once in alice's total;
//   - a 备线 shared by alice and bob stays billed to its owner (no split);
//   - an admin rule's 备线 and plain forwards bill as before;
//   - alice's reset zeroes her rules' 备线流量 only; a rule save does not put
//     back stale counters.
func TestLeiKwanBackupFlowContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			rows := []interface{}{
				&model.User{ID: 501, User: "alice", Pwd: "x", RoleID: 1, Status: 1, Flow: 99999, Num: 10, ExpTime: now + 86400000, FlowResetTime: 1},
				&model.User{ID: 502, User: "bob", Pwd: "x", RoleID: 1, Status: 1, Flow: 99999, Num: 10, ExpTime: now + 86400000, FlowResetTime: 1},
				&model.Node{ID: 602, Name: "深港ixp1", Secret: "lk-backup-entry", ServerIP: "203.0.113.2", Port: "10000-19999"},
				&model.Tunnel{ID: 801, Name: "深港", Status: 1, Type: 1, Flow: 1, TrafficRatio: 1},
				&model.ChainTunnel{TunnelID: 801, NodeID: 602, ChainType: "1"},
				&model.LeiKwanLine{ID: 1, AccountID: 1, RouteID: 10, Name: "利群·广港", RouteLabel: "广州 → 香港", Transport: "tcp+udp", BackupTunnelID: 801, Status: 1},
				&model.LeiKwanGrant{ID: 1, UserID: 501, MainEntryNodeID: 602, BackupLine: 1, Status: 1},
				&model.LeiKwanGrant{ID: 2, UserID: 502, MainEntryNodeID: 602, BackupLine: 1, Status: 1},
			}
			fwd := func(id int64, name string) {
				rows = append(rows,
					&model.Forward{ID: id, UserID: 1, UserName: "admin", Name: name, TunnelID: 801, Status: 1, RemoteAddr: "198.51.100.9:443"},
					&model.ForwardPort{ForwardID: id, NodeID: 602, Port: int(12000 + id%1000)})
			}
			fwd(148, "備線·263")     // alice R1
			fwd(587, "備線·280/282") // alice R2 + R3 (shared, same owner)
			fwd(600, "備線·mix")     // alice R5 + bob R4 (ambiguous)
			fwd(601, "備線·admin")   // admin R6
			fwd(999, "plain")      // not a 备线
			rule := func(id, user, forwardID, backup int64, name string) {
				rows = append(rows, &model.LeiKwanRule{ID: id, UserID: user, LineID: 1, AccountID: 1, ForwardID: forwardID, Name: name, ListenPort: int(10000 + id), Transport: "tcp+udp", TargetHost: "198.51.100.9", TargetPort: 443, BackupForwardID: backup, BackupMode: "linked", Status: 1, CreatedTime: now, UpdatedTime: now})
			}
			rule(1, 501, 263, 148, "263 alice")
			rule(2, 501, 280, 587, "280 alice")
			rule(3, 501, 282, 587, "282 alice")
			rule(4, 502, 290, 600, "290 bob")
			rule(5, 501, 291, 600, "291 alice")
			rule(6, 1, 264, 601, "264 admin")
			for _, row := range rows {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			h := handler.New(r, "lk-backup", "test")
			router := httpserver.NewRouter(h, "lk-backup", r)
			admin, _ := auth.GenerateToken(1, "admin", 0, "lk-backup")
			alice, _ := auth.GenerateToken(501, "alice", 1, "lk-backup")
			upload := func(items ...map[string]interface{}) {
				t.Helper()
				plain, _ := json.Marshal(items)
				cipher, err := security.NewAESCrypto("lk-backup-entry")
				if err != nil {
					t.Fatal(err)
				}
				enc, err := cipher.Encrypt(plain)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(map[string]interface{}{"encrypted": true, "data": enc})
				req := httptest.NewRequest(http.MethodPost, "/flow/upload?secret=lk-backup-entry", bytes.NewReader(raw))
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != 200 || w.Body.String() != "ok" {
					t.Fatalf("flow upload %d %s", w.Code, w.Body.String())
				}
			}
			item := func(forward int64, up, down int64) map[string]interface{} {
				return map[string]interface{}{"n": itoa(forward) + "_1_0_tcp", "d": up, "u": down}
			}
			user := func(id int64) model.User {
				var u model.User
				if err := r.DB().First(&u, id).Error; err != nil {
					t.Fatal(err)
				}
				return u
			}
			lk := func(id int64) model.LeiKwanRule {
				var x model.LeiKwanRule
				if err := r.DB().First(&x, id).Error; err != nil {
					t.Fatal(err)
				}
				return x
			}
			forward := func(id int64) model.Forward {
				var f model.Forward
				if err := r.DB().First(&f, id).Error; err != nil {
					t.Fatal(err)
				}
				return f
			}
			quota := func(uid int64) int64 {
				var q model.UserServerQuota
				if err := r.DB().Where("user_id = ? AND server_kind = 'node' AND server_ref_id = ?", uid, 602).Limit(1).Find(&q).Error; err != nil {
					t.Fatal(err)
				}
				return q.UsedIn + q.UsedOut
			}

			// 1. alice's own 备线 148: alice pays, the admin does not.
			upload(item(148, 100, 900))
			if a := user(501); a.InFlow != 100 || a.OutFlow != 900 {
				t.Fatalf("alice after 148: %d/%d", a.InFlow, a.OutFlow)
			}
			if ad := user(1); ad.InFlow != 0 || ad.OutFlow != 0 {
				t.Fatalf("admin billed for alice's 备线: %d/%d", ad.InFlow, ad.OutFlow)
			}
			if f := forward(148); f.InFlow != 100 || f.OutFlow != 900 {
				t.Fatalf("forward counters %d/%d", f.InFlow, f.OutFlow)
			}
			if x := lk(1); x.BackupInFlow != 100 || x.BackupOutFlow != 900 || x.InFlow != 0 {
				t.Fatalf("R1 备线流量 %d/%d (利群 %d)", x.BackupInFlow, x.BackupOutFlow, x.InFlow)
			}
			if quota(501) != 1000 || quota(1) != 0 {
				t.Fatalf("per-server quota alice %d admin %d", quota(501), quota(1))
			}

			// 2. shared by two of alice's rules: full bytes on both, billed once.
			upload(item(587, 50, 150))
			if x2, x3 := lk(2), lk(3); x2.BackupInFlow+x2.BackupOutFlow != 200 || x3.BackupInFlow+x3.BackupOutFlow != 200 {
				t.Fatalf("shared 587: R2 %d R3 %d", x2.BackupInFlow+x2.BackupOutFlow, x3.BackupInFlow+x3.BackupOutFlow)
			}
			if a := user(501); a.InFlow+a.OutFlow != 1200 {
				t.Fatalf("alice after the shared 备线: %d (want 1000 + 200 once)", a.InFlow+a.OutFlow)
			}

			// 3. alice + bob on one 备线: stays with the forward owner (admin).
			upload(item(600, 10, 30))
			if a, b, ad := user(501), user(502), user(1); a.InFlow+a.OutFlow != 1200 || b.InFlow+b.OutFlow != 0 || ad.InFlow+ad.OutFlow != 40 {
				t.Fatalf("ambiguous 600: alice %d bob %d admin %d", a.InFlow+a.OutFlow, b.InFlow+b.OutFlow, ad.InFlow+ad.OutFlow)
			}
			if x4, x5 := lk(4), lk(5); x4.BackupInFlow+x4.BackupOutFlow != 40 || x5.BackupInFlow+x5.BackupOutFlow != 40 {
				t.Fatal("ambiguous 备线 still shows its bytes on both rules")
			}

			// 4. the admin's own 备线 and a plain forward: billed as before.
			upload(item(601, 5, 5), item(999, 7, 13))
			if ad := user(1); ad.InFlow+ad.OutFlow != 40+10+20 {
				t.Fatalf("admin after 601 + plain: %d", ad.InFlow+ad.OutFlow)
			}
			if x := lk(6); x.BackupInFlow+x.BackupOutFlow != 10 {
				t.Fatalf("admin rule 备线流量 %d", x.BackupInFlow+x.BackupOutFlow)
			}

			// 5. APIs: rule rows + per-user totals.
			call := func(path, tok string, body interface{}) map[string]interface{} {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
				req.Header.Set("Authorization", tok)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Code != 0 {
					t.Fatalf("%s: %v %s", path, err, w.Body.String())
				}
				m, _ := out.Data.(map[string]interface{})
				return m
			}
			rulesByName := func(m map[string]interface{}) map[string]map[string]interface{} {
				out := map[string]map[string]interface{}{}
				for _, it := range m["rules"].([]interface{}) {
					v := it.(map[string]interface{})
					out[v["name"].(string)] = v
				}
				return out
			}
			adm := rulesByName(call("/api/v1/leikwan-rule/list", admin, map[string]interface{}{}))
			if v := adm["280 alice"]; num(v["backupInFlow"])+num(v["backupOutFlow"]) != 200 || num(v["backupSharedCount"]) != 1 || len(v["backupSharedWith"].([]interface{})) != 1 || v["backupSharedWith"].([]interface{})[0] != "282 alice" {
				t.Fatalf("admin row 280: %v", v)
			}
			if v := adm["291 alice"]; num(v["backupSharedCount"]) != 1 || v["backupSharedWith"].([]interface{})[0] != "290 bob" {
				t.Fatalf("admin row 291: %v", v)
			}
			own := call("/api/v1/leikwan-rule/list", alice, map[string]interface{}{})
			mine := rulesByName(own)
			if _, ok := mine["290 bob"]; ok || len(mine) != 4 {
				t.Fatalf("alice sees %d rules", len(mine))
			}
			if v := mine["291 alice"]; num(v["backupSharedCount"]) != 1 || len(v["backupSharedWith"].([]interface{})) != 0 {
				t.Fatalf("alice must not see bob's rule name: %v", v)
			}
			if v := mine["263 alice"]; num(v["backupInFlow"]) != 100 || num(v["backupOutFlow"]) != 900 || v["backupSharedCount"] != nil {
				t.Fatalf("alice row 263: %v", v)
			}
			// alice: 148 (1000) + 587 once (200) + 600 (40) = 1240
			if g := own["grant"].(map[string]interface{}); num(g["backupFlow"]) != 1240 {
				t.Fatalf("alice header 备线流量 %v", g["backupFlow"])
			}
			grants := call("/api/v1/leikwan/grant/list", admin, map[string]interface{}{})
			totals := map[float64]int64{}
			for _, it := range grants["grants"].([]interface{}) {
				g := it.(map[string]interface{})
				totals[g["userId"].(float64)] = num(g["backupFlow"])
			}
			if totals[501] != 1240 || totals[502] != 40 {
				t.Fatalf("授权 card totals %v", totals)
			}

			// 6. a handler-style read-modify-write keeps the billed counters.
			stale := lk(1)
			upload(item(148, 1, 1))
			stale.Name = "263 alice renamed"
			if err := r.SaveLeiKwanRule(&stale); err != nil {
				t.Fatal(err)
			}
			if x := lk(1); x.Name != "263 alice renamed" || x.BackupInFlow != 101 || x.BackupOutFlow != 901 {
				t.Fatalf("save clobbered counters: %s %d/%d", x.Name, x.BackupInFlow, x.BackupOutFlow)
			}

			// 7. alice's reset: her rules' 备线流量 restart; bob's stays.
			if err := r.ResetUserFlowByUser(501, time.Now().UnixMilli(), 1, "admin"); err != nil {
				t.Fatal(err)
			}
			for _, id := range []int64{1, 2, 3, 5} {
				if x := lk(id); x.BackupInFlow != 0 || x.BackupOutFlow != 0 {
					t.Fatalf("R%d after alice's reset: %d/%d", id, x.BackupInFlow, x.BackupOutFlow)
				}
			}
			if x := lk(4); x.BackupInFlow+x.BackupOutFlow != 40 {
				t.Fatal("bob's rule reset by alice's reset")
			}
			upload(item(148, 3, 7))
			if x, a := lk(1), user(501); x.BackupInFlow != 3 || x.BackupOutFlow != 7 || a.InFlow+a.OutFlow != 10 {
				t.Fatalf("after reset: R1 %d/%d alice %d", x.BackupInFlow, x.BackupOutFlow, a.InFlow+a.OutFlow)
			}
		})
	}
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
