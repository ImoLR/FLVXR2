//go:build linux

package landing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-gost/x/logsafe"
)

// TestEgressResourceProfile measures the landing Core and the agent-side
// manager on a throwaway setup (one network namespace, real pinned Core):
// RSS/threads/fds/goroutines with 1 and 50 nodes, per-feature deltas of the
// egress plan, 1000 concurrent connections through a forced-IPv4 outbound and
// back, idle CPU, the agent's reconcile cost per tick and disk/log growth.
// Opt-in: FLVX_LANDING_RESOURCE=1 as root with FLVX_LANDING_TEST_BINARY and
// FLVX_LANDING_GEODIR; FLVX_LANDING_RESOURCE_OUT receives the JSON report.
func TestEgressResourceProfile(t *testing.T) {
	if os.Getenv("FLVX_LANDING_RESOURCE") != "1" || os.Geteuid() != 0 {
		t.Skip("set FLVX_LANDING_RESOURCE=1 and run as root")
	}
	base := t.TempDir()
	dir := filepath.Join(base, "landing")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	core := realCore(t, dir)
	if !copyGeo(t, dir) {
		t.Skip("FLVX_LANDING_GEODIR not supplied")
	}
	const ns = "flvxres"
	sh := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	cleanup := func() {
		if out, err := exec.Command("ip", "netns", "pids", ns).Output(); err == nil {
			for _, pid := range strings.Fields(string(out)) {
				_ = exec.Command("kill", "-9", pid).Run()
			}
		}
		_ = exec.Command("ip", "netns", "del", ns).Run()
	}
	cleanup()
	t.Cleanup(cleanup)
	sh("ip", "netns", "add", ns)
	sh("ip", "-n", ns, "link", "set", "lo", "up")
	sh("ip", "-n", ns, "link", "add", "d0", "type", "dummy")
	for _, a := range []string{"11.0.1.1/32", "12.0.1.2/32"} {
		sh("ip", "-n", ns, "addr", "add", a, "dev", "d0")
	}
	sh("ip", "-n", ns, "link", "set", "d0", "up")

	coreLog := filepath.Join(base, "core.log")
	const metricsPort = 39772
	wrapper := filepath.Join(base, "core-in-netns")
	script := fmt.Sprintf(`#!/bin/sh
ulimit -n 65535
if [ "$1" = "run" ]; then
  python3 -c 'import json,sys; c=json.load(open(sys.argv[1])); c["metrics"]={"tag":"metrics","listen":"127.0.0.1:%d"}; json.dump(c,open(sys.argv[2],"w"))' "$3" "$3.metrics.json"
  exec ip netns exec %s %s run -config "$3.metrics.json" >>%s 2>&1
fi
exec ip netns exec %s %s "$@"
`, metricsPort, ns, core, coreLog, ns, core)
	if err := os.WriteFile(wrapper, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	echo := `import asyncio
async def handle(r, w):
    try:
        while True:
            d = await r.read(4096)
            if not d:
                break
            w.write(d)
            await w.drain()
    except Exception:
        pass
    finally:
        w.close()
async def main():
    s = await asyncio.start_server(handle, "12.0.1.2", 8080, backlog=4096)
    await s.serve_forever()
asyncio.run(main())
`
	if err := os.WriteFile(filepath.Join(base, "echo.py"), []byte(echo), 0644); err != nil {
		t.Fatal(err)
	}
	target := exec.Command("ip", "netns", "exec", ns, "sh", "-c", "ulimit -n 65535; exec python3 "+filepath.Join(base, "echo.py"))
	if err := target.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Process.Kill(); _, _ = target.Process.Wait() })

	m := NewManager(dir, true)
	m.execPath = wrapper
	var journal []string
	m.logs = logsafe.NewRoutineLogger(func(line string) { journal = append(journal, line) })
	m.detectLocal = func() localAddrs { return localAddrs{v4: netip.MustParseAddr("11.0.1.1")} }
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	nodes := make([]Node, 0, 50)
	for i := 1; i <= 50; i++ {
		n := testNode(t, int64(i))
		n.Port = 32000 + i
		nodes = append(nodes, n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	revision := int64(0)
	apply := func(nodes []Node, plan *Egress) {
		t.Helper()
		revision++
		status, err := m.Apply(ctx, Desired{Revision: revision, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, Nodes: nodes, Egress: plan})
		if err != nil {
			t.Fatalf("apply revision %d: %v", revision, err)
		}
		if plan != nil && status.Egress.State != "applied" {
			t.Fatalf("plan not applied: %+v", status.Egress)
		}
	}
	type sample struct {
		Label      string  `json:"label"`
		RSSMiB     float64 `json:"rss_mib"`
		Threads    int     `json:"threads"`
		FDs        int     `json:"fds"`
		Goroutines int     `json:"goroutines"`
		HeapMiB    float64 `json:"heap_inuse_mib"`
		CPUSeconds float64 `json:"cpu_seconds_total"`
	}
	nsGet := func(path string) string {
		out, err := exec.Command("ip", "netns", "exec", ns, "python3", "-c", fmt.Sprintf("import urllib.request;print(urllib.request.urlopen('http://127.0.0.1:%d%s',timeout=5).read().decode())", metricsPort, path)).Output()
		if err != nil {
			return ""
		}
		return string(out)
	}
	take := func(label string) sample {
		t.Helper()
		time.Sleep(2 * time.Second)
		pid := m.proc.Pid
		s := sample{Label: label}
		status, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		for _, line := range strings.Split(string(status), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && f[0] == "VmRSS:" {
				kb, _ := strconv.Atoi(f[1])
				s.RSSMiB = float64(kb) / 1024
			}
			if len(f) >= 2 && f[0] == "Threads:" {
				s.Threads, _ = strconv.Atoi(f[1])
			}
		}
		fds, _ := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
		s.FDs = len(fds)
		if g := nsGet("/debug/pprof/goroutine?debug=1"); strings.HasPrefix(g, "goroutine profile: total ") {
			s.Goroutines, _ = strconv.Atoi(strings.Fields(strings.TrimPrefix(g, "goroutine profile: total "))[0])
		}
		var vars struct {
			Memstats struct {
				HeapInuse uint64 `json:"HeapInuse"`
			} `json:"memstats"`
		}
		if json.Unmarshal([]byte(nsGet("/debug/vars")), &vars) == nil {
			s.HeapMiB = float64(vars.Memstats.HeapInuse) / (1 << 20)
		}
		stat, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if i := strings.LastIndexByte(string(stat), ')'); i > 0 {
			f := strings.Fields(string(stat)[i+2:])
			if len(f) > 13 {
				u, _ := strconv.ParseFloat(f[11], 64)
				k, _ := strconv.ParseFloat(f[12], 64)
				s.CPUSeconds = (u + k) / 100
			}
		}
		t.Logf("%-34s rss=%6.1f MiB heap=%6.1f MiB threads=%3d fds=%4d goroutines=%4d cpu=%.2fs", label, s.RSSMiB, s.HeapMiB, s.Threads, s.FDs, s.Goroutines, s.CPUSeconds)
		return s
	}
	report := map[string]any{}
	var samples []sample

	apply(nodes[:1], nil)
	samples = append(samples, take("1 node, default plan"))
	apply(nodes, nil)
	samples = append(samples, take("50 nodes, default plan"))

	all := make([]string, 0, len(nodes))
	for _, n := range nodes {
		all = append(all, n.InboundTag)
	}
	forced := EgressOutbound{Tag: "flvxo-f4", Type: "direct", IPVersion: "force_ipv4"}
	warp := EgressOutbound{Tag: "flvxo-warp", Type: "wireguard", IPVersion: "system", WireGuard: &EgressWireGuard{PrivateKey: goldenWGPrivate, PeerPublicKey: goldenWGPeer, AddressV4: "172.16.0.2", AddressV6: "2606:4700:110:8a36::2", Reserved: []int{1, 2, 3}}}
	planForced := &Egress{Version: 1, Outbounds: []EgressOutbound{forced}, Rules: []EgressRule{
		{Name: "BT", InboundTags: all, Protocol: []string{"bittorrent"}, Outbound: "block"},
		{Name: "default", InboundTags: all, Outbound: "flvxo-f4"},
	}}
	apply(nodes, planForced)
	samples = append(samples, take("50 nodes, forced IPv4 + BT block"))

	planWarp := &Egress{Version: 1, Outbounds: []EgressOutbound{forced, warp}, Rules: append([]EgressRule{
		{Name: "AI", InboundTags: all, Domain: []string{"geosite:openai", "geosite:anthropic", "geosite:google-gemini"}, Outbound: "flvxo-warp"},
	}, planForced.Rules...)}
	apply(nodes, planWarp)
	samples = append(samples, take("+ WARP outbound (idle) + AI geosite"))

	exempt := []string{}
	for i := 1; i <= 30; i++ {
		exempt = append(exempt, fmt.Sprintf("1.0.%d.7", i))
	}
	planCN := &Egress{Version: 1, ExemptSources: exempt, Outbounds: planWarp.Outbounds, Rules: append([]EgressRule{
		{Name: "cn ip", InboundTags: all, IP: []string{"geoip:cn"}, Outbound: "block"},
		{Name: "cn site", InboundTags: all, Domain: []string{"geosite:cn"}, Outbound: "block"},
		{Name: "cn source", InboundTags: all, SourceIP: []string{"geoip:cn"}, Outbound: "block"},
	}, planWarp.Rules...)}
	start := time.Now()
	apply(nodes, planCN)
	report["apply_block_cn_seconds"] = time.Since(start).Seconds()
	samples = append(samples, take("+ 屏蔽中国大陆 (geoip/geosite:cn + source minus 30 exempt)"))
	if rules, ok := m.egressCache.plan.rules[3].(map[string]any); ok {
		if list, ok := rules["sourceIP"].([]string); ok {
			report["cn_source_cidrs_after_exemption"] = len(list)
		}
	}

	// Steady state: the agent reconcile tick.
	var self0, child0 syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &self0)
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &child0)
	goroutines0 := runtime.NumGoroutine()
	ticks := 10
	tickStart := time.Now()
	for i := 0; i < ticks; i++ {
		m.mu.Lock()
		m.noteResult(m.reconcile(ctx))
		m.mu.Unlock()
	}
	wall := time.Since(tickStart)
	var self1, child1 syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &self1)
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &child1)
	cpu := func(a, b syscall.Rusage) float64 {
		d := func(x, y syscall.Timeval) float64 {
			return float64(y.Sec-x.Sec) + float64(y.Usec-x.Usec)/1e6
		}
		return d(a.Utime, b.Utime) + d(a.Stime, b.Stime)
	}
	report["agent_tick"] = map[string]any{
		"ticks": ticks, "wall_ms_per_tick": float64(wall.Milliseconds()) / float64(ticks),
		"agent_cpu_ms_per_tick":        cpu(self0, self1) * 1000 / float64(ticks),
		"cli_children_cpu_ms_per_tick": cpu(child0, child1) * 1000 / float64(ticks),
		"agent_goroutines_before":      goroutines0, "agent_goroutines_after": runtime.NumGoroutine(),
		"note": "children = the Core API CLI processes (lsi + statsquery per tick, pre-existing) incl. the ip-netns wrapper",
	}
	if m.egress.State != "applied" || m.lastError != "" {
		t.Fatalf("steady state not clean: %+v %q", m.egress, m.lastError)
	}

	// Idle CPU of the Core with 50 nodes and the full plan.
	idle0 := take("idle start")
	time.Sleep(30 * time.Second)
	idle1 := take("idle +30 s")
	report["core_idle_cpu_percent"] = (idle1.CPUSeconds - idle0.CPUSeconds) / 30 * 100

	// 1000 concurrent connections through node 1 (forced IPv4 direct), then closed.
	clientCfg := map[string]any{"log": map[string]any{"loglevel": "warning"},
		"inbounds":  []any{map[string]any{"tag": "in", "listen": "127.0.0.1", "port": 20001, "protocol": "dokodemo-door", "settings": map[string]any{"address": "12.0.1.2", "port": 8080, "network": "tcp"}}},
		"outbounds": []any{map[string]any{"tag": "out", "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": "11.0.1.1", "port": nodes[0].Port, "method": SS2022Method, "password": nodes[0].SS2022.ServerKey + ":" + nodes[0].SS2022.UserKey}}}}},
	}
	b, _ := json.Marshal(clientCfg)
	_ = os.WriteFile(filepath.Join(base, "client.json"), b, 0600)
	client := exec.Command("ip", "netns", "exec", ns, "sh", "-c", "ulimit -n 65535; exec "+core+" run -config "+filepath.Join(base, "client.json"))
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Process.Kill(); _, _ = client.Process.Wait() })
	time.Sleep(time.Second)
	load := `import socket, sys
n, port = int(sys.argv[1]), int(sys.argv[2])
conns = [socket.create_connection(("127.0.0.1", port), timeout=20) for _ in range(n)]
for c in conns:
    c.sendall(b"ping\n")
ok = 0
for c in conns:
    c.settimeout(20)
    try:
        if c.recv(16).startswith(b"ping"):
            ok += 1
    except Exception:
        pass
print("OPEN %d" % ok, flush=True)
sys.stdin.readline()
for c in conns:
    c.close()
print("CLOSED", flush=True)
sys.stdin.readline()
`
	_ = os.WriteFile(filepath.Join(base, "load.py"), []byte(load), 0644)
	before := take("before 1000 connections")
	loader := exec.Command("ip", "netns", "exec", ns, "sh", "-c", "ulimit -n 65535; exec python3 "+filepath.Join(base, "load.py")+" 1000 20001")
	stdin, _ := loader.StdinPipe()
	stdout, _ := loader.StdoutPipe()
	if err := loader.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(stdout)
	if !lines.Scan() || !strings.HasPrefix(lines.Text(), "OPEN ") {
		t.Fatalf("load client: %q", lines.Text())
	}
	opened, _ := strconv.Atoi(strings.TrimPrefix(lines.Text(), "OPEN "))
	peak := take("1000 connections open")
	_, _ = io.WriteString(stdin, "\n")
	if !lines.Scan() || lines.Text() != "CLOSED" {
		t.Fatalf("load client close: %q", lines.Text())
	}
	time.Sleep(15 * time.Second)
	after := take("15 s after closing them")
	_, _ = io.WriteString(stdin, "\n")
	_ = loader.Wait()
	time.Sleep(105 * time.Second)
	later := take("2 min after closing them")
	report["connections"] = map[string]any{"opened_and_echoed": opened, "before": before, "peak": peak, "after": after, "after_2min": later}
	if opened != 1000 {
		t.Errorf("only %d/1000 connections echoed through the forced-IPv4 outbound", opened)
	}
	if after.FDs > before.FDs+5 || after.Goroutines > before.Goroutines+10 {
		t.Errorf("connections leaked: fds %d→%d, goroutines %d→%d", before.FDs, after.FDs, before.Goroutines, after.Goroutines)
	}

	// Disk and log growth.
	var stateBytes int64
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			switch filepath.Base(path) {
			case "flvx-landing-core", "geoip.dat", "geosite.dat":
			default:
				stateBytes += info.Size()
			}
		}
		return nil
	})
	logInfo, _ := os.Stat(coreLog)
	logBytes := int64(0)
	if logInfo != nil {
		logBytes = logInfo.Size()
	}
	report["disk"] = map[string]any{"landing_state_bytes": stateBytes, "core_log_bytes_whole_test": logBytes, "agent_journal_lines": len(journal)}
	report["samples"] = samples
	out, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("%s", out)
	if path := os.Getenv("FLVX_LANDING_RESOURCE_OUT"); path != "" {
		_ = os.WriteFile(path, out, 0644)
	}
}
