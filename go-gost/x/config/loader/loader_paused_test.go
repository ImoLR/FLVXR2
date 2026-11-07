package loader

import (
	"net"
	"testing"

	"github.com/go-gost/core/logger"
	"github.com/go-gost/x/config"
	service_parser "github.com/go-gost/x/config/parsing/service"
	"github.com/go-gost/x/registry"

	_ "github.com/go-gost/x/connector/http"
	_ "github.com/go-gost/x/dialer/tcp"
	_ "github.com/go-gost/x/handler/forward/local"
	_ "github.com/go-gost/x/listener/tcp"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func tcpService(name, addr string, paused bool) *config.ServiceConfig {
	s := &config.ServiceConfig{
		Name: name, Addr: addr,
		Handler:   &config.HandlerConfig{Type: "tcp"},
		Listener:  &config.ListenerConfig{Type: "tcp"},
		Forwarder: &config.ForwarderConfig{Nodes: []*config.ForwardNodeConfig{{Name: "n1", Addr: "127.0.0.1:9"}}},
	}
	if paused {
		s.Metadata = map[string]any{"paused": true}
	}
	return s
}

// Plan 236: the agent loader (startup and reload) must not open a service whose
// gost.json entry is paused — it registers a closed placeholder instead —, while
// every other service is parsed (bound) as before.
func TestLoadKeepsPausedServicesClosed(t *testing.T) {
	if prev := logger.Default(); prev != nil {
		defer logger.SetDefault(prev)
	}
	pausedAddr, runningAddr := freeAddr(t), freeAddr(t)
	cfg := &config.Config{Services: []*config.ServiceConfig{
		tcpService("pr_paused_tcp", pausedAddr, true),
		tcpService("pr_running_tcp", runningAddr, false),
	}}
	t.Cleanup(func() {
		registry.ServiceRegistry().Unregister("pr_paused_tcp")
		registry.ServiceRegistry().Unregister("pr_running_tcp")
	})
	for round := 0; round < 2; round++ { // startup, then a reload of the same config
		if err := Load(cfg); err != nil {
			t.Fatal(err)
		}
		paused := registry.ServiceRegistry().Get("pr_paused_tcp")
		if paused == nil || !service_parser.IsPausedService(paused) {
			t.Fatalf("round %d: paused service registered as %T", round, paused)
		}
		if paused.Addr().String() != pausedAddr || paused.Serve() != nil || paused.Close() != nil {
			t.Fatalf("round %d: placeholder addr %v", round, paused.Addr())
		}
		l, err := net.Listen("tcp", pausedAddr)
		if err != nil {
			t.Fatalf("round %d: paused service holds its port: %v", round, err)
		}
		l.Close()
		running := registry.ServiceRegistry().Get("pr_running_tcp")
		if running == nil || service_parser.IsPausedService(running) {
			t.Fatalf("round %d: running service registered as %T", round, running)
		}
		if l, err := net.Listen("tcp", runningAddr); err == nil {
			l.Close()
			t.Fatalf("round %d: running service did not bind its port", round)
		}
	}
}
