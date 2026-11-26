package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestVendorBlockNetnsE2E (plan 316) runs the pinned Core as a landing in a
// network namespace and checks 屏蔽本机厂商官网 end to end through an SS2022
// node: the vendor's name is refused whether the client asks for the domain
// or dials the vendor by IP with the vendor's TLS SNI / HTTP Host; other
// names pass; a private target stays blocked whatever SNI is sent; the older
// "abuse" marker matches the requested domain only; switching the vendor off
// lets the SNI case through again. Needs root, FLVX_LANDING_NETNS_E2E=1 and
// FLVX_LANDING_TEST_BINARY (pinned Core). FLVX_VENDOR_E2E_OUT writes JSON.
func TestVendorBlockNetnsE2E(t *testing.T) {
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
	nss := []string{"flvxvb-c", "flvxvb-l", "flvxvb-t"}
	cleanup := func() {
		for _, ns := range nss {
			if out, err := exec.Command("ip", "netns", "pids", ns).Output(); err == nil {
				for _, pid := range strings.Fields(string(out)) {
					_ = exec.Command("kill", "-9", pid).Run()
				}
			}
			_ = exec.Command("ip", "netns", "del", ns).Run()
		}
		_ = os.RemoveAll("/etc/netns/flvxvb-l")
	}
	cleanup()
	t.Cleanup(cleanup)
	for _, ns := range nss {
		sh("ip", "netns", "add", ns)
		sh("ip", "-n", ns, "link", "set", "lo", "up")
	}
	sh("ip", "link", "add", "flvxvb-c0", "type", "veth", "peer", "name", "flvxvb-l0")
	sh("ip", "link", "set", "flvxvb-c0", "netns", "flvxvb-c")
	sh("ip", "link", "set", "flvxvb-l0", "netns", "flvxvb-l")
	sh("ip", "link", "add", "flvxvb-l1", "type", "veth", "peer", "name", "flvxvb-t0")
	sh("ip", "link", "set", "flvxvb-l1", "netns", "flvxvb-l")
	sh("ip", "link", "set", "flvxvb-t0", "netns", "flvxvb-t")
	sh("ip", "-n", "flvxvb-c", "addr", "add", "11.0.1.50/24", "dev", "flvxvb-c0")
	sh("ip", "-n", "flvxvb-l", "addr", "add", "11.0.1.1/24", "dev", "flvxvb-l0")
	sh("ip", "-n", "flvxvb-l", "addr", "add", "12.0.1.1/24", "dev", "flvxvb-l1")
	sh("ip", "-n", "flvxvb-l", "addr", "add", "10.77.0.1/24", "dev", "flvxvb-l1")
	sh("ip", "-n", "flvxvb-t", "addr", "add", "12.0.1.2/24", "dev", "flvxvb-t0")
	// A private address on the target too: must never be reached.
	sh("ip", "-n", "flvxvb-t", "addr", "add", "10.77.0.2/24", "dev", "flvxvb-t0")
	for _, l := range [][2]string{{"flvxvb-c", "flvxvb-c0"}, {"flvxvb-l", "flvxvb-l0"}, {"flvxvb-l", "flvxvb-l1"}, {"flvxvb-t", "flvxvb-t0"}} {
		sh("ip", "-n", l[0], "link", "set", l[1], "up")
	}
	// Name resolution inside the landing namespace (ip netns exec binds
	// /etc/netns/<ns>/hosts over /etc/hosts).
	if err := os.MkdirAll("/etc/netns/flvxvb-l", 0755); err != nil {
		t.Fatal(err)
	}
	hosts := "12.0.1.2 www.dmit.io dmit.io act.dmithost.net allowed.test www.allowed.test\n10.77.0.2 internal.test\n"
	if err := os.WriteFile("/etc/netns/flvxvb-l/hosts", []byte(hosts), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/etc/netns/flvxvb-l/resolv.conf", []byte("nameserver 127.0.0.1\noptions timeout:1 attempts:1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Target: TLS on 8443 (answers "tls-ok sni=<name>"), HTTP on 8080
	// (answers "http-ok host=<Host>"), every hit logged with the local address.
	sh("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes", "-days", "2",
		"-subj", "/CN=target.test", "-keyout", filepath.Join(base, "t.key"), "-out", filepath.Join(base, "t.crt"))
	targetLog := filepath.Join(base, "target.log")
	target := `import socket, ssl, sys, threading
log = open(sys.argv[1], "a", buffering=1)
base = sys.argv[2]
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(base + "/t.crt", base + "/t.key")
names = {}
def sni(sock, name, _):
    names[id(sock)] = name
ctx.sni_callback = sni
def serve(port, handler):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(256)
    while True:
        c, a = s.accept()
        threading.Thread(target=handler, args=(c, a), daemon=True).start()
def tls(c, a):
    c.settimeout(10)
    try:
        t = ctx.wrap_socket(c, server_side=True)
        n = names.get(id(t), "")
        log.write("tls %s sni=%s\n" % (t.getsockname()[0], n))
        t.sendall(("tls-ok sni=%s\n" % n).encode()); t.close()
    except Exception:
        c.close()
def http(c, a):
    c.settimeout(10)
    try:
        data = b""
        while b"\r\n\r\n" not in data:
            chunk = c.recv(1024)
            if not chunk: break
            data += chunk
        host = ""
        for line in data.split(b"\r\n"):
            if line.lower().startswith(b"host:"):
                host = line[5:].strip().decode()
        log.write("http %s host=%s\n" % (c.getsockname()[0], host))
        body = ("http-ok host=%s\n" % host).encode()
        c.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n" % len(body) + body)
    except Exception:
        pass
    c.close()
threading.Thread(target=serve, args=(8080, http), daemon=True).start()
serve(8443, tls)
`
	if err := os.WriteFile(filepath.Join(base, "target.py"), []byte(target), 0644); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command("ip", "netns", "exec", "flvxvb-t", "python3", filepath.Join(base, "target.py"), targetLog, base)
	srvOut, _ := os.Create(filepath.Join(base, "target.out"))
	srv.Stdout, srv.Stderr = srvOut, srvOut
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _, _ = srv.Process.Wait() })

	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxvb-l "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, true)
	m.execPath = wrapper
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	n1 := testNode(t, 1)
	n1.Port = 31001
	all := []string{"flvxl-1"}
	vendor := EgressRule{Name: "内置：屏蔽本机厂商官网（DMIT）", InboundTags: all, Domain: []string{"domain:dmit.io", "domain:dmithost.net"}, Outbound: "block", Builtin: builtinVendor}
	bt := EgressRule{Name: "默认：屏蔽 BT", InboundTags: all, Protocol: []string{"bittorrent"}, Outbound: "block", Builtin: builtinAbuse}
	// A user-style allow rule for the vendor's domain after it (must not help).
	allow := EgressRule{Name: "我的规则：直连 dmit", InboundTags: all, Domain: []string{"domain:dmit.io"}, Outbound: "direct"}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	apply := func(rev int64, rules ...EgressRule) {
		t.Helper()
		st, err := m.Apply(ctx, Desired{Revision: rev, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: []Node{n1}, Egress: &Egress{Version: 1, Rules: rules}})
		if err != nil || st.Egress.State != "applied" {
			t.Fatalf("apply %d: %v %+v", rev, err, st.Egress)
		}
	}

	// Client: the pinned Core as SS2022 client, one dokodemo inbound per
	// target address (no client-side sniffing: the landing sees exactly it).
	type route struct {
		name, addr string
		port       int
	}
	routes := []route{
		{"ip-tls", "12.0.1.2", 8443}, {"ip-http", "12.0.1.2", 8080},
		{"dom-dmit", "www.dmit.io", 8443}, {"dom-host", "act.dmithost.net", 8443}, {"dom-allowed", "allowed.test", 8443},
		{"dom-internal", "internal.test", 8443}, {"ip-internal", "10.77.0.2", 8443},
	}
	var inbounds, rules []any
	for i, r := range routes {
		inbounds = append(inbounds, map[string]any{"tag": "in-" + r.name, "listen": "127.0.0.1", "port": 21001 + i, "protocol": "dokodemo-door", "settings": map[string]any{"address": r.addr, "port": r.port, "network": "tcp"}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"in-" + r.name}, "outboundTag": "ss"})
	}
	clientCfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": inbounds,
		"outbounds": []any{map[string]any{"tag": "ss", "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": "11.0.1.1", "port": n1.Port, "method": SS2022Method, "password": n1.SS2022.ServerKey + ":" + n1.SS2022.UserKey}}}}},
		"routing": map[string]any{"rules": rules}})
	if err := os.WriteFile(filepath.Join(base, "client.json"), clientCfg, 0600); err != nil {
		t.Fatal(err)
	}
	client := exec.Command("ip", "netns", "exec", "flvxvb-c", core, "run", "-config", filepath.Join(base, "client.json"))
	clientOut, _ := os.Create(filepath.Join(base, "client.log"))
	client.Stdout, client.Stderr = clientOut, clientOut
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Process.Kill(); _, _ = client.Process.Wait() })
	portOf := map[string]int{}
	for i, r := range routes {
		portOf[r.name] = 21001 + i
	}
	probe := `import json, socket, ssl, sys, time
out = {}
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
for item in sys.argv[1:]:
    key, port, kind, name = item.split("|")
    t0 = time.time()
    for attempt in range(60):
        try:
            c = socket.create_connection(("127.0.0.1", int(port)), timeout=5); break
        except OSError:
            time.sleep(0.05)
    else:
        out[key] = "client-unreachable"; continue
    c.settimeout(4)
    try:
        if kind == "tls":
            s = ctx.wrap_socket(c, server_hostname=name)
            data = s.recv(256)
        else:
            c.sendall(("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n" % name).encode())
            data = b""
            while True:
                chunk = c.recv(1024)
                if not chunk: break
                data += chunk
            data = data.split(b"\r\n\r\n", 1)[-1]
        out[key] = data.decode(errors="replace").strip() or "blocked"
    except Exception as e:
        out[key] = "blocked"
    out[key + ".ms"] = round((time.time() - t0) * 1000)
    try: c.close()
    except Exception: pass
print(json.dumps(out))
`
	if err := os.WriteFile(filepath.Join(base, "probe.py"), []byte(probe), 0644); err != nil {
		t.Fatal(err)
	}
	type check struct{ key, route, kind, name, want string }
	run := func(phase string, checks []check) map[string]string {
		t.Helper()
		args := []string{"netns", "exec", "flvxvb-c", "python3", filepath.Join(base, "probe.py")}
		for _, c := range checks {
			args = append(args, fmt.Sprintf("%s|%d|%s|%s", c.key, portOf[c.route], c.kind, c.name))
		}
		raw, err := exec.Command("ip", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s probe: %v %s", phase, err, raw)
		}
		got := map[string]string{}
		var any map[string]any
		if err := json.Unmarshal(raw, &any); err != nil {
			t.Fatalf("%s probe output %s", phase, raw)
		}
		for k, v := range any {
			got[k] = fmt.Sprint(v)
		}
		for _, c := range checks {
			if got[c.key] != c.want {
				t.Errorf("%s %s = %q, want %q", phase, c.key, got[c.key], c.want)
			}
		}
		return got
	}
	results := map[string]any{}

	// Phase 1: plan-316 marker — names, SNI and Host all refused.
	apply(1, vendor, bt, allow)
	lsi, _ := m.api(ctx, "lsi")
	results["inbounds_listed"] = strings.Contains(string(lsi), "flvxl-1")
	if !strings.Contains(m.applied.Inbounds["flvxl-1"], `regexp:(?s)^`) {
		t.Fatalf("inbound without vendor sniff override: %s", m.applied.Inbounds["flvxl-1"])
	}
	results["phase1"] = run("vendor", []check{
		{"ip-sni-vendor", "ip-tls", "tls", "www.dmit.io", "blocked"},
		{"ip-sni-vendor-apex", "ip-tls", "tls", "dmit.io", "blocked"},
		{"ip-sni-vendor-2", "ip-tls", "tls", "act.dmithost.net", "blocked"},
		{"ip-sni-other", "ip-tls", "tls", "www.example.com", "tls-ok sni=www.example.com"},
		{"ip-sni-lookalike", "ip-tls", "tls", "notdmit.io", "tls-ok sni=notdmit.io"},
		{"ip-host-vendor", "ip-http", "http", "www.dmit.io", "blocked"},
		{"ip-host-other", "ip-http", "http", "www.example.com", "http-ok host=www.example.com"},
		{"dom-vendor", "dom-dmit", "tls", "www.example.com", "blocked"},
		{"dom-vendor-2", "dom-host", "tls", "act.dmithost.net", "blocked"},
		{"dom-allowed", "dom-allowed", "tls", "allowed.test", "tls-ok sni=allowed.test"},
		{"dom-internal-sni-allowed", "dom-internal", "tls", "allowed.test", "blocked"},
		{"dom-internal-sni-vendor", "dom-internal", "tls", "www.dmit.io", "blocked"},
		{"ip-internal-sni-vendor", "ip-internal", "tls", "www.dmit.io", "blocked"},
	})
	// Phase 2: the older "abuse" marker (agents before plan 316): the
	// requested domain is refused, SNI by IP is not matched.
	old := vendor
	old.Builtin = builtinAbuse
	apply(2, old, bt, allow)
	if strings.Contains(m.applied.Inbounds["flvxl-1"], `regexp:(?s)^`) {
		t.Fatalf("abuse-marked vendor rule kept the sniff override")
	}
	results["phase2"] = run("abuse-marker", []check{
		{"ip-sni-vendor", "ip-tls", "tls", "www.dmit.io", "tls-ok sni=www.dmit.io"},
		{"dom-vendor", "dom-dmit", "tls", "www.example.com", "blocked"},
		{"dom-allowed", "dom-allowed", "tls", "allowed.test", "tls-ok sni=allowed.test"},
		{"dom-internal-sni-vendor", "dom-internal", "tls", "www.dmit.io", "blocked"},
	})
	// Phase 3: back to the vendor marker, then the vendor removed (switch
	// off): two passes (inbound first, routing next) and SNI passes again.
	apply(3, vendor, bt)
	run("vendor-again", []check{{"ip-sni-vendor", "ip-tls", "tls", "www.dmit.io", "blocked"}})
	apply(4, bt)
	if len(m.applied.VendorSniff) != 0 {
		t.Fatalf("override kept after removal: %v", m.applied.VendorSniff)
	}
	if len(m.applied.VendorRules) == 0 {
		t.Fatal("routing dropped the names before the inbound stopped overriding them")
	}
	apply(5, bt)
	if len(m.applied.VendorRules) != 0 {
		t.Fatalf("transition rule left behind: %v", m.applied.VendorRules)
	}
	results["phase3"] = run("removed", []check{
		{"ip-sni-vendor", "ip-tls", "tls", "www.dmit.io", "tls-ok sni=www.dmit.io"},
		{"dom-vendor", "dom-dmit", "tls", "www.dmit.io", "tls-ok sni=www.dmit.io"},
		{"dom-internal-sni-vendor", "dom-internal", "tls", "www.dmit.io", "blocked"},
	})
	logb, _ := os.ReadFile(targetLog)
	if strings.Contains(string(logb), "10.77.0.2") {
		t.Fatalf("private target reached: %s", logb)
	}
	// The vendor's Host never reached the target while blocked (phase 1).
	if strings.Contains(string(logb), "host=www.dmit.io") {
		t.Fatalf("vendor Host reached the target: %s", logb)
	}
	results["target_log"] = strings.Split(strings.TrimSpace(string(logb)), "\n")
	if out := os.Getenv("FLVX_VENDOR_E2E_OUT"); out != "" {
		b, _ := json.MarshalIndent(results, "", "  ")
		_ = os.WriteFile(out, b, 0644)
	}
}
