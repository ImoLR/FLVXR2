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

// Plan 255 「线路监测」 through the real router: admin only (anonymous and
// normal users refused by the JWT middleware), chart CRUD with caps and the
// port 22 hint, the data endpoint buckets with 断流 kept.
func TestLineMonitorContract(t *testing.T) {
	const secret = "line-monitor-contract"
	router, r := setupContractRouter(t, secret)
	admin := mustAdminToken(t, secret)
	ensureContractUser(t, r, 2, 1)
	user, err := auth.GenerateToken(2, "normal_user", 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []map[string]interface{}{
		{"name": "AWS HK", "serverIpV4": "198.51.100.41"},
		{"name": "Fachost seednet 黑五", "serverIpV4": "198.51.100.42"},
		{"name": "Tcscloud Seednet", "serverIpV4": "198.51.100.43"},
	} {
		if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", n); out.Code != 0 {
			t.Fatalf("create node = %+v", out)
		}
	}

	paths := []string{"list", "options", "ports", "chart/save", "chart/delete", "chart/reorder", "line/pause", "data"}
	for _, p := range paths {
		path := "/api/v1/node/line-monitor/" + p
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"chartId":1,"id":1,"nodeId":1}`))
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		var anon struct{ Code int }
		_ = json.Unmarshal(res.Body.Bytes(), &anon)
		if anon.Code == 0 || res.Code == http.StatusOK && anon.Code == 0 {
			t.Fatalf("%s anonymous accepted: %d %s", path, res.Code, res.Body.String())
		}
		if out := requestContractEnvelope(t, router, user, path, map[string]interface{}{"chartId": 1, "id": 1, "nodeId": 1}); out.Code != 403 {
			t.Fatalf("%s as user: %+v", path, out)
		}
	}

	save := func(body map[string]interface{}) (int64, []interface{}) {
		t.Helper()
		out := requestContractEnvelope(t, router, admin, "/api/v1/node/line-monitor/chart/save", body)
		if out.Code != 0 {
			t.Fatalf("save %+v", out)
		}
		m := out.Data.(map[string]interface{})
		w, _ := m["warnings"].([]interface{})
		return int64(m["id"].(float64)), w
	}
	id, warn := save(map[string]interface{}{"name": "AWS HK → Seednet", "lines": []interface{}{
		map[string]interface{}{"sourceNodeId": 1, "targetType": "node", "targetNodeId": 2, "port": 30001, "label": "黑五"},
		map[string]interface{}{"sourceNodeId": 1, "targetType": "node", "targetNodeId": 3, "port": 30002},
	}})
	if len(warn) != 0 {
		t.Fatalf("warnings %v", warn)
	}
	hinet, warn := save(map[string]interface{}{"name": "Seednet → HiNet", "lines": []interface{}{
		map[string]interface{}{"sourceNodeId": 2, "targetType": "custom", "host": "hinet.example.test", "port": 22},
	}})
	if len(warn) != 1 || !strings.Contains(warn[0].(string), "SSH") {
		t.Fatalf("port 22 must save with a hint: %v", warn)
	}
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/line-monitor/chart/reorder", map[string]interface{}{"ids": []int64{hinet, id}}); out.Code != 0 {
		t.Fatalf("reorder %+v", out)
	}
	list := requestContractEnvelope(t, router, admin, "/api/v1/node/line-monitor/list", map[string]interface{}{})
	charts := list.Data.(map[string]interface{})["charts"].([]interface{})
	if len(charts) != 2 || charts[0].(map[string]interface{})["name"] != "Seednet → HiNet" {
		t.Fatalf("list order %+v", charts)
	}
	lines := charts[1].(map[string]interface{})["lines"].([]interface{})
	l0 := lines[0].(map[string]interface{})
	if l0["sourceName"] != "AWS HK" || l0["targetName"] != "Fachost seednet 黑五" || l0["effectiveHost"] != "198.51.100.42" || l0["label"] != "黑五" {
		t.Fatalf("line view %+v", l0)
	}
	opts := requestContractEnvelope(t, router, admin, "/api/v1/node/line-monitor/options", map[string]interface{}{})
	if nodes := opts.Data.(map[string]interface{})["nodes"].([]interface{}); len(nodes) != 3 {
		t.Fatalf("options %+v", opts.Data)
	}

	// Samples: one 断流 of 2 slots 20 min ago inside an otherwise clean hour.
	lineID := int64(l0["id"].(float64))
	now := time.Now().Unix() / 30 * 30
	var rows []model.LineMonitorSample
	for ts := now - 3600; ts < now-60; ts += 30 {
		s := model.LineMonitorSample{LineID: lineID, TS: ts, Status: 1, Ms10: 321}
		if ts == now-1200 || ts == now-1170 {
			s.Status, s.Ms10 = 3, 0
		}
		rows = append(rows, s)
	}
	if err := r.InsertLineMonitorSamples(rows); err != nil {
		t.Fatal(err)
	}
	for _, rng := range []string{"1h", "6h", "12h", "1d", "30d"} {
		out := requestContractEnvelope(t, router, admin, "/api/v1/node/line-monitor/data", map[string]interface{}{"chartId": id, "range": rng})
		if out.Code != 0 {
			t.Fatalf("data %s %+v", rng, out)
		}
		d := out.Data.(map[string]interface{})
		series := d["lines"].([]interface{})[0].(map[string]interface{})
		var down float64
		for _, v := range series["down"].([]interface{}) {
			down += v.(float64)
		}
		if down != 2 || len(series["avg"].([]interface{})) > 1500 {
			t.Fatalf("%s: down slots %v, points %d", rng, down, len(series["avg"].([]interface{})))
		}
		st := d["stats"].([]interface{})[0].(map[string]interface{})
		if st["downEvents"] != float64(1) || st["longestDown"] != float64(60) || st["avg"] != 32.1 {
			t.Fatalf("%s stats %+v", rng, st)
		}
		ev := d["events"].([]interface{})
		if len(ev) != 1 || ev[0].(map[string]interface{})["start"] != float64(now-1200) {
			t.Fatalf("%s events %+v", rng, ev)
		}
	}

	// Deleting the chart removes its lines and (in the background) samples.
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/line-monitor/chart/delete", map[string]interface{}{"id": id}); out.Code != 0 {
		t.Fatalf("delete %+v", out)
	}
	var left int64
	for i := 0; i < 100; i++ {
		r.DB().Model(&model.LineMonitorSample{}).Where("line_id = ?", lineID).Count(&left)
		if left == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if left != 0 {
		t.Fatalf("samples left after delete: %d", left)
	}
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/line-monitor/data", map[string]interface{}{"chartId": id, "range": "1h"}); out.Code == 0 {
		t.Fatal("data of a deleted chart")
	}
}
