package contract_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

// TestNodeGroupAdminGuardContract: plan 325 — the old node-group API
// (/api/v1/node-group/{list,create,update,delete,assign}) is admin only. A
// normal user gets the usual 403 「权限不足，仅管理员可操作」 on every endpoint
// and nothing changes; the admin still works, and plan 324's 利群入口 delete
// refusal still holds. SQLite and PostgreSQL.
func TestNodeGroupAdminGuardContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			db := r.DB()
			for _, row := range []interface{}{
				&model.User{ID: 871, User: "normal", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, ExpTime: now + 86400000, FlowResetTime: 1, Num: 10},
				&model.NodeGroup{ID: 5, Name: "利群入口", Color: "#0d9488", CreatedTime: 1},
				&model.NodeGroup{ID: 2, Name: "ixp", Color: "#3b82f6", CreatedTime: 1},
				&model.Node{ID: 821, Name: "百度云广州", Region: "CN", Secret: "nga-821", ServerIP: "203.0.113.21", Port: "1-2", Inx: 1, Status: 1,
					GroupID: sql.NullInt64{Int64: 5, Valid: true}},
				&model.Node{ID: 822, Name: "东京 IIJ", Region: "JP", Secret: "nga-822", ServerIP: "203.0.113.22", Port: "1-2", Inx: 2, Status: 1},
				&model.LeiKwanSetting{ID: 1, AccountID: 1, EntryGroupID: 5, GroupName: "利群汇聚"},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatalf("seed %T: %v", row, err)
				}
			}

			h := handler.New(r, probeSecret, "test")
			router := httpserver.NewRouter(h, probeSecret, r)
			admin, _ := auth.GenerateToken(1, "admin", 0, probeSecret)
			user, _ := auth.GenerateToken(871, "normal", 1, probeSecret)
			type g = map[string]interface{}
			call := func(path, token string, body interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/"+path, bytes.NewReader(raw))
				req.Host = "panel.example.com"
				req.Header.Set("Authorization", token)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatalf("%s: %v %s", path, err, w.Body.String())
				}
				return out
			}
			snapshot := func() string {
				t.Helper()
				type row struct {
					ID    int64
					Name  string
					Color string
				}
				var gs []row
				if err := db.Table("node_group").Select("id, name, color").Order("id").Scan(&gs).Error; err != nil {
					t.Fatal(err)
				}
				var ns []struct {
					ID      int64
					GroupID sql.NullInt64
				}
				if err := db.Table("node").Select("id, group_id").Order("id").Scan(&ns).Error; err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal([]interface{}{gs, ns})
				return string(raw)
			}

			before := snapshot()
			grp5 := int64(5)
			for _, c := range []struct {
				path string
				body interface{}
			}{
				{"node-group/list", g{}},
				{"node-group/create", g{"name": "用户建的", "color": "#ff0000"}},
				{"node-group/update", g{"id": 5, "name": "改名", "color": "#ff0000"}},
				{"node-group/delete", g{"id": 2}},
				{"node-group/assign", g{"nodeId": 822, "groupId": grp5}},
				{"node-group/assign", g{"nodeId": 821, "groupId": nil}},
			} {
				out := call(c.path, user, c.body)
				if out.Code != 403 || out.Msg != "权限不足，仅管理员可操作" || out.Data != nil {
					t.Fatalf("normal user %s: %+v", c.path, out)
				}
			}
			if after := snapshot(); after != before {
				t.Fatalf("normal user changed groups:\nbefore %s\nafter  %s", before, after)
			}

			// Admin: list / create / update / assign / delete still work.
			out := call("node-group/list", admin, g{})
			raw, _ := json.Marshal(out.Data)
			var list []struct {
				ID        int64  `json:"id"`
				Name      string `json:"name"`
				NodeCount int64  `json:"nodeCount"`
			}
			_ = json.Unmarshal(raw, &list)
			if out.Code != 0 || len(list) != 2 {
				t.Fatalf("admin list: %+v", out)
			}
			counts := map[int64]int64{}
			for _, x := range list {
				counts[x.ID] = x.NodeCount
			}
			if counts[5] != 1 || counts[2] != 0 {
				t.Fatalf("admin list counts %v", counts)
			}
			if out := call("node-group/create", admin, g{"name": "新分组"}); out.Code != 0 {
				t.Fatalf("admin create: %+v", out)
			}
			if out := call("node-group/update", admin, g{"id": 2, "name": "IXP", "color": "#3b82f6"}); out.Code != 0 {
				t.Fatalf("admin update: %+v", out)
			}
			if ng, _ := r.GetNodeGroupByID(2); ng == nil || ng.Name != "IXP" {
				t.Fatalf("admin update not saved: %+v", ng)
			}
			grp2 := int64(2)
			if out := call("node-group/assign", admin, g{"nodeId": 822, "groupId": grp2}); out.Code != 0 {
				t.Fatalf("admin assign: %+v", out)
			}
			if gid, _ := r.GetNodeGroupIDForNode(822); gid != 2 {
				t.Fatalf("admin assign not saved: %d", gid)
			}
			// Plan 324: the 利群入口 group is still never deleted, even by the admin.
			if out := call("node-group/delete", admin, g{"id": 5}); out.Code == 0 || out.Msg != "利群汇聚正在用这个分组当入口机，不能删除；可以改名、加减机器" {
				t.Fatalf("admin entry group delete: %+v", out)
			}
			if ng, _ := r.GetNodeGroupByID(5); ng == nil {
				t.Fatal("entry group deleted")
			}
			if out := call("node-group/delete", admin, g{"id": 2}); out.Code != 0 {
				t.Fatalf("admin delete: %+v", out)
			}
		})
	}
}
