package socket

import (
	"errors"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestWebSocketFallbackLogsRedactSecret(t *testing.T) {
	old := wsDial
	defer func() { wsDial = old }()
	wsDial = func(_ *websocket.Dialer, raw string) (*websocket.Conn, *http.Response, error) {
		return nil, &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`{"config":"test-node-psk"}`))}, &url.Error{Op: "Get", URL: raw, Err: errors.New("refused")}
	}
	r, w, _ := os.Pipe()
	oldout := os.Stdout
	os.Stdout = w
	_, _, err := dialWebSocketWithFallback(&websocket.Dialer{}, []string{"wss://panel/ws?secret=test-node-psk", "ws://panel/ws?secret=test-node-psk"})
	w.Close()
	os.Stdout = oldout
	out, _ := io.ReadAll(r)
	r.Close()
	if strings.Contains(string(out)+err.Error(), "test-node-psk") {
		t.Fatal("secret in websocket log/error")
	}
}
func TestCommandLogOmitsConfig(t *testing.T) {
	r, w, _ := os.Pipe()
	oldout := os.Stdout
	os.Stdout = w
	reporter := &WebSocketReporter{}
	reporter.routeCommand(CommandMessage{Type: "UnknownTestCommand", Data: map[string]interface{}{"secret": "private-psk", "config": "private-config", "token": "private-token"}})
	w.Close()
	os.Stdout = oldout
	out, _ := io.ReadAll(r)
	r.Close()
	if strings.Contains(string(out), "private-") || !strings.Contains(string(out), "UnknownTestCommand") {
		t.Fatalf("command log: %s", out)
	}
}
