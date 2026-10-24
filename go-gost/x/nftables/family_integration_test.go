//go:build linux

package nftables

import (
	"strings"
	"testing"
)

// connectClientPy connects, sends one line and prints "ok" when it comes back,
// "fail <error>" otherwise (exit code 0 either way).
const connectClientPy = `
import socket, sys
host, port = sys.argv[1], int(sys.argv[2])
try:
    s = socket.create_connection((host, port), timeout=3)
    s.sendall(b"plan150\n")
    print("ok" if s.recv(64) == b"plan150\n" else "fail short")
except OSError as e:
    print("fail", type(e).__name__)
`

// Plan 150: an nftables rule DNATs only packets of its target's address family.
// A client that reaches the node over IPv6 is never forwarded to an IPv4 target
// (and vice versa) — the kernel skips the NAT and the port has no listener.
// This is why the panel saves such rules as gost (node 48 「沪台」 is IPv6-only).
func TestDNATNeverCrossesAddressFamilies(t *testing.T) {
	clientNS, targetNS := requirePeerNamespaces(t)
	m := newIntegrationManager(t)

	startPyServer(t, targetNS, echoServerPy, targetIP4, "41150")
	startPyServer(t, targetNS, echoServerPy, targetIP6, "41151")
	if err := m.AddRule(150, 1, 2, 3, "tcp", 31150, targetIP4+":41150", 0); err != nil {
		t.Fatalf("add ipv4-target rule: %v", err)
	}
	if err := m.AddRule(151, 1, 2, 3, "tcp", 31151, "["+targetIP6+"]:41151", 0); err != nil {
		t.Fatalf("add ipv6-target rule: %v", err)
	}
	for _, tc := range []struct {
		name, node, port, want string
	}{
		{"ipv4 client -> ipv4 target", nodeIP4, "31150", "ok"},
		{"ipv6 client -> ipv6 target", nodeIP6, "31151", "ok"},
		{"ipv6 client -> ipv4 target (node 48 case)", nodeIP6, "31150", "fail"},
		{"ipv4 client -> ipv6 target", nodeIP4, "31151", "fail"},
	} {
		got := strings.Join(runPyClient(t, clientNS, connectClientPy, tc.node, tc.port), " ")
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: got %q, want %s", tc.name, got, tc.want)
		}
	}
}
