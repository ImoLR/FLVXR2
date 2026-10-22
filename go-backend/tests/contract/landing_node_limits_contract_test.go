package contract_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 131 — global default landing-node cap and admin owner transfer.

type landingLimitsLab struct {
	t      *testing.T
	r      *repo.Repository
	h      *handler.Handler
	router http.Handler
	admin  string
	server *model.LandingServer
}

func newLandingLimitsLab(t *testing.T, dialect string, users ...*model.User) *landingLimitsLab {
	t.Helper()
	r := landingContractRepo(t, dialect)
	// The seeded admin is user 1 (role 0).
	rows := []interface{}{&model.Node{ID: 641, Name: "native-limits", ServerIP: "203.0.113.41", Port: "10000-19999", LandingCapable: true}, &model.UserGroup{ID: 741, Name: "limits-granted", Status: 1}}
	for _, u := range users {
		rows = append(rows, u)
	}
	for _, v := range rows {
		if err := r.DB().Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	const secret = "landing-limits-contract"
	h := handler.New(r, secret, "test")
	h.SetMutationRateLimitEnabled(false)
	lab := &landingLimitsLab{t: t, r: r, h: h, router: httpserver.NewRouter(h, secret, r)}
	lab.admin, _ = auth.GenerateToken(1, "admin", 0, secret)
	if out := lab.call(lab.admin, "server/enable", map[string]interface{}{"nodeId": 641}); out.Code != 0 {
		t.Fatalf("enable %+v", out)
	}
	s, err := r.GetLandingServerForNode(641)
	if err != nil || s == nil {
		t.Fatal(err)
	}
	s.MaxNodesPerUser = 100
	if err := r.SaveLandingServer(s, []int64{741}); err != nil {
		t.Fatal(err)
	}
	lab.server = s
	return lab
}

func (l *landingLimitsLab) token(u *model.User) string {
	tok, _ := auth.GenerateToken(u.ID, u.User, u.RoleID, "landing-limits-contract")
	return tok
}

func (l *landingLimitsLab) call(token, path string, body interface{}) response.R {
	l.t.Helper()
	return requestContractEnvelope(l.t, l.router, token, "/api/v1/landing/"+path, body)
}

func (l *landingLimitsLab) grant(userIDs ...int64) {
	l.t.Helper()
	for _, id := range userIDs {
		if err := l.r.DB().Create(&model.UserGroupUser{UserGroupID: 741, UserID: id}).Error; err != nil {
			l.t.Fatal(err)
		}
	}
}

func (l *landingLimitsLab) create(token string, name string, extra map[string]interface{}) response.R {
	l.t.Helper()
	body := map[string]interface{}{"landingServerId": l.server.ID, "name": name, "protocol": "ss2022"}
	for k, v := range extra {
		body[k] = v
	}
	return l.call(token, "node/create", body)
}

func (l *landingLimitsLab) count(userID int64) int64 {
	l.t.Helper()
	var n int64
	if err := l.r.DB().Model(&model.LandingNode{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		l.t.Fatal(err)
	}
	return n
}

func (l *landingLimitsLab) userView(userID int64) map[string]interface{} {
	l.t.Helper()
	out := l.call(l.admin, "user/get", map[string]interface{}{"userId": userID})
	if out.Code != 0 {
		l.t.Fatalf("user/get %d: %+v", userID, out)
	}
	return out.Data.(map[string]interface{})
}

func (l *landingLimitsLab) wantCap(userID int64, max int, source string) {
	l.t.Helper()
	v := l.userView(userID)
	if int(v["maxNodes"].(float64)) != max || v["maxNodesSource"] != source {
		l.t.Fatalf("user %d cap = %v/%v, want %d/%s", userID, v["maxNodes"], v["maxNodesSource"], max, source)
	}
}

func TestLandingGlobalDefaultMaxNodesContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			a := &model.User{ID: 521, User: "follow-a", Flow: 100, Status: 1, RoleID: 1}
			b := &model.User{ID: 522, User: "custom-b", Flow: 100, Status: 1, RoleID: 1}
			c := &model.User{ID: 523, User: "legacy-c", Flow: 100, Status: 1, RoleID: 1}
			d := &model.User{ID: 524, User: "fresh-d", Flow: 100, Status: 1, RoleID: 1}
			lab := newLandingLimitsLab(t, dialect, a, b, c, d)
			lab.grant(521, 522, 523, 524)

			// Non-admins cannot read or change the global value.
			for _, path := range []string{"settings/get", "settings/save"} {
				if out := lab.call(lab.token(a), path, map[string]interface{}{"defaultMaxNodes": 99}); out.Code == 0 {
					t.Fatalf("non-admin %s succeeded", path)
				}
			}
			out := lab.call(lab.admin, "settings/get", map[string]interface{}{})
			if out.Code != 0 || out.Data.(map[string]interface{})["defaultMaxNodes"].(float64) != 5 {
				t.Fatalf("initial global must be 5: %+v", out)
			}
			lab.wantCap(521, 5, "global")
			for _, bad := range []interface{}{-1, 10001, nil} {
				if out := lab.call(lab.admin, "settings/save", map[string]interface{}{"defaultMaxNodes": bad}); out.Code == 0 {
					t.Fatalf("invalid global %v accepted", bad)
				}
			}
			// The generic config endpoint validates the key as well.
			if out := requestContractEnvelope(t, lab.router, lab.admin, "/api/v1/config/update", map[string]string{repo.LandingDefaultMaxNodesKey: "abc"}); out.Code == 0 {
				t.Fatal("generic config accepted a non-number")
			}

			// Global 2 applies live to users without an own value.
			if out := lab.call(lab.admin, "settings/save", map[string]interface{}{"defaultMaxNodes": 2}); out.Code != 0 {
				t.Fatalf("save global %+v", out)
			}
			lab.wantCap(521, 2, "global")
			for i := 0; i < 2; i++ {
				if out := lab.create(lab.token(a), fmt.Sprint("a", i), nil); out.Code != 0 {
					t.Fatalf("create under global: %+v", out)
				}
			}
			if out := lab.create(lab.token(a), "a-over", nil); out.Code == 0 || !strings.Contains(out.Msg, "上限") {
				t.Fatalf("global cap not enforced: %+v", out)
			}
			// Admin creating for the user is capped by the user's value too.
			if out := lab.create(lab.admin, "a-by-admin", map[string]interface{}{"userId": 521}); out.Code == 0 {
				t.Fatal("admin create-for-user bypassed the global cap")
			}

			// Own value overrides the global one (new client).
			if out := lab.call(lab.admin, "user/save", map[string]interface{}{"userId": 522, "maxNodesSource": "custom", "maxNodes": 3, "quotas": []interface{}{}}); out.Code != 0 {
				t.Fatalf("custom save %+v", out)
			}
			lab.wantCap(522, 3, "custom")
			for i := 0; i < 3; i++ {
				if out := lab.create(lab.token(b), fmt.Sprint("b", i), nil); out.Code != 0 {
					t.Fatalf("create under custom: %+v", out)
				}
			}
			if out := lab.create(lab.token(b), "b-over", nil); out.Code == 0 {
				t.Fatal("custom cap not enforced")
			}
			// An older client posting a bare number still sets an own value.
			if out := lab.call(lab.admin, "user/save", map[string]interface{}{"userId": 523, "maxNodes": 4, "quotas": []interface{}{}}); out.Code != 0 {
				t.Fatalf("legacy save %+v", out)
			}
			lab.wantCap(523, 4, "custom")
			// A save without any cap field keeps the current choice.
			if out := lab.call(lab.admin, "user/save", map[string]interface{}{"userId": 523, "quotas": []interface{}{}}); out.Code != 0 {
				t.Fatalf("save without cap %+v", out)
			}
			lab.wantCap(523, 4, "custom")
			if out := lab.call(lab.admin, "user/save", map[string]interface{}{"userId": 523, "maxNodesSource": "bogus", "maxNodes": 9}); out.Code == 0 {
				t.Fatal("unknown maxNodesSource accepted")
			}

			// Lowering the global below a user's count keeps every node.
			if _, err := lab.r.ReconcileLandingPolicy(time.Now()); err != nil {
				t.Fatal(err)
			}
			before := lab.count(521)
			if out := lab.call(lab.admin, "settings/save", map[string]interface{}{"defaultMaxNodes": 1}); out.Code != 0 {
				t.Fatal(out)
			}
			if _, err := lab.r.ReconcileLandingPolicy(time.Now()); err != nil {
				t.Fatal(err)
			}
			var kept []model.LandingNode
			if err := lab.r.DB().Where("user_id = ?", 521).Find(&kept).Error; err != nil || int64(len(kept)) != before {
				t.Fatalf("lowering removed nodes: %d of %d (%v)", len(kept), before, err)
			}
			for _, n := range kept {
				if n.Status != "active" {
					t.Fatalf("lowering changed node status: %+v", n)
				}
			}
			if out := lab.create(lab.token(a), "a-after-lower", nil); out.Code == 0 {
				t.Fatal("create over a lowered cap")
			}
			// Custom users are unaffected by the global value.
			lab.wantCap(522, 3, "custom")

			// 0 blocks users who follow it; the admin has no total cap.
			if out := lab.call(lab.admin, "settings/save", map[string]interface{}{"defaultMaxNodes": 0}); out.Code != 0 {
				t.Fatal(out)
			}
			if out := lab.create(lab.token(d), "d0", nil); out.Code == 0 {
				t.Fatal("global 0 did not block")
			}
			if out := lab.call(lab.admin, "user/save", map[string]interface{}{"userId": 524, "maxNodesSource": "custom", "maxNodes": 1, "quotas": []interface{}{}}); out.Code != 0 {
				t.Fatal(out)
			}
			if out := lab.create(lab.token(d), "d1", nil); out.Code != 0 {
				t.Fatalf("own value must override global 0: %+v", out)
			}
			for i := 0; i < 2; i++ {
				if out := lab.create(lab.admin, fmt.Sprint("admin", i), nil); out.Code != 0 {
					t.Fatalf("admin own node blocked by the users' cap: %+v", out)
				}
			}
			// Back to 跟随全局.
			if out := lab.call(lab.admin, "user/save", map[string]interface{}{"userId": 522, "maxNodesSource": "global", "quotas": []interface{}{}}); out.Code != 0 {
				t.Fatal(out)
			}
			lab.wantCap(522, 0, "global")
			var row model.LandingUserSetting
			if err := lab.r.DB().First(&row, "user_id = ?", 522).Error; err != nil || row.MaxNodesCustom || row.MaxNodes != repo.DefaultLandingUserMaxNodes {
				t.Fatalf("follow-global row must keep the old default 5 for rollbacks: %+v %v", row, err)
			}
			if out := lab.call(lab.admin, "settings/get", map[string]interface{}{}); out.Code != 0 || out.Data.(map[string]interface{})["customUserCount"].(float64) != 2 {
				t.Fatalf("custom user count %+v", out)
			}
		})
	}
}

// 20 parallel creates of a user following the global default never exceed it.
func TestLandingGlobalDefaultConcurrentCreatesContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := &model.User{ID: 525, User: "burst-e", Flow: 100, Status: 1, RoleID: 1}
			f := &model.User{ID: 526, User: "burst-f", Flow: 100, Status: 1, RoleID: 1}
			lab := newLandingLimitsLab(t, dialect, e, f)
			lab.grant(525, 526)
			burst := func(round, global int) {
				if out := lab.call(lab.admin, "settings/save", map[string]interface{}{"defaultMaxNodes": global}); out.Code != 0 {
					t.Fatal(out)
				}
				var wg sync.WaitGroup
				results := make([]response.R, 20)
				for i := range results {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						results[i] = lab.create(lab.token(e), fmt.Sprintf("burst-%d-%d", round, i), nil)
					}(i)
				}
				wg.Wait()
				for _, out := range results {
					if out.Code != 0 && !strings.Contains(out.Msg, "上限") {
						t.Fatalf("unexpected refusal: %+v", out)
					}
				}
				if n := lab.count(525); n != int64(global) {
					t.Fatalf("round %d: %d nodes, want exactly %d", round, n, global)
				}
			}
			burst(1, 3)
			burst(2, 6)
			// Round 3: a transfer to E races 10 creates of E; the cap (7)
			// still holds because both lock E's user row first.
			if out := lab.call(lab.admin, "user/save", map[string]interface{}{"userId": 526, "maxNodesSource": "custom", "maxNodes": 5, "quotas": []interface{}{}}); out.Code != 0 {
				t.Fatal(out)
			}
			var moving []int64
			for i := 0; i < 3; i++ {
				out := lab.create(lab.token(f), fmt.Sprint("f", i), nil)
				if out.Code != 0 {
					t.Fatal(out)
				}
				moving = append(moving, int64(out.Data.(map[string]interface{})["id"].(float64)))
			}
			if out := lab.call(lab.admin, "settings/save", map[string]interface{}{"defaultMaxNodes": 7}); out.Code != 0 {
				t.Fatal(out)
			}
			var wg sync.WaitGroup
			results := make([]response.R, 13)
			for i := range results {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					if i < len(moving) {
						results[i] = lab.call(lab.admin, "node/transfer", map[string]interface{}{"id": moving[i], "userId": 525})
						return
					}
					results[i] = lab.create(lab.token(e), fmt.Sprint("race", i), nil)
				}(i)
			}
			wg.Wait()
			moved := int64(0)
			for i, out := range results {
				if out.Code != 0 && !strings.Contains(out.Msg, "上限") {
					t.Fatalf("unexpected refusal: %+v", out)
				}
				if out.Code == 0 && i < len(moving) {
					moved++
				}
			}
			if n := lab.count(525); n != 7 {
				t.Fatalf("race round: %d nodes, want exactly 7", n)
			}
			if n := lab.count(526); n != 3-moved {
				t.Fatalf("F has %d nodes after %d transfers, want %d", n, moved, 3-moved)
			}
		})
	}
}

// Rows from before plan 131: 5 (the old hard default) follows the global
// value, every other value stays the user's own; the backfill runs once.
func TestLandingMaxNodesCustomBackfillContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			open, dropColumn := landingReopenableRepo(t, dialect)
			r := open()
			for _, v := range []interface{}{&model.User{ID: 551, User: "m5", Status: 1, RoleID: 1}, &model.User{ID: 552, User: "m2", Status: 1, RoleID: 1}, &model.User{ID: 553, User: "m0", Status: 1, RoleID: 1}, &model.User{ID: 554, User: "m10", Status: 1, RoleID: 1}, &model.User{ID: 555, User: "none", Status: 1, RoleID: 1}} {
				if err := r.DB().Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}
			// Simulate a pre-131 database: no flag column, no backfill marker.
			if err := r.DB().Exec(dropColumn).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.DB().Exec("DELETE FROM vite_config WHERE name = ?", "landing_user_max_nodes_custom_v1").Error; err != nil {
				t.Fatal(err)
			}
			for id, max := range map[int64]int{551: 5, 552: 2, 553: 0, 554: 10} {
				if err := r.DB().Exec("INSERT INTO landing_user_setting (user_id, max_nodes) VALUES (?, ?)", id, max).Error; err != nil {
					t.Fatal(err)
				}
			}
			r.Close()
			r = open()
			want := map[int64]repo.LandingMaxNodes{551: {Effective: 5, Source: "global"}, 552: {Effective: 2, Source: "custom"}, 553: {Effective: 0, Source: "custom"}, 554: {Effective: 10, Source: "custom"}, 555: {Effective: 5, Source: "global"}}
			check := func(global int) {
				t.Helper()
				for id, w := range want {
					got, err := r.EffectiveLandingMaxNodes(id)
					eff := w.Effective
					if w.Source == "global" {
						eff = global
					}
					if err != nil || got.Source != w.Source || got.Effective != eff || got.Global != global {
						t.Fatalf("user %d: %+v %v, want %d/%s", id, got, err, eff, w.Source)
					}
				}
			}
			check(5)
			if err := r.SetLandingDefaultMaxNodes(7); err != nil {
				t.Fatal(err)
			}
			check(7)
			// The marker keeps a later restart from re-flagging rows.
			if err := r.DB().Model(&model.LandingUserSetting{}).Where("user_id = ?", 552).Updates(map[string]interface{}{"max_nodes_custom": false, "max_nodes": 2}).Error; err != nil {
				t.Fatal(err)
			}
			r.Close()
			r = open()
			want[552] = repo.LandingMaxNodes{Source: "global"}
			check(7)
			r.Close()
		})
	}
}

// landingReopenableRepo opens the same database (SQLite file or PostgreSQL
// schema) more than once, like a panel restart.
func landingReopenableRepo(t *testing.T, dialect string) (func() *repo.Repository, string) {
	t.Helper()
	drop := "ALTER TABLE landing_user_setting DROP COLUMN max_nodes_custom"
	if dialect != "postgres" {
		path := filepath.Join(t.TempDir(), "reopen.db")
		return func() *repo.Repository {
			r, err := repo.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			return r
		}, drop
	}
	dsn := os.Getenv("FLVX_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set FLVX_POSTGRES_TEST_DSN to run PostgreSQL")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("landing_reopen_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); admin.Close() })
	scoped, err := withSearchPath(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	return func() *repo.Repository {
		r, err := repo.OpenPostgres(scoped)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}, drop
}

func TestLandingNodeTransferContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			a := &model.User{ID: 531, User: "owner-a", Flow: 100, Status: 1, RoleID: 1}
			b := &model.User{ID: 532, User: "target-b", Flow: 100, Status: 1, RoleID: 1}
			c := &model.User{ID: 533, User: "no-grant-c", Flow: 100, Status: 1, RoleID: 1}
			d := &model.User{ID: 534, User: "disabled-d", Flow: 100, Status: 0, RoleID: 1}
			lab := newLandingLimitsLab(t, dialect, a, b, c, d)
			lab.grant(531, 532, 534)
			out := lab.create(lab.admin, "for-a", map[string]interface{}{"userId": 531})
			if out.Code != 0 {
				t.Fatalf("admin create for A: %+v", out)
			}
			id := int64(out.Data.(map[string]interface{})["id"].(float64))
			original, _ := lab.r.GetLandingNode(id)
			transfer := func(token string, to int64) response.R {
				t.Helper()
				return lab.call(token, "node/transfer", map[string]interface{}{"id": id, "userId": to})
			}
			refuse := func(token string, to int64, want string) {
				t.Helper()
				out := transfer(token, to)
				if out.Code == 0 || !strings.Contains(out.Msg, want) {
					t.Fatalf("transfer to %d: %+v, want refusal containing %q", to, out, want)
				}
				if n, _ := lab.r.GetLandingNode(id); n.UserID != 531 {
					t.Fatalf("refused transfer moved the node: %+v", n)
				}
			}

			// Billing before the transfer goes to A.
			native := func(epoch string, up, down int64) repo.LandingCounter {
				return repo.LandingCounter{ID: id, NodeID: 641, InboundTag: original.InboundTag, Epoch: epoch, UpBytes: up, DownBytes: down}
			}
			ingest := func(c repo.LandingCounter) {
				t.Helper()
				if _, err := lab.r.ApplyFlowBatch(repo.FlowBatch{LandingCounters: []repo.LandingCounter{c}}, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			ingest(native("100", 1000, 2000))
			flow := func(userID int64) (int64, int64) {
				t.Helper()
				var u model.User
				if err := lab.r.DB().First(&u, userID).Error; err != nil {
					t.Fatal(err)
				}
				return u.InFlow, u.OutFlow
			}
			aIn, aOut := flow(531)
			if aIn == 0 || aOut == 0 {
				t.Fatalf("A not billed before transfer: %d/%d", aIn, aOut)
			}

			// Non-admin callers are refused.
			if out := transfer(lab.token(a), 532); out.Code == 0 {
				t.Fatal("non-admin transfer succeeded")
			}
			refuse(lab.admin, 531, "已属于")
			refuse(lab.admin, 999, "目标用户不存在")
			refuse(lab.admin, 533, "无权使用此落地服务器")
			refuse(lab.admin, 534, "停用或到期")
			// Rules of the old owner referencing the node are named.
			rule := &model.Forward{UserID: 531, UserName: "owner-a", Name: "a-rule", TunnelID: 1, RemoteAddr: "203.0.113.41:10000", Status: 1, LandingNodeID: id, CreatedTime: 1, UpdatedTime: 1}
			if err := lab.r.DB().Create(rule).Error; err != nil {
				t.Fatal(err)
			}
			refuse(lab.admin, 532, "「a-rule」")
			if err := lab.r.DB().Delete(rule).Error; err != nil {
				t.Fatal(err)
			}
			// Target's total cap (counting the moved node).
			if out := lab.call(lab.admin, "user/save", map[string]interface{}{"userId": 532, "maxNodesSource": "custom", "maxNodes": 1, "quotas": []interface{}{}}); out.Code != 0 {
				t.Fatal(out)
			}
			if out := lab.create(lab.token(b), "b-own", nil); out.Code != 0 {
				t.Fatal(out)
			}
			refuse(lab.admin, 532, "总数上限")
			// Target's per-server cap.
			if out := lab.call(lab.admin, "user/save", map[string]interface{}{"userId": 532, "maxNodesSource": "custom", "maxNodes": 10, "quotas": []interface{}{}}); out.Code != 0 {
				t.Fatal(out)
			}
			lab.server.MaxNodesPerUser = 1
			if err := lab.r.SaveLandingServer(lab.server, []int64{741}); err != nil {
				t.Fatal(err)
			}
			refuse(lab.admin, 532, "每人上限")
			lab.server.MaxNodesPerUser = 100
			if err := lab.r.SaveLandingServer(lab.server, []int64{741}); err != nil {
				t.Fatal(err)
			}
			// Delete-pending and chain-only nodes are not normal user nodes.
			if err := lab.r.DB().Model(&model.LandingNode{}).Where("id = ?", id).Update("delete_pending", true).Error; err != nil {
				t.Fatal(err)
			}
			refuse(lab.admin, 532, "正在删除")
			if err := lab.r.DB().Model(&model.LandingNode{}).Where("id = ?", id).Update("delete_pending", false).Error; err != nil {
				t.Fatal(err)
			}
			if err := lab.r.DB().Model(&model.LandingServer{}).Where("id = ?", lab.server.ID).Update("chain_only", true).Error; err != nil {
				t.Fatal(err)
			}
			refuse(lab.admin, 532, "线路机")
			if err := lab.r.DB().Model(&model.LandingServer{}).Where("id = ?", lab.server.ID).Update("chain_only", false).Error; err != nil {
				t.Fatal(err)
			}

			// Success: ownership moves, identity stays.
			out = transfer(lab.admin, 532)
			if out.Code != 0 {
				t.Fatalf("transfer %+v", out)
			}
			moved, _ := lab.r.GetLandingNode(id)
			if moved.UserID != 532 || moved.Port != original.Port || moved.ServerKey != original.ServerKey || moved.UserKey != original.UserKey || moved.InboundTag != original.InboundTag || moved.UpBytes != 1000 || moved.DownBytes != 2000 {
				t.Fatalf("transfer changed identity/counters: %+v", moved)
			}
			listed := func(token string) bool {
				t.Helper()
				out := lab.call(token, "node/list", map[string]interface{}{})
				for _, v := range out.Data.([]interface{}) {
					if int64(v.(map[string]interface{})["id"].(float64)) == id {
						return true
					}
				}
				return false
			}
			if !listed(lab.token(b)) || listed(lab.token(a)) {
				t.Fatal("node lists do not follow the new owner")
			}
			if out := lab.call(lab.token(b), "node/links", map[string]interface{}{"id": id}); out.Code != 0 {
				t.Fatalf("new owner links %+v", out)
			}
			if out := lab.call(lab.token(a), "node/links", map[string]interface{}{"id": id}); out.Code == 0 {
				t.Fatal("old owner still reads links")
			}

			// Billing after the transfer goes to B, A keeps what it had.
			bIn0, bOut0 := flow(532)
			ingest(native("100", 3000, 5000))
			if in, outB := flow(531); in != aIn || outB != aOut {
				t.Fatalf("old owner billed after transfer: %d/%d (was %d/%d)", in, outB, aIn, aOut)
			}
			bIn, bOut := flow(532)
			if bIn-bIn0 != (3000-1000)*aIn/1000 || bOut-bOut0 != (5000-2000)*aOut/2000 {
				t.Fatalf("new owner billing %d/%d, want delta ×ratio of 2000/3000", bIn-bIn0, bOut-bOut0)
			}
			var q model.UserServerQuota
			if err := lab.r.DB().Where("user_id = ? AND server_kind = ? AND server_ref_id = ?", 532, "node", 641).First(&q).Error; err != nil || q.UsedIn != bIn-bIn0 {
				t.Fatalf("per-server quota of the new owner: %+v %v", q, err)
			}

			// To the admin (userId 0 = caller) is allowed.
			if out := transfer(lab.admin, 0); out.Code != 0 {
				t.Fatalf("transfer to admin %+v", out)
			}
			if n, _ := lab.r.GetLandingNode(id); n.UserID != 1 {
				t.Fatalf("admin transfer owner %d", n.UserID)
			}
		})
	}
}
