package contract_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
)

// Plan 147 (user 10-24: 「那个设置只针对转发面板，妙妙屋那边…不要动」): the
// 计算方式 choice is FLVX-native only. Through the API a shared server and its
// nodes refuse 仅入站, Custom never receives conn_mode — not even from a share
// advertising landing-conn-mode-v1 (contract 暂缓) — the views say fixed
// 入站+出站 and show the Core's total count, and users only get the label.
func TestSharedLandingConnModeContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			e := newSharedContract(t, dialect)
			s := e.servers[0]
			id := e.node(t, "ss2022")
			other := e.node(t, "vless_reality_vision")
			key := strconv.FormatInt(id, 10)
			desiredJSON := func() string {
				t.Helper()
				e.f.mu.Lock()
				defer e.f.mu.Unlock()
				raw, _ := json.Marshal(e.f.desired[7].Nodes)
				return string(raw)
			}
			adminNode := func(nodeID int64) map[string]any {
				t.Helper()
				list := e.ok(t, "node/list", e.admin, map[string]any{}).Data.([]any)
				for _, item := range list {
					v := item.(map[string]any)
					if int64(v["id"].(float64)) == nodeID {
						return v
					}
				}
				t.Fatalf("node %d missing", nodeID)
				return nil
			}
			adminServer := func() map[string]any {
				t.Helper()
				for _, item := range e.ok(t, "server/list", e.admin, map[string]any{}).Data.([]any) {
					if sv := item.(map[string]any); int64(sv["id"].(float64)) == s.ID {
						return sv
					}
				}
				t.Fatal("server missing")
				return nil
			}
			poll := func() { e.ok(t, "integration/sync", e.admin, map[string]any{}) }

			if e.call(t, "node/update", e.user, map[string]any{"id": id, "connMode": "inbound"}).Code == 0 {
				t.Fatal("user changed the conn mode")
			}
			if r := e.call(t, "node/update", e.admin, map[string]any{"id": id, "connMode": "inbound", "maxConnections": 50}); r.Code == 0 || !strings.Contains(r.Msg, "入站+出站") {
				t.Fatalf("shared node 仅入站 accepted: %+v", r)
			}
			e.ok(t, "node/update", e.admin, map[string]any{"id": id, "maxConnections": 50})
			before := desiredJSON()
			if strings.Contains(before, "conn_mode") {
				t.Fatalf("Custom received conn_mode: %s", before)
			}
			// Custom + Helper advertise conn_modes v1: nothing changes for FLVX.
			e.f.mu.Lock()
			e.f.shares[0].ConnModes = &landingcustom.ShareConnModes{Versions: []int{1}}
			e.f.mu.Unlock()
			poll()
			if after := desiredJSON(); after != before {
				t.Fatalf("shared desired changed with conn_modes v1:\n%s\n%s", before, after)
			}
			if r := e.call(t, "node/update", e.admin, map[string]any{"id": id, "connMode": "inbound"}); r.Code == 0 {
				t.Fatal("shared node 仅入站 accepted with conn_modes v1")
			}
			// Status with inbound_connections: the total count stays what is shown.
			e.f.mu.Lock()
			st := e.f.status[7]
			st.CollectedAt = time.Now().UnixMilli() + 1000
			four := 4
			st.Nodes = []landingcustom.NodeStatus{{ID: key, State: "active", Connections: 9, InboundConnections: &four}, {ID: strconv.FormatInt(other, 10), State: "active", Connections: 6}}
			e.f.status[7] = st
			e.f.mu.Unlock()
			poll()
			v := adminNode(id)
			if v["connections"].(float64) != 9 || v["connModeEffective"] != "total" || v["connModeConfigured"] != "total" || v["connModePending"] != false || v["connModeFixed"] != true {
				t.Fatalf("shared node view %+v", v)
			}
			sv := adminServer()
			if sv["connections"].(float64) != 15 || sv["connectionsMode"] != "total" || sv["connModeSupported"] != false || sv["connModeFixed"] != true || sv["maxConnectionsMode"] != "total" {
				t.Fatalf("server view %+v", sv)
			}
			raw, _ := json.Marshal(e.ok(t, "node/list", e.user, map[string]any{}).Data)
			if strings.Contains(string(raw), "connModePending") || strings.Contains(string(raw), "inboundConnections") || strings.Contains(string(raw), `"connMode"`) || !strings.Contains(string(raw), `"connModeEffective":"total"`) {
				t.Fatalf("user node view: %s", raw)
			}
			raw, _ = json.Marshal(e.ok(t, "server/list", e.user, map[string]any{}).Data)
			if strings.Contains(string(raw), "maxConnMode") || strings.Contains(string(raw), `"connMode"`) {
				t.Fatalf("user server view: %s", raw)
			}
			// Server save: 仅入站 refused (node and total), 「应用到现有节点」 ignored.
			cur, err := e.r.GetLandingServer(s.ID)
			if err != nil || cur == nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(cur)
			body := map[string]any{}
			_ = json.Unmarshal(b, &body)
			body["protocols"], body["realityServerNames"], body["userGroupIds"] = []string{"ss2022", "vless_reality_vision"}, []string{"www.lovelive-anime.jp"}, []int64{701}
			body["connMode"], body["defaultMaxConnections"], body["applyConnModeToNodes"] = "inbound", 40, true
			if r := e.call(t, "server/save", e.admin, body); r.Code == 0 || !strings.Contains(r.Msg, "入站+出站") {
				t.Fatalf("shared server 仅入站 accepted: %+v", r)
			}
			body["connMode"] = "total"
			e.ok(t, "server/save", e.admin, body)
			for _, nodeID := range []int64{id, other} {
				if n, _ := e.r.GetLandingNode(nodeID); n.MaxConnections == 40 {
					t.Fatalf("apply-to-existing touched shared node %d", nodeID)
				}
			}
			body["maxConnMode"], body["applyConnModeToNodes"] = "inbound", false
			if e.call(t, "server/save", e.admin, body).Code == 0 {
				t.Fatal("shared 仅入站 total accepted")
			}
			if strings.Contains(desiredJSON(), "conn_mode") {
				t.Fatal("conn_mode reached Custom")
			}
		})
	}
}
