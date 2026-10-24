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

// TestOnlineIPv6NetnsE2E (plan 129): the native landing Core counts IPv6
// clients per /64, like the 妙妙屋 shared Core: three IPv6 clients, two in
// 2001:db8:1:1::/64 and one in 2001:db8:1:2::/64, plus one IPv4 client, hold
// SS2022 connections; the agent reports 3 online IPs, the /64 entries as
// "2001:db8:1:1::/64" (what the panel now lists). Needs root, iproute2,
// python3, FLVX_LANDING_TEST_BINARY and FLVX_LANDING_NETNS_E2E=1.
func TestOnlineIPv6NetnsE2E(t *testing.T) {
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
	nss := []string{"flvx6-c", "flvx6-l", "flvx6-t"}
	var procs []*exec.Cmd
	cleanup := func() {
		for _, p := range procs {
			_ = p.Process.Kill()
			_, _ = p.Process.Wait()
		}
		for _, ns := range nss {
			_ = exec.Command("ip", "netns", "del", ns).Run()
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	for _, ns := range nss {
		sh("ip", "netns", "add", ns)
		sh("ip", "-n", ns, "link", "set", "lo", "up")
	}
	sh("ip", "link", "add", "flvx6-c0", "type", "veth", "peer", "name", "flvx6-l0")
	sh("ip", "link", "set", "flvx6-c0", "netns", "flvx6-c")
	sh("ip", "link", "set", "flvx6-l0", "netns", "flvx6-l")
	sh("ip", "link", "add", "flvx6-l1", "type", "veth", "peer", "name", "flvx6-t0")
	sh("ip", "link", "set", "flvx6-l1", "netns", "flvx6-l")
	sh("ip", "link", "set", "flvx6-t0", "netns", "flvx6-t")
	for _, a := range []string{"2001:db8:1:1::10/64", "2001:db8:1:1::11/64", "2001:db8:1:2::10/64"} {
		sh("ip", "-n", "flvx6-c", "addr", "add", a, "dev", "flvx6-c0", "nodad")
	}
	sh("ip", "-n", "flvx6-c", "addr", "add", "203.0.113.60/24", "dev", "flvx6-c0")
	sh("ip", "-n", "flvx6-l", "addr", "add", "2001:db8:1:1::1/64", "dev", "flvx6-l0", "nodad")
	sh("ip", "-n", "flvx6-l", "addr", "add", "2001:db8:1:2::1/64", "dev", "flvx6-l0", "nodad")
	sh("ip", "-n", "flvx6-l", "addr", "add", "203.0.113.1/24", "dev", "flvx6-l0")
	sh("ip", "-n", "flvx6-l", "addr", "add", "198.51.100.1/24", "dev", "flvx6-l1")
	sh("ip", "-n", "flvx6-t", "addr", "add", "198.51.100.2/24", "dev", "flvx6-t0")
	for _, l := range [][2]string{{"flvx6-c", "flvx6-c0"}, {"flvx6-l", "flvx6-l0"}, {"flvx6-l", "flvx6-l1"}, {"flvx6-t", "flvx6-t0"}} {
		sh("ip", "-n", l[0], "link", "set", l[1], "up")
	}
	// Target: accepts and holds TCP connections.
	hold := `import socket, threading, time
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1); s.bind(("198.51.100.2", 9100)); s.listen(64)
keep = []
while True:
    c, a = s.accept(); keep.append(c)
`
	if err := os.WriteFile(filepath.Join(base, "hold.py"), []byte(hold), 0644); err != nil {
		t.Fatal(err)
	}
	start := func(ns, name string, args ...string) {
		cmd := exec.Command("ip", append([]string{"netns", "exec", ns}, args...)...)
		out, _ := os.Create(filepath.Join(base, name+".log"))
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		procs = append(procs, cmd)
	}
	start("flvx6-t", "hold", "python3", filepath.Join(base, "hold.py"))

	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvx6-l "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, true)
	m.execPath = wrapper
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	n := testNode(t, 1)
	n.Port = 31001
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := m.Apply(ctx, Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: []Node{n}}); err != nil {
		t.Fatal(err)
	}
	// Client Core: one SS2022 outbound per source address (sendThrough).
	sources := map[string]string{"a": "2001:db8:1:1::10", "b": "2001:db8:1:1::11", "c": "2001:db8:1:2::10", "d": "203.0.113.60"}
	var inbounds, outbounds, rules []any
	i := 0
	for name, src := range sources {
		server := "2001:db8:1:1::1"
		if strings.HasPrefix(src, "2001:db8:1:2::") {
			server = "2001:db8:1:2::1"
		}
		if !strings.Contains(src, ":") {
			server = "203.0.113.1"
		}
		inbounds = append(inbounds, map[string]any{"tag": "in-" + name, "listen": "127.0.0.1", "port": 21001 + i, "protocol": "dokodemo-door", "settings": map[string]any{"address": "198.51.100.2", "port": 9100, "network": "tcp"}})
		outbounds = append(outbounds, map[string]any{"tag": "out-" + name, "sendThrough": src, "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": server, "port": n.Port, "method": SS2022Method, "password": n.SS2022.ServerKey + ":" + n.SS2022.UserKey}}}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"in-" + name}, "outboundTag": "out-" + name})
		i++
	}
	cfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": inbounds, "outbounds": outbounds, "routing": map[string]any{"rules": rules}})
	if err := os.WriteFile(filepath.Join(base, "client.json"), cfg, 0600); err != nil {
		t.Fatal(err)
	}
	start("flvx6-c", "client", core, "run", "-config", filepath.Join(base, "client.json"))
	time.Sleep(time.Second)
	// Two held connections per source.
	conns := `import socket, sys, time
keep = []
for port in sys.argv[1:]:
    for k in range(2):
        for attempt in range(60):
            try:
                c = socket.create_connection(("127.0.0.1", int(port)), timeout=5); c.sendall(b"hi"); keep.append(c); break
            except OSError:
                time.sleep(0.05)
print("held", len(keep), flush=True)
time.sleep(60)
`
	if err := os.WriteFile(filepath.Join(base, "conns.py"), []byte(conns), 0644); err != nil {
		t.Fatal(err)
	}
	start("flvx6-c", "conns", "python3", filepath.Join(base, "conns.py"), "21001", "21002", "21003", "21004")
	var counter Counter
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		m.mu.Lock()
		err := m.collect(ctx)
		cs := append([]Counter(nil), m.counters...)
		m.mu.Unlock()
		if err != nil || len(cs) == 0 || cs[0].OnlineIPCount == nil {
			continue
		}
		counter = cs[0]
		if *counter.OnlineIPCount >= 3 && len(counter.OnlineIPs) >= 3 {
			break
		}
	}
	got, _ := json.Marshal(counter.OnlineIPs)
	t.Logf("native online IPs: count=%v list=%s", fmtPtr(counter.OnlineIPCount), got)
	want := map[string]int64{"2001:db8:1:1::/64": 4, "2001:db8:1:2::/64": 2, "203.0.113.60": 2}
	if counter.OnlineIPCount == nil || *counter.OnlineIPCount != 3 || len(counter.OnlineIPs) != 3 {
		t.Fatalf("want 3 online IPs (two /64 + one IPv4): %s", got)
	}
	for _, ip := range counter.OnlineIPs {
		if c, ok := want[ip.IP]; !ok || ip.Connections < c {
			t.Errorf("unexpected entry %+v (want %v)", ip, want)
		}
	}
}

func fmtPtr(p *int) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprint(*p)
}
