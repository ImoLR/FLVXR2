//go:build linux

package nftables

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-gost/x/service"
)

const heldQuotaClientPy = `
import socket, struct, sys
source, dest, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(3)
s.bind((source, 0)); s.connect((dest, port))
s.sendall(b'p')
if s.recv(1) != b'p': raise RuntimeError('no echo')
print('ready', flush=True)
for line in sys.stdin:
    if line.strip() == 'ping':
        s.sendall(b'p')
        print('pong' if s.recv(1) == b'p' else 'broken', flush=True)
    else: break
s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack('ii', 1, 0))
s.close()
`

const probeQuotaClientPy = `
import socket, struct, sys
source, dest, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.settimeout(1.5)
s.bind((source, 0))
try:
    s.connect((dest, port)); s.sendall(b'p')
    print('accepted' if s.recv(1) == b'p' else 'blocked')
except (OSError, socket.timeout): print('blocked')
finally:
    s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack('ii', 1, 0))
    s.close()
`

const probeQuotaUDPPy = `
import socket, sys
source, dest, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(1)
s.bind((source, 0))
try:
    s.sendto(b'p', (dest, port))
    print('accepted' if s.recv(1) == b'r' else 'blocked')
except (OSError, socket.timeout): print('blocked')
finally: s.close()
`

type heldQuotaConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	closed bool
}

func holdQuotaConnection(t *testing.T, ns, source string, port int) *heldQuotaConn {
	t.Helper()
	cmd := nsPython(ns, heldQuotaClientPy, source, nodeIP4, fmt.Sprint(port))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &heldQuotaConn{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}
	t.Cleanup(func() { h.close() })
	line, err := h.stdout.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("hold connection: %q, %v", line, err)
	}
	return h
}

func (h *heldQuotaConn) ping(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(h.stdin, "ping\n"); err != nil {
		t.Fatal(err)
	}
	line, err := h.stdout.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "pong" {
		t.Fatalf("existing connection stopped: %q, %v", line, err)
	}
}

func (h *heldQuotaConn) close() {
	if h.closed {
		return
	}
	h.closed = true
	_, _ = io.WriteString(h.stdin, "close\n")
	_ = h.stdin.Close()
	_ = h.cmd.Wait()
}

func probeQuotaConnection(t *testing.T, ns, source string, port int) bool {
	t.Helper()
	out, err := nsPython(ns, probeQuotaClientPy, source, nodeIP4, fmt.Sprint(port)).CombinedOutput()
	if err != nil {
		t.Fatalf("quota probe: %v (%s)", err, out)
	}
	result := strings.TrimSpace(string(out))
	if result != "accepted" && result != "blocked" {
		t.Fatalf("quota probe result %q", result)
	}
	return result == "accepted"
}

func probeQuotaUDP(t *testing.T, ns, source string, port int) bool {
	t.Helper()
	out, err := nsPython(ns, probeQuotaUDPPy, source, nodeIP4, fmt.Sprint(port)).CombinedOutput()
	if err != nil {
		t.Fatalf("UDP quota probe: %v (%s)", err, out)
	}
	result := strings.TrimSpace(string(out))
	if result != "accepted" && result != "blocked" {
		t.Fatalf("UDP quota probe result %q", result)
	}
	return result == "accepted"
}

func TestNftQuotaRealConntrackGates(t *testing.T) {
	clientNS, targetNS := requirePeerNamespaces(t)
	m := newIntegrationManager(t)
	const firstIP, secondIP = "10.231.1.2", "10.231.1.3"

	t.Run("unlimited forwarding has no quota overhead", func(t *testing.T) {
		if err := m.AddRule(200, 1, 7, 1, "tcp", 31020, targetIP4+":41020", 0); err != nil {
			t.Fatal(err)
		}
		if m.quotaTable != nil || m.quotaPollStop != nil || len(m.quotaForwards) != 0 {
			t.Fatal("unlimited forward installed quota rules or polling")
		}
		if err := m.RemoveForward(200, "tcp", nil, false); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("rule connection cap", func(t *testing.T) {
		startPyServer(t, targetNS, echoServerPy, targetIP4, "41021")
		if err := m.AddRule(201, 1, 7, 1, "tcp", 31021, targetIP4+":41021", 0, RuleQuota{MaxConnections: 2}); err != nil {
			t.Fatal(err)
		}
		defer m.RemoveForward(201, "tcp", nil, false)
		a := holdQuotaConnection(t, clientNS, firstIP, 31021)
		b := holdQuotaConnection(t, clientNS, firstIP, 31021)
		if probeQuotaConnection(t, clientNS, firstIP, 31021) {
			t.Fatal("third connection passed a rule cap of two")
		}
		a.ping(t)
		b.ping(t)
		a.close()
		// The denied SYN has a short conntrack lifetime and the closed flow uses RST.
		time.Sleep(3 * time.Second)
		if !probeQuotaConnection(t, clientNS, firstIP, 31021) {
			t.Fatal("connection slot did not return after close")
		}
	})

	t.Run("rule IP cap", func(t *testing.T) {
		startPyServer(t, targetNS, echoServerPy, targetIP4, "41022")
		if err := m.AddRule(202, 1, 7, 1, "tcp", 31022, targetIP4+":41022", 0, RuleQuota{MaxClientIPs: 1}); err != nil {
			t.Fatal(err)
		}
		defer m.RemoveForward(202, "tcp", nil, false)
		a := holdQuotaConnection(t, clientNS, firstIP, 31022)
		if err := m.ReconcileQuota(); err != nil {
			t.Fatal(err)
		}
		if probeQuotaConnection(t, clientNS, secondIP, 31022) {
			t.Fatal("second source IP passed rule IP cap")
		}
		if !probeQuotaConnection(t, clientNS, firstIP, 31022) {
			t.Fatal("same active IP was rejected")
		}
		a.ping(t)
	})

	t.Run("UDP rule IP cap", func(t *testing.T) {
		startPyServer(t, targetNS, udpServerPy, targetIP4, "41025", "1")
		if err := m.AddRule(205, 1, 7, 1, "udp", 31025, targetIP4+":41025", 0, RuleQuota{MaxClientIPs: 1}); err != nil {
			t.Fatal(err)
		}
		defer m.RemoveForward(205, "udp", nil, false)
		if !probeQuotaUDP(t, clientNS, firstIP, 31025) {
			t.Fatal("first UDP source IP was rejected")
		}
		if err := m.ReconcileQuota(); err != nil {
			t.Fatal(err)
		}
		if probeQuotaUDP(t, clientNS, secondIP, 31025) {
			t.Fatal("second UDP source IP passed rule cap")
		}
		if !probeQuotaUDP(t, clientNS, firstIP, 31025) {
			t.Fatal("active UDP source IP was rejected")
		}
	})

	t.Run("rule connection cap shared by TCP and UDP", func(t *testing.T) {
		startPyServer(t, targetNS, echoServerPy, targetIP4, "41026")
		startPyServer(t, targetNS, udpServerPy, targetIP4, "41026", "1")
		quota := RuleQuota{MaxConnections: 1}
		if err := m.AddRule(206, 1, 7, 1, "tcp", 31026, targetIP4+":41026", 0, quota); err != nil {
			t.Fatal(err)
		}
		defer m.RemoveForward(206, "tcp", nil, false)
		if err := m.AddRule(206, 1, 7, 1, "udp", 31026, targetIP4+":41026", 0, quota); err != nil {
			t.Fatal(err)
		}
		defer m.RemoveForward(206, "udp", nil, false)
		a := holdQuotaConnection(t, clientNS, firstIP, 31026)
		if probeQuotaUDP(t, clientNS, firstIP, 31026) {
			t.Fatal("UDP passed the TCP-held per-forward connection cap")
		}
		a.ping(t)
	})

	t.Run("user pool across nft forwards", func(t *testing.T) {
		for _, port := range []int{41023, 41024} {
			startPyServer(t, targetNS, echoServerPy, targetIP4, fmt.Sprint(port))
		}
		quota := RuleQuota{Group: "user-707", GroupMaxConnections: 2, GroupMaxClientIPs: -1}
		if err := m.AddRule(203, 1, 707, 1, "tcp", 31023, targetIP4+":41023", 0, quota); err != nil {
			t.Fatal(err)
		}
		defer m.RemoveForward(203, "tcp", nil, false)
		if err := m.AddRule(204, 1, 707, 1, "tcp", 31024, targetIP4+":41024", 0, quota); err != nil {
			t.Fatal(err)
		}
		defer m.RemoveForward(204, "tcp", nil, false)
		a := holdQuotaConnection(t, clientNS, firstIP, 31023)
		b := holdQuotaConnection(t, clientNS, secondIP, 31024)
		if err := m.ReconcileQuota(); err != nil {
			t.Fatal(err)
		}
		if probeQuotaConnection(t, clientNS, firstIP, 31023) || probeQuotaConnection(t, clientNS, secondIP, 31024) {
			t.Fatal("user pool admitted a third connection")
		}
		var found bool
		for _, usage := range service.QuotaGroupUsages() {
			if usage.Group == quota.Group {
				found = true
				if usage.Connections != 2 || len(usage.ClientIPs) != 2 {
					t.Fatalf("reported nft pool usage: %+v", usage)
				}
			}
		}
		if !found {
			t.Fatal("nft-only user pool was not reported")
		}
		a.ping(t)
		b.ping(t)
		a.close()
		if err := m.ReconcileQuota(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		if err := m.ReconcileQuota(); err != nil {
			t.Fatal(err)
		}
		if !probeQuotaConnection(t, clientNS, firstIP, 31023) {
			t.Fatal("pool slot did not return after close")
		}
		if err := service.SetQuotaGroupBudgets([]service.QuotaGroupBudget{{Group: quota.Group, MaxConnections: 0, MaxClientIPs: -1}}); err != nil {
			t.Fatal(err)
		}
		if err := m.ReconcileQuota(); err != nil {
			t.Fatal(err)
		}
		if probeQuotaConnection(t, clientNS, firstIP, 31023) {
			t.Fatal("zero budget admitted a connection")
		}
		b.ping(t)
		if err := service.SetQuotaGroupBudgets([]service.QuotaGroupBudget{{Group: quota.Group, MaxConnections: -1, MaxClientIPs: -1}}); err != nil {
			t.Fatal(err)
		}
		if err := m.ReconcileQuota(); err != nil {
			t.Fatal(err)
		}
		if !probeQuotaConnection(t, clientNS, firstIP, 31023) {
			t.Fatal("unlimited budget still blocked a connection")
		}
	})
}
