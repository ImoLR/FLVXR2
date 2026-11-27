package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/store/model"
)

// Plan 321 (user 11-28): 线路监测 for normal users, read-only, through the
// real router. Every admin endpoint (list / options / ports / chart save /
// delete / reorder / pause / data / check-port verify / rotate / command /
// test and the new 「用户可见」 switch) answers 403 to a normal user; the user
// endpoints list only the charts left visible and carry no node IP, target
// host, port, DDNS name or probe error text; a hidden chart's data is refused.
func TestLineMonitorUserViewContract(t *testing.T) {
	const secret = "line-monitor-user-321"
	router, r := setupContractRouter(t, secret)
	admin := mustAdminToken(t, secret)
	ensureContractUser(t, r, 2, 1)
	user, err := auth.GenerateToken(2, "normal_user", 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	call := func(token, path string, body interface{}) (int, string) {
		t.Helper()
		out := requestContractEnvelope(t, router, token, path, body)
		raw, _ := json.Marshal(out)
		return out.Code, string(raw)
	}
	const awsIP, seedIP, ddnsName, customHost = "198.51.100.41", "198.51.100.42", "fachost-seednet.imgamer.top", "hinet2.example.test"
	for _, n := range []map[string]interface{}{
		{"name": "AWS HK", "serverIpV4": awsIP, "vendors": []string{"aws"}},
		{"name": "Fachost seednet 黑五", "serverIpV4": ddnsName, "vendors": []string{"fachost"}},
		{"name": "Tcscloud Seednet", "serverIpV4": "198.51.100.43", "vendors": []string{"tcscloud"}},
	} {
		if c, raw := call(admin, "/api/v1/node/create", n); c != 0 {
			t.Fatalf("create node: %s", raw)
		}
	}
	save := func(body map[string]interface{}) int64 {
		t.Helper()
		out := requestContractEnvelope(t, router, admin, "/api/v1/node/line-monitor/chart/save", body)
		if out.Code != 0 {
			t.Fatalf("save %+v", out)
		}
		return int64(out.Data.(map[string]interface{})["id"].(float64))
	}
	seedChart := save(map[string]interface{}{"name": "AWS HK → Seednet", "lines": []interface{}{
		map[string]interface{}{"sourceNodeId": 1, "targetType": "node", "targetNodeId": 2, "port": 30001, "label": "黑五"},
		map[string]interface{}{"sourceNodeId": 1, "targetType": "node", "targetNodeId": 3, "port": 30002},
	}})
	hinetChart := save(map[string]interface{}{"name": "Seednet → HiNet", "lines": []interface{}{
		map[string]interface{}{"sourceNodeId": 2, "targetType": "custom", "host": customHost, "port": 25931},
	}})
	hidden := save(map[string]interface{}{"name": "内部", "lines": []interface{}{
		map[string]interface{}{"sourceNodeId": 3, "targetType": "node", "targetNodeId": 1, "port": 30003},
	}})

	// Admin endpoints: 403 for the user, also the new switch.
	for _, p := range []string{"list", "options", "ports", "chart/save", "chart/delete", "chart/reorder", "line/pause", "data",
		"check-port/verify", "check-port/rotate", "check-port/command", "check-port/test", "chart/user-visible"} {
		path := "/api/v1/node/line-monitor/" + p
		if c, raw := call(user, path, map[string]interface{}{"chartId": seedChart, "id": seedChart, "nodeId": 1, "visible": true}); c != 403 {
			t.Fatalf("%s as user: %s", path, raw)
		}
	}
	// User endpoints need a login.
	for _, p := range []string{"/api/v1/line-monitor/list", "/api/v1/line-monitor/data"} {
		req := httptest.NewRequest(http.MethodPost, p, bytes.NewBufferString(`{"chartId":1}`))
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		var anon struct{ Code int }
		_ = json.Unmarshal(res.Body.Bytes(), &anon)
		if anon.Code != 401 {
			t.Fatalf("%s anonymous: %s", p, res.Body.String())
		}
	}
	// The admin's 「用户可见」 switch.
	if c, raw := call(admin, "/api/v1/node/line-monitor/chart/user-visible", map[string]interface{}{"id": hidden, "visible": false}); c != 0 {
		t.Fatalf("user-visible: %s", raw)
	}
	if c, raw := call(admin, "/api/v1/node/line-monitor/chart/user-visible", map[string]interface{}{"id": 999, "visible": false}); c == 0 {
		t.Fatalf("switch on a missing chart: %s", raw)
	}
	_, raw := call(admin, "/api/v1/node/line-monitor/list", map[string]interface{}{})
	if !strings.Contains(raw, `"userHidden":true`) || strings.Count(raw, `"userHidden":false`) != 2 {
		t.Fatalf("admin list lacks the switch state: %.1500s", raw)
	}

	// Samples on the 黑五 line (one 断流) and a live reason quoting host:port.
	var seedLine model.LineMonitorLine
	r.DB().Where("chart_id = ? AND target_node_id = 2", seedChart).First(&seedLine)
	now := time.Now().Unix() / 30 * 30
	var rows []model.LineMonitorSample
	for ts := now - 3600; ts < now-60; ts += 30 {
		s := model.LineMonitorSample{LineID: seedLine.ID, TS: ts, Status: 1, Ms10: 321}
		if ts == now-1200 {
			s.Status, s.Ms10 = 3, 0
		}
		rows = append(rows, s)
	}
	if err := r.InsertLineMonitorSamples(rows); err != nil {
		t.Fatal(err)
	}

	c, userList := call(user, "/api/v1/line-monitor/list", map[string]interface{}{})
	if c != 0 {
		t.Fatalf("user list: %s", userList)
	}
	var lv struct {
		Data struct {
			Charts []struct {
				ID    int64                    `json:"id"`
				Name  string                   `json:"name"`
				Lines []map[string]interface{} `json:"lines"`
			} `json:"charts"`
		} `json:"data"`
	}
	_ = json.Unmarshal([]byte(userList), &lv)
	if len(lv.Data.Charts) != 2 || lv.Data.Charts[0].ID != seedChart || lv.Data.Charts[1].ID != hinetChart {
		t.Fatalf("user charts: %s", userList)
	}
	l0 := lv.Data.Charts[0].Lines[0]
	if l0["sourceName"] != "AWS HK" || l0["targetName"] != "Fachost seednet 黑五" || l0["label"] != "黑五" {
		t.Fatalf("user line view: %+v", l0)
	}
	if h := lv.Data.Charts[1].Lines[0]; h["targetName"] != "自定义目标" || h["targetCustom"] != true {
		t.Fatalf("custom target view: %+v", h)
	}
	allowed := map[string]bool{"id": true, "chartId": true, "kind": true, "label": true, "color": true, "paused": true, "sort": true, "sourceName": true,
		"sourceOnline": true, "targetName": true, "targetCustom": true, "leikwanLineName": true, "fixedEntry": true, "live": true, "createdTime": true}
	for _, ch := range lv.Data.Charts {
		for _, l := range ch.Lines {
			for k := range l {
				if !allowed[k] {
					t.Fatalf("user line carries %q: %+v", k, l)
				}
			}
		}
	}
	for _, secret := range []string{awsIP, seedIP, ddnsName, customHost, "内部", "effectiveHost", "checkPort", "engine"} {
		if strings.Contains(userList, secret) {
			t.Fatalf("user list leaks %q: %s", secret, userList)
		}
	}

	c, data := call(user, "/api/v1/line-monitor/data", map[string]interface{}{"chartId": seedChart, "range": "1h"})
	if c != 0 {
		t.Fatalf("user data: %s", data)
	}
	if !strings.Contains(data, `"downEvents":1`) || !strings.Contains(data, `"down":[`) {
		t.Fatalf("user data lacks the 断流: %.1500s", data)
	}
	for _, secret := range []string{awsIP, ddnsName, customHost, "reason", "queryMs"} {
		if strings.Contains(data, secret) {
			t.Fatalf("user data leaks %q: %.1500s", secret, data)
		}
	}
	if c, raw := call(user, "/api/v1/line-monitor/data", map[string]interface{}{"chartId": hidden, "range": "1h"}); c == 0 {
		t.Fatalf("hidden chart data for a user: %s", raw)
	}
	// Admin data unchanged (the user view is a separate endpoint).
	if c, raw := call(admin, "/api/v1/node/line-monitor/data", map[string]interface{}{"chartId": hidden, "range": "1h"}); c != 0 {
		t.Fatalf("admin data of a hidden chart: %s", raw)
	}
	// Visible again → listed again.
	call(admin, "/api/v1/node/line-monitor/chart/user-visible", map[string]interface{}{"id": hidden, "visible": true})
	if _, raw := call(user, "/api/v1/line-monitor/list", map[string]interface{}{}); !strings.Contains(raw, "内部") {
		t.Fatalf("chart visible again not listed: %.800s", raw)
	}
}
