package contract_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
)

// Plan 144: /tunnel/user/tunnel items carry groups [{id,name,color,inx,entryKeys}]
// and keep every existing field.
func TestUserTunnelListPickerGroupsContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, repo := setupContractRouter(t, secret)
	now := time.Now().UnixMilli()
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if err := repo.DB().Exec(q, args...).Error; err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO user(id, user, pwd, role_id, exp_time, flow, in_flow, out_flow, flow_reset_time, num, created_time, updated_time, status)
		VALUES(2, 'grouped_user', 'x', 1, 2727251700000, 99999, 0, 0, 1, 99999, ?, ?, 1)`, now, now)
	for _, id := range []int{71, 72} {
		exec(`INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
			VALUES(?, ?, 1.0, 1, 'tls', 99999, ?, ?, 1, NULL, ?)`, id, "t"+string(rune('0'+id%10)), now, now, id)
	}
	exec(`INSERT INTO node(id, name, secret, server_ip, port, created_time, updated_time, status, region_city) VALUES(81, 'entry-a', 's81', '10.0.0.81', '1000-2000', ?, ?, 1, '深圳')`, now, now)
	exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx) VALUES(71, '1', 81, 0), (72, '1', 81, 0)`)
	exec(`INSERT INTO tunnel_group(id, name, color, inx, created_time, updated_time, status) VALUES(91, '深港', '#10b981', 1, ?, ?, 1), (92, '外部组', '#ef4444', 0, ?, ?, 1)`, now, now, now, now)
	exec(`INSERT INTO tunnel_group_tunnel(tunnel_group_id, tunnel_id, created_time) VALUES(91, 71, ?), (92, 71, ?)`, now, now)
	exec(`INSERT INTO tunnel_group_tunnel_entry(tunnel_group_id, tunnel_id, node_id, created_time) VALUES(91, 71, 81, ?), (92, 71, 81, ?)`, now, now)
	exec(`INSERT INTO user_tunnel(id, user_id, tunnel_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status) VALUES(61, 2, 71, 1, 1, 0, 0, 1, 2727251700000, 1), (62, 2, 72, 1, 1, 0, 0, 1, 2727251700000, 1)`)
	exec(`INSERT INTO group_permission_grant(user_group_id, tunnel_group_id, user_tunnel_id, created_by_group, created_time) VALUES(5, 91, 61, 1, ?)`, now)

	list := func(token string) (string, []map[string]interface{}) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tunnel/user/tunnel", nil)
		req.Header.Set("Authorization", token)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		var out struct {
			Code int                      `json:"code"`
			Data []map[string]interface{} `json:"data"`
		}
		body := res.Body.String()
		if err := json.Unmarshal([]byte(body), &out); err != nil || out.Code != 0 {
			t.Fatalf("response %s (%v)", body, err)
		}
		return body, out.Data
	}
	groupNames := func(item map[string]interface{}) []string {
		names := []string{}
		for _, g := range item["groups"].([]interface{}) {
			obj := g.(map[string]interface{})
			for _, k := range []string{"id", "name", "color", "inx", "entryKeys"} {
				if _, ok := obj[k]; !ok {
					t.Fatalf("group %v lacks %s", obj, k)
				}
			}
			names = append(names, obj["name"].(string))
		}
		return names
	}
	baseKeys := []string{"entryGroups", "exitRegions", "groups", "id", "lineMachine", "chainAvailable", "name", "remark", "trafficRatio", "portRangeMin", "portRangeMax"}
	sort.Strings(baseKeys)

	adminToken, _ := auth.GenerateToken(1, "admin_user", 0, secret)
	_, admin := list(adminToken)
	if len(admin) != 2 || !reflect.DeepEqual(groupNames(admin[0]), []string{"外部组", "深港"}) || len(groupNames(admin[1])) != 0 {
		t.Fatalf("admin groups %v", admin)
	}
	keys := []string{}
	for k := range admin[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, baseKeys) {
		t.Fatalf("admin item keys %v", keys)
	}

	userToken, _ := auth.GenerateToken(2, "grouped_user", 1, secret)
	body, user := list(userToken)
	if len(user) != 2 || !reflect.DeepEqual(groupNames(user[0]), []string{"深港"}) || len(groupNames(user[1])) != 0 {
		t.Fatalf("user groups %s", body)
	}
	if strings.Contains(body, "外部组") || strings.Contains(body, "entry-a") {
		t.Fatalf("user sees a foreign group or node name: %s", body)
	}
	entry := user[0]["entryGroups"].([]interface{})[0].(map[string]interface{})["key"]
	if got := user[0]["groups"].([]interface{})[0].(map[string]interface{})["entryKeys"]; !reflect.DeepEqual(got, []interface{}{entry}) {
		t.Fatalf("entryKeys %v want [%v]", got, entry)
	}
}
