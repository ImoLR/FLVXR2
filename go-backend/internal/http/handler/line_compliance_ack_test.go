package handler

import (
	"strings"
	"testing"

	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 164: admin 「已确认合规」 on a hand-typed 线路机 rule target.
func TestLineComplianceAck(t *testing.T) {
	e, _ := newChainEnv(t)
	e.exec(`INSERT INTO landing_server(id, source, node_id, name, public_host, status, revision, created_time, updated_time, protocols) VALUES (50, 'native', 2, 'land', '192.0.2.2', 'active', 1, 1, 1, '["ss2022","vless_reality_vision"]')`)
	e.exec(`INSERT INTO landing_node(id, user_id, landing_server_id, name, protocol, port, inbound_tag, status, created_time, updated_time) VALUES
		(91, 7, 50, 'ss', 'ss2022', 31001, 'flvxl-91', 'active', 1, 1)`)
	// 700: hand-typed target on 线路机 tunnel 10 (like prod #109) → ackable.
	// 701: SS landing node on tunnel 10 → 不合规, but the panel knows it.
	// 702: plain tunnel 30 → not a 线路机 rule.
	// 703: second hand-typed rule on 线路机 tunnel 20.
	for _, f := range [][3]int64{{700, 10, 1800}, {701, 10, 1801}, {702, 30, 1802}, {703, 20, 1803}} {
		e.addForward(f[0], 7, f[1])
		node := int64(1)
		if f[1] == 30 {
			node = 5
		}
		e.exec("INSERT INTO forward_port(forward_id, node_id, port) VALUES(?, ?, ?)", f[0], node, f[2])
	}
	e.exec("UPDATE forward SET landing_node_id = 91, remote_addr = '192.0.2.2:31001' WHERE id = 701")
	e.exec("UPDATE forward SET remote_addr = 'node.example.com:10020' WHERE id = 700")

	sig := func(id int64) string {
		t.Helper()
		var s string
		if err := e.r.DB().Raw("SELECT line_compliance_ack_sig FROM forward WHERE id = ?", id).Row().Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	decorate := func(admin bool) (int, map[int64]map[string]interface{}) {
		t.Helper()
		items, err := e.r.ListForwards()
		if err != nil {
			t.Fatal(err)
		}
		n := e.h.decorateForwardChains(items, admin)
		byID := map[int64]map[string]interface{}{}
		for _, item := range items {
			byID[asInt64(item["id"], 0)] = item
		}
		return n, byID
	}
	listAs := func(uid int64, role int) map[string]interface{} {
		t.Helper()
		e.h.wsServer = ws.NewServer(e.r, "test") // the rule list reads live metrics
		defer func() { e.h.wsServer = nil }()
		code, msg, data := callAs(t, e.h.forwardList, uid, role, map[string]interface{}{})
		if code != 0 {
			t.Fatalf("list: %d %s", code, msg)
		}
		return data
	}
	listItem := func(data map[string]interface{}, id int64) map[string]interface{} {
		for _, raw := range data["items"].([]interface{}) {
			if item := raw.(map[string]interface{}); asInt64(item["id"], 0) == id {
				return item
			}
		}
		t.Fatalf("rule %d not listed", id)
		return nil
	}

	if n, items := decorate(true); n != 3 || items[700]["lineNonCompliant"] != true || items[701]["lineNonCompliant"] != true || items[703]["lineNonCompliant"] != true || items[702]["lineNonCompliant"] != nil {
		t.Fatalf("before: count %d, items %v", n, items)
	}

	// Non-admins can neither set it through the endpoint nor the rule dialog.
	if code, _, _ := callAs(t, e.h.forwardLineComplianceAck, 7, 1, map[string]interface{}{"id": 700, "ack": true}); code != 403 {
		t.Fatalf("user endpoint: code %d", code)
	}
	if code, msg, _ := callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": 700, "name": "usb", "tunnelId": 10, "inPort": 1800, "lineComplianceAck": true}); code != 0 {
		t.Fatalf("user edit: %s", msg)
	}
	if sig(700) != "" {
		t.Fatal("a user set 「已确认合规」")
	}

	// Ineligible rules are refused (landing node with a known protocol, plain tunnel, missing rule).
	for id, want := range map[int64]string{701: "落地节点", 702: "线路机", 999: "不存在"} {
		if code, msg, _ := callAs(t, e.h.forwardLineComplianceAck, 1, 0, map[string]interface{}{"id": id, "ack": true}); code == 0 || !strings.Contains(msg, want) {
			t.Errorf("rule %d: %d %q", id, code, msg)
		}
	}

	// Admin confirms 700 through the endpoint.
	if code, msg, _ := callAs(t, e.h.forwardLineComplianceAck, 1, 0, map[string]interface{}{"id": 700, "ack": true}); code != 0 {
		t.Fatalf("admin ack: %s", msg)
	}
	if sig(700) != repo.LineComplianceAckSig(10, 0, "node.example.com:10020") {
		t.Fatalf("stored %q", sig(700))
	}
	n, items := decorate(true)
	if n != 2 || items[700]["lineNonCompliant"] != nil || items[700]["lineComplianceAck"] != true || items[700]["lineMachine"] != true {
		t.Fatalf("admin view after ack: count %d, 700 %v", n, items[700])
	}
	if _, items := decorate(false); items[700]["lineNonCompliant"] != nil || items[700]["lineComplianceAck"] != nil {
		t.Fatalf("user view after ack: %v", items[700])
	}
	// The list endpoint: count for admins only, no ack flag for the owner.
	if data := listAs(1, 0); asInt(data["lineNonCompliantCount"], -1) != 2 || listItem(data, 700)["lineComplianceAck"] != true || listItem(data, 700)["lineNonCompliant"] != nil {
		t.Fatalf("admin list: count %v, 700 %v", data["lineNonCompliantCount"], listItem(data, 700))
	}
	if data := listAs(7, 1); data["lineNonCompliantCount"] != nil || listItem(data, 700)["lineComplianceAck"] != nil || listItem(data, 700)["lineNonCompliant"] != nil || listItem(data, 703)["lineNonCompliant"] != true {
		t.Fatalf("user list: %v / %v", data["lineNonCompliantCount"], listItem(data, 700))
	}

	// The owner editing the rule without touching its target keeps it.
	if code, msg, _ := callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": 700, "name": "usb2", "tunnelId": 10, "inPort": 1800}); code != 0 {
		t.Fatalf("user rename: %s", msg)
	}
	if sig(700) == "" {
		t.Fatal("a rename dropped the confirmation")
	}

	// A target changed behind the panel's back (older version after a
	// rollback, direct DB edit) no longer counts — the signature is checked.
	e.exec("UPDATE forward SET remote_addr = 'other.invalid:10020' WHERE id = 700")
	if n, items := decorate(true); n != 3 || items[700]["lineNonCompliant"] != true || items[700]["lineComplianceAck"] != nil {
		t.Fatalf("stale confirmation still counted: %d %v", n, items[700])
	}
	e.exec("UPDATE forward SET remote_addr = 'node.example.com:10020' WHERE id = 700")
	if n, _ := decorate(true); n != 2 {
		t.Fatalf("restored target: %d", n)
	}

	// Rule dialog: admin unticks, then ticks again (applied after the save).
	if code, msg, data := callAs(t, e.h.forwardUpdate, 1, 0, map[string]interface{}{"id": 700, "tunnelId": 10, "inPort": 1800, "lineComplianceAck": false}); code != 0 || data != nil && data["warnings"] != nil {
		t.Fatalf("admin untick: %s %v", msg, data)
	}
	if sig(700) != "" {
		t.Fatal("untick kept the confirmation")
	}
	if code, msg, _ := callAs(t, e.h.forwardUpdate, 1, 0, map[string]interface{}{"id": 703, "tunnelId": 20, "inPort": 1803, "lineComplianceAck": true}); code != 0 {
		t.Fatalf("admin tick: %s", msg)
	}
	if sig(703) != repo.LineComplianceAckSig(20, 0, "1.1.1.1:443") {
		t.Fatalf("dialog tick stored %q", sig(703))
	}
	if n, items := decorate(true); n != 2 || items[703]["lineComplianceAck"] != true || items[700]["lineNonCompliant"] != true {
		t.Fatalf("after dialog: %d", n)
	}
	// Ineligible tick from the dialog: saved rule, warning, nothing stored.
	if code, msg, data := callAs(t, e.h.forwardUpdate, 1, 0, map[string]interface{}{"id": 701, "tunnelId": 10, "inPort": 1801, "landingNodeId": 91, "lineComplianceAck": true}); code != 0 || data == nil || !strings.Contains(asString(data["warnings"]), "已确认合规未保存") {
		t.Fatalf("ineligible dialog tick: %d %s %v", code, msg, data)
	}
	if sig(701) != "" {
		t.Fatal("landing-node rule got a confirmation")
	}

	// Moving the rule to another tunnel clears it (batch 换隧道 → plain tunnel 30).
	_, _, res := callAs(t, e.h.forwardBatchChangeTunnel, 1, 0, map[string]interface{}{"forwardIds": []int64{703}, "targetTunnelId": 30})
	if asInt(res["successCount"], 0) != 1 {
		t.Fatalf("batch move: %v", res)
	}
	if sig(703) != "" {
		t.Fatal("tunnel move kept the confirmation")
	}

	// Admin clears through the endpoint.
	if code, msg, _ := callAs(t, e.h.forwardLineComplianceAck, 1, 0, map[string]interface{}{"id": 700, "ack": true}); code != 0 {
		t.Fatal(msg)
	}
	if code, msg, _ := callAs(t, e.h.forwardLineComplianceAck, 1, 0, map[string]interface{}{"id": 700, "ack": false}); code != 0 || sig(700) != "" {
		t.Fatalf("admin clear: %s", msg)
	}
}
