package socket

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func captureRoutineOutput(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old; w.Close(); r.Close() }()
	f()
	w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func routineTestReporter(t *testing.T) (*WebSocketReporter, *websocket.Conn) {
	t.Helper()
	incoming := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			incoming <- c
		}
	}))
	t.Cleanup(server.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	peer := <-incoming
	t.Cleanup(func() { client.Close(); peer.Close() })
	return &WebSocketReporter{conn: client, connected: true, secret: "private-psk"}, peer
}

func readRoutineResponse(t *testing.T, peer *websocket.Conn) CommandResponse {
	t.Helper()
	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	var response CommandResponse
	if err := peer.ReadJSON(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestRoutineSuccessfulTCPCommandsAndResponsesAreSilent(t *testing.T) {
	reporter, peer := routineTestReporter(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	out := captureRoutineOutput(t, func() {
		for i := 0; i < 25; i++ {
			cmd := CommandMessage{Type: "TcpPing", RequestId: fmt.Sprint(i), Data: TcpPingRequest{IP: "localhost", Port: listener.Addr().(*net.TCPAddr).Port, Count: 2, Timeout: 1000}}
			reporter.routeCommand(cmd)
			response := readRoutineResponse(t, peer)
			result := response.Data.(map[string]interface{})
			if !response.Success || result["success"] != true || result["packetLoss"] != float64(0) || response.RequestId != cmd.RequestId {
				t.Fatalf("response changed: %+v", response)
			}
		}
		for i := 0; i < 10; i++ {
			reporter.routeCommand(CommandMessage{Type: "ListServices", Data: map[string]interface{}{}})
			if !readRoutineResponse(t, peer).Success {
				t.Fatal("ListServices failed")
			}
		}
	})
	if out != "" {
		t.Fatalf("successful commands logged: %s", out)
	}
}

func TestRoutineFailuresAndResponseErrorsDoNotFlood(t *testing.T) {
	reporter, peer := routineTestReporter(t)
	out := captureRoutineOutput(t, func() {
		for i := 0; i < 20; i++ {
			reporter.routeCommand(CommandMessage{Type: "TcpPing", Data: TcpPingRequest{IP: "127.0.0.1", Port: 0}})
			response := readRoutineResponse(t, peer)
			if !response.Success || response.Data.(map[string]interface{})["success"] != false {
				t.Fatalf("envelope changed: %+v", response)
			}
			reporter.routeCommand(CommandMessage{Type: "PipePing", Data: map[string]interface{}{"tunnelId": 987654321, "port": 10015}})
			readRoutineResponse(t, peer)
		}
		// Losing the response connection is part of the same command result, not a
		// separate unbounded log or a false recovery when the probe itself succeeds.
		reporter.connected = false
		for i := 0; i < 20; i++ {
			reporter.routeCommand(CommandMessage{Type: "ListServices", Data: map[string]interface{}{}})
		}
		reporter.connected = true
		reporter.routeCommand(CommandMessage{Type: "ListServices", Data: map[string]interface{}{}})
		readRoutineResponse(t, peer)
	})
	if strings.Count(out, "首次失败") != 3 || strings.Count(out, "已恢复") != 1 || strings.Count(out, "\n") != 4 {
		t.Fatal(out)
	}
}

func TestRoutinePartialFailuresRecoveryAndRedaction(t *testing.T) {
	reporter := &WebSocketReporter{secret: "private-psk"}
	cmd := CommandMessage{Type: "TcpPing", Data: TcpPingRequest{IP: "private-psk", Port: 443}}
	response := CommandResponse{Success: true, Data: TcpPingResponse{Success: true, PacketLoss: 50}}
	out := captureRoutineOutput(t, func() {
		reporter.logRoutineResult(cmd, response, nil)
		reporter.logRoutineResult(cmd, response, nil)
		response.Data = TcpPingResponse{Success: true}
		reporter.logRoutineResult(cmd, response, nil)
		reporter.logRoutineResult(cmd, response, nil)
	})
	if strings.Contains(out, "private-psk") || strings.Count(out, "\n") != 2 || !strings.Contains(out, "丢包 50.0%") || !strings.Contains(out, "已恢复") {
		t.Fatal(out)
	}
}

func TestNonRoutineCommandKeepsSingleRedactedLog(t *testing.T) {
	reporter, peer := routineTestReporter(t)
	out := captureRoutineOutput(t, func() {
		reporter.routeCommand(CommandMessage{Type: "UpgradeAgent", Data: map[string]interface{}{"secret": "private-psk", "config": "private-config"}})
		readRoutineResponse(t, peer)
	})
	if out != "🔔 收到命令: UpgradeAgent (data redacted)\n" {
		t.Fatal(out)
	}
}

func TestOtherRoutineSuccessesAreSilent(t *testing.T) {
	reporter := &WebSocketReporter{}
	out := captureRoutineOutput(t, func() {
		for i := 0; i < 100; i++ {
			reporter.logRoutineResult(CommandMessage{Type: "PipePing", Data: map[string]interface{}{"tunnelId": 1, "port": 10001}}, CommandResponse{Success: true, Data: TcpPingResponse{Success: true}}, nil)
			reporter.logRoutineResult(CommandMessage{Type: "ServiceMonitorCheck", Data: map[string]interface{}{"type": "tcp", "target": "example:443"}}, CommandResponse{Success: true, Data: ServiceMonitorCheckResult{Success: true}}, nil)
			reporter.logRoutineResult(CommandMessage{Type: "GetServiceClientIPs", Data: map[string]interface{}{"forwardIds": []int64{1, 2}}}, CommandResponse{Success: true}, nil)
		}
	})
	if out != "" {
		t.Fatal(out)
	}
}
