package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestEgressNetnsDataPlane proves the guarantees with real traffic through the
// pinned Core, isolated in three network namespaces (client → landing → target):
//   - 强制 IPv4: IPv4 literal and A records work; IPv6 literals, AAAA-only names
//     and IPv6 datagrams inside an established UDP session never leave.
//   - 强制 IPv6: the mirror image.
//   - 禁止大陆直连: a mainland source is blocked, an exempt FLVX-node source
//     passes and keeps following later rules, a non-mainland source passes.
//
// Needs root, iproute2, python3, FLVX_LANDING_TEST_BINARY, FLVX_LANDING_GEODIR
// and FLVX_LANDING_NETNS_E2E=1. Nothing outside the namespaces is touched apart
// from /etc/netns/flvxeg-l (hosts file for the landing namespace), removed on exit.
func TestEgressNetnsDataPlane(t *testing.T) {
	if os.Getenv("FLVX_LANDING_NETNS_E2E") != "1" || os.Geteuid() != 0 {
		t.Skip("set FLVX_LANDING_NETNS_E2E=1 and run as root")
	}
	base := t.TempDir()
	dir := filepath.Join(base, "landing")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	core := realCore(t, dir)
	if !copyGeo(t, dir) {
		t.Skip("FLVX_LANDING_GEODIR not supplied")
	}
	sh := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	cleanup := func() {
		for _, ns := range []string{"flvxeg-c", "flvxeg-l", "flvxeg-t"} {
			_ = exec.Command("ip", "netns", "pids", ns).Run()
			if out, err := exec.Command("ip", "netns", "pids", ns).Output(); err == nil {
				for _, pid := range strings.Fields(string(out)) {
					_ = exec.Command("kill", "-9", pid).Run()
				}
			}
			_ = exec.Command("ip", "netns", "del", ns).Run()
		}
		_ = os.RemoveAll("/etc/netns/flvxeg-l")
	}
	cleanup()
	t.Cleanup(cleanup)
	for _, ns := range []string{"flvxeg-c", "flvxeg-l", "flvxeg-t"} {
		sh("ip", "netns", "add", ns)
		sh("ip", "-n", ns, "link", "set", "lo", "up")
	}
	sh("ip", "link", "add", "flvxeg-c0", "type", "veth", "peer", "name", "flvxeg-l0")
	sh("ip", "link", "set", "flvxeg-c0", "netns", "flvxeg-c")
	sh("ip", "link", "set", "flvxeg-l0", "netns", "flvxeg-l")
	sh("ip", "link", "add", "flvxeg-l1", "type", "veth", "peer", "name", "flvxeg-t0")
	sh("ip", "link", "set", "flvxeg-l1", "netns", "flvxeg-l")
	sh("ip", "link", "set", "flvxeg-t0", "netns", "flvxeg-t")
	for _, a := range []string{"1.0.1.2/24", "1.0.1.3/24", "203.0.113.50/24"} {
		sh("ip", "-n", "flvxeg-c", "addr", "add", a, "dev", "flvxeg-c0")
	}
	for _, a := range []string{"1.0.1.1/24", "203.0.113.1/24"} {
		sh("ip", "-n", "flvxeg-l", "addr", "add", a, "dev", "flvxeg-l0")
	}
	sh("ip", "-n", "flvxeg-l", "addr", "add", "198.51.100.1/24", "dev", "flvxeg-l1")
	sh("ip", "-n", "flvxeg-l", "addr", "add", "2001:db8:2::1/64", "dev", "flvxeg-l1", "nodad")
	sh("ip", "-n", "flvxeg-t", "addr", "add", "198.51.100.2/24", "dev", "flvxeg-t0")
	sh("ip", "-n", "flvxeg-t", "addr", "add", "2001:db8:2::2/64", "dev", "flvxeg-t0", "nodad")
	for _, l := range [][2]string{{"flvxeg-c", "flvxeg-c0"}, {"flvxeg-l", "flvxeg-l0"}, {"flvxeg-l", "flvxeg-l1"}, {"flvxeg-t", "flvxeg-t0"}} {
		sh("ip", "-n", l[0], "link", "set", l[1], "up")
	}
	if err := os.MkdirAll("/etc/netns/flvxeg-l", 0755); err != nil {
		t.Fatal(err)
	}
	hosts := "127.0.0.1 localhost\n2001:db8:2::2 v6only.flvx.test\n198.51.100.2 v4only.flvx.test\n198.51.100.2 both.flvx.test\n2001:db8:2::2 both.flvx.test\n"
	if err := os.WriteFile("/etc/netns/flvxeg-l/hosts", []byte(hosts), 0644); err != nil {
		t.Fatal(err)
	}

	// Target: dual-stack TCP (8080, 8081) and UDP echo (9000) servers that log
	// every peer they see.
	targetLog := filepath.Join(base, "target.log")
	server := `import socket, sys, threading
log = open(sys.argv[1], "a", buffering=1)
def tcp(port):
    s = socket.socket(socket.AF_INET6, socket.SOCK_STREAM)
    s.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("::", port)); s.listen(512)
    while True:
        c, a = s.accept()
        peer = a[0][7:] if a[0].startswith("::ffff:") else a[0]
        log.write("tcp %d %s\n" % (port, peer))
        try:
            c.sendall(("peer=%s\n" % peer).encode())
        except Exception:
            pass
        c.close()
def udp():
    s = socket.socket(socket.AF_INET6, socket.SOCK_DGRAM)
    s.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
    s.bind(("::", 9000))
    while True:
        d, a = s.recvfrom(2048)
        peer = a[0][7:] if a[0].startswith("::ffff:") else a[0]
        log.write("udp %s %s\n" % (peer, d.decode(errors="replace")))
        s.sendto(b"echo:" + d, a)
for p in (8080, 8081):
    threading.Thread(target=tcp, args=(p,), daemon=True).start()
udp()
`
	if err := os.WriteFile(filepath.Join(base, "server.py"), []byte(server), 0644); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command("ip", "netns", "exec", "flvxeg-t", "python3", filepath.Join(base, "server.py"), targetLog)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _, _ = srv.Process.Wait() })

	// Landing: the Manager drives the pinned Core inside flvxeg-l.
	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxeg-l "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, true)
	m.execPath = wrapper
	m.detectLocal = func() localAddrs {
		return localAddrs{v4: netip.MustParseAddr("198.51.100.1"), v6: netip.MustParseAddr("2001:db8:2::1")}
	}
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	nodes := []Node{testNode(t, 1), testNode(t, 2), testNode(t, 3), testNode(t, 4)}
	for i := range nodes {
		nodes[i].Port = 31001 + i
	}
	plan := &Egress{Version: 1, Outbounds: []EgressOutbound{
		{Tag: "flvxo-f4", Type: "direct", IPVersion: "force_ipv4"},
		{Tag: "flvxo-f6", Type: "direct", IPVersion: "force_ipv6"},
	}, Rules: []EgressRule{
		{InboundTags: []string{"flvxl-1"}, Outbound: "flvxo-f4"},
		{InboundTags: []string{"flvxl-2"}, Outbound: "flvxo-f6"},
		// 屏蔽中国大陆 c) 禁止大陆直连, then an ordinary later rule.
		{InboundTags: []string{"flvxl-3"}, SourceIP: []string{"geoip:cn"}, Outbound: "block"},
		{InboundTags: []string{"flvxl-3"}, Port: "8081", Outbound: "block"},
	}, ExemptSources: []string{"1.0.1.3"}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	status, err := m.Apply(ctx, Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, Nodes: nodes, Egress: plan})
	if err != nil || status.Egress.State != "applied" {
		t.Fatalf("apply: %v %+v", err, status.Egress)
	}

	// Client: one Core in flvxeg-c; each dokodemo inbound (unix socket, reachable
	// from the test process) is pinned to one target and one landing node/source.
	ss := func(n Node) string { return n.SS2022.ServerKey + ":" + n.SS2022.UserKey }
	type route struct{ name, target, node, server, source string }
	routes := []route{
		{"f4-v4lit", "198.51.100.2", "1", "203.0.113.1", "203.0.113.50"},
		{"f4-v6lit", "2001:db8:2::2", "1", "203.0.113.1", "203.0.113.50"},
		{"f4-v6only", "v6only.flvx.test", "1", "203.0.113.1", "203.0.113.50"},
		{"f4-both", "both.flvx.test", "1", "203.0.113.1", "203.0.113.50"},
		{"f6-v6lit", "2001:db8:2::2", "2", "203.0.113.1", "203.0.113.50"},
		{"f6-v4lit", "198.51.100.2", "2", "203.0.113.1", "203.0.113.50"},
		{"f6-v4only", "v4only.flvx.test", "2", "203.0.113.1", "203.0.113.50"},
		{"f6-both", "both.flvx.test", "2", "203.0.113.1", "203.0.113.50"},
		{"sys-v6lit", "2001:db8:2::2", "4", "203.0.113.1", "203.0.113.50"},
		{"sys-v4lit", "198.51.100.2", "4", "203.0.113.1", "203.0.113.50"},
		{"cn-blocked", "198.51.100.2", "3", "1.0.1.1", "1.0.1.2"},
		{"cn-exempt", "198.51.100.2", "3", "1.0.1.1", "1.0.1.3"},
		{"cn-exempt-8081", "198.51.100.2:8081", "3", "1.0.1.1", "1.0.1.3"},
		{"noncn", "198.51.100.2", "3", "203.0.113.1", "203.0.113.50"},
	}
	var inbounds, outbounds, rules []any
	for _, r := range routes {
		host, port := r.target, 8080
		if h, p, err := net.SplitHostPort(r.target); err == nil {
			host = h
			port, _ = strconv.Atoi(p)
		}
		idx, _ := strconv.Atoi(r.node)
		n := nodes[idx-1]
		inbounds = append(inbounds, map[string]any{"tag": "in-" + r.name, "listen": "127.0.0.1", "port": 20001 + len(inbounds), "protocol": "dokodemo-door", "settings": map[string]any{"address": host, "port": port, "network": "tcp"}})
		outbounds = append(outbounds, map[string]any{"tag": "out-" + r.name, "protocol": "shadowsocks", "sendThrough": r.source, "settings": map[string]any{"servers": []any{map[string]any{"address": r.server, "port": n.Port, "method": SS2022Method, "password": ss(n)}}}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"in-" + r.name}, "outboundTag": "out-" + r.name})
	}
	// SOCKS inbounds for the UDP session tests.
	for i, node := range []string{"1", "2", "4"} {
		idx, _ := strconv.Atoi(node)
		n := nodes[idx-1]
		tag := "udp-" + node
		inbounds = append(inbounds, map[string]any{"tag": "in-" + tag, "listen": "127.0.0.1", "port": 1081 + i, "protocol": "socks", "settings": map[string]any{"udp": true, "auth": "noauth"}})
		outbounds = append(outbounds, map[string]any{"tag": "out-" + tag, "protocol": "shadowsocks", "sendThrough": "203.0.113.50", "settings": map[string]any{"servers": []any{map[string]any{"address": "203.0.113.1", "port": n.Port, "method": SS2022Method, "password": ss(n)}}}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"in-" + tag}, "outboundTag": "out-" + tag})
	}
	clientCfg := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": inbounds, "outbounds": outbounds, "routing": map[string]any{"rules": rules}}
	b, _ := json.Marshal(clientCfg)
	if err := os.WriteFile(filepath.Join(base, "client.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	client := exec.Command("ip", "netns", "exec", "flvxeg-c", core, "run", "-config", filepath.Join(base, "client.json"))
	clientOut, _ := os.Create(filepath.Join(base, "client.log"))
	client.Stdout, client.Stderr = clientOut, clientOut
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Process.Kill(); _, _ = client.Process.Wait() })
	// TCP client inside flvxeg-c: each dokodemo port maps to one route.
	fetcher := `import json, socket, sys, time
out = {}
for item in sys.argv[1:]:
    name, port = item.split("=")
    line = ""
    for attempt in range(40):
        try:
            c = socket.create_connection(("127.0.0.1", int(port)), timeout=5)
            break
        except OSError:
            time.sleep(0.05)
    else:
        out[name] = "client-unreachable"; continue
    try:
        c.settimeout(5)
        data = b""
        while not data.endswith(b"\n"):
            chunk = c.recv(256)
            if not chunk:
                break
            data += chunk
        line = data.decode().strip()
    except Exception:
        pass
    c.close()
    out[name] = line
print(json.dumps(out))
`
	if err := os.WriteFile(filepath.Join(base, "fetch.py"), []byte(fetcher), 0644); err != nil {
		t.Fatal(err)
	}
	fetchArgs := []string{"netns", "exec", "flvxeg-c", "python3", filepath.Join(base, "fetch.py")}
	for i, r := range routes {
		fetchArgs = append(fetchArgs, fmt.Sprintf("%s=%d", r.name, 20001+i))
	}
	fetchOut, err := exec.Command("ip", fetchArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("tcp client: %v %s", err, fetchOut)
	}
	fetched := map[string]string{}
	if err := json.Unmarshal(fetchOut, &fetched); err != nil {
		cl, _ := os.ReadFile(filepath.Join(base, "client.log"))
		t.Fatalf("tcp client output %s: %v\n%s", fetchOut, err, cl)
	}
	fetch := func(name string) string { return fetched[name] }
	expect := map[string]string{
		"f4-v4lit": "peer=198.51.100.1", "f4-v6lit": "", "f4-v6only": "", "f4-both": "peer=198.51.100.1",
		"f6-v6lit": "peer=2001:db8:2::1", "f6-v4lit": "", "f6-v4only": "", "f6-both": "peer=2001:db8:2::1",
		"sys-v6lit": "peer=2001:db8:2::1", "sys-v4lit": "peer=198.51.100.1",
		"cn-blocked": "", "cn-exempt": "peer=198.51.100.1", "cn-exempt-8081": "", "noncn": "peer=198.51.100.1",
	}
	results := map[string]string{}
	for _, r := range routes {
		got := fetch(r.name)
		results[r.name] = got
		if got != expect[r.name] {
			t.Errorf("%s: got %q want %q", r.name, got, expect[r.name])
		}
	}

	// UDP: one SOCKS5 association, first an allowed datagram, then one of the
	// forbidden family inside the same session (full-cone path, no re-routing).
	udpClient := `import socket, struct, sys
port = int(sys.argv[1]); order = sys.argv[2:]
t = socket.create_connection(("127.0.0.1", port), timeout=3)
t.sendall(b"\x05\x01\x00"); t.recv(2)
t.sendall(b"\x05\x03\x00\x01\x00\x00\x00\x00\x00\x00"); r = t.recv(10)
relay = (socket.inet_ntoa(r[4:8]), struct.unpack(">H", r[8:10])[0])
u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); u.settimeout(1.5)
out = []
for target in order:
    if ":" in target:
        hdr = b"\x00\x00\x00\x04" + socket.inet_pton(socket.AF_INET6, target)
    else:
        hdr = b"\x00\x00\x00\x01" + socket.inet_aton(target)
    u.sendto(hdr + struct.pack(">H", 9000) + ("p%d" % port).encode(), relay)
    try:
        u.recvfrom(2048); out.append("ok")
    except Exception:
        out.append("none")
print(",".join(out))
`
	if err := os.WriteFile(filepath.Join(base, "udp.py"), []byte(udpClient), 0644); err != nil {
		t.Fatal(err)
	}
	udp := func(port int, order ...string) string {
		args := append([]string{"netns", "exec", "flvxeg-c", "python3", filepath.Join(base, "udp.py"), strconv.Itoa(port)}, order...)
		out, err := exec.Command("ip", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("udp client: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := udp(1081, "198.51.100.2", "2001:db8:2::2"); !strings.HasPrefix(got, "ok,") || strings.HasSuffix(got, ",ok") {
		t.Errorf("force_ipv4 UDP session: %s", got)
	}
	if got := udp(1082, "2001:db8:2::2", "198.51.100.2"); !strings.HasPrefix(got, "ok,") || strings.HasSuffix(got, ",ok") {
		t.Errorf("force_ipv6 UDP session: %s", got)
	}
	if got := udp(1083, "198.51.100.2", "2001:db8:2::2"); got != "ok,ok" {
		t.Errorf("system UDP session (control): %s", got)
	}

	// The target log is the ground truth: the forced outbounds never produced a
	// connection or datagram of the other family.
	time.Sleep(300 * time.Millisecond)
	logRaw, _ := os.ReadFile(targetLog)
	var v4From, v6From int
	udpPeers := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(logRaw)), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		if f[0] == "tcp" {
			if strings.Contains(f[2], ":") {
				v6From++
			} else {
				v4From++
			}
		}
		if f[0] == "udp" && len(f) >= 3 {
			udpPeers[f[1]+" "+f[2]]++
		}
	}
	// v6 TCP: f6-v6lit, f6-both, sys-v6lit. v4 TCP: f4-v4lit, f4-both, sys-v4lit, cn-exempt, noncn.
	if v6From != 3 || v4From != 5 {
		t.Errorf("target saw %d IPv4 and %d IPv6 TCP connections; want 5 and 3\n%s", v4From, v6From, logRaw)
	}
	if udpPeers["2001:db8:2::1 p1081"] != 0 || udpPeers["198.51.100.1 p1082"] != 0 {
		t.Errorf("forbidden-family datagrams reached the target: %v", udpPeers)
	}
	if udpPeers["198.51.100.1 p1081"] != 1 || udpPeers["2001:db8:2::1 p1082"] != 1 || udpPeers["198.51.100.1 p1083"] != 1 || udpPeers["2001:db8:2::1 p1083"] != 1 {
		t.Errorf("allowed datagrams missing: %v", udpPeers)
	}
	raw, _ := json.MarshalIndent(map[string]any{"tcp": results, "udp_peers": udpPeers, "target_v4_tcp": v4From, "target_v6_tcp": v6From}, "", "  ")
	if out := os.Getenv("FLVX_LANDING_E2E_OUT"); out != "" {
		_ = os.WriteFile(out, raw, 0644)
	}
	t.Logf("netns data plane: %s", raw)
	if t.Failed() {
		cl, _ := os.ReadFile(filepath.Join(base, "client.log"))
		t.Logf("client log: %s", cl)
		fmt.Println(string(logRaw))
	}
}
