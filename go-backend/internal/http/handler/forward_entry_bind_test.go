package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"

	"gorm.io/gorm"
)

// Plan 152 fixture (both dialects): tunnel 25 has entries 23 「Mkcloud 沪日ixp 440」
// and 51 「vmsilo 沪日首发」 (+ 52 in the 3-entry variant). Group 3 grants entry
// 23, group 8 entry 51. User 7 holds both groups, user 8 only group 3.
type bindEnv struct {
	t    *testing.T
	r    *repo.Repository
	h    *Handler
	mu   sync.Mutex
	cmds []bindCmd
	now  int64
}

type bindCmd struct {
	node    int64
	command string
	payload string
}

func newBindEnv(t *testing.T, dialect string, extraEntry bool) *bindEnv {
	t.Helper()
	r := sharedLandingTestRepo(t, dialect)
	e := &bindEnv{t: t, r: r, h: &Handler{repo: r}, now: time.Now().UnixMilli()}
	e.h.nodeCommandSender = func(id int64, command string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		raw, _ := json.Marshal(data)
		e.mu.Lock()
		e.cmds = append(e.cmds, bindCmd{id, command, string(raw)})
		e.mu.Unlock()
		return ws.CommandResult{Success: true}, nil
	}
	far := e.now + 86400000*30
	for _, u := range []model.User{
		{ID: 7, User: "bind-u7", Pwd: "x", RoleID: 1, ExpTime: far, Flow: 99999, FlowResetTime: 1, Num: 20, CreatedTime: e.now, Status: 1},
		{ID: 8, User: "bind-u8", Pwd: "x", RoleID: 1, ExpTime: far, Flow: 99999, FlowResetTime: 1, Num: 20, CreatedTime: e.now, Status: 1},
	} {
		e.create(&u)
	}
	nodes := []model.Node{
		{ID: 23, Name: "Mkcloud 沪日ixp 440", Secret: "s23", ServerIP: "192.0.2.23", Port: "1000-65535", CreatedTime: e.now, Status: 1, Region: "CN", RegionCity: "上海A"},
		{ID: 51, Name: "vmsilo 沪日首发", Secret: "s51", ServerIP: "192.0.2.51", Port: "1000-65535", CreatedTime: e.now, Status: 1, Region: "CN", RegionCity: "上海B"},
	}
	if extraEntry {
		// 52 shares 51's public label: one user key covers both.
		nodes = append(nodes, model.Node{ID: 52, Name: "vmsilo 二号", Secret: "s52", ServerIP: "192.0.2.52", Port: "1000-65535", CreatedTime: e.now, Status: 1, Region: "CN", RegionCity: "上海B"})
	}
	for i := range nodes {
		e.create(&nodes[i])
	}
	e.create(&model.Tunnel{ID: 25, Name: "{入口} -> 出口JP(Akari JP)", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 1, CreatedTime: e.now, UpdatedTime: e.now, Status: 1,
		InIP: sql.NullString{String: "192.0.2.23,192.0.2.51", Valid: true}})
	for i, n := range nodes {
		e.create(&model.ChainTunnel{TunnelID: 25, ChainType: "1", NodeID: n.ID, Inx: nullInt(int64(i))})
	}
	e.create(&model.TunnelGroup{ID: 3, Name: "沪日440", CreatedTime: e.now, UpdatedTime: e.now, Status: 1})
	e.create(&model.TunnelGroup{ID: 8, Name: "沪日IEPL", CreatedTime: e.now, UpdatedTime: e.now, Status: 1})
	e.create(&model.TunnelGroupTunnel{TunnelGroupID: 3, TunnelID: 25, CreatedTime: e.now})
	e.create(&model.TunnelGroupTunnel{TunnelGroupID: 8, TunnelID: 25, CreatedTime: e.now})
	e.create(&model.TunnelGroupTunnelEntry{TunnelGroupID: 3, TunnelID: 25, NodeID: 23, CreatedTime: e.now})
	e.create(&model.TunnelGroupTunnelEntry{TunnelGroupID: 8, TunnelID: 25, NodeID: 51, CreatedTime: e.now})
	if extraEntry {
		e.create(&model.TunnelGroupTunnelEntry{TunnelGroupID: 8, TunnelID: 25, NodeID: 52, CreatedTime: e.now})
	}
	e.create(&model.UserTunnel{ID: 70, UserID: 7, TunnelID: 25, Num: 10, Flow: 99999, FlowResetTime: 1, ExpTime: far, Status: 1})
	e.create(&model.UserTunnel{ID: 80, UserID: 8, TunnelID: 25, Num: 10, Flow: 99999, FlowResetTime: 1, ExpTime: far, Status: 1})
	for _, g := range []model.GroupPermissionGrant{
		{UserGroupID: 1, TunnelGroupID: 3, UserTunnelID: 70, CreatedByGroup: 1, CreatedTime: e.now},
		{UserGroupID: 1, TunnelGroupID: 8, UserTunnelID: 70, CreatedByGroup: 1, CreatedTime: e.now},
		{UserGroupID: 2, TunnelGroupID: 3, UserTunnelID: 80, CreatedByGroup: 1, CreatedTime: e.now},
	} {
		e.create(&g)
	}
	return e
}

func nullInt(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

func (e *bindEnv) create(v interface{}) {
	e.t.Helper()
	if err := e.r.DB().Create(v).Error; err != nil {
		e.t.Fatalf("create %T: %v", v, err)
	}
}

func (e *bindEnv) resetCmds() {
	e.mu.Lock()
	e.cmds = nil
	e.mu.Unlock()
}

// nodesFor lists nodes that got a command mentioning forward id fid.
func (e *bindEnv) nodesFor(fid int64, command string) []int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	seen := map[int64]bool{}
	var out []int64
	needle := fmt.Sprintf(`"%d_`, fid) // service names start with "<forwardID>_"
	for _, c := range e.cmds {
		if (command == "" || c.command == command) && strings.Contains(c.payload, needle) && !seen[c.node] {
			seen[c.node] = true
			out = append(out, c.node)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (e *bindEnv) call(fn http.HandlerFunc, sub string, role int, body string) (int, string, json.RawMessage) {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: sub, RoleID: role}))
	w := httptest.NewRecorder()
	fn(w, req)
	var res struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		e.t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return res.Code, res.Msg, res.Data
}

func (e *bindEnv) createRule(sub string, role int, extra string) int64 {
	e.t.Helper()
	body := `{"name":"bind","tunnelId":25,"remoteAddr":"1.1.1.1:443"` + extra + `}`
	code, msg, data := e.call(e.h.forwardCreate, sub, role, body)
	if code != 0 {
		e.t.Fatalf("create %s: %d %s", body, code, msg)
	}
	var out struct{ ID int64 }
	_ = json.Unmarshal(data, &out)
	return out.ID
}

func (e *bindEnv) ports(fid int64) []int64 {
	e.t.Helper()
	ports, err := e.r.ListForwardPorts(fid)
	if err != nil {
		e.t.Fatal(err)
	}
	ids := forwardPortNodeIDs(ports)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (e *bindEnv) binding(fid int64) string {
	e.t.Helper()
	f, err := e.r.GetForwardRecord(fid)
	if err != nil || f == nil {
		e.t.Fatalf("forward %d: %v", fid, err)
	}
	return f.EntryNodeIDs
}

func (e *bindEnv) expect(fid int64, wantPorts []int64, wantBinding string) {
	e.t.Helper()
	if got := e.ports(fid); !reflect.DeepEqual(got, wantPorts) {
		e.t.Fatalf("forward %d ports %v, want %v", fid, got, wantPorts)
	}
	if got := e.binding(fid); got != wantBinding {
		e.t.Fatalf("forward %d binding %q, want %q", fid, got, wantBinding)
	}
}

func (e *bindEnv) forwardItem(fid int64, viewer repo.TunnelNameViewer) map[string]interface{} {
	e.t.Helper()
	items, err := e.r.ListForwards()
	if err != nil && e.r.DB().Dialector.Name() == "postgres" {
		// ListForwards joins the unquoted table user, which PostgreSQL
		// rejects (pre-existing, unrelated to plan 152): build the same row
		// fields from the package query, which shares resolveForwardIngress.
		items, err = nil, nil
		for _, owner := range []int64{1, 7, 8} {
			rows, listErr := e.r.GetUserPackageForwards(owner)
			if listErr != nil {
				e.t.Fatal(listErr)
			}
			for _, row := range rows {
				items = append(items, map[string]interface{}{"id": row.ID, "tunnelId": row.TunnelID, "tunnelName": row.TunnelName, "inIp": row.InIP})
			}
		}
	}
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.r.DecorateForwardItems(items, viewer); err != nil {
		e.t.Fatal(err)
	}
	for _, item := range items {
		if asInt64(item["id"], 0) == fid {
			return item
		}
	}
	e.t.Fatalf("forward %d not listed", fid)
	return nil
}

func bindDialects(t *testing.T, fn func(t *testing.T, dialect string)) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { fn(t, dialect) })
	}
}

func TestEntryBindCreateUpdateUnbind(t *testing.T) {
	bindDialects(t, func(t *testing.T, dialect string) {
		e := newBindEnv(t, dialect, false)
		// Admin under the 沪日IEPL pill: only entry 51 listens.
		bound := e.createRule("1", 0, `,"inPort":15001,"entryKey":"node-51"`)
		e.expect(bound, []int64{51}, "51")
		if got := e.nodesFor(bound, ""); !reflect.DeepEqual(got, []int64{51}) {
			t.Fatalf("deploy went to %v, want only 51", got)
		}
		// No key = today's behaviour: every entry.
		plain := e.createRule("1", 0, `,"inPort":15002`)
		e.expect(plain, []int64{23, 51}, "")
		// Unknown key: clear error, never silently all entries.
		code, msg, _ := e.call(e.h.forwardCreate, "1", 0, `{"name":"x","tunnelId":25,"remoteAddr":"1.1.1.1:443","entryKey":"node-99"}`)
		if code == 0 || msg != "入口已变化，请重新选择隧道" {
			t.Fatalf("unknown key: %d %q", code, msg)
		}
		// Edit without entryKey keeps the binding.
		e.resetCmds()
		if code, msg, _ := e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"inPort":15003}`, bound)); code != 0 {
			t.Fatalf("update keep: %d %s", code, msg)
		}
		e.expect(bound, []int64{51}, "51")
		if got := e.nodesFor(bound, ""); !reflect.DeepEqual(got, []int64{51}) {
			t.Fatalf("keep-update touched %v", got)
		}
		// Rebind to 23: deploy on 23, delete on 51 only.
		e.resetCmds()
		if code, msg, _ := e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"entryKey":"node-23"}`, bound)); code != 0 {
			t.Fatalf("rebind: %d %s", code, msg)
		}
		e.expect(bound, []int64{23}, "23")
		if got := e.nodesFor(bound, "DeleteService"); !reflect.DeepEqual(got, []int64{51}) {
			t.Fatalf("rebind deleted on %v, want [51]", got)
		}
		if got := e.nodesFor(bound, "UpdateService"); !reflect.DeepEqual(got, []int64{23}) {
			t.Fatalf("rebind deployed on %v, want [23]", got)
		}
		// Unbind: every entry again.
		if code, msg, _ := e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"entryKey":""}`, bound)); code != 0 {
			t.Fatalf("unbind: %d %s", code, msg)
		}
		e.expect(bound, []int64{23, 51}, "")
		// An unbound rule edited without a key stays unbound.
		if code, msg, _ := e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"inPort":15004}`, plain)); code != 0 {
			t.Fatalf("update plain: %d %s", code, msg)
		}
		e.expect(plain, []int64{23, 51}, "")
	})
}

func TestEntryBindListAddressAndKeys(t *testing.T) {
	bindDialects(t, func(t *testing.T, dialect string) {
		e := newBindEnv(t, dialect, false)
		e.r.DB().Model(&model.Node{}).Where("id = ?", 23).Update("entry_alias", "mk沪日ixp440")
		adminRule := e.createRule("1", 0, `,"inPort":15011,"entryKey":"node-51"`)
		userRule := e.createRule("7", 1, `,"inPort":15012,"entryKey":"`+userKey(t, e, 51)+`"`)
		e.expect(userRule, []int64{51}, "51")

		admin := e.forwardItem(adminRule, repo.TunnelNameViewer{Admin: true})
		if admin["inIp"] != "192.0.2.51:15011" {
			t.Fatalf("bound rule address %v, want only entry 51", admin["inIp"])
		}
		if admin["entryKey"] != "node-51" || admin["entryLabel"] != "vmsilo 沪日首发" || admin["tunnelName"] != "vmsilo 沪日首发 -> 出口JP(Akari JP)" {
			t.Fatalf("admin view %v %v %v", admin["entryKey"], admin["entryLabel"], admin["tunnelName"])
		}
		user := e.forwardItem(userRule, repo.TunnelNameViewer{UserID: 7})
		raw, _ := json.Marshal(user)
		if strings.Contains(string(raw), "vmsilo") || strings.Contains(string(raw), "Mkcloud") || strings.Contains(string(raw), "node-") {
			t.Fatalf("non-admin row leaks node identity: %s", raw)
		}
		if user["entryKey"] != userKey(t, e, 51) || user["entryLabel"] != "上海B" || user["tunnelName"] != "上海B -> 出口JP(Akari JP)" {
			t.Fatalf("user view %v %v %v", user["entryKey"], user["entryLabel"], user["tunnelName"])
		}
		// Unbound rules keep the tunnel-wide address (unchanged behaviour).
		plain := e.createRule("1", 0, `,"inPort":15013`)
		if got := e.forwardItem(plain, repo.TunnelNameViewer{Admin: true}); got["inIp"] != "192.0.2.23:15013,192.0.2.51:15013" || got["entryKey"] != nil {
			t.Fatalf("unbound row %v %v", got["inIp"], got["entryKey"])
		}
	})
}

func userKey(t *testing.T, e *bindEnv, nodeID int64) string {
	t.Helper()
	keys, err := e.r.EntryKeysForNodes([]int64{nodeID}, false)
	if err != nil || keys[nodeID] == "" {
		t.Fatalf("user key of %d: %v", nodeID, err)
	}
	return keys[nodeID]
}

func TestEntryBindUserKeysAndAdminOnBehalf(t *testing.T) {
	bindDialects(t, func(t *testing.T, dialect string) {
		e := newBindEnv(t, dialect, true)
		// One user key (上海B) covers 51 and 52: both are bound.
		rule := e.createRule("7", 1, `,"inPort":15021,"entryKey":"`+userKey(t, e, 51)+`"`)
		e.expect(rule, []int64{51, 52}, "51,52")
		// Users cannot address nodes by id.
		code, msg, _ := e.call(e.h.forwardCreate, "7", 1, `{"name":"x","tunnelId":25,"remoteAddr":"1.1.1.1:443","entryKey":"node-23"}`)
		if code == 0 || msg != "入口已变化，请重新选择隧道" {
			t.Fatalf("user node key: %d %q", code, msg)
		}
		// Admin edits the user's rule: resolved against the owner's entries.
		if code, msg, _ := e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"entryKey":"node-23"}`, rule)); code != 0 {
			t.Fatalf("admin rebind: %d %s", code, msg)
		}
		e.expect(rule, []int64{23}, "23")
		// User 8 only has entry 23: admin cannot bind its rule to 51.
		r8 := e.createRule("1", 0, `,"userId":8,"inPort":15022`)
		e.expect(r8, []int64{23}, "")
		code, msg, _ = e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"entryKey":"node-51"}`, r8))
		if code == 0 || msg != "入口已变化，请重新选择隧道" {
			t.Fatalf("admin bind outside owner's grant: %d %q", code, msg)
		}
		// A key covering every allowed entry is stored unbound.
		if code, msg, _ := e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"entryKey":"node-23"}`, r8)); code != 0 {
			t.Fatalf("all-entry key: %d %s", code, msg)
		}
		e.expect(r8, []int64{23}, "")
	})
}

func TestEntryBindStaleFallbackAndReconcile(t *testing.T) {
	bindDialects(t, func(t *testing.T, dialect string) {
		e := newBindEnv(t, dialect, true)
		pruned := e.createRule("1", 0, `,"inPort":15031,"entryKey":"node-51"`)
		e.r.DB().Model(&model.Forward{}).Where("id = ?", pruned).Update("entry_node_ids", "51,52")
		e.r.DB().Exec("DELETE FROM forward_port WHERE forward_id = ?", pruned)
		e.r.DB().Create(&model.ForwardPort{ForwardID: pruned, NodeID: 51, Port: 15031})
		e.r.DB().Create(&model.ForwardPort{ForwardID: pruned, NodeID: 52, Port: 15031})
		cleared := e.createRule("1", 0, `,"inPort":15032,"entryKey":"node-51"`)
		user := e.createRule("7", 1, `,"inPort":15033,"entryKey":"`+userKey(t, e, 51)+`"`)
		e.expect(user, []int64{51, 52}, "51,52")

		// Entry 51 leaves the tunnel (tunnel edit): bindings fall back.
		old, _ := e.h.tunnelEntryNodeIDs(25)
		e.r.DB().Exec("DELETE FROM chain_tunnel WHERE tunnel_id = 25 AND node_id = 51")
		e.r.DB().Exec("DELETE FROM tunnel_group_tunnel_entry WHERE tunnel_id = 25 AND node_id = 51")
		newIDs, _ := e.h.tunnelEntryNodeIDs(25)
		e.resetCmds()
		e.h.cleanupTunnelForwardRuntimesOnRemovedEntryNodes(25, old, newIDs)
		e.h.syncTunnelForwardsEntryPorts(25, newIDs)
		e.h.reconcileTunnelEntryPermissions(0, 25)
		e.expect(pruned, []int64{52}, "52")
		e.expect(cleared, []int64{23, 52}, "")
		e.expect(user, []int64{52}, "52")
		if got := e.nodesFor(cleared, "DeleteService"); !reflect.DeepEqual(got, []int64{51}) {
			t.Fatalf("cleared rule removed on %v", got)
		}
		// Idempotent: a second pass changes nothing and sends nothing.
		e.resetCmds()
		e.h.reconcileTunnelEntryPermissions(0, 0)
		e.expect(pruned, []int64{52}, "52")
		e.expect(cleared, []int64{23, 52}, "")
		if len(e.cmds) != 0 {
			t.Fatalf("second pass sent %v", e.cmds)
		}
		// The owner loses the bound entry's grant: cleared to the remaining grant.
		e.r.DB().Exec("DELETE FROM group_permission_grant WHERE user_tunnel_id = 70 AND tunnel_group_id = 8")
		e.h.reconcileTunnelEntryPermissions(7, 0)
		e.expect(user, []int64{23}, "")
	})
}

func TestEntryBindRedeployAndNewEntries(t *testing.T) {
	bindDialects(t, func(t *testing.T, dialect string) {
		e := newBindEnv(t, dialect, false)
		bound := e.createRule("1", 0, `,"inPort":15041,"entryKey":"node-51"`)
		plain := e.createRule("1", 0, `,"inPort":15042`)
		// Tunnel redeploy: the bound rule only goes to 51.
		e.resetCmds()
		if err := e.h.redeployTunnelAndForwards(25); err != nil {
			t.Fatal(err)
		}
		if got := e.nodesFor(bound, ""); !reflect.DeepEqual(got, []int64{51}) {
			t.Fatalf("redeploy sent bound rule to %v", got)
		}
		if got := e.nodesFor(plain, ""); !reflect.DeepEqual(got, []int64{23, 51}) {
			t.Fatalf("redeploy sent plain rule to %v", got)
		}
		// Node-scoped replace (reconnect of 23): never the bound rule.
		e.resetCmds()
		if err := e.h.syncTunnelForwardsOnNode(25, 23); err != nil {
			t.Fatal(err)
		}
		if got := e.nodesFor(bound, ""); len(got) != 0 {
			t.Fatalf("node 23 redeploy touched bound rule: %v", got)
		}
		ids, _ := e.r.ListActiveForwardIDsByNode(23)
		for _, id := range ids {
			if id == bound {
				t.Fatal("bound rule listed for node 23 reconnect")
			}
		}
		// A new tunnel entry: unbound rules gain it, bound ones never do.
		e.create(&model.Node{ID: 60, Name: "new", Secret: "s60", ServerIP: "192.0.2.60", Port: "1000-65535", CreatedTime: e.now, Status: 1})
		old, _ := e.h.tunnelEntryNodeIDs(25)
		e.create(&model.ChainTunnel{TunnelID: 25, ChainType: "1", NodeID: 60, Inx: nullInt(9)})
		newIDs, _ := e.h.tunnelEntryNodeIDs(25)
		tx := e.r.BeginTx()
		if err := e.h.validateTunnelEntryPortConflictsForNewEntriesTx(tx, 25, old, newIDs); err != nil {
			t.Fatal(err)
		}
		tx.Rollback()
		e.h.syncTunnelForwardsEntryPorts(25, newIDs)
		e.h.reconcileTunnelEntryPermissions(0, 25)
		e.expect(bound, []int64{51}, "51")
		e.expect(plain, []int64{23, 51, 60}, "")
		// Moving to another tunnel clears the binding (batch 换隧道, delete migration).
		if err := e.r.UpdateForwardTunnel(bound, 25, e.now); err != nil {
			t.Fatal(err)
		}
		if got := e.binding(bound); got != "" {
			t.Fatalf("move kept binding %q", got)
		}
	})
}

func TestTunnelEntryNameRendering(t *testing.T) {
	bindDialects(t, func(t *testing.T, dialect string) {
		e := newBindEnv(t, dialect, false)
		e.r.DB().Model(&model.Node{}).Where("id = ?", 23).Update("entry_alias", "mk沪日ixp440")
		e.create(&model.Tunnel{ID: 26, Name: "plain -> 出口HK", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 1, CreatedTime: e.now, UpdatedTime: e.now, Status: 1})
		e.create(&model.ChainTunnel{TunnelID: 26, ChainType: "1", NodeID: 23})
		find := func(items []map[string]interface{}, id int64) map[string]interface{} {
			for _, item := range items {
				if asInt64(item["id"], 0) == id {
					return item
				}
			}
			t.Fatalf("tunnel %d missing", id)
			return nil
		}
		admin, err := e.r.ListEnabledTunnelSummariesWithGroups()
		if err != nil {
			t.Fatal(err)
		}
		a := find(admin, 25)
		if a["name"] != "mk沪日ixp440/vmsilo 沪日首发 -> 出口JP(Akari JP)" {
			t.Fatalf("admin generic name %v", a["name"])
		}
		groups, _ := json.Marshal(a["entryGroups"])
		var named []struct{ Key, Name string }
		_ = json.Unmarshal(groups, &named)
		names := map[string]string{}
		for _, g := range named {
			names[g.Key] = g.Name
		}
		if names["node-23"] != "mk沪日ixp440 -> 出口JP(Akari JP)" || names["node-51"] != "vmsilo 沪日首发 -> 出口JP(Akari JP)" {
			t.Fatalf("admin per-entry names %s", groups)
		}
		if find(admin, 26)["name"] != "plain -> 出口HK" {
			t.Fatal("plain name changed")
		}
		// User 8 (entry 23 only): exactly the alias, like the old name.
		u8, err := e.r.ListUserAccessibleTunnelsWithGroups(8)
		if err != nil {
			t.Fatal(err)
		}
		if got := find(u8, 25)["name"]; got != "mk沪日ixp440 -> 出口JP(Akari JP)" {
			t.Fatalf("user 8 name %v", got)
		}
		// User 7 (both): no alias on 51 → public label, never the node name.
		u7, _ := e.r.ListUserAccessibleTunnelsWithGroups(7)
		item := find(u7, 25)
		raw, _ := json.Marshal(item)
		if item["name"] != "mk沪日ixp440/上海B -> 出口JP(Akari JP)" || strings.Contains(string(raw), "vmsilo") || item["nameTemplate"] != nil {
			t.Fatalf("user 7 item %s", raw)
		}
		// Admin tunnel page: rendered name + raw template for the edit form.
		list, _ := e.r.ListTunnels()
		lt := find(list, 25)
		if lt["nameTemplate"] != "{入口} -> 出口JP(Akari JP)" || lt["name"] != "mk沪日ixp440/vmsilo 沪日首发 -> 出口JP(Akari JP)" {
			t.Fatalf("tunnel list %v / %v", lt["name"], lt["nameTemplate"])
		}
		if find(list, 26)["nameTemplate"] != nil {
			t.Fatal("plain tunnel got a template")
		}
		// Renderer batch equals the per-user restriction (user 8 → entry 23 only).
		tr, err := e.r.NewTunnelNameRenderer(repo.TunnelNameViewer{UserID: 8}, map[int64]string{25: "{入口} -> X"})
		if err != nil || tr.Name(25, "{入口} -> X") != "mk沪日ixp440 -> X" {
			t.Fatalf("renderer user 8: %v", err)
		}
		// Admin display without alias falls back to the node name.
		if got := e.r.RenderTunnelNamesAdmin(map[int64]string{25: "{入口}"})[25]; got != "mk沪日ixp440/vmsilo 沪日首发" {
			t.Fatalf("admin fallback %q", got)
		}
	})
}

func TestTunnelNameRendererNoQueryWithoutToken(t *testing.T) {
	e := newBindEnv(t, "sqlite", false)
	var queries int
	db := e.r.DB()
	_ = db.Callback().Query().After("gorm:query").Register("plan152:count", func(*gorm.DB) { queries++ })
	_ = db.Callback().Raw().After("gorm:raw").Register("plan152:countraw", func(*gorm.DB) { queries++ })
	tr, err := e.r.NewTunnelNameRenderer(repo.TunnelNameViewer{UserID: 7}, map[int64]string{25: "plain", 26: "x -> y"})
	if err != nil || queries != 0 || tr.Name(25, "plain") != "plain" {
		t.Fatalf("plain names cost %d queries (err %v)", queries, err)
	}
}

func TestEntryLatencyKeys(t *testing.T) {
	e := newBindEnv(t, "sqlite", false)
	e.h.pathLatency = map[int64]tunnelPathLatency{25: {TunnelID: 25, UpdatedAt: time.Now().UnixMilli(), Entries: []pathEntryLatency{
		{NodeID: 23, EntryName: "Mkcloud 沪日ixp 440", LatencyMS: 26, Status: "ok"},
		{NodeID: 51, EntryName: "vmsilo 沪日首发", LatencyMS: 31, Status: "ok"},
	}}}
	get := func(sub string, role int) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: sub, RoleID: role}))
		w := httptest.NewRecorder()
		e.h.userTunnelLatency(w, req)
		return w.Body.String()
	}
	if body := get("1", 0); !strings.Contains(body, `"entryKey":"node-23"`) || !strings.Contains(body, `"entryKey":"node-51"`) {
		t.Fatalf("admin latency %s", body)
	}
	body := get("7", 1)
	if !strings.Contains(body, `"entryKey":"`+userKey(t, e, 23)+`"`) || !strings.Contains(body, `"entryKey":"`+userKey(t, e, 51)+`"`) || strings.Contains(body, "vmsilo") || strings.Contains(body, "node-") {
		t.Fatalf("user latency %s", body)
	}
}

func TestNodeEntryAliasValidation(t *testing.T) {
	if _, err := normalizeNodeEntryAlias(strings.Repeat("长", 101)); err == nil {
		t.Fatal("101 chars accepted")
	}
	if _, err := normalizeNodeEntryAlias("a{入口}"); err == nil {
		t.Fatal("token accepted")
	}
	if v, err := normalizeNodeEntryAlias("  mk沪日ixp440 "); err != nil || v != "mk沪日ixp440" {
		t.Fatalf("trim: %q %v", v, err)
	}
}
