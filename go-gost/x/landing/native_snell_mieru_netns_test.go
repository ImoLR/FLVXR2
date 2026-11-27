//go:build linux

package landing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// TestNativeSnellMieruNetnsE2E (plan 317) runs the pinned Core through the
// Manager in a landing namespace and the real mihomo client (≥ 1.19.32) in
// two client namespaces (11.0.1.50, 11.0.3.50), with targets behind the
// landing (TCP 12.0.1.2, UDP DNS on 12.0.1.2 and 12.0.1.3). It measures, for
// Snell v4 / v5 (obfs none / http / tls) and Mieru (client transport TCP and
// UDP), next to SS2022 and VLESS Reality on the same Core:
//   - connect (TCP) and UDP (two DNS servers in one UDP session: per-packet
//     target), traffic counters both ways;
//   - 连接数 cap in both modes (inbound / total), online-IP cap, disable → cut;
//   - per-user 速率 (kernel, incl. Mieru UDP);
//   - warm-pool hold: silent pre-dialed TCP conns aged FLVX_SM_HOLD_S (default
//     135 s) carry a real client afterwards; the untouched silent conn's
//     effective hold;
//   - Core CPU per GB at 10 parallel streams (vs VLESS / SS2022).
//
// Needs root, iproute2, python3, openssl, FLVX_LANDING_TEST_BINARY (pinned
// Core), FLVX_MIHOMO_BIN and FLVX_SM_E2E=1 (≈ 6 min). FLVX_SM_OUT writes
// the numbers (JSON).
func TestNativeSnellMieruNetnsE2E(t *testing.T) {
	if os.Getenv("FLVX_SM_E2E") != "1" || os.Geteuid() != 0 {
		t.Skip("set FLVX_SM_E2E=1 and run as root")
	}
	mihomo := os.Getenv("FLVX_MIHOMO_BIN")
	if mihomo == "" {
		t.Skip("FLVX_MIHOMO_BIN not set")
	}
	holdS := 135
	if v, err := strconv.Atoi(os.Getenv("FLVX_SM_HOLD_S")); err == nil && v > 0 {
		holdS = v
	}
	base := t.TempDir()
	dir := filepath.Join(base, "landing")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	core := realCore(t, dir)
	sh := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
		return string(out)
	}
	nss := []string{"flvxsm-c", "flvxsm-d", "flvxsm-l", "flvxsm-t"}
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
	link := func(a, aDev, aIP, b, bDev, bIP string) {
		sh("ip", "link", "add", aDev, "type", "veth", "peer", "name", bDev)
		sh("ip", "link", "set", aDev, "netns", a)
		sh("ip", "link", "set", bDev, "netns", b)
		sh("ip", "-n", a, "addr", "add", aIP, "dev", aDev)
		sh("ip", "-n", b, "addr", "add", bIP, "dev", bDev)
		sh("ip", "-n", a, "link", "set", aDev, "up")
		sh("ip", "-n", b, "link", "set", bDev, "up")
	}
	link("flvxsm-c", "flvxsm-c0", "11.0.1.50/24", "flvxsm-l", "flvxsm-l0", "11.0.1.1/24")
	link("flvxsm-d", "flvxsm-d0", "11.0.3.50/24", "flvxsm-l", "flvxsm-l2", "11.0.3.1/24")
	link("flvxsm-l", "flvxsm-l1", "12.0.1.1/24", "flvxsm-t", "flvxsm-t0", "12.0.1.2/24")
	sh("ip", "-n", "flvxsm-t", "addr", "add", "12.0.1.3/24", "dev", "flvxsm-t0")
	// 5 ms each way client ↔ landing: a real path, small enough for bulk runs.
	sh("ip", "netns", "exec", "flvxsm-c", "tc", "qdisc", "add", "dev", "flvxsm-c0", "root", "netem", "delay", "5ms", "limit", "100000")
	sh("ip", "netns", "exec", "flvxsm-l", "tc", "qdisc", "add", "dev", "flvxsm-l0", "root", "netem", "delay", "5ms", "limit", "100000")
	sh("ip", "netns", "exec", "flvxsm-l", "sysctl", "-qw", "net.ipv4.ip_forward=0")

	// Target: TCP hold (9100: "ok\n" then hold), bulk (9200: "D <n>" sends n
	// bytes, "U <n>" reads n bytes then answers "done"), Reality dest (8443),
	// UDP DNS on 12.0.1.2:53 and 12.0.1.3:53 (A = the server's own address).
	sh("openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes", "-days", "2",
		"-subj", "/CN=www.flvx.test", "-addext", "subjectAltName=DNS:www.flvx.test",
		"-keyout", filepath.Join(base, "dest.key"), "-out", filepath.Join(base, "dest.crt"))
	for name, body := range map[string]string{"target.py": smTargetPy, "cli.py": smClientPy, "pool.py": smPoolPy} {
		if err := os.WriteFile(filepath.Join(base, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	target := exec.Command("ip", "netns", "exec", "flvxsm-t", "python3", filepath.Join(base, "target.py"), base)
	targetLog, _ := os.Create(filepath.Join(base, "target.log"))
	target.Stdout, target.Stderr = targetLog, targetLog
	if err := target.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Process.Kill(); _, _ = target.Process.Wait() })

	// Landing Manager in its namespace (rates on its netlink handle).
	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxsm-l "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	lns, err := netns.GetFromName("flvxsm-l")
	if err != nil {
		t.Fatal(err)
	}
	defer lns.Close()
	lh, err := netlink.NewHandleAt(lns)
	if err != nil {
		t.Fatal(err)
	}
	defer lh.Close()
	m := NewManager(dir, true)
	m.execPath = wrapper
	m.rates = newRateEngineAt(lh)
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
		m.ReleaseRates(5 * time.Second)
	})
	const uuid = "8a8e3c4d-1f2b-4c3d-9e8f-0a1b2c3d4e5f"
	ss := testNode(t, 1)
	ss.Port = 31401
	reality := Node{ID: 2, InboundTag: "flvxl-2", Protocol: "vless_reality_vision", Port: 31402, Enabled: true,
		VLESS: &VLESS{UUID: uuid, Flow: "xtls-rprx-vision", Reality: Reality{Dest: "12.0.1.2:8443", ServerNames: []string{"www.flvx.test"}, PrivateKey: goldenChainPrivate, ShortIDs: []string{"a1b2c3d4"}}}}
	snell := func(id int64, version int, obfs, host string) Node {
		n := publicSnellNode(t, id, version, obfs, host)
		n.Port = 31400 + int(id)
		return n
	}
	mieru := func(id int64) Node {
		n := publicMieruNode(t, id)
		n.Port = 31400 + int(id)
		n.Mieru.Username = fmt.Sprintf("lab%02duser", id)
		return n
	}
	nodes := []Node{ss, reality,
		snell(3, 4, "", ""), snell(4, 5, "http", "www.bing.com"), snell(5, 4, "tls", "www.bing.com"), snell(6, 5, "", ""),
		mieru(7),
		snell(8, 4, "", ""), mieru(9), // 连接数 inbound 2
		snell(10, 4, "", ""), mieru(11), // online IPs 1
		snell(12, 4, "", ""), mieru(13), // 速率 10 Mbps (landing-user-7)
		snell(14, 4, "", ""), mieru(15), // 连接数 total 2
		snell(16, 4, "", ""), mieru(17), // warm-pool hold
	}
	for _, i := range []int{7, 8} { // 8, 9
		nodes[i].MaxConnections, nodes[i].ConnMode = 2, ConnModeInbound
	}
	nodes[9].MaxOnlineIPs, nodes[10].MaxOnlineIPs = 1, 1
	nodes[11].RateGroup, nodes[12].RateGroup = "landing-user-7", "landing-user-7"
	nodes[13].MaxConnections, nodes[14].MaxConnections = 2, 2
	byID := map[int64]Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	rates := &UserRates{Version: LandingRateVersion, Groups: []RateGroup{{Group: "landing-user-7", RateMbps: 10}}}
	desired := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 400, Nodes: nodes, UserRates: rates}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	st, err := m.Apply(ctx, desired)
	if err != nil || len(st.NodeErrors) != 0 {
		t.Fatalf("apply: %v %+v", err, st.NodeErrors)
	}
	epoch := st.Epoch
	results := map[string]any{"hold_s": holdS, "defer_accept": guardDeferAccept, "core": CoreVersion}

	// Warm pool: pre-dial the silent conns at once so the hold overlaps the
	// other phases.
	poolOut := filepath.Join(base, "pool.json")
	pool := exec.Command("ip", "netns", "exec", "flvxsm-c", "python3", filepath.Join(base, "pool.py"), strconv.Itoa(holdS), poolOut, "31416", "31417")
	poolLog, _ := os.Create(filepath.Join(base, "pool.log"))
	pool.Stdout, pool.Stderr = poolLog, poolLog
	if err := pool.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Process.Kill(); _, _ = pool.Process.Wait() })
	poolStart := time.Now()

	// mihomo: one socks listener per proxy.
	pubOut := sh(core, "x25519", "-i", goldenChainPrivate)
	pub := ""
	for _, line := range strings.Split(pubOut, "\n") {
		if strings.HasPrefix(line, "Password (PublicKey): ") || strings.HasPrefix(line, "Public key: ") {
			pub = strings.TrimSpace(line[strings.Index(line, ":")+1:])
		}
	}
	type proxy struct {
		name string
		yaml string
	}
	snellProxy := func(name, server string, port int, n Node) proxy {
		y := fmt.Sprintf("  - {name: %s, type: snell, server: %s, port: %d, psk: %s, version: %d, udp: true", name, server, port, n.Snell.PSK, n.Snell.Version)
		if n.Snell.Obfs == "http" || n.Snell.Obfs == "tls" {
			y += fmt.Sprintf(", obfs-opts: {mode: %s, host: %s}", n.Snell.Obfs, n.Snell.ObfsHost)
		}
		return proxy{name, y + "}"}
	}
	mieruProxy := func(name, server string, port int, n Node, transport string) proxy {
		return proxy{name, fmt.Sprintf("  - {name: %s, type: mieru, server: %s, port: %d, transport: %s, username: %s, password: %s, multiplexing: MULTIPLEXING_LOW, udp: true}", name, server, port, transport, n.Mieru.Username, n.Mieru.Password)}
	}
	land := "11.0.1.1"
	proxies := []proxy{
		{"ss", fmt.Sprintf("  - {name: ss, type: ss, server: %s, port: %d, cipher: %s, password: \"%s:%s\", udp: true}", land, ss.Port, SS2022Method, ss.SS2022.ServerKey, ss.SS2022.UserKey)},
		{"vless", fmt.Sprintf("  - {name: vless, type: vless, server: %s, port: %d, uuid: %s, flow: xtls-rprx-vision, network: tcp, tls: true, servername: www.flvx.test, client-fingerprint: chrome, reality-opts: {public-key: %s, short-id: a1b2c3d4}, udp: true}", land, reality.Port, uuid, pub)},
		snellProxy("snell4", land, 31403, byID[3]), snellProxy("snell5http", land, 31404, byID[4]), snellProxy("snell4tls", land, 31405, byID[5]), snellProxy("snell5", land, 31406, byID[6]),
		mieruProxy("mierutcp", land, 31407, byID[7], "TCP"), mieruProxy("mieruudp", land, 31407, byID[7], "UDP"),
		snellProxy("capsnell", land, 31408, byID[8]), mieruProxy("capmieru", land, 31409, byID[9], "TCP"),
		snellProxy("ipsnell", land, 31410, byID[10]), mieruProxy("ipmieru", land, 31411, byID[11], "TCP"), mieruProxy("ipmieruudp", land, 31411, byID[11], "UDP"),
		snellProxy("ratesnell", land, 31412, byID[12]), mieruProxy("ratemieru", land, 31413, byID[13], "TCP"), mieruProxy("ratemieruudp", land, 31413, byID[13], "UDP"),
		snellProxy("totsnell", land, 31414, byID[14]), mieruProxy("totmieru", land, 31415, byID[15], "TCP"),
		snellProxy("poolsnell", "127.0.0.1", 25416, byID[16]), mieruProxy("poolmieru", "127.0.0.1", 25417, byID[17], "TCP"),
		snellProxy("freshsnell", "127.0.0.1", 26416, byID[16]), mieruProxy("freshmieru", "127.0.0.1", 26417, byID[17], "TCP"),
	}
	portOf := map[string]int{}
	writeMihomo := func(name string, list []proxy) {
		var b strings.Builder
		b.WriteString("mode: rule\nlog-level: warning\nipv6: false\nallow-lan: false\nunified-delay: false\nprofile: {store-selected: false, store-fake-ip: false}\nproxies:\n")
		for _, p := range list {
			b.WriteString(p.yaml + "\n")
		}
		b.WriteString("listeners:\n")
		for i, p := range list {
			port := 17000 + i
			portOf[p.name] = port
			b.WriteString(fmt.Sprintf("  - {name: in-%s, type: socks, listen: 127.0.0.1, port: %d, udp: true, proxy: %s}\n", p.name, port, p.name))
		}
		b.WriteString("rules:\n  - MATCH,DIRECT\n")
		mdir := filepath.Join(base, name)
		_ = os.MkdirAll(mdir, 0700)
		if err := os.WriteFile(filepath.Join(mdir, "config.yaml"), []byte(b.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
	startMihomo := func(ns, name string) {
		cmd := exec.Command("ip", "netns", "exec", ns, mihomo, "-d", filepath.Join(base, name))
		logf, _ := os.Create(filepath.Join(base, name+".log"))
		cmd.Stdout, cmd.Stderr = logf, logf
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	}
	writeMihomo("mihomo-c", proxies)
	startMihomo("flvxsm-c", "mihomo-c")
	// Client 2 (another source IP): only the online-IP nodes, through 11.0.3.1.
	land2 := "11.0.3.1"
	proxies2 := []proxy{snellProxy("ipsnell", land2, 31410, byID[10]), mieruProxy("ipmieru", land2, 31411, byID[11], "TCP"), mieruProxy("ipmieruudp", land2, 31411, byID[11], "UDP")}
	portOf2 := map[string]int{}
	{
		saved := portOf
		portOf = map[string]int{}
		writeMihomo("mihomo-d", proxies2)
		portOf2, portOf = portOf, saved
	}
	startMihomo("flvxsm-d", "mihomo-d")
	time.Sleep(3 * time.Second)

	cli := func(ns string, args ...string) map[string]any {
		t.Helper()
		cmd := exec.Command("ip", append([]string{"netns", "exec", ns, "nice", "-n", "10", "python3", filepath.Join(base, "cli.py")}, args...)...)
		out, err := cmd.Output()
		var v map[string]any
		if jerr := json.Unmarshal(out, &v); jerr != nil {
			return map[string]any{"error": fmt.Sprintf("%v %s", err, out)}
		}
		return v
	}
	type holder struct {
		res   map[string]any
		stdin io.WriteCloser
		out   *bufio.Reader
		cmd   *exec.Cmd
	}
	// hold opens n streams through a socks port and keeps them (until release).
	hold := func(ns string, socks, n int) *holder {
		t.Helper()
		cmd := exec.Command("ip", "netns", "exec", ns, "python3", filepath.Join(base, "cli.py"), "hold", strconv.Itoa(socks), strconv.Itoa(n))
		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		r := bufio.NewReader(stdout)
		line, _ := r.ReadString('\n')
		h := &holder{stdin: stdin, out: r, cmd: cmd}
		_ = json.Unmarshal([]byte(line), &h.res)
		return h
	}
	// watch asks a holder how many of its streams are closed after secs.
	watch := func(h *holder, secs int) map[string]any {
		_, _ = io.WriteString(h.stdin, fmt.Sprintf("watch %d\n", secs))
		line, _ := h.out.ReadString('\n')
		var v map[string]any
		_ = json.Unmarshal([]byte(line), &v)
		return v
	}
	release := func(h *holder) {
		_ = h.stdin.Close()
		_ = h.cmd.Wait()
	}
	counter := func(id int64) Counter {
		for i := 0; i < 30; i++ {
			for _, c := range m.Counters() {
				if c.ID == id {
					return c
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		return Counter{}
	}
	num := func(v any) float64 { f, _ := v.(float64); return f }

	// 1. Connect + UDP DNS (two servers in one UDP association).
	connect := map[string]any{}
	for _, p := range []string{"ss", "vless", "snell4", "snell5http", "snell4tls", "snell5", "mierutcp", "mieruudp"} {
		connect[p] = map[string]any{"tcp": cli("flvxsm-c", "tcp", strconv.Itoa(portOf[p])), "udp": cli("flvxsm-c", "dns", strconv.Itoa(portOf[p]))}
	}
	results["connect"] = connect

	// 2. Counters both ways + Core CPU at 10 parallel streams.
	cpu := map[string]any{}
	for _, tc := range []struct {
		proxy string
		id    int64
	}{{"vless", 2}, {"ss", 1}, {"snell4", 3}, {"snell5", 6}, {"mierutcp", 7}, {"mieruudp", 7}} {
		c0, p0 := counter(tc.id), procStats(m)
		down := cli("flvxsm-c", "bulk", strconv.Itoa(portOf[tc.proxy]), "D", strconv.Itoa(8<<20), "10")
		p1 := procStats(m)
		up := cli("flvxsm-c", "bulk", strconv.Itoa(portOf[tc.proxy]), "U", strconv.Itoa(2<<20), "10")
		p2 := procStats(m)
		time.Sleep(500 * time.Millisecond)
		c1 := counter(tc.id)
		gbDown := num(down["bytes"]) / 1e9
		gbUp := num(up["bytes"]) / 1e9
		row := map[string]any{"down": down, "up": up,
			"counter_down_mb": float64(c1.DownBytes-c0.DownBytes) / 1e6, "counter_up_mb": float64(c1.UpBytes-c0.UpBytes) / 1e6,
			"core_cpu_s_down": p1["cpu_s"] - p0["cpu_s"], "core_cpu_s_up": p2["cpu_s"] - p1["cpu_s"], "rss_mib": p2["rss_mib"]}
		if gbDown > 0 {
			row["cpu_s_per_gb_down"] = (p1["cpu_s"] - p0["cpu_s"]) / gbDown
		}
		if gbUp > 0 {
			row["cpu_s_per_gb_up"] = (p2["cpu_s"] - p1["cpu_s"]) / gbUp
		}
		cpu[tc.proxy] = row
	}
	results["traffic_cpu"] = cpu

	// 3. 连接数: inbound mode 2 (nodes 8/9), total mode 2 (nodes 14/15).
	caps := map[string]any{}
	for _, tc := range []struct {
		proxy string
		id    int64
	}{{"capsnell", 8}, {"capmieru", 9}, {"totsnell", 14}, {"totmieru", 15}} {
		h := hold("flvxsm-c", portOf[tc.proxy], 4)
		time.Sleep(time.Second)
		c := counter(tc.id)
		row := map[string]any{"streams": h.res, "core_connections": c.Connections}
		if c.InboundConnections != nil {
			row["core_inbound"] = *c.InboundConnections
		}
		caps[tc.proxy] = row
		release(h)
	}
	results["conn_cap"] = caps

	// 4. Online IPs 1 (nodes 10/11): client 1 holds, client 2 is refused;
	// after client 1 leaves, client 2 gets in.
	ips := map[string]any{}
	for _, tc := range []struct {
		proxy string
		id    int64
	}{{"ipsnell", 10}, {"ipmieru", 11}, {"ipmieruudp", 11}} {
		h1 := hold("flvxsm-c", portOf[tc.proxy], 1)
		time.Sleep(time.Second)
		second := cli("flvxsm-d", "tcp", strconv.Itoa(portOf2[tc.proxy]))
		c := counter(tc.id)
		release(h1)
		time.Sleep(3 * time.Second)
		var after map[string]any
		for i := 0; i < 10; i++ {
			if after = cli("flvxsm-d", "tcp", strconv.Itoa(portOf2[tc.proxy])); after["ok"] == true {
				break
			}
			time.Sleep(3 * time.Second)
		}
		online := -1
		if c.OnlineIPCount != nil {
			online = *c.OnlineIPCount
		}
		ips[tc.proxy] = map[string]any{"first": h1.res, "second_while_first_holds": second, "online_ip_count": online, "second_after_first_left": after}
	}
	results["online_ip"] = ips

	// 5. 速率 10 Mbps per user (nodes 12/13; Mieru UDP is shaped on UDP).
	rate := map[string]any{}
	for _, p := range []string{"ratesnell", "ratemieru", "ratemieruudp"} {
		rate[p] = map[string]any{"down": cli("flvxsm-c", "bulk", strconv.Itoa(portOf[p]), "D", strconv.Itoa(3<<20), "1"), "up": cli("flvxsm-c", "bulk", strconv.Itoa(portOf[p]), "U", strconv.Itoa(3<<20), "1")}
	}
	results["rate_10mbps"] = rate

	// 6. Warm-pool hold (nodes 16/17): wait for the hold, then real clients
	// through the aged conns and through fresh ones.
	if left := time.Duration(holdS)*time.Second + 5*time.Second - time.Since(poolStart); left > 0 {
		mid, _ := exec.Command("ip", "netns", "exec", "flvxsm-l", "ss", "-Htn", "state", "syn-recv", "( sport = :31416 or sport = :31417 )").Output()
		est, _ := exec.Command("ip", "netns", "exec", "flvxsm-l", "ss", "-Htn", "state", "established", "( sport = :31416 or sport = :31417 )").Output()
		results["pool_mid_syn_recv"] = len(strings.Split(strings.TrimSpace(string(mid)), "\n"))
		results["pool_mid_established"] = len(strings.Fields(string(est))) / 4
		time.Sleep(left)
	}
	for i := 0; i < 30; i++ {
		b, _ := os.ReadFile(poolOut)
		if strings.Contains(string(b), `"ready"`) {
			break
		}
		time.Sleep(time.Second)
	}
	warm := map[string]any{}
	for _, p := range []string{"poolsnell", "poolmieru", "freshsnell", "freshmieru"} {
		var runs []any
		for i := 0; i < 3; i++ {
			runs = append(runs, cli("flvxsm-c", "tcp", strconv.Itoa(portOf[p])))
		}
		warm[p] = runs
	}
	results["warm"] = warm
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(poolOut)
		var v map[string]any
		if json.Unmarshal(b, &v) == nil && v["watch_done"] == true {
			results["pool"] = v
			break
		}
		time.Sleep(2 * time.Second)
	}

	// 7. Disable → cut (Snell node 3, Mieru node 7 TCP and UDP transport).
	cut := map[string]any{}
	holders := map[string]*holder{}
	for _, p := range []string{"snell4", "mierutcp", "mieruudp"} {
		holders[p] = hold("flvxsm-c", portOf[p], 2)
	}
	time.Sleep(time.Second)
	off := desired
	off.Revision = 2
	off.Nodes = append([]Node(nil), nodes...)
	off.Nodes[2].Enabled, off.Nodes[6].Enabled = false, false
	if st, err = m.Apply(ctx, off); err != nil || st.Epoch != epoch {
		t.Errorf("disable apply: %v (epoch %s → %s)", err, epoch, st.Epoch)
	}
	for p, h := range holders {
		cut[p] = map[string]any{"opened": h.res, "after_disable": watch(h, 10)}
		release(h)
	}
	cut["snell4_new_after_disable"] = cli("flvxsm-c", "tcp", strconv.Itoa(portOf["snell4"]))
	cut["mierutcp_new_after_disable"] = cli("flvxsm-c", "tcp", strconv.Itoa(portOf["mierutcp"]))
	results["disable_cut"] = cut
	results["core_epoch_unchanged"] = st.Epoch == epoch
	results["proc"] = procStats(m)

	b, _ := json.MarshalIndent(results, "", "  ")
	if out := os.Getenv("FLVX_SM_OUT"); out != "" {
		_ = os.WriteFile(out, b, 0644)
	}
	t.Logf("results: %s", b)
	if t.Failed() {
		for _, name := range []string{"mihomo-c.log", "mihomo-d.log", "pool.log", "target.log"} {
			l, _ := os.ReadFile(filepath.Join(base, name))
			t.Logf("%s:\n%s", name, l)
		}
	}
}

const smTargetPy = `import socket, ssl, struct, sys, threading
base = sys.argv[1]
def serve(port, handler):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(1024)
    while True:
        c, a = s.accept()
        threading.Thread(target=handler, args=(c,), daemon=True).start()
def holdh(c):
    try:
        c.sendall(b"ok\n"); c.settimeout(600)
        while c.recv(64):
            pass
    except Exception:
        pass
    c.close()
def bulk(c):
    c.settimeout(60)
    try:
        f = c.makefile("rb")
        cmd, n = f.readline().split()
        n = int(n)
        if cmd == b"D":
            chunk = b"\x5a" * 65536
            while n > 0:
                k = min(n, len(chunk)); c.sendall(chunk[:k]); n -= k
        else:
            while n > 0:
                d = f.read1(min(n, 65536)) if hasattr(f, "read1") else c.recv(65536)
                if not d:
                    break
                n -= len(d)
            c.sendall(b"done\n")
    except Exception:
        pass
    try:
        c.shutdown(socket.SHUT_WR)
        c.recv(1)
    except Exception:
        pass
    c.close()
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain(base + "/dest.crt", base + "/dest.key")
ctx.set_alpn_protocols(["h2", "http/1.1"])
def tls(c):
    c.settimeout(10)
    try:
        t = ctx.wrap_socket(c, server_side=True)
        t.sendall(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
        t.close()
    except Exception:
        c.close()
def dns(ip):
    u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    u.bind((ip, 53))
    while True:
        q, a = u.recvfrom(2048)
        if len(q) < 12:
            continue
        qid = q[:2]
        i = 12
        while q[i] != 0:
            i += 1 + q[i]
        question = q[12:i + 5]
        ans = b"\xc0\x0c" + struct.pack(">HHIH", 1, 1, 60, 4) + socket.inet_aton(ip)
        u.sendto(qid + b"\x81\x80" + struct.pack(">HHHH", 1, 1, 0, 0) + question + ans, a)
threading.Thread(target=serve, args=(9100, holdh), daemon=True).start()
threading.Thread(target=serve, args=(9200, bulk), daemon=True).start()
threading.Thread(target=serve, args=(8443, tls), daemon=True).start()
threading.Thread(target=dns, args=("12.0.1.3",), daemon=True).start()
dns("12.0.1.2")
`

// smClientPy: socks5 helper. tcp <port>: one CONNECT to 12.0.1.2:9100 →
// "ok"; hold <port> <n>: n streams kept until stdin closes ("watch s" reports
// how many closed within s); bulk <port> D|U <bytes> <parallel>; dns <port>:
// UDP ASSOCIATE, 4 queries alternating 12.0.1.2 / 12.0.1.3, each answer must
// carry the queried server's address.
const smClientPy = `import json, os, socket, struct, sys, threading, time
def socks(port, host, dport, timeout=8):
    s = socket.create_connection(("127.0.0.1", port), timeout=timeout)
    s.sendall(b"\x05\x01\x00"); r = s.recv(2)
    s.sendall(b"\x05\x01\x00\x01" + socket.inet_aton(host) + struct.pack(">H", dport))
    r = b""
    while len(r) < 10:
        d = s.recv(10 - len(r))
        if not d:
            raise OSError("socks closed")
        r += d
    if r[1] != 0:
        raise OSError("socks reply %d" % r[1])
    return s
def ok_stream(port):
    t0 = time.time()
    s = socks(port, "12.0.1.2", 9100)
    s.settimeout(8)
    d = s.recv(3)
    return s, d == b"ok\n", round((time.time() - t0) * 1000, 1)
cmd, port = sys.argv[1], int(sys.argv[2])
if cmd == "tcp":
    try:
        s, ok, ms = ok_stream(port); s.close()
        print(json.dumps({"ok": ok, "ms": ms}))
    except Exception as e:
        print(json.dumps({"ok": False, "error": str(e)}))
elif cmd == "hold":
    n = int(sys.argv[3]); held, res = [], []
    for i in range(n):
        try:
            s, ok, ms = ok_stream(port)
            res.append(ok)
            if ok:
                held.append(s)
        except Exception as e:
            res.append(False)
        time.sleep(0.3)
    print(json.dumps({"ok": len(held), "results": res}), flush=True)
    for line in sys.stdin:
        if line.startswith("watch"):
            secs = float(line.split()[1]); t0 = time.time(); closed = set()
            while time.time() - t0 < secs and len(closed) < len(held):
                for i, s in enumerate(held):
                    if i in closed:
                        continue
                    s.setblocking(False)
                    try:
                        if s.recv(1) == b"":
                            closed.add(i)
                    except BlockingIOError:
                        pass
                    except OSError:
                        closed.add(i)
                time.sleep(0.2)
            print(json.dumps({"held": len(held), "closed": len(closed), "within_s": round(time.time() - t0, 1)}), flush=True)
    for s in held:
        s.close()
elif cmd == "bulk":
    way, size, par = sys.argv[3], int(sys.argv[4]), int(sys.argv[5])
    got, errs = [0] * par, []
    def one(i):
        try:
            s = socks(port, "12.0.1.2", 9200, timeout=30); s.settimeout(60)
            s.sendall(("%s %d\n" % (way, size)).encode())
            if way == "D":
                while got[i] < size:
                    d = s.recv(65536)
                    if not d:
                        break
                    got[i] += len(d)
            else:
                chunk = b"\x33" * 65536; left = size
                while left > 0:
                    k = min(left, len(chunk)); s.sendall(chunk[:k]); left -= k
                if s.recv(5).startswith(b"done"):
                    got[i] = size
            s.close()
        except Exception as e:
            errs.append(str(e))
    t0 = time.time()
    th = [threading.Thread(target=one, args=(i,)) for i in range(par)]
    [x.start() for x in th]; [x.join() for x in th]
    secs = time.time() - t0; total = sum(got)
    print(json.dumps({"bytes": total, "secs": round(secs, 2), "mbps": round(total * 8 / secs / 1e6, 1), "complete": total == size * par, "errors": errs[:3]}))
elif cmd == "dns":
    try:
        c = socket.create_connection(("127.0.0.1", port), timeout=8)
        c.sendall(b"\x05\x01\x00"); c.recv(2)
        c.sendall(b"\x05\x03\x00\x01" + b"\0\0\0\0" + b"\0\0")
        r = c.recv(10)
        if r[1] != 0:
            raise OSError("udp associate %d" % r[1])
        relay = (socket.inet_ntoa(r[4:8]), struct.unpack(">H", r[8:10])[0])
        if relay[0] == "0.0.0.0":
            relay = ("127.0.0.1", relay[1])
        u = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); u.settimeout(4)
        res = []
        for i in range(4):
            server = ("12.0.1.2", "12.0.1.3")[i % 2]
            qid = struct.pack(">H", 0x4000 + i)
            q = qid + b"\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00" + b"\x03lab\x04test\x00" + b"\x00\x01\x00\x01"
            u.sendto(b"\0\0\0\x01" + socket.inet_aton(server) + struct.pack(">H", 53) + q, relay)
            try:
                d, _ = u.recvfrom(2048)
                src = socket.inet_ntoa(d[4:8]); body = d[10:]
                answer = socket.inet_ntoa(body[-4:]) if len(body) >= 16 else ""
                res.append({"server": server, "from": src, "a": answer, "ok": answer == server and body[:2] == qid})
            except Exception as e:
                res.append({"server": server, "ok": False, "error": str(e)})
        print(json.dumps({"ok": all(x["ok"] for x in res), "queries": res}))
    except Exception as e:
        print(json.dumps({"ok": False, "error": str(e)}))
`

// smPoolPy: what an entry's warm pool does for a 端口转发 rule — pre-dial 8
// silent TCP conns per landing port, keep them HOLD s, then serve a local
// port (25000 + landing port − 31000 …) that pipes each client connection
// over one aged conn; 26xxx dials fresh. One untouched conn per port measures
// the effective hold.
const smPoolPy = `import json, socket, sys, threading, time
HOLD, OUT, PORTS = float(sys.argv[1]), sys.argv[2], [int(p) for p in sys.argv[3:]]
LAND = "11.0.1.1"; N = 8
res = {"ports": PORTS}
def dial(p):
    s = socket.create_connection((LAND, p), timeout=5)
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
pools = {p: [dial(p) for _ in range(N)] for p in PORTS}
watch = {p: dial(p) for p in PORTS}
done = {}
def watcher(p):
    w = watch[p]
    try:
        w.settimeout(420)
        d = w.recv(1)
        done[p] = {"closed_after_s": round(time.time() - t0, 1), "how": "fin" if d == b"" else "data"}
    except Exception as e:
        done[p] = {"closed_after_s": round(time.time() - t0, 1), "how": "error:%s" % type(e).__name__}
for p in PORTS:
    threading.Thread(target=watcher, args=(p,), daemon=True).start()
time.sleep(HOLD)
res["aged_s"] = round(time.time() - t0, 1)
res["alive"] = {str(p): [alive(s) for s in pools[p]] for p in PORTS}
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
lock = threading.Lock()
used = {p: 0 for p in PORTS}
def serve(local, take):
    l = socket.socket(); l.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); l.bind(("127.0.0.1", local)); l.listen(64)
    while True:
        c, _ = l.accept()
        u = take()
        if u is None:
            c.close(); continue
        threading.Thread(target=pipe, args=(c, u), daemon=True).start()
        threading.Thread(target=pipe, args=(u, c), daemon=True).start()
def taker(p):
    def take():
        with lock:
            if not pools[p]:
                return None
            used[p] += 1
            return pools[p].pop(0)
    return take
for p in PORTS:
    threading.Thread(target=serve, args=(25000 + p - 31000, taker(p)), daemon=True).start()
    threading.Thread(target=serve, args=(26000 + p - 31000, lambda p=p: dial(p)), daemon=True).start()
res["ready"] = True
json.dump(res, open(OUT, "w"))
while len(done) < len(PORTS):
    time.sleep(1)
res["watch"] = {str(p): done[p] for p in PORTS}
res["pooled_used"] = {str(p): used[p] for p in PORTS}
res["watch_done"] = True
json.dump(res, open(OUT, "w"))
time.sleep(3600)
`
