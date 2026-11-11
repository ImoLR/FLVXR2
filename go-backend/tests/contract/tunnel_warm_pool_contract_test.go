package contract_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-backend/internal/auth"
)

// Plan 244 「预热池」 through the router: on by default, stored off from the
// create form, kept on a save without the key, flipped by the light
// endpoint (admin only), listed on /tunnel/list.
func TestTunnelWarmPoolSwitchContract(t *testing.T) {
	secret := "contract-jwt-secret"
	router, repo := setupContractRouter(t, secret)
	now := time.Now().UnixMilli()
	adminToken, err := auth.GenerateToken(1, "admin_user", 0, secret)
	if err != nil {
		t.Fatal(err)
	}
	userToken, err := auth.GenerateToken(2, "plain_user", 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	post := func(token, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", token)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	if err := repo.DB().Exec(`
		INSERT INTO node(name, secret, server_ip, server_ip_v4, server_ip_v6, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "warm-entry", "warm-entry-secret", "10.44.0.1", "10.44.0.1", "", "44000-44010", "", "v1", 1, 1, 1, now, now, 1, "[::]", "[::]", 0).Error; err != nil {
		t.Fatal(err)
	}
	entryID := mustLastInsertID(t, repo, "warm-entry")
	warm := func(name string) int {
		return mustQueryInt(t, repo, `SELECT warm_pool FROM tunnel WHERE name = ?`, name)
	}

	assertCode(t, post(adminToken, "/api/v1/tunnel/create", `{"name":"warm-default","type":1,"flow":1,"status":1,"inNodeId":[{"nodeId":`+jsonInt(entryID)+`,"protocol":"tls"}]}`), 0)
	if got := warm("warm-default"); got != 1 {
		t.Fatalf("new tunnel warm_pool = %d, want 1 (default on)", got)
	}
	assertCode(t, post(adminToken, "/api/v1/tunnel/create", `{"name":"warm-off","type":1,"flow":1,"status":1,"warmPool":0,"inNodeId":[{"nodeId":`+jsonInt(entryID)+`,"protocol":"tls"}]}`), 0)
	if got := warm("warm-off"); got != 0 {
		t.Fatalf("created off, warm_pool = %d", got)
	}
	offID := mustLastInsertID(t, repo, "warm-off")

	// A full save without the key keeps it; with the key it changes.
	assertCode(t, post(adminToken, "/api/v1/tunnel/update", `{"id":`+jsonInt(offID)+`,"name":"warm-off","type":1,"flow":1,"trafficRatio":1,"status":1,"inNodeId":[{"nodeId":`+jsonInt(entryID)+`,"protocol":"tls"}]}`), 0)
	if got := warm("warm-off"); got != 0 {
		t.Fatalf("save without warmPool changed it to %d", got)
	}
	assertCode(t, post(adminToken, "/api/v1/tunnel/update", `{"id":`+jsonInt(offID)+`,"name":"warm-off","type":1,"flow":1,"trafficRatio":1,"status":1,"warmPool":1,"inNodeId":[{"nodeId":`+jsonInt(entryID)+`,"protocol":"tls"}]}`), 0)
	if got := warm("warm-off"); got != 1 {
		t.Fatalf("save with warmPool 1 → %d", got)
	}

	// Light endpoint: admin only.
	res := post(userToken, "/api/v1/tunnel/warm-pool", `{"id":`+jsonInt(offID)+`,"warmPool":0}`)
	if res.Code == http.StatusOK && bytes.Contains(res.Body.Bytes(), []byte(`"code":0`)) {
		t.Fatalf("non-admin flipped the switch: %s", res.Body.String())
	}
	if got := warm("warm-off"); got != 1 {
		t.Fatalf("non-admin changed warm_pool to %d", got)
	}
	assertCode(t, post(adminToken, "/api/v1/tunnel/warm-pool", `{"id":`+jsonInt(offID)+`,"warmPool":0}`), 0)
	if got := warm("warm-off"); got != 0 {
		t.Fatalf("light endpoint stored %d", got)
	}
	list := post(adminToken, "/api/v1/tunnel/list", `{}`)
	if !bytes.Contains(list.Body.Bytes(), []byte(`"warmPool":0`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"warmPool":1`)) {
		t.Fatalf("tunnel list lacks the switch: %s", list.Body.String())
	}
}
