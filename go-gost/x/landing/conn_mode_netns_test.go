package landing

import (
	"bufio"
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

// TestConnModeNetnsE2E is the plan-141 lab check with the pinned Core in three
// network namespaces (client → landing → target):
//   - 仅入站 node, limit 3: 3 of 5 held client connections are accepted although
//     the Core holds ≥ 3 outbound sockets as well (current_total ≥ 6); the 4th
//     and 5th are rejected; right after closing them 3 new ones fit again;
//   - 入站+出站 node, limit 3 (today's behaviour): in+out sockets share the
//     limit, so far fewer client connections fit.
//
// Needs root, iproute2, python3, FLVX_LANDING_TEST_BINARY (pinned Core) and
// FLVX_LANDING_NETNS_E2E=1. FLVX_CONNMODE_E2E_OUT writes the numbers as JSON.
func TestConnModeNetnsE2E(t *testing.T) {
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
	nss := []string{"flvxcm-c", "flvxcm-l", "flvxcm-t"}
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
	sh("ip", "link", "add", "flvxcm-c0", "type", "veth", "peer", "name", "flvxcm-l0")
	sh("ip", "link", "set", "flvxcm-c0", "netns", "flvxcm-c")
	sh("ip", "link", "set", "flvxcm-l0", "netns", "flvxcm-l")
	sh("ip", "link", "add", "flvxcm-l1", "type", "veth", "peer", "name", "flvxcm-t0")
	sh("ip", "link", "set", "flvxcm-l1", "netns", "flvxcm-l")
	sh("ip", "link", "set", "flvxcm-t0", "netns", "flvxcm-t")
	sh("ip", "-n", "flvxcm-c", "addr", "add", "203.0.113.50/24", "dev", "flvxcm-c0")
	sh("ip", "-n", "flvxcm-l", "addr", "add", "203.0.113.1/24", "dev", "flvxcm-l0")
	sh("ip", "-n", "flvxcm-l", "addr", "add", "198.51.100.1/24", "dev", "flvxcm-l1")
	sh("ip", "-n", "flvxcm-t", "addr", "add", "198.51.100.2/24", "dev", "flvxcm-t0")
	for _, l := range [][2]string{{"flvxcm-c", "flvxcm-c0"}, {"flvxcm-l", "flvxcm-l0"}, {"flvxcm-l", "flvxcm-l1"}, {"flvxcm-t", "flvxcm-t0"}} {
		sh("ip", "-n", l[0], "link", "set", l[1], "up")
	}

	// Target: greets every connection and holds it until the peer closes, so
	// each accepted client connection keeps one outbound socket in the Core.
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
	srv := exec.Command("ip", "netns", "exec", "flvxcm-t", "python3", filepath.Join(base, "target.py"))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _, _ = srv.Process.Wait() })

	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxcm-l "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, true)
	m.execPath = wrapper
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	inbound, total := testNode(t, 1), testNode(t, 2)
	inbound.Port, inbound.MaxConnections, inbound.ConnMode = 31101, 3, ConnModeInbound
	total.Port, total.MaxConnections = 31102, 3
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := m.Apply(ctx, Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: []Node{inbound, total}}); err != nil {
		t.Fatal(err)
	}

	// Client: the same Core as SS2022 client, one local port per landing node.
	ss := func(n Node, tag string) map[string]any {
		return map[string]any{"tag": tag, "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": "203.0.113.1", "port": n.Port, "method": SS2022Method, "password": n.SS2022.ServerKey + ":" + n.SS2022.UserKey}}}}
	}
	dokodemo := func(tag string, port int) map[string]any {
		return map[string]any{"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "dokodemo-door", "settings": map[string]any{"address": "198.51.100.2", "port": 9100, "network": "tcp"}}
	}
	clientCfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning"},
		"inbounds":  []any{dokodemo("in-inbound", 21001), dokodemo("in-total", 21002)},
		"outbounds": []any{ss(inbound, "out-inbound"), ss(total, "out-total")},
		"routing":   map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"in-inbound"}, "outboundTag": "out-inbound"}, map[string]any{"type": "field", "inboundTag": []string{"in-total"}, "outboundTag": "out-total"}}}})
	if err := os.WriteFile(filepath.Join(base, "client.json"), clientCfg, 0600); err != nil {
		t.Fatal(err)
	}
	client := exec.Command("ip", "netns", "exec", "flvxcm-c", core, "run", "-config", filepath.Join(base, "client.json"))
	clientLog, _ := os.Create(filepath.Join(base, "client.log"))
	client.Stdout, client.Stderr = clientLog, clientLog
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Process.Kill(); _, _ = client.Process.Wait() })
	time.Sleep(2 * time.Second)

	// hold.py <port> <n>: opens n connections one by one, keeps the greeted
	// ones, prints {"ok": k, "results": [...]}, then holds until stdin closes.
	holdPy := `import json, socket, sys, time
port, n = int(sys.argv[1]), int(sys.argv[2])
held, results = [], []
for i in range(n):
    s = socket.create_connection(("127.0.0.1", port), timeout=5)
    s.settimeout(4)
    try:
        ok = s.recv(3) == b"ok\n"
    except Exception:
        ok = False
    results.append(ok)
    if ok:
        held.append(s)
    else:
        s.close()
    time.sleep(0.3)
print(json.dumps({"ok": len(held), "results": results}), flush=True)
sys.stdin.read()
for s in held:
    s.close()
`
	if err := os.WriteFile(filepath.Join(base, "hold.py"), []byte(holdPy), 0644); err != nil {
		t.Fatal(err)
	}
	type phase struct {
		Name        string `json:"name"`
		Opened      int    `json:"opened"`
		Accepted    int    `json:"accepted"`
		Results     []bool `json:"results"`
		CoreTotal   int64  `json:"core_current_total"`
		CoreInbound int64  `json:"core_inbound_current"`
	}
	counter := func(id int64) (int64, int64) {
		for i := 0; i < 20; i++ {
			for _, c := range m.Counters() {
				if c.ID == id && c.InboundConnections != nil {
					return c.Connections, *c.InboundConnections
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("no counter")
		return 0, 0
	}
	run := func(name string, port, n int, id int64) phase {
		t.Helper()
		cmd := exec.Command("ip", "netns", "exec", "flvxcm-c", "python3", filepath.Join(base, "hold.py"), strconv.Itoa(port), strconv.Itoa(n))
		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var out struct {
			OK      int    `json:"ok"`
			Results []bool `json:"results"`
		}
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			t.Fatalf("%s: %v %q", name, err, line)
		}
		time.Sleep(500 * time.Millisecond)
		totalNow, inboundNow := counter(id)
		_ = stdin.Close()
		_ = cmd.Wait()
		p := phase{Name: name, Opened: n, Accepted: out.OK, Results: out.Results, CoreTotal: totalNow, CoreInbound: inboundNow}
		t.Logf("%s: accepted %d/%d %v, Core while held: current_total=%d inbound_current=%d", name, p.Accepted, n, p.Results, totalNow, inboundNow)
		return p
	}
	a := run("仅入站 limit 3, open 5", 21001, 5, 1)
	time.Sleep(time.Second)
	b := run("仅入站 limit 3, reopen 3 right after closing", 21001, 3, 1)
	c := run("入站+出站 limit 3, open 5", 21002, 5, 2)
	if a.Accepted != 3 || a.CoreInbound != 3 || a.CoreTotal < 6 {
		t.Fatalf("仅入站: %+v", a)
	}
	if b.Accepted != 3 {
		t.Fatalf("仅入站 reopen: %+v", b)
	}
	if c.Accepted >= 3 {
		t.Fatalf("入站+出站 accepted %d of limit 3 (in+out should share it): %+v", c.Accepted, c)
	}
	if out := os.Getenv("FLVX_CONNMODE_E2E_OUT"); out != "" {
		raw, _ := json.MarshalIndent(map[string]any{"core": CoreVersion, "phases": []phase{a, b, c}}, "", "  ")
		_ = os.WriteFile(out, raw, 0644)
	}
}
