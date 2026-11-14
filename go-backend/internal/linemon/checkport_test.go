package linemon

import (
	"net/netip"
	"regexp"
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
