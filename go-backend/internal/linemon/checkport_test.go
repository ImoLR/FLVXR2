package linemon

import (
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestCheckPortScriptValidation(t *testing.T) {
	for _, c := range []struct{ port, window int }{{22, 24}, {80, 24}, {1023, 24}, {65536, 24}, {25931, 0}, {25931, 2}, {25931, 48}} {
		if _, _, err := CheckPortScript(c.port, c.window, nil); err == nil {
			t.Errorf("port %d window %d accepted", c.port, c.window)
		}
	}
	many := make([]netip.Addr, MaxCheckAllow+1)
	for i := range many {
		many[i] = netip.AddrFrom4([4]byte{198, 51, 100, byte(i)})
	}
	if _, _, err := CheckPortScript(25931, 24, many); err == nil {
		t.Error("too many allow IPs accepted")
	}
	if _, _, err := CheckPortScript(25931, 24, []netip.Addr{{}}); err == nil {
		t.Error("invalid addr accepted")
	}
}

// Only integers, fixed words and netip-printed addresses are interpolated:
// every line that differs between two very different inputs differs only in
// digits / hex / '.' / ':' (T6 of the threat table).
func TestCheckPortScriptOnlyIntegers(t *testing.T) {
	a, ua, err := CheckPortScript(25931, 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, ub, err := CheckPortScript(41234, 72, []netip.Addr{netip.MustParseAddr("2001:db8::7"), netip.MustParseAddr("::ffff:198.51.100.9")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b, "IPAddressAllow=198.51.100.9 2001:db8::7\nIPAddressDeny=any\n") || strings.Contains(a, "IPAddressAllow") {
		t.Fatalf("allow lines wrong:\n%s", b)
	}
	b = strings.Replace(b, "IPAddressAllow=198.51.100.9 2001:db8::7\nIPAddressDeny=any\n", "", 1)
	diffOK := regexp.MustCompile(`^[0-9a-f.:]*$`)
	for _, pair := range [][2]string{{a, b}, {ua, ub}} {
		la, lb := strings.Split(pair[0], "\n"), strings.Split(pair[1], "\n")
		if len(la) != len(lb) {
			t.Fatalf("line counts differ: %d vs %d", len(la), len(lb))
		}
		for i := range la {
			if la[i] == lb[i] {
				continue
			}
			// strip the common prefix / suffix; what is left must be numeric
			x, y := la[i], lb[i]
			for len(x) > 0 && len(y) > 0 && x[0] == y[0] {
				x, y = x[1:], y[1:]
			}
			for len(x) > 0 && len(y) > 0 && x[len(x)-1] == y[len(y)-1] {
				x, y = x[:len(x)-1], y[:len(y)-1]
			}
			if !diffOK.MatchString(x) || !diffOK.MatchString(y) {
				t.Fatalf("line %d interpolates non-numeric text: %q vs %q", i, la[i], lb[i])
			}
		}
	}
	for _, want := range []string{"P=25931\n", "W=${FLVX_LINEMON_TEST_IDLE:-86400}\n", "Accept=yes\n", "MaxConnections=16\n",
		"MaxConnectionsPerSource=4\n", "ExecStart=/bin/true\n", "DynamicUser=yes\n", "ProtectSystem=strict\n", "NoNewPrivileges=yes\n",
		"RuntimeMaxSec=5\n", "MemoryMax=8M\n", "LogLevelMax=warning\n", "OnUnitActiveSec=${T}s\n", "date +%s", "idle $idle, removed"} {
		if !strings.Contains(a, want) {
			t.Errorf("install lacks %q", want)
		}
	}
	for _, bad := range []string{"curl", "wget", "| sh", "| bash", "rm -rf", "rm -r "} {
		if strings.Contains(a+ua, bad) {
			t.Errorf("command contains %q", bad)
		}
	}
	if !strings.HasPrefix(a, "sh <<'FLVX_LINEMON'\n") || !strings.HasSuffix(a, "\nFLVX_LINEMON\n") || !strings.HasSuffix(ua, "\nFLVX_LINEMON\n") {
		t.Fatal("not one quoted heredoc")
	}
	if strings.Count(a, "%!") > 0 || strings.Count(ua, "%!") > 0 {
		t.Fatal("format verb leaked")
	}
}

func TestSuggestCustomPort(t *testing.T) {
	used := map[int]bool{}
	for i := 0; i < 300; i++ {
		p, err := SuggestCustomPort(used)
		if err != nil {
			t.Fatal(err)
		}
		if p < 20000 || p > 29999 || p%1000 == 0 || used[p] || (p >= 27000 && p <= 27050) || p == 25565 || p == 22222 {
			t.Fatalf("bad suggestion %d", p)
		}
		used[p] = true
	}
}

// watchScript extracts the watchdog that the install command writes to
// $D/watch.sh, with the fixed paths moved under dir (never touches the host).
func watchScript(t *testing.T, install, dir string) string {
	t.Helper()
	start := strings.Index(install, "cat <<'EOW'\n")
	end := strings.Index(install, "\nEOW\n")
	if start < 0 || end < start {
		t.Fatal("no watchdog heredoc")
	}
	body := install[start+len("cat <<'EOW'\n") : end]
	body = strings.ReplaceAll(body, "/var/lib/flvx-linemon", dir+"/state")
	body = strings.ReplaceAll(body, "/etc/systemd/system", dir+"/units")
	return "P=25931\nW=600\n" + body + "\n"
}

// Plan 259: the watchdog keeps the port while NAccepted grows OR systemd logs
// getpeername failures for its socket (probers that reset at once), and
// removes it after the idle window otherwise. Runs the real generated shell
// with stubbed systemctl / journalctl / date / rm in a temp dir.
func TestCheckPortWatchdogFallback(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	install, _, err := CheckPortScript(25931, 24, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, d := range []string{"bin", "units", "state/25931"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, "bin", name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// systemctl show → $FAKE_N; anything else (disable / daemon-reload) is logged.
	stub("systemctl", `if [ "$1" = show ]; then echo "$FAKE_N"; else echo "systemctl $*" >> "$FAKE_LOG"; fi`)
	// journalctl prints $FAKE_JOURNAL when --since is not after $FAKE_JOURNAL_AT.
	stub("journalctl", `since=; while [ $# -gt 0 ]; do [ "$1" = --since ] && since=${2#@}; shift; done
echo "journalctl since=$since" >> "$FAKE_LOG"
[ -n "$FAKE_JOURNAL" ] && [ "$since" -le "$FAKE_JOURNAL_AT" ] && echo "$FAKE_JOURNAL"; exit 0`)
	stub("date", `echo "$FAKE_NOW"`)
	stub("rm", `echo "rm $*" >> "$FAKE_LOG"`)
	stub("rmdir", `exit 0`)
	script := filepath.Join(dir, "watch.sh")
	if err := os.WriteFile(script, []byte(watchScript(t, install, dir)), 0o644); err != nil {
		t.Fatal(err)
	}
	logf := filepath.Join(dir, "log")
	run := func(now int64, n, journal string, journalAt int64) (removed bool, state string) {
		t.Helper()
		_ = os.WriteFile(logf, nil, 0o644)
		cmd := exec.Command(sh, script)
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(dir, "bin")+":"+os.Getenv("PATH"),
			"FAKE_N="+n, "FAKE_NOW="+strconv.FormatInt(now, 10), "FAKE_LOG="+logf,
			"FAKE_JOURNAL="+journal, "FAKE_JOURNAL_AT="+strconv.FormatInt(journalAt, 10))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("watch.sh: %v\n%s", err, out)
		}
		lb, _ := os.ReadFile(logf)
		sb, _ := os.ReadFile(filepath.Join(dir, "state/25931/state"))
		return strings.Contains(string(lb), "systemctl disable --now"), strings.TrimSpace(string(sb))
	}
	const t0 = 1_800_000_000
	gp := "flvx-linemon-25931.socket: getpeername failed: Transport endpoint is not connected"
	if rm, st := run(t0, "0", "", 0); rm || st != "0 1800000000" {
		t.Fatalf("first tick: removed=%v state=%q", rm, st)
	}
	// NAccepted grows (agents with plan 259): kept, state follows.
	if rm, st := run(t0+300, "1", "", 0); rm || st != "1 1800000300" {
		t.Fatalf("accepted grew: removed=%v state=%q", rm, st)
	}
	// NAccepted stuck, but getpeername lines since the last activity (an
	// old RST-closing agent): activity → state time moves, port kept.
	if rm, st := run(t0+900, "1", gp, t0+800); rm || st != "1 1800000900" {
		t.Fatalf("getpeername fallback: removed=%v state=%q", rm, st)
	}
	lb, _ := os.ReadFile(logf)
	if !strings.Contains(string(lb), "journalctl since=1800000300") {
		t.Fatalf("journal must be read since the last activity: %s", lb)
	}
	// Journal lines from before the last activity do not count again; within
	// the window nothing is removed.
	if rm, st := run(t0+1200, "1", gp, t0+800); rm || st != "1 1800000900" {
		t.Fatalf("old journal line: removed=%v state=%q", rm, st)
	}
	// Idle for the window (600 s here): removed.
	if rm, _ := run(t0+1500, "1", "", 0); !rm {
		t.Fatal("idle window passed: not removed")
	}
	// journalctl missing / failing (minimal systems): still removed after the window.
	stub("journalctl", `exit 127`)
	_ = os.WriteFile(filepath.Join(dir, "state/25931/state"), []byte("1 1800000900\n"), 0o644)
	if rm, _ := run(t0+1500, "1", gp, t0+1400); !rm {
		t.Fatal("without journalctl the idle removal must still work")
	}
}
