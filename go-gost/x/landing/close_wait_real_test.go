package landing

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// closeWaitSockets counts this namespace's TCP sockets towards 127.0.0.1:port in
// CLOSE_WAIT (st 08) — the Core's outbound side after the target sent FIN.
func closeWaitSockets(t *testing.T, port int) int {
	t.Helper()
	want := fmt.Sprintf("0100007F:%04X", port)
	n := 0
	for _, name := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		s := bufio.NewScanner(f)
		for s.Scan() {
			fields := strings.Fields(s.Text())
			if len(fields) > 3 && strings.HasSuffix(fields[2], want) && fields[3] == "08" {
				n++
			}
		}
		_ = f.Close()
	}
	return n
}

// Real pinned Core: with default_close_wait_timeout_seconds the Core closes an
// outbound socket the remote half-closed; without it the socket stays in
// CLOSE_WAIT (xray's own half-close timers are raised to 300 s here, so only
// the machine-protection timeout can close it).
func TestRealCoreCloseWaitTimeout(t *testing.T) {
	dir := t.TempDir()
	core := realCore(t, dir)
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	targetPort := target.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, err := target.Accept()
			if err != nil {
				return
			}
			// Reply, then half-close: the Core's outbound socket enters CLOSE_WAIT
			// while the client keeps its side open.
			_, _ = c.Write([]byte("hello"))
			_ = c.(*net.TCPConn).CloseWrite()
			defer c.Close()
		}
	}()
	inPort := freePort(t)
	m := NewManager(dir, true)
	if err := os.MkdirAll(filepath.Join(dir, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"log":       map[string]any{"loglevel": "warning", "access": "none"},
		"policy":    map[string]any{"levels": map[string]any{"0": map[string]any{"handshake": 4, "connIdle": 300, "uplinkOnly": 300, "downlinkOnly": 300}}},
		"inbounds":  []any{map[string]any{"tag": "in", "listen": "127.0.0.1", "port": inPort, "protocol": "dokodemo-door", "settings": map[string]any{"address": "127.0.0.1", "port": targetPort, "network": "tcp"}}},
		"outbounds": []any{map[string]any{"tag": "direct", "protocol": "freedom"}},
	}
	if err := m.writeJSON("cw-config.json", cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, core, "run", "-config", filepath.Join(dir, "cw-config.json"))
	cmd.Env = append(os.Environ(), "MMWXC_CORE_CONTROL_SOCKET="+m.socket())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	for i := 0; i < 100; i++ {
		if _, err = m.snapshot(ctx); err == nil {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("control socket:", err)
	}
	open := func() net.Conn {
		t.Helper()
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", inPort), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 5)
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := c.Read(buf); err != nil || string(buf) != "hello" {
			t.Fatalf("relay: %q %v", buf, err)
		}
		return c
	}
	waitCloseWait := func(want func(int) bool, within time.Duration) int {
		deadline := time.Now().Add(within)
		n := closeWaitSockets(t, targetPort)
		for !want(n) && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			n = closeWaitSockets(t, targetPort)
		}
		return n
	}

	// 1) No timeout (today's null): the half-closed outbound socket stays.
	if err := m.applyLimits(ctx, coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	c1 := open()
	defer c1.Close()
	if n := waitCloseWait(func(n int) bool { return n >= 1 }, 3*time.Second); n < 1 {
		t.Fatal("no CLOSE_WAIT socket to observe")
	}
	time.Sleep(4 * time.Second)
	if n := closeWaitSockets(t, targetPort); n < 1 {
		t.Fatal("CLOSE_WAIT cleared without a timeout configured")
	}
	_ = c1.Close()
	waitCloseWait(func(n int) bool { return n == 0 }, 5*time.Second)

	// 2) Machine protection sets 2 s: the Core closes it.
	m.SetCloseWaitTimeout(2)
	if err := m.applyLimits(ctx, coreSnapshot{}); err != nil {
		t.Fatal(err)
	}
	c2 := open()
	defer c2.Close()
	started := time.Now()
	if n := waitCloseWait(func(n int) bool { return n >= 1 }, 3*time.Second); n < 1 {
		t.Fatal("no CLOSE_WAIT socket to observe")
	}
	if n := waitCloseWait(func(n int) bool { return n == 0 }, 6*time.Second); n != 0 {
		t.Fatalf("CLOSE_WAIT not closed by the 2 s timeout (%d left)", n)
	}
	t.Logf("CLOSE_WAIT closed %.1fs after the half-close with timeout 2s", time.Since(started).Seconds())
}
