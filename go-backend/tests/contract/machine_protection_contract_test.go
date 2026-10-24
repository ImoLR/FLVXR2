package contract_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go-backend/internal/auth"
	"go-backend/internal/http/response"
	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 118: node machine-protection settings, the push to capable agents (on
// connect, on change, again on reconnect) and admin/user visibility of the
// whole-machine sample.

type agentCmd struct {
	Type      string                 `json:"type"`
	RequestID string                 `json:"requestId"`
	Data      map[string]interface{} `json:"data"`
}

func dialAgent(t *testing.T, base, secret string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(base+"/system-info?type=1&secret="+secret+"&version=test&machineProtect=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// nextMachineCmd answers every command and returns the next SetMachineProtection.
func nextMachineCmd(t *testing.T, c *websocket.Conn, secret string) agentCmd {
	t.Helper()
	aes, err := security.NewAESCrypto(secret)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_ = c.SetReadDeadline(deadline)
		_, payload, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("no SetMachineProtection: %v", err)
		}
		raw := payload
		var wrap struct {
			Encrypted bool   `json:"encrypted"`
			Data      string `json:"data"`
		}
		if json.Unmarshal(payload, &wrap) == nil && wrap.Encrypted {
			if raw, err = aes.Decrypt(wrap.Data); err != nil {
				t.Fatal(err)
			}
		}
		var cmd agentCmd
		if err := json.Unmarshal(raw, &cmd); err != nil || cmd.Type == "" {
			continue
		}
		_ = c.WriteJSON(map[string]interface{}{"type": cmd.Type + "Response", "requestId": cmd.RequestID, "success": true, "message": "OK"})
		if cmd.Type == "SetMachineProtection" {
			return cmd
		}
	}
}

func getContractEnvelope(t *testing.T, router http.Handler, token, path string) response.R {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", token)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	var out response.R
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

func nodeRow(t *testing.T, r *repo.Repository, id int64) model.Node {
	t.Helper()
	var n model.Node
	if err := r.DB().Where("id = ?", id).Take(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMachineProtectionSettingsContract(t *testing.T) {
	const secret = "machine-protect-contract"
	router, r := setupContractRouter(t, secret)
	admin := mustAdminToken(t, secret)

	// Create: defaults on; an explicit choice (incl. false / 0) is stored as given.
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", map[string]interface{}{"name": "defaults", "serverIpV4": "198.51.100.10"}); out.Code != 0 {
		t.Fatalf("create = %+v", out)
	}
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", map[string]interface{}{"name": "custom", "serverIpV4": "198.51.100.11",
		"machineProtectEnabled": false, "machineMaxTotal": 0, "machineTwBuckets": 0, "machineCloseWaitSeconds": 30}); out.Code != 0 {
		t.Fatalf("create custom = %+v", out)
	}
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", map[string]interface{}{"name": "bad", "serverIpV4": "198.51.100.12", "machineMaxTotal": 50}); out.Code == 0 {
		t.Fatal("cap below 100 accepted on create")
	}
	var nodes []model.Node
	if err := r.DB().Order("id").Find(&nodes).Error; err != nil || len(nodes) != 2 {
		t.Fatalf("nodes %d %v", len(nodes), err)
	}
	if n := nodes[0]; !n.MachineProtectEnabled || n.MachineMaxTotal != 1000 || n.MachineTWBuckets != 200 || n.MachineCloseWaitSeconds != 10 {
		t.Fatalf("defaults %+v", n)
	}
	if n := nodes[1]; n.MachineProtectEnabled || n.MachineMaxTotal != 0 || n.MachineTWBuckets != 0 || n.MachineCloseWaitSeconds != 30 {
		t.Fatalf("explicit values %+v", n)
	}
	id := nodes[0].ID

	update := func(extra map[string]interface{}) response.R {
		body := map[string]interface{}{"id": id, "name": "defaults", "serverIpV4": "198.51.100.10", "port": nodes[0].Port}
		for k, v := range extra {
			body[k] = v
		}
		return requestContractEnvelope(t, router, admin, "/api/v1/node/update", body)
	}
	for _, bad := range []map[string]interface{}{{"machineMaxTotal": 99}, {"machineTwBuckets": -1}, {"machineCloseWaitSeconds": 3601}, {"machineMaxTotal": "abc"}, {"machineProtectEnabled": "maybe"}, {"machineMaxTotal": 1500.5}} {
		if out := update(bad); out.Code == 0 {
			t.Fatalf("%v accepted", bad)
		}
		if n := nodeRow(t, r, id); n.MachineMaxTotal != 1000 || n.MachineTWBuckets != 200 || n.MachineCloseWaitSeconds != 10 || !n.MachineProtectEnabled {
			t.Fatalf("rejected update changed the row: %+v", n)
		}
	}
	// A partial update keeps the other values; an update without the keys keeps all.
	if out := update(map[string]interface{}{"machineTwBuckets": 0}); out.Code != 0 {
		t.Fatalf("partial = %+v", out)
	}
	if out := update(nil); out.Code != 0 {
		t.Fatalf("plain update = %+v", out)
	}
	if n := nodeRow(t, r, id); n.MachineTWBuckets != 0 || n.MachineMaxTotal != 1000 || !n.MachineProtectEnabled || n.MachineCloseWaitSeconds != 10 {
		t.Fatalf("partial update %+v", n)
	}

	// Admin node list: settings + no sample yet (unknown); non-admins cannot list nodes.
	out := requestContractEnvelope(t, router, admin, "/api/v1/node/list", map[string]interface{}{})
	var first map[string]interface{}
	for _, it := range out.Data.([]interface{}) {
		if m := it.(map[string]interface{}); m["id"] == float64(id) {
			first = m
		}
	}
	if first["machineProtectEnabled"] != true || first["machineMaxTotal"] != float64(1000) || first["machineTwBuckets"] != float64(0) ||
		first["machineCloseWaitSeconds"] != float64(10) || first["machine"] != nil || first["machineProtectCapable"] != false {
		t.Fatalf("node list item %v", first)
	}
	ensureContractUser(t, r, 2, 1)
	user, _ := auth.GenerateToken(2, "user", 1, secret)
	if out := requestContractEnvelope(t, router, user, "/api/v1/node/list", map[string]interface{}{}); out.Code != 403 {
		t.Fatalf("user node list = %+v", out)
	}
	if out := requestContractEnvelope(t, router, user, "/api/v1/node/update", map[string]interface{}{"id": id, "machineMaxTotal": 0}); out.Code != 403 {
		t.Fatalf("user node update = %+v", out)
	}
}

func TestMachineProtectionPushAndVisibilityContract(t *testing.T) {
	const secret = "machine-protect-push"
	router, r := setupContractRouter(t, secret)
	admin := mustAdminToken(t, secret)
	srv := httptest.NewServer(router)
	defer srv.Close()
	base := "ws" + strings.TrimPrefix(srv.URL, "http")
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", map[string]interface{}{"name": "agent", "serverIpV4": "198.51.100.20"}); out.Code != 0 {
		t.Fatalf("create = %+v", out)
	}
	n := nodeRow(t, r, 1)

	// Connect: the stored setting is pushed at once.
	c := dialAgent(t, base, n.Secret)
	cmd := nextMachineCmd(t, c, n.Secret)
	if cmd.Data["enabled"] != true || cmd.Data["maxTotal"] != float64(1000) || cmd.Data["twBuckets"] != float64(200) || cmd.Data["closeWaitSeconds"] != float64(10) {
		t.Fatalf("push on connect %v", cmd.Data)
	}
	// Change: pushed again.
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/update", map[string]interface{}{"id": n.ID, "name": "agent", "serverIpV4": "198.51.100.20",
		"port": n.Port, "machineProtectEnabled": false}); out.Code != 0 {
		t.Fatalf("update = %+v", out)
	}
	if cmd = nextMachineCmd(t, c, n.Secret); cmd.Data["enabled"] != false || cmd.Data["maxTotal"] != float64(1000) {
		t.Fatalf("push on change %v", cmd.Data)
	}
	// Reconnect: pushed again (the panel is authoritative).
	c.Close()
	c = dialAgent(t, base, n.Secret)
	if cmd = nextMachineCmd(t, c, n.Secret); cmd.Data["enabled"] != false {
		t.Fatalf("push on reconnect %v", cmd.Data)
	}

	// A metric with the sample: admins see everything, monitor users only the chip fields.
	metric := `{"type":"metric","data":{"uptime":5,"cpu_usage":1,"memory_usage":1,"tcp_conns":700,` +
		`"machine_conns":{"total":823,"tcp":700,"udp":123,"established":300,"time_wait":350,"close_wait":20,"other_tcp":30},` +
		`"machine_protect":{"owner":"flvx","enabled":true,"max_total":1000,"tw_buckets":500,"close_wait_seconds":10,"blocking":true,"dropped_total":12,"ports":4,"error":"x"}}}`
	if err := c.WriteMessage(websocket.TextMessage, []byte(metric)); err != nil {
		t.Fatal(err)
	}
	var adminMachine map[string]interface{}
	for i := 0; i < 100 && adminMachine == nil; i++ {
		out := requestContractEnvelope(t, router, admin, "/api/v1/node/list", map[string]interface{}{})
		item := out.Data.([]interface{})[0].(map[string]interface{})
		adminMachine, _ = item["machine"].(map[string]interface{})
		if adminMachine == nil {
			time.Sleep(20 * time.Millisecond)
		} else if item["machineProtectCapable"] != true {
			t.Fatal("capability not reported")
		}
	}
	if adminMachine == nil {
		t.Fatal("sample not in the node list")
	}
	prot := adminMachine["protect"].(map[string]interface{})
	conns := adminMachine["conns"].(map[string]interface{})
	if conns["total"] != float64(823) || conns["timeWait"] != float64(350) || prot["maxTotal"] != float64(1000) || prot["twBuckets"] != float64(500) ||
		prot["blocking"] != true || prot["droppedTotal"] != float64(12) || prot["error"] != "x" {
		t.Fatalf("admin machine %v", adminMachine)
	}
	if out := getContractEnvelope(t, router, admin, "/api/v1/monitor/nodes"); out.Code != 0 ||
		!strings.Contains(mustJSON(t, out.Data), `"twBuckets":500`) {
		t.Fatalf("admin monitor = %s", mustJSON(t, out.Data))
	}
	// A monitor user who sees the node gets the chip fields only.
	r.DB().Exec(`INSERT INTO user(id, user, pwd, role_id, exp_time, flow, in_flow, out_flow, flow_reset_time, num, created_time, updated_time, status)
		VALUES(2, 'mon', 'x', 1, ?, 1, 0, 0, 1, 1, 1, 1, 1)`, time.Now().Add(24*time.Hour).UnixMilli())
	r.DB().Exec("INSERT INTO monitor_permission(user_id, full_access, created_time) VALUES(2, 1, 1)")
	user, _ := auth.GenerateToken(2, "mon", 1, secret)
	out := getContractEnvelope(t, router, user, "/api/v1/monitor/nodes")
	body := mustJSON(t, out.Data)
	if out.Code != 0 || !strings.Contains(body, `"total":823`) || !strings.Contains(body, `"maxTotal":1000`) || !strings.Contains(body, `"blocking":true`) {
		t.Fatalf("user monitor = %d %s", out.Code, body)
	}
	for _, leak := range []string{"twBuckets", "closeWaitSeconds", `"error"`, `"ports"`} {
		if strings.Contains(body, leak) {
			t.Fatalf("user monitor leaks %s: %s", leak, body)
		}
	}
	// A user without monitor permission sees nothing.
	ensureContractUser(t, r, 3, 1)
	stranger, _ := auth.GenerateToken(3, "x", 1, secret)
	if out := getContractEnvelope(t, router, stranger, "/api/v1/monitor/nodes"); out.Code != 403 {
		t.Fatalf("stranger monitor = %+v", out)
	}
	// Deleting the node drops its sample.
	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/delete", map[string]interface{}{"id": n.ID}); out.Code != 0 {
		t.Fatalf("delete = %+v", out)
	}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
