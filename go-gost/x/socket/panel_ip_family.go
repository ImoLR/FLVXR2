package socket

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-gost/x/panelconn"
	"github.com/gorilla/websocket"
)

func (w *WebSocketReporter) sessionErrorLocked(conn *websocket.Conn, err error) {
	if conn != w.conn {
		return
	}
	w.connected = false
	if w.sessionEnded {
		return
	}
	w.sessionEnded = true
	if w.ctx.Err() != nil || !abnormalPanelClose(err) {
		return
	}
	if panelconn.Family(conn.RemoteAddr()) == "IPv6" {
		panelconn.Default.IPv6Failure()
	}
}

func abnormalPanelClose(err error) bool {
	if err == nil {
		return false
	}
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return closeErr.Code == websocket.CloseAbnormalClosure || closeErr.Code == websocket.CloseTLSHandshake
	}
	return true
}

func (w *WebSocketReporter) handleSetPanelIPFamily(data interface{}) (bool, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return false, err
	}
	var req struct {
		PanelIPFamily *string `json:"panelIpFamily"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		return false, err
	}
	if req.PanelIPFamily == nil {
		return false, fmt.Errorf("panelIpFamily is required")
	}
	mode, err := panelconn.Normalize(*req.PanelIPFamily)
	if err != nil {
		return false, err
	}
	path := w.configPath
	if path == "" {
		path = getConfigDir(w.serviceName) + "/config.json"
	}
	if err := updateAgentSettings(path, map[string]interface{}{"panel_ip_family": mode}); err != nil {
		return false, err
	}
	return panelconn.Default.SetMode(mode)
}

// Called after acknowledging the command, so the panel receives the result
// before the next connection uses the persisted setting.
func (w *WebSocketReporter) reconnectPanel() {
	w.connMutex.Lock()
	defer w.connMutex.Unlock()
	w.sessionEnded = true
	w.reconnectRequested = true
	w.connected = false
	if w.conn != nil {
		_ = w.conn.Close()
	}
}
