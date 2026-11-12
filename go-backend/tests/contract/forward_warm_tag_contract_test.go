package contract_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/security"

	"github.com/gorilla/websocket"
)

// Plan 249 through the router + agent WebSocket: a warm-capable agent reports
// its pool counters on the metric message; the forward list carries the 预热
// tag per rule — admins every state (预热中 · 数据不足 here), the rule's owner
// only the green one, without node names / targets / counts; a sleeping key
// shows nothing; the admin tunnel list shows the same 数据不足 state.
func TestForwardWarmTagContract(t *testing.T) {
	const secret = "forward-warm-tag"
	router, r := setupContractRouter(t, secret)
	admin := mustAdminToken(t, secret)
	srv := httptest.NewServer(router)
	defer srv.Close()
	base := "ws" + strings.TrimPrefix(srv.URL, "http")

	if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", map[string]interface{}{"name": "warm-entry", "serverIpV4": "198.51.100.30"}); out.Code != 0 {
		t.Fatalf("node create = %+v", out)
	}
	n := nodeRow(t, r, 1)
	c, _, err := websocket.DefaultDialer.Dial(base+"/system-info?type=1&secret="+n.Secret+"&version=test&warmPool=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The agent answers every command (AddService, SetWarmPools, …).
	aes, err := security.NewAESCrypto(n.Secret)
	if err != nil {
		t.Fatal(err)
	}
	var wmu sync.Mutex // one writer at a time (gorilla/websocket)
	send := func(b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return c.WriteMessage(websocket.TextMessage, b)
	}
	go func() {
		for {
			_, payload, err := c.ReadMessage()
			if err != nil {
				return
			}
			raw := payload
			var wrap struct {
				Encrypted bool   `json:"encrypted"`
				Data      string `json:"data"`
			}
			if json.Unmarshal(payload, &wrap) == nil && wrap.Encrypted {
				if raw, err = aes.Decrypt(wrap.Data); err != nil {
					continue
				}
			}
			var cmd struct { // data may be an object or a list (AddService)
				Type      string `json:"type"`
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(raw, &cmd) != nil || cmd.Type == "" || cmd.RequestID == "" {
				continue
			}
			b, _ := json.Marshal(map[string]interface{}{"type": cmd.Type + "Response", "requestId": cmd.RequestID, "success": true, "message": "OK"})
			_ = send(b)
		}
	}()

	if out := requestContractEnvelope(t, router, admin, "/api/v1/tunnel/create", map[string]interface{}{
		"name": "warm-port", "type": 1, "flow": 1, "status": 1, "inNodeId": []map[string]interface{}{{"nodeId": n.ID, "protocol": "tls"}}}); out.Code != 0 {
		t.Fatalf("tunnel create = %+v", out)
	}
	tid := mustQueryInt64(t, r, `SELECT id FROM tunnel WHERE name = ?`, "warm-port")
	if out := requestContractEnvelope(t, router, admin, "/api/v1/forward/create", map[string]interface{}{
		"name": "warm-rule", "tunnelId": tid, "remoteAddr": "198.51.100.8:8388"}); out.Code != 0 {
		t.Fatalf("forward create = %+v", out)
	}
	fid := mustQueryInt64(t, r, `SELECT id FROM forward WHERE name = ?`, "warm-rule")
	// The rule belongs to user 2 (role 1).
	ensureContractUser(t, r, 2, 1)
	if err := r.DB().Exec(`UPDATE forward SET user_id = 2 WHERE id = ?`, fid).Error; err != nil {
		t.Fatal(err)
	}
	user, err := auth.GenerateToken(2, "token_user_2", 1, secret)
	if err != nil {
		t.Fatal(err)
	}

	report := func(key string) {
		t.Helper()
		if err := send([]byte(`{"type":"metric","data":{"uptime":5,"cpu_usage":1,"memory_usage":1,"warm_pool":{"cap":64,"live":2,"keys":[` + key + `]}}}`)); err != nil {
			t.Fatal(err)
		}
	}
	owner := fmt.Sprintf("%d_2_0_tcp", fid)
	warmState := func(token string) string {
		t.Helper()
		out := requestContractEnvelope(t, router, token, "/api/v1/forward/list", map[string]interface{}{})
		if out.Code != 0 {
			t.Fatalf("forward list = %+v", out)
		}
		data, _ := out.Data.(map[string]interface{})
		items, _ := data["items"].([]interface{})
		for _, it := range items {
			item, _ := it.(map[string]interface{})
			if int64(item["id"].(float64)) == fid {
				if st, ok := item["warmPoolState"]; ok {
					return mustJSON(t, st)
				}
				return ""
			}
		}
		t.Fatalf("rule %d not in the list for this viewer", fid)
		return ""
	}
	waitAdmin := func(want string) string {
		t.Helper()
		var got string
		for i := 0; i < 200; i++ {
			if got = warmState(admin); strings.Contains(got, want) || (want == "" && got == "") {
				return got
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("admin warm tag = %q, want %q", got, want)
		return ""
	}

	// 4 streams, 3 warm: 预热中 · 数据不足 for admins, nothing for the owner.
	report(`{"kind":"s","owner":"` + owner + `","addr":"198.51.100.8:8388","target":true,"idle":2,"coldN":3,"coldMs":90,"missN":1,"missMs":30,"warmN":3}`)
	got := waitAdmin(`"status":"warming"`)
	if !strings.Contains(got, `"streams":4`) || !strings.Contains(got, `"minWarm":10`) || !strings.Contains(got, "198.51.100.8:8388") {
		t.Fatalf("admin 数据不足 = %s", got)
	}
	if got := warmState(user); got != "" {
		t.Fatalf("owner sees 数据不足: %s", got)
	}
	tunnels := requestContractEnvelope(t, router, admin, "/api/v1/tunnel/list", map[string]interface{}{})
	if body := mustJSON(t, tunnels.Data); !strings.Contains(body, `"status":"warming"`) {
		t.Fatalf("tunnel list without 数据不足: %s", body)
	}

	// Enough warm streams: green for both; the owner gets status + ms only.
	report(`{"kind":"s","owner":"` + owner + `","addr":"198.51.100.8:8388","target":true,"idle":2,"coldN":10,"coldMs":300,"warmN":20}`)
	waitAdmin(`"status":"faster"`)
	got = warmState(user)
	if !strings.Contains(got, `"status":"faster"`) || !strings.Contains(got, `"savedMs":`) {
		t.Fatalf("owner green tag = %s", got)
	}
	for _, leak := range []string{"198.51.100", "warm-entry", "warmN", "streams", "coldMs", "hitPct", "NodeId"} {
		if strings.Contains(got, leak) {
			t.Fatalf("owner tag leaks %q: %s", leak, got)
		}
	}

	// The key sleeps (no stream for 30 min on the agent): nothing.
	report(`{"kind":"s","owner":"` + owner + `","addr":"198.51.100.8:8388","target":true,"sleep":true}`)
	waitAdmin("")
	if got := warmState(user); got != "" {
		t.Fatalf("owner tag of a sleeping key: %s", got)
	}
}
