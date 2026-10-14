package socket

import (
	"context"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/go-gost/core/chain"
	"github.com/go-gost/relay"
)

type pipeTestTransport struct {
	chain.Transporter
	dial func(context.Context, string) (net.Conn, error)
}

func (p pipeTestTransport) Dial(ctx context.Context, addr string) (net.Conn, error) {
	return p.dial(ctx, addr)
}
func (p pipeTestTransport) Handshake(_ context.Context, c net.Conn) (net.Conn, error) { return c, nil }

func TestLeiKwanPipeProbeRequiresLandingResponse(t *testing.T) {
	for _, reply := range []bool{true, false} {
		t.Run(map[bool]string{true: "landing responds", false: "client accepts but landing unavailable"}[reply], func(t *testing.T) {
			transport := pipeTestTransport{dial: func(context.Context, string) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					var req relay.Request
					if _, err := req.ReadFrom(server); err != nil {
						return
					}
					if req.Cmd == relay.CmdConnect || req.Cmd == relay.CmdBind || len(req.Features) != 0 {
						t.Error("probe must not request routing to a destination")
					}
					if reply {
						_, _ = (&relay.Response{Version: relay.Version1, Status: relay.StatusBadRequest}).WriteTo(server)
					}
				}()
				return client, nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := probePipeTransport(ctx, transport, "127.0.0.1:10015")
			if (err == nil) != reply {
				t.Fatalf("reply=%v err=%v", reply, err)
			}
		})
	}
}

func TestLeiKwanPipeProbeCancellationDoesNotLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()
	for i := 0; i < 25; i++ {
		done := make(chan struct{})
		transport := pipeTestTransport{dial: func(context.Context, string) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer close(done)
				defer server.Close()
				var req relay.Request
				_, _ = req.ReadFrom(server)
				buf := make([]byte, 1)
				_, _ = server.Read(buf)
			}()
			return client, nil
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		if err := probePipeTransport(ctx, transport, "127.0.0.1:10015"); err == nil {
			t.Fatal("stalled pipe succeeded")
		}
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("server connection leaked")
		}
	}
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > baseline+2 && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	if current := runtime.NumGoroutine(); current > baseline+2 {
		t.Fatalf("goroutines before=%d after=%d", baseline, current)
	}
}

func TestLeiKwanPipeProbeMissingChain(t *testing.T) {
	reporter := &WebSocketReporter{}
	data, err := reporter.handlePipePing(map[string]interface{}{"tunnelId": int64(987654321), "port": 10015})
	if err != nil || data.Success || data.ErrorMessage == "" {
		t.Fatalf("missing chain: %+v %v", data, err)
	}
}
