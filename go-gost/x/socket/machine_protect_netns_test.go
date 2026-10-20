//go:build linux

package socket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	corelogger "github.com/go-gost/core/logger"
	"github.com/go-gost/x/config"
	_ "github.com/go-gost/x/connector/forward"
	_ "github.com/go-gost/x/connector/http"
	_ "github.com/go-gost/x/dialer/tcp"
	_ "github.com/go-gost/x/dialer/udp"
	_ "github.com/go-gost/x/handler/forward/local"
	_ "github.com/go-gost/x/listener/tcp"
	_ "github.com/go-gost/x/listener/udp"
	xlogger "github.com/go-gost/x/logger"
	"github.com/go-gost/x/machineprotect"
	"github.com/go-gost/x/nftables"
	"github.com/go-gost/x/registry"
	"golang.org/x/sys/unix"
)

// Agent netns E2E for machine protection (plan 118). Run only through
// machine_protect_netns.sh, which builds the throwaway namespaces
//
//	client C 10.231.1.2 <-> 10.231.1.1 node N (this test) 10.231.2.1 <-> 10.231.2.2 target T
//
// and runs the test binary inside N: real gost services, a real nft DNAT
// forward, the real protection loop and the real nft guard. Nothing on the host
// is touched.
const (
	e2eNodeIP   = "10.231.1.1"
	e2eTargetIP = "10.231.2.2"
)

func e2eEnv(t *testing.T) (client, target string) {
	if os.Getenv("FLVX_MP_NETNS_E2E") != "1" {
		t.Skip("run through machine_protect_netns.sh")
	}
	client, target = os.Getenv("FLVX_MP_CLIENT_NS"), os.Getenv("FLVX_MP_TARGET_NS")
	if client == "" || target == "" {
		t.Fatal("namespace names missing")
	}
	return client, target
}

// inNetns runs fn on a thread switched into the named namespace; sockets created
// synchronously inside fn belong to it.
func inNetns(t *testing.T, name string, fn func()) {
	t.Helper()
	runtime.LockOSThread()
	self, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		runtime.UnlockOSThread()
		t.Fatal(err)
	}
	defer self.Close()
	target, err := os.Open("/run/netns/" + name)
	if err != nil {
		runtime.UnlockOSThread()
		t.Fatal(err)
	}
	defer target.Close()
	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		runtime.UnlockOSThread()
		t.Fatal(err)
	}
	defer func() {
		if err := unix.Setns(int(self.Fd()), unix.CLONE_NEWNET); err != nil {
			// Never hand a thread in the wrong namespace back to the scheduler.
			t.Errorf("restore netns: %v", err)
			return
		}
		runtime.UnlockOSThread()
	}()
	fn()
}

func echoTCP(t *testing.T, ln net.Listener) {
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
}

func echoUDP(t *testing.T, pc net.PacketConn) {
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()
}

func dialIn(t *testing.T, ns, network, addr string, timeout time.Duration) (net.Conn, error) {
	var c net.Conn
	var err error
	inNetns(t, ns, func() { c, err = net.DialTimeout(network, addr, timeout) })
	return c, err
}

func roundTrip(c net.Conn, msg string) error {
	_ = c.SetDeadline(time.Now().Add(1500 * time.Millisecond))
	defer c.SetDeadline(time.Time{})
	if _, err := c.Write([]byte(msg)); err != nil {
		return err
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil {
		return err
	}
	if string(buf) != msg {
		return fmt.Errorf("echo %q, want %q", buf, msg)
	}
	return nil
}

// newUDPWorks opens a NEW UDP socket (= new conntrack flow) and expects an echo.
func newUDPWorks(t *testing.T, ns, addr string) bool {
	c, err := dialIn(t, ns, "udp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return roundTrip(c, "udp-new") == nil
}

func newTCPWorks(t *testing.T, ns, addr string) bool {
	c, err := dialIn(t, ns, "tcp", addr, 1500*time.Millisecond)
	if err != nil {
		return false
	}
	defer c.Close()
	return roundTrip(c, "tcp-new") == nil
}

func nftList(t *testing.T) string {
	out, _ := exec.Command("nft", "list", "table", "inet", machineprotect.TableName).CombinedOutput()
	return string(out)
}

func nftHasTable(t *testing.T) bool {
	out, err := exec.Command("nft", "list", "tables").CombinedOutput()
	if err != nil {
		t.Fatalf("nft list tables: %v %s", err, out)
	}
	return strings.Contains(string(out), "inet "+machineprotect.TableName)
}

func twValue(t *testing.T) int64 {
	b, err := os.ReadFile("/proc/sys/net/ipv4/tcp_max_tw_buckets")
	if err != nil {
		t.Fatal(err)
	}
	v, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return v
}

func waitSnap(t *testing.T, p *machineprotect.Protector, what string, ok func(*machineprotect.Snapshot) bool) *machineprotect.Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := p.Snapshot(); s != nil && ok(s) {
			return s
		}
		time.Sleep(50 * time.Millisecond)
	}
	s := p.Snapshot()
	t.Fatalf("timeout waiting for %s; last %+v %+v", what, s.Conns, s.Protect)
	return nil
}

func evidence(t *testing.T, format string, args ...any) {
	t.Helper()
	line := fmt.Sprintf(format, args...)
	t.Log(line)
	if path := os.Getenv("FLVX_MP_EVIDENCE"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintln(f, line)
			_ = f.Close()
		}
	}
}

func TestMachineProtectionNetnsE2E(t *testing.T) {
	clientNS, targetNS := e2eEnv(t)
	base := t.TempDir()
	runDir, stateDir := filepath.Join(base, "run"), filepath.Join(base, "etc")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	originalTW := twValue(t)

	// Target namespace: echo servers behind the forwards.
	inNetns(t, targetNS, func() {
		for _, port := range []int{30001, 30003} {
			ln, err := net.Listen("tcp", net.JoinHostPort(e2eTargetIP, strconv.Itoa(port)))
			if err != nil {
				t.Fatal(err)
			}
			echoTCP(t, ln)
		}
		for _, port := range []int{30001, 30004} {
			pc, err := net.ListenPacket("udp", net.JoinHostPort(e2eTargetIP, strconv.Itoa(port)))
			if err != nil {
				t.Fatal(err)
			}
			echoUDP(t, pc)
		}
	})

	// Node: real gost forward services (TCP + UDP on 20001) like the panel deploys them.
	if corelogger.Default() == nil {
		corelogger.SetDefault(xlogger.NewLogger(xlogger.LevelOption(corelogger.ErrorLevel), xlogger.OutputOption(io.Discard)))
	}
	svcs := []config.ServiceConfig{
		{Name: "9001_1_1_tcp", Addr: ":20001", Handler: &config.HandlerConfig{Type: "tcp"}, Listener: &config.ListenerConfig{Type: "tcp"},
			Forwarder: &config.ForwarderConfig{Nodes: []*config.ForwardNodeConfig{{Name: "t", Addr: e2eTargetIP + ":30001"}}}},
		{Name: "9001_1_1_udp", Addr: ":20001", Handler: &config.HandlerConfig{Type: "udp"}, Listener: &config.ListenerConfig{Type: "udp"},
			Forwarder: &config.ForwarderConfig{Nodes: []*config.ForwardNodeConfig{{Name: "t", Addr: e2eTargetIP + ":30001"}}}},
	}
	if err := createServices(createServicesRequest{Data: svcs}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, s := range svcs {
			if svc := registry.ServiceRegistry().Get(s.Name); svc != nil {
				registry.ServiceRegistry().Unregister(s.Name)
				_ = svc.Close()
			}
		}
	})
	// Node: real nft DNAT forwards (TCP 20003, UDP 20004) through the agent's manager.
	mgr, err := nftables.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Close)
	if err := mgr.AddRule(9002, 1, 1, 1, "tcp", 20003, e2eTargetIP+":30003", 0); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AddRule(9003, 1, 1, 1, "udp", 20004, e2eTargetIP+":30004", 0); err != nil {
		t.Fatal(err)
	}
	// Node: a port that is not FLVX's.
	other, err := net.Listen("tcp", e2eNodeIP+":20009")
	if err != nil {
		t.Fatal(err)
	}
	echoTCP(t, other)

	w := NewWebSocketReporter("", "")
	defer w.Stop()
	w.setNftablesManager(mgr)
	p := machineprotect.New(machineprotect.Options{RunDir: runDir, StateDir: stateDir, Ports: w.businessPorts,
		HelperActive: func(context.Context) bool { return false }, Interval: 200 * time.Millisecond,
		Logf: func(s string) { evidence(t, "agent log: %s", s) }})
	w.protector = p
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	const capTotal, twCap = 60, 10
	p.Configure(machineprotect.Settings{Enabled: true, MaxTotal: capTotal, TWBuckets: twCap, CloseWaitSeconds: 10})
	s := waitSnap(t, p, "idle protection", func(s *machineprotect.Snapshot) bool {
		return s.Protect.Enabled && s.Protect.Ports == 4 && !s.Protect.Blocking
	})
	evidence(t, "idle: conns=%+v protect ports=%d blocking=%t", *s.Conns, s.Protect.Ports, s.Protect.Blocking)
	if list := nftList(t); !strings.Contains(list, "tcp_ports") || strings.Contains(list, "hook prerouting") {
		t.Fatalf("idle table must hold only sets + counter:\n%s", list)
	}
	if twValue(t) != twCap {
		t.Fatalf("tcp_max_tw_buckets %d, want %d", twValue(t), twCap)
	}
	evidence(t, "tw_buckets: original %d → applied %d (netns)", originalTW, twValue(t))
	portsDoc, err := os.ReadFile(filepath.Join(runDir, machineprotect.BusinessPortsRelPath))
	if err != nil {
		t.Fatal(err)
	}
	evidence(t, "business-ports.json: %s", bytes.TrimSpace(portsDoc))
	if !strings.Contains(string(portsDoc), `"tcp":[20001,20003]`) || !strings.Contains(string(portsDoc), `"udp":[20001,20004]`) {
		t.Fatalf("ports file %s", portsDoc)
	}

	// Existing flows opened before the cap is reached.
	gostTCP, err := dialIn(t, clientNS, "tcp", e2eNodeIP+":20001", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer gostTCP.Close()
	dnatTCP, err := dialIn(t, clientNS, "tcp", e2eNodeIP+":20003", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer dnatTCP.Close()
	gostUDP, err := dialIn(t, clientNS, "udp", e2eNodeIP+":20001", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer gostUDP.Close()
	dnatUDP, err := dialIn(t, clientNS, "udp", e2eNodeIP+":20004", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer dnatUDP.Close()
	for name, c := range map[string]net.Conn{"gost tcp": gostTCP, "dnat tcp": dnatTCP, "gost udp": gostUDP, "dnat udp": dnatUDP} {
		if err := roundTrip(c, "before"); err != nil {
			t.Fatalf("%s before the cap: %v", name, err)
		}
	}

	// Push the machine over the cap: every idle client = 2 sockets on the node.
	var idle []net.Conn
	for i := 0; i < 40; i++ {
		c, err := dialIn(t, clientNS, "tcp", e2eNodeIP+":20001", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		idle = append(idle, c)
	}
	s = waitSnap(t, p, "blocking", func(s *machineprotect.Snapshot) bool { return s.Protect.Blocking })
	evidence(t, "over cap: conns=%+v blocking=%t", *s.Conns, s.Protect.Blocking)
	list := nftList(t)
	evidence(t, "nft while blocking:\n%s", strings.TrimSpace(list))
	// nft prints priority -150 by its standard name "mangle".
	if !strings.Contains(list, "hook prerouting priority mangle") || !strings.Contains(list, `iifname "lo" return`) ||
		!strings.Contains(list, "ct state ! new return") || !strings.Contains(list, `fib daddr type local counter name "dropped" drop`) {
		t.Fatalf("blocking chain:\n%s", list)
	}

	checks := []struct {
		name string
		ok   bool
		want bool
	}{
		{"new TCP to gost port 20001", newTCPWorks(t, clientNS, e2eNodeIP+":20001"), false},
		{"new TCP to nft DNAT port 20003", newTCPWorks(t, clientNS, e2eNodeIP+":20003"), false},
		{"new UDP to gost port 20001", newUDPWorks(t, clientNS, e2eNodeIP+":20001"), false},
		{"new UDP to nft DNAT port 20004", newUDPWorks(t, clientNS, e2eNodeIP+":20004"), false},
		{"existing gost TCP", roundTrip(gostTCP, "during") == nil, true},
		{"existing DNAT TCP", roundTrip(dnatTCP, "during") == nil, true},
		{"existing gost UDP flow", roundTrip(gostUDP, "during") == nil, true},
		{"existing DNAT UDP flow", roundTrip(dnatUDP, "during") == nil, true},
		{"new TCP to non-business port 20009", newTCPWorks(t, clientNS, e2eNodeIP+":20009"), true},
	}
	// Loopback is exempt (gost listens on [::]:20001).
	loop, err := net.DialTimeout("tcp", "127.0.0.1:20001", time.Second)
	checks = append(checks, struct {
		name string
		ok   bool
		want bool
	}{"new loopback TCP 127.0.0.1:20001", err == nil && roundTrip(loop, "lo") == nil, true})
	if loop != nil {
		loop.Close()
	}
	self, err := net.DialTimeout("tcp", e2eNodeIP+":20001", time.Second)
	checks = append(checks, struct {
		name string
		ok   bool
		want bool
	}{"new TCP from the node to its own IP (via lo)", err == nil && roundTrip(self, "self") == nil, true})
	if self != nil {
		self.Close()
	}
	for _, c := range checks {
		evidence(t, "blocking: %-46s works=%t (want %t)", c.name, c.ok, c.want)
		if c.ok != c.want {
			t.Errorf("%s: works=%t, want %t", c.name, c.ok, c.want)
		}
	}
	s = waitSnap(t, p, "drop counter", func(s *machineprotect.Snapshot) bool { return s.Protect.DroppedTotal > 0 })
	evidence(t, "dropped_total=%d", s.Protect.DroppedTotal)

	// Release: close the idle clients; TIME_WAIT stays capped by tw_buckets.
	for _, c := range idle {
		_ = c.Close()
	}
	s = waitSnap(t, p, "release", func(s *machineprotect.Snapshot) bool { return !s.Protect.Blocking })
	evidence(t, "released: conns=%+v (release below %d)", *s.Conns, machineprotect.ReleaseLevel(capTotal))
	if s.Conns.TimeWait > twCap {
		t.Fatalf("TIME_WAIT %d above tw_buckets %d", s.Conns.TimeWait, twCap)
	}
	if strings.Contains(nftList(t), "hook prerouting") {
		t.Fatal("drop chain kept after release")
	}
	if !newTCPWorks(t, clientNS, e2eNodeIP+":20001") || !newTCPWorks(t, clientNS, e2eNodeIP+":20003") ||
		!newUDPWorks(t, clientNS, e2eNodeIP+":20001") || !newUDPWorks(t, clientNS, e2eNodeIP+":20004") {
		t.Fatal("new connections still blocked after release")
	}
	evidence(t, "after release: new TCP/UDP to 20001/20003/20004 work")

	// Disable: table removed, sysctl restored.
	p.Configure(machineprotect.Settings{Enabled: false, MaxTotal: capTotal, TWBuckets: twCap, CloseWaitSeconds: 10})
	waitSnap(t, p, "disabled", func(s *machineprotect.Snapshot) bool { return !s.Protect.Enabled })
	if nftHasTable(t) {
		t.Fatalf("table kept after disable:\n%s", nftList(t))
	}
	if twValue(t) != originalTW {
		t.Fatalf("tw_buckets %d, want original %d", twValue(t), originalTW)
	}
	evidence(t, "disabled: table removed, tw_buckets restored to %d", twValue(t))

	// mmwx-owned machine: a fresh Helper status file appears.
	p.Configure(machineprotect.Settings{Enabled: true, MaxTotal: capTotal, TWBuckets: twCap, CloseWaitSeconds: 10})
	waitSnap(t, p, "re-enabled", func(s *machineprotect.Snapshot) bool {
		return s.Protect.Enabled && s.Protect.Owner == machineprotect.OwnerFLVX && twValue(t) == twCap
	})
	statusPath := filepath.Join(runDir, machineprotect.MmwxStatusRelPath)
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatus := func() {
		doc, _ := json.Marshal(map[string]any{"version": 1, "owner": "mmwx", "updated_at": time.Now().UTC().Format(time.RFC3339),
			"enabled": true, "max_total": 1500, "tw_buckets": 500, "close_wait_seconds": 10, "total": 30, "tcp": 25, "udp": 5, "blocking": false, "dropped_total": 3})
		if err := os.WriteFile(statusPath, doc, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeStatus()
	s = waitSnap(t, p, "mmwx owner", func(s *machineprotect.Snapshot) bool { return s.Protect.Owner == machineprotect.OwnerMmwx })
	if nftHasTable(t) {
		t.Fatalf("FLVX table kept on an mmwx-owned machine:\n%s", nftList(t))
	}
	if twValue(t) != originalTW {
		t.Fatalf("FLVX sysctl kept on an mmwx-owned machine: %d", twValue(t))
	}
	if _, err := os.Stat(filepath.Join(runDir, machineprotect.BusinessPortsRelPath)); err != nil {
		t.Fatal("ports file missing on an mmwx-owned machine")
	}
	mirror, _ := json.Marshal(s.Protect)
	evidence(t, "mmwx-owned: no FLVX table, tw_buckets %d untouched, machine_protect %s", twValue(t), mirror)
	// The Helper later changes the sysctl itself: FLVX must leave it alone.
	if err := os.WriteFile("/proc/sys/net/ipv4/tcp_max_tw_buckets", []byte("777"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		writeStatus()
		time.Sleep(250 * time.Millisecond)
	}
	if twValue(t) != 777 {
		t.Fatalf("FLVX touched the Helper's sysctl: %d", twValue(t))
	}
	_ = os.WriteFile("/proc/sys/net/ipv4/tcp_max_tw_buckets", []byte(strconv.FormatInt(originalTW, 10)), 0o644)

	if n := countGoroutines("machineprotect.(*Protector).Run"); n != 1 {
		t.Fatalf("%d protection loops", n)
	}
	cancel()
}

// A restarted agent (new process = new protector, same state dir) adopts the
// leftover table and restores the saved original when told to disable.
func TestMachineProtectionNetnsRestart(t *testing.T) {
	e2eEnv(t)
	base := t.TempDir()
	runDir, stateDir := filepath.Join(base, "run"), filepath.Join(base, "etc")
	_ = os.MkdirAll(stateDir, 0o755)
	original := twValue(t)
	ports := func() machineprotect.PortSet { return machineprotect.PortSet{TCP: []int{20101}} }
	start := func(s machineprotect.Settings) (*machineprotect.Protector, context.CancelFunc) {
		p := machineprotect.New(machineprotect.Options{RunDir: runDir, StateDir: stateDir, Ports: ports,
			HelperActive: func(context.Context) bool { return false }, Interval: 100 * time.Millisecond, Logf: func(string) {}})
		p.Configure(s)
		ctx, cancel := context.WithCancel(context.Background())
		go p.Run(ctx)
		return p, cancel
	}
	before := runtime.NumGoroutine()
	p1, stop1 := start(machineprotect.Settings{Enabled: true, MaxTotal: 100000, TWBuckets: 321, CloseWaitSeconds: 10})
	waitSnap(t, p1, "first process applied", func(s *machineprotect.Snapshot) bool { return s.Protect.Enabled && twValue(t) == 321 })
	stop1() // agent stops/crashes; table + sysctl stay
	time.Sleep(200 * time.Millisecond)
	if !nftHasTable(t) || twValue(t) != 321 {
		t.Fatal("state not left for the next process")
	}
	p2, stop2 := start(machineprotect.Settings{Enabled: false})
	waitSnap(t, p2, "second process disabled", func(s *machineprotect.Snapshot) bool { return !s.Protect.Enabled })
	time.Sleep(300 * time.Millisecond)
	if nftHasTable(t) || twValue(t) != original {
		t.Fatalf("restart did not clean up: tw=%d nft=%s", twValue(t), nftList(t))
	}
	stop2()
	time.Sleep(300 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines %d → %d after two agent lifetimes", before, after)
	}
	evidence(t, "restart: leftover table adopted and removed, tw_buckets 321 → %d restored", twValue(t))
}
