package landing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGuardHopCountNetnsE2E (plan 148): with the Reality guard on, the guard's
// loopback hop (REALITY → 127.0.0.1 flvxg-<port> → dest) must not count
// against the server-wide connection limit, the way mmwx core-sync-dht
// measured it (guard-counts.json):
//   - 20 unauthenticated probes with the right SNI (relayed to the real dest
//     through the guard): the Core's global current_total right after them;
//   - limit 30 (节点连接数, in+out), 15 authenticated Reality users opened one
//     by one and held (15 × inbound + outbound = 30): all must fit.
//
// The pinned Core (2bf03c9) keeps the hop out of the counts; with
// FLVX_LANDING_OLD_CORE (previous pin b01adef) the old Core runs as a control
// and counts it (mmwx: 37 sockets after 20 probes, 4 of 15 users), proving the
// test sees the difference. Needs root, iproute2, python3,
// FLVX_LANDING_TEST_BINARY and FLVX_LANDING_NETNS_E2E=1; FLVX_GUARD_COUNT_OUT
// writes the numbers as JSON.
func TestGuardHopCountNetnsE2E(t *testing.T) {
	if os.Getenv("FLVX_LANDING_NETNS_E2E") != "1" || os.Geteuid() != 0 {
		t.Skip("set FLVX_LANDING_NETNS_E2E=1 and run as root")
	}
	pinned := os.Getenv("FLVX_LANDING_TEST_BINARY")
	if pinned == "" {
		t.Skip("pinned Core integration binary not supplied")
	}
	if hash, err := fileHash(pinned); err != nil || hash != CoreSHA256["amd64"] {
		t.Fatal("pinned binary checksum mismatch", err)
	}
	type coreCase struct {
		name, path string
		uncounted  bool
	}
	cases := []coreCase{{"pinned", pinned, true}}
	if old := os.Getenv("FLVX_LANDING_OLD_CORE"); old != "" {
		cases = append(cases, coreCase{"old-previous-pin", old, false})
	}
	results := map[string]any{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { results[c.name] = runGuardCountLab(t, c.path, c.uncounted) })
	}
	if out := os.Getenv("FLVX_GUARD_COUNT_OUT"); out != "" {
		b, _ := json.MarshalIndent(results, "", " ")
		_ = os.WriteFile(out, b, 0644)
	}
}

type globalCounts struct {
	CurrentTotal  int64  `json:"current_total"`
	RejectedTotal uint64 `json:"rejected_global_total_limit"`
}

func coreGlobal(t *testing.T, m *Manager) globalCounts {
	t.Helper()
	var s struct {
		Global globalCounts `json:"global"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.control(ctx, http.MethodGet, "/v1/snapshot", nil, &s); err != nil {
		t.Fatal(err)
	}
	return s.Global
}

func runGuardCountLab(t *testing.T, binary string, uncounted bool) map[string]any {
	base := t.TempDir()
	dir := filepath.Join(base, "landing")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	core := filepath.Join(dir, "flvx-landing-core")
	if err := os.WriteFile(core, b, 0755); err != nil {
		t.Fatal(err)
	}
	sum, _ := fileHash(core)
	saved := coreExpected
	coreExpected = func() (string, bool) { return sum, true }
	t.Cleanup(func() { coreExpected = saved })
	sh := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	nss := []string{"flvxgc-c", "flvxgc-l", "flvxgc-t"}
	var procs []*exec.Cmd
	cleanup := func() {
		for _, p := range procs {
			_ = p.Process.Kill()
			_, _ = p.Process.Wait()
		}
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
	sh("ip", "link", "add", "flvxgc-c0", "type", "veth", "peer", "name", "flvxgc-l0")
	sh("ip", "link", "set", "flvxgc-c0", "netns", "flvxgc-c")
	sh("ip", "link", "set", "flvxgc-l0", "netns", "flvxgc-l")
	sh("ip", "link", "add", "flvxgc-l1", "type", "veth", "peer", "name", "flvxgc-t0")
	sh("ip", "link", "set", "flvxgc-l1", "netns", "flvxgc-l")
	sh("ip", "link", "set", "flvxgc-t0", "netns", "flvxgc-t")
	sh("ip", "-n", "flvxgc-c", "addr", "add", "11.0.1.70/24", "dev", "flvxgc-c0")
	sh("ip", "-n", "flvxgc-l", "addr", "add", "11.0.1.1/24", "dev", "flvxgc-l0")
	sh("ip", "-n", "flvxgc-l", "addr", "add", "12.0.1.1/24", "dev", "flvxgc-l1")
	sh("ip", "-n", "flvxgc-t", "addr", "add", "12.0.1.2/24", "dev", "flvxgc-t0")
	for _, l := range [][2]string{{"flvxgc-c", "flvxgc-c0"}, {"flvxgc-l", "flvxgc-l0"}, {"flvxgc-l", "flvxgc-l1"}, {"flvxgc-t", "flvxgc-t0"}} {
		sh("ip", "-n", l[0], "link", "set", l[1], "up")
	}
	bg := func(ns, name string, args ...string) {
		t.Helper()
		cmd := exec.Command("ip", append([]string{"netns", "exec", ns}, args...)...)
		out, _ := os.Create(filepath.Join(base, name+".log"))
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		procs = append(procs, cmd)
	}

	// Target: the Reality dest (TLS 1.3 with ALPN, like a real web site; the
	// new REALITY needs the dest to negotiate ALPN) and a hold server that
	// greets and keeps each connection open until the client closes it.
	sh("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes", "-days", "2",
		"-subj", "/CN=www.flvx.test", "-addext", "subjectAltName=DNS:www.flvx.test", "-keyout", filepath.Join(base, "dest.key"), "-out", filepath.Join(base, "dest.crt"))
	target := `import socket, ssl, sys, threading
base = sys.argv[1]
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER); ctx.load_cert_chain(base + "/dest.crt", base + "/dest.key")
ctx.set_alpn_protocols(["h2", "http/1.1"])
def serve(port, handler):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(1024)
    while True:
        c, a = s.accept()
        threading.Thread(target=handler, args=(c,), daemon=True).start()
def tls(c):
    c.settimeout(10)
    try:
        t = ctx.wrap_socket(c, server_side=True)
        t.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
        t.recv(1)  # keep the connection like a web server until the peer closes
        t.close()
    except Exception:
        c.close()
def hold(c):
    try:
        c.sendall(b"ok\n")
        while c.recv(4096):
            pass
    except Exception:
        pass
    c.close()
threading.Thread(target=serve, args=(9100, hold), daemon=True).start()
serve(8443, tls)
`
	if err := os.WriteFile(filepath.Join(base, "target.py"), []byte(target), 0644); err != nil {
		t.Fatal(err)
	}
	bg("flvxgc-t", "target", "python3", filepath.Join(base, "target.py"), base)

	// Landing: one guarded Reality node, server-wide limit 30 (in+out).
	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxgc-l "+core+" \"$@\"\n"), 0755); err != nil {
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
	node := Node{ID: 2, InboundTag: "flvxl-2", Protocol: "vless_reality_vision", Port: 31002, Enabled: true, GuardPort: 39998,
		VLESS: &VLESS{UUID: uuid, Flow: "xtls-rprx-vision", Reality: Reality{Dest: "12.0.1.2:8443", ServerNames: []string{"www.flvx.test"}, PrivateKey: goldenChainPrivate, ShortIDs: []string{"a1b2c3d4"}}}}
	plan := &Egress{Version: 1, Rules: []EgressRule{
		{Name: "默认：屏蔽 BT", InboundTags: []string{"flvxl-2"}, Protocol: []string{"bittorrent"}, Outbound: "block", Builtin: builtinAbuse},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	status, err := m.Apply(ctx, Desired{Revision: 1, Enabled: true, MaxConnections: 30, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: []Node{node}, Egress: plan})
	if err != nil || status.Egress.State != "applied" || status.Egress.Guard != "on" {
		t.Fatalf("apply: %v %+v", err, status.Egress)
	}
	version := sh(core, "version")
	if i := strings.Index(version, "\n"); i > 0 {
		version = version[:i]
	}

	// Client: the same Core as Reality client behind a dokodemo port.
	pub := ""
	for _, line := range strings.Split(sh(core, "x25519", "-i", goldenChainPrivate), "\n") {
		if strings.HasPrefix(line, "Password (PublicKey): ") || strings.HasPrefix(line, "Public key: ") {
			pub = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		}
	}
	clientCfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"tag": "in", "listen": "127.0.0.1", "port": 21001, "protocol": "dokodemo-door", "settings": map[string]any{"address": "12.0.1.2", "port": 9100, "network": "tcp"}}},
		"outbounds": []any{map[string]any{"tag": "out", "protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": "11.0.1.1", "port": node.Port, "users": []any{map[string]any{"id": uuid, "flow": "xtls-rprx-vision", "encryption": "none"}}}}},
			"streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"serverName": "www.flvx.test", "publicKey": pub, "shortId": "a1b2c3d4", "fingerprint": "chrome"}}}}})
	if err := os.WriteFile(filepath.Join(base, "client.json"), clientCfg, 0600); err != nil {
		t.Fatal(err)
	}
	bg("flvxgc-c", "client", core, "run", "-config", filepath.Join(base, "client.json"))
	time.Sleep(1500 * time.Millisecond)

	lab := `import json, socket, ssl, sys, time
cmd = sys.argv[1]
if cmd == "probes":
    ok = 0
    for i in range(int(sys.argv[2])):
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
        try:
            c = socket.create_connection(("11.0.1.1", 31002), timeout=5); c.settimeout(5)
            t = ctx.wrap_socket(c, server_hostname="www.flvx.test"); t.close(); ok += 1
        except Exception:
            pass
    print(json.dumps({"ok": ok}), flush=True)
else:
    held, results = [], []
    for i in range(int(sys.argv[2])):
        s = socket.create_connection(("127.0.0.1", 21001), timeout=5); s.settimeout(5)
        try:
            good = s.recv(3) == b"ok\n"
        except Exception:
            good = False
        results.append(good)
        (held.append(s) if good else s.close())
        time.sleep(0.3)
    print(json.dumps({"ok": len(held), "results": results}), flush=True)
    sys.stdin.read()
    for s in held:
        s.close()
`
	if err := os.WriteFile(filepath.Join(base, "lab.py"), []byte(lab), 0644); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{"core": version, "expect_uncounted": uncounted}
	failures := []string{}
	check := func(ok bool, format string, args ...any) {
		if !ok {
			failures = append(failures, fmt.Sprintf(format, args...))
		}
	}

	// A. 20 right-SNI probes through the guard to the dest.
	idle := coreGlobal(t, m)
	var probes struct {
		OK int `json:"ok"`
	}
	_ = json.Unmarshal([]byte(lastLine(sh("ip", "netns", "exec", "flvxgc-c", "python3", filepath.Join(base, "lab.py"), "probes", "20"))), &probes)
	time.Sleep(300 * time.Millisecond)
	after03 := coreGlobal(t, m)
	time.Sleep(2700 * time.Millisecond)
	after3 := coreGlobal(t, m)
	out["probes"] = map[string]any{"ok": probes.OK, "idle_total": idle.CurrentTotal, "total_after_0_3s": after03.CurrentTotal, "total_after_3s": after3.CurrentTotal}
	// (On the control Core the counted hops hit the limit: some probes fail.)
	check(!uncounted || probes.OK == 20, "right-SNI probes must reach the dest through the guard: %d/20", probes.OK)

	// B. 15 users under limit 30, opened one by one and held.
	cmd := exec.Command("ip", "netns", "exec", "flvxgc-c", "python3", filepath.Join(base, "lab.py"), "users", "15")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var users struct {
		OK      int    `json:"ok"`
		Results []bool `json:"results"`
	}
	_ = json.Unmarshal([]byte(line), &users)
	time.Sleep(1500 * time.Millisecond)
	held := coreGlobal(t, m)
	_ = stdin.Close()
	_ = cmd.Wait()
	out["users_limit30"] = map[string]any{"ok_of_15": users.OK, "results": users.Results, "held_total": held.CurrentTotal, "rejected_global_total": held.RejectedTotal - after3.RejectedTotal}
	out["core_stats"] = procStats(m)
	if uncounted {
		check(after03.CurrentTotal <= 2, "guard hop counted: global current_total %d right after 20 probes", after03.CurrentTotal)
		check(users.OK == 15 && held.RejectedTotal == after3.RejectedTotal, "users under limit 30: %d/15 accepted, %d rejected by the global limit", users.OK, held.RejectedTotal-after3.RejectedTotal)
	} else {
		check(after03.CurrentTotal >= 10 || users.OK < 15, "control Core shows no guard-hop count (probes %d, users %d/15): test cannot see the difference", after03.CurrentTotal, users.OK)
	}
	b2, _ := json.Marshal(out)
	t.Logf("%s", b2)
	for _, f := range failures {
		t.Error(f)
	}
	return out
}
