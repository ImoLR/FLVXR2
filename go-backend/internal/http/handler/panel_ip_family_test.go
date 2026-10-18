package handler

import (
	"testing"

	"go-backend/internal/store/model"
)

func TestNodePanelIPFamilyAPI(t *testing.T) {
	e := newFlowTestEnv(t)
	body := map[string]interface{}{"name": "panel-family", "serverIpV4": "192.0.2.10", "panelIpFamily": "prefer_ipv4"}
	if code, msg := redeployMutationRequest(t, e.h.nodeCreate, body); code != 0 {
		t.Fatal(msg)
	}
	var node model.Node
	if err := e.r.DB().Where("name = ?", "panel-family").First(&node).Error; err != nil {
		t.Fatal(err)
	}
	if node.PanelIPFamily != "prefer_ipv4" {
		t.Fatal(node.PanelIPFamily)
	}
	body["id"] = node.ID
	for _, mode := range []string{"ipv4_only", "ipv6_only", "prefer_ipv4", "auto", ""} {
		body["panelIpFamily"] = mode
		if code, msg := redeployMutationRequest(t, e.h.nodeUpdate, body); code != 0 {
			t.Fatal(msg)
		}
		want := mode
		if want == "" {
			want = "auto"
		}
		items, err := e.r.ListNodes(nil)
		if err != nil || len(items) != 1 || items[0]["panelIpFamily"] != want {
			t.Fatalf("items=%v err=%v", items, err)
		}
	}
	body["panelIpFamily"] = "bad"
	for _, fn := range []string{"create", "update"} {
		handler := e.h.nodeCreate
		if fn == "update" {
			handler = e.h.nodeUpdate
		}
		if code, _ := redeployMutationRequest(t, handler, body); code == 0 {
			t.Fatal("invalid family accepted")
		}
	}
	e.exec("UPDATE node SET panel_ip_family = 'ipv6_only' WHERE id = ?", node.ID)
	delete(body, "panelIpFamily")
	if code, msg := redeployMutationRequest(t, e.h.nodeUpdate, body); code != 0 {
		t.Fatal(msg)
	}
	got, _ := e.r.GetNodeByID(node.ID)
	if got.PanelIPFamily != "ipv6_only" {
		t.Fatal("legacy update erased mode")
	}
	e.addNode(99, "legacy")
	legacy, _ := e.r.GetNodeByID(99)
	if legacy.PanelIPFamily != "" {
		t.Fatal("legacy default is not auto")
	}
	var col struct {
		Notnull   int
		DfltValue string
	}
	if err := e.r.DB().Raw("SELECT [notnull], dflt_value FROM pragma_table_info('node') WHERE name='panel_ip_family'").Scan(&col).Error; err != nil || col.Notnull != 1 || (col.DfltValue != "''" && col.DfltValue != `""`) {
		t.Fatalf("column=%+v err=%v", col, err)
	}
}
