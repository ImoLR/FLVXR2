package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestScanGuardNetnsE2E proves plan 202 (禁止端口扫描) with real traffic
// through the pinned Core (v1.4.83, scan_guard.v1) in three network
// namespaces: client (SOCKS → SS2022) → landing (Core + this agent code) →
// target (every address of 13.0.0.0/8 is local there, so a scan reaches as
// many distinct IPs as it wants without touching any real host).
//
//   - horizontal: 200 IPs on port 22 → hit at the 100th IP, node blocked at
//     once, the other nodes keep working; the agent reports the hit with the
//     node id and keeps holding it across a Core restart until the panel
//     records it; 解除禁用 (node enabled again) lifts the Core block with its
//     seq; a second scan after that is caught again.
//   - vertical: 60 ports of one IP → hit at the 30th port.
//   - web ports: port 443 → no hit at 100 IPs, hit at the 500th IP.
//   - FLVX_SCAN_E2E_SLOW=1 (≈6 min, run in parallel on separate nodes): slow
//     total scan (6 new IPs/s, each on its own port) → hit at the 1000th IP;
//     heavy normal browsing (300 distinct 443/80 IPs per minute, 100 of them
//     new) for 6 minutes → no hit; the pessimistic case (300 never-seen IPs
//     per minute) → reports when it trips the total.
//   - FLVX_SCAN_E2E_LOAD=1: Core / agent CPU + RSS at 200 and 2000 new
//     connections per second, guard on vs off, and a 100k-IP scan.
//
// Needs root, iproute2, FLVX_LANDING_TEST_BINARY (the pinned v1.4.83 asset)
// and FLVX_LANDING_NETNS_E2E=1. FLVX_SCAN_E2E_OUT writes the results as JSON.
func TestScanGuardNetnsE2E(t *testing.T) {
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
	nss := []string{"flvxsg-c", "flvxsg-l", "flvxsg-t"}
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
	sh("ip", "link", "add", "flvxsg-c0", "type", "veth", "peer", "name", "flvxsg-l0")
	sh("ip", "link", "set", "flvxsg-c0", "netns", "flvxsg-c")
	sh("ip", "link", "set", "flvxsg-l0", "netns", "flvxsg-l")
	sh("ip", "link", "add", "flvxsg-l1", "type", "veth", "peer", "name", "flvxsg-t0")
	sh("ip", "link", "set", "flvxsg-l1", "netns", "flvxsg-l")
	sh("ip", "link", "set", "flvxsg-t0", "netns", "flvxsg-t")
	sh("ip", "-n", "flvxsg-c", "addr", "add", "11.0.2.50/24", "dev", "flvxsg-c0")
	sh("ip", "-n", "flvxsg-l", "addr", "add", "11.0.2.1/24", "dev", "flvxsg-l0")
	sh("ip", "-n", "flvxsg-l", "addr", "add", "12.0.2.1/24", "dev", "flvxsg-l1")
	sh("ip", "-n", "flvxsg-t", "addr", "add", "12.0.2.2/24", "dev", "flvxsg-t0")
	for _, l := range [][2]string{{"flvxsg-c", "flvxsg-c0"}, {"flvxsg-l", "flvxsg-l0"}, {"flvxsg-l", "flvxsg-l1"}, {"flvxsg-t", "flvxsg-t0"}} {
		sh("ip", "-n", l[0], "link", "set", l[1], "up")
	}
	// Scan targets: the whole of 13.0.0.0/8 lives in the target namespace.
	sh("ip", "-n", "flvxsg-l", "route", "add", "13.0.0.0/8", "via", "12.0.2.2")
	sh("ip", "-n", "flvxsg-t", "route", "add", "local", "13.0.0.0/8", "dev", "lo")
	for _, ns := range nss {
		_ = exec.Command("ip", "netns", "exec", ns, "sysctl", "-qw", "net.ipv4.ip_local_port_range=10000 65000", "net.ipv4.tcp_tw_reuse=1").Run()
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := func(ns string, env ...string) *exec.Cmd {
		cmd := exec.Command("ip", "netns", "exec", ns, self, "-test.run=^TestScanLabHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), env...)
		return cmd
	}
	srv := helper("flvxsg-t", "FLVX_SCAN_LAB_ROLE=server")
	srvLog, _ := os.Create(filepath.Join(base, "server.log"))
	srv.Stdout, srv.Stderr = srvLog, srvLog
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _, _ = srv.Process.Wait() })

	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxsg-l "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, true)
	m.execPath = wrapper
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	nodes := []Node{}
	tags := []string{}
	for i := int64(1); i <= 7; i++ {
		n := testNode(t, i)
		n.Port = 31000 + int(i)
		nodes = append(nodes, n)
		tags = append(tags, n.InboundTag)
	}
	rev := int64(0)
	var seen int64
	desired := func() Desired {
		rev++
		ns := append([]Node(nil), nodes...)
		return Desired{Revision: rev, Enabled: true, CoreMemoryMB: 512, CoreCPUPercent: 200, Nodes: ns,
			ScanGuard: &ScanGuard{Enabled: true, Identities: tags, Horizontal: 100, HorizontalWeb: 500, WebPorts: []int{80, 443}, WindowS: 60, Vertical: 30, Total: 1000, TotalWindowS: 300, Seen: seen}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	var mu sync.Mutex // serializes Apply between the test and the tick loop
	cur := desired()
	apply := func(d Desired) Status {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		cur = d
		st, err := m.Apply(ctx, d)
		if err != nil {
			t.Fatalf("apply rev %d: %v", d.Revision, err)
		}
		return st
	}
	tick := func() Status {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		st, err := m.Apply(ctx, cur)
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
		return st
	}
	st := apply(cur)
	if st.ScanGuard == nil || st.ScanGuard.State != "on" {
		t.Fatalf("guard not on: %+v", st.ScanGuard)
	}

	// Client: one SOCKS inbound per node.
	var inbounds, outbounds, rules []any
	for i, n := range nodes {
		tag := strconv.Itoa(i + 1)
		inbounds = append(inbounds, map[string]any{"tag": "in-" + tag, "listen": "127.0.0.1", "port": 21001 + i, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}})
		outbounds = append(outbounds, map[string]any{"tag": "out-" + tag, "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": "11.0.2.1", "port": n.Port, "method": SS2022Method, "password": n.SS2022.ServerKey + ":" + n.SS2022.UserKey}}}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"in-" + tag}, "outboundTag": "out-" + tag})
	}
	clientCfg, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "error"}, "inbounds": inbounds, "outbounds": outbounds, "routing": map[string]any{"rules": rules}})
	if err := os.WriteFile(filepath.Join(base, "client.json"), clientCfg, 0600); err != nil {
		t.Fatal(err)
	}
	client := exec.Command("ip", "netns", "exec", "flvxsg-c", core, "run", "-config", filepath.Join(base, "client.json"))
	clientOut, _ := os.Create(filepath.Join(base, "client.log"))
	client.Stdout, client.Stderr = clientOut, clientOut
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Process.Kill(); _, _ = client.Process.Wait() })

	socks := func(node int) string { return "127.0.0.1:" + strconv.Itoa(21000+node) }
	run := func(job labJob) labResult {
		t.Helper()
		raw, _ := json.Marshal(job)
		cmd := helper("flvxsg-c", "FLVX_SCAN_LAB_ROLE=client", "FLVX_SCAN_LAB_JOB="+string(raw))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("client job: %v", err)
		}
		var res labResult
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "RESULT ") {
				if err := json.Unmarshal([]byte(line[7:]), &res); err != nil {
					t.Fatal(err)
				}
			}
		}
		return res
	}
	fetch := func(node int, target string) bool {
		t.Helper()
		var ok bool
		for attempt := 0; attempt < 20; attempt++ {
			r := run(labJob{Socks: socks(node), Rounds: []labRound{{Targets: []string{target}}}, Parallel: 1})
			if ok = r.Reached == 1; ok {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		return ok
	}
	for i := range nodes {
		if !fetch(i+1, "13.9.0.1:443") {
			t.Fatalf("node %d cannot reach the target before any scan", i+1)
		}
	}
	results := map[string]any{}
	hitOf := func(st Status, tag string) *ScanHit {
		if st.ScanGuard == nil {
			return nil
		}
		for i := range st.ScanGuard.Hits {
			if st.ScanGuard.Hits[i].InboundTag == tag {
				return &st.ScanGuard.Hits[i]
			}
		}
		return nil
	}
	ips := func(b, n, from int, port int) []string {
		out := make([]string, n)
		for i := range out {
			k := from + i
			out[i] = fmt.Sprintf("13.%d.%d.%d:%d", b, k/250, k%250+1, port)
		}
		return out
	}

	// 1. Horizontal scan through node 1.
	h := run(labJob{Socks: socks(1), Rounds: []labRound{{Targets: ips(1, 200, 0, 22)}}, Parallel: 32})
	st = tick()
	hit := hitOf(st, "flvxl-1")
	if hit == nil || hit.Type != "horizontal" || hit.Count != 100 || hit.Port != 22 || hit.ID != 1 || hit.WindowS != 60 {
		t.Fatalf("horizontal hit %+v (scan %+v)", hit, h)
	}
	if h.Reached > 99 {
		t.Fatalf("horizontal scan reached %d targets", h.Reached)
	}
	if m.ScanSeq() != hit.Seq {
		t.Fatal("system-info seq not set")
	}
	results["horizontal"] = map[string]any{"scan": h, "hit": hit}
	if fetch(1, "13.9.0.2:443") {
		t.Fatal("blocked node 1 still reaches targets")
	}
	if !fetch(2, "13.9.0.2:443") {
		t.Fatal("node 2 broken by node 1's block")
	}

	// 2. Core restart before the panel recorded the hit: still blocked.
	mu.Lock()
	if m.proc != nil {
		_ = m.proc.Kill()
		m.proc = nil
	}
	mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	st = tick()
	if hitOf(st, "flvxl-1") == nil || st.ScanGuard.State != "on" || len(st.ScanGuard.Blocked) != 0 {
		t.Fatalf("after Core restart: %+v", st.ScanGuard)
	}
	if fetch(1, "13.9.0.3:443") {
		t.Fatal("node 1 reachable after a Core restart before the panel recorded the hit")
	}
	if !fetch(2, "13.9.0.3:443") {
		t.Fatal("node 2 broken after the Core restart")
	}
	results["core_restart_hold"] = true

	// 3. The panel records it (node 1 disabled), then 解除禁用.
	seen = hit.Seq
	nodes[0].Enabled = false
	st = apply(desired())
	if len(st.ScanGuard.Hits) != 0 || m.ScanSeq() != 0 {
		t.Fatalf("recorded hit still pending: %+v", st.ScanGuard)
	}
	nodes[0].Enabled = true
	apply(desired())
	if !fetch(1, "13.9.0.4:443") {
		t.Fatal("node 1 not back after 解除禁用")
	}

	// 4. Second scan → the Core blocks again; record + unblock lifts the
	//    Core's own block (scan_unblock with the hit's seq).
	time.Sleep(time.Second)
	run(labJob{Socks: socks(1), Rounds: []labRound{{Targets: ips(1, 150, 400, 22)}}, Parallel: 32})
	st = tick()
	hit2 := hitOf(st, "flvxl-1")
	if hit2 == nil || hit2.Seq <= hit.Seq || len(st.ScanGuard.Blocked) != 1 {
		t.Fatalf("second scan not caught: %+v", st.ScanGuard)
	}
	seen = hit2.Seq
	nodes[0].Enabled = false
	apply(desired())
	nodes[0].Enabled = true
	unblockStart := time.Now()
	st = apply(desired())
	if !fetch(1, "13.9.0.5:443") {
		t.Fatal("Core block not lifted by scan_unblock")
	}
	results["unblock_ms"] = time.Since(unblockStart).Milliseconds()
	if st.ScanGuard != nil && len(st.ScanGuard.Blocked) != 0 {
		// The unblock is pushed in this apply; the next read confirms it.
		if st = tick(); len(st.ScanGuard.Blocked) != 0 {
			t.Fatalf("still blocked after unblock: %+v", st.ScanGuard)
		}
	}

	// 5. Vertical scan through node 2.
	v := run(labJob{Socks: socks(2), Rounds: []labRound{{Targets: func() []string {
		out := []string{}
		for p := 1000; p < 1060; p++ {
			out = append(out, "13.2.0.1:"+strconv.Itoa(p))
		}
		return out
	}()}}, Parallel: 16})
	st = tick()
	if hv := hitOf(st, "flvxl-2"); hv == nil || hv.Type != "vertical" || hv.Count != 30 || hv.Host != "13.2.0.1" {
		t.Fatalf("vertical hit %+v (scan %+v)", hv, v)
	} else {
		results["vertical"] = map[string]any{"scan": v, "hit": hv}
	}

	// 6. Web port: 450 IPs on 443 are not a hit, 600 are (at the 500th).
	w1 := run(labJob{Socks: socks(3), Rounds: []labRound{{Targets: ips(3, 450, 0, 443)}}, Parallel: 32})
	if st = tick(); hitOf(st, "flvxl-3") != nil || w1.Reached != 450 {
		t.Fatalf("450 IPs on 443 tripped the guard: %+v / %+v", hitOf(st, "flvxl-3"), w1)
	}
	w2 := run(labJob{Socks: socks(3), Rounds: []labRound{{Targets: ips(3, 150, 450, 443)}}, Parallel: 32})
	st = tick()
	if hw := hitOf(st, "flvxl-3"); hw == nil || hw.Type != "horizontal" || hw.Count != 500 || hw.Port != 443 {
		t.Fatalf("web hit %+v (%+v)", hw, w2)
	} else {
		results["web_443"] = map[string]any{"no_hit_at_450": w1, "scan": w2, "hit": hw}
	}

	if os.Getenv("FLVX_SCAN_E2E_SLOW") == "1" {
		// Keep the agent's 10 s tick going while the slow runs last.
		stop := make(chan struct{})
		var ticks sync.WaitGroup
		ticks.Add(1)
		go func() {
			defer ticks.Done()
			for {
				select {
				case <-stop:
					return
				case <-time.After(10 * time.Second):
					mu.Lock()
					_, _ = m.Apply(ctx, cur)
					mu.Unlock()
				}
			}
		}()
		var wg sync.WaitGroup
		var slow, browse, pessimistic labResult
		// Slow total scan on node 4: 6 new IPs per second, each on its own
		// port (never a horizontal / vertical pattern), 1100 IPs.
		wg.Add(3)
		go func() {
			defer wg.Done()
			targets := make([]string, 1100)
			for i := range targets {
				targets[i] = fmt.Sprintf("13.4.%d.%d:%d", i/250, i%250+1, 2000+i)
			}
			slow = run(labJob{Socks: socks(4), Rounds: []labRound{{Targets: targets, Seconds: 1100.0 / 6}}, Parallel: 8})
		}()
		// Heavy normal browsing on node 5: every minute 300 distinct IPs
		// (100 new, 200 seen in the previous minutes), 443 mostly, some 80.
		go func() {
			defer wg.Done()
			browse = run(labJob{Socks: socks(5), Rounds: browseRounds(5, 6, 100, 200, 400), Parallel: 16})
		}()
		// Pessimistic browsing on node 6: 300 never-seen IPs every minute.
		go func() {
			defer wg.Done()
			pessimistic = run(labJob{Socks: socks(6), Rounds: browseRounds(6, 6, 300, 0, 1), Parallel: 16})
		}()
		wg.Wait()
		close(stop)
		ticks.Wait()
		st = tick()
		hs := hitOf(st, "flvxl-4")
		if hs == nil || hs.Type != "total" || hs.Count != 1000 {
			t.Fatalf("slow total hit %+v (%+v)", hs, slow)
		}
		if hb := hitOf(st, "flvxl-5"); hb != nil || browse.Failed != 0 {
			t.Fatalf("heavy browsing tripped the guard: %+v / %+v", hb, browse)
		}
		hp := hitOf(st, "flvxl-6")
		results["slow_total"] = map[string]any{"scan": slow, "hit": hs}
		results["heavy_browsing"] = map[string]any{"run": browse, "distinct_ips": browse.Distinct, "hit": nil}
		results["pessimistic_browsing"] = map[string]any{"run": pessimistic, "hit": hp}
	}

	if os.Getenv("FLVX_SCAN_E2E_LOAD") == "1" {
		results["load"] = scanLoad(t, m, &mu, &cur, ctx, run, socks, desired, &nodes)
	}
	if out := os.Getenv("FLVX_SCAN_E2E_OUT"); out != "" {
		b, _ := json.MarshalIndent(results, "", "  ")
		_ = os.WriteFile(out, b, 0644)
	}
}

// browseRounds models browsing: every minute `fresh` never-seen IPs plus
// `popular` picks rotating over a stable set of popularPool IPs (the CDNs,
// search, video and app endpoints everybody hits again and again); 1 in 6 on
// port 80, the rest 443, spread evenly over the minute.
func browseRounds(b, minutes, fresh, popular, popularPool int) []labRound {
	next, rot := 0, 0
	rounds := []labRound{}
	for m := 0; m < minutes; m++ {
		targets := []string{}
		for i := 0; i < fresh; i++ {
			port := 443
			if next%6 == 0 {
				port = 80
			}
			targets = append(targets, fmt.Sprintf("13.%d.%d.%d:%d", b, next/250, next%250+1, port))
			next++
		}
		for i := 0; i < popular; i++ {
			k := rot % popularPool
			rot++
			port := 443
			if k%6 == 0 {
				port = 80
			}
			targets = append(targets, fmt.Sprintf("13.%d.%d.%d:%d", b+100, k/250, k%250+1, port))
		}
		// Interleave fresh and popular targets over the minute.
		mixed := make([]string, 0, len(targets))
		for i, j := 0, fresh; i < fresh || j < len(targets); {
			if i < fresh {
				mixed = append(mixed, targets[i])
				i++
			}
			for k := 0; k < 2 && j < len(targets); k++ {
				mixed = append(mixed, targets[j])
				j++
			}
		}
		rounds = append(rounds, labRound{Targets: mixed, Seconds: 60})
	}
	return rounds
}

// labJob is one client run (helper process in the client namespace).
type labJob struct {
	Socks    string     `json:"socks"`
	Rounds   []labRound `json:"rounds"`
	Parallel int        `json:"parallel"`
}
type labRound struct {
	Targets []string `json:"targets"`
	Seconds float64  `json:"seconds"` // spread evenly; 0 = as fast as possible
}
type labResult struct {
	Attempts    int     `json:"attempts"`
	Reached     int     `json:"reached"`
	Failed      int     `json:"failed"`
	FirstFailAt float64 `json:"first_fail_s"` // seconds after start (-1 = none)
	FirstFailN  int     `json:"first_fail_n"` // attempt number of the first failure
	Elapsed     float64 `json:"elapsed_s"`
	Distinct    int     `json:"distinct_ips"`
	Rate        float64 `json:"conns_per_s"`
}

// TestScanLabHelper is the client / server process of the scan lab (only
// when started by TestScanGuardNetnsE2E / scanLoad).
func TestScanLabHelper(t *testing.T) {
	switch os.Getenv("FLVX_SCAN_LAB_ROLE") {
	case "server":
		labServer()
	case "client":
		var job labJob
		if err := json.Unmarshal([]byte(os.Getenv("FLVX_SCAN_LAB_JOB")), &job); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(labClient(job))
		fmt.Println("RESULT " + string(b))
	case "load":
		labLoad()
	default:
		t.Skip("lab helper")
	}
}

func labServer() {
	for _, port := range []int{22, 80, 443, 8080, 9000} {
		ln, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(port))
		if err != nil {
			fmt.Println("listen", port, err)
			os.Exit(1)
		}
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					continue
				}
				go func() {
					_ = c.SetDeadline(time.Now().Add(3 * time.Second))
					buf := make([]byte, 16)
					_, _ = c.Read(buf)
					_, _ = c.Write([]byte("ok\n"))
					_ = c.Close()
				}()
			}
		}()
	}
	select {}
}

// labDial: SOCKS5 CONNECT, one payload, expect "ok".
func labDial(socks, target string, timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", socks, timeout)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))
	host, portS, _ := net.SplitHostPort(target)
	port, _ := strconv.Atoi(portS)
	ip := net.ParseIP(host).To4()
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		return false
	}
	buf := make([]byte, 16)
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return false
	}
	req := append([]byte{5, 1, 0, 1}, ip...)
	req = append(req, byte(port>>8), byte(port))
	if _, err := c.Write(req); err != nil {
		return false
	}
	if _, err := io.ReadFull(c, buf[:10]); err != nil || buf[1] != 0 {
		return false
	}
	if _, err := c.Write([]byte("ping\n")); err != nil {
		return false
	}
	n, _ := io.ReadFull(c, buf[:3])
	return n == 3 && string(buf[:3]) == "ok\n"
}

func labClient(job labJob) labResult {
	if job.Parallel <= 0 {
		job.Parallel = 1
	}
	res := labResult{FirstFailAt: -1}
	sem := make(chan struct{}, job.Parallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	distinct := map[string]bool{}
	start := time.Now()
	n := 0
	for _, r := range job.Rounds {
		roundStart := time.Now()
		for i, target := range r.Targets {
			if r.Seconds > 0 {
				at := roundStart.Add(time.Duration(float64(i) * r.Seconds / float64(len(r.Targets)) * float64(time.Second)))
				time.Sleep(time.Until(at))
			}
			host, _, _ := net.SplitHostPort(target)
			distinct[host] = true
			n++
			idx := n
			sem <- struct{}{}
			wg.Add(1)
			go func(target string) {
				defer wg.Done()
				ok := labDial(job.Socks, target, 3*time.Second)
				<-sem
				mu.Lock()
				defer mu.Unlock()
				res.Attempts++
				if ok {
					res.Reached++
					return
				}
				res.Failed++
				if res.FirstFailAt < 0 || idx < res.FirstFailN {
					res.FirstFailAt = time.Since(start).Seconds()
					res.FirstFailN = idx
				}
			}(target)
		}
		if r.Seconds > 0 {
			time.Sleep(time.Until(roundStart.Add(time.Duration(r.Seconds * float64(time.Second)))))
		}
	}
	wg.Wait()
	res.Elapsed = time.Since(start).Seconds()
	res.Distinct = len(distinct)
	if res.Elapsed > 0 {
		res.Rate = float64(res.Attempts) / res.Elapsed
	}
	return res
}

// ---- resource measurements (FLVX_SCAN_E2E_LOAD=1) ----------------------

// labLoad keeps a target rate of new connections for a duration over a
// small pool of IPs (no scan pattern), FLVX_SCAN_LAB_JOB = {socks, rate,
// seconds, pool}.
func labLoad() {
	var job struct {
		Socks   string  `json:"socks"`
		Rate    float64 `json:"rate"`
		Seconds float64 `json:"seconds"`
		Pool    int     `json:"pool"`
		Scan    int     `json:"scan"` // > 0: that many distinct IPs on port 22 instead
	}
	_ = json.Unmarshal([]byte(os.Getenv("FLVX_SCAN_LAB_JOB")), &job)
	var ok, fail atomic.Int64
	sem := make(chan struct{}, 512)
	var wg sync.WaitGroup
	start := time.Now()
	total := int(job.Rate * job.Seconds)
	if job.Scan > 0 {
		total = job.Scan
	}
	for i := 0; i < total; i++ {
		if job.Rate > 0 {
			time.Sleep(time.Until(start.Add(time.Duration(float64(i) / job.Rate * float64(time.Second)))))
		}
		target := fmt.Sprintf("13.7.0.%d:443", i%job.Pool+1)
		if job.Scan > 0 {
			target = fmt.Sprintf("13.8.%d.%d:22", (i/250)%250, i%250+1)
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			if labDial(job.Socks, target, 3*time.Second) {
				ok.Add(1)
			} else {
				fail.Add(1)
			}
			<-sem
		}(target)
	}
	wg.Wait()
	el := time.Since(start).Seconds()
	b, _ := json.Marshal(map[string]any{"ok": ok.Load(), "failed": fail.Load(), "elapsed_s": el, "conns_per_s": float64(total) / el})
	fmt.Println("RESULT " + string(b))
}

// procSample: CPU seconds (utime+stime) and RSS MiB of a pid.
func procSample(pid int) (float64, float64) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0
	}
	f := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+2:]))
	ut, _ := strconv.ParseFloat(f[11], 64)
	stt, _ := strconv.ParseFloat(f[12], 64)
	rss := 0.0
	if s, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status"); err == nil {
		for _, line := range strings.Split(string(s), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				kb, _ := strconv.ParseFloat(strings.Fields(line)[1], 64)
				rss = kb / 1024
			}
		}
	}
	return (ut + stt) / 100, rss
}

func scanLoad(t *testing.T, m *Manager, mu *sync.Mutex, cur *Desired, ctx context.Context, run func(labJob) labResult, socks func(int) string, desired func() Desired, nodes *[]Node) map[string]any {
	t.Helper()
	self, _ := os.Executable()
	load := func(env string) map[string]any {
		cmd := exec.Command("ip", "netns", "exec", "flvxsg-c", self, "-test.run=^TestScanLabHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "FLVX_SCAN_LAB_ROLE=load", "FLVX_SCAN_LAB_JOB="+env)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		res := map[string]any{}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "RESULT ") {
				_ = json.Unmarshal([]byte(line[7:]), &res)
			}
		}
		return res
	}
	measure := func(label, job string) map[string]any {
		mu.Lock()
		corePid := m.proc.Pid
		mu.Unlock()
		self := os.Getpid()
		c0, _ := procSample(corePid)
		a0, _ := procSample(self)
		// The agent's own work during the run: its 10 s reconcile tick.
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				case <-time.After(10 * time.Second):
					mu.Lock()
					_, _ = m.Apply(ctx, *cur)
					mu.Unlock()
				}
			}
		}()
		res := load(job)
		close(stop)
		<-done
		c1, crss := procSample(corePid)
		a1, arss := procSample(self)
		el, _ := res["elapsed_s"].(float64)
		res["core_cpu_pct"] = (c1 - c0) / el * 100
		res["core_rss_mib"] = crss
		res["agent_cpu_pct"] = (a1 - a0) / el * 100
		res["agent_rss_mib"] = arss
		t.Logf("%s: %v", label, res)
		return res
	}
	out := map[string]any{}
	setGuard := func(on bool) {
		d := desired()
		if !on {
			d.ScanGuard = nil
		}
		mu.Lock()
		*cur = d
		_, err := m.Apply(ctx, d)
		mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	order := []bool{false, true}
	if os.Getenv("FLVX_SCAN_E2E_LOAD_ON_FIRST") == "1" {
		order = []bool{true, false} // RSS of one Core process grows run by run
	}
	for _, rate := range []int{200, 2000} {
		for _, on := range order {
			setGuard(on)
			time.Sleep(2 * time.Second)
			label := fmt.Sprintf("rate%d_guard_%t", rate, on)
			out[label] = measure(label, fmt.Sprintf(`{"socks":%q,"rate":%d,"seconds":20,"pool":50}`, socks(7), rate))
		}
	}
	// 100k-IP scan through node 7 with the guard on (default thresholds).
	setGuard(true)
	out["scan_100k"] = measure("scan_100k", fmt.Sprintf(`{"socks":%q,"rate":0,"seconds":0,"pool":1,"scan":100000}`, socks(7)))
	return out
}
