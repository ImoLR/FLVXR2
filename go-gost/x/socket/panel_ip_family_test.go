package socket

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gost/x/panelconn"
	"github.com/gorilla/websocket"
)

func TestPanelIPFamilyPersist(t *testing.T) {
	old := panelconn.Default
	panelconn.Default = panelconn.New()
	defer func() { panelconn.Default.CloseIdleConnections(); panelconn.Default = old }()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"addr":"panel","secret":"psk","unknown":9007199254740993,"http":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	w := NewWebSocketReporter("", "")
	w.configPath = path
	defer w.Stop()
	for _, mode := range []string{"prefer_ipv4", "ipv4_only", "ipv6_only", "auto"} {
		changed, err := w.handleSetPanelIPFamily(map[string]interface{}{"panelIpFamily": mode})
		if err != nil || !changed || panelconn.Default.Mode() != mode {
			t.Fatalf("%s: changed=%t err=%v", mode, changed, err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]json.RawMessage
		if err := json.Unmarshal(b, &cfg); err != nil {
			t.Fatal(err)
		}
		if string(cfg["panel_ip_family"]) != `"`+mode+`"` || string(cfg["unknown"]) != "9007199254740993" || string(cfg["http"]) != "1" {
			t.Fatalf("config lost values: %s", b)
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatal("permissions changed")
		}
	}
	before, _ := os.ReadFile(path)
	if _, err := w.handleSetPanelIPFamily(map[string]interface{}{"panelIpFamily": "bad"}); err == nil {
		t.Fatal("invalid accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("invalid value changed file")
	}
	if changed, err := w.handleSetPanelIPFamily(map[string]interface{}{"panelIpFamily": ""}); changed || err != nil {
		t.Fatal("empty must equal auto")
	}
	if _, err := w.handleSetPanelIPFamily(map[string]interface{}{}); err == nil {
		t.Fatal("missing value accepted")
	}
	w.configPath = filepath.Join(t.TempDir(), "missing.json")
	if _, err := w.handleSetPanelIPFamily(map[string]interface{}{"panelIpFamily": "ipv6_only"}); err == nil || panelconn.Default.Mode() != "auto" {
		t.Fatal("failed persist applied setting")
	}
}

func TestAbnormalPanelClose(t *testing.T) {
	for _, code := range []int{websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.ClosePolicyViolation, websocket.CloseInternalServerErr, websocket.CloseNoStatusReceived} {
		if abnormalPanelClose(&websocket.CloseError{Code: code}) {
			t.Fatalf("server close %d counted", code)
		}
	}
	if !abnormalPanelClose(&websocket.CloseError{Code: websocket.CloseAbnormalClosure}) || !abnormalPanelClose(os.ErrDeadlineExceeded) || !abnormalPanelClose(errors.New("connection reset by peer")) || abnormalPanelClose(nil) {
		t.Fatal("abnormal close classification")
	}
}
