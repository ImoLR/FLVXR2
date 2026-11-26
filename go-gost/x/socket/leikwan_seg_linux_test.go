//go:build linux

package socket

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fakeDiagMsg builds one SOCK_DIAG_BY_FAMILY answer (inet_diag_msg + an
// INET_DIAG_INFO attribute with tcpi_rtt / tcpi_min_rtt).
func fakeDiagMsg(seq uint32, dst netip.AddrPort, state uint8, rttUs, minUs uint32) []byte {
	ne := binary.NativeEndian
	info := make([]byte, 232)
	ne.PutUint32(info[lkTCPInfoRttOff:], rttUs)
	ne.PutUint32(info[lkTCPInfoMinOff:], minUs)
	msg := make([]byte, lkInetDiagMsgSize)
	if dst.Addr().Is4() {
		msg[0] = unix.AF_INET
		a := dst.Addr().As4()
		copy(msg[24:], a[:])
	} else {
		msg[0] = unix.AF_INET6
		a := dst.Addr().As16()
		copy(msg[24:], a[:])
	}
	msg[1] = state
	binary.BigEndian.PutUint16(msg[4:], 40000)
	binary.BigEndian.PutUint16(msg[6:], dst.Port())
	attr := make([]byte, 4+len(info))
	ne.PutUint16(attr[0:], uint16(len(attr)))
	ne.PutUint16(attr[2:], lkInetDiagInfo)
	copy(attr[4:], info)
	// an unrelated attribute first (INET_DIAG_SHUTDOWN, 1 byte + padding)
	other := []byte{5, 0, 8, 0, 0, 0, 0, 0}
	body := append(append(msg, other...), attr...)
	h := make([]byte, 16)
	ne.PutUint32(h[0:], uint32(16+len(body)))
	ne.PutUint16(h[4:], unix.SOCK_DIAG_BY_FAMILY)
	ne.PutUint32(h[8:], seq)
	return append(h, body...)
}

func fakeDone(seq uint32) []byte {
	ne := binary.NativeEndian
	b := make([]byte, 20)
	ne.PutUint32(b[0:], 20)
	ne.PutUint16(b[4:], unix.NLMSG_DONE)
	ne.PutUint32(b[8:], seq)
	return b
}

func TestLeiKwanSegParse(t *testing.T) {
	a := netip.MustParseAddrPort("43.139.2.249:5438")
	mapped := netip.AddrPortFrom(netip.AddrFrom16(a.Addr().As16()), 5438) // ::ffff:43.139.2.249
	buf := append(fakeDiagMsg(7, a, lkTCPEstablished, 2474, 1823), fakeDiagMsg(7, mapped, lkTCPEstablished, 3014, 0)...)
	buf = append(buf, fakeDiagMsg(6, a, lkTCPEstablished, 99999, 0)...) // another request's answer: ignored
	buf = append(buf, fakeDiagMsg(7, a, 6, 5000, 0)...)                 // not ESTABLISHED: ignored
	socks, done, err := lkSegParse(buf, 7)
	if err != nil || done || len(socks) != 2 {
		t.Fatalf("socks %+v done %v err %v", socks, done, err)
	}
	if socks[0].Dst != a || socks[0].RttUs != 2474 || socks[0].MinUs != 1823 || socks[1].Dst != a || socks[1].RttUs != 3014 {
		t.Fatalf("socks %+v", socks)
	}
	if _, done, err := lkSegParse(fakeDone(7), 7); !done || err != nil {
		t.Fatalf("done %v err %v", done, err)
	}
	// NLMSG_ERROR with -EINVAL → an error.
	e := make([]byte, 36)
	binary.NativeEndian.PutUint32(e[0:], 36)
	binary.NativeEndian.PutUint16(e[4:], unix.NLMSG_ERROR)
	binary.NativeEndian.PutUint32(e[8:], 7)
	binary.NativeEndian.PutUint32(e[16:], uint32(0xffffffea)) // -22
	if _, _, err := lkSegParse(e, 7); !errors.Is(err, unix.EINVAL) {
		t.Fatalf("err %v", err)
	}
	// A truncated message is an error, not a panic.
	if _, _, err := lkSegParse(buf[:30], 7); err == nil {
		t.Fatal("truncated message accepted")
	}
}

// The request carries the kernel-side port filter (D_GE && D_LE).
func TestLeiKwanSegRequest(t *testing.T) {
	b := lkSegRequest(9, unix.AF_INET, 5438)
	ne := binary.NativeEndian
	if len(b) != 92 || ne.Uint32(b) != 92 || ne.Uint16(b[4:]) != unix.SOCK_DIAG_BY_FAMILY || ne.Uint32(b[8:]) != 9 {
		t.Fatalf("header % x", b[:16])
	}
	r := b[16:]
	if r[0] != unix.AF_INET || r[1] != unix.IPPROTO_TCP || r[2] != 1<<(lkInetDiagInfo-1) || ne.Uint32(r[4:]) != 1<<lkTCPEstablished {
		t.Fatalf("req % x", r[:8])
	}
	bc := b[16+56+4:]
	if ne.Uint16(b[16+56:]) != 20 || ne.Uint16(b[16+56+2:]) != lkInetDiagReqBC ||
		bc[0] != lkInetDiagBCDGE || bc[1] != 8 || ne.Uint16(bc[2:]) != 20 || ne.Uint16(bc[6:]) != 5438 ||
		bc[8] != lkInetDiagBCDLE || bc[9] != 8 || ne.Uint16(bc[10:]) != 12 || ne.Uint16(bc[14:]) != 5438 {
		t.Fatalf("bytecode % x", b[16+56:])
	}
}

func TestLeiKwanSegStats(t *testing.T) {
	ms, minMs := lkSegStats([]uint32{3173, 2474, 1879, 3014, 2385, 2557})
	if ms != 2.47 || minMs != 1.87 {
		t.Fatalf("ms %v min %v", ms, minMs)
	}
	if ms, minMs := lkSegStats(nil); ms != 0 || minMs != 0 {
		t.Fatal("empty")
	}
}

// The host report: the UDP selection's ingress (offered.json, else
// lines.json) gets the 入口段 of this host's connections to it; nothing
// else (no TCP-only ingress, no other endpoint).
func TestLeiKwanHostReadEntrySeg(t *testing.T) {
	m := lkFixture(t, "aliyun-gz")
	now := m.now()
	ts := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339Nano) }
	write := func(rel string, doc any) {
		t.Helper()
		raw, _ := json.Marshal(doc)
		p := filepath.Join(m.root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	status := func(id int, udpRef, tcpRef string) {
		write("run/ultra/forward-"+strconv.Itoa(id)+".status.json", map[string]any{"forward_id": id, "state": "ready",
			"latest_tcp": map[string]any{"selection_ref": tcpRef, "rtt_ms": 15, "selected_at": ts(20 * time.Second)},
			"active_udp": map[string]any{"selection_ref": udpRef, "rtt_ms": 11, "probe_sent": 30, "probe_received": 30, "selected_at": ts(3 * time.Minute)}})
	}
	ff := "run/leikwan-fast-failover/"
	// 263: UDP ref in the current offer.
	status(263, "Zg8", "Zg8")
	write(ff+"forward-263.offered.json", map[string]any{"at": 1, "offered": []map[string]any{
		{"endpoint": "106.75.147.154:5438", "ref": "2qb"}, {"endpoint": "43.139.2.249:5438", "ref": "Zg8"}}})
	// 280: UDP ref only in lines.json (an earlier offer).
	status(280, "L1M", "yg9")
	write(ff+"forward-280.offered.json", map[string]any{"at": 1, "offered": []map[string]any{{"endpoint": "106.75.137.85:5438", "ref": "yg9"}}})
	write(ff+"forward-280.lines.json", map[string]any{"L1M": map[string]any{"endpoint": "120.232.166.118:5438"}})
	// 281: its UDP ingress has no connection on this host.
	status(281, "Q", "Q")
	write(ff+"forward-281.offered.json", map[string]any{"at": 1, "offered": []map[string]any{{"endpoint": "1.2.3.4:6000", "ref": "Q"}}})
	// 282: UDP ref unknown → no seg.
	status(282, "nope", "nope")

	var asked []int
	m.segDump = func(ports []int) ([]lkSegSock, error) {
		asked = append([]int(nil), ports...)
		ap := netip.MustParseAddrPort
		return []lkSegSock{
			{Dst: ap("43.139.2.249:5438"), RttUs: 2667}, {Dst: ap("43.139.2.249:5438"), RttUs: 1879}, {Dst: ap("43.139.2.249:5438"), RttUs: 2474},
			{Dst: ap("120.232.166.118:5438"), RttUs: 5889}, {Dst: ap("120.232.166.118:5438"), RttUs: 0},
			{Dst: ap("106.75.137.85:5438"), RttUs: 30000}, // the TCP ingress of 280: not its 入口段
		}, nil
	}
	_, st := m.read()
	if st.Rtt != 2 || len(asked) != 2 || asked[0] != 5438 || asked[1] != 6000 {
		t.Fatalf("rtt %d asked %v", st.Rtt, asked)
	}
	by := map[int64]LKForward{}
	for _, f := range st.Forwards {
		by[f.ForwardID] = f
	}
	if s := by[263].Seg; s == nil || s.Endpoint != "43.139.2.249:5438" || s.Conns != 3 || s.Ms != 2.47 || s.MinMs != 1.87 {
		t.Fatalf("263 %+v", s)
	}
	if s := by[280].Seg; s == nil || s.Endpoint != "120.232.166.118:5438" || s.Conns != 2 || s.Ms != 5.88 {
		t.Fatalf("280 %+v", s)
	}
	if s := by[281].Seg; s == nil || s.Conns != 0 || s.Ms != 0 || s.Err != "" {
		t.Fatalf("281 %+v", s)
	}
	if s := by[282].Seg; s != nil {
		t.Fatalf("282 %+v", s)
	}
	// A forward without UDP selection (fixture 279) → no seg.
	if s := by[279].Seg; s != nil {
		t.Fatalf("279 %+v", s)
	}
	// The report key ignores 入口段 (it rides along, never makes a report due).
	key := lkReportKey(&LeiKwanHostReport{V: 1, State: st})
	st2 := st
	st2.Forwards = append([]LKForward(nil), st.Forwards...)
	for i := range st2.Forwards {
		if st2.Forwards[i].Seg != nil {
			c := *st2.Forwards[i].Seg
			c.Ms, c.Conns = 9.9, 7
			st2.Forwards[i].Seg = &c
		}
	}
	if lkReportKey(&LeiKwanHostReport{V: 1, State: st2}) != key {
		t.Fatal("入口段 changed the report key")
	}
	// The connection table cannot be read → Err, no number.
	m.segDump = func([]int) ([]lkSegSock, error) { return nil, errors.New("EPERM") }
	_, st = m.read()
	for _, f := range st.Forwards {
		if f.ForwardID == 263 && (f.Seg == nil || f.Seg.Err == "" || f.Seg.Ms != 0) {
			t.Fatalf("263 on error %+v", f.Seg)
		}
	}
}

// The real sock_diag path on this kernel: our own loopback connections show
// up under their remote port (and only those).
func TestLeiKwanSegDumpLoopback(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
	}
	time.Sleep(50 * time.Millisecond)
	socks, err := lkSegDump([]int{port})
	if err != nil {
		t.Skipf("sock_diag not available here: %v", err)
	}
	want := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port))
	n := 0
	for _, s := range socks {
		if s.Dst.Port() != uint16(port) {
			t.Fatalf("kernel filter let %v through", s.Dst)
		}
		if s.Dst == want {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("found %d of 3 connections: %+v", n, socks)
	}
}
