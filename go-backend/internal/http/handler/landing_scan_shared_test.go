package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// scanCustom is a fake Custom for plan 205 (landing-scan-guard-v1 §9): the
// share advertises scan_guard when set, every desired body is kept raw, and
// the status (incl. scan_guard) is served as set by the test.
type scanCustom struct {
	mu          sync.Mutex
	share       landingcustom.Share
	status      map[string]any
	bodies      []string
	desired     landingcustom.Desired
	statusCalls int
}

func (g *scanCustom) serve(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case landingcustom.Prefix + "/shares":
			json.NewEncoder(w).Encode(map[string]any{"shares": []landingcustom.Share{g.share}})
		case landingcustom.Prefix + "/landing-hosts":
			w.Write([]byte("{}"))
		case landingcustom.Prefix + "/shares/7/desired":
			raw, _ := io.ReadAll(req.Body)
			g.bodies = append(g.bodies, string(raw))
			g.desired = landingcustom.Desired{}
			_ = json.Unmarshal(raw, &g.desired)
			json.NewEncoder(w).Encode(map[string]int64{"accepted_revision": g.desired.Revision})
		case landingcustom.Prefix + "/shares/7/status":
			g.statusCalls++
			json.NewEncoder(w).Encode(g.status)
		default:
			t.Errorf("unexpected path %s", req.URL.Path)
			w.WriteHeader(404)
		}
	}))
}

func (g *scanCustom) last(t *testing.T) (string, landingcustom.Desired) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.bodies) == 0 {
		t.Fatal("no desired pushed")
	}
	return g.bodies[len(g.bodies)-1], g.desired
}

func desiredNode(d landingcustom.Desired, id int64) *landingcustom.Node {
	for i := range d.Nodes {
		if d.Nodes[i].ID == fmt.Sprint(id) {
			return &d.Nodes[i]
		}
	}
	return nil
}

func scanIdentities(d landingcustom.Desired) string {
	ids := []string{}
	for _, v := range d.ScanGuard["identities"].([]any) {
		ids = append(ids, v.(string))
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// TestSharedLandingScanGuard (plan 205): a share without the capability keeps
// today's payload and 「共享服务器暂不支持」; once advertised it gets the native
// scan_guard object (one revision bump, nodes byte-identical), its relayed
// hits disable the node through the native records (event, seen, revision,
// enabled:false), malformed reports never fail the poll, 解除禁用 pushes the
// node back, the rule off still sends enabled:false + seen, and the status is
// read only by the existing once-a-minute poll.
func TestSharedLandingScanGuard(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := sharedLandingTestRepo(t, dialect)
			h := New(r, "shared-scan", "test")
			ctx := context.Background()
			if err := r.DB().Create(&model.User{ID: 2, User: "alice", Pwd: "x", RoleID: 1, Status: 1, Flow: -1}).Error; err != nil {
				t.Fatal(err)
			}
			g := &scanCustom{share: landingcustom.Share{ID: 7, Name: "Dmit T1", Hosts: landingcustom.Hosts{IPv4: "203.0.113.7"}, Status: "active", PortMin: 30000, PortMax: 30010,
				Core: landingcustom.Core{State: "running"}, Protocols: []string{"ss2022", "vless_reality_vision"}}}
			g.status = map[string]any{"applied_revision": 0, "core": map[string]any{"state": "running"}, "nodes": []any{}, "share_traffic_used_bytes": 0, "collected_at": time.Now().UnixMilli()}
			custom := g.serve(t)
			defer custom.Close()
			if err := r.SaveLandingIntegration(custom.URL, "flvx_fixture"); err != nil {
				t.Fatal(err)
			}
			h.syncSharedIntegration(ctx, true)
			servers, _ := r.ListLandingServers(0, true)
			if len(servers) != 1 {
				t.Fatalf("servers %d", len(servers))
			}
			sid := servers[0].ID
			if err := r.DB().Create(&model.LandingServerUser{LandingServerID: sid, UserID: 2}).Error; err != nil {
				t.Fatal(err)
			}
			r.DB().Model(&model.LandingServer{}).Where("id = ?", sid).Update("max_nodes_per_user", 8)
			n1, err := r.CreateLandingNode(2, sid, "a", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			n2, err := r.CreateLandingNode(2, sid, "b", "vless_reality_vision", false)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.syncSharedLandingServer(sid); err != nil {
				t.Fatal(err)
			}
			adminState := func() map[string]any {
				t.Helper()
				v := dataMap(t, layerCall(t, h, "/api/v1/landing/scan/get", map[string]any{}, 1, 0))
				return v["servers"].([]any)[0].(map[string]any)
			}
			userRow := func() map[string]any {
				t.Helper()
				v := dataMap(t, layerCall(t, h, "/api/v1/landing/node/rules/get", map[string]any{"id": n1.ID}, 2, 1))
				return v["builtin"].(map[string]any)["scanGuard"].(map[string]any)
			}
			report := func(guard any) {
				t.Helper()
				g.mu.Lock()
				g.status["applied_revision"] = g.desired.Revision
				g.status["collected_at"] = time.Now().UnixMilli()
				if guard == nil {
					delete(g.status, "scan_guard")
				} else {
					g.status["scan_guard"] = guard
				}
				g.mu.Unlock()
				h.syncSharedIntegration(ctx, true)
				h.egressSyncWG.Wait()
			}

			// Older Custom / Helper / Core: nothing new is sent, even when a
			// stray report arrives; the row keeps 共享服务器暂不支持.
			raw, d := g.last(t)
			if strings.Contains(raw, "scan_guard") || d.ScanGuard != nil {
				t.Fatalf("share without the capability got scan_guard: %s", raw)
			}
			report(nil)
			if st := adminState(); st["state"] != "shared" || st["label"] != "共享服务器暂不支持" {
				t.Fatalf("unsupported share state %v", st)
			}
			if row := userRow(); row["state"] != "shared" {
				t.Fatalf("unsupported share user row %v", row)
			}
			if raw, _ := g.last(t); strings.Contains(raw, "scan_guard") {
				t.Fatalf("scan_guard sent after a poll: %s", raw)
			}
			oldRevision := d.Revision

			// Custom v1.4.84+ advertises it: one revision bump, the native
			// object for every node of the desired state, nodes unchanged.
			g.mu.Lock()
			g.share.ScanGuard = &landingcustom.ShareScanGuard{Versions: []int{1, 1, 0, 5000}}
			g.mu.Unlock()
			h.syncSharedIntegration(ctx, true)
			raw2, d2 := g.last(t)
			sg := d2.ScanGuard
			if d2.Revision != oldRevision+1 || sg == nil || sg["enabled"] != true || sg["seen"].(float64) != 0 || sg["horizontal"].(float64) != 100 || sg["horizontal_web"].(float64) != 500 ||
				sg["vertical"].(float64) != 30 || sg["total"].(float64) != 1000 || sg["total_window_s"].(float64) != 300 || sg["window_s"].(float64) != 60 {
				t.Fatalf("scan_guard desired %s (old revision %d)", raw2, oldRevision)
			}
			if got, want := scanIdentities(d2), fmt.Sprintf("flvxl-%d,flvxl-%d", n1.ID, n2.ID); got != want {
				t.Fatalf("identities %s want %s", got, want)
			}
			b1, _ := json.Marshal(d.Nodes)
			b2, _ := json.Marshal(d2.Nodes)
			if !bytes.Equal(b1, b2) {
				t.Fatalf("nodes changed:\n%s\n%s", b1, b2)
			}
			if s, _ := r.GetLandingServer(sid); !strings.Contains(s.SharedCaps, `"scan_guard":{"versions":[1]}`) {
				t.Fatalf("caps not sanitized/stored: %s", s.SharedCaps)
			}
			h.syncSharedIntegration(ctx, true)
			if _, d3 := g.last(t); d3.Revision != d2.Revision {
				t.Fatalf("revision moved without a change: %d → %d", d2.Revision, d3.Revision)
			}
			if st := adminState(); st["state"] != "pending" {
				t.Fatalf("not reported yet: %v", st)
			}
			report(map[string]any{"state": "on", "hits": []any{}, "blocked": []any{}})
			if st := adminState(); st["state"] != "on" || st["label"] != "生效中" {
				t.Fatalf("on state %v", st)
			}
			if row := userRow(); row["state"] != "on" || row["label"] != "生效中" {
				t.Fatalf("user row %v", row)
			}
			report(map[string]any{"state": "core_update", "hits": []any{}, "blocked": []any{}})
			if st := adminState(); st["state"] != "shared_core" {
				t.Fatalf("core_update state %v", st)
			}
			report(map[string]any{"state": "on", "hits": []any{}, "blocked": []any{}})

			// The existing poll cadence: background ticks within the minute
			// read no status (the report rides on the 60 s poll only).
			g.mu.Lock()
			calls := g.statusCalls
			g.mu.Unlock()
			h.syncSharedIntegration(ctx, false)
			h.syncSharedIntegration(ctx, false)
			g.mu.Lock()
			if g.statusCalls != calls {
				t.Fatalf("status read %d extra times within a minute", g.statusCalls-calls)
			}
			g.mu.Unlock()

			// A hit (node id as a string, contract §9.3): node disabled at once,
			// event recorded, seen + enabled:false pushed with a new revision.
			const seq = int64(1793709225987084)
			hit := func(id int64, s int64, kind string) map[string]any {
				return map[string]any{"id": fmt.Sprint(id), "inbound_tag": fmt.Sprintf("flvxl-%d", id), "type": kind, "count": 100, "port": 22, "window_s": 60, "at": "2026-11-04T08:00:00.123456789Z", "seq": s}
			}
			revBefore := d2.Revision
			report(map[string]any{"state": "on", "hits": []any{hit(n1.ID, seq, "horizontal")}, "blocked": []any{fmt.Sprintf("flvxl-%d", n1.ID)}})
			n, _ := r.GetLandingNode(n1.ID)
			if n.Status != "disabled" || n.ScanBlockedAt == 0 || n.Port != n1.Port {
				t.Fatalf("node after hit %+v", n)
			}
			_, d4 := g.last(t)
			if d4.Revision <= revBefore || d4.ScanGuard["seen"].(float64) != float64(seq) {
				t.Fatalf("seen not pushed back: revision %d seen %v", d4.Revision, d4.ScanGuard["seen"])
			}
			if dn := desiredNode(d4, n1.ID); dn == nil || dn.Enabled {
				t.Fatalf("blocked node in desired %+v", dn)
			}
			if !strings.Contains(scanIdentities(d4), fmt.Sprintf("flvxl-%d", n1.ID)) {
				t.Fatal("blocked node dropped from identities (would lift the Core block)")
			}
			events, total, err := r.ListLandingScanEvents(0, 10)
			if err != nil || total != 1 || !events[0].Blocked || events[0].LandingNodeID != n1.ID || events[0].UserID != 2 || events[0].LandingServerID != sid {
				t.Fatalf("events %+v %d %v", events, total, err)
			}
			// The Helper repeats it until it sees the ack: recorded once.
			report(map[string]any{"state": "on", "hits": []any{hit(n1.ID, seq, "horizontal")}, "blocked": []any{}})
			if _, total, _ := r.ListLandingScanEvents(0, 10); total != 1 {
				t.Fatal("hit recorded twice")
			}
			if n, _ := r.GetLandingNode(n1.ID); n.Status != "disabled" || n.ScanBlockedAt == 0 {
				t.Fatal("policy pass re-enabled a scan-blocked shared node")
			}
			// Owner: the 联系管理员 message on the node.
			var list []map[string]any
			b, _ := json.Marshal(layerCall(t, h, "/api/v1/landing/node/list", map[string]any{}, 2, 1).Data)
			_ = json.Unmarshal(b, &list)
			found := false
			for _, v := range list {
				if int64(v["id"].(float64)) == n1.ID {
					block, _ := v["scanBlock"].(map[string]any)
					found = block != nil && block["message"] == "该落地节点因检测到端口扫描（同一端口 22 在 60 秒内连接了 100 个不同 IP）已被禁用，如有疑问请联系管理员"
				}
			}
			if !found {
				t.Fatalf("owner message missing: %v", list)
			}

			// Malformed reports and hits are dropped; the poll and billing go on.
			for _, bad := range []any{"on", map[string]any{"state": "exploded"}, map[string]any{"state": "on", "hits": "x"},
				map[string]any{"state": "on", "hits": []any{map[string]any{"id": "x", "type": "total", "seq": seq + 3}, map[string]any{"id": fmt.Sprint(n2.ID), "type": "total", "seq": 0},
					map[string]any{"inbound_tag": "flvxl-99x", "type": "total", "seq": seq + 4}}}} {
				report(bad)
				s, _ := r.GetLandingServer(sid)
				if s.SharedSyncError != "" || s.ScanSeenSeq != seq {
					t.Fatalf("bad report %v: error %q seen %d", bad, s.SharedSyncError, s.ScanSeenSeq)
				}
			}
			if _, total, _ := r.ListLandingScanEvents(0, 10); total != 1 {
				t.Fatal("malformed hit recorded")
			}
			if n, _ := r.GetLandingNode(n2.ID); n.Status != "active" {
				t.Fatal("malformed hit disabled a node")
			}

			// 解除禁用 (node button): enabled again with a new revision; the
			// seen mark stays, so the Helper sends scan_unblock for that seq.
			revBlocked := g.desired.Revision
			if out := layerCall(t, h, "/api/v1/landing/node/scan/unblock", map[string]any{"id": n1.ID}, 1, 0); out.Code != 0 {
				t.Fatal(out.Msg)
			}
			h.egressSyncWG.Wait()
			h.syncSharedIntegration(ctx, false)
			_, d5 := g.last(t)
			if d5.Revision <= revBlocked || d5.ScanGuard["seen"].(float64) != float64(seq) {
				t.Fatalf("unblock desired revision %d (blocked %d) seen %v", d5.Revision, revBlocked, d5.ScanGuard["seen"])
			}
			if dn := desiredNode(d5, n1.ID); dn == nil || !dn.Enabled {
				t.Fatalf("unblocked node in desired %+v", dn)
			}
			if events, _, _ := r.ListLandingScanEvents(n1.ID, 1); events[0].UnblockedAt == 0 || events[0].UnblockedBy == "" {
				t.Fatalf("unblock not logged %+v", events[0])
			}

			// Rule off: still sent (enabled false, no identities, seen kept).
			if out := layerCall(t, h, "/api/v1/landing/scan/save", map[string]any{"on": false, "horizontal": 100, "horizontalWeb": 500, "vertical": 30, "total": 2000}, 1, 0); out.Code != 0 {
				t.Fatal(out.Msg)
			}
			h.syncSharedIntegration(ctx, false)
			_, d6 := g.last(t)
			if d6.Revision <= d5.Revision || d6.ScanGuard["enabled"] != false || scanIdentities(d6) != "" || d6.ScanGuard["seen"].(float64) != float64(seq) || d6.ScanGuard["total"].(float64) != 2000 {
				t.Fatalf("rule off desired %+v", d6.ScanGuard)
			}
			if st := adminState(); st["state"] != "off" {
				t.Fatalf("off state %v", st)
			}
		})
	}
}

// TestLandingScanRecreateRefused (plan 205): while a user's node is disabled
// by 禁止端口扫描 — also after they delete it — the user cannot create a
// landing node (native or shared, API or the rule page's auto-create), with
// the 联系管理员 message; admins are not limited; 解除禁用 from the event row
// (node deleted) lets them create again.
func TestLandingScanRecreateRefused(t *testing.T) {
	e := newFlowTestEnv(t)
	guardSeed(t, e)
	if err := e.r.DB().Create(&model.LandingServerUser{LandingServerID: 1, UserID: 2}).Error; err != nil {
		t.Fatal(err)
	}
	// The native agent never confirms here (sync errors are ignored by the
	// create / unblock handlers); this test is about the panel's checks.
	e.h.nodeCommandSender = func(int64, string, interface{}, time.Duration) (ws.CommandResult, error) {
		return ws.CommandResult{Success: false, Message: "test agent"}, nil
	}
	e.exec("UPDATE landing_server SET max_nodes_per_user=20 WHERE id=1")
	e.exec("INSERT INTO landing_user_setting(user_id, max_nodes, max_nodes_custom, rule_max_node, rule_max_node_custom, rule_max_total, rule_max_total_custom) VALUES (2, 20, true, 5, false, 10, false)")
	create := func(uid int64, role int, body map[string]any) (int64, string) {
		t.Helper()
		out := layerCall(t, e.h, "/api/v1/landing/node/create", body, uid, role)
		if out.Code != 0 {
			return 0, out.Msg
		}
		return int64(dataMap(t, out)["id"].(float64)), ""
	}
	if _, msg := create(2, 1, map[string]any{"landingServerId": 1, "name": "before", "protocol": "ss2022"}); msg != "" {
		t.Fatalf("create before any block: %s", msg)
	}
	const seq = int64(1793709225987084)
	notices, _, err := e.r.RecordLandingScanStatus(1, "on", []repo.LandingScanHit{{InboundTag: "flvxl-4", Type: "vertical", Count: 30, Host: "198.51.100.7", WindowS: 60, At: time.Now(), Seq: seq}}, time.Now())
	if err != nil || len(notices) != 1 {
		t.Fatalf("record %v %v", notices, err)
	}
	const want = "你的落地节点因检测到端口扫描（同一个 IP 198.51.100.7 在 60 秒内被连接了 30 个不同端口）已被禁用，暂不能新建落地节点，如有疑问请联系管理员"
	if _, msg := create(2, 1, map[string]any{"landingServerId": 1, "name": "again", "protocol": "ss2022"}); msg != want {
		t.Fatalf("create while blocked: %q", msg)
	}
	// The rule page's auto-create (no reusable node of that protocol).
	if _, _, err := e.r.ReuseOrCreateLandingNode(2, 1, "规则落地", "vless_reality_vision", false); err != nil {
		t.Fatalf("reuse of an existing active node refused: %v", err)
	}
	e.exec("UPDATE landing_node SET status='disabled' WHERE user_id=2 AND protocol='vless_reality_vision'")
	if _, _, err := e.r.ReuseOrCreateLandingNode(2, 1, "规则落地", "vless_reality_vision", false); err == nil || err.Error() != want {
		t.Fatalf("auto-create while blocked: %v", err)
	}
	e.exec("UPDATE landing_node SET status='active' WHERE user_id=2 AND protocol='vless_reality_vision'")
	// The user's server card says why.
	servers := layerCall(t, e.h, "/api/v1/landing/server/list", map[string]any{}, 2, 1)
	if b, _ := json.Marshal(servers.Data); !strings.Contains(string(b), `"scanCreateBlocked":"`+want+`"`) {
		t.Fatalf("server card: %s", b)
	}
	// Admins are not limited (for the user, or themselves).
	if _, msg := create(1, 0, map[string]any{"landingServerId": 1, "userId": 2, "name": "by-admin", "protocol": "ss2022"}); msg != "" {
		t.Fatalf("admin create for the user: %s", msg)
	}
	if admin := layerCall(t, e.h, "/api/v1/landing/server/list", map[string]any{}, 1, 0); strings.Contains(fmt.Sprint(admin.Data), "scanCreateBlocked") {
		t.Fatal("admin view carries scanCreateBlocked")
	}

	// The user deletes the blocked node: the record stays, so does the
	// restriction.
	if err := e.r.DeleteLandingNode(4); err != nil {
		t.Fatal(err)
	}
	if _, msg := create(2, 1, map[string]any{"landingServerId": 1, "name": "after-delete", "protocol": "ss2022"}); msg != want {
		t.Fatalf("create after deleting the blocked node: %q", msg)
	}
	events, total, _ := e.r.ListLandingScanEvents(0, 10)
	if total != 1 || events[0].NodeBlocked || events[0].UnblockedAt != 0 {
		t.Fatalf("event after delete %+v", events)
	}
	if v := dataMap(t, layerCall(t, e.h, "/api/v1/landing/scan/get", map[string]any{}, 1, 0)); v["deletedBlocked"].(float64) != 1 || v["blockedNodes"].(float64) != 0 {
		t.Fatalf("admin counts %v", v)
	}
	// Only an admin lifts it (event row).
	if out := layerCall(t, e.h, "/api/v1/landing/scan/event/unblock", map[string]any{"id": events[0].ID}, 2, 1); out.Code == 0 {
		t.Fatal("owner lifted the record")
	}
	if out := layerCall(t, e.h, "/api/v1/landing/scan/event/unblock", map[string]any{"id": events[0].ID}, 1, 0); out.Code != 0 {
		t.Fatal(out.Msg)
	}
	if out := layerCall(t, e.h, "/api/v1/landing/scan/event/unblock", map[string]any{"id": events[0].ID}, 1, 0); out.Code == 0 {
		t.Fatal("second lift accepted")
	}
	if _, msg := create(2, 1, map[string]any{"landingServerId": 1, "name": "after-unblock", "protocol": "ss2022"}); msg != "" {
		t.Fatalf("create after 解除禁用: %s", msg)
	}
	if events, _, _ := e.r.ListLandingScanEvents(4, 1); events[0].UnblockedBy == "" || events[0].UnblockedAt == 0 {
		t.Fatalf("lift not logged %+v", events[0])
	}

	// Event-row 解除禁用 on a node that still exists = the node's button.
	if _, _, err := e.r.RecordLandingScanStatus(1, "on", []repo.LandingScanHit{{InboundTag: "flvxl-1", Type: "total", Count: 1000, WindowS: 300, At: time.Now(), Seq: seq + 9}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, msg := create(2, 1, map[string]any{"landingServerId": 1, "name": "x", "protocol": "ss2022"}); msg == "" {
		t.Fatal("create while node 1 is blocked")
	}
	events, _, _ = e.r.ListLandingScanEvents(1, 1)
	if out := layerCall(t, e.h, "/api/v1/landing/scan/event/unblock", map[string]any{"id": events[0].ID}, 1, 0); out.Code != 0 || dataMap(t, out)["nodeId"].(float64) != 1 {
		t.Fatalf("event unblock of an existing node: %+v", out)
	}
	e.h.egressSyncWG.Wait()
	if n, _ := e.r.GetLandingNode(1); n.Status != "active" || n.ScanBlockedAt != 0 {
		t.Fatalf("node 1 after event unblock %+v", n)
	}
}
