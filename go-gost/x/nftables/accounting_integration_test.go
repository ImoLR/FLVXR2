//go:build linux

package nftables

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

// Integration tests with real traffic. Run them through netns_integration.sh, which creates a
// client, a node (where the tests run) and a target namespace.

const (
	nodeIP4   = "10.231.1.1" // node address the client connects to
	targetIP4 = "10.231.2.2"
	nodeIP6   = "fd31:1::1"
	targetIP6 = "fd31:2::2"
)

func requirePeerNamespaces(t *testing.T) (clientNS, targetNS string) {
	t.Helper()
	requireNFTIntegration(t)
	clientNS, targetNS = os.Getenv("FLVXR2_NFT_CLIENT_NS"), os.Getenv("FLVXR2_NFT_TARGET_NS")
	if clientNS == "" || targetNS == "" {
		t.Skip("run through netns_integration.sh (needs client and target namespaces)")
	}
	return clientNS, targetNS
}

func newIntegrationManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager()
	if err != nil {
		t.Fatalf("initialize nftables manager: %v", err)
	}
	if m.acctTable == nil {
		t.Fatal("accounting table was not created")
	}
	t.Cleanup(func() { removeTestTable(t, m) })
	return m
}

// tcpServerPy accepts connections, reads <recv> bytes, answers <send> bytes and waits for the
// client to close.
const tcpServerPy = `
import socket, sys
host, port, recv, send = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])
s = socket.socket(socket.AF_INET6 if ':' in host else socket.AF_INET, socket.SOCK_STREAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind((host, port)); s.listen(16)
print("ready", flush=True)
chunk = b"d" * 65536
while True:
    c, _ = s.accept()
    got = 0
    while got < recv:
        b = c.recv(65536)
        if not b: break
        got += len(b)
    left = send
    while left > 0:
        left -= c.send(chunk[:min(left, 65536)])
    c.shutdown(socket.SHUT_WR)
    while c.recv(65536): pass
    c.close()
`

// tcpClientPy sends <send> bytes, reads <recv> bytes and prints "<received> <seconds>".
const tcpClientPy = `
import socket, sys, time
host, port, send, recv = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])
s = socket.create_connection((host, port), timeout=60)
start = time.time()
chunk = b"u" * 65536
left = send
while left > 0:
    left -= s.send(chunk[:min(left, 65536)])
got = 0
while got < recv:
    b = s.recv(65536)
    if not b: break
    got += len(b)
print(got, time.time() - start)
s.close()
`

// udpServerPy answers every datagram with <reply> bytes.
const udpServerPy = `
import socket, sys
host, port, reply = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
s = socket.socket(socket.AF_INET6 if ':' in host else socket.AF_INET, socket.SOCK_DGRAM)
s.bind((host, port))
print("ready", flush=True)
while True:
    _, peer = s.recvfrom(65536)
    s.sendto(b"r" * reply, peer)
`

// udpClientPy sends <count> datagrams of <size> bytes, waits for each answer and prints
// "<answers> <answer bytes>".
const udpClientPy = `
import socket, sys
host, port, count, size = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])
s = socket.socket(socket.AF_INET6 if ':' in host else socket.AF_INET, socket.SOCK_DGRAM)
s.settimeout(2)
answers = got = 0
for _ in range(count):
    s.sendto(b"q" * size, (host, port))
    try:
        b, _ = s.recvfrom(65536)
        answers += 1
        got += len(b)
    except socket.timeout:
        pass
print(answers, got)
`

// echoServerPy echoes every connection's bytes back.
const echoServerPy = `
import socket, sys, threading
host, port = sys.argv[1], int(sys.argv[2])
s = socket.socket(socket.AF_INET6 if ':' in host else socket.AF_INET, socket.SOCK_STREAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind((host, port)); s.listen(16)
print("ready", flush=True)
def serve(c):
    try:
        while True:
            b = c.recv(65536)
            if not b: break
            c.sendall(b)
    except OSError:
        pass
    c.close()
while True:
    c, _ = s.accept()
    threading.Thread(target=serve, args=(c,), daemon=True).start()
`

// pingClientPy keeps one connection busy for <seconds>: 1000 bytes out and back every 50ms.
// It prints "survived <bytes>" or "broken <seconds>".
const pingClientPy = `
import socket, sys, time
host, port, seconds = sys.argv[1], int(sys.argv[2]), float(sys.argv[3])
s = socket.create_connection((host, port), timeout=5)
s.settimeout(2)
start = time.time(); total = 0
try:
    while time.time() - start < seconds:
        s.sendall(b"p" * 1000)
        got = 0
        while got < 1000:
            b = s.recv(65536)
            if not b: raise OSError("closed")
            got += len(b)
        total += got
        time.sleep(0.05)
    print("survived", total)
except OSError:
    print("broken", time.time() - start)
`

func nsPython(ns, script string, args ...string) *exec.Cmd {
	argv := append([]string{"python3", "-c", script}, args...)
	if ns == "" {
		return exec.Command(argv[0], argv[1:]...)
	}
	return exec.Command("ip", append([]string{"netns", "exec", ns}, argv...)...)
}

func startPyServer(t *testing.T, ns, script string, args ...string) {
	t.Helper()
	cmd := nsPython(ns, script, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err == nil && strings.TrimSpace(line) != "ready" {
			err = fmt.Errorf("unexpected server output %q", line)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("server: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}
}

func runPyClient(t *testing.T, ns, script string, args ...string) []string {
	t.Helper()
	out, err := nsPython(ns, script, args...).Output()
	if err != nil {
		t.Fatalf("client %v: %v (%s)", args, err, out)
	}
	return strings.Fields(string(out))
}

type tcpTransfer struct {
	received int64
	elapsed  time.Duration
}

func runTCPTransfer(t *testing.T, clientNS, host string, port int, send, recv int64) tcpTransfer {
	t.Helper()
	f := runPyClient(t, clientNS, tcpClientPy, host, strconv.Itoa(port), strconv.FormatInt(send, 10), strconv.FormatInt(recv, 10))
	if len(f) != 2 {
		t.Fatalf("client output %q", f)
	}
	got, _ := strconv.ParseInt(f[0], 10, 64)
	secs, _ := strconv.ParseFloat(f[1], 64)
	if got != recv {
		t.Fatalf("client received %d bytes, want %d", got, recv)
	}
	return tcpTransfer{received: got, elapsed: time.Duration(secs * float64(time.Second))}
}

// collectFor sums what CollectTraffic reports for one forward and protocol.
func collectFor(t *testing.T, m *Manager, forwardID int64, protocol string) TrafficDelta {
	t.Helper()
	var sum TrafficDelta
	for _, d := range m.CollectTraffic() {
		if d.ForwardID != forwardID || d.Protocol != protocol {
			continue
		}
		sum.ForwardID, sum.UserID, sum.UserTunnelID, sum.Protocol, sum.Port = d.ForwardID, d.UserID, d.UserTunnelID, d.Protocol, d.Port
		sum.UploadBytes += d.UploadBytes
		sum.DownloadBytes += d.DownloadBytes
	}
	return sum
}

// expectPayloadBytes checks a counted direction against the payload sent that way. The
// counters see IP packets, so they include headers (and the other direction's ACKs).
func expectPayloadBytes(t *testing.T, what string, counted uint64, payload, otherPayload int64) {
	t.Helper()
	slack := uint64(float64(payload+otherPayload)*0.06) + 64*1024
	if counted < uint64(payload) || counted > uint64(payload)+slack {
		t.Fatalf("%s counted %d bytes for %d payload bytes (allowed %d..%d)", what, counted, payload, payload, uint64(payload)+slack)
	}
}

func TestAccountingCountsForwardedTCPBothDirections(t *testing.T) {
	clientNS, targetNS := requirePeerNamespaces(t)
	m := newIntegrationManager(t)

	cases := []struct {
		name         string
		forwardID    int64
		port         int
		node, target string
	}{
		{name: "ipv4", forwardID: 11, port: 31011, node: nodeIP4, target: targetIP4},
		{name: "ipv6", forwardID: 12, port: 31012, node: nodeIP6, target: targetIP6},
	}
	const upload, download = 1_000_000, 3_000_000
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			targetPort := tc.port + 10000
			startPyServer(t, targetNS, tcpServerPy, tc.target, strconv.Itoa(targetPort), strconv.Itoa(upload), strconv.Itoa(download))
			target := fmt.Sprintf("%s:%d", tc.target, targetPort)
			if strings.Contains(tc.target, ":") {
				target = fmt.Sprintf("[%s]:%d", tc.target, targetPort)
			}
			if err := m.AddRule(tc.forwardID, 1, 2, 3, "tcp", tc.port, target, 0); err != nil {
				t.Fatalf("add rule: %v", err)
			}
			runTCPTransfer(t, clientNS, tc.node, tc.port, upload, download)

			d := collectFor(t, m, tc.forwardID, "tcp")
			expectPayloadBytes(t, "upload", d.UploadBytes, upload, download)
			expectPayloadBytes(t, "download", d.DownloadBytes, download, upload)
			if d.UserID != 2 || d.UserTunnelID != 3 || d.Port != tc.port {
				t.Fatalf("delta carries user=%d user_tunnel=%d port=%d", d.UserID, d.UserTunnelID, d.Port)
			}
			if again := collectFor(t, m, tc.forwardID, "tcp"); again.UploadBytes != 0 || again.DownloadBytes != 0 {
				t.Fatalf("second collection reported %+v again", again)
			}
		})
	}
}

func TestAccountingCountsForwardedUDP(t *testing.T) {
	clientNS, targetNS := requirePeerNamespaces(t)
	m := newIntegrationManager(t)

	startPyServer(t, targetNS, udpServerPy, targetIP4, "41021", "1200")
	if err := m.AddRule(21, 1, 2, 3, "udp", 31021, targetIP4+":41021", 0); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	f := runPyClient(t, clientNS, udpClientPy, nodeIP4, "31021", "200", "500")
	answers, _ := strconv.ParseInt(f[0], 10, 64)
	if answers < 190 {
		t.Fatalf("only %d of 200 datagrams answered", answers)
	}
	d := collectFor(t, m, 21, "udp")
	// 28 bytes of IPv4+UDP header per datagram.
	if d.UploadBytes < 200*500 || d.UploadBytes > 200*(500+28) {
		t.Fatalf("udp upload counted %d bytes, want %d..%d", d.UploadBytes, 200*500, 200*(500+28))
	}
	if d.DownloadBytes < uint64(answers)*1200 || d.DownloadBytes > uint64(answers)*(1200+28) {
		t.Fatalf("udp download counted %d bytes for %d answers", d.DownloadBytes, answers)
	}
}

func TestAccountingCountsLocalTarget(t *testing.T) {
	clientNS, _ := requirePeerNamespaces(t)
	m := newIntegrationManager(t)

	// The target listens on the node itself: input and output hooks see the traffic.
	const upload, download = 500_000, 2_000_000
	startPyServer(t, "", tcpServerPy, nodeIP4, "41031", strconv.Itoa(upload), strconv.Itoa(download))
	if err := m.AddRule(31, 1, 2, 3, "tcp", 31031, nodeIP4+":41031", 0); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	runTCPTransfer(t, clientNS, nodeIP4, 31031, upload, download)
	d := collectFor(t, m, 31, "tcp")
	expectPayloadBytes(t, "upload", d.UploadBytes, upload, download)
	expectPayloadBytes(t, "download", d.DownloadBytes, download, upload)
}

func TestAccountingSpeedLimitPolicesBothDirections(t *testing.T) {
	clientNS, targetNS := requirePeerNamespaces(t)
	m := newIntegrationManager(t)

	const size = 3 * 1024 * 1024
	startPyServer(t, targetNS, tcpServerPy, targetIP4, "41041", "1000", strconv.Itoa(size))
	startPyServer(t, targetNS, tcpServerPy, targetIP4, "41042", "1000", strconv.Itoa(size))
	// 8 Mbps = 1 MiB/s per direction, burst 256 KiB.
	if err := m.AddRule(41, 1, 2, 3, "tcp", 31041, targetIP4+":41041", 8); err != nil {
		t.Fatalf("add limited rule: %v", err)
	}
	if err := m.AddRule(42, 1, 2, 3, "tcp", 31042, targetIP4+":41042", 0); err != nil {
		t.Fatalf("add unlimited rule: %v", err)
	}

	unlimited := runTCPTransfer(t, clientNS, nodeIP4, 31042, 1000, size)
	limited := runTCPTransfer(t, clientNS, nodeIP4, 31041, 1000, size)
	t.Logf("3 MiB download: unlimited %v, limited to 8 Mbps %v", unlimited.elapsed, limited.elapsed)
	if limited.elapsed < 1500*time.Millisecond {
		t.Fatalf("limited download took %v, the limit allows at least ~1.75s", limited.elapsed)
	}
	if unlimited.elapsed >= limited.elapsed {
		t.Fatalf("unlimited download (%v) not faster than the limited one (%v)", unlimited.elapsed, limited.elapsed)
	}
	// Dropped packets are not counted: the counted download stays close to the payload.
	d := collectFor(t, m, 41, "tcp")
	expectPayloadBytes(t, "limited download", d.DownloadBytes, size, 1000)
}

func TestAccountingKeepsTrafficOfDeletedRule(t *testing.T) {
	clientNS, targetNS := requirePeerNamespaces(t)
	m := newIntegrationManager(t)

	const download = 1_500_000
	startPyServer(t, targetNS, tcpServerPy, targetIP4, "41051", "1000", strconv.Itoa(download))
	if err := m.AddRule(51, 1, 2, 3, "tcp", 31051, targetIP4+":41051", 0); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	runTCPTransfer(t, clientNS, nodeIP4, 31051, 1000, download)

	// Paused before the reporter collected: the bytes must still be reported.
	if err := m.RemoveForward(51, "tcp", []int{31051}, false); err != nil {
		t.Fatalf("remove forward: %v", err)
	}
	d := collectFor(t, m, 51, "tcp")
	expectPayloadBytes(t, "download of the deleted rule", d.DownloadBytes, download, 1000)
	if again := collectFor(t, m, 51, "tcp"); again.UploadBytes != 0 || again.DownloadBytes != 0 {
		t.Fatalf("deleted rule reported again: %+v", again)
	}
	if n := countTaggedRules(t, m, 51); n != 0 {
		t.Fatalf("%d rules of the deleted forward left", n)
	}
	if chains := accountingChainsOf(t, m, 51); len(chains) != 0 {
		t.Fatalf("accounting chains left: %v", chains)
	}
	elements, err := m.conn.GetSetElements(m.portMaps["tcp"])
	if err != nil {
		t.Fatalf("read port map: %v", err)
	}
	if len(elements) != 0 {
		t.Fatalf("port map still has %d elements", len(elements))
	}
}

func countTaggedRules(t *testing.T, m *Manager, forwardID int64) int {
	t.Helper()
	rules, err := m.conn.GetRules(m.table, &nftables.Chain{Name: PreroutingChain, Table: m.table})
	if err != nil {
		t.Fatalf("read prerouting rules: %v", err)
	}
	n := 0
	for _, rule := range rules {
		if tag, ok := parseRuleTag(rule); ok && tag.ForwardID == forwardID {
			n++
		}
	}
	return n
}

func accountingChainsOf(t *testing.T, m *Manager, forwardID int64) []string {
	t.Helper()
	var names []string
	for _, protocol := range []string{"tcp", "udp"} {
		refs, err := m.forwardAccountingChains(forwardID, protocol)
		if err != nil {
			t.Fatalf("list accounting chains: %v", err)
		}
		for _, r := range refs {
			names = append(names, r.name)
		}
	}
	return names
}

func TestDeleteNeverRemovesAnotherForwardsRules(t *testing.T) {
	clientNS, targetNS := requirePeerNamespaces(t)
	m := newIntegrationManager(t)

	startPyServer(t, targetNS, tcpServerPy, targetIP4, "41062", "1000", "200000")
	if err := m.AddRule(61, 1, 2, 3, "tcp", 31061, targetIP4+":41061", 0); err != nil {
		t.Fatalf("add rule 61: %v", err)
	}
	if err := m.AddRule(62, 1, 2, 3, "tcp", 31062, targetIP4+":41062", 0); err != nil {
		t.Fatalf("add rule 62: %v", err)
	}

	// The panel names a port that now belongs to forward 62: only forward 61 goes.
	if err := m.RemoveForward(61, "tcp", []int{31062}, false); err != nil {
		t.Fatalf("remove forward 61: %v", err)
	}
	if n := countTaggedRules(t, m, 61); n != 0 {
		t.Fatalf("forward 61 rules left: %d", n)
	}
	if n := countTaggedRules(t, m, 62); n != 1 {
		t.Fatalf("forward 62 has %d DNAT rules, want 1", n)
	}
	runTCPTransfer(t, clientNS, nodeIP4, 31062, 1000, 200000)
	if d := collectFor(t, m, 62, "tcp"); d.DownloadBytes < 200000 {
		t.Fatalf("forward 62 no longer counted: %+v", d)
	}

	// A forward that moved to another port is removed by id although the panel sends the
	// new port.
	if err := m.AddRule(61, 1, 2, 3, "tcp", 31063, targetIP4+":41061", 0); err != nil {
		t.Fatalf("re-add rule 61: %v", err)
	}
	if err := m.RemoveForward(61, "tcp", []int{31064}, false); err != nil {
		t.Fatalf("remove moved forward: %v", err)
	}
	if n := countTaggedRules(t, m, 61); n != 0 {
		t.Fatalf("rule of the moved forward left: %d", n)
	}

	// Untagged rules of an older agent are removed by protocol and port.
	legacy := &nftables.Rule{
		Table: m.table,
		Chain: &nftables.Chain{Name: PreroutingChain, Table: m.table},
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{6}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: portBytes(31065)},
			&expr.Counter{},
		},
	}
	m.conn.AddRule(legacy)
	if err := m.conn.Flush(); err != nil {
		t.Fatalf("add legacy rule: %v", err)
	}
	if err := m.RemoveForward(65, "tcp", []int{31065}, false); err != nil {
		t.Fatalf("remove legacy forward: %v", err)
	}
	rules, err := m.conn.GetRules(m.table, &nftables.Chain{Name: PreroutingChain, Table: m.table})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if _, tagged := parseRuleTag(rule); !tagged && matchPortInRule(rule, portBytes(31065)) {
			t.Fatal("untagged legacy rule left")
		}
	}
	if n := countTaggedRules(t, m, 62); n != 1 {
		t.Fatalf("forward 62 lost its rule: %d", n)
	}
}

func TestRemoveForwardTerminatesEstablishedConnectionsOnlyWhenAsked(t *testing.T) {
	clientNS, targetNS := requirePeerNamespaces(t)
	m := newIntegrationManager(t)
	startPyServer(t, targetNS, echoServerPy, targetIP4, "41071")

	runPing := func(port int, seconds string) (*exec.Cmd, *strings.Builder) {
		out := &strings.Builder{}
		cmd := nsPython(clientNS, pingClientPy, nodeIP4, strconv.Itoa(port), seconds)
		cmd.Stdout = out
		if err := cmd.Start(); err != nil {
			t.Fatalf("start ping client: %v", err)
		}
		return cmd, out
	}

	// Re-sync (delete + add without terminate): the established connection keeps running
	// and is counted by the new rule.
	if err := m.AddRule(71, 1, 2, 3, "tcp", 31071, targetIP4+":41071", 0); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	cmd, out := runPing(31071, "3")
	time.Sleep(time.Second)
	if err := m.RemoveForward(71, "tcp", []int{31071}, false); err != nil {
		t.Fatalf("remove for re-sync: %v", err)
	}
	if err := m.AddRule(71, 1, 2, 3, "tcp", 31071, targetIP4+":41071", 0); err != nil {
		t.Fatalf("re-add rule: %v", err)
	}
	_ = collectFor(t, m, 71, "tcp") // traffic before the re-add
	time.Sleep(time.Second)
	if d := collectFor(t, m, 71, "tcp"); d.UploadBytes < 5000 || d.DownloadBytes < 5000 {
		t.Fatalf("established connection not counted after the re-sync: %+v", d)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("ping client: %v", err)
	}
	if !strings.HasPrefix(out.String(), "survived") {
		t.Fatalf("re-sync broke the established connection: %q", out.String())
	}

	// Pause/delete (terminate): the established connection ends.
	cmd, out = runPing(31071, "6")
	time.Sleep(time.Second)
	if err := m.RemoveForward(71, "tcp", []int{31071}, true); err != nil {
		t.Fatalf("remove with terminate: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("ping client: %v", err)
	}
	if !strings.HasPrefix(out.String(), "broken") {
		t.Fatalf("connection of a paused forward kept running: %q", out.String())
	}
}

func TestManagerLeavesForeignChainsAndClearsItsOwnOnRestart(t *testing.T) {
	requireNFTIntegration(t)

	// A WireGuard path chain lives in the same table.
	seed := &nftables.Conn{}
	table := seed.AddTable(&nftables.Table{Name: TableName, Family: TableFamily})
	wg := seed.AddChain(&nftables.Chain{Name: "flvx_wg_path_7", Table: table})
	seed.AddRule(&nftables.Rule{Table: table, Chain: wg, Exprs: []expr.Any{&expr.Counter{}}})
	if err := seed.Flush(); err != nil {
		t.Fatalf("seed wg chain: %v", err)
	}

	m1 := newIntegrationManager(t)
	if err := m1.AddRule(81, 1, 2, 3, "tcp", 31081, targetIP4+":41081", 8); err != nil {
		t.Fatalf("add rule: %v", err)
	}
	if err := m1.RemoveForward(81, "udp", nil, false); err != nil { // nothing there
		t.Fatalf("remove absent rule: %v", err)
	}

	// Agent restart: stale DNAT rules and accounting chains are dropped (the panel re-syncs).
	m2, err := NewManager()
	if err != nil {
		t.Fatalf("restart manager: %v", err)
	}
	if n := countTaggedRules(t, m2, 81); n != 0 {
		t.Fatalf("stale DNAT rules after restart: %d", n)
	}
	if chains := accountingChainsOf(t, m2, 81); len(chains) != 0 {
		t.Fatalf("stale accounting chains after restart: %v", chains)
	}
	if err := m2.AddRule(81, 1, 2, 3, "tcp", 31081, targetIP4+":41081", 0); err != nil {
		t.Fatalf("re-add after restart: %v", err)
	}

	rules, err := m2.conn.GetRules(m2.table, &nftables.Chain{Name: "flvx_wg_path_7", Table: m2.table})
	if err != nil || len(rules) != 1 {
		t.Fatalf("wireguard chain touched: rules=%d err=%v", len(rules), err)
	}
}
