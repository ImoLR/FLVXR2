package contract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
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

// Plan 155: /tunnel/split-entry + /tunnel/merge-entry, and a regression check
// that the original multi-entry tunnel feature (create/edit/deploy) behaves as
// before. Mock agents record every command per node.

const (
	splitEntryA = int64(901) // like node 23: v4 entry, rules listen here
	splitEntryB = int64(902) // like node 51: v6-only entry, no rules
	splitHop1   = int64(903)
	splitHop2a  = int64(904)
	splitHop2b  = int64(905)
	splitExit   = int64(906)
)

type splitLab struct {
	t      *testing.T
	r      *repo.Repository
	h      *handler.Handler
	router http.Handler
	srv    *httptest.Server
	admin  string
	secret string
	mu     sync.Mutex
	cmds   map[int64][]string
	last   time.Time
}

func newSplitLab(t *testing.T, dialect string) *splitLab {
	t.Helper()
	l := &splitLab{t: t, r: landingContractRepo(t, dialect), secret: "split-contract", cmds: map[int64][]string{}}
	l.h = handler.New(l.r, l.secret, "test")
	l.router = httpserver.NewRouter(l.h, l.secret, l.r)
	l.srv = httptest.NewServer(l.router)
	t.Cleanup(l.srv.Close)
	l.admin, _ = auth.GenerateToken(1, "admin", 0, l.secret)
	now := time.Now().UnixMilli()
	nodes := []struct {
		id         int64
		name, ip   string
		v4, v6     string
		portRange  string
		listenAddr string
	}{
		{splitEntryA, "entry-a", "198.51.100.23", "198.51.100.23", "", "20000-20099", "[::]"},
		{splitEntryB, "entry-b", "2001:db8::51", "", "2001:db8::51", "21000-21099", "[::]"},
		{splitHop1, "hop-1", "198.51.100.3", "198.51.100.3", "", "22000-22099", "[::]"},
		{splitHop2a, "hop-2a", "198.51.100.4", "198.51.100.4", "", "23000-23099", "[::]"},
		{splitHop2b, "hop-2b", "198.51.100.5", "198.51.100.5", "", "24000-24099", "[::]"},
		{splitExit, "exit", "198.51.100.6", "198.51.100.6", "", "25000-25099", "[::]"},
	}
	for _, n := range nodes {
		var v4, v6 interface{}
		if n.v4 != "" {
			v4 = n.v4
		}
		if n.v6 != "" {
			v6 = n.v6
		}
		if err := l.r.DB().Exec(`INSERT INTO node(id, name, secret, server_ip, server_ip_v4, server_ip_v6, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx)
			VALUES(?, ?, ?, ?, ?, ?, ?, '', 'v1', 1, 1, 1, ?, ?, 0, ?, ?, 0)`,
			n.id, n.name, fmt.Sprintf("split-node-%d", n.id), n.ip, v4, v6, n.portRange, now, now, n.listenAddr, n.listenAddr).Error; err != nil {
			t.Fatalf("insert node %d: %v", n.id, err)
		}
	}
	ids := []int64{}
	for _, n := range nodes {
		id := n.id
		ids = append(ids, id)
		stop := startMockNodeSessionWithCommandRecorder(t, l.srv.URL, fmt.Sprintf("split-node-%d", id), func(cmd string, data json.RawMessage) (bool, string) {
			l.record(id, cmd, data)
			return false, ""
		})
		t.Cleanup(stop)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var online int64
		l.r.DB().Model(&model.Node{}).Where("id IN ? AND status = 1", ids).Count(&online)
		if online == int64(len(ids)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mock agents did not come online")
		}
		time.Sleep(50 * time.Millisecond)
	}
	l.quiet()
	l.reset()
	return l
}

// record keeps "<Type> <object names>" per node (ListServices/metrics noise skipped).
func (l *splitLab) record(nodeID int64, cmd string, data json.RawMessage) {
	switch cmd {
	case "ListServices", "GetServiceClientIPs", "TcpPing", "GetCurrentConns", "ListNftablesRules":
		return
	}
	names := splitCommandNames(data)
	l.mu.Lock()
	l.cmds[nodeID] = append(l.cmds[nodeID], strings.TrimSpace(cmd+" "+strings.Join(names, ",")))
	l.last = time.Now()
	l.mu.Unlock()
}

func splitCommandNames(data json.RawMessage) []string {
	var names []string
	add := func(m map[string]interface{}) {
		for _, k := range []string{"name", "chain"} {
			if s, ok := m[k].(string); ok && s != "" {
				names = append(names, s)
			}
		}
		if list, ok := m["services"].([]interface{}); ok {
			for _, v := range list {
				if s, ok := v.(string); ok {
					names = append(names, s)
				} else if mm, ok := v.(map[string]interface{}); ok {
					if s, ok := mm["name"].(string); ok {
						names = append(names, s)
					}
				}
			}
		}
	}
	var obj map[string]interface{}
	if json.Unmarshal(data, &obj) == nil {
		add(obj)
	}
	var arr []map[string]interface{}
	if json.Unmarshal(data, &arr) == nil {
		for _, m := range arr {
			add(m)
		}
	}
	sort.Strings(names)
	return names
}

func (l *splitLab) quiet() {
	for i := 0; i < 60; i++ {
		time.Sleep(100 * time.Millisecond)
		l.mu.Lock()
		idle := time.Since(l.last) > 400*time.Millisecond
		l.mu.Unlock()
		if idle {
			return
		}
	}
}

func (l *splitLab) reset() {
	l.mu.Lock()
	l.cmds = map[int64][]string{}
	l.mu.Unlock()
}

func (l *splitLab) commands() map[int64][]string {
	l.quiet()
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[int64][]string{}
	for k, v := range l.cmds {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func (l *splitLab) call(token, path string, body interface{}) response.R {
	l.t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	l.router.ServeHTTP(res, req)
	var out response.R
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		l.t.Fatalf("%s: decode %q: %v", path, res.Body.String(), err)
	}
	return out
}

func (l *splitLab) ok(token, path string, body interface{}) response.R {
	l.t.Helper()
	out := l.call(token, path, body)
	if out.Code != 0 {
		l.t.Fatalf("%s: code=%d msg=%s", path, out.Code, out.Msg)
	}
	return out
}

func (l *splitLab) tunnelID(name string) int64 {
	l.t.Helper()
	var ids []int64
	l.r.DB().Model(&model.Tunnel{}).Where("name = ?", name).Pluck("id", &ids)
	if len(ids) != 1 {
		l.t.Fatalf("tunnel %q: %v", name, ids)
	}
	return ids[0]
}

func (l *splitLab) chains(tunnelID int64) []model.ChainTunnel {
	var rows []model.ChainTunnel
	l.r.DB().Where("tunnel_id = ?", tunnelID).Order("chain_type ASC, inx ASC, id ASC").Find(&rows)
	return rows
}

func (l *splitLab) entries(tunnelID int64) []int64 {
	var ids []int64
	l.r.DB().Model(&model.ChainTunnel{}).Where("tunnel_id = ? AND chain_type = '1'", tunnelID).Order("inx ASC, id ASC").Pluck("node_id", &ids)
	return ids
}

func (l *splitLab) tunnel(id int64) model.Tunnel {
	var t model.Tunnel
	l.r.DB().Where("id = ?", id).First(&t)
	return t
}

func (l *splitLab) groupEntries(groupID int64) map[int64][]int64 {
	out := map[int64][]int64{}
	var members []model.TunnelGroupTunnel
	l.r.DB().Where("tunnel_group_id = ?", groupID).Find(&members)
	for _, m := range members {
		out[m.TunnelID] = []int64{}
	}
	var rows []model.TunnelGroupTunnelEntry
	l.r.DB().Where("tunnel_group_id = ?", groupID).Order("node_id ASC").Find(&rows)
	for _, e := range rows {
		out[e.TunnelID] = append(out[e.TunnelID], e.NodeID)
	}
	return out
}

func (l *splitLab) userTunnel(userID, tunnelID int64) (model.UserTunnel, bool) {
	var ut model.UserTunnel
	err := l.r.DB().Where("user_id = ? AND tunnel_id = ?", userID, tunnelID).First(&ut).Error
	return ut, err == nil
}

func (l *splitLab) forwardPorts() []string {
	var rows []model.ForwardPort
	l.r.DB().Order("forward_id ASC, node_id ASC").Find(&rows)
	out := []string{}
	for _, p := range rows {
		out = append(out, fmt.Sprintf("%d@%d:%d", p.ForwardID, p.NodeID, p.Port))
	}
	return out
}

func multiEntryTunnelPayload(name string, entries ...int64) map[string]interface{} {
	in := []map[string]interface{}{}
	for i, id := range entries {
		item := map[string]interface{}{"nodeId": id, "protocol": "tls"}
		if i == 0 {
			item["connectIpType"] = "v4"
		}
		in = append(in, item)
	}
	return map[string]interface{}{
		"name": name, "type": 2, "flow": 2, "trafficRatio": 1.5, "status": 1, "http": 1, "tls": 1, "socks": 1,
		"inNodeId": in,
		"chainNodes": []interface{}{
			[]map[string]interface{}{{"nodeId": splitHop1, "protocol": "tls", "strategy": "round", "connectIpType": "v4"}},
			[]map[string]interface{}{
				{"nodeId": splitHop2a, "protocol": "tls", "strategy": "round", "connectIpType": "v4"},
				{"nodeId": splitHop2b, "protocol": "tls", "strategy": "round", "connectIpType": "v4"},
			},
		},
		"outNodeId": []map[string]interface{}{{"nodeId": splitExit, "protocol": "tls", "strategy": "round", "connectIpType": "v4"}},
	}
}

func countPrefix(cmds []string, prefix string) int {
	n := 0
	for _, c := range cmds {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// TestMultiEntryTunnelUnchangedContract pins the original multi-entry feature:
// create deploys every entry, edit (entry add/remove) and redeploy work as
// before — plan 155 must not change any of it.
func TestMultiEntryTunnelUnchangedContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			l := newSplitLab(t, dialect)
			l.ok(l.admin, "/api/v1/tunnel/create", multiEntryTunnelPayload("multi", splitEntryA, splitEntryB))
			id := l.tunnelID("multi")
			if got := l.entries(id); fmt.Sprint(got) != fmt.Sprint([]int64{splitEntryA, splitEntryB}) {
				t.Fatalf("entries %v", got)
			}
			tun := l.tunnel(id)
			if tun.InIP.String != "198.51.100.23,2001:db8::51" {
				t.Fatalf("in_ip %q", tun.InIP.String)
			}
			cmds := l.commands()
			for _, n := range []int64{splitEntryA, splitEntryB} {
				if countPrefix(cmds[n], fmt.Sprintf("AddChains chains_%d", id)) != 1 {
					t.Fatalf("entry %d: %v", n, cmds[n])
				}
			}
			for _, n := range []int64{splitHop1, splitHop2a, splitHop2b, splitExit} {
				if countPrefix(cmds[n], fmt.Sprintf("AddService %d_tls", id)) != 1 {
					t.Fatalf("hop %d: %v", n, cmds[n])
				}
			}
			// Edit: drop entry B through the regular update — full redeploy as before.
			l.reset()
			upd := multiEntryTunnelPayload("multi", splitEntryA)
			upd["id"] = id
			l.ok(l.admin, "/api/v1/tunnel/update", upd)
			if got := l.entries(id); fmt.Sprint(got) != fmt.Sprint([]int64{splitEntryA}) {
				t.Fatalf("entries after update %v", got)
			}
			cmds = l.commands()
			if countPrefix(cmds[splitEntryB], fmt.Sprintf("DeleteChains chains_%d", id)) != 1 {
				t.Fatalf("entry B after update: %v", cmds[splitEntryB])
			}
			for _, n := range []int64{splitHop1, splitExit} {
				if countPrefix(cmds[n], fmt.Sprintf("DeleteService %d_tls", id)) != 1 || countPrefix(cmds[n], fmt.Sprintf("AddService %d_tls", id)) != 1 {
					t.Fatalf("hop %d after update (expected delete+add, the listener restart plan 155 avoids): %v", n, cmds[n])
				}
			}
			// Edit: add entry B back; redeploy pushes both entries.
			l.reset()
			upd = multiEntryTunnelPayload("multi", splitEntryA, splitEntryB)
			upd["id"] = id
			l.ok(l.admin, "/api/v1/tunnel/update", upd)
			if got := l.entries(id); fmt.Sprint(got) != fmt.Sprint([]int64{splitEntryA, splitEntryB}) {
				t.Fatalf("entries after re-add %v", got)
			}
			if countPrefix(l.commands()[splitEntryB], fmt.Sprintf("AddChains chains_%d", id)) != 1 {
				t.Fatal("entry B not redeployed on re-add")
			}
			l.reset()
			l.ok(l.admin, "/api/v1/tunnel/batch-redeploy", map[string]interface{}{"ids": []int64{id}})
			cmds = l.commands()
			for _, n := range []int64{splitEntryA, splitEntryB} {
				if countPrefix(cmds[n], fmt.Sprintf("AddChains chains_%d", id)) == 0 {
					t.Fatalf("redeploy entry %d: %v", n, cmds[n])
				}
			}
		})
	}
}

type splitWorld struct {
	l                       *splitLab
	t440, tIEPL             int64 // tunnel groups (线路分组)
	ug440, ugIEPL           int64 // user groups
	uA, uBoth, uDirect      int64 // users: A only (group), A+B (groups), direct
	tunnel, portTunnel      int64
	folder                  int64
	ruleA                   int64
	rawName, rawPortName    string
	inIPBefore, inIPBefore1 string
}

func newSplitWorld(t *testing.T, dialect string) *splitWorld {
	l := newSplitLab(t, dialect)
	w := &splitWorld{l: l, rawName: "{入口} -> 出口JP(test)", rawPortName: "{入口} -> 出口JP(端口转发)"}
	now := time.Now().UnixMilli()
	exp := time.Now().Add(365 * 24 * time.Hour).UnixMilli()
	for _, u := range []model.User{
		{ID: 951, User: "split-a", Pwd: "x", RoleID: 1, Status: 1, Flow: 500, Num: 20, ExpTime: exp, FlowResetTime: 1, CreatedTime: now},
		{ID: 952, User: "split-both", Pwd: "x", RoleID: 1, Status: 1, Flow: 500, Num: 20, ExpTime: exp, FlowResetTime: 1, CreatedTime: now},
		{ID: 953, User: "split-direct", Pwd: "x", RoleID: 1, Status: 1, Flow: 500, Num: 20, ExpTime: exp, FlowResetTime: 1, CreatedTime: now},
	} {
		u := u
		if err := l.r.DB().Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	w.uA, w.uBoth, w.uDirect = 951, 952, 953
	l.ok(l.admin, "/api/v1/tunnel/create", multiEntryTunnelPayload(w.rawName, splitEntryA, splitEntryB))
	w.tunnel = l.tunnelID(w.rawName)
	port := map[string]interface{}{"name": w.rawPortName, "type": 1, "flow": 2, "trafficRatio": 1, "status": 1, "http": 1, "tls": 1, "socks": 1,
		"inNodeId": []map[string]interface{}{{"nodeId": splitEntryA, "protocol": "tls"}, {"nodeId": splitEntryB, "protocol": "tls"}}}
	l.ok(l.admin, "/api/v1/tunnel/create", port)
	w.portTunnel = l.tunnelID(w.rawPortName)
	w.inIPBefore = l.tunnel(w.tunnel).InIP.String
	w.inIPBefore1 = l.tunnel(w.portTunnel).InIP.String
	// A tunnel created after them (inx shift check) and a tunnel-page folder.
	l.ok(l.admin, "/api/v1/tunnel/create", map[string]interface{}{"name": "later", "type": 1, "flow": 2, "status": 1,
		"inNodeId": []map[string]interface{}{{"nodeId": splitHop1, "protocol": "tls"}}})
	folder := model.TunnelGroupNew{Name: "folder-440", CreatedTime: now, UpdatedTime: now}
	if err := l.r.DB().Create(&folder).Error; err != nil {
		t.Fatal(err)
	}
	w.folder = folder.ID
	if err := l.r.AssignTunnelToGroupNew(w.tunnel, []int64{w.folder}); err != nil {
		t.Fatal(err)
	}
	// 线路分组 like prod: 440 = entry A of both tunnels, IEPL = entry B.
	for _, g := range []*model.TunnelGroup{{Name: "沪日440", Status: 1, CreatedTime: now, UpdatedTime: now}, {Name: "沪日IEPL", Status: 1, CreatedTime: now, UpdatedTime: now}} {
		if err := l.r.DB().Create(g).Error; err != nil {
			t.Fatal(err)
		}
		if g.Name == "沪日440" {
			w.t440 = g.ID
		} else {
			w.tIEPL = g.ID
		}
	}
	both := []int64{w.tunnel, w.portTunnel}
	l.ok(l.admin, "/api/v1/group/tunnel/assign", map[string]interface{}{"groupId": w.t440, "tunnelIds": both,
		"tunnelEntries": map[string][]int64{fmt.Sprint(w.tunnel): {splitEntryA}, fmt.Sprint(w.portTunnel): {splitEntryA}}})
	l.ok(l.admin, "/api/v1/group/tunnel/assign", map[string]interface{}{"groupId": w.tIEPL, "tunnelIds": both,
		"tunnelEntries": map[string][]int64{fmt.Sprint(w.tunnel): {splitEntryB}, fmt.Sprint(w.portTunnel): {splitEntryB}}})
	for _, g := range []*model.UserGroup{{Name: "ug-440", Status: 1, CreatedTime: now, UpdatedTime: now}, {Name: "ug-iepl", Status: 1, CreatedTime: now, UpdatedTime: now}} {
		if err := l.r.DB().Create(g).Error; err != nil {
			t.Fatal(err)
		}
		if g.Name == "ug-440" {
			w.ug440 = g.ID
		} else {
			w.ugIEPL = g.ID
		}
	}
	l.ok(l.admin, "/api/v1/group/user/assign", map[string]interface{}{"groupId": w.ug440, "userIds": []int64{w.uA, w.uBoth}})
	l.ok(l.admin, "/api/v1/group/user/assign", map[string]interface{}{"groupId": w.ugIEPL, "userIds": []int64{w.uBoth}})
	l.ok(l.admin, "/api/v1/group/permission/assign", map[string]interface{}{"userGroupId": w.ug440, "tunnelGroupId": w.t440})
	l.ok(l.admin, "/api/v1/group/permission/assign", map[string]interface{}{"userGroupId": w.ugIEPL, "tunnelGroupId": w.tIEPL})
	// Direct grant (no group): sees every entry.
	l.ok(l.admin, "/api/v1/tunnel/user/assign", map[string]interface{}{"userId": w.uDirect, "tunnelId": w.tunnel, "flow": 77, "num": 7, "expTime": exp, "flowResetTime": 9, "status": 1})
	if err := l.r.DB().Model(&model.UserTunnel{}).Where("user_id = ? AND tunnel_id = ?", w.uDirect, w.tunnel).Updates(map[string]interface{}{"in_flow": 1234, "out_flow": 5678}).Error; err != nil {
		t.Fatal(err)
	}
	// Rule of the A-only user: listens on entry A only (like the 13 prod rules).
	tokA, _ := auth.GenerateToken(w.uA, "split-a", 1, l.secret)
	l.ok(tokA, "/api/v1/forward/create", map[string]interface{}{"tunnelId": w.tunnel, "name": "rule-a", "remoteAddr": "1.1.1.1:443"})
	var fid []int64
	l.r.DB().Model(&model.Forward{}).Where("name = ?", "rule-a").Pluck("id", &fid)
	w.ruleA = fid[0]
	if got := l.forwardPorts(); len(got) != 1 || !strings.Contains(got[0], fmt.Sprintf("@%d:", splitEntryA)) {
		t.Fatalf("rule ports %v", got)
	}
	l.quiet()
	l.reset()
	return w
}

func splitDryRun(dry bool) *bool { return &dry }

func TestTunnelSplitEntryContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			w := newSplitWorld(t, dialect)
			l := w.l
			name, twinName := "entry-a -> 出口JP(test)", "entry-b -> 出口JP(test)"
			portsBefore := l.forwardPorts()
			chainsBefore := l.chains(w.tunnel)
			inxBefore := l.tunnel(w.tunnel).Inx
			// Lists sort by inx ASC (new tunnels get the lowest): put "later" right after the original.
			if err := l.r.DB().Model(&model.Tunnel{}).Where("name = ?", "later").Update("inx", inxBefore+1).Error; err != nil {
				t.Fatal(err)
			}
			laterInx := l.tunnel(l.tunnelID("later")).Inx
			portInx := l.tunnel(w.portTunnel).Inx
			utDirect, _ := l.userTunnel(w.uDirect, w.tunnel)

			// Dry run: full plan, nothing written, nothing pushed.
			out := l.ok(l.admin, "/api/v1/tunnel/split-entry", map[string]interface{}{"tunnelId": w.tunnel, "entryNodeId": splitEntryB, "name": name, "twinName": twinName})
			plan, _ := json.Marshal(out.Data)
			var p struct {
				DryRun       bool
				EntriesAfter []int64
				Users        []struct {
					UserID int64
					Twin   string
				}
				Groups []struct {
					GroupID        int64
					TwinJoins      bool
					OriginalLeaves bool
				}
			}
			_ = json.Unmarshal(plan, &p)
			if !p.DryRun || fmt.Sprint(p.EntriesAfter) != fmt.Sprint([]int64{splitEntryA}) {
				t.Fatalf("dry run plan %s", plan)
			}
			modes := map[int64]string{}
			for _, u := range p.Users {
				modes[u.UserID] = u.Twin
			}
			if modes[w.uA] != "none" || modes[w.uBoth] != "group" || modes[w.uDirect] != "direct" {
				t.Fatalf("user modes %v", modes)
			}
			for _, g := range p.Groups {
				if g.GroupID == w.tIEPL && !(g.TwinJoins && g.OriginalLeaves) || g.GroupID == w.t440 && (g.TwinJoins || g.OriginalLeaves) {
					t.Fatalf("group plan %s", plan)
				}
			}
			if _, err := l.r.TunnelIDByName(twinName); err != nil || len(l.entries(w.tunnel)) != 2 || l.tunnel(w.tunnel).Name != w.rawName {
				t.Fatal("dry run wrote something")
			}
			if cmds := l.commands(); len(cmds) != 0 {
				t.Fatalf("dry run pushed %v", cmds)
			}

			// Apply.
			out = l.ok(l.admin, "/api/v1/tunnel/split-entry", map[string]interface{}{"tunnelId": w.tunnel, "entryNodeId": splitEntryB, "name": name, "twinName": twinName, "dryRun": false})
			twin := l.tunnelID(twinName)
			orig := l.tunnel(w.tunnel)
			if orig.Name != name || fmt.Sprint(l.entries(w.tunnel)) != fmt.Sprint([]int64{splitEntryA}) || orig.InIP.String != "198.51.100.23" {
				t.Fatalf("original after split: %q %v %q", orig.Name, l.entries(w.tunnel), orig.InIP.String)
			}
			tw := l.tunnel(twin)
			if fmt.Sprint(l.entries(twin)) != fmt.Sprint([]int64{splitEntryB}) || tw.InIP.String != "2001:db8::51" || tw.Type != 2 ||
				tw.TrafficRatio != orig.TrafficRatio || tw.Flow != orig.Flow || tw.HTTP != 1 || tw.TLS != 1 || tw.Socks != 1 || tw.Status != 1 {
				t.Fatalf("twin %+v entries %v", tw, l.entries(twin))
			}
			if tw.Inx != inxBefore+1 || l.tunnel(l.tunnelID("later")).Inx != laterInx+1 || orig.Inx != inxBefore || l.tunnel(w.portTunnel).Inx != portInx {
				t.Fatalf("inx: orig %d→%d twin %d later %d→%d", inxBefore, orig.Inx, tw.Inx, laterInx, l.tunnel(l.tunnelID("later")).Inx)
			}
			// Same hop chain, fresh ports; the original's chain rows unchanged except entry B.
			hopKey := func(rows []model.ChainTunnel, withPort bool) []string {
				out := []string{}
				for _, c := range rows {
					if c.ChainType == "1" {
						continue
					}
					k := fmt.Sprintf("%s/%d/%d/%s/%s/%s", c.ChainType, c.Inx.Int64, c.NodeID, c.Protocol.String, c.Strategy.String, c.ConnectIPType.String)
					if withPort {
						k += fmt.Sprintf(":%d", c.Port.Int64)
					}
					out = append(out, k)
				}
				return out
			}
			if fmt.Sprint(hopKey(l.chains(twin), false)) != fmt.Sprint(hopKey(chainsBefore, false)) {
				t.Fatalf("twin hops %v vs %v", hopKey(l.chains(twin), false), hopKey(chainsBefore, false))
			}
			if fmt.Sprint(hopKey(l.chains(w.tunnel), true)) != fmt.Sprint(hopKey(chainsBefore, true)) {
				t.Fatal("original hop rows changed")
			}
			for _, c := range l.chains(twin) {
				for _, o := range chainsBefore {
					if c.ChainType != "1" && c.NodeID == o.NodeID && c.Port.Int64 == o.Port.Int64 {
						t.Fatalf("twin reuses port %d on node %d", c.Port.Int64, c.NodeID)
					}
				}
			}
			if got := l.forwardPorts(); fmt.Sprint(got) != fmt.Sprint(portsBefore) {
				t.Fatalf("rule ports changed %v → %v", portsBefore, got)
			}
			// Groups: 440 unchanged, IEPL now holds the twin with entry B.
			if g := l.groupEntries(w.t440); fmt.Sprint(g[w.tunnel]) != fmt.Sprint([]int64{splitEntryA}) || len(g) != 2 {
				t.Fatalf("440 after split %v", g)
			}
			if g := l.groupEntries(w.tIEPL); fmt.Sprint(g[twin]) != fmt.Sprint([]int64{splitEntryB}) {
				t.Fatalf("IEPL after split %v", g)
			} else if _, still := g[w.tunnel]; still {
				t.Fatalf("original still in IEPL %v", g)
			}
			var folders int64
			l.r.DB().Model(&model.TunnelGroupTunnelNew{}).Where("tunnel_group_id = ? AND tunnel_id = ?", w.folder, twin).Count(&folders)
			if folders != 1 {
				t.Fatal("twin not in the folder")
			}
			// Users: A-only none; both via group with copied limits; direct copied, fresh counters.
			if _, ok := l.userTunnel(w.uA, twin); ok {
				t.Fatal("A-only user got the twin")
			}
			if _, ok := l.userTunnel(w.uA, w.tunnel); !ok {
				t.Fatal("A-only user lost the original")
			}
			origBoth, _ := l.userTunnel(w.uBoth, w.tunnel)
			twBoth, ok := l.userTunnel(w.uBoth, twin)
			if !ok || twBoth.Num != origBoth.Num || twBoth.Flow != origBoth.Flow || twBoth.ExpTime != origBoth.ExpTime {
				t.Fatalf("both-user twin row %+v vs %+v", twBoth, origBoth)
			}
			twDirect, ok := l.userTunnel(w.uDirect, twin)
			if !ok || twDirect.Flow != 77 || twDirect.Num != 7 || twDirect.FlowResetTime != 9 || twDirect.ExpTime != utDirect.ExpTime || twDirect.InFlow != 0 || twDirect.OutFlow != 0 {
				t.Fatalf("direct twin row %+v", twDirect)
			}
			if d, _ := l.userTunnel(w.uDirect, w.tunnel); d.InFlow != 1234 || d.OutFlow != 5678 {
				t.Fatal("original counters touched")
			}
			// Pushes: entry A and the original's hop services are not touched at all.
			cmds := l.commands()
			if len(cmds[splitEntryA]) != 0 {
				t.Fatalf("entry A got %v", cmds[splitEntryA])
			}
			wantB := map[string]int{fmt.Sprintf("AddChains chains_%d", twin): 1, fmt.Sprintf("DeleteChains chains_%d", w.tunnel): 1, "SetProtocol": 1}
			for k, n := range wantB {
				if countPrefix(cmds[splitEntryB], k) != n {
					t.Fatalf("entry B: want %d×%q, got %v", n, k, cmds[splitEntryB])
				}
			}
			if len(cmds[splitEntryB]) != 3 {
				t.Fatalf("entry B pushes %v", cmds[splitEntryB])
			}
			for _, n := range []int64{splitHop1, splitHop2a, splitHop2b, splitExit} {
				for _, c := range cmds[n] {
					if strings.Contains(c, fmt.Sprintf("%d_tls", w.tunnel)) || strings.Contains(c, fmt.Sprintf("chains_%d", w.tunnel)) || strings.HasPrefix(c, "Update") || strings.HasPrefix(c, "Delete") {
						t.Fatalf("hop %d touched the original: %v", n, cmds[n])
					}
				}
				if countPrefix(cmds[n], fmt.Sprintf("AddService %d_tls", twin)) != 1 {
					t.Fatalf("hop %d twin service: %v", n, cmds[n])
				}
			}
			t.Logf("split pushes per node: %v", cmds)

			// Picker: the A-only user sees the original only, with the plain name.
			tokA, _ := auth.GenerateToken(w.uA, "split-a", 1, l.secret)
			list := l.ok(tokA, "/api/v1/tunnel/user/tunnel", map[string]interface{}{})
			raw, _ := json.Marshal(list.Data)
			var picker []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			}
			_ = json.Unmarshal(raw, &picker)
			seen := map[int64]string{}
			for _, it := range picker {
				seen[it.ID] = it.Name
			}
			if seen[w.tunnel] != name || seen[twin] != "" || len(seen) != 2 {
				t.Fatalf("A-only picker %v", seen)
			}

			// Undo.
			l.reset()
			l.ok(l.admin, "/api/v1/tunnel/merge-entry", map[string]interface{}{"tunnelId": w.tunnel, "twinId": twin, "name": w.rawName, "inIp": w.inIPBefore, "dryRun": false})
			orig = l.tunnel(w.tunnel)
			if orig.Name != w.rawName || orig.InIP.String != w.inIPBefore || fmt.Sprint(l.entries(w.tunnel)) != fmt.Sprint([]int64{splitEntryA, splitEntryB}) || orig.Inx != inxBefore {
				t.Fatalf("after undo %q %q %v", orig.Name, orig.InIP.String, l.entries(w.tunnel))
			}
			if fmt.Sprint(hopKey(l.chains(w.tunnel), true)) != fmt.Sprint(hopKey(chainsBefore, true)) {
				t.Fatal("hops changed by undo")
			}
			var left int64
			l.r.DB().Model(&model.Tunnel{}).Where("id = ?", twin).Count(&left)
			if left != 0 {
				t.Fatal("twin not deleted")
			}
			if l.tunnel(l.tunnelID("later")).Inx != laterInx {
				t.Fatal("inx not shifted back")
			}
			if g := l.groupEntries(w.tIEPL); fmt.Sprint(g[w.tunnel]) != fmt.Sprint([]int64{splitEntryB}) {
				t.Fatalf("IEPL after undo %v", g)
			}
			if g := l.groupEntries(w.t440); fmt.Sprint(g[w.tunnel]) != fmt.Sprint([]int64{splitEntryA}) {
				t.Fatalf("440 after undo %v", g)
			}
			if got := l.forwardPorts(); fmt.Sprint(got) != fmt.Sprint(portsBefore) {
				t.Fatalf("rule ports changed by undo %v", got)
			}
			if _, ok := l.userTunnel(w.uBoth, w.tunnel); !ok {
				t.Fatal("both-user lost the original after undo")
			}
			cmds = l.commands()
			if len(cmds[splitEntryA]) != 0 || countPrefix(cmds[splitEntryB], fmt.Sprintf("AddChains chains_%d", w.tunnel)) != 1 || countPrefix(cmds[splitEntryB], fmt.Sprintf("DeleteChains chains_%d", twin)) != 1 {
				t.Fatalf("undo pushes %v", cmds)
			}
			for _, n := range []int64{splitHop1, splitExit} {
				for _, c := range cmds[n] {
					if strings.Contains(c, fmt.Sprintf("%d_tls", w.tunnel)) || strings.Contains(c, fmt.Sprintf("chains_%d", w.tunnel)) {
						t.Fatalf("undo touched the original on hop %d: %v", n, cmds[n])
					}
				}
			}
			t.Logf("undo pushes per node: %v", cmds)
		})
	}
}

func TestTunnelSplitEntryPortForwardAndRefusalsContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			w := newSplitWorld(t, dialect)
			l := w.l
			refuse := func(label string, body map[string]interface{}, want string) {
				t.Helper()
				body["dryRun"] = false
				out := l.call(l.admin, "/api/v1/tunnel/split-entry", body)
				if out.Code == 0 || !strings.Contains(out.Msg, want) {
					t.Fatalf("%s: code=%d msg=%q (want %q)", label, out.Code, out.Msg, want)
				}
			}
			// The rules listen on entry A: splitting A off is refused.
			refuse("rules on entry", map[string]interface{}{"tunnelId": w.tunnel, "entryNodeId": splitEntryA, "name": "x1", "twinName": "x2"}, "规则在该入口上监听")
			refuse("not an entry", map[string]interface{}{"tunnelId": w.tunnel, "entryNodeId": splitHop1, "name": "x1", "twinName": "x2"}, "不是该隧道的入口")
			refuse("token", map[string]interface{}{"tunnelId": w.tunnel, "entryNodeId": splitEntryB, "name": "{入口} x", "twinName": "x2"}, "{入口}")
			refuse("duplicate name", map[string]interface{}{"tunnelId": w.tunnel, "entryNodeId": splitEntryB, "name": "x1", "twinName": "later"}, "已被其他隧道使用")
			if got := l.commands(); len(got) != 0 {
				t.Fatalf("refusals pushed %v", got)
			}
			// Non-admin cannot call it.
			tokA, _ := auth.GenerateToken(w.uA, "split-a", 1, l.secret)
			if out := l.call(tokA, "/api/v1/tunnel/split-entry", map[string]interface{}{"tunnelId": w.tunnel, "entryNodeId": splitEntryB, "name": "x1", "twinName": "x2"}); out.Code != 403 {
				t.Fatalf("user call: %+v", out)
			}

			// Port-forward tunnel (type 1, entries only): twin type 1, no chain objects.
			name, twinName := "entry-a -> 出口JP(端口转发)", "entry-b -> 出口JP(端口转发)"
			l.ok(l.admin, "/api/v1/tunnel/split-entry", map[string]interface{}{"tunnelId": w.portTunnel, "entryNodeId": splitEntryB, "name": name, "twinName": twinName, "dryRun": false})
			twin := l.tunnelID(twinName)
			if l.tunnel(twin).Type != 1 || fmt.Sprint(l.entries(twin)) != fmt.Sprint([]int64{splitEntryB}) || fmt.Sprint(l.entries(w.portTunnel)) != fmt.Sprint([]int64{splitEntryA}) {
				t.Fatalf("port twin %+v", l.tunnel(twin))
			}
			cmds := l.commands()
			if len(cmds[splitEntryA]) != 0 || len(cmds[splitEntryB]) != 1 || countPrefix(cmds[splitEntryB], "SetProtocol") != 1 {
				t.Fatalf("type-1 split pushes %v", cmds)
			}
			// Single entry now: a second split is refused; undo works.
			refuse("single entry", map[string]interface{}{"tunnelId": w.portTunnel, "entryNodeId": splitEntryA, "name": "x1", "twinName": "x2"}, "只有一个入口")
			l.ok(l.admin, "/api/v1/tunnel/merge-entry", map[string]interface{}{"tunnelId": w.portTunnel, "twinId": twin, "name": w.rawPortName, "inIp": w.inIPBefore1, "dryRun": false})
			if fmt.Sprint(l.entries(w.portTunnel)) != fmt.Sprint([]int64{splitEntryA, splitEntryB}) || l.tunnel(w.portTunnel).Name != w.rawPortName || l.tunnel(w.portTunnel).InIP.String != w.inIPBefore1 {
				t.Fatal("port tunnel undo")
			}
			// A user who can only use entry B (IEPL group only) would lose the original: refused.
			now := time.Now().UnixMilli()
			onlyB := model.User{ID: 954, User: "split-only-b", Pwd: "x", RoleID: 1, Status: 1, Flow: 500, Num: 20, ExpTime: time.Now().Add(time.Hour).UnixMilli(), FlowResetTime: 1, CreatedTime: now}
			if err := l.r.DB().Create(&onlyB).Error; err != nil {
				t.Fatal(err)
			}
			l.ok(l.admin, "/api/v1/group/user/assign", map[string]interface{}{"groupId": w.ugIEPL, "userIds": []int64{w.uBoth, onlyB.ID}})
			if _, ok := l.userTunnel(onlyB.ID, w.tunnel); !ok {
				t.Fatal("group sync did not grant the B-only user")
			}
			refuse("user only on B", map[string]interface{}{"tunnelId": w.tunnel, "entryNodeId": splitEntryB, "name": "x1", "twinName": "x2"}, "只能使用该入口")
			if fmt.Sprint(l.entries(w.tunnel)) != fmt.Sprint([]int64{splitEntryA, splitEntryB}) {
				t.Fatal("refused split changed entries")
			}
		})
	}
}
