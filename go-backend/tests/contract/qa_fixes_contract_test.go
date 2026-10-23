package contract_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 136: QA round-2 panel fixes (F11, F12, F15, F23, F24, F26, F27).

func waitContract(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// F11: a stored "online" survives a panel restart only until the panel starts
// serving; the agent connecting sets it again, disconnecting clears it.
func TestNodeOnlineStateResetOnStartupContract(t *testing.T) {
	const secret = "qa136-f11"
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	now := time.Now().UnixMilli()
	for _, n := range []model.Node{
		{ID: 31, Name: "stale-online", Secret: "f11-node-secret", ServerIP: "198.51.100.31", Port: "20000-20100", Status: 1, CreatedTime: now},
		{ID: 32, Name: "never-connects", Secret: "f11-absent-secret", ServerIP: "198.51.100.32", Port: "20000-20100", Status: 1, CreatedTime: now},
		{ID: 33, Name: "federation", Secret: "f11-remote-secret", ServerIP: "198.51.100.33", Port: "20000-20100", Status: 1, IsRemote: 1, CreatedTime: now},
	} {
		if err := r.DB().Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	h := handler.New(r, secret, "test")
	router := httpserver.NewRouter(h, secret, r)
	admin := mustAdminToken(t, secret)

	h.ResetNodeOnlineState() // what App.Run does before serving
	status := func(id int64) int {
		return mustQueryInt(t, r, `SELECT status FROM node WHERE id = ?`, id)
	}
	if status(31) != 0 || status(32) != 0 {
		t.Fatalf("local nodes still online after startup reset: 31=%d 32=%d", status(31), status(32))
	}
	if status(33) != 1 {
		t.Fatal("federation node status must stay as synced by its panel")
	}
	listStatus := func(id int64) int {
		out := requestContractEnvelope(t, router, admin, "/api/v1/node/list", nil)
		if out.Code != 0 {
			t.Fatalf("node list %+v", out)
		}
		for _, item := range out.Data.([]interface{}) {
			m := item.(map[string]interface{})
			if contractValueAsInt64(m["id"]) == id {
				return int(contractValueAsInt64(m["status"]))
			}
		}
		t.Fatalf("node %d not listed", id)
		return -1
	}
	if listStatus(32) != 0 {
		t.Fatal("node list shows an absent node online")
	}

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	agent := dialAgent(t, "ws"+strings.TrimPrefix(server.URL, "http"), "f11-node-secret")
	waitContract(t, "agent connect → online", func() bool { return status(31) == 1 })
	if listStatus(31) != 1 || listStatus(32) != 0 {
		t.Fatal("list status does not follow live connections")
	}
	_ = agent.Close()
	waitContract(t, "agent disconnect → offline", func() bool { return status(31) == 0 })
}

func insertUserTunnelGrant(t *testing.T, r *repo.Repository, userID, tunnelID int64) {
	t.Helper()
	if err := r.DB().Exec(`INSERT INTO user_tunnel(user_id, tunnel_id, speed_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status)
		VALUES(?, ?, NULL, 999, 99999, 0, 0, 1, 2727251700000, 1)`, userID, tunnelID).Error; err != nil {
		t.Fatalf("grant user %d tunnel %d: %v", userID, tunnelID, err)
	}
}

func contractIDList(v interface{}) []int64 {
	out := []int64{}
	list, _ := v.([]interface{})
	for _, x := range list {
		out = append(out, contractValueAsInt64(x))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// F12: migrating rules on tunnel delete only to tunnels every owner may use.
func TestTunnelDeleteMigrationRespectsOwnerPermissionsContract(t *testing.T) {
	const secret = "qa136-f12"
	router, r := setupContractRouter(t, secret)
	admin := mustAdminToken(t, secret)
	now := time.Now().UnixMilli()
	ensureContractUser(t, r, 2, 1)
	if err := r.DB().Exec(`UPDATE user SET user = 'contract-user' WHERE id = 2`).Error; err != nil {
		t.Fatal(err)
	}

	source, sourceNode := seedTunnelDeleteTunnelWithNode(t, r, now, "f12-source", "f12-source-node", "25000-25010")
	forwardID := seedTunnelDeleteForward(t, r, now, source, sourceNode, "f12-user-rule", 25001)
	denied, _ := seedTunnelDeleteTunnelWithNode(t, r, now, "f12-not-granted", "f12-denied-node", "25000-25010")
	allowed, _ := seedTunnelDeleteTunnelWithNode(t, r, now, "f12-granted", "f12-allowed-node", "25000-25010")
	insertUserTunnelGrant(t, r, 2, source)
	insertUserTunnelGrant(t, r, 2, allowed)

	out := requestContractEnvelope(t, router, admin, "/api/v1/tunnel/delete-preview", map[string]interface{}{"id": source})
	if out.Code != 0 {
		t.Fatalf("preview %+v", out)
	}
	if got := contractIDList(out.Data.(map[string]interface{})["allowedTargetTunnelIds"]); len(got) != 1 || got[0] != allowed {
		t.Fatalf("allowed targets %v, want [%d]", got, allowed)
	}
	out = requestContractEnvelope(t, router, admin, "/api/v1/tunnel/batch-delete-preview", map[string]interface{}{"ids": []int64{source}})
	if got := contractIDList(out.Data.(map[string]interface{})["allowedTargetTunnelIds"]); out.Code != 0 || len(got) != 1 || got[0] != allowed {
		t.Fatalf("batch preview targets %v (%+v)", got, out)
	}

	out = requestContractEnvelope(t, router, admin, "/api/v1/tunnel/delete-with-forwards", map[string]interface{}{"id": source, "action": "replace", "targetTunnelId": denied})
	if out.Code == 0 || !strings.Contains(mustJSON(t, out), "contract-user") || !strings.Contains(mustJSON(t, out), "没有目标隧道的权限") {
		t.Fatalf("migration to an unpermitted tunnel: %s", mustJSON(t, out))
	}
	if n := mustQueryInt(t, r, `SELECT COUNT(1) FROM tunnel WHERE id = ?`, source); n != 1 {
		t.Fatal("refused migration deleted the source tunnel")
	}
	if got := mustQueryInt64(t, r, `SELECT tunnel_id FROM forward WHERE id = ?`, forwardID); got != source {
		t.Fatalf("refused migration moved the rule to %d", got)
	}

	out = requestContractEnvelope(t, router, admin, "/api/v1/tunnel/delete-with-forwards", map[string]interface{}{"id": source, "action": "replace", "targetTunnelId": allowed})
	if out.Code != 0 {
		t.Fatalf("migration to a permitted tunnel: %s", mustJSON(t, out))
	}
	if got := mustQueryInt64(t, r, `SELECT tunnel_id FROM forward WHERE id = ?`, forwardID); got != allowed {
		t.Fatalf("rule on tunnel %d after migration, want %d", got, allowed)
	}

	// Batch change tunnel by an admin re-checks the owner the same way.
	out = requestContractEnvelope(t, router, admin, "/api/v1/forward/batch-change-tunnel", map[string]interface{}{"forwardIds": []int64{forwardID}, "targetTunnelId": denied})
	if s := mustJSON(t, out); !strings.Contains(s, "没有目标隧道的权限") {
		t.Fatalf("batch change to an unpermitted tunnel: %s", s)
	}
	if got := mustQueryInt64(t, r, `SELECT tunnel_id FROM forward WHERE id = ?`, forwardID); got != allowed {
		t.Fatalf("refused batch change moved the rule to %d", got)
	}
}

// F15: a pipe FLVX still has but LeiKwan no longer lists is reported, with its
// tunnel, and can be cleaned up with the existing delete (no upstream write).
func TestLeiKwanMissingPipesListedContract(t *testing.T) {
	e := newLeiKwanEnv(t)
	id := e.account()
	now := time.Now().UnixMilli()
	if err := e.repository.DB().Exec(`INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, status, created_time, updated_time, inx, ip_preference)
		VALUES(80, '利群·广港·AWS HK', 1.0, 2, 'tls', 1, 1, ?, ?, 0, '')`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	tunnelID := int64(80)
	for _, p := range []model.LeiKwanPipe{
		{AccountID: id, ForwardID: 318, TunnelID: &tunnelID, LandingNodeID: 100, LandingPort: 20044, ListenPort: 10044, RouteLabel: "广港", Transport: "tcp", TargetHost: "8.8.8.8", ETag: `"forward-318-r1"`, UpdatedTime: now},
		{AccountID: id, ForwardID: 319, LandingNodeID: 100, LandingPort: 20045, ListenPort: 10045, RouteLabel: "沪日", Transport: "tcp", TargetHost: "8.8.8.8", ETag: `"forward-319-r1"`, UpdatedTime: now},
	} {
		if err := e.repository.DB().Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}
	out, _ := e.call("overview", e.admin, map[string]interface{}{"accountId": id})
	if out.Code != 0 {
		t.Fatal(out)
	}
	data := out.Data.(map[string]interface{})
	if len(data["forwards"].([]interface{})) != 6 {
		t.Fatal("LeiKwan forwards changed")
	}
	missing := data["missingPipes"].([]interface{})
	if len(missing) != 2 {
		t.Fatalf("missing pipes %v", missing)
	}
	byForward := map[int64]map[string]interface{}{}
	for _, m := range missing {
		item := m.(map[string]interface{})
		byForward[contractValueAsInt64(item["pipe"].(map[string]interface{})["forwardId"])] = item
	}
	if byForward[318]["tunnelName"] != "利群·广港·AWS HK" || contractValueAsInt64(byForward[318]["pipe"].(map[string]interface{})["tunnelId"]) != 80 {
		t.Fatalf("pipe 318 %v", byForward[318])
	}
	if _, w := e.fake.Counts(); w != 0 {
		t.Fatal("listing missing pipes wrote to LeiKwan")
	}
	// A pipe bound to a tunnel is not deleted here (tunnel delete handles it).
	p318, _ := e.repository.GetLeiKwanPipeByForwardID(318)
	if out, _ := e.call("pipe/delete", e.admin, map[string]interface{}{"pipeId": p318.ID, "etag": p318.ETag, "confirmed": true}); out.Code == 0 {
		t.Fatal("pipe still used by a tunnel was deleted")
	}
	// The detached one: LeiKwan answers 404, only the FLVX record goes.
	p319, _ := e.repository.GetLeiKwanPipeByForwardID(319)
	if out, _ := e.call("pipe/delete", e.admin, map[string]interface{}{"pipeId": p319.ID, "etag": p319.ETag, "confirmed": true}); out.Code != 0 {
		t.Fatalf("delete detached missing pipe %+v", out)
	}
	if p, _ := e.repository.GetLeiKwanPipeByForwardID(319); p != nil {
		t.Fatal("pipe record kept")
	}
	if _, w := e.fake.Counts(); w != 0 {
		t.Fatal("cleanup of a pipe already gone on LeiKwan wrote upstream")
	}
}

// F23: refusing a node delete names the tunnel that still uses it.
func TestNodeDeleteRefusalNamesTunnelContract(t *testing.T) {
	const secret = "qa136-f23"
	router, r := setupContractRouter(t, secret)
	admin := mustAdminToken(t, secret)
	_, nodeID := seedTunnelDeleteTunnelWithNode(t, r, time.Now().UnixMilli(), "香港入口隧道", "f23-node", "26000-26010")
	out := requestContractEnvelope(t, router, admin, "/api/v1/node/delete", map[string]interface{}{"id": nodeID})
	if out.Code == 0 || !strings.Contains(out.Msg, "「香港入口隧道」") {
		t.Fatalf("refusal %q", out.Msg)
	}
}

// F24: a save based on an older version of the user is refused.
func TestUserUpdateOptimisticVersionContract(t *testing.T) {
	const secret = "qa136-f24"
	router, r := setupContractRouter(t, secret)
	admin := mustAdminToken(t, secret)
	now := time.Now().UnixMilli()
	u := model.User{ID: 5, User: "two-tabs", Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 100, FlowResetTime: 1, Num: 10, CreatedTime: now - 1000, Status: 1}
	if err := r.DB().Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	body := func(name string, flow int, version int64) map[string]interface{} {
		return map[string]interface{}{"id": 5, "user": "two-tabs", "name": name, "flow": flow, "num": 10, "expTime": u.ExpTime, "flowResetTime": 1, "status": 1, "expectedUpdatedTime": version}
	}
	loaded := u.CreatedTime // never edited: the list reports createdTime
	if out := requestContractEnvelope(t, router, admin, "/api/v1/user/update", body("tab A note", 100, loaded)); out.Code != 0 {
		t.Fatalf("tab A save %+v", out)
	}
	out := requestContractEnvelope(t, router, admin, "/api/v1/user/update", body("", 200, loaded))
	if out.Code == 0 || !strings.Contains(out.Msg, "其他页面被修改") {
		t.Fatalf("stale tab B save not refused: %+v", out)
	}
	if name := mustQueryString(t, r, `SELECT name FROM user WHERE id = 5`); name != "tab A note" {
		t.Fatalf("tab A note overwritten: %q", name)
	}
	fresh := mustQueryInt64(t, r, `SELECT updated_time FROM user WHERE id = 5`)
	if out := requestContractEnvelope(t, router, admin, "/api/v1/user/update", body("tab A note", 200, fresh)); out.Code != 0 {
		t.Fatalf("save after reload %+v", out)
	}
	// Old clients without the field keep working.
	b := body("tab A note", 300, 0)
	delete(b, "expectedUpdatedTime")
	if out := requestContractEnvelope(t, router, admin, "/api/v1/user/update", b); out.Code != 0 {
		t.Fatalf("save without version %+v", out)
	}
}

// F27: node/info and node/report-ip with an unknown secret answer 401 instead
// of panicking on a nil node.
func TestNodeEndpointsUnknownSecretContract(t *testing.T) {
	const secret = "qa136-f27"
	router, _ := setupContractRouter(t, secret)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/node/info", ""},
		{http.MethodPost, "/api/v1/node/report-ip", `{"public_ip":"203.0.113.5"}`},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Authorization", "not-a-node-secret")
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code >= 500 || !strings.Contains(res.Body.String(), `"code":401`) || strings.Contains(res.Body.String(), "runtime error") {
			t.Fatalf("%s: http %d %s", c.path, res.Code, res.Body.String())
		}
	}
}

// F26: a failed login costs about the same whether the account exists.
func TestLoginTimingUnknownUserContract(t *testing.T) {
	e := newSecEnv(t)
	hash, err := security.HashPassword("right-password")
	if err != nil {
		t.Fatal(err)
	}
	e.addUser(t, 40, "known_bcrypt", hash, 1)
	e.addUser(t, 41, "known_md5", security.MD5("right-password"), 1)
	ip := 0
	measure := func(user string) time.Duration {
		var xs []time.Duration
		for i := 0; i < 5; i++ {
			ip++
			start := time.Now()
			out := e.login(t, user, "wrong-password", "10.136.0."+strconv.Itoa(ip))
			xs = append(xs, time.Since(start))
			if out.Code == 0 {
				t.Fatalf("%s logged in with a wrong password", user)
			}
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
		return xs[len(xs)/2]
	}
	known := measure("known_bcrypt")
	unknown := measure("no_such_user_x")
	legacy := measure("known_md5")
	t.Logf("median failed login: known bcrypt %s, unknown %s, legacy md5 %s", known, unknown, legacy)
	if unknown*2 < known || legacy*2 < known {
		t.Fatalf("unknown/legacy logins much faster than a known one: %s / %s vs %s", unknown, legacy, known)
	}
}
