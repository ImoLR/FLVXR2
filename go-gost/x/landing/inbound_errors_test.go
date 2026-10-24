package landing

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-gost/x/logsafe"
)

// Plan 147: the probe after a failed add names the busy network.
func TestClassifyInboundFailure(t *testing.T) {
	tcp, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	port := tcp.Addr().(*net.TCPAddr).Port
	reality := Node{ID: 1, Port: port, Protocol: "vless_reality_vision"}
	if code, reason := classifyInboundFailure(reality, errors.New("landing gRPC adi failed")); code != NodeErrorPortInUse || reason != "TCP: address already in use" {
		t.Fatalf("tcp busy: %s %q", code, reason)
	}
	udp, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	ss := Node{ID: 2, Port: udp.LocalAddr().(*net.UDPAddr).Port, Protocol: "ss2022"}
	if code, reason := classifyInboundFailure(ss, errors.New("x")); code != NodeErrorPortInUse || reason != "UDP: address already in use" {
		t.Fatalf("udp busy: %s %q", code, reason)
	}
	free := Node{ID: 3, Port: freeTCPPort(t), Protocol: "vless_reality_vision"}
	if code, reason := classifyInboundFailure(free, errors.New("landing gRPC adi failed")); code != NodeErrorAddFailed || reason != "core rejected the inbound" {
		t.Fatalf("free port: %s %q", code, reason)
	}
}

// Backoff, reset rules, report and quiet logging of a failed add.
func TestInboundFailureBackoffAndLog(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	m := NewManager(t.TempDir(), true)
	m.logs = logsafe.NewRoutineLogger(func(s string) { mu.Lock(); lines = append(lines, s); mu.Unlock() })
	m.desired.Revision = 4
	now := time.Unix(1_700_000_000, 0)
	n := Node{ID: 7, InboundTag: "flvxl-7", Port: 30007, Protocol: "vless_reality_vision"}
	tcp, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	n.Port = tcp.Addr().(*net.TCPAddr).Port
	if !m.inboundRetryDue(n.InboundTag, "cfg", now) {
		t.Fatal("first add not due")
	}
	// Five failures across 15 minutes of ticks: delays 10 s, 30 s, 1 m, 2 m, 5 m.
	at := now
	for i, want := range []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 10 * time.Minute} {
		m.noteInboundFailure(n.InboundTag, "cfg", n, errors.New("landing gRPC adi failed"), at)
		if m.inboundRetryDue(n.InboundTag, "cfg", at.Add(want-time.Second)) || !m.inboundRetryDue(n.InboundTag, "cfg", at.Add(want)) {
			t.Fatalf("failure %d: retry delay is not %s", i+1, want)
		}
		at = at.Add(want)
	}
	// A moved port (new config) or a new revision retries at once.
	if !m.inboundRetryDue(n.InboundTag, "cfg2", at) {
		t.Fatal("changed config waits")
	}
	m.desired.Revision = 5
	if !m.inboundRetryDue(n.InboundTag, "cfg", at) {
		t.Fatal("new revision waits")
	}
	errs := m.nodeErrors()
	if len(errs) != 1 || errs[0].ID != 7 || errs[0].Port != n.Port || errs[0].Code != NodeErrorPortInUse || errs[0].Reason != "TCP: address already in use" || errs[0].Since != now.UnixMilli() {
		t.Fatalf("report %+v", errs)
	}
	// One line for 7 failures within 10 minutes of the first plus at most one
	// count line per 10 minutes, then one recovery line.
	mu.Lock()
	failLines := len(lines)
	mu.Unlock()
	if failLines < 1 || failLines > 3 {
		t.Fatalf("log lines for 7 failures: %d %q", failLines, lines)
	}
	m.pruneInboundFailures(map[string]string{})
	if len(m.nodeErrors()) != 0 {
		t.Fatal("unwanted tag still reported")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != failLines+1 || !strings.Contains(lines[len(lines)-1], "已恢复") {
		t.Fatalf("recovery line: %q", lines)
	}
	if m.nodeErrors() == nil {
		t.Fatal("node_errors must be present (empty list) on new agents")
	}
}

// TestLandingPortBusyRealCore (plan 147) runs the pinned Core with two
// inbounds while another program already listens on the first one's port:
// the free inbound comes up, the busy one is reported with port and reason,
// retries are backed off (no Core CLI call per tick), and once the port is
// free the next revision adds it.
func TestLandingPortBusyRealCore(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "landing")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	core := realCore(t, dir)
	// Count Core CLI calls through a wrapper (api <command> ...).
	calls := filepath.Join(base, "calls.log")
	wrapper := filepath.Join(base, "core-counted")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n[ \"$1\" = api ] && echo \"$2\" >> "+calls+"\nexec "+core+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir, true)
	m.execPath = wrapper
	var mu sync.Mutex
	var lines []string
	m.logs = logsafe.NewRoutineLogger(func(s string) { mu.Lock(); lines = append(lines, s); mu.Unlock() })
	t.Cleanup(func() {
		if m.proc != nil {
			_ = m.proc.Kill()
			_, _ = m.proc.Wait()
		}
	})
	busyLn, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busyLn.Close()
	busy, free := testNode(t, 1), testNode(t, 2)
	busy.Port = busyLn.Addr().(*net.TCPAddr).Port
	free.Port = freeTCPPort(t)
	d := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 100, Nodes: []Node{busy, free}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	status, err := m.Apply(ctx, d)
	if err != nil || status.State != "running" || status.Error != "" {
		t.Fatalf("apply: %v %+v", err, status)
	}
	if len(status.NodeErrors) != 1 || status.NodeErrors[0].ID != 1 || status.NodeErrors[0].Port != busy.Port || status.NodeErrors[0].Code != NodeErrorPortInUse || status.NodeErrors[0].Reason != "TCP: address already in use" {
		t.Fatalf("node errors %+v", status.NodeErrors)
	}
	listed, err := m.api(ctx, "lsi")
	if err != nil || !strings.Contains(string(listed), `"flvxl-2"`) || strings.Contains(string(listed), `"flvxl-1"`) {
		t.Fatalf("inbounds: %v %s", err, listed)
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(free.Port)), 2*time.Second)
	if err != nil {
		t.Fatalf("free inbound not listening: %v", err)
	}
	_ = c.Close()
	countAdi := func() int {
		raw, _ := os.ReadFile(calls)
		n := 0
		for _, l := range strings.Split(string(raw), "\n") {
			if l == "adi" {
				n++
			}
		}
		return n
	}
	adiAfterApply := countAdi()
	stats0 := procStats(m)
	// Three 10 s-tick reconciles inside the first backoff: no add attempt.
	for i := 0; i < 3; i++ {
		m.mu.Lock()
		m.noteResult(m.reconcile(ctx))
		m.mu.Unlock()
	}
	if got := countAdi(); got != adiAfterApply {
		t.Fatalf("add retried inside the backoff: %d → %d", adiAfterApply, got)
	}
	if st := m.Status(); st.State != "running" || len(st.NodeErrors) != 1 {
		t.Fatalf("status during backoff %+v", st)
	}
	mu.Lock()
	failLines := 0
	for _, l := range lines {
		if strings.Contains(l, "landing inbound") {
			failLines++
		}
	}
	mu.Unlock()
	if failLines != 1 {
		t.Fatalf("log lines for the busy port: %d %q", failLines, lines)
	}
	// The other program exits; the next revision adds the inbound at once.
	_ = busyLn.Close()
	d.Revision = 2
	status, err = m.Apply(ctx, d)
	if err != nil || len(status.NodeErrors) != 0 {
		t.Fatalf("after release: %v %+v", err, status.NodeErrors)
	}
	listed, _ = m.api(ctx, "lsi")
	if !strings.Contains(string(listed), `"flvxl-1"`) || !strings.Contains(string(listed), `"flvxl-2"`) {
		t.Fatalf("inbounds after release: %s", listed)
	}
	stats1 := procStats(m)
	raw, _ := os.ReadFile(calls)
	t.Logf("core CLI calls total=%d adi=%d; core rss %.1f→%.1f MiB fds %.0f→%.0f cpu %.2f→%.2f s",
		strings.Count(string(raw), "\n"), countAdi(), stats0["rss_mib"], stats1["rss_mib"], stats0["fds"], stats1["fds"], stats0["cpu_s"], stats1["cpu_s"])
}
