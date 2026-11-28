package contract_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

// TestNodeGroupTabsContract: plan 324 节点 page groups on SQLite and
// PostgreSQL — one group set with the probe (probe_groups), 救命版 members
// kept on a node-page save, other probe settings untouched; the 利群入口
// node_group listed (admin only), renamed / members added and removed,
// removal refused while 利群汇聚 uses the machine, never deleted (save and
// node-group/delete), never on the public probe.
func TestNodeGroupTabsContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			db := r.DB()
			rows := []interface{}{
				&model.User{ID: 861, User: "khalilgao", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, ExpTime: now + 86400000, FlowResetTime: 1, Num: 10},
				&model.User{ID: 862, User: "wings", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, ExpTime: now + 86400000, FlowResetTime: 1, Num: 10},
				&model.NodeGroup{ID: 5, Name: "利群入口", Color: "#0d9488", CreatedTime: 1},
				&model.NodeGroup{ID: 2, Name: "ixp", Color: "#3b82f6", CreatedTime: 1},
			}
			for _, row := range rows {
				if err := db.Create(row).Error; err != nil {
					t.Fatalf("seed %T: %v", row, err)
				}
			}
			grp := func(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id > 0} }
			for _, n := range []*model.Node{
				{ID: 811, Name: "东京 IIJ", Region: "JP", Secret: "ngt-811", ServerIP: "203.0.113.11", Port: "1-2", Inx: 1, Status: 1},
				{ID: 812, Name: "香港 HKT", Region: "HK", Secret: "ngt-812", ServerIP: "203.0.113.12", Port: "1-2", Inx: 2, Status: 1},
				{ID: 813, Name: "百度云广州", Region: "CN", Secret: "ngt-813", ServerIP: "203.0.113.13", Port: "1-2", Inx: 3, Status: 1, GroupID: grp(5)},
				{ID: 814, Name: "阿里广州", Region: "CN", Secret: "ngt-814", ServerIP: "203.0.113.14", Port: "1-2", Inx: 4, Status: 1, GroupID: grp(5)},
				{ID: 815, Name: "火山上海", Region: "CN", Secret: "ngt-815", ServerIP: "203.0.113.15", Port: "1-2", Inx: 5, Status: 1, GroupID: grp(2)},
				{ID: 816, Name: "火山广州", Region: "CN", Secret: "ngt-816", ServerIP: "203.0.113.16", Port: "1-2", Inx: 6, Status: 1, GroupID: grp(5)},
			} {
				if err := db.Create(n).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, row := range []interface{}{
				&model.LeiKwanSetting{ID: 1, AccountID: 1, EntryGroupID: 5, GroupName: "利群汇聚"},
				&model.LeiKwanLine{ID: 1, AccountID: 1, RouteID: 21, Name: "沪日", Status: 1},
				&model.LeiKwanLine{ID: 2, AccountID: 1, RouteID: 22, Name: "沪台", Status: 1},
				&model.LeiKwanGrant{UserID: 861, MainEntryNodeID: 813, BackupLine: 1, Status: 1},
				// wings changed 主入口 816 → 814 an hour ago: 816 is still in its grace.
				&model.LeiKwanGrant{UserID: 862, MainEntryNodeID: 814, PrevEntryNodeID: 816, EntryChangedAt: now - 3600_000, BackupLine: 1, Status: 1},
				&model.LeiKwanRule{UserID: 861, LineID: 2, ForwardID: 9001, Name: "a", ListenPort: 10018, Status: 1},
				&model.LeiKwanRule{UserID: 861, LineID: 1, ForwardID: 9002, Name: "b", ListenPort: 10019, Status: 1},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatalf("seed %T: %v", row, err)
				}
			}
			// Probe: on, group mode, one group with a 救命版 member (old 9).
			for k, v := range map[string]string{
				"login_monitor_link": "true",
				"probe_title":        "我的探针",
				"probe_nodes":        `{"hidden":[812],"names":{"811":"东京"}}`,
				"probe_group_mode":   "group",
				"probe_groups":       `[{"id":1,"name":"线路机","nodes":[811],"old":[9]},{"id":2,"name":"IPLC","nodes":[812]}]`,
			} {
				if err := r.UpsertConfig(k, v, now); err != nil {
					t.Fatal(err)
				}
			}

			h := handler.New(r, probeSecret, "test")
			router := httpserver.NewRouter(h, probeSecret, r)
			admin, _ := auth.GenerateToken(1, "admin", 0, probeSecret)
			user, _ := auth.GenerateToken(861, "khalilgao", 1, probeSecret)
			call := func(path, token string, body interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/"+path, bytes.NewReader(raw))
				req.Host = "panel.example.com"
				if token != "" {
					req.Header.Set("Authorization", token)
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatalf("%s: %v %s", path, err, w.Body.String())
				}
				return out
			}
			type view struct {
				Groups []struct {
					ID    int64   `json:"id"`
					Name  string  `json:"name"`
					Nodes []int64 `json:"nodes"`
				} `json:"groups"`
				LeiKwan *struct {
					ID    int64               `json:"id"`
					Name  string              `json:"name"`
					Nodes []int64             `json:"nodes"`
					Pos   int                 `json:"pos"`
					Uses  map[string][]string `json:"uses"`
					Notes map[string][]string `json:"notes"`
				} `json:"leikwan"`
			}
			decode := func(out response.R) view {
				t.Helper()
				if out.Code != 0 {
					t.Fatalf("call failed %+v", out)
				}
				raw, _ := json.Marshal(out.Data)
				var v view
				if err := json.Unmarshal(raw, &v); err != nil {
					t.Fatal(err)
				}
				return v
			}
			snapshotGroups := func() string {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, "/api/v1/probe/snapshot", nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("snapshot %d %s", w.Code, w.Body.String())
				}
				var env struct {
					Data map[string]json.RawMessage `json:"data"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &env)
				return string(env.Data["groups"])
			}
			type g = map[string]interface{}
			lk := func(name string, pos int, nodes ...int64) g {
				return g{"name": name, "pos": pos, "nodes": nodes}
			}

			// Admin only.
			if out := call("node/groups", user, nil); out.Code == 0 {
				t.Fatalf("user read node groups: %+v", out)
			}
			if out := call("node/groups/save", user, g{"groups": []g{}}); out.Code == 0 {
				t.Fatalf("user saved node groups: %+v", out)
			}

			// GET: probe groups (FLVX ids only) + the 利群 group, last by default.
			v := decode(call("node/groups", admin, nil))
			if len(v.Groups) != 2 || v.Groups[0].Name != "线路机" || len(v.Groups[0].Nodes) != 1 || v.Groups[0].Nodes[0] != 811 {
				t.Fatalf("groups %+v", v.Groups)
			}
			if v.LeiKwan == nil || v.LeiKwan.ID != 5 || v.LeiKwan.Name != "利群入口" || len(v.LeiKwan.Nodes) != 3 || v.LeiKwan.Pos != 2 {
				t.Fatalf("leikwan %+v", v.LeiKwan)
			}
			if u := strings.Join(v.LeiKwan.Uses["813"], "|"); u != "用户 khalilgao（专线 沪日、沪台）的主入口" {
				t.Fatalf("uses 813 %q", u)
			}
			if u := strings.Join(v.LeiKwan.Uses["816"], "|"); !strings.Contains(u, "用户 wings换主入口后的旧入口（宽限到 ") {
				t.Fatalf("uses 816 %q", u)
			}
			if len(v.LeiKwan.Uses["814"]) != 1 {
				t.Fatalf("uses 814 %v", v.LeiKwan.Uses["814"])
			}
			before := snapshotGroups()
			if before != `[{"name":"线路机"}]` {
				t.Fatalf("probe shows 利群: %s", before)
			}

			// Removing a machine 利群汇聚 uses is refused, nothing written.
			out := call("node/groups/save", admin, g{"groups": []g{{"id": 1, "name": "线路机", "nodes": []int64{811}}}, "leikwan": lk("利群入口", 1, 814, 816)})
			if out.Code == 0 || !strings.Contains(out.Msg, "百度云广州 正在被利群汇聚用作 用户 khalilgao（专线 沪日、沪台）的主入口") || !strings.Contains(out.Msg, "请先到利群汇聚换主入口") {
				t.Fatalf("remove main entry: %+v", out)
			}
			out = call("node/groups/save", admin, g{"groups": []g{}, "leikwan": lk("利群入口", 0, 813, 814)})
			if out.Code == 0 || !strings.Contains(out.Msg, "火山广州 正在被利群汇聚用作 用户 wings换主入口后的旧入口") || !strings.Contains(out.Msg, "宽限") {
				t.Fatalf("remove grace entry: %+v", out)
			}
			if v2 := decode(call("node/groups", admin, nil)); len(v2.Groups) != 2 || len(v2.LeiKwan.Nodes) != 3 {
				t.Fatalf("refused save wrote: %+v", v2)
			}

			// A probe-group name can't clash with the 利群 group.
			if out := call("node/groups/save", admin, g{"groups": []g{{"id": 0, "name": "利群入口", "nodes": []int64{}}}, "leikwan": lk("利群入口", 0, 813, 814, 816)}); out.Code == 0 {
				t.Fatalf("duplicate name saved: %+v", out)
			}
			// Nor with a hidden old node_group (unique index).
			if out := call("node/groups/save", admin, g{"groups": []g{}, "leikwan": lk("IXP", 0, 813, 814, 816)}); out.Code == 0 || !strings.Contains(out.Msg, "旧节点分组同名") {
				t.Fatalf("old group name clash: %+v", out)
			}

			// Rename + add 815 (was in old group ixp: overwritten) + reorder +
			// a new probe group; 救命版 member of 线路机 kept; IPLC dropped.
			v = decode(call("node/groups/save", admin, g{
				"groups": []g{
					{"id": -1, "name": "家宽", "nodes": []int64{812, 811}},
					{"id": 1, "name": "线路机", "nodes": []int64{811, 812}},
				},
				"leikwan": lk("利群入口机", 1, 813, 814, 816, 815),
			}))
			if len(v.Groups) != 2 || v.Groups[0].Name != "家宽" || v.Groups[0].ID <= 1 || v.Groups[1].ID != 1 || len(v.Groups[1].Nodes) != 2 {
				t.Fatalf("saved groups %+v", v.Groups)
			}
			if v.LeiKwan == nil || v.LeiKwan.Name != "利群入口机" || len(v.LeiKwan.Nodes) != 4 || v.LeiKwan.Pos != 1 {
				t.Fatalf("saved leikwan %+v", v.LeiKwan)
			}
			if gid, _ := r.GetNodeGroupIDForNode(815); gid != 5 {
				t.Fatalf("815 group %d", gid)
			}
			if ng, _ := r.GetNodeGroupByID(5); ng == nil || ng.Name != "利群入口机" || ng.Color != "#0d9488" {
				t.Fatalf("node_group 5 %+v", ng)
			}
			cfg, _ := r.GetConfigsByNames([]string{"probe_groups", "probe_title", "probe_nodes", "probe_group_mode", "login_monitor_link"})
			if !strings.Contains(cfg["probe_groups"], `"name":"线路机","nodes":[811,812],"old":[9]`) || strings.Contains(cfg["probe_groups"], "利群") || strings.Contains(cfg["probe_groups"], "IPLC") {
				t.Fatalf("stored probe_groups %s", cfg["probe_groups"])
			}
			if cfg["probe_title"] != "我的探针" || cfg["probe_nodes"] != `{"hidden":[812],"names":{"811":"东京"}}` || cfg["probe_group_mode"] != "group" || cfg["login_monitor_link"] != "true" {
				t.Fatalf("other probe settings changed %+v", cfg)
			}
			// The probe card sees the same groups (救命版 member as -9) and no 利群.
			pv := call("config/probe", admin, nil)
			raw, _ := json.Marshal(pv.Data)
			if pv.Code != 0 || strings.Contains(string(raw), "利群") || !strings.Contains(string(raw), `"name":"家宽"`) {
				t.Fatalf("probe admin view %s", raw)
			}
			after := snapshotGroups()
			if after != `[{"name":"家宽"},{"name":"线路机"}]` {
				t.Fatalf("probe after save: %s", after)
			}

			// Removing an unused member works (815 back out → group_id NULL).
			v = decode(call("node/groups/save", admin, g{"groups": []g{{"id": 1, "name": "线路机", "nodes": []int64{811}}}, "leikwan": lk("利群入口机", 9, 813, 814, 816)}))
			if len(v.LeiKwan.Nodes) != 3 || v.LeiKwan.Pos != 1 {
				t.Fatalf("after remove %+v", v.LeiKwan)
			}
			if gid, _ := r.GetNodeGroupIDForNode(815); gid != 0 {
				t.Fatalf("815 still in group %d", gid)
			}
			// Omitting leikwan leaves it as it is (can't be dropped by a save).
			v = decode(call("node/groups/save", admin, g{"groups": []g{}}))
			if v.LeiKwan == nil || len(v.LeiKwan.Nodes) != 3 || v.LeiKwan.Pos != 0 {
				t.Fatalf("leikwan after omit %+v", v.LeiKwan)
			}

			// node-group/delete refuses the entry group; others still go.
			out = call("node-group/delete", admin, g{"id": 5})
			if out.Code == 0 || out.Msg != "利群汇聚正在用这个分组当入口机，不能删除；可以改名、加减机器" {
				t.Fatalf("entry group delete: %+v", out)
			}
			if ng, _ := r.GetNodeGroupByID(5); ng == nil {
				t.Fatal("entry group deleted")
			}
			if out := call("node-group/delete", admin, g{"id": 2}); out.Code != 0 {
				t.Fatalf("other group delete: %+v", out)
			}

			// No entry group configured → no 利群 group in the view.
			if err := db.Model(&model.LeiKwanSetting{}).Where("id = 1").Update("entry_group_id", 0).Error; err != nil {
				t.Fatal(err)
			}
			if v := decode(call("node/groups", admin, nil)); v.LeiKwan != nil {
				t.Fatalf("leikwan without entry group %+v", v.LeiKwan)
			}
		})
	}
}
