package contract_test

// Plan 268: the 利群 grant is only 主入口 + 备线 (规则数 / 到期 follow the user,
// 流量 is not shown), and each 利群专线 takes its 备线隧道 from ANY tunnel with
// 「按新备线重建」 for the automatic backups (linked imported ones never move).

import (
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

func TestLeiKwanGrantBackupContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { leiKwanGrantBackupFlow(t, dialect) })
	}
}

func (e *lkLineEnv) lineByName(name string) map[string]interface{} {
	e.t.Helper()
	lines := e.ok("/api/v1/leikwan/line/list", e.admin, map[string]interface{}{})
	for _, l := range lines["lines"].([]interface{}) {
		m := l.(map[string]interface{})
		if m["name"] == name {
			return m
		}
	}
	e.t.Fatalf("line %s missing", name)
	return nil
}

func (e *lkLineEnv) saveLineTunnel(line map[string]interface{}, tunnelID int64) {
	e.t.Helper()
	e.ok("/api/v1/leikwan/line/save", e.admin, map[string]interface{}{"id": num(line["id"]), "name": line["name"], "routeId": num(line["routeId"]),
		"transport": line["transport"], "backupTunnelId": tunnelID, "accel": line["accel"]})
}

func leiKwanGrantBackupFlow(t *testing.T, dialect string) {
	e := newLKLineEnv(t, dialect)
	db := e.r.DB()
	e.ok("/api/v1/leikwan/import/apply", e.admin, map[string]interface{}{"confirmed": true})
	linkedBefore := map[int64]int64{}
	var imported []model.LeiKwanRule
	e.must(db.Find(&imported).Error)
	for _, x := range imported {
		linkedBefore[x.ForwardID] = x.BackupForwardID
	}

	// The new admin endpoint is admin-only.
	if out, _ := e.call("/api/v1/leikwan/line/backup-rebuild", e.user, map[string]interface{}{"lineId": 1}); out.Code != 403 {
		t.Fatalf("user rebuild access: %+v", out)
	}

	// --- grant: only 主入口 + 备线; old clients' num / flowGb / expTime are ignored ---
	past := time.Now().Add(-time.Hour).UnixMilli()
	saved := e.ok("/api/v1/leikwan/grant/save", e.admin, map[string]interface{}{"userId": 2, "mainEntryNodeId": 60, "backupLine": true, "num": 1, "flowGb": 100, "expTime": past})
	g := saved["grant"].(map[string]interface{})
	for _, k := range []string{"num", "flowGb", "expTime"} {
		if _, ok := g[k]; ok {
			t.Fatalf("grant view still has %s: %v", k, g)
		}
	}
	var row model.LeiKwanGrant
	e.must(db.Where("user_id = 2").First(&row).Error)
	if row.Num != 0 || row.FlowGB != 0 || row.ExpTime != 0 {
		t.Fatalf("grant stored ignored fields: %+v", row)
	}
	list := e.ok("/api/v1/leikwan/grant/list", e.admin, map[string]interface{}{})
	for _, it := range list["grants"].([]interface{}) {
		if _, ok := it.(map[string]interface{})["flowGb"]; ok {
			t.Fatalf("grant list still has flowGb: %v", it)
		}
	}

	// --- 备线隧道: any tunnel (tunnel-forward type 2 too), unknown refused ---
	gg := e.lineByName("利群·广港")
	if out, _ := e.call("/api/v1/leikwan/line/save", e.admin, map[string]interface{}{"id": num(gg["id"]), "name": "利群·广港", "routeId": 21, "transport": "tcp+udp", "backupTunnelId": 9999, "accel": gg["accel"]}); out.Code == 0 {
		t.Fatal("unknown backup tunnel accepted")
	}
	e.saveLineTunnel(gg, 4)
	if l := e.lineByName("利群·广港"); num(l["backupTunnelId"]) != 4 {
		t.Fatalf("type-2 backup tunnel not saved: %v", l)
	}
	lines := e.ok("/api/v1/leikwan/line/list", e.admin, map[string]interface{}{})
	var t4 map[string]interface{}
	for _, it := range lines["backupTunnels"].([]interface{}) {
		m := it.(map[string]interface{})
		if num(m["id"]) == 4 {
			t4 = m
		}
	}
	if t4 == nil || num(t4["type"]) != 2 || len(t4["exits"].([]interface{})) != 1 || t4["exits"].([]interface{})[0] != "AWS HK" ||
		len(t4["entryNodeIds"].([]interface{})) != 1 || num(t4["entryNodeIds"].([]interface{})[0]) != 2 {
		t.Fatalf("tunnel 4 brief: %v", t4)
	}
	if _, ok := t4["frontLatency"].([]interface{}); !ok {
		t.Fatalf("frontLatency must be an array: %v", t4)
	}
	fronts := lines["fronts"].([]interface{})
	frontNames := []string{}
	for _, f := range fronts {
		frontNames = append(frontNames, f.(map[string]interface{})["name"].(string))
	}
	if strings.Join(frontNames, ",") != "百度云广州,阿里广州" {
		t.Fatalf("fronts = grant main entries: %v", frontNames)
	}
	// Back to the port-forward tunnel 105 for the user's first rule.
	e.saveLineTunnel(e.lineByName("利群·广港"), 105)

	// --- user's 到期 / 总规则数 gate 利群 rules ---
	e.fake.Mu.Lock()
	e.fake.Options.ForwardLimit = 20
	e.fake.Mu.Unlock()
	e.fakeLimitRefresh()
	e.must(db.Model(&model.User{}).Where("id = 2").Update("exp_time", past).Error)
	opts := e.ok("/api/v1/leikwan-rule/options", e.user, map[string]interface{}{})
	if msg, _ := opts["grantError"].(string); !strings.Contains(msg, "到期") {
		t.Fatalf("expired user options: %v", opts)
	}
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": num(gg["id"]), "name": "a", "landingNodeId": 501, "requestId": "req-exp-0001"}); out.Code == 0 || !strings.Contains(out.Msg, "到期") {
		t.Fatalf("expired user created a 利群 rule: %+v", out)
	}
	if _, writes := e.fake.Counts(); writes != 0 {
		t.Fatalf("refused create wrote to LeiKwan: %d", writes)
	}
	// Plan 280: 利群 rules count only against 利群转发数 (leikwan_num), not
	// against 总转发数 (num). num = the user's normal forwards + 1, leikwan_num 1.
	var normalForwards int64
	e.must(db.Model(&model.Forward{}).Where("user_id = 2").Count(&normalForwards).Error)
	e.must(db.Model(&model.User{}).Where("id = 2").Updates(map[string]interface{}{"exp_time": time.Now().Add(24 * time.Hour).UnixMilli(), "num": normalForwards + 1, "leikwan_num": 1}).Error)
	if lim, _ := e.ok("/api/v1/leikwan-rule/options", e.user, map[string]interface{}{})["ruleLimit"].(map[string]interface{}); lim == nil || num(lim["limit"]) != 1 || num(lim["used"]) != 0 || lim["unlimited"] != false {
		t.Fatalf("options ruleLimit before: %v", lim)
	}
	created := e.ok("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": num(gg["id"]), "name": "我的广港", "landingNodeId": 501, "requestId": "req-num-0001"})
	rule := created["rule"].(map[string]interface{})
	if rule["userExpired"] != false {
		t.Fatalf("fresh user rule shows expired: %v", rule)
	}
	// leikwan_num = 1 is used: the options say so up front and a second one is refused…
	opts = e.ok("/api/v1/leikwan-rule/options", e.user, map[string]interface{}{})
	if msg, _ := opts["grantError"].(string); msg != "已达到利群转发数上限（1 条），请删除不用的利群转发后再添加" || opts["userBlocked"] != true {
		t.Fatalf("options at 利群 limit: %v", opts)
	}
	if lim, _ := opts["ruleLimit"].(map[string]interface{}); lim == nil || num(lim["limit"]) != 1 || num(lim["used"]) != 1 {
		t.Fatalf("options ruleLimit after: %v", lim)
	}
	if out, _ := e.call("/api/v1/leikwan-rule/create", e.user, map[string]interface{}{"lineId": num(gg["id"]), "name": "第二条", "landingNodeId": 502, "requestId": "req-num-0002"}); out.Code == 0 || !strings.Contains(out.Msg, "利群转发数上限") {
		t.Fatalf("second 利群 rule past user.leikwan_num: %+v", out)
	}
	// …while a normal forward still fits 总转发数 (利群 rules are not counted there).
	if err := e.r.CheckUserRuleLimit(2, 1); err != nil {
		t.Fatalf("normal forward limit counts 利群 rules: %v", err)
	}
	// The admin actor is exempt from the count (not from 到期).
	e.ok("/api/v1/leikwan-rule/create", e.admin, map[string]interface{}{"lineId": num(gg["id"]), "name": "管理员代建", "landingNodeId": 502, "userId": 2, "requestId": "req-num-0003"})
	pkg := e.ok("/api/v1/user/package", e.user, map[string]interface{}{})
	info := pkg["userInfo"].(map[string]interface{})
	if n := num(info["leikwanRuleCount"]); n != 2 || num(info["leikwanNum"]) != 1 {
		t.Fatalf("dashboard 利群 count / limit: %v / %v", info["leikwanRuleCount"], info["leikwanNum"])
	}
	// An expired user's rules show 已到期.
	e.must(db.Model(&model.User{}).Where("id = 2").Update("exp_time", past).Error)
	mine := e.ok("/api/v1/leikwan-rule/list", e.user, map[string]interface{}{})["rules"].([]interface{})
	if len(mine) != 2 || mine[0].(map[string]interface{})["userExpired"] != true {
		t.Fatalf("expired rules view: %v", mine)
	}
	e.must(db.Model(&model.User{}).Where("id = 2").Update("exp_time", time.Now().Add(24*time.Hour).UnixMilli()).Error)

	// --- auto backups use the line's tunnel; 「按新备线重建」 moves only them ---
	var mineRows []model.LeiKwanRule
	e.must(db.Where("user_id = 2").Order("id").Find(&mineRows).Error)
	oldBackups := []int64{}
	for _, x := range mineRows {
		var b model.Forward
		e.must(db.First(&b, x.BackupForwardID).Error)
		if x.BackupMode != "auto" || b.TunnelID != 105 || b.UserID != 1 {
			t.Fatalf("auto backup not on the line's tunnel: %+v %+v", x, b)
		}
		oldBackups = append(oldBackups, x.BackupForwardID)
	}
	e.saveLineTunnel(e.lineByName("利群·广港"), 90)
	st := e.lineByName("利群·广港")["backupStats"].(map[string]interface{})
	if num(st["stale"]) != 2 || num(st["auto"]) != 2 || num(st["linked"]) != 3 {
		t.Fatalf("backup stats after tunnel change: %v", st)
	}
	// Changing the tunnel alone moves nothing.
	for _, id := range oldBackups {
		if e.forwardCount("id = ? AND tunnel_id = 105", id) != 1 {
			t.Fatal("line save moved an auto backup by itself")
		}
	}
	res := e.ok("/api/v1/leikwan/line/backup-rebuild", e.admin, map[string]interface{}{"lineId": num(gg["id"])})
	counts := res["result"].(map[string]interface{})
	if num(counts["rebuilt"]) != 2 || num(counts["linked"]) != 3 || len(res["warnings"].([]interface{})) != 0 {
		t.Fatalf("rebuild: %v", res)
	}
	for _, id := range oldBackups {
		if e.forwardCount("id = ?", id) != 0 {
			t.Fatalf("old auto backup %d left behind", id)
		}
	}
	e.must(db.Where("user_id = 2").Order("id").Find(&mineRows).Error)
	for _, x := range mineRows {
		var b model.Forward
		e.must(db.First(&b, x.BackupForwardID).Error)
		if b.TunnelID != 90 || x.BackupMode != "auto" || !strings.HasPrefix(b.Name, "備線·") {
			t.Fatalf("rebuilt backup: %+v %+v", x, b)
		}
	}
	if st := e.lineByName("利群·广港")["backupStats"].(map[string]interface{}); num(st["stale"]) != 0 {
		t.Fatalf("stale after rebuild: %v", st)
	}
	// Rebuild onto a tunnel-forward (type 2) tunnel works the same way.
	e.saveLineTunnel(e.lineByName("利群·广港"), 4)
	res = e.ok("/api/v1/leikwan/line/backup-rebuild", e.admin, map[string]interface{}{"lineId": num(gg["id"])})
	if num(res["result"].(map[string]interface{})["rebuilt"]) != 2 {
		t.Fatalf("rebuild onto type 2: %v", res)
	}
	// No backup tunnel any more → the auto backups are removed.
	e.saveLineTunnel(e.lineByName("利群·广港"), 0)
	res = e.ok("/api/v1/leikwan/line/backup-rebuild", e.admin, map[string]interface{}{"lineId": num(gg["id"])})
	if num(res["result"].(map[string]interface{})["removed"]) != 2 {
		t.Fatalf("rebuild without tunnel: %v", res)
	}
	e.must(db.Where("user_id = 2").Find(&mineRows).Error)
	for _, x := range mineRows {
		if x.BackupForwardID != 0 {
			t.Fatalf("auto backup kept without a tunnel: %+v", x)
		}
	}

	// Linked imported backups never moved and still exist.
	var after []model.LeiKwanRule
	e.must(db.Where("user_id = 1").Find(&after).Error)
	for _, x := range after {
		if linkedBefore[x.ForwardID] != x.BackupForwardID || x.BackupMode != "linked" {
			t.Fatalf("linked backup changed: %+v (was %d)", x, linkedBefore[x.ForwardID])
		}
	}
	for _, id := range []int64{148, 595, 601, 587, 599, 619} {
		var b model.Forward
		e.must(db.First(&b, id).Error)
		want := map[int64]int64{148: 90, 619: 90, 595: 105, 601: 105, 587: 4, 599: 4}[id]
		if b.TunnelID != want {
			t.Fatalf("linked backup %d moved to tunnel %d", id, b.TunnelID)
		}
	}
}
