package contract_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

func (e *ruleLimitEnv) addTunnel(t *testing.T, name string, grantUsers ...int64) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	tunnel := model.Tunnel{Name: name, TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1}
	if err := e.r.DB().Create(&tunnel).Error; err != nil {
		t.Fatal(err)
	}
	var nodeID int64
	if err := e.r.DB().Model(&model.Node{}).Select("id").Where("name = ?", "rl-node").Scan(&nodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.r.DB().Create(&model.ChainTunnel{TunnelID: tunnel.ID, ChainType: "1", NodeID: nodeID, Port: sql.NullInt64{Int64: 20002, Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}}).Error; err != nil {
		t.Fatal(err)
	}
	for _, uid := range grantUsers {
		if err := e.r.DB().Create(&model.UserTunnel{UserID: uid, TunnelID: tunnel.ID, Num: 10, Flow: 99999, FlowResetTime: 1, ExpTime: now + 86400000*365, Status: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return tunnel.ID
}

func (e *ruleLimitEnv) forward(t *testing.T, id int64) model.Forward {
	t.Helper()
	var f model.Forward
	if err := e.r.DB().First(&f, id).Error; err != nil {
		t.Fatalf("forward %d: %v", id, err)
	}
	return f
}

// A normal user (B) must not read, change or delete another user's (A's)
// rules, nor reach admin-only settings. Each hole found in plan 130 has a case.
func TestNormalUserCannotTouchOthersOrAdminSettings(t *testing.T) {
	e := newRuleLimitEnv(t, "sqlite")
	const userA, userB = int64(401), int64(402)
	tokA := e.addUser(t, userA, 10)
	tokB := e.addUser(t, userB, 10)
	victim := e.seedRule(t, userA, "victim", 1)
	if err := e.r.DB().Model(&model.Forward{}).Where("id = ?", victim).Updates(map[string]interface{}{"in_flow": 123, "out_flow": 456, "inx": 7}).Error; err != nil {
		t.Fatal(err)
	}
	other := e.addTunnel(t, "rl-tunnel-b", userB)
	ungranted := e.addTunnel(t, "rl-tunnel-none")
	before := e.forward(t, victim)

	unchanged := func(label string) {
		t.Helper()
		after := e.forward(t, victim)
		if after.Name != before.Name || after.Status != before.Status || after.TunnelID != before.TunnelID || after.InFlow != before.InFlow || after.OutFlow != before.OutFlow || after.Inx != before.Inx || after.UserID != userA {
			t.Fatalf("%s changed another user's rule: %+v -> %+v", label, before, after)
		}
	}
	refused := func(label, path string, body interface{}) {
		t.Helper()
		if out := e.call(t, tokB, path, body); out.Code == 0 {
			t.Fatalf("%s: normal user call succeeded: %+v", label, out)
		}
		unchanged(label)
	}
	batchNoop := func(label, path string, body interface{}) {
		t.Helper()
		out := e.call(t, tokB, path, body)
		raw, _ := json.Marshal(out.Data)
		if out.Code == 0 && (strings.Contains(string(raw), `"successCount":1`) || strings.Contains(string(raw), `"success":true`)) {
			t.Fatalf("%s: batch call touched another user's rule: %s", label, raw)
		}
		unchanged(label)
	}

	// Single-rule calls on A's rule.
	refused("update", "/api/v1/forward/update", map[string]interface{}{"id": victim, "name": "hacked", "tunnelId": e.tunnelID, "remoteAddr": "9.9.9.9:53"})
	refused("pause", "/api/v1/forward/pause", map[string]interface{}{"id": victim})
	refused("resume", "/api/v1/forward/resume", map[string]interface{}{"id": victim})
	refused("delete", "/api/v1/forward/delete", map[string]interface{}{"id": victim})
	refused("force-delete", "/api/v1/forward/force-delete", map[string]interface{}{"id": victim})
	refused("client-ips", "/api/v1/forward/client-ips", map[string]interface{}{"id": victim})
	refused("diagnose", "/api/v1/forward/diagnose", map[string]interface{}{"forwardId": victim, "id": victim})
	refused("reset logs", "/api/v1/forward/traffic-reset-logs", map[string]interface{}{"forwardId": victim})
	// Batch calls on A's rule.
	batchNoop("batch-delete", "/api/v1/forward/batch-delete", map[string]interface{}{"ids": []int64{victim}})
	batchNoop("batch-pause", "/api/v1/forward/batch-pause", map[string]interface{}{"ids": []int64{victim}})
	batchNoop("batch-resume", "/api/v1/forward/batch-resume", map[string]interface{}{"ids": []int64{victim}})
	batchNoop("batch-redeploy", "/api/v1/forward/batch-redeploy", map[string]interface{}{"ids": []int64{victim}})
	batchNoop("batch-change-tunnel", "/api/v1/forward/batch-change-tunnel", map[string]interface{}{"forwardIds": []int64{victim}, "targetTunnelId": other})
	batchNoop("batch-reset-traffic", "/api/v1/forward/batch-reset-traffic", map[string]interface{}{"forwardIds": []int64{victim}})
	// Hole 1 (fixed): update-order had no owner check.
	e.call(t, tokB, "/api/v1/forward/update-order", map[string]interface{}{"forwards": []map[string]interface{}{{"id": victim, "inx": -99}}})
	unchanged("update-order")
	// List never shows A's rule to B.
	out := e.call(t, tokB, "/api/v1/forward/list", map[string]interface{}{})
	if raw, _ := json.Marshal(out.Data); strings.Contains(string(raw), `"victim"`) {
		t.Fatalf("forward list leaked another user's rule: %s", raw)
	}
	// userId in a normal user's create is ignored: the rule is B's.
	if out := e.create(t, tokB, "owned-by-b", map[string]interface{}{"userId": userA}); out.Code != 0 {
		t.Fatalf("own create: %+v", out)
	}
	var owner int64
	if err := e.r.DB().Model(&model.Forward{}).Select("user_id").Where("name = ?", "owned-by-b").Scan(&owner).Error; err != nil || owner != userB {
		t.Fatalf("create with foreign userId stored owner %d (%v)", owner, err)
	}
	// Ungranted tunnel and an entry port outside the node's range.
	if out := e.call(t, tokB, "/api/v1/forward/create", map[string]interface{}{"tunnelId": ungranted, "name": "x", "remoteAddr": "1.1.1.1:53"}); out.Code == 0 {
		t.Fatal("create on ungranted tunnel succeeded")
	}
	if out := e.create(t, tokB, "bad-port", map[string]interface{}{"inPort": 30500}); out.Code == 0 {
		t.Fatal("create with an entry port outside the node range succeeded")
	}

	// Hole 3 (fixed): reset logs of other users' rules could be deleted.
	if err := e.r.DB().Create(&model.ForwardTrafficResetLog{ForwardID: victim, ForwardName: "victim", UserID: userA, UserName: "rl_user_401", ResetTime: time.Now().UnixMilli(), OperatorID: userA, OperatorName: "a", Reason: "手动归零", CreatedTime: time.Now().UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	var logID int64
	e.r.DB().Model(&model.ForwardTrafficResetLog{}).Select("id").Where("forward_id = ?", victim).Scan(&logID)
	if out := e.call(t, tokB, "/api/v1/forward/traffic-reset-log/delete", map[string]interface{}{"id": logID}); out.Code == 0 {
		t.Fatal("deleted another user's reset log")
	}
	if out := e.call(t, tokA, "/api/v1/forward/traffic-reset-log/delete", map[string]interface{}{"id": logID}); out.Code != 0 {
		t.Fatalf("owner cannot delete own reset log: %+v", out)
	}

	// Hole 2 (fixed): resetting counters lifted the admin's per-rule traffic limit.
	limited := e.seedRule(t, userB, "b-limited", 1)
	plain := e.seedRule(t, userB, "b-plain", 1)
	e.r.DB().Model(&model.Forward{}).Where("id IN ?", []int64{limited, plain}).Updates(map[string]interface{}{"in_flow": 10, "out_flow": 10})
	e.r.DB().Model(&model.Forward{}).Where("id = ?", limited).Update("traffic_limit", 1)
	e.call(t, tokB, "/api/v1/forward/batch-reset-traffic", map[string]interface{}{"forwardIds": []int64{limited, plain}})
	if f := e.forward(t, limited); f.InFlow != 10 || f.OutFlow != 10 {
		t.Fatalf("user reset a rule with an admin traffic limit: %+v", f)
	}
	if f := e.forward(t, plain); f.InFlow != 0 || f.OutFlow != 0 {
		t.Fatalf("user could not reset an ordinary rule: %+v", f)
	}
	if out := e.call(t, e.admin, "/api/v1/forward/batch-reset-traffic", map[string]interface{}{"forwardIds": []int64{limited}}); out.Code != 0 {
		t.Fatalf("admin reset: %+v", out)
	}
	if f := e.forward(t, limited); f.InFlow != 0 {
		t.Fatalf("admin reset did not apply: %+v", f)
	}

	// Admin-only settings: 规则数 / flow / per-tunnel grant / landing caps / nodes / speed limits.
	var utB int64
	e.r.DB().Model(&model.UserTunnel{}).Select("id").Where("user_id = ? AND tunnel_id = ?", userB, e.tunnelID).Scan(&utB)
	for _, c := range []struct {
		path string
		body map[string]interface{}
	}{
		{"/api/v1/user/update", map[string]interface{}{"id": userB, "user": "rl_user_402", "num": 9999, "flow": 9999}},
		{"/api/v1/tunnel/user/update", map[string]interface{}{"id": utB, "num": 9999, "flow": 9999}},
		{"/api/v1/tunnel/user/assign", map[string]interface{}{"userId": userB, "tunnelId": ungranted}},
		{"/api/v1/landing/user/save", map[string]interface{}{"userId": userB, "maxNodes": 99}},
		{"/api/v1/landing/node/update", map[string]interface{}{"id": 1, "maxConnections": 0}},
		{"/api/v1/node/update", map[string]interface{}{"id": 1, "port": "1-65535"}},
		{"/api/v1/speed-limit/create", map[string]interface{}{"name": "x", "speed": 1}},
		{"/api/v1/user/update-order", map[string]interface{}{"users": []map[string]interface{}{{"id": userA, "inx": -5}}}}, // hole 4
		{"/api/v1/tls-template/list", map[string]interface{}{}},                                                            // hole 5
	} {
		if out := e.call(t, tokB, c.path, c.body); out.Code == 0 {
			t.Fatalf("normal user reached admin-only %s: %+v", c.path, out)
		}
	}
	var u model.User
	e.r.DB().First(&u, userB)
	if u.Num != 10 || u.Flow != 99999 {
		t.Fatalf("normal user changed own limits: num=%d flow=%d", u.Num, u.Flow)
	}
	var grants int64
	e.r.DB().Model(&model.UserTunnel{}).Where("user_id = ? AND tunnel_id = ?", userB, ungranted).Count(&grants)
	if grants != 0 {
		t.Fatal("normal user granted itself a tunnel")
	}

	// Hole 6 (fixed): auto-renew / auto-buy toggles took any user id.
	if out := e.call(t, tokB, "/api/v1/user/toggle-auto-renew", map[string]interface{}{"id": userA, "autoRenew": 1}); out.Code == 0 {
		t.Fatal("toggled another user's auto-renew")
	}
	if out := e.call(t, tokB, "/api/v1/user/toggle-auto-buy-traffic", map[string]interface{}{"id": userA, "autoBuyTraffic": 1}); out.Code == 0 {
		t.Fatal("toggled another user's auto-buy")
	}
	var a model.User
	e.r.DB().First(&a, userA)
	if a.AutoRenew != 0 || a.AutoBuyTraffic != 0 {
		t.Fatalf("another user's toggles changed: %+v", a)
	}
	if out := e.call(t, tokB, "/api/v1/user/toggle-auto-renew", map[string]interface{}{"id": userB, "autoRenew": 1}); out.Code != 0 {
		t.Fatalf("own auto-renew toggle: %+v", out)
	}
	// Hole 8 (fixed): group ids of any user.
	if out := e.call(t, tokB, "/api/v1/user/groups", map[string]interface{}{"id": userA}); out.Code == 0 {
		t.Fatal("read another user's groups")
	}

	// Hole 7 (fixed): the shop's payment config exposed merchant keys.
	now := time.Now().UnixMilli()
	e.r.DB().Create(&model.PaymentConfig{Channel: "YIPAY", Config: `{"gateway_url":"https://pay.example","pid":"1001","key":"MERCHANT-SECRET","notify_url":"https://n"}`, Enabled: 1, CreatedAt: now, UpdatedAt: now})
	e.r.DB().Create(&model.PaymentConfig{Channel: "USDT", Config: `{"pid":"9","secret_key":"GM-SECRET","token":"TOK","network":"TRC20","currency":"USDT"}`, Enabled: 1, CreatedAt: now, UpdatedAt: now})
	out = e.call(t, tokB, "/api/v1/payment/config", map[string]interface{}{})
	raw, _ := json.Marshal(out.Data)
	if out.Code != 0 || strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "TOK") || strings.Contains(string(raw), "1001") || !strings.Contains(string(raw), "TRC20") {
		t.Fatalf("public payment config: code=%d %s", out.Code, raw)
	}

	// Hole 9 (fixed): order status / pay of another user's order.
	order := model.Order{OrderNo: "RL-ORDER-1", UserID: userA, UserName: "a", ProductID: 1, ProductName: "p", ProductType: "package", Amount: 100, PayCurrency: "YIPAY", CreatedAt: now, UpdatedAt: now}
	if err := e.r.DB().Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/order/status", "/api/v1/payment/pay"} {
		if out := e.call(t, tokB, path, map[string]interface{}{"order_id": order.ID}); out.Code == 0 || out.Msg != "订单不存在" {
			t.Fatalf("%s on another user's order: %+v", path, out)
		}
	}
	if out := e.call(t, tokA, "/api/v1/order/status", map[string]interface{}{"order_id": order.ID}); out.Code != 0 {
		t.Fatalf("own order status: %+v", out)
	}
}
