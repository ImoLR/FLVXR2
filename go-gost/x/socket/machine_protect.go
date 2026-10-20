package socket

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/go-gost/x/landing"
	"github.com/go-gost/x/machineprotect"
	"github.com/go-gost/x/registry"
)

// machineProtectVersion is the handshake capability (?machineProtect=1): the
// panel only sends SetMachineProtection to agents that announce it.
const machineProtectVersion = 1

// machineProtectionKey holds the last panel settings in config.json, so the agent
// applies them on start before the panel connects.
const machineProtectionKey = "machine_protection"

type nftablesRef struct{ mgr NftablesManagerInterface }

// initMachineProtection builds the ONE protection loop of this process; Start
// runs it. WebSocket reconnects never create another one.
func (w *WebSocketReporter) initMachineProtection(configDir, configPath string) {
	if w.protector != nil {
		return
	}
	w.protector = machineprotect.New(machineprotect.Options{
		StateDir:  configDir,
		Ports:     w.businessPorts,
		CloseWait: func(seconds int64) { landing.Default().SetCloseWaitTimeout(seconds) },
	})
	if s, ok := loadMachineProtectionSettings(configPath); ok {
		w.protector.Configure(s)
	}
}

func loadMachineProtectionSettings(path string) (machineprotect.Settings, bool) {
	if path == "" {
		return machineprotect.Settings{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return machineprotect.Settings{}, false
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(b, &cfg) != nil || len(cfg[machineProtectionKey]) == 0 {
		return machineprotect.Settings{}, false
	}
	var s machineprotect.Settings
	if json.Unmarshal(cfg[machineProtectionKey], &s) != nil || s.Validate() != nil {
		fmt.Println("⚠️ config.json 中的 machine_protection 无效，等待面板下发")
		return machineprotect.Settings{}, false
	}
	return s, true
}

// handleSetMachineProtection persists then applies the panel's settings.
func (w *WebSocketReporter) handleSetMachineProtection(data interface{}) error {
	if w.protector == nil {
		return errors.New("machine protection is not running")
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var req struct {
		Enabled          *bool  `json:"enabled"`
		MaxTotal         *int64 `json:"maxTotal"`
		TWBuckets        *int64 `json:"twBuckets"`
		CloseWaitSeconds *int64 `json:"closeWaitSeconds"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		return err
	}
	if req.Enabled == nil || req.MaxTotal == nil || req.TWBuckets == nil || req.CloseWaitSeconds == nil {
		return errors.New("enabled, maxTotal, twBuckets and closeWaitSeconds are required")
	}
	s := machineprotect.Settings{Enabled: *req.Enabled, MaxTotal: *req.MaxTotal, TWBuckets: *req.TWBuckets, CloseWaitSeconds: *req.CloseWaitSeconds}
	if err := s.Validate(); err != nil {
		return err
	}
	path := w.configPath
	if path == "" {
		path = getConfigDir(w.serviceName) + "/config.json"
	}
	if err := updateAgentSettings(path, map[string]interface{}{machineProtectionKey: s}); err != nil {
		return err
	}
	w.protector.Configure(s)
	return nil
}

// setNftablesManager publishes the current nft manager to the protection loop.
func (w *WebSocketReporter) setNftablesManager(mgr NftablesManagerInterface) {
	w.nftablesMgr = mgr
	w.nftRef.Store(&nftablesRef{mgr: mgr})
}

func (w *WebSocketReporter) currentNftablesManager() NftablesManagerInterface {
	if ref := w.nftRef.Load(); ref != nil {
		return ref.mgr
	}
	return nil
}

// businessPorts lists every port this agent serves for users: gost service
// listeners (except loopback-only ones), nft-forwarded rule ports and native
// landing inbound ports. The panel/management connection is outbound and never
// listed.
func (w *WebSocketReporter) businessPorts() machineprotect.PortSet {
	c := machineprotect.NewPortCollector()
	if reg := registry.ServiceRegistry(); reg != nil {
		for _, svc := range reg.GetAll() {
			if svc == nil {
				continue
			}
			addListenerPort(c, svc.Addr())
		}
	}
	if mgr := w.currentNftablesManager(); mgr != nil {
		tcp, udp := mgr.ListenPorts()
		for _, p := range tcp {
			c.AddTCP(p)
		}
		for _, p := range udp {
			c.AddUDP(p)
		}
	}
	tcp, udp := landing.Default().PublicPorts()
	for _, p := range tcp {
		c.AddTCP(p)
	}
	for _, p := range udp {
		c.AddUDP(p)
	}
	return c.Set()
}

func addListenerPort(c *machineprotect.PortCollector, addr net.Addr) {
	if addr == nil {
		return
	}
	var ip net.IP
	port := 0
	switch a := addr.(type) {
	case *net.TCPAddr:
		ip, port = a.IP, a.Port
	case *net.UDPAddr:
		ip, port = a.IP, a.Port
	default:
		host, p, err := net.SplitHostPort(addr.String())
		if err != nil {
			return
		}
		ip = net.ParseIP(strings.Trim(host, "[]"))
		port, _ = strconv.Atoi(p)
	}
	if ip != nil && ip.IsLoopback() {
		return // reachable only from this host; nothing to protect
	}
	network := addr.Network()
	switch {
	case strings.HasPrefix(network, "udp"):
		c.AddUDP(port)
	case strings.HasPrefix(network, "tcp"):
		c.AddTCP(port)
	}
}

// machineReport returns the newest protection sample once per sample and
// connection (the panel keeps the last value in memory): the 1 s metric carries
// it only every 5 s.
func (w *WebSocketReporter) machineReport() (*machineprotect.Conns, *machineprotect.Protect) {
	if w.protector == nil {
		return nil, nil
	}
	snap := w.protector.Snapshot()
	if snap == nil || snap.Seq == w.machineSentSeq {
		return nil, nil
	}
	w.machineSentSeq = snap.Seq
	prot := snap.Protect
	return snap.Conns, &prot
}
