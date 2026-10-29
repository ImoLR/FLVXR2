//go:build linux

package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// TestUserRateNetnsE2E (plan 161) runs the pinned Core through the Manager in
// a landing namespace, with SS2022 clients (the same Core binary) in a client
// namespace and a target behind the landing server. User 7 owns nodes 1 and 2
// (速率 20), user 9 owns node 3 (no rate). It proves, with real traffic:
//   - both of user 7's nodes share ONE limit per direction (combined ≈ 20),
//     split fairly between the two connections;
//   - user 9 on the same Core and device is not limited;
//   - a rate change (20 → 40) and the removal apply to a running connection
//     without dropping it and without restarting the Core;
//   - an agent restart (new Manager on the same directory) rebuilds exactly
//     one set of kernel objects and keeps the limit;
//   - ReleaseRates (graceful stop) leaves nothing behind.
//
// Needs root, iproute2, python3, FLVX_LANDING_TEST_BINARY (pinned Core) and
// FLVX_LANDING_NETNS_E2E=1. FLVX_USERRATE_E2E_OUT writes the numbers.
func TestUserRateNetnsE2E(t *testing.T) {
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
	nss := []string{"flvxur-c", "flvxur-l", "flvxur-t"}
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
	link("flvxur-c", "flvxur-c0", "11.0.1.50/24", "flvxur-l", "flvxur-l0", "11.0.1.1/24")
	link("flvxur-l", "flvxur-l1", "12.0.1.1/24", "flvxur-t", "flvxur-t0", "12.0.1.2/24")
	// 20 ms each way between client and landing (a real path, not loopback speed).
	sh("ip", "netns", "exec", "flvxur-c", "tc", "qdisc", "add", "dev", "flvxur-c0", "root", "netem", "delay", "20ms", "limit", "100000")
	sh("ip", "netns", "exec", "flvxur-l", "tc", "qdisc", "add", "dev", "flvxur-l0", "root", "netem", "delay", "20ms", "limit", "100000")

	if err := os.WriteFile(filepath.Join(base, "target.py"), []byte(rateTargetPy), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "flows.py"), []byte(rateFlowsPy), 0644); err != nil {
		t.Fatal(err)
	}
	target := exec.Command("ip", "netns", "exec", "flvxur-t", "python3", filepath.Join(base, "target.py"))
	if err := target.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Process.Kill(); _, _ = target.Process.Wait() })

	wrapper := filepath.Join(base, "core-in-netns")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec ip netns exec flvxur-l "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	lns, err := netns.GetFromName("flvxur-l")
	if err != nil {
		t.Fatal(err)
	}
	defer lns.Close()
	lh, err := netlink.NewHandleAt(lns)
	if err != nil {
		t.Fatal(err)
	}
	defer lh.Close()
	newManager := func() *Manager {
		m := NewManager(dir, true)
		m.execPath = wrapper
		m.rates = newRateEngineAt(lh)
		return m
	}
	m := newManager()
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	})
	n1, n2, n3 := testNode(t, 1), testNode(t, 2), testNode(t, 3)
	n1.Port, n2.Port, n3.Port = 31301, 31302, 31303
	n1.RateGroup, n2.RateGroup = "landing-user-7", "landing-user-7"
	desired := func(rev int64, rate int) Desired {
		d := Desired{Revision: rev, Enabled: true, Nodes: []Node{n1, n2, n3}}
		if rate > 0 {
			d.UserRates = &UserRates{Version: LandingRateVersion, Groups: []RateGroup{{Group: "landing-user-7", RateMbps: rate}}}
		} else {
			d.Nodes = []Node{n1, n2, n3}
			d.Nodes[0].RateGroup, d.Nodes[1].RateGroup = "", ""
		}
		return d
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	st, err := m.Apply(ctx, desired(1, 20))
	if err != nil {
		t.Fatalf("apply: %v (%+v)", err, st)
	}
	if st.UserRate == nil || st.UserRate.State != "on" || st.UserRate.Ports != 4 {
		t.Fatalf("rate status %+v", st.UserRate)
	}

	// SS2022 clients: one dokodemo port per node.
	client := map[string]any{"log": map[string]any{"loglevel": "warning"}}
	var inbounds, outbounds, rules []any
	for i, n := range []Node{n1, n2, n3} {
		lport := 21301 + i
		inbounds = append(inbounds, map[string]any{"tag": fmt.Sprintf("in%d", i), "listen": "127.0.0.1", "port": lport, "protocol": "dokodemo-door", "settings": map[string]any{"address": "12.0.1.2", "port": 9100, "network": "tcp"}})
		outbounds = append(outbounds, map[string]any{"tag": fmt.Sprintf("out%d", i), "protocol": "shadowsocks", "settings": map[string]any{"servers": []any{map[string]any{"address": "11.0.1.1", "port": n.Port, "method": SS2022Method, "password": n.SS2022.ServerKey + ":" + n.SS2022.UserKey}}}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{fmt.Sprintf("in%d", i)}, "outboundTag": fmt.Sprintf("out%d", i)})
	}
	client["inbounds"], client["outbounds"], client["routing"] = inbounds, outbounds, map[string]any{"rules": rules}
	raw, _ := json.Marshal(client)
	if err := os.WriteFile(filepath.Join(base, "client.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	cl := exec.Command("ip", "netns", "exec", "flvxur-c", core, "run", "-config", filepath.Join(base, "client.json"))
	if err := cl.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Process.Kill(); _, _ = cl.Process.Wait() })
	time.Sleep(1500 * time.Millisecond)

	type flows struct {
		Mbps   []float64   `json:"mbps"`
		Series [][]float64 `json:"series"`
		Errors []string    `json:"errors"`
	}
	runErr := func(mode string, secs, warm int, ports ...int) (flows, error) {
		args := []string{"netns", "exec", "flvxur-c", "python3", filepath.Join(base, "flows.py"), mode, fmt.Sprint(secs), fmt.Sprint(warm)}
		for _, p := range ports {
			args = append(args, fmt.Sprintf("127.0.0.1:%d", p))
		}
		var f flows
		out, err := exec.Command("ip", args...).Output()
		if err != nil {
			return f, fmt.Errorf("flows %v: %w", args, err)
		}
		if err := json.Unmarshal(out, &f); err != nil {
			return f, fmt.Errorf("flows output %s: %w", out, err)
		}
		return f, nil
	}
	run := func(mode string, secs, warm int, ports ...int) flows {
		t.Helper()
		f, err := runErr(mode, secs, warm, ports...)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	sum := func(v []float64) float64 {
		s := 0.0
		for _, x := range v {
			s += x
		}
		return s
	}
	numbers := map[string]any{}
	within := func(name string, got, want, tol float64) {
		t.Helper()
		numbers[name] = math.Round(got*100) / 100
		if math.Abs(got-want) > want*tol {
			t.Errorf("%s = %.2f Mbps, want %.0f ±%.0f %%", name, got, want, tol*100)
		}
	}

	// Two downloads over both of user 7's nodes, user 9 next to them.
	dl := run("get", 10, 3, 21301, 21302, 21303)
	within("user7_download_total", dl.Mbps[0]+dl.Mbps[1], 20, 0.12)
	numbers["user7_download_flows"] = dl.Mbps[:2]
	numbers["user9_download"] = dl.Mbps[2]
	if dl.Mbps[2] < 60 {
		t.Errorf("user 9 limited: %.1f Mbps", dl.Mbps[2])
	}
	if lo, hi := math.Min(dl.Mbps[0], dl.Mbps[1]), math.Max(dl.Mbps[0], dl.Mbps[1]); hi == 0 || lo/hi < 0.7 {
		t.Errorf("unfair split %v", dl.Mbps[:2])
	}
	ul := run("put", 10, 3, 21301, 21302)
	within("user7_upload_total", sum(ul.Mbps), 20, 0.12)
	numbers["user7_upload_flows"] = ul.Mbps

	// Live change on a running connection: 20 → 40 after 6 s, removed after 16 s.
	type liveResult struct {
		f   flows
		err error
	}
	done := make(chan liveResult, 1)
	go func() {
		f, err := runErr("get", 26, 0, 21301)
		done <- liveResult{f, err}
	}()
	liveStart := time.Now()
	time.Sleep(6 * time.Second)
	epoch := m.Status().Epoch
	t40 := time.Now()
	if _, err := m.Apply(ctx, desired(2, 40)); err != nil {
		t.Fatalf("apply 40: %v", err)
	}
	numbers["apply_40_ms"] = time.Since(t40).Milliseconds()
	numbers["apply_40_at_s"] = math.Round(t40.Sub(liveStart).Seconds()*10) / 10
	time.Sleep(10*time.Second - time.Since(t40))
	tNone := time.Now()
	if _, err := m.Apply(ctx, desired(3, 0)); err != nil {
		t.Fatalf("apply none: %v", err)
	}
	numbers["apply_none_ms"] = time.Since(tNone).Milliseconds()
	numbers["apply_none_at_s"] = math.Round(tNone.Sub(liveStart).Seconds()*10) / 10
	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	live := res.f
	numbers["live_series"] = live.Series
	if len(live.Errors) > 0 || len(live.Series) < 24 {
		t.Fatalf("running connection broke: %+v", live)
	}
	avg := func(rows [][]float64) float64 {
		s := 0.0
		for _, r := range rows {
			s += r[0]
		}
		return s / float64(len(rows))
	}
	within("live_before_20", avg(live.Series[2:6]), 20, 0.15)
	within("live_after_40", avg(live.Series[11:16]), 40, 0.15)
	// Removal at 16 s: the class is released (unlimited) before anything is
	// deleted, so nothing queued is dropped or overtaken — no dip below the old
	// limit — and the connection grows past 40 (TCP takes a few seconds).
	minAfter, maxAfter := math.Inf(1), 0.0
	for _, r := range live.Series[16:25] {
		minAfter, maxAfter = math.Min(minAfter, r[0]), math.Max(maxAfter, r[0])
	}
	numbers["live_after_removal_min"], numbers["live_after_removal_max"] = minAfter, maxAfter
	if minAfter < 36 {
		t.Errorf("throughput dipped when the limit was removed: %v", live.Series[16:25])
	}
	if maxAfter < 50 {
		t.Errorf("not faster after removal: %v", live.Series[16:25])
	}
	if m.Status().Epoch != epoch {
		t.Error("the Core restarted for a rate change")
	}
	// The next tick (≥ rateDrain later) removes the drained IFBs.
	m.mu.Lock()
	m.applyRates()
	m.mu.Unlock()
	if _, ok := rateIfbIndex(lh, rateIfbDown); ok {
		t.Error("IFB left after the last rate went")
	}

	// Agent restart: a new Manager on the same directory rebuilds one set.
	if _, err := m.Apply(ctx, desired(4, 20)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		m = newManager()
		if _, err := m.Apply(ctx, desired(4, 20)); err != nil {
			t.Fatalf("restart %d: %v", i, err)
		}
	}
	links, _ := lh.LinkList()
	ifbs := 0
	for _, l := range links {
		if l.Type() == "ifb" {
			ifbs++
		}
	}
	if ifbs != 2 {
		t.Errorf("%d IFB devices after restarts", ifbs)
	}
	filters := 0
	for _, dev := range []string{"flvxur-l0", "flvxur-l1"} {
		l, _ := lh.LinkByName(dev)
		for _, parent := range []uint32{netlink.HANDLE_MIN_INGRESS, netlink.HANDLE_MIN_EGRESS} {
			fs, _ := lh.FilterList(l, parent)
			for _, f := range fs {
				if (f.Attrs().Priority == ratePrefV4 || f.Attrs().Priority == ratePrefV6) && f.Attrs().Handle != 0 {
					filters++
				}
			}
		}
	}
	// 4 (port, proto) pairs × IPv4/IPv6 × in/out on both devices.
	if filters != 32 {
		t.Errorf("%d filters after restarts, want 32", filters)
	}
	again := run("get", 8, 2, 21301, 21302)
	within("after_restart_total", sum(again.Mbps), 20, 0.12)

	// Graceful stop: nothing left.
	m.ReleaseRates(3 * time.Second)
	if _, ok := rateIfbIndex(lh, rateIfbUp); ok {
		t.Error("IFB left after ReleaseRates")
	}
	if out := os.Getenv("FLVX_USERRATE_E2E_OUT"); out != "" {
		raw, _ := json.MarshalIndent(numbers, "", "  ")
		_ = os.WriteFile(out, raw, 0644)
	}
	t.Logf("numbers: %v", numbers)
}

func rateIfbIndex(h *netlink.Handle, name string) (int, bool) {
	l, err := h.LinkByName(name)
	if err != nil {
		return 0, false
	}
	return l.Attrs().Index, true
}

// target: 'd' = endless download, 'u' = upload sink reporting its total.
const rateTargetPy = `import socket, threading, time
def serve(c):
    try:
        mode = c.recv(1)
        if mode == b"u":
            total = [0]
            stop = [False]
            def report():
                while not stop[0]:
                    try:
                        c.sendall(("C%d\n" % total[0]).encode())
                    except Exception:
                        return
                    time.sleep(0.1)
            threading.Thread(target=report, daemon=True).start()
            while True:
                b = c.recv(65536)
                if not b:
                    break
                total[0] += len(b)
            stop[0] = True
        else:
            buf = b"x" * 65536
            while True:
                c.sendall(buf)
    except Exception:
        pass
    c.close()
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 9100)); s.listen(256)
while True:
    c, _ = s.accept()
    threading.Thread(target=serve, args=(c,), daemon=True).start()
`

// flows.py get|put <secs> <warm> addr...: per-flow FLVX Mbps (bytes/s / 131072)
// after warm-up and the per-second series; uploads count what the target got.
const rateFlowsPy = `import json, socket, sys, threading, time
mode, secs, warm, addrs = sys.argv[1], float(sys.argv[2]), int(sys.argv[3]), sys.argv[4:]
counts = [0] * len(addrs)
errors = []
end = time.time() + secs
def flow(i, addr):
    host, port = addr.rsplit(":", 1)
    try:
        c = socket.create_connection((host, int(port)), timeout=5)
        c.settimeout(max(0.5, end - time.time() + 1))
        if mode == "get":
            c.sendall(b"d")
            while time.time() < end:
                b = c.recv(65536)
                if not b:
                    errors.append(addr + ": closed")
                    return
                counts[i] += len(b)
        else:
            c.sendall(b"u")
            buf = b"y" * 65536
            def reader():
                acc = b""
                while True:
                    try:
                        d = c.recv(4096)
                    except Exception:
                        return
                    if not d:
                        return
                    acc += d
                    while b"\n" in acc:
                        line, acc = acc.split(b"\n", 1)
                        if line.startswith(b"C"):
                            counts[i] = int(line[1:])
            threading.Thread(target=reader, daemon=True).start()
            while time.time() < end:
                c.sendall(buf)
        c.close()
    except Exception as e:
        if time.time() < end - 0.5:
            errors.append(addr + ": " + str(e))
threads = [threading.Thread(target=flow, args=(i, a), daemon=True) for i, a in enumerate(addrs)]
for th in threads:
    th.start()
series, prev = [], [0] * len(addrs)
while time.time() < end:
    time.sleep(1)
    row = [round((counts[i] - prev[i]) / 131072.0, 2) for i in range(len(addrs))]
    prev = list(counts)
    series.append(row)
used = series[warm:] or series
mbps = [round(sum(r[i] for r in used) / len(used), 2) for i in range(len(addrs))]
print(json.dumps({"mbps": mbps, "series": series, "errors": errors}))
`
