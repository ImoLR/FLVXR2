package landing

import (
	"context"
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

// TestUTPNetnsE2E (plan 129): BitTorrent over UDP (uTP) through the FLVX
// native landing with the panel's default 「默认：屏蔽 BT」 rule, real traffic in
// three network namespaces (client → landing → target), for SS2022 and VLESS
// Reality:
//   - a real libtorrent leecher (uTP only, via SOCKS5 UDP) downloading from a
//     real libtorrent seeder; real captured uTP SYNs of libtorrent 2.0.11 /
//     Transmission 4.1 / 2.94 replayed to the seeder;
//   - must keep working: real dig (DNS), a real kernel WireGuard handshake +
//     data, QUIC (curl HTTP/3 Initial), STUN and NTP first packets, plain UDP.
//
// The pinned Core (FLVX_LANDING_TEST_BINARY) must block uTP; with
// FLVX_LANDING_OLD_CORE (the v1.4.59 asset) the old Core runs as a control
// and must let it through (proving the test sees the difference).
// Needs root, iproute2, python3 + libtorrent, dig, wg, openssl and
// FLVX_LANDING_NETNS_E2E=1; FLVX_UTP_E2E_OUT writes the results as JSON.
func TestUTPNetnsE2E(t *testing.T) {
	if os.Getenv("FLVX_LANDING_NETNS_E2E") != "1" || os.Geteuid() != 0 {
		t.Skip("set FLVX_LANDING_NETNS_E2E=1 and run as root")
	}
	if err := exec.Command("python3", "-c", "import libtorrent").Run(); err != nil {
		t.Skip("python3 libtorrent not installed")
	}
	for _, tool := range []string{"dig", "wg", "openssl", "ip"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	type coreCase struct {
		name, path string
		block      bool
	}
	pinned := os.Getenv("FLVX_LANDING_TEST_BINARY")
	if pinned == "" {
		t.Skip("pinned Core integration binary not supplied")
	}
	if hash, err := fileHash(pinned); err != nil || hash != CoreSHA256["amd64"] {
		t.Fatal("pinned binary checksum mismatch", err)
	}
	cases := []coreCase{{"pinned", pinned, true}}
	if old := os.Getenv("FLVX_LANDING_OLD_CORE"); old != "" {
		cases = append(cases, coreCase{"old-v1.4.59", old, false})
	}
	results := map[string]any{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			results[c.name] = runUTPLab(t, c.path, c.block)
		})
	}
	if out := os.Getenv("FLVX_UTP_E2E_OUT"); out != "" {
		b, _ := json.MarshalIndent(results, "", " ")
		_ = os.WriteFile(out, b, 0644)
	}
}

// Real first datagrams (captured 2026-10-23 by the mmwx utp-core-fix lab,
// Xray-core-mmwx common/protocol/bittorrent/testdata/udp_first_packets.txt).
var utpRealSYNs = map[string]string{
	"libtorrent-2.0.11":       "410071a58f8f5926000000000000000070530000",
	"transmission-4.1.0-beta": "4100456729bbb39c000000000004000023c60000",
	"transmission-2.94":       "4102228d3ba121a20000000000040000d00d000000080000000000000000",
}

var udpRealOthers = map[string]string{
	"quic-initial-curl": "cf000000010800984294af4f6eb60000449ec4f4123a21788e3bc0563b91aba10f91cdbdaaedfae33fb3955a9a36d310ee",
	"stun-binding":      "000100002112a4425a5283fab7cb9b6ada58a1e8",
	"ntp-client":        "23000000000000000000000000000000000000000000000000000000000000000000000000000000c1d3c191bf0aa951",
	"wireguard-init":    "01000000f4f3d70014983f92f01b55aa496ffc9fbc2a48e108785c5636b50e8f63a4d8beca045029d75ebf86da",
	"plain":             "13" + strings.Repeat("00", 47),
}

const utpLabPy = `import json, os, socket, struct, sys, threading, time
UTP = {0: "ST_DATA", 1: "ST_FIN", 2: "ST_STATE", 3: "ST_RESET", 4: "ST_SYN"}
def lt_settings(listen, socks=None):
    import libtorrent as lt
    s = {"listen_interfaces": listen, "enable_dht": False, "enable_lsd": False, "enable_upnp": False, "enable_natpmp": False,
         "enable_outgoing_tcp": False, "enable_incoming_tcp": False, "enable_outgoing_utp": True, "enable_incoming_utp": True,
         "allow_multiple_connections_per_ip": True, "user_agent": "lab", "peer_connect_timeout": 5, "handshake_timeout": 5,
         "alert_mask": lt.alert.category_t.error_notification | lt.alert.category_t.peer_notification}
    if socks:
        host, port = socks.rsplit(":", 1)
        s.update({"proxy_type": lt.proxy_type_t.socks5, "proxy_hostname": host, "proxy_port": int(port), "proxy_peer_connections": True})
    return s
def add(ses, torrent, directory, seed):
    import libtorrent as lt
    atp = lt.add_torrent_params(); atp.ti = lt.torrent_info(torrent); atp.save_path = directory
    if seed:
        atp.flags |= lt.torrent_flags.seed_mode
    return ses.add_torrent(atp)
def socks_assoc(port):
    c = socket.create_connection(("127.0.0.1", port), timeout=5)
    c.sendall(b"\x05\x01\x00"); assert c.recv(2) == b"\x05\x00"
    c.sendall(b"\x05\x03\x00\x01\x00\x00\x00\x00\x00\x00"); r = c.recv(10); assert r[1] == 0, r
    relay = (socket.inet_ntoa(r[4:8]), struct.unpack(">H", r[8:10])[0])
    return c, (("127.0.0.1", relay[1]) if relay[0] == "0.0.0.0" else relay)
cmd = sys.argv[1]
if cmd == "make":
    import libtorrent as lt
    fs = lt.file_storage(); lt.add_files(fs, sys.argv[2])
    tor = lt.create_torrent(fs, 256 * 1024, lt.create_torrent.v1_only)
    lt.set_piece_hashes(tor, os.path.dirname(os.path.abspath(sys.argv[2])))
    open(sys.argv[3], "wb").write(lt.bencode(tor.generate()))
elif cmd == "seed":
    import libtorrent as lt
    ses = lt.session(lt_settings(sys.argv[4])); add(ses, sys.argv[2], sys.argv[3], True)
    print("seeding", flush=True)
    while True:
        time.sleep(5)
        for a in ses.pop_alerts():
            print(time.strftime("%T"), type(a).__name__, a.message()[:120], flush=True)
elif cmd == "leech":
    import libtorrent as lt
    torrent, directory, listen, peer, seconds, socks = sys.argv[2:8]
    ses = lt.session(lt_settings(listen, socks)); h = add(ses, torrent, directory, False)
    host, port = peer.rsplit(":", 1); start = time.time(); peers = 0
    while time.time() - start < float(seconds):
        h.connect_peer((host, int(port))); time.sleep(0.5)
        st = h.status(); peers = max(peers, st.num_peers)
        if st.is_seeding:
            break
    st = h.status()
    print(json.dumps({"done": bool(st.is_seeding), "bytes": st.total_wanted_done, "seconds": round(time.time() - start, 2), "peers": peers}))
elif cmd == "replay":
    port, dst = int(sys.argv[2]), sys.argv[3]
    ctl, relay = socks_assoc(port); out = {}
    for item in sys.argv[4:]:
        name, hx = item.split("=", 1)
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(3)  # new socket = new Core UDP session
        ip, p = dst.rsplit(":", 1)
        s.sendto(b"\x00\x00\x00\x01" + socket.inet_aton(ip) + struct.pack(">H", int(p)) + bytes.fromhex(hx), relay)
        try:
            d = s.recvfrom(4096)[0][10:]
            out[name] = UTP.get(d[0] >> 4, "reply") if len(d) >= 20 and d[0] & 15 == 1 and name.startswith("utp:") else "reply"
        except socket.timeout:
            out[name] = "none"
        s.close()
    ctl.close(); print(json.dumps(out))
elif cmd == "echo":
    for port in sys.argv[3:]:
        def serve(port=int(port)):
            s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.bind((sys.argv[2], port))
            while True:
                d, a = s.recvfrom(65535); s.sendto(d, a)
        threading.Thread(target=serve, daemon=True).start()
    while True:
        time.sleep(60)
elif cmd == "dns":
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.bind((sys.argv[2], 53))
    while True:
        q, a = s.recvfrom(4096)
        i = 12
        while q[i]:
            i += q[i] + 1
        qend = i + 5
        s.sendto(q[:2] + b"\x81\x80" + q[4:6] + b"\x00\x01\x00\x00\x00\x00" + q[12:qend] + b"\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x3c\x00\x04\xc0\x00\x02\x07", a)
elif cmd == "wgecho":
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(6)
    s.sendto(b"wg-probe", (sys.argv[2], int(sys.argv[3])))
    try:
        print(json.dumps({"ok": s.recvfrom(64)[0] == b"wg-probe"}))
    except socket.timeout:
        print(json.dumps({"ok": False}))
`

func runUTPLab(t *testing.T, binary string, expectBlock bool) map[string]any {
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
	nss := []string{"flvxut-c", "flvxut-l", "flvxut-t"}
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
	sh("ip", "link", "add", "flvxut-c0", "type", "veth", "peer", "name", "flvxut-l0")
	sh("ip", "link", "set", "flvxut-c0", "netns", "flvxut-c")
	sh("ip", "link", "set", "flvxut-l0", "netns", "flvxut-l")
	sh("ip", "link", "add", "flvxut-l1", "type", "veth", "peer", "name", "flvxut-t0")
	sh("ip", "link", "set", "flvxut-l1", "netns", "flvxut-l")
	sh("ip", "link", "set", "flvxut-t0", "netns", "flvxut-t")
	sh("ip", "-n", "flvxut-c", "addr", "add", "203.0.113.60/24", "dev", "flvxut-c0")
	sh("ip", "-n", "flvxut-l", "addr", "add", "203.0.113.1/24", "dev", "flvxut-l0")
	sh("ip", "-n", "flvxut-l", "addr", "add", "198.51.100.1/24", "dev", "flvxut-l1")
	sh("ip", "-n", "flvxut-t", "addr", "add", "198.51.100.2/24", "dev", "flvxut-t0")
	for _, l := range [][2]string{{"flvxut-c", "flvxut-c0"}, {"flvxut-l", "flvxut-l0"}, {"flvxut-l", "flvxut-l1"}, {"flvxut-t", "flvxut-t0"}} {
		sh("ip", "-n", l[0], "link", "set", l[1], "up")
	}
	lab := filepath.Join(base, "lab.py")
	if err := os.WriteFile(lab, []byte(utpLabPy), 0644); err != nil {
		t.Fatal(err)
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
	// Target: uTP-only seeder of a 4 MiB file, DNS, UDP echo, Reality dest.
	seedDir := filepath.Join(base, "seed")
	_ = os.MkdirAll(seedDir, 0700)
	blob := make([]byte, 4<<20)
	for i := range blob {
		blob[i] = byte(i * 7)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "blob.bin"), blob, 0644); err != nil {
		t.Fatal(err)
	}
	torrent := filepath.Join(base, "lab.torrent")
	sh("python3", lab, "make", filepath.Join(seedDir, "blob.bin"), torrent)
	bg("flvxut-t", "seeder", "python3", lab, "seed", torrent, seedDir, "198.51.100.2:6881")
	bg("flvxut-t", "dns", "python3", lab, "dns", "198.51.100.2")
	bg("flvxut-t", "echo", "python3", lab, "echo", "198.51.100.2", "7777", "443", "3478", "123", "4500")
	sh("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes", "-days", "2",
		"-subj", "/CN=www.flvx.test", "-addext", "subjectAltName=DNS:www.flvx.test", "-keyout", filepath.Join(base, "dest.key"), "-out", filepath.Join(base, "dest.crt"))
	bg("flvxut-t", "tls", "openssl", "s_server", "-quiet", "-accept", "198.51.100.2:8443", "-cert", filepath.Join(base, "dest.crt"), "-key", filepath.Join(base, "dest.key"), "-tls1_3", "-www")

	// Landing: FLVX agent with the panel's default anti-abuse rules.
	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxut-l "+core+" \"$@\"\n"), 0755); err != nil {
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
	n1 := testNode(t, 1)
	n1.Port = 31001
	n2 := Node{ID: 2, InboundTag: "flvxl-2", Protocol: "vless_reality_vision", Port: 31002, Enabled: true,
		VLESS: &VLESS{UUID: uuid, Flow: "xtls-rprx-vision", Reality: Reality{Dest: "198.51.100.2:8443", ServerNames: []string{"www.flvx.test"}, PrivateKey: goldenChainPrivate, ShortIDs: []string{"a1b2c3d4"}}}}
	all := []string{"flvxl-1", "flvxl-2"}
	plan := &Egress{Version: 1, Rules: []EgressRule{
		{Name: "默认：屏蔽 SMTP 25", InboundTags: all, Port: "25", Network: "tcp", Outbound: "block", Builtin: builtinAbuse},
		{Name: "默认：屏蔽 BT", InboundTags: all, Protocol: []string{"bittorrent"}, Outbound: "block", Builtin: builtinAbuse},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	status, err := m.Apply(ctx, Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: []Node{n1, n2}, Egress: plan})
	if err != nil || status.Egress.State != "applied" {
		t.Fatalf("apply: %v %+v", err, status.Egress)
	}
	time.Sleep(2 * time.Second)
	idle := procStats(m)
	version := sh(core, "version")
	if i := strings.Index(version, "\n"); i > 0 {
		version = version[:i]
	}

	// Client: the Core as client; SOCKS5 (UDP) and dokodemo UDP per protocol.
	pubOut := sh(core, "x25519", "-i", goldenChainPrivate)
	pub := ""
	for _, line := range strings.Split(pubOut, "\n") {
		if strings.HasPrefix(line, "Password (PublicKey): ") || strings.HasPrefix(line, "Public key: ") {
			pub = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		}
	}
	outs := []any{
		map[string]any{"tag": "ss", "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": "203.0.113.1", "port": n1.Port, "method": SS2022Method, "password": n1.SS2022.ServerKey + ":" + n1.SS2022.UserKey}}}},
		map[string]any{"tag": "vless", "protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{"address": "203.0.113.1", "port": n2.Port, "users": []any{map[string]any{"id": uuid, "flow": "xtls-rprx-vision-udp443", "encryption": "none"}}}}},
			"streamSettings": map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"serverName": "www.flvx.test", "publicKey": pub, "shortId": "a1b2c3d4", "fingerprint": "chrome"}}},
	}
	var inbounds, rules []any
	for i, proto := range []string{"ss", "vless"} {
		in := []map[string]any{
			{"tag": proto + "-socks", "listen": "127.0.0.1", "port": 1080 + i, "protocol": "socks", "settings": map[string]any{"udp": true, "ip": "127.0.0.1"}},
			{"tag": proto + "-dns", "listen": "127.0.0.1", "port": 5353 + i, "protocol": "dokodemo-door", "settings": map[string]any{"address": "198.51.100.2", "port": 53, "network": "udp"}},
			{"tag": proto + "-wg", "listen": "127.0.0.1", "port": 51821 + i, "protocol": "dokodemo-door", "settings": map[string]any{"address": "198.51.100.2", "port": 51820, "network": "udp"}},
		}
		for _, v := range in {
			inbounds = append(inbounds, v)
			rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{v["tag"].(string)}, "outboundTag": proto})
		}
	}
	clientCfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": inbounds, "outbounds": outs, "routing": map[string]any{"rules": rules}})
	if err := os.WriteFile(filepath.Join(base, "client.json"), clientCfg, 0600); err != nil {
		t.Fatal(err)
	}
	bg("flvxut-c", "client", core, "run", "-config", filepath.Join(base, "client.json"))
	time.Sleep(time.Second)

	// WireGuard: real kernel peers, the client's endpoint is the dokodemo port.
	ka, kb := sh("wg", "genkey"), sh("wg", "genkey")
	pubKey := func(k string) string {
		cmd := exec.Command("wg", "pubkey")
		cmd.Stdin = strings.NewReader(k)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	pa, pb := pubKey(ka), pubKey(kb)
	_ = os.WriteFile(filepath.Join(base, "ka"), []byte(ka), 0600)
	_ = os.WriteFile(filepath.Join(base, "kb"), []byte(kb), 0600)
	run := func(ns string, args ...string) string {
		out, _ := exec.Command("ip", append([]string{"netns", "exec", ns}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	wg := func(port int) map[string]any {
		sh("ip", "-n", "flvxut-t", "link", "add", "wgl", "type", "wireguard")
		sh("ip", "-n", "flvxut-c", "link", "add", "wgl", "type", "wireguard")
		defer func() {
			_ = exec.Command("ip", "-n", "flvxut-t", "link", "del", "wgl").Run()
			_ = exec.Command("ip", "-n", "flvxut-c", "link", "del", "wgl").Run()
		}()
		run("flvxut-t", "wg", "set", "wgl", "private-key", filepath.Join(base, "kb"), "listen-port", "51820", "peer", pa, "allowed-ips", "10.73.0.2/32")
		run("flvxut-c", "wg", "set", "wgl", "private-key", filepath.Join(base, "ka"), "peer", pb, "endpoint", fmt.Sprintf("127.0.0.1:%d", port), "allowed-ips", "10.73.0.1/32")
		sh("ip", "-n", "flvxut-t", "addr", "add", "10.73.0.1/24", "dev", "wgl")
		sh("ip", "-n", "flvxut-c", "addr", "add", "10.73.0.2/24", "dev", "wgl")
		sh("ip", "-n", "flvxut-t", "link", "set", "wgl", "up")
		sh("ip", "-n", "flvxut-c", "link", "set", "wgl", "up")
		echo := exec.Command("ip", "netns", "exec", "flvxut-t", "python3", lab, "echo", "10.73.0.1", "9999")
		if err := echo.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = echo.Process.Kill(); _, _ = echo.Process.Wait() }()
		time.Sleep(300 * time.Millisecond)
		res := map[string]any{}
		_ = json.Unmarshal([]byte(run("flvxut-c", "python3", lab, "wgecho", "10.73.0.1", "9999")), &res)
		res["handshake"] = strings.Contains(run("flvxut-c", "wg", "show", "wgl"), "latest handshake")
		return res
	}
	out := map[string]any{"core": version, "expect_block": expectBlock, "idle": idle}
	failures := []string{}
	check := func(ok bool, format string, args ...any) {
		if !ok {
			failures = append(failures, fmt.Sprintf(format, args...))
		}
	}
	for i, proto := range []string{"ss", "vless"} {
		socks := 1080 + i
		r := map[string]any{}
		leechDir := filepath.Join(base, "leech-"+proto)
		_ = os.MkdirAll(leechDir, 0700)
		leech := map[string]any{}
		_ = json.Unmarshal([]byte(lastLine(run("flvxut-c", "python3", lab, "leech", torrent, leechDir, fmt.Sprintf("127.0.0.1:%d", 6990+i), "198.51.100.2:6881", "20", fmt.Sprintf("127.0.0.1:%d", socks)))), &leech)
		r["utp_download"] = leech
		var replayArgs []string
		for name, hx := range utpRealSYNs {
			replayArgs = append(replayArgs, "utp:"+name+"="+hx)
		}
		syn := map[string]string{}
		_ = json.Unmarshal([]byte(run("flvxut-c", append([]string{"python3", lab, "replay", strconv.Itoa(socks), "198.51.100.2:6881"}, replayArgs...)...)), &syn)
		r["utp_syn_replay"] = syn
		others := map[string]string{}
		for name, hx := range udpRealOthers {
			port := map[string]string{"quic-initial-curl": "443", "stun-binding": "3478", "ntp-client": "123", "wireguard-init": "4500", "plain": "7777"}[name]
			res := map[string]string{}
			_ = json.Unmarshal([]byte(run("flvxut-c", "python3", lab, "replay", strconv.Itoa(socks), "198.51.100.2:"+port, name+"="+hx)), &res)
			others[name] = res[name]
		}
		r["udp_first_packets"] = others
		dig := run("flvxut-c", "dig", "+tries=1", "+time=4", "-p", strconv.Itoa(5353+i), "@127.0.0.1", "example.com", "A")
		r["dns_dig"] = strings.Contains(dig, "status: NOERROR") && strings.Contains(dig, "192.0.2.7")
		r["wireguard"] = wg(51821 + i)
		out[proto] = r

		done, _ := leech["done"].(bool)
		answered := 0
		for _, v := range syn {
			if v == "ST_STATE" {
				answered++
			}
		}
		if expectBlock {
			check(!done && fmt.Sprint(leech["bytes"]) == "0", "%s: uTP download not blocked: %v", proto, leech)
			check(answered == 0, "%s: uTP SYNs answered by the seeder: %v", proto, syn)
		} else {
			check(done, "%s: control Core did not complete the uTP download: %v", proto, leech)
			check(answered == len(utpRealSYNs), "%s: control Core blocked uTP SYNs: %v", proto, syn)
		}
		for name, v := range others {
			check(v == "reply", "%s: %s UDP blocked (%q)", proto, name, v)
		}
		check(r["dns_dig"] == true, "%s: dig failed: %s", proto, dig)
		w := r["wireguard"].(map[string]any)
		check(w["ok"] == true && w["handshake"] == true, "%s: WireGuard failed: %v", proto, w)
	}
	out["after"] = procStats(m)
	seedLog, _ := os.ReadFile(filepath.Join(base, "seeder.log"))
	out["seeder_peer_alerts"] = strings.Count(string(seedLog), "peer_connect_alert")
	b2, _ := json.Marshal(out)
	t.Logf("%s", b2)
	for _, f := range failures {
		t.Error(f)
	}
	return out
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
