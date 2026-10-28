package landing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestUserLimitsNetnsE2E (plan 158) runs the pinned Core in four network
// namespaces: client (11.0.1.50) and relay (11.0.2.60 = one of our FLVX nodes)
// → landing → target. Two nodes of user 7 share the Core group
// landing-user-7; node 3 belongs to another user. It proves:
//   - the IP reserve keeps a slot for the relay while no direct IP fits;
//   - relayed connections are counted apart (usage) and never use the user's
//     total budget: budget 2 admits exactly 2 direct connections over both
//     nodes while 4 relays stay open;
//   - the per-server cap counts relays (server scope) — limit 7 with 6 open
//     admits 1 more;
//   - another user's node on the same Core is not affected;
//   - a full idle group (budget 0) is blocked, budget 1 admits exactly 1.
//
// Needs root, iproute2, python3, FLVX_LANDING_TEST_BINARY (pinned Core) and
// FLVX_LANDING_NETNS_E2E=1. FLVX_USERLIMITS_E2E_OUT writes the numbers.
func TestUserLimitsNetnsE2E(t *testing.T) {
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
	nss := []string{"flvxul-c", "flvxul-r", "flvxul-l", "flvxul-t"}
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
	link("flvxul-c", "flvxul-c0", "11.0.1.50/24", "flvxul-l", "flvxul-l0", "11.0.1.1/24")
	link("flvxul-r", "flvxul-r0", "11.0.2.60/24", "flvxul-l", "flvxul-l1", "11.0.2.1/24")
	link("flvxul-l", "flvxul-l2", "12.0.1.1/24", "flvxul-t", "flvxul-t0", "12.0.1.2/24")

	target := `import socket, threading
def hold(c):
    try:
        c.sendall(b"ok\n")
        c.settimeout(300)
        while c.recv(64):
            pass
    except Exception:
        pass
    c.close()
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 9100)); s.listen(256)
while True:
    c, _ = s.accept()
    threading.Thread(target=hold, args=(c,), daemon=True).start()
`
	if err := os.WriteFile(filepath.Join(base, "target.py"), []byte(target), 0644); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command("ip", "netns", "exec", "flvxul-t", "python3", filepath.Join(base, "target.py"))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _, _ = srv.Process.Wait() })

	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxul-l "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, true)
	m.execPath = wrapper
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	n1, n2, n3 := testNode(t, 1), testNode(t, 2), testNode(t, 3)
	n1.Port, n2.Port, n3.Port = 31201, 31202, 31203
	n1.QuotaGroup, n2.QuotaGroup = "landing-user-7", "landing-user-7"
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	go m.runBudgetApplier(ctx)
	desired := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: []Node{n1, n2, n3}, UserLimits: &UserLimits{
		Version: UserLimitsVersion, RelaySources: []string{"11.0.2.60"},
		Groups: []UserGroup{{Group: "landing-user-7", MaxConnections: -1, MaxClientIPs: -1, ServerMaxConnections: -1}},
	}}
	if _, err := m.Apply(ctx, desired); err != nil {
		t.Fatal(err)
	}

	ss := func(n Node, tag, address string) map[string]any {
		return map[string]any{"tag": tag, "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": address, "port": n.Port, "method": SS2022Method, "password": n.SS2022.ServerKey + ":" + n.SS2022.UserKey}}}}
	}
	dokodemo := func(tag string, port int) map[string]any {
		return map[string]any{"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "dokodemo-door", "settings": map[string]any{"address": "12.0.1.2", "port": 9100, "network": "tcp"}}
	}
	startClient := func(ns, name, address string, nodes []Node) {
		var inbounds, outbounds, rules []any
		for i, n := range nodes {
			in, out := fmt.Sprintf("in-%d", i), fmt.Sprintf("out-%d", i)
			inbounds = append(inbounds, dokodemo(in, 21001+i))
			outbounds = append(outbounds, ss(n, out, address))
			rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{in}, "outboundTag": out})
		}
		cfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": inbounds, "outbounds": outbounds, "routing": map[string]any{"rules": rules}})
		path := filepath.Join(base, name+".json")
		if err := os.WriteFile(path, cfg, 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("ip", "netns", "exec", ns, core, "run", "-config", path)
		logf, _ := os.Create(filepath.Join(base, name+".log"))
		cmd.Stdout, cmd.Stderr = logf, logf
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	}
	// client: 21001 → node 1, 21002 → node 2, 21003 → node 3; relay: 21001 → node 1.
	startClient("flvxul-c", "client", "11.0.1.1", []Node{n1, n2, n3})
	startClient("flvxul-r", "relay", "11.0.2.1", []Node{n1})
	time.Sleep(2 * time.Second)

	holdPy := `import json, socket, sys, time
port, n = int(sys.argv[1]), int(sys.argv[2])
held, results = [], []
for i in range(n):
    try:
        s = socket.create_connection(("127.0.0.1", port), timeout=5)
        s.settimeout(4)
        ok = s.recv(3) == b"ok\n"
    except Exception:
        ok = False
    results.append(ok)
    if ok:
        held.append(s)
    else:
        try:
            s.close()
        except Exception:
            pass
    time.sleep(0.3)
print(json.dumps({"ok": len(held), "results": results}), flush=True)
sys.stdin.read()
for s in held:
    s.close()
`
	if err := os.WriteFile(filepath.Join(base, "hold.py"), []byte(holdPy), 0644); err != nil {
		t.Fatal(err)
	}
	type holder struct {
		accepted int
		stdin    io.WriteCloser
		cmd      *exec.Cmd
	}
	var open []*holder
	hold := func(ns string, port, n int) int {
		t.Helper()
		cmd := exec.Command("ip", "netns", "exec", ns, "python3", filepath.Join(base, "hold.py"), strconv.Itoa(port), strconv.Itoa(n))
		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			OK int `json:"ok"`
		}
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			t.Fatalf("%v %q", err, line)
		}
		h := &holder{accepted: out.OK, stdin: stdin, cmd: cmd}
		open = append(open, h)
		return out.OK
	}
	closeAll := func() {
		for _, h := range open {
			_ = h.stdin.Close()
			_ = h.cmd.Wait()
		}
		open = nil
	}
	t.Cleanup(closeAll)
	usage := func() GroupUsage {
		_ = m.Counters() // the 5 s flow-upload sample
		for _, u := range m.GroupUsages() {
			if u.Group == "landing-user-7" {
				return u
			}
		}
		return GroupUsage{}
	}
	// coreGroup reads the group's applied limits straight from the Core.
	coreGroup := func() (conns, ips *int64, current int64) {
		transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", m.socket())
		}}
		defer transport.CloseIdleConnections()
		res, err := (&http.Client{Transport: transport, Timeout: 4 * time.Second}).Get("http://landing-core/v1/snapshot")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var s struct {
			Groups []struct {
				Group   string `json:"group"`
				Current int64  `json:"inbound_current"`
				Conns   *int64 `json:"max_inbound_connections"`
				IPs     *int64 `json:"max_inbound_online_ips"`
			} `json:"management_groups"`
		}
		_ = json.NewDecoder(res.Body).Decode(&s)
		for _, g := range s.Groups {
			if g.Group == "landing-user-7" {
				return g.Conns, g.IPs, g.Current
			}
		}
		return nil, nil, 0
	}
	waitApplied := func(want func(conns, ips *int64) bool) {
		t.Helper()
		for i := 0; i < 50; i++ {
			c, ip, _ := coreGroup()
			if want(c, ip) {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		c, ip, _ := coreGroup()
		t.Fatalf("budget not applied: conns=%v ips=%v", c, ip)
	}
	is := func(p *int64, v int64) bool { return p != nil && *p == v }
	budget := func(b GroupBudget) {
		t.Helper()
		b.Group = "landing-user-7"
		if err := m.SetGroupBudgets([]GroupBudget{b}); err != nil {
			t.Fatal(err)
		}
	}
	results := map[string]any{"core": CoreVersion}

	// 1. IP reserve: no direct IP fits (budget 0) but the announced relay keeps a slot.
	budget(GroupBudget{MaxConnections: -1, MaxClientIPs: 0, ServerMaxConnections: -1, RelayIPs: 1})
	waitApplied(func(c, ip *int64) bool { return c == nil && is(ip, 1) })
	relayIP := hold("flvxul-r", 21001, 1)
	directIP := hold("flvxul-c", 21001, 1)
	budget(GroupBudget{MaxConnections: -1, MaxClientIPs: 1, ServerMaxConnections: -1, RelayIPs: 1})
	waitApplied(func(c, ip *int64) bool { return c == nil && is(ip, 2) })
	directIP2 := hold("flvxul-c", 21001, 1)
	results["ip_reserve"] = map[string]int{"relay_accepted": relayIP, "direct_with_0_ips": directIP, "direct_with_1_ip": directIP2}
	t.Logf("IP reserve: relay %d/1, direct with 0 IPs %d/1, direct with 1 IP %d/1", relayIP, directIP, directIP2)
	if relayIP != 1 || directIP != 0 || directIP2 != 1 {
		t.Fatalf("IP reserve: %+v", results["ip_reserve"])
	}
	closeAll()
	time.Sleep(7 * time.Second)

	// 2. Relays never use the user's total: 4 relays open, budget 2 → 2 direct over both nodes.
	budget(GroupBudget{MaxConnections: -1, MaxClientIPs: -1, ServerMaxConnections: -1})
	waitApplied(func(c, ip *int64) bool { return c == nil && ip == nil })
	relays := hold("flvxul-r", 21001, 4)
	u := usage()
	budget(GroupBudget{MaxConnections: 2, MaxClientIPs: -1, ServerMaxConnections: -1})
	waitApplied(func(c, _ *int64) bool { return is(c, 2+int64(u.RelayConnections)) })
	d1 := hold("flvxul-c", 21001, 2)
	d2 := hold("flvxul-c", 21002, 2)
	after := usage()
	results["total_dedupe"] = map[string]any{"relays_open": relays, "relay_counted": u.RelayConnections, "direct_node1": d1, "direct_node2": d2, "usage": after}
	t.Logf("total dedupe: relays %d (counted %d), direct node1 %d + node2 %d, usage %+v", relays, u.RelayConnections, d1, d2, after)
	if relays != 4 || u.RelayConnections != 4 || u.Connections != 0 || d1+d2 != 2 || after.Connections != 2 || after.RelayConnections != 4 || len(after.ClientIPs) != 1 || after.ClientIPs[0] != "11.0.1.50" {
		t.Fatalf("total dedupe: %+v", results["total_dedupe"])
	}

	// 3. The per-server cap counts what the Core receives (relays included).
	budget(GroupBudget{MaxConnections: -1, MaxClientIPs: -1, ServerMaxConnections: 7})
	waitApplied(func(c, _ *int64) bool { return is(c, 7) })
	d3 := hold("flvxul-c", 21002, 2)
	other := hold("flvxul-c", 21003, 3)
	results["server_cap"] = map[string]int{"open_before": 6, "cap": 7, "accepted_of_2": d3, "other_user_node_of_3": other}
	t.Logf("server cap 7 with 6 open: %d/2 accepted; other user's node %d/3", d3, other)
	if d3 != 1 || other != 3 {
		t.Fatalf("server cap: %+v", results["server_cap"])
	}
	closeAll()
	for i := 0; i < 30; i++ {
		if _, _, cur := coreGroup(); cur == 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = usage()

	// 4. A full idle group is blocked (the Core reads 0 as unlimited); budget 1 admits 1.
	budget(GroupBudget{MaxConnections: 0, MaxClientIPs: -1, ServerMaxConnections: -1})
	time.Sleep(time.Second)
	full := hold("flvxul-c", 21001, 1)
	closeAll()
	budget(GroupBudget{MaxConnections: 1, MaxClientIPs: -1, ServerMaxConnections: -1})
	waitApplied(func(c, _ *int64) bool { return is(c, 1) })
	one := hold("flvxul-c", 21001, 2)
	results["full_group"] = map[string]int{"budget_0_accepted_of_1": full, "budget_1_accepted_of_2": one}
	t.Logf("full idle group: %d/1 with budget 0, %d/2 with budget 1", full, one)
	if full != 0 || one != 1 {
		t.Fatalf("full group: %+v", results["full_group"])
	}
	closeAll()
	if out := os.Getenv("FLVX_USERLIMITS_E2E_OUT"); out != "" {
		raw, _ := json.MarshalIndent(results, "", "  ")
		_ = os.WriteFile(out, raw, 0644)
	}
}
