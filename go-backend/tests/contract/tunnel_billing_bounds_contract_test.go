package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/response"
)

// Plan 172: the tunnel billing multiplier (flow 1/2 × trafficRatio 0.01..100)
// is bounded server-side on create and update.

const (
	billingFlowMsg  = "流量计算方式无效，只能是单向(1)或双向(2)"
	billingRatioMsg = "流量倍率必须在 0.01 到 100 之间"
)

type billingCase struct {
	name    string
	fields  string // JSON fragment, e.g. `"flow":2,"trafficRatio":100`
	wantMsg string // "" = accepted
}

var billingRejectCases = []billingCase{
	{"flow zero", `"flow":0`, billingFlowMsg},
	{"flow negative", `"flow":-1`, billingFlowMsg},
	{"flow three", `"flow":3`, billingFlowMsg},
	{"flow 99999", `"flow":99999`, billingFlowMsg},
	{"flow fraction", `"flow":1.5`, billingFlowMsg},
	{"flow text", `"flow":"abc"`, billingFlowMsg},
	{"ratio zero", `"flow":1,"trafficRatio":0`, billingRatioMsg},
	{"ratio negative", `"flow":1,"trafficRatio":-1`, billingRatioMsg},
	{"ratio below min", `"flow":1,"trafficRatio":0.009`, billingRatioMsg},
	{"ratio above max", `"flow":1,"trafficRatio":100.01`, billingRatioMsg},
	{"ratio 99999", `"flow":1,"trafficRatio":99999`, billingRatioMsg},
	{"ratio NaN text", `"flow":1,"trafficRatio":"NaN"`, billingRatioMsg},
	{"ratio Inf text", `"flow":1,"trafficRatio":"Inf"`, billingRatioMsg},
	{"ratio text", `"flow":1,"trafficRatio":"abc"`, billingRatioMsg},
}

func billingPost(t *testing.T, router http.Handler, token, path, body string) response.R {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	var out response.R
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func TestTunnelBillingBoundsCreateContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, repo := setupContractRouter(t, secret)
	now := time.Now().UnixMilli()
	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	insertNode := func(name, ip, portRange string) int64 {
		if err := repo.DB().Exec(`
			INSERT INTO node(name, secret, server_ip, server_ip_v4, server_ip_v6, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, name, name+"-secret", ip, ip, "", portRange, "", "v1", 1, 1, 1, now, now, 1, "[::]", "[::]", 0).Error; err != nil {
			t.Fatalf("insert node %s: %v", name, err)
		}
		return mustLastInsertID(t, repo, name)
	}
	entryID := insertNode("bill-entry", "10.72.0.1", "30000-30100")
	exitID := insertNode("bill-exit", "10.72.0.2", "32000-32100")

	body := func(name, fields string) string {
		return `{"name":"` + name + `","type":2,` + fields + `,"status":1,"inNodeId":[{"nodeId":` + jsonInt(entryID) + `,"protocol":"tls"}],"chainNodes":[],"outNodeId":[{"nodeId":` + jsonInt(exitID) + `,"protocol":"tls"}]}`
	}

	for i, tc := range billingRejectCases {
		name := "bill-reject-" + strconv.Itoa(i)
		out := billingPost(t, router, adminToken, "/api/v1/tunnel/create", body(name, tc.fields))
		if out.Code == 0 || out.Msg != tc.wantMsg {
			t.Fatalf("%s: expected rejection %q, got code=%d msg=%q", tc.name, tc.wantMsg, out.Code, out.Msg)
		}
		if n := mustQueryInt(t, repo, `SELECT COUNT(1) FROM tunnel WHERE name = ?`, name); n != 0 {
			t.Fatalf("%s: rejected create must not insert a tunnel, found %d", tc.name, n)
		}
	}

	accept := []struct {
		name      string
		fields    string
		wantFlow  int64
		wantRatio float64
	}{
		{"min edge", `"flow":1,"trafficRatio":0.01`, 1, 0.01},
		{"max edge", `"flow":2,"trafficRatio":100`, 2, 100},
		{"string numbers", `"flow":"2","trafficRatio":"1.5"`, 2, 1.5},
		{"defaults when absent", `"remark":""`, 1, 1},
		{"null keeps defaults", `"flow":null,"trafficRatio":null`, 1, 1},
	}
	for i, tc := range accept {
		name := "bill-accept-" + strconv.Itoa(i)
		out := billingPost(t, router, adminToken, "/api/v1/tunnel/create", body(name, tc.fields))
		if out.Code != 0 {
			t.Fatalf("%s: expected create success, got code=%d msg=%q", tc.name, out.Code, out.Msg)
		}
		var flow int64
		var ratio float64
		if err := repo.DB().Raw(`SELECT flow, traffic_ratio FROM tunnel WHERE name = ?`, name).Row().Scan(&flow, &ratio); err != nil {
			t.Fatalf("%s: query tunnel: %v", tc.name, err)
		}
		if flow != tc.wantFlow || ratio != tc.wantRatio {
			t.Fatalf("%s: stored flow=%d ratio=%v, want %d/%v", tc.name, flow, ratio, tc.wantFlow, tc.wantRatio)
		}
	}
}

func TestTunnelBillingBoundsUpdateContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, repo := setupContractRouter(t, secret)
	now := time.Now().UnixMilli()
	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	insertNode := func(name, ip, portRange string) int64 {
		if err := repo.DB().Exec(`
			INSERT INTO node(name, secret, server_ip, server_ip_v4, server_ip_v6, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, name, name+"-secret", ip, ip, "", portRange, "", "v1", 1, 1, 1, now, now, 1, "[::]", "[::]", 0).Error; err != nil {
			t.Fatalf("insert node %s: %v", name, err)
		}
		return mustLastInsertID(t, repo, name)
	}
	entryID := insertNode("bill-up-entry", "10.73.0.1", "40000-40100")
	exitID := insertNode("bill-up-exit", "10.73.0.2", "42000-42100")

	if err := repo.DB().Exec(`
		INSERT INTO tunnel(name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "bill-up-tunnel", 1.0, 2, "tls", 1, now, now, 1, nil, 0).Error; err != nil {
		t.Fatalf("insert tunnel: %v", err)
	}
	tunnelID := mustLastInsertID(t, repo, "bill-up-tunnel")

	body := func(fields string) string {
		return `{"id":` + jsonInt(tunnelID) + `,"name":"bill-up-tunnel","type":2,` + fields + `,"status":1,"inNodeId":[{"nodeId":` + jsonInt(entryID) + `,"protocol":"tls"}],"chainNodes":[],"outNodeId":[{"nodeId":` + jsonInt(exitID) + `,"protocol":"tls"}]}`
	}
	stored := func() (int64, float64) {
		var flow int64
		var ratio float64
		if err := repo.DB().Raw(`SELECT flow, traffic_ratio FROM tunnel WHERE id = ?`, tunnelID).Row().Scan(&flow, &ratio); err != nil {
			t.Fatalf("query tunnel: %v", err)
		}
		return flow, ratio
	}

	for _, tc := range billingRejectCases {
		out := billingPost(t, router, adminToken, "/api/v1/tunnel/update", body(tc.fields))
		if out.Code == 0 || out.Msg != tc.wantMsg {
			t.Fatalf("%s: expected rejection %q, got code=%d msg=%q", tc.name, tc.wantMsg, out.Code, out.Msg)
		}
		if flow, ratio := stored(); flow != 1 || ratio != 1 {
			t.Fatalf("%s: rejected update changed billing to flow=%d ratio=%v", tc.name, flow, ratio)
		}
	}

	for _, tc := range []struct {
		fields    string
		wantFlow  int64
		wantRatio float64
	}{
		{`"flow":2,"trafficRatio":100`, 2, 100},
		{`"flow":1,"trafficRatio":0.01`, 1, 0.01},
	} {
		out := billingPost(t, router, adminToken, "/api/v1/tunnel/update", body(tc.fields))
		if out.Code != 0 {
			t.Fatalf("%s: expected update success, got code=%d msg=%q", tc.fields, out.Code, out.Msg)
		}
		if flow, ratio := stored(); flow != tc.wantFlow || ratio != tc.wantRatio {
			t.Fatalf("%s: stored flow=%d ratio=%v", tc.fields, flow, ratio)
		}
	}
}

func TestPathBillingBoundsContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, _ := setupContractRouter(t, secret)
	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	cases := []billingCase{
		{"flow negative", `"flow":-1`, billingFlowMsg},
		{"flow three", `"flow":3`, billingFlowMsg},
		{"flow 99999", `"flow":99999`, billingFlowMsg},
		{"ratio negative", `"flow":1,"trafficRatio":-1`, billingRatioMsg},
		{"ratio below min", `"flow":1,"trafficRatio":0.001`, billingRatioMsg},
		{"ratio above max", `"flow":2,"trafficRatio":100.5`, billingRatioMsg},
	}
	for _, path := range []string{"/api/v1/path/create", "/api/v1/path/update"} {
		for _, tc := range cases {
			b := `{"id":1,"name":"bill-path","transport":"wireguard","nodeIds":[901,902],` + tc.fields + `}`
			out := billingPost(t, router, adminToken, path, b)
			if out.Code == 0 || out.Msg != tc.wantMsg {
				t.Fatalf("%s %s: expected rejection %q, got code=%d msg=%q", path, tc.name, tc.wantMsg, out.Code, out.Msg)
			}
		}
	}
}
