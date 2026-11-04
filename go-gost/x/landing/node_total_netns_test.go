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

// TestNodeTotalNetnsE2E is the plan-211 lab check with the pinned Core in
// three network namespaces (client → landing → target):
//   - node 1, 节点总连接数 8 sent the panel-only way (max_connections without
//     conn_mode = Core port max_total_connections): every accepted client
//     costs 2 sockets (inbound + outbound) → 4 of 6 fit; after the clients
//     close, the Core's outbound sockets sit in TIME_WAIT and still count
//     (Counter.Connections and TCP.TimeWait), so new clients are refused until
//     the kernel drops them;
//   - node 2, target half-closes: the Core's outbound sockets in CLOSE_WAIT
//     are counted too;
//   - node 3, 入站 3 + 总 5 (landing_conn_mode 2 field): the total is hit
//     first (2 clients = 4 sockets, the 3rd would make 6);
//   - node 4, 入站 2 + 总 100: the inbound limit is hit first.
//
// Needs root, iproute2, python3, FLVX_LANDING_TEST_BINARY (pinned Core) and
// FLVX_LANDING_NETNS_E2E=1. FLVX_NODETOTAL_E2E_OUT writes the numbers as JSON.
func TestNodeTotalNetnsE2E(t *testing.T) {
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
	nss := []string{"flvxnt-c", "flvxnt-l", "flvxnt-t"}
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
	sh("ip", "link", "add", "flvxnt-c0", "type", "veth", "peer", "name", "flvxnt-l0")
	sh("ip", "link", "set", "flvxnt-c0", "netns", "flvxnt-c")
	sh("ip", "link", "set", "flvxnt-l0", "netns", "flvxnt-l")
	sh("ip", "link", "add", "flvxnt-l1", "type", "veth", "peer", "name", "flvxnt-t0")
	sh("ip", "link", "set", "flvxnt-l1", "netns", "flvxnt-l")
	sh("ip", "link", "set", "flvxnt-t0", "netns", "flvxnt-t")
	sh("ip", "-n", "flvxnt-c", "addr", "add", "11.0.2.50/24", "dev", "flvxnt-c0")
	sh("ip", "-n", "flvxnt-l", "addr", "add", "11.0.2.1/24", "dev", "flvxnt-l0")
	sh("ip", "-n", "flvxnt-l", "addr", "add", "12.0.2.1/24", "dev", "flvxnt-l1")
	sh("ip", "-n", "flvxnt-t", "addr", "add", "12.0.2.2/24", "dev", "flvxnt-t0")
	for _, l := range [][2]string{{"flvxnt-c", "flvxnt-c0"}, {"flvxnt-l", "flvxnt-l0"}, {"flvxnt-l", "flvxnt-l1"}, {"flvxnt-t", "flvxnt-t0"}} {
		sh("ip", "-n", l[0], "link", "set", l[1], "up")
	}

	// Target: 9100 greets and holds until the peer closes; 9101 greets and
	// half-closes at once (the Core's outbound socket enters CLOSE_WAIT).
	target := `import socket, threading
def hold(c, half):
    try:
        c.sendall(b"ok\n")
        if half:
            c.shutdown(socket.SHUT_WR)
        c.settimeout(300)
        while c.recv(64):
            pass
    except Exception:
        pass
    c.close()
def serve(port, half):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("0.0.0.0", port)); s.listen(256)
    while True:
        c, _ = s.accept()
        threading.Thread(target=hold, args=(c, half), daemon=True).start()
threading.Thread(target=serve, args=(9101, True), daemon=True).start()
serve(9100, False)
`
	if err := os.WriteFile(filepath.Join(base, "target.py"), []byte(target), 0644); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command("ip", "netns", "exec", "flvxnt-t", "python3", filepath.Join(base, "target.py"))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _, _ = srv.Process.Wait() })

	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxnt-l "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, true)
	m.execPath = wrapper
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	n1, n2, n3, n4 := testNode(t, 1), testNode(t, 2), testNode(t, 3), testNode(t, 4)
	n1.Port, n1.MaxConnections = 31201, 8 // panel-only path: no conn_mode
	n2.Port, n2.MaxConnections = 31202, 100
	n3.Port, n3.MaxConnections, n3.ConnMode, n3.MaxTotalConnections = 31203, 3, ConnModeInbound, 5
	n4.Port, n4.MaxConnections, n4.ConnMode, n4.MaxTotalConnections = 31204, 2, ConnModeInbound, 100
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := m.Apply(ctx, Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: []Node{n1, n2, n3, n4}}); err != nil {
		t.Fatal(err)
	}

	ss := func(n Node, tag string) map[string]any {
		return map[string]any{"tag": tag, "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": "11.0.2.1", "port": n.Port, "method": SS2022Method, "password": n.SS2022.ServerKey + ":" + n.SS2022.UserKey}}}}
	}
	dokodemo := func(tag string, port, targetPort int) map[string]any {
		return map[string]any{"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "dokodemo-door", "settings": map[string]any{"address": "12.0.2.2", "port": targetPort, "network": "tcp"}}
	}
	route := func(in, out string) map[string]any {
		return map[string]any{"type": "field", "inboundTag": []string{in}, "outboundTag": out}
	}
	clientCfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning"},
		"inbounds":  []any{dokodemo("in1", 22001, 9100), dokodemo("in2", 22002, 9101), dokodemo("in3", 22003, 9100), dokodemo("in4", 22004, 9100)},
		"outbounds": []any{ss(n1, "out1"), ss(n2, "out2"), ss(n3, "out3"), ss(n4, "out4")},
		"routing":   map[string]any{"rules": []any{route("in1", "out1"), route("in2", "out2"), route("in3", "out3"), route("in4", "out4")}}})
	if err := os.WriteFile(filepath.Join(base, "client.json"), clientCfg, 0600); err != nil {
		t.Fatal(err)
	}
	client := exec.Command("ip", "netns", "exec", "flvxnt-c", core, "run", "-config", filepath.Join(base, "client.json"))
	clientLog, _ := os.Create(filepath.Join(base, "client.log"))
	client.Stdout, client.Stderr = clientLog, clientLog
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Process.Kill(); _, _ = client.Process.Wait() })
	time.Sleep(2 * time.Second)

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
	type sample struct {
		AtMs        int64      `json:"at_ms"`
		Connections int64      `json:"connections"`
		Inbound     int64      `json:"inbound_current"`
		TCP         *TCPCounts `json:"tcp"`
	}
	start := time.Now()
	counter := func(id int64) sample {
		for i := 0; i < 20; i++ {
			for _, c := range m.Counters() {
				if c.ID == id && c.InboundConnections != nil {
					return sample{time.Since(start).Milliseconds(), c.Connections, *c.InboundConnections, c.TCP}
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("no counter")
		return sample{}
	}
	type phase struct {
		Name     string `json:"name"`
		Opened   int    `json:"opened"`
		Accepted int    `json:"accepted"`
		Results  []bool `json:"results"`
		Held     sample `json:"while_held"`
	}
	// run opens n clients, samples while they are held, then closes them
	// (close=false keeps them until the returned func is called).
	run := func(name string, port, n int, id int64) (phase, func()) {
		t.Helper()
		cmd := exec.Command("ip", "netns", "exec", "flvxnt-c", "python3", filepath.Join(base, "hold.py"), strconv.Itoa(port), strconv.Itoa(n))
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
		time.Sleep(300 * time.Millisecond)
		p := phase{Name: name, Opened: n, Accepted: out.OK, Results: out.Results, Held: counter(id)}
		t.Logf("%s: accepted %d/%d %v, while held: total=%d inbound=%d tcp=%+v", name, p.Accepted, n, p.Results, p.Held.Connections, p.Held.Inbound, p.Held.TCP)
		return p, func() { _ = stdin.Close(); _ = cmd.Wait() }
	}

	a, closeA := run("总 8 (panel-only), open 6", 22001, 6, 1)
	closeA()
	// After the clients close, the Core closes its outbound sockets first →
	// they wait in TIME_WAIT (60 s on Linux) and keep counting.
	var tw sample
	for i := 0; i < 20; i++ {
		tw = counter(1)
		if tw.TCP != nil && tw.TCP.TimeWait > 0 && tw.Inbound == 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("after close: %+v tcp=%+v", tw, tw.TCP)
	b, closeB := run("总 8 while TIME_WAIT lingers, open 4", 22001, 4, 1)
	closeB()
	c, closeC := run("half-closing target, open 3", 22002, 3, 2)
	closeC()
	d, closeD := run("入站 3 + 总 5, open 4", 22003, 4, 3)
	closeD()
	e, closeE := run("入站 2 + 总 100, open 4", 22004, 4, 4)
	closeE()

	if a.Accepted != 4 || a.Held.Connections != 8 || a.Held.TCP == nil || a.Held.TCP.Inbound != 4 || a.Held.TCP.Outbound != 4 {
		t.Fatalf("总 8: %+v tcp=%+v", a, a.Held.TCP)
	}
	if tw.TCP == nil || tw.TCP.TimeWait == 0 || tw.Connections < tw.TCP.TimeWait || tw.Connections != tw.TCP.Inbound+tw.TCP.Outbound {
		t.Fatalf("TIME_WAIT not counted in the total: %+v tcp=%+v", tw, tw.TCP)
	}
	if want := 8 - int(tw.Connections); b.Accepted > (want+1)/2 {
		t.Fatalf("TIME_WAIT sockets did not count against the cap: %d accepted with %d lingering", b.Accepted, tw.Connections)
	}
	if c.Accepted != 3 || c.Held.TCP == nil || c.Held.TCP.CloseWait == 0 || c.Held.Connections != c.Held.TCP.Inbound+c.Held.TCP.Outbound {
		t.Fatalf("CLOSE_WAIT not counted: %+v tcp=%+v", c, c.Held.TCP)
	}
	if d.Accepted != 2 {
		t.Fatalf("入站 3 + 总 5: total should be hit first (2 accepted): %+v", d)
	}
	if e.Accepted != 2 || e.Held.Inbound != 2 {
		t.Fatalf("入站 2 + 总 100: inbound should be hit first: %+v", e)
	}
	if out := os.Getenv("FLVX_NODETOTAL_E2E_OUT"); out != "" {
		raw, _ := json.MarshalIndent(map[string]any{"core": CoreVersion, "phases": []phase{a, b, c, d, e}, "after_close": tw}, "", "  ")
		_ = os.WriteFile(out, raw, 0644)
	}
}
