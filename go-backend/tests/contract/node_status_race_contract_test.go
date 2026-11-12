package contract_test

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gorm.io/gorm"

	"go-backend/internal/store/repo"
)

// Plan 248 (11-12 01:59 TW): Mkcloud 沪日ixp 440 and Bestvm HK 年付 stayed
// 「离线」 for 16 h while their agents were connected. The panel stalled, the
// old session's 离线 write queued on the single SQLite connection behind the
// new session's 在线 write (database/sql hands a freed connection to a random
// waiter) and landed last. These tests hold the old session's write back on
// purpose: the stored flag must still end up 在线 and heal if it is wrong.

func insertStatusNode(t *testing.T, r *repo.Repository, name, secret string, status int) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, server_ip_v4, server_ip_v6, port, interface_name, version, http, tls, socks, created_time, updated_time, status, tcp_listen_addr, udp_listen_addr, inx)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, name, secret, "10.48.0.1", "10.48.0.1", "", "43000-43010", "", "v1", 1, 1, 1, now, now, status, "[::]", "[::]", 0).Error; err != nil {
		t.Fatal(err)
	}
	return mustLastInsertID(t, r, name)
}

// dialStatusAgent connects a minimal agent (answers pings, nothing else). It
// does not wait for the panel to register the session.
func dialStatusAgent(t *testing.T, baseURL, nodeSecret string) *websocket.Conn {
	t.Helper()
	u, _ := url.Parse(baseURL)
	u.Scheme = "ws"
	u.Path = "/system-info"
	q := u.Query()
	for k, v := range map[string]string{"type": "1", "secret": nodeSecret, "version": "3.0.27-fork.129 (debian/amd64)", "http": "1", "tls": "1", "socks": "1"} {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("dial agent: %v", err)
	}
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	return conn
}

func nodeStatusOf(t *testing.T, r *repo.Repository, nodeID int64) int {
	t.Helper()
	return mustQueryInt(t, r, `SELECT status FROM node WHERE id = ?`, nodeID)
}

func waitStoredNodeStatus(t *testing.T, r *repo.Repository, nodeID int64, want int, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if nodeStatusOf(t, r, nodeID) == want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// holdOfflineWrites blocks every node.status=0 update until release is
// called. The hold sits before GORM opens its transaction, so it holds no
// database connection — like a writer still queued for the pool.
func holdOfflineWrites(t *testing.T, r *repo.Repository) (held <-chan struct{}, release func()) {
	t.Helper()
	heldCh := make(chan struct{}, 8)
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	err := r.DB().Callback().Update().Before("gorm:begin_transaction").Register("plan248:hold-offline", func(db *gorm.DB) {
		if db.Statement == nil || db.Statement.Table != "node" {
			return
		}
		values, ok := db.Statement.Dest.(map[string]interface{})
		if !ok {
			return
		}
		if st, ok := values["status"]; !ok || st != 0 {
			return
		}
		select {
		case heldCh <- struct{}{}:
		default:
		}
		<-gate
	})
	if err != nil {
		t.Fatal(err)
	}
	return heldCh, release
}

func TestNodeStatusStaleOfflineWriteCannotOverrideNewSessionContract(t *testing.T) {
	router, r := setupContractRouter(t, "contract-jwt-secret")
	nodeSecret := "plan248-race-node"
	nodeID := insertStatusNode(t, r, "plan248-race", nodeSecret, 0)
	server := httptest.NewServer(router)
	defer server.Close()

	oldConn := dialStatusAgent(t, server.URL, nodeSecret)
	if !waitStoredNodeStatus(t, r, nodeID, 1, 5*time.Second) {
		t.Fatalf("first session never stored 在线")
	}

	// Marks the new session's 在线 write (it stores the handshake version).
	if err := r.DB().Exec(`UPDATE node SET version = 'before-reconnect' WHERE id = ?`, nodeID).Error; err != nil {
		t.Fatal(err)
	}
	held, release := holdOfflineWrites(t, r)
	// The old session ends (pong timeout in production) …
	_ = oldConn.Close()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatalf("old session never wrote 离线")
	}
	// … its 离线 write is stuck while the agent reconnects.
	newConn := dialStatusAgent(t, server.URL, nodeSecret)
	defer newConn.Close()
	// Before the fix the new session's 在线 write lands here, ahead of the
	// held 离线 write; after it the new session waits for the old write (the
	// 1 s also lets it register).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && mustQueryString(t, r, `SELECT version FROM node WHERE id = ?`, nodeID) == "before-reconnect" {
		time.Sleep(20 * time.Millisecond)
	}
	release()

	if !waitStoredNodeStatus(t, r, nodeID, 1, 3*time.Second) {
		t.Fatalf("node stored 离线 while its new agent session is live (stale old-session write won)")
	}
	time.Sleep(300 * time.Millisecond)
	if st := nodeStatusOf(t, r, nodeID); st != 1 {
		t.Fatalf("node status flipped to %d after the old write was released", st)
	}
}

func TestNodeStatusSelfHealsFromLiveSessionContract(t *testing.T) {
	router, r := setupContractRouter(t, "contract-jwt-secret")
	nodeSecret := "plan248-heal-node"
	nodeID := insertStatusNode(t, r, "plan248-heal", nodeSecret, 0)
	server := httptest.NewServer(router)
	defer server.Close()

	conn := dialStatusAgent(t, server.URL, nodeSecret)
	defer conn.Close()
	if !waitStoredNodeStatus(t, r, nodeID, 1, 5*time.Second) {
		t.Fatalf("session never stored 在线")
	}
	// Any stale writer (the 01:59 race, a failed write, an old process) left
	// 离线 behind although the agent is connected and reporting.
	if err := r.DB().Exec(`UPDATE node SET status = 0 WHERE id = ?`, nodeID).Error; err != nil {
		t.Fatal(err)
	}
	metric, _ := json.Marshal(map[string]interface{}{"type": "metric", "data": map[string]interface{}{"cpu_usage": 1.5, "memory_usage": 20.0, "uptime": 100}})
	if err := conn.WriteMessage(websocket.TextMessage, metric); err != nil {
		t.Fatal(err)
	}
	if !waitStoredNodeStatus(t, r, nodeID, 1, 3*time.Second) {
		t.Fatalf("stored 离线 not healed by the live session's metric report")
	}
}
