package landing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealityPinNetnsLab (plan 227, lab proof (e)) runs INSIDE the landing
// namespace prepared by runs/reality-domain-pick/work/lab/pin-lab.sh:
//
//	flvxrp-c (client 11.0.9.2) — flvxrp-l (landing 11.0.9.1 / 12.0.9.1, this test) — flvxrp-t (dest "CDN": 12.0.9.10 slow +40 ms, 12.0.9.11 fast)
//
// The pinned Core runs a guarded VLESS Reality node whose dest is the domain
// www.pin.test (resolved by a fake lookup to both CDN IPs). Checks: the guard
// dials the fastest IP; after the pinned IP is made unreachable (nft drop in
// this namespace) the 6-hourly re-check moves the pin to the other IP, only
// the loopback guard inbound is re-added (same Core process and epoch, node
// inbound untouched) and a live VLESS connection survives.
func TestRealityPinNetnsLab(t *testing.T) {
	if os.Getenv("FLVX_REALITY_PIN_LAB") != "1" || os.Geteuid() != 0 {
		t.Skip("run via pin-lab.sh (FLVX_REALITY_PIN_LAB=1, root, inside flvxrp-l)")
	}
	pinned := os.Getenv("FLVX_LANDING_TEST_BINARY")
	if hash, err := fileHash(pinned); err != nil || hash != CoreSHA256["amd64"] {
		t.Fatal("pinned binary checksum mismatch", err)
	}
	base := os.Getenv("FLVX_REALITY_PIN_DIR")
	if base == "" {
		base = t.TempDir()
	}
	dir := filepath.Join(base, "landing")
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(pinned)
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
	var procs []*exec.Cmd
	t.Cleanup(func() {
		for _, p := range procs {
			_ = p.Process.Kill()
			_, _ = p.Process.Wait()
		}
		_ = exec.Command("nft", "delete", "table", "ip", "flvxrplab").Run()
	})
	bg := func(name string, args ...string) *exec.Cmd {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		out, _ := os.Create(filepath.Join(base, name+".log"))
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		procs = append(procs, cmd)
		return cmd
	}
	sh("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes", "-days", "2",
		"-subj", "/CN=www.pin.test", "-addext", "subjectAltName=DNS:www.pin.test", "-keyout", filepath.Join(base, "dest.key"), "-out", filepath.Join(base, "dest.crt"))
	// Dest: TLS on 8443 answers with the local IP it was reached on; 9100
	// echoes (the target of the long-lived VLESS connection).
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
        ip = c.getsockname()[0]
        t = ctx.wrap_socket(c, server_side=True)
        body = ip.encode()
        t.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n" % len(body) + body)
        t.recv(1)
        t.close()
    except Exception:
        c.close()
def echo(c):
    try:
        while True:
            d = c.recv(4096)
            if not d:
                break
            c.sendall(d)
    except Exception:
        pass
    c.close()
threading.Thread(target=serve, args=(9100, echo), daemon=True).start()
serve(8443, tls)
`
	_ = os.WriteFile(filepath.Join(base, "target.py"), []byte(target), 0644)
	bg("target", "ip", "netns", "exec", "flvxrp-t", "python3", filepath.Join(base, "target.py"), base)

	m := NewManager(dir, true)
	m.execPath = core
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	m.pins.net.lookup = func(ctx context.Context, host string) ([]net.IP, error) {
		if host != "www.pin.test" {
			return nil, fmt.Errorf("no such host %s", host)
		}
		return []net.IP{net.ParseIP("12.0.9.10"), net.ParseIP("12.0.9.11")}, nil
	}
	clock := time.Now()
	m.pins.now = func() time.Time { return clock }
	var pinLogs []string
	m.pins.logf = func(format string, args ...any) { pinLogs = append(pinLogs, fmt.Sprintf(format, args...)) }

	const uuid = "8a8e3c4d-1f2b-4c3d-9e8f-0a1b2c3d4e5f"
	node := Node{ID: 2, InboundTag: "flvxl-2", Protocol: "vless_reality_vision", Port: 31002, Enabled: true, GuardPort: 39998,
		VLESS: &VLESS{UUID: uuid, Flow: "xtls-rprx-vision", Reality: Reality{Dest: "www.pin.test:8443", ServerNames: []string{"www.pin.test"}, PrivateKey: goldenChainPrivate, ShortIDs: []string{"a1b2c3d4"}}}}
	desired := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: []Node{node}, Egress: &Egress{Version: 1}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	status, err := m.Apply(ctx, desired)
	if err != nil || status.Egress.Guard != "on" {
		t.Fatalf("apply: %v %+v", err, status.Egress)
	}
	out := map[string]any{}
	guardCfg := func() string { return m.applied.Inbounds[guardTag(39998)] }
	nodeCfg := m.applied.Inbounds["flvxl-2"]
	pid, epoch := m.proc.Pid, status.Epoch
	out["first"] = map[string]any{"pins": status.RealityPins, "guard": guardCfg()}
	if len(status.RealityPins) != 1 || status.RealityPins[0].PinnedIP != "12.0.9.11" || !strings.Contains(guardCfg(), `"address":"12.0.9.11"`) {
		t.Fatalf("first pin: %+v guard=%s", status.RealityPins, guardCfg())
	}

	// Client side: one unauthenticated TLS probe with the node's SNI (Reality
	// hands it to the dest through the guard: the body names the dest IP that
	// served it) and one real VLESS Reality connection held open.
	lab := `import json, socket, ssl, sys, time
if sys.argv[1] == "probe":
    res = []
    for i in range(int(sys.argv[2])):
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
        t0 = time.time()
        try:
            c = socket.create_connection(("11.0.9.1", 31002), timeout=5); c.settimeout(5)
            t = ctx.wrap_socket(c, server_hostname="www.pin.test")
            hs = (time.time() - t0) * 1000
            t.sendall(b"GET / HTTP/1.1\r\nHost: www.pin.test\r\n\r\n")
            data = b""
            while b"\r\n\r\n" not in data or len(data.split(b"\r\n\r\n", 1)[1]) < 9:
                d = t.recv(4096)
                if not d:
                    break
                data += d
            res.append({"ip": data.split(b"\r\n\r\n", 1)[1].decode(), "handshake_ms": round(hs, 1)})
            t.close()
        except Exception as e:
            res.append({"error": str(e)[:80]})
        time.sleep(0.2)
    print(json.dumps(res), flush=True)
else:
    s = socket.create_connection(("127.0.0.1", 21001), timeout=5); s.settimeout(5)
    s.sendall(b"ping1"); first = s.recv(5)
    print(json.dumps({"first": first.decode()}), flush=True)
    sys.stdin.readline()
    try:
        s.sendall(b"ping2"); second = s.recv(5).decode()
    except Exception as e:
        second = "error " + str(e)[:60]
    print(json.dumps({"second": second}), flush=True)
`
	_ = os.WriteFile(filepath.Join(base, "lab.py"), []byte(lab), 0644)
	probe := func(n int) []map[string]any {
		var res []map[string]any
		raw := sh("ip", "netns", "exec", "flvxrp-c", "python3", filepath.Join(base, "lab.py"), "probe", fmt.Sprint(n))
		_ = json.Unmarshal([]byte(lastLine(raw)), &res)
		return res
	}
	before := probe(5)
	out["probe_before"] = before
	for _, r := range before {
		if r["ip"] != "12.0.9.11" {
			t.Fatalf("probe before reached %v", before)
		}
	}
	pub := ""
	for _, line := range strings.Split(sh(core, "x25519", "-i", goldenChainPrivate), "\n") {
		if strings.HasPrefix(line, "Password (PublicKey): ") || strings.HasPrefix(line, "Public key: ") {
			pub = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		}
	}
	clientCfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"tag": "in", "listen": "127.0.0.1", "port": 21001, "protocol": "dokodemo-door", "settings": map[string]any{"address": "12.0.9.11", "port": 9100, "network": "tcp"}}},
		"outbounds": []any{map[string]any{"tag": "out", "protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": "11.0.9.1", "port": node.Port, "users": []any{map[string]any{"id": uuid, "flow": "xtls-rprx-vision", "encryption": "none"}}}}},
			"streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"serverName": "www.pin.test", "publicKey": pub, "shortId": "a1b2c3d4", "fingerprint": "chrome"}}}}})
	_ = os.WriteFile(filepath.Join(base, "client.json"), clientCfg, 0600)
	bg("client", "ip", "netns", "exec", "flvxrp-c", core, "run", "-config", filepath.Join(base, "client.json"))
	time.Sleep(1500 * time.Millisecond)
	held := exec.Command("ip", "netns", "exec", "flvxrp-c", "python3", filepath.Join(base, "lab.py"), "hold")
	stdin, _ := held.StdinPipe()
	stdout, _ := held.StdoutPipe()
	if err := held.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	line, _ := reader.ReadString('\n')
	out["live_conn_before"] = strings.TrimSpace(line)
	if !strings.Contains(line, "ping1") {
		t.Fatalf("live VLESS connection did not start: %q", line)
	}

	// The pinned IP becomes unreachable; 6 h later the re-check moves the pin.
	sh("nft", "add", "table", "ip", "flvxrplab")
	sh("nft", "add", "chain", "ip", "flvxrplab", "out", "{ type filter hook output priority 0 ; }")
	sh("nft", "add", "rule", "ip", "flvxrplab", "out", "ip", "daddr", "12.0.9.11", "tcp", "dport", "8443", "drop")
	clock = clock.Add(realityPinRecheck + time.Minute)
	if moved := m.pins.recheckDue(ctx); !moved {
		t.Fatalf("pin did not move: %+v", m.pins.statuses())
	}
	status, err = m.Apply(ctx, desired) // same revision/content = the next 10 s reconcile tick
	if err != nil {
		t.Fatal(err)
	}
	out["after"] = map[string]any{"pins": status.RealityPins, "guard": guardCfg(), "pin_log": pinLogs}
	if len(status.RealityPins) != 1 || status.RealityPins[0].PinnedIP != "12.0.9.10" || !strings.Contains(guardCfg(), `"address":"12.0.9.10"`) {
		t.Fatalf("after: %+v guard=%s", status.RealityPins, guardCfg())
	}
	if m.proc.Pid != pid || status.Epoch != epoch {
		t.Fatalf("Core restarted: pid %d→%d epoch %s→%s", pid, m.proc.Pid, epoch, status.Epoch)
	}
	if m.applied.Inbounds["flvxl-2"] != nodeCfg {
		t.Fatal("node inbound re-rendered")
	}
	after := probe(5)
	out["probe_after"] = after
	for _, r := range after {
		if r["ip"] != "12.0.9.10" {
			t.Fatalf("probe after reached %v", after)
		}
	}
	_, _ = io.WriteString(stdin, "\n")
	line, _ = reader.ReadString('\n')
	_ = held.Wait()
	out["live_conn_after"] = strings.TrimSpace(line)
	if !strings.Contains(line, "ping2") {
		t.Fatalf("live VLESS connection dropped by the re-pin: %q", line)
	}
	out["core"] = map[string]any{"pid": pid, "epoch": epoch, "same_pid_after": m.proc.Pid == pid}
	b2, _ := json.MarshalIndent(out, "", " ")
	if p := os.Getenv("FLVX_REALITY_PIN_OUT"); p != "" {
		_ = os.WriteFile(p, b2, 0644)
	}
	t.Logf("%s", b2)
}
