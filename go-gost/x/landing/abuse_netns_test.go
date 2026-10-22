package landing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAbuseGuardNetnsE2E proves plan 127 with real traffic through the pinned
// Core in three network namespaces (client → landing → target):
//   - default rules: TCP 25 blocked, 443 and 587 pass, a BitTorrent handshake
//     is blocked while other bytes to the same port pass; the 防滥用 preset
//     blocks 465/587/2525/23/445 but not 443.
//   - Reality guard: an unauthenticated probe with a wrong (or no) SNI is
//     closed on the guarded node and relayed to the dest on the unguarded one;
//     a probe with the right SNI still gets the real dest's certificate.
//   - authenticated users: throughput and new-connection first-byte latency,
//     guard on vs off, and (optional) the same under delay + loss.
//
// Needs root, iproute2, python3, openssl, FLVX_LANDING_TEST_BINARY and
// FLVX_LANDING_NETNS_E2E=1. FLVX_ABUSE_E2E_OUT writes the measurements as JSON;
// FLVX_ABUSE_E2E_LOSS=1 adds the delay + loss run.
func TestAbuseGuardNetnsE2E(t *testing.T) {
	if os.Getenv("FLVX_LANDING_NETNS_E2E") != "1" || os.Geteuid() != 0 {
		t.Skip("set FLVX_LANDING_NETNS_E2E=1 and run as root")
	}
	base := t.TempDir()
	dir := filepath.Join(base, "landing")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	core := realCore(t, dir)
	sh := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	nss := []string{"flvxab-c", "flvxab-l", "flvxab-t"}
	cleanup := func() {
		for _, ns := range nss {
			if out, err := exec.Command("ip", "netns", "pids", ns).Output(); err == nil {
				for _, pid := range strings.Fields(string(out)) {
					_ = exec.Command("kill", "-9", pid).Run()
				}
			}
			_ = exec.Command("ip", "netns", "del", ns).Run()
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	for _, ns := range nss {
		sh("ip", "netns", "add", ns)
		sh("ip", "-n", ns, "link", "set", "lo", "up")
	}
	sh("ip", "link", "add", "flvxab-c0", "type", "veth", "peer", "name", "flvxab-l0")
	sh("ip", "link", "set", "flvxab-c0", "netns", "flvxab-c")
	sh("ip", "link", "set", "flvxab-l0", "netns", "flvxab-l")
	sh("ip", "link", "add", "flvxab-l1", "type", "veth", "peer", "name", "flvxab-t0")
	sh("ip", "link", "set", "flvxab-l1", "netns", "flvxab-l")
	sh("ip", "link", "set", "flvxab-t0", "netns", "flvxab-t")
	sh("ip", "-n", "flvxab-c", "addr", "add", "203.0.113.50/24", "dev", "flvxab-c0")
	sh("ip", "-n", "flvxab-l", "addr", "add", "203.0.113.1/24", "dev", "flvxab-l0")
	sh("ip", "-n", "flvxab-l", "addr", "add", "198.51.100.1/24", "dev", "flvxab-l1")
	sh("ip", "-n", "flvxab-t", "addr", "add", "198.51.100.2/24", "dev", "flvxab-t0")
	for _, l := range [][2]string{{"flvxab-c", "flvxab-c0"}, {"flvxab-l", "flvxab-l0"}, {"flvxab-l", "flvxab-l1"}, {"flvxab-t", "flvxab-t0"}} {
		sh("ip", "-n", l[0], "link", "set", l[1], "up")
	}

	// Real dest: TLS 1.3 server for www.flvx.test that logs every SNI it sees.
	sh("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes", "-days", "2",
		"-subj", "/CN=www.flvx.test", "-addext", "subjectAltName=DNS:www.flvx.test",
		"-keyout", filepath.Join(base, "dest.key"), "-out", filepath.Join(base, "dest.crt"))
	targetLog := filepath.Join(base, "target.log")
	target := `import socket, ssl, sys, threading
log = open(sys.argv[1], "a", buffering=1)
base = sys.argv[2]
def serve(port, handler):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(1024)
    while True:
        c, a = s.accept()
        threading.Thread(target=handler, args=(c, a, port), daemon=True).start()
def banner(c, a, port):
    log.write("tcp %d %s\n" % (port, a[0]))
    try:
        c.sendall(("peer=%s port=%d\n" % (a[0], port)).encode())
    except Exception:
        pass
    c.close()
def bt(c, a, port):
    c.settimeout(5)
    try:
        data = c.recv(68)
        kind = "bt" if data[:20] == b"\x13BitTorrent protocol" else "plain"
        log.write("tcp %d %s %s\n" % (port, a[0], kind))
        c.sendall(("got=%s\n" % kind).encode())
    except Exception:
        pass
    c.close()
def bulk(c, a, port):
    c.settimeout(30)
    try:
        n = int(c.recv(32).strip())
        chunk = b"\0" * 65536
        while n > 0:
            k = min(n, len(chunk)); c.sendall(chunk[:k]); n -= k
    except Exception:
        pass
    c.close()
def echo1(c, a, port):
    c.settimeout(10)
    try:
        if c.recv(1):
            c.sendall(b"x")
    except Exception:
        pass
    c.close()
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(base + "/dest.crt", base + "/dest.key")
def sni(sock, name, _):
    log.write("tls sni=%s\n" % name)
ctx.sni_callback = sni
def tls(c, a, port):
    c.settimeout(10)
    try:
        t = ctx.wrap_socket(c, server_side=True)
        t.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
        t.close()
    except Exception:
        c.close()
for p in (25, 443, 465, 587, 2525, 23, 445, 8081):
    threading.Thread(target=serve, args=(p, banner), daemon=True).start()
threading.Thread(target=serve, args=(6881, bt), daemon=True).start()
threading.Thread(target=serve, args=(9000, bulk), daemon=True).start()
threading.Thread(target=serve, args=(9001, echo1), daemon=True).start()
serve(8443, tls)
`
	if err := os.WriteFile(filepath.Join(base, "target.py"), []byte(target), 0644); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command("ip", "netns", "exec", "flvxab-t", "python3", filepath.Join(base, "target.py"), targetLog, base)
	srvOut, _ := os.Create(filepath.Join(base, "target.out"))
	srv.Stdout, srv.Stderr = srvOut, srvOut
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _, _ = srv.Process.Wait() })

	// Landing: SS2022 node 1 (built-in default), Reality node 2 (guarded),
	// Reality node 3 (same dest, unguarded), SS2022 node 4 (+ 防滥用 preset).
	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxab-l "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, true)
	m.execPath = wrapper
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	const uuid = "8a8e3c4d-1f2b-4c3d-9e8f-0a1b2c3d4e5f"
	reality := func(id int64, port, guard int) Node {
		return Node{ID: id, InboundTag: "flvxl-" + strconv.FormatInt(id, 10), Protocol: "vless_reality_vision", Port: port, Enabled: true, GuardPort: guard,
			VLESS: &VLESS{UUID: uuid, Flow: "xtls-rprx-vision", Reality: Reality{Dest: "198.51.100.2:8443", ServerNames: []string{"www.flvx.test"}, PrivateKey: goldenChainPrivate, ShortIDs: []string{"a1b2c3d4"}}}}
	}
	n1, n4 := testNode(t, 1), testNode(t, 4)
	n1.Port, n4.Port = 31001, 31004
	nodes := []Node{n1, reality(2, 31002, 39999), reality(3, 31003, 0), n4}
	all := []string{"flvxl-1", "flvxl-2", "flvxl-3", "flvxl-4"}
	plan := &Egress{Version: 1, Rules: []EgressRule{
		{Name: "默认：屏蔽 SMTP 25", InboundTags: all, Port: "25", Network: "tcp", Outbound: "block", Builtin: builtinAbuse},
		{Name: "默认：屏蔽 BT", InboundTags: all, Protocol: []string{"bittorrent"}, Outbound: "block", Builtin: builtinAbuse},
		{Name: "屏蔽发信端口", InboundTags: []string{"flvxl-4"}, Port: "465,587,2525", Network: "tcp", Outbound: "block"},
		{Name: "屏蔽 Telnet/SMB", InboundTags: []string{"flvxl-4"}, Port: "23,135-139,445", Outbound: "block"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// Idle cost of the guard: first without it, then with it (same Core).
	unguarded := append([]Node(nil), nodes...)
	unguarded[1].GuardPort = 0
	if _, err := m.Apply(ctx, Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: unguarded, Egress: plan}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	idleOff := procStats(m)
	status, err := m.Apply(ctx, Desired{Revision: 2, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: nodes, Egress: plan})
	time.Sleep(3 * time.Second)
	idleOn := procStats(m)
	if err != nil || status.Egress.State != "applied" || status.Egress.Guard != "on" || status.Egress.GuardNodes != 1 {
		t.Fatalf("apply: %v %+v", err, status.Egress)
	}
	listed, _ := m.api(ctx, "lsi")
	if !strings.Contains(string(listed), "flvxg-39999") {
		t.Fatalf("guard inbound missing: %s", listed)
	}

	// Client: the same Core binary as client, one dokodemo inbound per case.
	pubOut, err := exec.Command(core, "x25519", "-i", goldenChainPrivate).Output()
	if err != nil {
		t.Fatal(err)
	}
	pub := ""
	for _, line := range strings.Split(string(pubOut), "\n") {
		if strings.HasPrefix(line, "Password (PublicKey): ") || strings.HasPrefix(line, "Public key: ") {
			pub = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		}
	}
	if pub == "" {
		t.Fatalf("public key: %s", pubOut)
	}
	ss := func(n Node) map[string]any {
		return map[string]any{"protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": "203.0.113.1", "port": n.Port, "method": SS2022Method, "password": n.SS2022.ServerKey + ":" + n.SS2022.UserKey}}}}
	}
	vless := func(port int) map[string]any {
		return map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": "203.0.113.1", "port": port, "users": []any{map[string]any{"id": uuid, "flow": "xtls-rprx-vision", "encryption": "none"}}}}},
			"streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"serverName": "www.flvx.test", "publicKey": pub, "shortId": "a1b2c3d4", "fingerprint": "chrome"}}}
	}
	outs := map[string]map[string]any{"n1": ss(n1), "n4": ss(n4), "g": vless(31002), "u": vless(31003)}
	type route struct {
		name, out string
		port      int
	}
	routes := []route{
		{"n1-25", "n1", 25}, {"n1-443", "n1", 443}, {"n1-587", "n1", 587}, {"n1-465", "n1", 465}, {"n1-bt", "n1", 6881}, {"n1-plain6881", "n1", 6881},
		{"n4-443", "n4", 443}, {"n4-587", "n4", 587}, {"n4-465", "n4", 465}, {"n4-2525", "n4", 2525}, {"n4-23", "n4", 23}, {"n4-445", "n4", 445}, {"n4-25", "n4", 25}, {"n4-bt", "n4", 6881}, {"n4-8081", "n4", 8081},
		{"g-25", "g", 25}, {"g-443", "g", 443}, {"g-bt", "g", 6881}, {"u-443", "u", 443},
		{"g-bulk", "g", 9000}, {"u-bulk", "u", 9000}, {"g-lat", "g", 9001}, {"u-lat", "u", 9001},
	}
	var inbounds, outbounds, rules []any
	for i, r := range routes {
		inbounds = append(inbounds, map[string]any{"tag": "in-" + r.name, "listen": "127.0.0.1", "port": 20001 + i, "protocol": "dokodemo-door", "settings": map[string]any{"address": "198.51.100.2", "port": r.port, "network": "tcp"}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"in-" + r.name}, "outboundTag": "out-" + r.out})
	}
	for name, o := range outs {
		o["tag"] = "out-" + name
		outbounds = append(outbounds, o)
	}
	clientCfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": inbounds, "outbounds": outbounds, "routing": map[string]any{"rules": rules}})
	if err := os.WriteFile(filepath.Join(base, "client.json"), clientCfg, 0600); err != nil {
		t.Fatal(err)
	}
	client := exec.Command("ip", "netns", "exec", "flvxab-c", core, "run", "-config", filepath.Join(base, "client.json"))
	clientOut, _ := os.Create(filepath.Join(base, "client.log"))
	client.Stdout, client.Stderr = clientOut, clientOut
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Process.Kill(); _, _ = client.Process.Wait() })
	portOf := map[string]int{}
	for i, r := range routes {
		portOf[r.name] = 20001 + i
	}

	py := func(name, code string, args ...string) string {
		t.Helper()
		path := filepath.Join(base, name)
		if err := os.WriteFile(path, []byte(code), 0644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("ip", append([]string{"netns", "exec", "flvxab-c", "python3", path}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v %s", name, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	fetch := `import json, socket, sys, time
out = {}
bt = b"\x13BitTorrent protocol" + b"\0" * 8 + b"I" * 20 + b"P" * 20
for item in sys.argv[1:]:
    name, port = item.split("=")
    for attempt in range(60):
        try:
            c = socket.create_connection(("127.0.0.1", int(port)), timeout=5); break
        except OSError:
            time.sleep(0.05)
    else:
        out[name] = "client-unreachable"; continue
    c.settimeout(5)
    try:
        if name.endswith("-bt"):
            c.sendall(bt)
        elif name.endswith("plain6881"):
            c.sendall(b"hello, not a torrent".ljust(68, b"."))
        data = b""
        while not data.endswith(b"\n"):
            chunk = c.recv(256)
            if not chunk:
                break
            data += chunk
        out[name] = data.decode(errors="replace").strip()
    except Exception as e:
        out[name] = ""
    c.close()
print(json.dumps(out))
`
	var fetchArgs []string
	for _, r := range routes {
		if strings.HasSuffix(r.name, "-bulk") || strings.HasSuffix(r.name, "-lat") {
			continue
		}
		fetchArgs = append(fetchArgs, fmt.Sprintf("%s=%d", r.name, portOf[r.name]))
	}
	got := map[string]string{}
	if err := json.Unmarshal([]byte(py("fetch.py", fetch, fetchArgs...)), &got); err != nil {
		t.Fatal(err)
	}
	pass := func(port int) string { return fmt.Sprintf("peer=198.51.100.1 port=%d", port) }
	expect := map[string]string{
		"n1-25": "", "n1-443": pass(443), "n1-587": pass(587), "n1-465": pass(465), "n1-bt": "", "n1-plain6881": "got=plain",
		"n4-443": pass(443), "n4-587": "", "n4-465": "", "n4-2525": "", "n4-23": "", "n4-445": "", "n4-25": "", "n4-bt": "", "n4-8081": pass(8081),
		"g-25": "", "g-443": pass(443), "g-bt": "", "u-443": pass(443),
	}
	for k, want := range expect {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}

	// Unauthenticated probes straight at the Reality ports.
	probe := `import hashlib, json, socket, ssl, sys
out = {}
for item in sys.argv[1:]:
    name, port, sni = item.split("=")
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
    try:
        c = socket.create_connection(("203.0.113.1", int(port)), timeout=5); c.settimeout(5)
        t = ctx.wrap_socket(c, server_hostname=(sni or None))
        der = t.getpeercert(binary_form=True)
        out[name] = "cert:" + hashlib.sha256(der).hexdigest()[:16]
        t.close()
    except Exception as e:
        out[name] = "closed:" + type(e).__name__
print(json.dumps(out))
`
	probes := map[string]string{}
	if err := json.Unmarshal([]byte(py("probe.py", probe, "g-right=31002=www.flvx.test", "g-wrong=31002=www.example.com", "g-nosni=31002=", "u-right=31003=www.flvx.test", "u-wrong=31003=www.example.com")), &probes); err != nil {
		t.Fatal(err)
	}
	certDER, err := exec.Command("openssl", "x509", "-in", filepath.Join(base, "dest.crt"), "-outform", "DER").Output()
	if err != nil {
		t.Fatal(err)
	}
	destCert := "cert:" + sha256Hex(certDER)[:16]
	if probes["g-right"] != destCert || probes["u-right"] != destCert || probes["u-wrong"] != destCert {
		t.Errorf("right SNI / unguarded probes must get the real dest certificate %s: %v", destCert, probes)
	}
	if !strings.HasPrefix(probes["g-wrong"], "closed:") || !strings.HasPrefix(probes["g-nosni"], "closed:") {
		t.Errorf("guarded node relayed a wrong/no-SNI probe: %v", probes)
	}
	time.Sleep(300 * time.Millisecond)
	logRaw, _ := os.ReadFile(targetLog)
	sniSeen := map[string]int{}
	for _, line := range strings.Split(string(logRaw), "\n") {
		if strings.HasPrefix(line, "tls sni=") {
			sniSeen[strings.TrimPrefix(line, "tls sni=")]++
		}
	}
	// Only the unguarded node let www.example.com through to the dest.
	if sniSeen["www.example.com"] != 1 || sniSeen["None"] != 0 {
		t.Errorf("dest saw SNIs %v; want www.example.com exactly once (unguarded probe)", sniSeen)
	}
	if strings.Count(string(logRaw), "tcp 6881 198.51.100.1 bt") != 0 || strings.Count(string(logRaw), "tcp 25 ") != 0 {
		t.Errorf("blocked traffic reached the target:\n%s", logRaw)
	}

	// Authenticated users: guard on vs off.
	measure := `import json, socket, statistics, sys, time
mode, port, n = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
def conn():
    for attempt in range(60):
        try:
            return socket.create_connection(("127.0.0.1", port), timeout=10)
        except OSError:
            time.sleep(0.05)
    raise SystemExit("unreachable")
if mode == "bulk":
    rates = []
    for i in range(n):
        c = conn(); c.settimeout(30)
        size = 256 << 20
        t0 = time.perf_counter(); c.sendall(b"%d\n" % size); got = 0
        while got < size:
            b = c.recv(1 << 20)
            if not b:
                break
            got += len(b)
        dt = time.perf_counter() - t0; c.close()
        rates.append(got / dt / (1 << 20) if got == size else 0.0)
    print(json.dumps({"mib_s": rates}))
else:
    lat, fails = [], 0
    for i in range(n):
        try:
            t0 = time.perf_counter(); c = conn(); c.settimeout(10); c.sendall(b"GET / HTTP/1.1\r\nHost: lat.flvx.test\r\n\r\n")
            b = c.recv(1); dt = time.perf_counter() - t0; c.close()
            if b == b"x":
                lat.append(dt * 1000)
            else:
                fails += 1
        except Exception:
            fails += 1
    lat.sort()
    q = lambda p: round(lat[min(len(lat) - 1, int(len(lat) * p))], 2) if lat else None
    print(json.dumps({"n": n, "fails": fails, "p50_ms": q(0.5), "p90_ms": q(0.9), "p99_ms": q(0.99), "mean_ms": round(statistics.mean(lat), 2) if lat else None}))
`
	if err := os.WriteFile(filepath.Join(base, "measure.py"), []byte(measure), 0644); err != nil {
		t.Fatal(err)
	}
	run := func(mode, name string, n int) map[string]any {
		out, err := exec.Command("ip", "netns", "exec", "flvxab-c", "python3", filepath.Join(base, "measure.py"), mode, strconv.Itoa(portOf[name]), strconv.Itoa(n)).CombinedOutput()
		if err != nil {
			t.Fatalf("measure %s %s: %v %s", mode, name, err, out)
		}
		res := map[string]any{}
		_ = json.Unmarshal(out, &res)
		return res
	}
	coreStats := func() map[string]float64 { return procStats(m) }
	results := map[string]any{"functional": got, "probes": probes, "dest_sni": sniSeen}
	perf := map[string]any{"idle_guard_off": idleOff, "idle_guard_on": idleOn}
	before := coreStats()
	perf["core_before"] = before
	rounds := 2
	if os.Getenv("FLVX_ABUSE_E2E_SKIP_PERF") == "1" {
		rounds = 0
	}
	for round := 1; round <= rounds; round++ {
		for _, side := range []string{"g", "u"} {
			s0 := coreStats()
			lat := run("lat", side+"-lat", 300)
			s1 := coreStats()
			lat["core_cpu_ms_per_conn"] = (s1["cpu_s"] - s0["cpu_s"]) * 1000 / 300
			perf[fmt.Sprintf("lat_%s_r%d", side, round)] = lat
			s0 = coreStats()
			bulk := run("bulk", side+"-bulk", 3)
			s1 = coreStats()
			bulk["core_cpu_s"] = s1["cpu_s"] - s0["cpu_s"]
			bulk["core_rss_mib_after"] = s1["rss_mib"]
			perf[fmt.Sprintf("bulk_%s_r%d", side, round)] = bulk
		}
	}
	for _, k := range []string{"lat_g_r1", "lat_u_r1", "lat_g_r2", "lat_u_r2"} {
		if v, _ := perf[k].(map[string]any); rounds > 0 && (v == nil || v["fails"].(float64) != 0) {
			t.Errorf("%s: authenticated connections failed: %v", k, perf[k])
		}
	}
	if os.Getenv("FLVX_ABUSE_E2E_LOSS") == "1" {
		// Delay each way (default 40 ms, FLVX_ABUSE_E2E_DELAY) between client
		// and landing, 3 % loss on the client's egress: a lost ClientHello
		// segment arrives one TLP/RTO later.
		delay := os.Getenv("FLVX_ABUSE_E2E_DELAY")
		if delay == "" {
			delay = "40ms"
		}
		perf["loss_delay_each_way"] = delay
		sh("ip", "netns", "exec", "flvxab-c", "tc", "qdisc", "add", "dev", "flvxab-c0", "root", "netem", "delay", delay, "loss", "3%")
		sh("ip", "netns", "exec", "flvxab-l", "tc", "qdisc", "add", "dev", "flvxab-l0", "root", "netem", "delay", delay)
		for round := 1; round <= 2; round++ {
			for _, side := range []string{"g", "u"} {
				perf[fmt.Sprintf("loss_lat_%s_r%d", side, round)] = run("lat", side+"-lat", 250)
			}
		}
		sh("ip", "netns", "exec", "flvxab-c", "tc", "qdisc", "del", "dev", "flvxab-c0", "root")
		sh("ip", "netns", "exec", "flvxab-l", "tc", "qdisc", "del", "dev", "flvxab-l0", "root")
	}
	perf["core_after"] = coreStats()
	results["perf"] = perf
	raw, _ := json.MarshalIndent(results, "", "  ")
	if out := os.Getenv("FLVX_ABUSE_E2E_OUT"); out != "" {
		_ = os.WriteFile(out, raw, 0644)
	}
	t.Logf("abuse/guard e2e: %s", raw)

	// Switching the guard off restores the real dest; probes are relayed again.
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Minute)
	defer cancel2()
	nodes[1].GuardPort = 0
	status, err = m.Apply(ctx2, Desired{Revision: 3, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: nodes, Egress: plan})
	if err != nil || status.Egress.Guard != "" {
		t.Fatalf("guard off: %v %+v", err, status.Egress)
	}
	listed, _ = m.api(ctx2, "lsi")
	if strings.Contains(string(listed), "flvxg-") {
		t.Fatalf("guard inbound left behind: %s", listed)
	}
	if err := json.Unmarshal([]byte(py("probe.py", probe, "g-wrong=31002=www.example.com")), &probes); err != nil || probes["g-wrong"] != destCert {
		t.Errorf("unguarded again: %v %v", probes, err)
	}
	if t.Failed() {
		cl, _ := os.ReadFile(filepath.Join(base, "client.log"))
		t.Logf("client log: %s\ntarget log:\n%s", cl, logRaw)
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// procStats reads the Core's RSS, CPU time, threads and open fds.
func procStats(m *Manager) map[string]float64 {
	pid := 0
	if m.proc != nil {
		pid = m.proc.Pid
	}
	statusRaw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	statRaw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	out := map[string]float64{}
	for _, line := range strings.Split(string(statusRaw), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "VmRSS:":
			kb, _ := strconv.ParseFloat(f[1], 64)
			out["rss_mib"] = kb / 1024
		case "Threads:":
			out["threads"], _ = strconv.ParseFloat(f[1], 64)
		}
	}
	if f := strings.Fields(string(statRaw)); len(f) > 15 {
		u, _ := strconv.ParseFloat(f[13], 64)
		s, _ := strconv.ParseFloat(f[14], 64)
		out["cpu_s"] = (u + s) / 100
	}
	if fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid)); err == nil {
		out["fds"] = float64(len(fds))
	}
	return out
}
