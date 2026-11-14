package socket

import (
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-gost/x/config"
	"github.com/go-gost/x/landing"
	"github.com/go-gost/x/monitorport"
)

// Plan 255 「检测端口」: the MonitorPort command (open / close / list / sync)
// on the panel's authenticated channel. The ports are owned by
// monitorport.Default(); this file wires the agent's own port knowledge in
// (a check port never takes a port of the agent's services) and makes a real
// service win when it needs a port a check port holds.

var processStart = time.Now()

const (
	// monitorPortSettle: ports open only once the gost config is loaded and
	// its services listen (the reporter starts before the services).
	monitorPortSettle = 5 * time.Second
	// monitorPortRestoreAfter: the persisted set is restored this long after
	// the start (the panel's reconnect sync usually comes first).
	monitorPortRestoreAfter = 10 * time.Second
)

// initMonitorPorts: the set lives next to config.json; it is restored once
// the services of this agent are up (unless the panel's sync came first).
func (w *WebSocketReporter) initMonitorPorts(configDir, configPath string) {
	dir := configDir
	if configPath != "" {
		if abs, err := filepath.Abs(configPath); err == nil {
			dir = filepath.Dir(abs)
		}
	}
	monitorport.Default().Configure(filepath.Join(dir, monitorport.StateFile), w.monitorPortInUse)
	go func() {
		select {
		case <-time.After(monitorPortRestoreAfter):
		case <-w.ctx.Done():
			return
		}
		monitorport.Default().Restore()
	}()
}

// handleMonitorPort runs one MonitorPort command (after the settle time).
func (w *WebSocketReporter) handleMonitorPort(data interface{}) (monitorport.Response, error) {
	if wait := monitorPortSettle - time.Since(processStart); wait > 0 {
		time.Sleep(wait)
	}
	return monitorport.Default().Handle(data)
}

// monitorPortInUse: ports the agent serves itself — gost services (incl.
// paused and loopback ones, from the config), nft rule ports and native
// landing ports. The bind itself catches every other program.
func (w *WebSocketReporter) monitorPortInUse(port int) (bool, string) {
	set := w.businessPorts()
	for _, p := range set.TCP {
		if p == port {
			return true, "该端口是本节点规则 / 落地正在使用的端口"
		}
	}
	if cfg := config.Global(); cfg != nil {
		for _, s := range cfg.Services {
			if s != nil && listenPort(s.Addr) == port {
				return true, "该端口属于本节点的转发服务"
			}
		}
	}
	tcp, _ := landing.Default().PublicPorts()
	for _, p := range tcp {
		if p == port {
			return true, "该端口是本节点的落地端口"
		}
	}
	return false, ""
}

// listenPort is the port of a service address (":123", "0.0.0.0:123",
// "[::]:123", "123"); 0 when there is none.
func listenPort(addr string) int {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return 0
	}
	if _, p, err := net.SplitHostPort(addr); err == nil {
		n, _ := strconv.Atoi(p)
		return n
	}
	n, _ := strconv.Atoi(addr)
	return n
}

// yieldMonitorPortsToServices closes check ports on the ports of services
// about to listen (a real rule always wins).
func yieldMonitorPortsToServices(services []config.ServiceConfig) {
	for i := range services {
		if p := listenPort(services[i].Addr); p > 0 {
			monitorport.Default().Yield(p)
		}
	}
}

// yieldMonitorPortsToLanding does the same for native landing inbounds.
func yieldMonitorPortsToLanding(d landing.Desired) {
	for _, n := range d.Nodes {
		if n.Port > 0 {
			monitorport.Default().Yield(n.Port)
		}
	}
}
