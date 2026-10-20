package machineprotect

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tcpHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

func tcpRow(i int, st string) string {
	return fmt.Sprintf("%4d: 0100007F:%04X 00000000:0000 %s 00000000:00000000 00:00000000 00000000     0        0 %d 1 0000000000000000 100 0 0 10 0\n", i, 1000+i, st, 10000+i)
}

const udpHeader = "   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops\n"

func udpRow(i int) string {
	return fmt.Sprintf("%5d: 00000000:%04X 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 %d 2 0000000000000000 0\n", i, 2000+i, 20000+i)
}

func writeProcNet(t *testing.T, root string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "proc", "net")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// mmwxReadConnections is a verbatim copy of the mmwx Helper algorithm
// (cmd/mmwxc-helper/procnet.go readConnections / countProcNetRows) used as the
// reference: FLVX must show the same number for the same machine.
func mmwxReadConnections(root string) (tcpCount, udpCount int64, err error) {
	count := func(path string) (int64, bool) {
		file, err := os.Open(path)
		if err != nil {
			return 0, false
		}
		defer file.Close()
		var lines int64
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			lines++
		}
		if scanner.Err() != nil {
			return 0, false
		}
		if lines == 0 {
			return 0, true
		}
		return lines - 1, true
	}
	p := func(name string) string { return filepath.Join(root, "proc", "net", name) }
	tcp, okTCP := count(p("tcp"))
	tcp6, okTCP6 := count(p("tcp6"))
	udp, okUDP := count(p("udp"))
	udp6, okUDP6 := count(p("udp6"))
	if !okTCP && !okTCP6 && !okUDP && !okUDP6 {
		return 0, 0, fmt.Errorf("no /proc/net socket tables are readable")
	}
	return tcp + tcp6, udp + udp6, nil
}

func TestCounterMatchesMmwxAlgorithm(t *testing.T) {
	var tcp, tcp6 strings.Builder
	tcp.WriteString(tcpHeader)
	tcp6.WriteString(tcpHeader)
	states := []string{"01", "06", "08", "0A", "01", "06", "06", "02", "0B", "08", "01", "0c"}
	for i, st := range states {
		tcp.WriteString(tcpRow(i, st))
	}
	for i := 0; i < 7; i++ {
		tcp6.WriteString(tcpRow(i, "06"))
	}
	var udp strings.Builder
	udp.WriteString(udpHeader)
	for i := 0; i < 5; i++ {
		udp.WriteString(udpRow(i))
	}
	cases := map[string]map[string]string{
		"all four":                {"tcp": tcp.String(), "tcp6": tcp6.String(), "udp": udp.String(), "udp6": udpHeader + udpRow(9)},
		"ipv6 disabled":           {"tcp": tcp.String(), "udp": udp.String()},
		"header only":             {"tcp": tcpHeader, "tcp6": tcpHeader, "udp": udpHeader, "udp6": udpHeader},
		"empty files":             {"tcp": "", "tcp6": "", "udp": "", "udp6": ""},
		"no trailing newline":     {"tcp": strings.TrimSuffix(tcp.String(), "\n"), "udp": strings.TrimSuffix(udp.String(), "\n")},
		"only udp6":               {"udp6": udpHeader + udpRow(1) + udpRow(2)},
		"blank line inside table": {"tcp": tcpHeader + tcpRow(1, "01") + "\n" + tcpRow(2, "06")},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeProcNet(t, root, files)
			got, err := NewCounter(root).Read()
			wantTCP, wantUDP, wantErr := mmwxReadConnections(root)
			if (err != nil) != (wantErr != nil) {
				t.Fatalf("err=%v, mmwx err=%v", err, wantErr)
			}
			if got.TCP != wantTCP || got.UDP != wantUDP || got.Total != wantTCP+wantUDP {
				t.Fatalf("got tcp=%d udp=%d total=%d, mmwx tcp=%d udp=%d", got.TCP, got.UDP, got.Total, wantTCP, wantUDP)
			}
			if got.Established+got.TimeWait+got.CloseWait+got.OtherTCP != got.TCP {
				t.Fatalf("breakdown does not add up: %+v", got)
			}
		})
	}
	root := t.TempDir()
	writeProcNet(t, root, cases["all four"])
	got, _ := NewCounter(root).Read()
	want := Conns{Total: 12 + 7 + 5 + 1, TCP: 19, UDP: 6, Established: 3, TimeWait: 3 + 7, CloseWait: 2, OtherTCP: 4}
	if got != want {
		t.Fatalf("breakdown %+v, want %+v", got, want)
	}
}

func TestCounterNoTablesIsError(t *testing.T) {
	if _, err := NewCounter(t.TempDir()).Read(); err == nil {
		t.Fatal("missing /proc/net accepted")
	}
}

// The real tables of this host, copied once so both algorithms see the same bytes.
func TestCounterMatchesMmwxOnRealProc(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{}
	for _, name := range []string{"tcp", "tcp6", "udp", "udp6"} {
		if b, err := os.ReadFile(filepath.Join("/proc/net", name)); err == nil {
			files[name] = string(b)
		}
	}
	if len(files) == 0 {
		t.Skip("no /proc/net")
	}
	writeProcNet(t, root, files)
	got, err := NewCounter(root).Read()
	wantTCP, wantUDP, wantErr := mmwxReadConnections(root)
	if err != nil || wantErr != nil || got.TCP != wantTCP || got.UDP != wantUDP {
		t.Fatalf("got %+v err=%v, mmwx tcp=%d udp=%d err=%v", got, err, wantTCP, wantUDP, wantErr)
	}
	if got.Established+got.TimeWait+got.CloseWait+got.OtherTCP != got.TCP || got.OtherTCP < 0 {
		t.Fatalf("breakdown does not add up: %+v", got)
	}
	// The live read uses the same code path on the real /proc.
	if _, err := NewCounter("").Read(); err != nil {
		t.Fatal(err)
	}
}

func TestCounterReusesBuffer(t *testing.T) {
	root := t.TempDir()
	var tcp strings.Builder
	tcp.WriteString(tcpHeader)
	for i := 0; i < 2000; i++ {
		tcp.WriteString(tcpRow(i, "06"))
	}
	writeProcNet(t, root, map[string]string{"tcp": tcp.String(), "udp": udpHeader})
	c := NewCounter(root)
	if _, err := c.Read(); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(20, func() {
		if got, err := c.Read(); err != nil || got.TimeWait != 2000 {
			t.Fatalf("%+v %v", got, err)
		}
	})
	// os.Open/File objects allocate a little; the 300 KB table must not.
	if allocs > 20 {
		t.Fatalf("%.0f allocations per sample", allocs)
	}
}
