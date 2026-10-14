package socket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/go-gost/core/chain"
	"github.com/go-gost/relay"
	"github.com/go-gost/x/registry"
)

// PipePing is deliberately restricted to the loopback first hop of an already
// deployed tunnel. It reuses that transport (including TLS) and never opens an
// arbitrary destination or creates a separate connection pool.
func (w *WebSocketReporter) handlePipePing(data interface{}) (TcpPingResponse, error) {
	var req struct {
		TunnelID int64 `json:"tunnelId"`
		Port     int   `json:"port"`
		Timeout  int   `json:"timeout"`
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return TcpPingResponse{}, err
	}
	if err = json.Unmarshal(raw, &req); err != nil {
		return TcpPingResponse{}, err
	}
	if req.TunnelID <= 0 || req.Port < 1 || req.Port > 65535 {
		return TcpPingResponse{}, errors.New("invalid pipe probe")
	}
	if req.Timeout <= 0 || req.Timeout > 5000 {
		req.Timeout = 5000
	}
	parent := w.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(req.Timeout)*time.Millisecond)
	defer cancel()
	result := TcpPingResponse{IP: "127.0.0.1", Port: req.Port, PacketLoss: 100}
	ch := registry.ChainRegistry().Get(fmt.Sprintf("chains_%d", req.TunnelID))
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(req.Port))
	if ch == nil {
		result.ErrorMessage = "入口未部署对应利群管道转发链"
		return result, nil
	}
	route := ch.Route(ctx, "tcp", address)
	if route == nil || len(route.Nodes()) != 1 || route.Nodes()[0].Addr != address {
		result.ErrorMessage = "入口未部署对应利群管道转发链"
		return result, nil
	}
	start := time.Now()
	err = probePipeTransport(ctx, route.Nodes()[0].Options().Transport, address)
	if err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}
	result.Success = true
	result.PacketLoss = 0
	result.AverageTime = float64(time.Since(start).Microseconds()) / 1000
	return result, nil
}

func probePipeTransport(ctx context.Context, transport chain.Transporter, address string) error {
	if transport == nil {
		return errors.New("pipe transport missing")
	}
	conn, err := transport.Dial(ctx, address)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	// Cancellation closes a pending handshake/read; stop waits are bounded by
	// the connection deadline and no detached goroutine survives a probe.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	secure, err := transport.Handshake(ctx, conn)
	if err != nil {
		return err
	}
	if secure != conn {
		defer secure.Close()
	}
	if err = secure.SetDeadline(deadline); err != nil {
		return err
	}
	// Unsupported command elicits a relay error before routing,
	// or opening any target connection. This proves the actual landing replied.
	request := relay.Request{Version: relay.Version1, Cmd: relay.CmdMask}
	if _, err = request.WriteTo(secure); err != nil {
		return err
	}
	var response relay.Response
	if _, err = response.ReadFrom(secure); err != nil {
		return err
	}
	if response.Version != relay.Version1 || response.Status != relay.StatusBadRequest {
		return errors.New("落地未返回有效 FLVX relay 响应")
	}
	return nil
}
