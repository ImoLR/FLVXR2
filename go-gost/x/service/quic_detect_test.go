package service

import (
	"context"
	"crypto/aes"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	mrand "math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// captureQUICInitials dials a local UDP socket that never answers with
// quic-go (the library behind hysteria2, TUIC, Juicity, sing-box / Xray QUIC)
// and returns the first datagrams it sends: the Initial and its retransmits.
func captureQUICInitials(t testing.TB, version quic.Version, n int) [][]byte {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		_, _ = quic.DialAddr(ctx, pc.LocalAddr().String(),
			&tls.Config{ServerName: "www.example.com", NextProtos: []string{"h3"}, InsecureSkipVerify: true},
			&quic.Config{Versions: []quic.Version{version}, HandshakeIdleTimeout: 3 * time.Second})
	}()
	var out [][]byte
	buf := make([]byte, 65535)
	for len(out) < n {
		_ = pc.SetReadDeadline(time.Now().Add(3 * time.Second))
		m, _, err := pc.ReadFrom(buf)
		if err != nil {
			t.Fatalf("captured %d of %d datagrams: %v", len(out), n, err)
		}
		out = append(out, append([]byte(nil), buf[:m]...))
	}
	return out
}

func TestQUICInitialLiveQuicGo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version quic.Version
	}{{"v1", quic.Version1}, {"v2", quic.Version2}} {
		t.Run(tc.name, func(t *testing.T) {
			grams := captureQUICInitials(t, tc.version, 2)
			for i, g := range grams {
				if !isQUICInitial(g) {
					t.Fatalf("datagram %d (%d bytes, %x…) not detected", i, len(g), g[:16])
				}
				// The same packet cut below the RFC 9000 minimum is not a
				// client Initial any more.
				if isQUICInitial(g[:quicMinInitialDatagram-1]) {
					t.Fatalf("datagram %d cut to 1199 bytes still detected", i)
				}
			}
		})
	}
}

// Lab captures (runs/quic-block/lab): the first datagram real clients sent to
// a socket that never answers. One hex file per client.
func TestQUICInitialCapturedClients(t *testing.T) {
	files, _ := filepath.Glob("testdata/quic/*.hex")
	if len(files) == 0 {
		t.Skip("no captures")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		want := !strings.Contains(filepath.Base(f), "pass-")
		if got := isQUICInitial(b); got != want {
			t.Errorf("%s (%d bytes): detected=%v want %v", filepath.Base(f), len(b), got, want)
		}
	}
}

// quicTestInitial builds a v1-style Initial header with the given fields and
// pads the datagram to size.
func quicTestInitial(first byte, version uint32, dcid, scid, token int, length uint64, size int) []byte {
	b := make([]byte, 0, size)
	b = append(b, first)
	b = binary.BigEndian.AppendUint32(b, version)
	b = append(b, byte(dcid))
	b = append(b, make([]byte, dcid)...)
	b = append(b, byte(scid))
	b = append(b, make([]byte, scid)...)
	b = quicAppendVarint(b, uint64(token))
	b = append(b, make([]byte, token)...)
	b = quicAppendVarint(b, length)
	for len(b) < size {
		b = append(b, 0)
	}
	return b
}

func quicAppendVarint(b []byte, v uint64) []byte {
	switch {
	case v < 1<<6:
		return append(b, byte(v))
	case v < 1<<14:
		return binary.BigEndian.AppendUint16(b, uint16(v)|0x4000)
	case v < 1<<30:
		return binary.BigEndian.AppendUint32(b, uint32(v)|0x80000000)
	default:
		return binary.BigEndian.AppendUint64(b, v|0xc000000000000000)
	}
}

func TestQUICInitialHeaderRules(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		want bool
	}{
		{"v1 initial", quicTestInitial(0xc3, quicVersion1, 8, 0, 0, 1180, 1200), true},
		{"v1 initial cid 20/20 token 64", quicTestInitial(0xc0, quicVersion1, 20, 20, 64, 1000, 1350), true},
		{"v2 initial (type 1)", quicTestInitial(0xd3, quicVersion2, 8, 8, 0, 1150, 1252), true},
		{"draft-29 initial", quicTestInitial(0xc1, 0xff00001d, 8, 0, 0, 1180, 1200), true},
		{"draft-34 initial", quicTestInitial(0xc1, 0xff000022, 8, 0, 0, 1180, 1200), true},
		{"1199 bytes", quicTestInitial(0xc3, quicVersion1, 8, 0, 0, 1170, 1199), false},
		{"short header", append([]byte{0x43}, make([]byte, 1299)...), false},
		{"no fixed bit", quicTestInitial(0x83, quicVersion1, 8, 0, 0, 1180, 1200), false},
		{"v1 0-RTT", quicTestInitial(0xd3, quicVersion1, 8, 0, 0, 1180, 1200), false},
		{"v1 handshake", quicTestInitial(0xe3, quicVersion1, 8, 0, 0, 1180, 1200), false},
		{"v1 retry", quicTestInitial(0xf3, quicVersion1, 8, 0, 0, 1180, 1200), false},
		{"v2 with v1 initial bits", quicTestInitial(0xc3, quicVersion2, 8, 0, 0, 1180, 1200), false},
		{"version negotiation", quicTestInitial(0xc3, 0, 8, 0, 0, 1180, 1200), false},
		{"unknown version", quicTestInitial(0xc3, 0x0a0a0a0a, 8, 0, 0, 1180, 1200), false},
		{"draft-26", quicTestInitial(0xc3, 0xff00001a, 8, 0, 0, 1180, 1200), false},
		{"dcid 21", quicTestInitial(0xc3, quicVersion1, 21, 0, 0, 1100, 1200), false},
		{"scid 21", quicTestInitial(0xc3, quicVersion1, 8, 21, 0, 1100, 1200), false},
		{"token past end", quicTestInitial(0xc3, quicVersion1, 8, 0, 1300, 1, 1350)[:1300], false},
		{"length 19", quicTestInitial(0xc3, quicVersion1, 8, 0, 0, 19, 1200), false},
		{"length past end", quicTestInitial(0xc3, quicVersion1, 8, 0, 0, 1190, 1200), false},
		{"length 8-byte varint", quicTestInitial(0xc3, quicVersion1, 8, 0, 0, 1<<31, 1200), false},
	}
	q046 := append([]byte{0xc3, 'Q', '0', '4', '6', 0x50}, make([]byte, 1294)...)
	q050 := append([]byte{0xc3, 'Q', '0', '5', '0', 8, 1, 2, 3, 4, 5, 6, 7, 8, 0}, make([]byte, 1285)...)
	t051 := append([]byte{0xc3, 'T', '0', '5', '1', 8, 1, 2, 3, 4, 5, 6, 7, 8, 0}, make([]byte, 1285)...)
	q050scid := append([]byte{0xc3, 'Q', '0', '5', '0', 8, 1, 2, 3, 4, 5, 6, 7, 8, 8}, make([]byte, 1285)...)
	cases = append(cases,
		struct {
			name string
			b    []byte
			want bool
		}{"gQUIC Q046", q046, true},
		struct {
			name string
			b    []byte
			want bool
		}{"gQUIC Q050", q050, true},
		struct {
			name string
			b    []byte
			want bool
		}{"gQUIC T051", t051, true},
		struct {
			name string
			b    []byte
			want bool
		}{"gQUIC Q050 with SCID", q050scid, false},
	)
	for _, tc := range cases {
		if got := isQUICInitial(tc.b); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

// quicFuzzN: QUIC_FUZZ_N=1000000000 for the plan-doc run; default keeps
// `go test ./...` quick.
func quicFuzzN(def int) int {
	if v, err := strconv.Atoi(os.Getenv("QUIC_FUZZ_N")); err == nil && v > 0 {
		return v
	}
	return def
}

// TestQUICRandomDatagramsNeverMatch: random datagrams of 1200–1500 bytes (the
// size band where the check runs; SS / SS2022 / Salamander / mieru look like
// this). Only the first 64 bytes are re-randomized per datagram: the check
// never reads further. Also measures the acceptance rate with the version
// forced to v1 to confirm the per-field probabilities of the plan doc.
func TestQUICRandomDatagramsNeverMatch(t *testing.T) {
	n := quicFuzzN(20_000_000)
	r := mrand.New(mrand.NewPCG(283, uint64(time.Now().UnixNano())))
	buf := make([]byte, 1500)
	for i := range buf {
		buf[i] = byte(r.Uint32())
	}
	hits, structHits, firstByte := 0, 0, 0
	for i := 0; i < n; i++ {
		for j := 0; j < 64; j += 8 {
			binary.LittleEndian.PutUint64(buf[j:], r.Uint64())
		}
		b := buf[:1200+int(r.Uint32N(301))]
		if isQUICInitial(b) {
			hits++
			t.Errorf("random datagram matched: %x", b[:64])
		}
		if b[0]&0xF0 == 0xC0 {
			firstByte++
			b[1], b[2], b[3], b[4] = 0, 0, 0, 1
			if isQUICInitial(b) {
				structHits++
			}
		}
	}
	rate := float64(structHits) / float64(n)
	// IETF versions accepted: v1, v2, drafts 27–34 = 10 of 2^32.
	t.Logf("random %d: hits %d; first byte C0–CF %d (%.4f, want 1/16); with v1 forced %d (%.3g per datagram, model 2.2e-5) → full check ≈ %.2g per datagram (×10/2^32) + gQUIC ≈ 5.7e-14",
		n, hits, firstByte, float64(firstByte)/float64(n), structHits, rate, rate*10/4294967296)
}

// TestQUICShadowsocksUDPNeverMatch: SS2022 UDP packets as sing-box / Xray
// build them — AES block-encrypted session ID + packet ID header (aes-gcm
// methods), random 24-byte nonce (chacha20-poly1305), legacy AEAD salt — at
// every size between 1200 and 1500 bytes.
func TestQUICShadowsocksUDPNeverMatch(t *testing.T) {
	n := quicFuzzN(20_000_000) / 10
	r := mrand.New(mrand.NewPCG(2022, uint64(time.Now().UnixNano())))
	key := make([]byte, 16)
	for i := range key {
		key[i] = byte(r.Uint32())
	}
	block, _ := aes.NewCipher(key)
	buf := make([]byte, 1500)
	for i := range buf {
		buf[i] = byte(r.Uint32())
	}
	var hdr [16]byte
	session := r.Uint64()
	for i := 0; i < n; i++ {
		if i%100000 == 0 {
			session = r.Uint64() // a new client session now and then
		}
		binary.BigEndian.PutUint64(hdr[:8], session)
		binary.BigEndian.PutUint64(hdr[8:], uint64(i))
		size := 1200 + i%301
		block.Encrypt(buf[:16], hdr[:]) // 2022-blake3-aes-*-gcm separate header
		for j := 16; j < 64; j += 8 {
			binary.LittleEndian.PutUint64(buf[j:], r.Uint64())
		}
		if isQUICInitial(buf[:size]) {
			t.Fatalf("SS2022 aes packet %d matched: %x", i, buf[:32])
		}
		for j := 0; j < 64; j += 8 { // chacha20 nonce / legacy salt: random
			binary.LittleEndian.PutUint64(buf[j:], r.Uint64())
		}
		if isQUICInitial(buf[:size]) {
			t.Fatalf("SS random-prefix packet %d matched: %x", i, buf[:32])
		}
	}
}

// Other UDP our users run: none of these can look like a QUIC Initial
// (first byte or version layout), even padded to 1200+ bytes.
func TestQUICOtherUDPNeverMatch(t *testing.T) {
	pad := func(b []byte) []byte { return append(b, make([]byte, 1300-len(b))...) }
	dns, _ := hex.DecodeString("c3a1010000010000000000010377777706676f6f676c6503636f6d0000010001000029100000000000000c000a0008")
	cases := map[string][]byte{
		"DNS query (ID c3a1)":          pad(dns),
		"WireGuard initiation":         pad([]byte{1, 0, 0, 0, 0xde, 0xad}),
		"WireGuard data":               pad([]byte{4, 0, 0, 0, 0xc3, 0, 0, 0, 1}),
		"RTP":                          pad([]byte{0x80, 0x6f, 0x12, 0x34}),
		"STUN binding":                 pad([]byte{0x00, 0x01, 0x00, 0x00, 0x21, 0x12, 0xa4, 0x42}),
		"DTLS ClientHello":             pad([]byte{0x16, 0xfe, 0xfd, 0, 0, 0, 0, 0, 0, 0, 0}),
		"OpenVPN hard reset":           pad([]byte{0x38, 1, 2, 3, 4, 5, 6, 7, 8}),
		"QUIC v1 short header (1-RTT)": pad([]byte{0x47, 1, 2, 3, 4, 5, 6, 7, 8}),
	}
	for name, b := range cases {
		if isQUICInitial(b) {
			t.Errorf("%s matched", name)
		}
	}
}

// fakeUDPSession feeds queued datagrams to the guard.
type fakeUDPSession struct {
	net.Conn
	in      [][]byte
	blocked int
}

func (f *fakeUDPSession) Read(b []byte) (int, error) {
	if len(f.in) == 0 {
		return 0, io.EOF
	}
	n := copy(b, f.in[0])
	f.in = f.in[1:]
	return n, nil
}

func (f *fakeUDPSession) Block() { f.blocked++ }

func TestQUICGuardChecksEveryDatagram(t *testing.T) {
	initial := captureQUICInitials(t, quic.Version1, 1)[0]
	r := mrand.New(mrand.NewPCG(1, 2))
	junk := make([]byte, 1350)
	for i := range junk {
		junk[i] = byte(r.Uint32())
	}
	junk[0] = 0x12
	small := []byte("hello")
	sess := &fakeUDPSession{in: [][]byte{small, junk, small, initial, small}}
	_, _, _, _, q0 := ProtocolBlockCounts()
	g := wrapUDPSession(sess, protocolBlockFlags{tls: 1})
	buf := make([]byte, 65535)
	for i, want := range []int{len(small), len(junk), len(small)} {
		n, err := g.Read(buf)
		if err != nil || n != want {
			t.Fatalf("datagram %d: n=%d err=%v, want %d bytes passed", i, n, err, want)
		}
	}
	n, err := g.Read(buf)
	if n != 0 || !errors.Is(err, errConnectionBlocked) {
		t.Fatalf("QUIC Initial after junk: n=%d err=%v, want blocked", n, err)
	}
	if sess.blocked != 1 {
		t.Fatalf("session Block() called %d times, want 1", sess.blocked)
	}
	if _, _, _, _, q1 := ProtocolBlockCounts(); q1-q0 != 1 {
		t.Fatalf("quic counter delta %d, want 1", q1-q0)
	}
	if class := handlerErrorClass(err); class != "blocked" {
		t.Fatalf("handler error class %q, want blocked (no handler error log line)", class)
	}
}

func TestWrapUDPSessionOnlyWhenTLSBlocked(t *testing.T) {
	sess := &fakeUDPSession{}
	for _, tc := range []struct {
		flags protocolBlockFlags
		guard bool
	}{
		{protocolBlockFlags{}, false},
		{protocolBlockFlags{http: 1, socks: 1}, false},
		{protocolBlockFlags{http: 1, socks: 1, blockOther: 1}, false},
		{protocolBlockFlags{tls: 1}, true},
		{protocolBlockFlags{http: 1, tls: 1, socks: 1}, true},
	} {
		c := wrapUDPSession(sess, tc.flags)
		_, isGuard := c.(*quicGuardConn)
		if isGuard != tc.guard || (!tc.guard && c != net.Conn(sess)) {
			t.Errorf("flags %+v: wrapped %T, want guard=%v", tc.flags, c, tc.guard)
		}
	}
	// Flag off: a QUIC Initial passes untouched.
	initial := quicTestInitial(0xc3, quicVersion1, 8, 0, 0, 1180, 1200)
	sess.in = [][]byte{initial}
	c := wrapUDPSession(sess, protocolBlockFlags{http: 1, socks: 1})
	if n, err := c.Read(make([]byte, 2048)); n != len(initial) || err != nil {
		t.Fatalf("flag off: n=%d err=%v", n, err)
	}
}

func TestServiceWrapsUDPSessionsWithoutTCPDetector(t *testing.T) {
	sess := &fakeUDPSession{}
	tlsSvc := NewService("udp-tls", nil, nil, ProtocolFilterOption(1, 1, 1, 0)).(*defaultService)
	if _, ok := tlsSvc.wrapProtocolDetection(sess).(*quicGuardConn); !ok {
		t.Fatal("tls=1 UDP session not guarded")
	}
	lineSvc := NewService("udp-line", nil, nil, ProtocolFilterOption(1, 0, 1, 0)).(*defaultService)
	if c := lineSvc.wrapProtocolDetection(sess); c != net.Conn(sess) {
		t.Fatalf("tls=0 UDP session wrapped in %T, want untouched (no bufio per session)", c)
	}
	// TCP conns keep the first-read detector.
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, ok := tlsSvc.wrapProtocolDetection(a).(*detectConn); !ok {
		t.Fatal("TCP conn lost the first-read detector")
	}
}

func BenchmarkQUICCheckRandomDatagram(b *testing.B) {
	r := mrand.New(mrand.NewPCG(3, 4))
	grams := make([][]byte, 1024)
	for i := range grams {
		g := make([]byte, 1200+r.IntN(301))
		for j := range g {
			g[j] = byte(r.Uint32())
		}
		grams[i] = g
	}
	b.ReportAllocs()
	b.ResetTimer()
	hits := 0
	for i := 0; i < b.N; i++ {
		g := grams[i&1023]
		if len(g) >= quicMinInitialDatagram && g[0]&0xC0 == 0xC0 && isQUICInitial(g) {
			hits++
		}
	}
	if hits != 0 {
		b.Fatal(hits)
	}
}

func BenchmarkQUICCheckLongHeaderLike(b *testing.B) {
	g := quicTestInitial(0xc3, 0x0a0a0a0a, 8, 0, 0, 1180, 1350) // passes the first byte, fails the version
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if isQUICInitial(g) {
			b.Fatal("hit")
		}
	}
}

func BenchmarkQUICCheckInitial(b *testing.B) {
	g := quicTestInitial(0xc3, quicVersion1, 8, 0, 0, 1180, 1252)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !isQUICInitial(g) {
			b.Fatal("miss")
		}
	}
}

// loopSession hands the same datagram back on every read (no queue cost).
type loopSession struct {
	net.Conn
	g []byte
}

func (l *loopSession) Read(b []byte) (int, error) { return copy(b, l.g), nil }
func (l *loopSession) Block()                     {}

func benchmarkSessionRead(b *testing.B, size int, guarded bool) {
	r := mrand.New(mrand.NewPCG(5, 6))
	g := make([]byte, size)
	for j := range g {
		g[j] = byte(r.Uint32())
	}
	g[0] = 0xd7 // long-header-looking: the guard runs its full check
	var c net.Conn = &loopSession{g: g}
	if guarded {
		c = wrapUDPSession(c.(quicSession), protocolBlockFlags{tls: 1})
	}
	buf := make([]byte, 65535)
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Read(buf); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUDPSessionRead1350Raw(b *testing.B)     { benchmarkSessionRead(b, 1350, false) }
func BenchmarkUDPSessionRead1350Guarded(b *testing.B) { benchmarkSessionRead(b, 1350, true) }
func BenchmarkUDPSessionRead200Raw(b *testing.B)      { benchmarkSessionRead(b, 200, false) }
func BenchmarkUDPSessionRead200Guarded(b *testing.B)  { benchmarkSessionRead(b, 200, true) }
