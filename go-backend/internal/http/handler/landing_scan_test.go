package handler

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// TestLandingScanGuardFlow (plan 202): scan_guard only goes to agents with
// landing_scan_guard; a reported hit disables the node at once (status
// disabled + reason, port kept), is recorded once, the panel pushes again with
// the seen mark; the owner sees the 联系管理员 message and cannot lift it;
// 解除禁用 (button or status edit) re-enables and logs who did it.
func TestLandingScanGuardFlow(t *testing.T) {
	e := newFlowTestEnv(t)
	guardSeed(t, e)
	// The owner may use the server (the policy pass keeps unblocked nodes on).
	if err := e.r.DB().Create(&model.LandingServerUser{LandingServerID: 1, UserID: 2}).Error; err != nil {
		t.Fatal(err)
	}
	const seq = int64(1793709225987084)
	var mu sync.Mutex
	var payloads []map[string]interface{}
	var report []map[string]any // hits the fake agent still holds
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		mu.Lock()
		defer mu.Unlock()
		payload := data.(map[string]interface{})
		payloads = append(payloads, payload)
		out := map[string]interface{}{"revision": payload["revision"], "epoch": "1700000000000000001", "state": "running", "nodes": []repo.LandingCounter{}, "egress": map[string]any{"state": "applied", "ready": true}}
		if sg, ok := payload["scan_guard"].(map[string]interface{}); ok {
			seen := sg["seen"].(int64)
			pending := []map[string]any{}
			for _, h := range report {
				if h["seq"].(int64) > seen {
					pending = append(pending, h)
				}
			}
			report = pending
			out["scan_guard"] = map[string]any{"state": "on", "hits": pending}
		}
		return ws.CommandResult{Success: true, Data: out}, nil
	}
	last := func() map[string]interface{} {
		mu.Lock()
		defer mu.Unlock()
		return payloads[len(payloads)-1]
	}
	sync := func() {
		t.Helper()
		if _, err := e.h.syncLandingServer(1); err != nil {
			t.Fatal(err)
		}
		e.h.egressSyncWG.Wait()
	}
	nodeRulesScan := func(uid int64, role int, node int64) map[string]any {
		t.Helper()
		v := dataMap(t, layerCall(t, e.h, "/api/v1/landing/node/rules/get", map[string]any{"id": node}, uid, role))
		return v["builtin"].(map[string]any)["scanGuard"].(map[string]any)
	}

	// Old agent: nothing new in the payload, the row says 需要升级节点.
	sync()
	if _, ok := last()["scan_guard"]; ok {
		t.Fatal("scan_guard sent to an agent without landing_scan_guard")
	}
	if sg := nodeRulesScan(2, 1, 4); sg["state"] != "agent" || sg["on"] != true || sg["horizontal"].(float64) != 100 {
		t.Fatalf("old agent row %v", sg)
	}

	// Capable agent: guard on for every SS2022 / VLESS node of the server.
	e.exec("UPDATE node SET landing_scan_guard=1 WHERE id=2")
	sync()
	sg, ok := last()["scan_guard"].(map[string]interface{})
	if !ok || sg["enabled"] != true || strings.Join(sg["identities"].([]string), ",") != "flvxl-1,flvxl-2,flvxl-3,flvxl-4" || sg["seen"].(int64) != 0 ||
		sg["horizontal"].(int) != 100 || sg["horizontal_web"].(int) != 500 || sg["vertical"].(int) != 30 || sg["total"].(int) != 1000 || sg["total_window_s"].(int) != 300 {
		t.Fatalf("scan_guard %v", sg)
	}
	rev := last()["revision"]
	sync()
	if last()["revision"] != rev {
		t.Fatal("unchanged guard moved the revision")
	}
	if s, _ := e.r.GetLandingServer(1); s.ScanState != "on" {
		t.Fatalf("scan state %q", s.ScanState)
	}
	if sg := nodeRulesScan(2, 1, 4); sg["state"] != "on" || sg["label"] != "生效中" {
		t.Fatalf("user row %v", sg)
	}

	// The agent reports a hit on node 4: disabled at once, pushed again with
	// the seen mark, recorded once.
	mu.Lock()
	report = []map[string]any{{"id": 4, "inbound_tag": "flvxl-4", "type": "horizontal", "count": 100, "port": 22, "window_s": 60, "at": "2026-11-03T12:30:00Z", "seq": seq}}
	mu.Unlock()
	sync()
	n, _ := e.r.GetLandingNode(4)
	if n.Status != "disabled" || n.AutoDisabled || n.ScanBlockedAt == 0 || n.Port != 30004 {
		t.Fatalf("node after hit %+v", n)
	}
	if sg := last()["scan_guard"].(map[string]interface{}); sg["seen"].(int64) != seq {
		t.Fatalf("seen not pushed back: %v", sg["seen"])
	}
	for _, d := range last()["nodes"].([]map[string]interface{}) {
		if d["inbound_tag"] == "flvxl-4" && d["enabled"] != false {
			t.Fatal("blocked node still enabled in the payload")
		}
	}
	events, total, err := e.r.ListLandingScanEvents(0, 10)
	if err != nil || total != 1 || !events[0].Blocked || events[0].LandingNodeID != 4 || events[0].Port != 22 {
		t.Fatalf("events %v %d %v", events, total, err)
	}
	sync()
	if _, total, _ := e.r.ListLandingScanEvents(0, 10); total != 1 {
		t.Fatal("hit recorded twice")
	}
	// The lifecycle pass keeps it disabled.
	e.h.reconcileLandingPolicy(time.Now())
	e.h.egressSyncWG.Wait()
	if n, _ := e.r.GetLandingNode(4); n.Status != "disabled" || n.ScanBlockedAt == 0 {
		t.Fatal("policy pass re-enabled a scan-blocked node")
	}

	// Owner view: the message; owner cannot lift it.
	var list []map[string]any
	raw, _ := json.Marshal(layerCall(t, e.h, "/api/v1/landing/node/list", map[string]any{}, 2, 1).Data)
	_ = json.Unmarshal(raw, &list)
	var mine map[string]any
	for _, v := range list {
		if v["id"].(float64) == 4 {
			mine = v
		}
	}
	block, _ := mine["scanBlock"].(map[string]any)
	if block == nil || block["message"] != "该落地节点因检测到端口扫描（同一端口 22 在 60 秒内连接了 100 个不同 IP）已被禁用，如有疑问请联系管理员" || block["typeLabel"] != "横向" {
		t.Fatalf("owner view %v", mine["scanBlock"])
	}
	if out := layerCall(t, e.h, "/api/v1/landing/node/scan/unblock", map[string]any{"id": 4}, 2, 1); out.Code == 0 {
		t.Fatal("owner lifted the scan block")
	}
	if out := layerCall(t, e.h, "/api/v1/landing/node/update", map[string]any{"id": 4, "status": "active"}, 2, 1); out.Code == 0 {
		t.Fatal("owner re-enabled the node")
	}
	if out := layerCall(t, e.h, "/api/v1/landing/scan/events", map[string]any{}, 2, 1); out.Code == 0 {
		t.Fatal("owner read the event list")
	}

	// Admin view: counts + server state.
	admin := dataMap(t, layerCall(t, e.h, "/api/v1/landing/scan/get", map[string]any{}, 1, 0))
	if admin["blockedNodes"].(float64) != 1 || admin["events"].(float64) != 1 || admin["servers"].([]any)[0].(map[string]any)["state"] != "on" {
		t.Fatalf("admin view %v", admin)
	}

	// 解除禁用 by the admin.
	out := layerCall(t, e.h, "/api/v1/landing/node/scan/unblock", map[string]any{"id": 4}, 1, 0)
	if out.Code != 0 {
		t.Fatal(out.Msg)
	}
	e.h.egressSyncWG.Wait()
	if n, _ := e.r.GetLandingNode(4); n.Status != "active" || n.ScanBlockedAt != 0 || n.ScanBlock != "" {
		t.Fatalf("after unblock %+v", n)
	}
	for _, d := range last()["nodes"].([]map[string]interface{}) {
		if d["inbound_tag"] == "flvxl-4" && d["enabled"] != true {
			t.Fatal("unblocked node not enabled in the payload")
		}
	}
	events, _, _ = e.r.ListLandingScanEvents(4, 10)
	if events[0].UnblockedAt == 0 || events[0].UnblockedBy == "" || events[0].NodeBlocked {
		t.Fatalf("unblock not logged %+v", events[0])
	}
	if out := layerCall(t, e.h, "/api/v1/landing/node/scan/unblock", map[string]any{"id": 4}, 1, 0); out.Code == 0 {
		t.Fatal("unblock of a node that is not blocked accepted")
	}

	// A second hit, lifted through the admin status edit.
	mu.Lock()
	report = []map[string]any{{"id": 4, "inbound_tag": "flvxl-4", "type": "vertical", "count": 30, "host": "198.51.100.7", "window_s": 60, "at": "2026-11-03T12:40:00Z", "seq": seq + 5}}
	mu.Unlock()
	sync()
	if n, _ := e.r.GetLandingNode(4); n.ScanBlockedAt == 0 || !strings.Contains(repo.LandingScanBlockOf(n).Reason(), "198.51.100.7") {
		t.Fatal("second hit not applied")
	}
	if out := layerCall(t, e.h, "/api/v1/landing/node/update", map[string]any{"id": 4, "status": "active"}, 1, 0); out.Code != 0 {
		t.Fatal(out.Msg)
	}
	e.h.egressSyncWG.Wait()
	if n, _ := e.r.GetLandingNode(4); n.Status != "active" || n.ScanBlockedAt != 0 {
		t.Fatalf("status edit did not lift %+v", n)
	}
	if events, _, _ := e.r.ListLandingScanEvents(4, 1); events[0].UnblockedBy == "" {
		t.Fatal("status edit unblock not logged")
	}

	// Settings: invalid thresholds rejected; switching off empties the list.
	if out := layerCall(t, e.h, "/api/v1/landing/scan/save", map[string]any{"on": true, "horizontal": 5, "horizontalWeb": 500, "vertical": 30, "total": 1000}, 1, 0); out.Code == 0 {
		t.Fatal("horizontal 5 accepted")
	}
	if out := layerCall(t, e.h, "/api/v1/landing/scan/save", map[string]any{"on": false, "horizontal": 150, "horizontalWeb": 600, "vertical": 40, "total": 2000}, 1, 0); out.Code != 0 {
		t.Fatal(out.Msg)
	}
	sync()
	sg = last()["scan_guard"].(map[string]interface{})
	if sg["enabled"] != false || len(sg["identities"].([]string)) != 0 || sg["total"].(int) != 2000 {
		t.Fatalf("switched-off payload %v", sg)
	}
	if v := nodeRulesScan(2, 1, 4); v["state"] != "off" || v["total"].(float64) != 2000 {
		t.Fatalf("off row %v", v)
	}
}

// TestLandingScanSharedAndNotice: a shared server shows 共享服务器暂不支持;
// the Telegram text lists at most 10 nodes.
func TestLandingScanSharedAndNotice(t *testing.T) {
	state, label := landingScanServerState(&model.LandingServer{Source: "shared"}, nil, true)
	if state != "shared" || label != "共享服务器暂不支持" {
		t.Fatal(state, label)
	}
	lines := []string{}
	for i := 0; i < 12; i++ {
		lines = append(lines, "· n")
	}
	text := landingScanNoticeText(lines)
	if !strings.HasPrefix(text, "⚠️ 12 个落地节点因端口扫描已被禁用") || strings.Count(text, "· n") != 10 || !strings.Contains(text, "另有 2 个") {
		t.Fatal(text)
	}
	if one := landingScanNoticeText(lines[:1]); !strings.HasPrefix(one, "⚠️ 落地节点因端口扫描已被禁用") {
		t.Fatal(one)
	}
}
