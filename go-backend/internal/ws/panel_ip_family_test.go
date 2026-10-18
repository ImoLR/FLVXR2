package ws

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go-backend/internal/security"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func panelFamilyServer(t *testing.T, desired string) (*Server, *repo.Repository, string) {
	t.Helper()
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if err := r.DB().Create(&model.Node{ID: 1, Name: "panel", Secret: "test-secret", ServerIP: "127.0.0.1", PanelIPFamily: desired}).Error; err != nil {
		t.Fatal(err)
	}
	s := NewServer(r, "test-jwt")
	h := httptest.NewServer(s)
	t.Cleanup(h.Close)
	return s, r, "ws" + strings.TrimPrefix(h.URL, "http") + "/system-info?type=1&secret=test-secret"
}

func TestPanelIPFamilyPushAndOldAgent(t *testing.T) {
	for _, tc := range []struct {
		name, desired, reported string
		push                    bool
	}{
		{"new", "prefer_ipv4", "auto", true}, {"reset", "auto", "ipv6_only", true},
		{"old", "ipv4_only", "", false}, {"same", "ipv6_only", "ipv6_only", false},
		{"installer", "", "prefer_ipv4", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, url := panelFamilyServer(t, tc.desired)
			done := make(chan error, 1)
			s.SetNodeOnlineHook(func(id int64) { done <- s.SyncPanelIPFamily(id) })
			if tc.reported != "" {
				url += "&panelIpFamily=" + tc.reported
			}
			c, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if tc.push {
				_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
				_, payload, err := c.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
				aes, _ := security.NewAESCrypto("test-secret")
				raw := decryptIfNeeded(payload, aes, "test-secret")
				var cmd struct {
					Type, RequestID string
					Data            map[string]string
				}
				if err := json.Unmarshal([]byte(raw), &cmd); err != nil {
					t.Fatal(err)
				}
				if cmd.Type != "SetPanelIPFamily" || cmd.Data["panelIpFamily"] != tc.desired {
					t.Fatalf("command=%+v", cmd)
				}
				if err := c.WriteJSON(map[string]interface{}{"type": "SetPanelIPFamilyResponse", "requestId": cmd.RequestID, "success": true}); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("sync blocked")
			}
			if !tc.push {
				_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				if _, _, err := c.ReadMessage(); err == nil {
					t.Fatal("unexpected command")
				}
			}
			if tc.name == "installer" {
				n, _ := r.GetNodeByID(1)
				if n.PanelIPFamily != tc.reported {
					t.Fatal("installer mode not retained")
				}
			}
		})
	}
}

func TestDeadAgentMarkedOfflineWithinTwoMinutes(t *testing.T) {
	s, r, url := panelFamilyServer(t, "")
	online := make(chan struct{}, 1)
	offline := make(chan struct{}, 1)
	s.SetNodeOnlineHook(func(int64) { online <- struct{}{} })
	s.SetNodeOfflineHook(func(int64) { offline <- struct{}{} })
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-online
	start := time.Now()
	// No reader/pong after a successful handshake: TCP stays open throughout.
	select {
	case <-offline:
		elapsed := time.Since(start)
		if elapsed < wsPongWait-time.Second || elapsed > 2*time.Minute {
			t.Fatalf("offline after %s", elapsed)
		}
		node, err := r.GetNodeByID(1)
		if err != nil || node.Status != 0 {
			t.Fatalf("node=%+v err=%v", node, err)
		}
		t.Logf("half-open agent marked offline after %s", elapsed)
	case <-time.After(70 * time.Second):
		t.Fatal("half-open node remained online")
	}
}
