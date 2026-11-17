package landing

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestGuardWarmHoldNetnsE2E proves plan 274 on a real kernel with the pinned
// Core (client → landing, RTT 2 × FLVX_GUARD_HOLD_DELAY, default 20 ms each
// way):
//   - silent TCP conns to a guarded Reality inbound (what an entry's warm pool
//     pre-dials for a 端口转发 rule) stay usable for FLVX_GUARD_HOLD_S
//     (default 135 s > the pool's ageMax 110 s); the kernel keeps them as
//     request sockets (SYN-RECV), the Core never accepts them;
//   - after that hold a real Reality client works through every aged conn
//     (0 failures) and its first byte comes one RTT earlier than through a
//     freshly dialed conn (same relay path), while a wrong-SNI probe on an
//     aged conn is still closed by the guard and an allowed-SNI probe still
//     gets the real dest;
//   - the effective hold of one untouched silent conn (TCP_DEFER_ACCEPT 130 s
//     → SYN-ACK retransmit rounding) is measured and must be ≥ 130 s.
//
// Needs root, iproute2, python3, openssl, FLVX_LANDING_TEST_BINARY and
// FLVX_GUARD_HOLD_E2E=1 (≈ 5 min). FLVX_GUARD_HOLD_OUT writes the numbers.
func TestGuardWarmHoldNetnsE2E(t *testing.T) {
	if os.Getenv("FLVX_GUARD_HOLD_E2E") != "1" || os.Geteuid() != 0 {
		t.Skip("set FLVX_GUARD_HOLD_E2E=1 and run as root")
	}
	holdS := 135
	if v, err := strconv.Atoi(os.Getenv("FLVX_GUARD_HOLD_S")); err == nil && v > 0 {
		holdS = v
	}
	delay := os.Getenv("FLVX_GUARD_HOLD_DELAY")
	if delay == "" {
		delay = "20ms"
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
	nss := []string{"flvxgh-c", "flvxgh-l", "flvxgh-t"}
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
	sh("ip", "link", "add", "flvxgh-c0", "type", "veth", "peer", "name", "flvxgh-l0")
	sh("ip", "link", "set", "flvxgh-c0", "netns", "flvxgh-c")
	sh("ip", "link", "set", "flvxgh-l0", "netns", "flvxgh-l")
	sh("ip", "link", "add", "flvxgh-l1", "type", "veth", "peer", "name", "flvxgh-t0")
	sh("ip", "link", "set", "flvxgh-l1", "netns", "flvxgh-l")
	sh("ip", "link", "set", "flvxgh-t0", "netns", "flvxgh-t")
	sh("ip", "-n", "flvxgh-c", "addr", "add", "11.0.8.50/24", "dev", "flvxgh-c0")
	sh("ip", "-n", "flvxgh-l", "addr", "add", "11.0.8.1/24", "dev", "flvxgh-l0")
	sh("ip", "-n", "flvxgh-l", "addr", "add", "12.0.8.1/24", "dev", "flvxgh-l1")
	sh("ip", "-n", "flvxgh-t", "addr", "add", "12.0.8.2/24", "dev", "flvxgh-t0")
	for _, l := range [][2]string{{"flvxgh-c", "flvxgh-c0"}, {"flvxgh-l", "flvxgh-l0"}, {"flvxgh-l", "flvxgh-l1"}, {"flvxgh-t", "flvxgh-t0"}} {
		sh("ip", "-n", l[0], "link", "set", l[1], "up")
	}
	sh("ip", "-n", "flvxgh-t", "route", "add", "default", "via", "12.0.8.1")
	sh("ip", "netns", "exec", "flvxgh-c", "tc", "qdisc", "add", "dev", "flvxgh-c0", "root", "netem", "delay", delay)
	sh("ip", "netns", "exec", "flvxgh-l", "tc", "qdisc", "add", "dev", "flvxgh-l0", "root", "netem", "delay", delay)

	sh("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes", "-days", "2",
		"-subj", "/CN=www.flvx.test", "-addext", "subjectAltName=DNS:www.flvx.test",
		"-keyout", filepath.Join(base, "dest.key"), "-out", filepath.Join(base, "dest.crt"))
	target := `import socket, ssl, sys, threading
base = sys.argv[1]
def serve(port, handler):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(1024)
    while True:
        c, a = s.accept()
        threading.Thread(target=handler, args=(c,), daemon=True).start()
def echo1(c):
    c.settimeout(10)
    try:
        if c.recv(1):
            c.sendall(b"x")
    except Exception:
        pass
    c.close()
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(base + "/dest.crt", base + "/dest.key")
# Like a real site (and lab dest.py): ALPN h2 / http/1.1. A bare OpenSSL 3.5
# server without ALPN or an SNI callback fails REALITY's authenticated path.
ctx.set_alpn_protocols(["h2", "http/1.1"])
def tls(c):
    c.settimeout(10)
    try:
        t = ctx.wrap_socket(c, server_side=True)
        t.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
        t.close()
    except Exception:
        c.close()
threading.Thread(target=serve, args=(9001, echo1), daemon=True).start()
serve(8443, tls)
`
	if err := os.WriteFile(filepath.Join(base, "target.py"), []byte(target), 0644); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command("ip", "netns", "exec", "flvxgh-t", "python3", filepath.Join(base, "target.py"), base)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _, _ = srv.Process.Wait() })

	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nif [ \"$1\" = run ]; then exec ip netns exec flvxgh-l "+core+" \"$@\" >> "+filepath.Join(base, "core.log")+" 2>&1; fi\nexec ip netns exec flvxgh-l "+core+" \"$@\"\n"), 0755); err != nil {
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
	reality := Node{ID: 2, InboundTag: "flvxl-2", Protocol: "vless_reality_vision", Port: 31002, Enabled: true, GuardPort: 39999,
		VLESS: &VLESS{UUID: uuid, Flow: "xtls-rprx-vision", Reality: Reality{Dest: "12.0.8.2:8443", ServerNames: []string{"www.flvx.test"}, PrivateKey: goldenChainPrivate, ShortIDs: []string{"a1b2c3d4"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// The production default rules (plan 127 builtins) like on every server.
	plan := &Egress{Version: 1, Rules: []EgressRule{
		{Name: "默认：屏蔽 SMTP 25", InboundTags: []string{"flvxl-2"}, Port: "25", Network: "tcp", Outbound: "block", Builtin: builtinAbuse},
		{Name: "默认：屏蔽 BT", InboundTags: []string{"flvxl-2"}, Protocol: []string{"bittorrent"}, Outbound: "block", Builtin: builtinAbuse},
	}}
	plain := reality
	plain.ID, plain.InboundTag, plain.Port, plain.GuardPort = 3, "flvxl-3", 31003, 0
	plan.Rules[0].InboundTags = []string{"flvxl-2", "flvxl-3"}
	plan.Rules[1].InboundTags = []string{"flvxl-2", "flvxl-3"}
	status, err := m.Apply(ctx, Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: []Node{reality, plain}, Egress: plan})
	if err != nil || status.Egress.State != "applied" || status.Egress.Guard != "on" || status.Egress.GuardNodes != 1 {
		t.Fatalf("apply: %v %+v", err, status.Egress)
	}
	// The rendered listener really carries the new defer value.
	cfg, _ := os.ReadFile(filepath.Join(dir, "applied.json"))
	if !strings.Contains(string(cfg), `\"value\":\"`+guardDeferAccept+`\"`) && !strings.Contains(string(cfg), `"value":"`+guardDeferAccept+`"`) {
		t.Fatalf("applied inbound lacks TCP_DEFER_ACCEPT %s: %s", guardDeferAccept, cfg)
	}

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
	vlessAt := func(addr string, port int) map[string]any {
		return map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": addr, "port": port, "users": []any{map[string]any{"id": uuid, "flow": "xtls-rprx-vision", "encryption": "none"}}}}},
			"streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"serverName": "www.flvx.test", "publicKey": pub, "shortId": "a1b2c3d4", "fingerprint": "chrome"}}}
	}
	// pool: through aged pre-dialed conns (relay :25001); relay: the same
	// relay, but dialing fresh per conn (:25002) = cold baseline; cold: direct.
	outs := map[string]map[string]any{"pool": vlessAt("127.0.0.1", 25001), "relay": vlessAt("127.0.0.1", 25002), "cold": vlessAt("11.0.8.1", 31002), "plain": vlessAt("11.0.8.1", 31003)}
	var inbounds, outbounds, rules []any
	ports := map[string]int{}
	i := 0
	for name, o := range outs {
		o["tag"] = "out-" + name
		outbounds = append(outbounds, o)
		ports[name] = 20001 + i
		inbounds = append(inbounds, map[string]any{"tag": "in-" + name, "listen": "127.0.0.1", "port": ports[name], "protocol": "dokodemo-door", "settings": map[string]any{"address": "12.0.8.2", "port": 9001, "network": "tcp"}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"in-" + name}, "outboundTag": "out-" + name})
		i++
	}
	clientCfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": inbounds, "outbounds": outbounds, "routing": map[string]any{"rules": rules}})
	if err := os.WriteFile(filepath.Join(base, "client.json"), clientCfg, 0600); err != nil {
		t.Fatal(err)
	}
	client := exec.Command("ip", "netns", "exec", "flvxgh-c", core, "run", "-config", filepath.Join(base, "client.json"))
	clientLog, _ := os.Create(filepath.Join(base, "client.log"))
	client.Stdout, client.Stderr = clientLog, clientLog
	t.Cleanup(func() {
		if t.Failed() {
			cl, _ := os.ReadFile(filepath.Join(base, "client.log"))
			pl, _ := os.ReadFile(filepath.Join(base, "pool.log"))
			co, _ := os.ReadFile(filepath.Join(base, "core.log"))
			t.Logf("client log:\n%s\npool log:\n%s\ncore log:\n%s", cl, pl, co)
		}
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Process.Kill(); _, _ = client.Process.Wait() })

	const pooled = 8
	pool := `import json, socket, ssl, sys, threading, time
LAND = ("11.0.8.1", 31002)
N, HOLD, OUT = int(sys.argv[1]), float(sys.argv[2]), sys.argv[3]
res = {}
def dial():
    s = socket.create_connection(LAND, timeout=5)
    s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    return s
def alive(s):
    s.setblocking(False)
    try:
        return "data" if s.recv(1, socket.MSG_PEEK) else "closed"
    except BlockingIOError:
        return "alive"
    except OSError as e:
        return "error:%s" % e
    finally:
        s.setblocking(True)
t0 = time.time()
pool = [dial() for _ in range(N)]
probes = {"wrong": dial(), "right": dial()}
watch = dial()
def watcher():
    try:
        watch.settimeout(400)
        d = watch.recv(1)
        res["watch"] = {"closed_after_s": round(time.time() - t0, 1), "how": "fin" if d == b"" else "data"}
    except Exception as e:
        res["watch"] = {"closed_after_s": round(time.time() - t0, 1), "how": "error:%s" % e}
    json.dump(res, open(OUT, "w"))
threading.Thread(target=watcher, daemon=True).start()
time.sleep(HOLD)
res["aged_s"] = round(time.time() - t0, 1)
res["alive"] = [alive(s) for s in pool + list(probes.values())]
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
for name, sni in (("wrong", "www.example.com"), ("right", "www.flvx.test")):
    s = probes[name]; s.settimeout(5)
    try:
        t = ctx.wrap_socket(s, server_hostname=sni)
        der = t.getpeercert(binary_form=True)
        res["probe_" + name] = "cert:%d" % len(der)
        t.close()
    except Exception as e:
        res["probe_" + name] = "closed:%s" % type(e).__name__
def pipe(a, b):
    try:
        while True:
            d = a.recv(65536)
            if not d:
                break
            b.sendall(d)
    except Exception:
        pass
    for x in (a, b):
        try: x.shutdown(socket.SHUT_RDWR)
        except Exception: pass
def serve(port, take):
    l = socket.socket(); l.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); l.bind(("127.0.0.1", port)); l.listen(64)
    while True:
        c, _ = l.accept()
        u = take()
        if u is None:
            c.close(); continue
        threading.Thread(target=pipe, args=(c, u), daemon=True).start()
        threading.Thread(target=pipe, args=(u, c), daemon=True).start()
lock = threading.Lock()
def take_pooled():
    with lock:
        return pool.pop(0) if pool else None
threading.Thread(target=serve, args=(25001, take_pooled), daemon=True).start()
threading.Thread(target=serve, args=(25002, dial), daemon=True).start()
res["ready"] = True
json.dump(res, open(OUT, "w"))
while "watch" not in res:
    time.sleep(1)
json.dump(res, open(OUT, "w"))
time.sleep(3600)
`
	poolOut := filepath.Join(base, "pool.json")
	if err := os.WriteFile(filepath.Join(base, "pool.py"), []byte(pool), 0644); err != nil {
		t.Fatal(err)
	}
	relay := exec.Command("ip", "netns", "exec", "flvxgh-c", "python3", filepath.Join(base, "pool.py"), strconv.Itoa(pooled), strconv.Itoa(holdS), poolOut)
	relayLog, _ := os.Create(filepath.Join(base, "pool.log"))
	relay.Stdout, relay.Stderr = relayLog, relayLog
	if err := relay.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Process.Kill(); _, _ = relay.Process.Wait() })
	results := map[string]any{"hold_s": holdS, "delay_each_way": delay, "defer_accept": guardDeferAccept}

	// Mid-hold: the silent conns are request sockets, none accepted by the Core.
	time.Sleep(time.Duration(holdS/2) * time.Second)
	ssOut := func(state string) int {
		out, _ := exec.Command("ip", "netns", "exec", "flvxgh-l", "ss", "-Htn", "state", state, "( sport = :31002 )").Output()
		return len(strings.Fields(strings.TrimSpace(string(out)))) / 4
	}
	results["mid_syn_recv"], results["mid_established"] = ssOut("syn-recv"), ssOut("established")
	if results["mid_syn_recv"].(int) < pooled+3 || results["mid_established"].(int) != 0 {
		t.Errorf("mid-hold sockets: syn-recv %v established %v (want ≥ %d / 0)", results["mid_syn_recv"], results["mid_established"], pooled+3)
	}
	readJSON := func() map[string]any {
		b, _ := os.ReadFile(poolOut)
		out := map[string]any{}
		_ = json.Unmarshal(b, &out)
		return out
	}
	deadline := time.Now().Add(time.Duration(holdS)*time.Second + time.Minute)
	var pr map[string]any
	for time.Now().Before(deadline) {
		if pr = readJSON(); pr["ready"] == true {
			break
		}
		time.Sleep(time.Second)
	}
	if pr["ready"] != true {
		t.Fatalf("pool relay not ready: %v", pr)
	}
	results["pool"] = pr
	for i, a := range pr["alive"].([]any) {
		if a != "alive" {
			t.Errorf("aged conn %d after %v s: %v", i, pr["aged_s"], a)
		}
	}
	if !strings.HasPrefix(pr["probe_wrong"].(string), "closed:") {
		t.Errorf("wrong-SNI probe on an aged conn got through: %v", pr["probe_wrong"])
	}
	if !strings.HasPrefix(pr["probe_right"].(string), "cert:") {
		t.Errorf("allowed-SNI probe on an aged conn did not reach the dest: %v", pr["probe_right"])
	}
	measure := `import json, socket, statistics, sys, time
port, n = int(sys.argv[1]), int(sys.argv[2])
lat, fails, errs = [], 0, []
for i in range(n):
    try:
        t0 = time.perf_counter(); c = socket.create_connection(("127.0.0.1", port), timeout=10); c.settimeout(10); c.sendall(b"y")
        b = c.recv(1); dt = time.perf_counter() - t0; c.close()
        if b == b"x":
            lat.append(dt * 1000)
        else:
            fails += 1; errs.append("got %r after %.0f ms" % (b, dt * 1000))
    except Exception as e:
        fails += 1; errs.append("%s: %s" % (type(e).__name__, e))
    time.sleep(0.2)
lat.sort()
print(json.dumps({"n": n, "fails": fails, "errors": errs[:3], "p50_ms": round(statistics.median(lat), 1) if lat else None, "all_ms": [round(x, 1) for x in lat]}))
`
	if err := os.WriteFile(filepath.Join(base, "measure.py"), []byte(measure), 0644); err != nil {
		t.Fatal(err)
	}
	run := func(name string, n int) map[string]any {
		out, err := exec.Command("ip", "netns", "exec", "flvxgh-c", "python3", filepath.Join(base, "measure.py"), strconv.Itoa(ports[name]), strconv.Itoa(n)).CombinedOutput()
		if err != nil {
			t.Fatalf("measure %s: %v %s", name, err, out)
		}
		res := map[string]any{}
		_ = json.Unmarshal(out, &res)
		return res
	}
	results["lat_unguarded_direct"] = run("plain", 4)
	poolLat := run("pool", pooled)
	relayLat := run("relay", pooled)
	coldLat := run("cold", pooled)
	results["lat_pool"], results["lat_relay_cold"], results["lat_cold_direct"] = poolLat, relayLat, coldLat
	if poolLat["fails"].(float64) != 0 || relayLat["fails"].(float64) != 0 || coldLat["fails"].(float64) != 0 {
		t.Errorf("Reality through aged / fresh conns failed: pool %v relay %v cold %v", poolLat, relayLat, coldLat)
	}
	rtt := 2 * func() float64 { d, _ := time.ParseDuration(delay); return float64(d.Milliseconds()) }()
	if p, r := poolLat["p50_ms"], relayLat["p50_ms"]; p != nil && r != nil {
		saved := r.(float64) - p.(float64)
		results["saved_ms"] = saved
		if saved < rtt*0.6 || saved > rtt*1.6 {
			t.Errorf("pooled first byte saved %.1f ms, want ≈ 1 RTT (%.0f ms)", saved, rtt)
		}
	}
	// Effective hold of the untouched silent conn.
	deadline = time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		if pr = readJSON(); pr["watch"] != nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	results["watch"] = pr["watch"]
	if w, _ := pr["watch"].(map[string]any); w == nil || w["closed_after_s"].(float64) < 130 {
		t.Errorf("silent conn held only %v (want ≥ 130 s)", pr["watch"])
	}
	b, _ := json.MarshalIndent(results, "", "  ")
	t.Logf("guard hold: %s", b)
	if out := os.Getenv("FLVX_GUARD_HOLD_OUT"); out != "" {
		_ = os.WriteFile(out, b, 0644)
	}
}
