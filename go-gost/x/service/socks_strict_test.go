package service

import (
	"encoding/binary"
	"encoding/hex"
	"io"
	mrand "math/rand/v2"
	"os"
	"testing"
)

// Plan 182: first bytes captured from real SOCKS clients against a server that
// never answers (runs/socks-strict/capture/greetings.jsonl). Every client sends
// its greeting alone and waits.
var capturedSOCKSGreetings = []struct{ client, hex string }{
	{"curl --socks5", "05020001"},
	{"curl --socks5-hostname", "05020001"},
	{"curl --socks4", "040100500a09080700"},
	{"curl --socks4a", "0401005000000001006578616d706c652e7465737400"},
	{"curl socks5h://user:pass", "0503000102"},
	{"Go x/net/proxy", "050100"},
	{"Go x/net/proxy user/pass", "05020002"},
	{"PySocks socks5 rdns", "050100"},
	{"PySocks socks5 local dns", "050100"},
	{"PySocks socks5 user/pass", "05020002"},
	{"PySocks socks4", "040100500a09080700"},
	{"PySocks socks4a", "0401005000000001006578616d706c652e7465737400"},
	{"Xray socks outbound", "050100"},
	{"Xray socks outbound user/pass", "050102"},
	{"sing-box socks5", "050100"},
	{"sing-box socks5 user/pass", "050102"},
	{"sing-box socks4", "040100500a09080700"},
	{"sing-box socks4a", "0401005000000001006578616d706c652e7465737400"},
	{"Chromium socks5", "050100"},
	{"Chromium socks4", "040100500a09080700"},
}

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDetectSOCKSRealClientsStillBlocked(t *testing.T) {
	flags := protocolBlockFlags{http: 1, tls: 1, socks: 1}
	for _, c := range capturedSOCKSGreetings {
		data := mustHex(t, c.hex)
		if !detectSOCKS(data) {
			t.Errorf("%s greeting %s not detected", c.client, c.hex)
		}
		if !isProtocolBlocked(data, flags) {
			t.Errorf("%s greeting %s not blocked with socks=1", c.client, c.hex)
		}
		if isProtocolBlocked(data, protocolBlockFlags{http: 1, tls: 1}) {
			t.Errorf("%s greeting %s blocked with socks=0", c.client, c.hex)
		}
	}
}

func TestDetectSOCKSStrictShapes(t *testing.T) {
	long := make([]byte, 0, 300)
	long = append(long, 0x04, 0x01, 0x00, 0x50, 1, 2, 3, 4)
	for i := 0; i < socks4MaxField; i++ {
		long = append(long, 'a')
	}
	long = append(long, 0)
	tooLong := append(append(append([]byte(nil), long[:len(long)-1]...), 'a'), 0)

	cases := []struct {
		name string
		hex  string
		raw  []byte
		want bool
	}{
		{name: "socks5 no-auth", hex: "050100", want: true},
		{name: "socks5 user/pass only", hex: "050102", want: true},
		{name: "socks5 ten assigned methods", hex: "050a00010203040506070809", want: true},
		{name: "socks5 private method + no-auth", hex: "050280" + "00", want: true},
		{name: "socks5 greeting + pipelined request", hex: "050100" + "050100030b6578616d706c652e7465737400" + "50", want: false},
		{name: "socks5 one trailing byte", hex: "05010000", want: false},
		{name: "socks5 truncated", hex: "050200", want: false},
		{name: "socks5 zero methods", hex: "0500", want: false},
		{name: "socks5 eleven methods", hex: "050b0001020304050607080900", want: false},
		{name: "socks5 gssapi only", hex: "050101", want: false},
		{name: "socks5 unassigned method", hex: "0502000a", want: false},
		{name: "socks5 no-acceptable method", hex: "050200ff", want: false},
		{name: "socks4 connect", hex: "040100500a09080700", want: true},
		{name: "socks4 bind", hex: "040200500a09080700", want: true},
		{name: "socks4 userid", hex: "040100500a09080775736572" + "00", want: true},
		{name: "socks4 userid 255", raw: long, want: true},
		{name: "socks4 userid 256", raw: tooLong, want: false},
		{name: "socks4a host", hex: "0401005000000001006578616d706c652e7465737400", want: true},
		{name: "socks4 bad command", hex: "040300500a09080700", want: false},
		{name: "socks4 no terminator", hex: "040100500a090807", want: false},
		{name: "socks4 no terminator userid", hex: "040100500a0908077573", want: false},
		{name: "socks4 bytes after NUL", hex: "040100500a0908070000", want: false},
		{name: "socks4 non-printable userid", hex: "040100500a09080701" + "00", want: false},
		{name: "socks4a empty host", hex: "040100500000000100" + "00", want: false},
		{name: "socks4a host missing NUL", hex: "040100500000000100" + "6578", want: false},
		{name: "socks4a host non-printable", hex: "040100500000000100" + "65ff00", want: false},
		{name: "socks4 ip 0.0.0.0 is not 4a", hex: "040100500000000000" + "", want: true},
		{name: "empty", hex: "", want: false},
		{name: "two bytes", hex: "0501", want: false},
	}
	for _, c := range cases {
		data := c.raw
		if data == nil {
			data = mustHex(t, c.hex)
		}
		if got := detectSOCKS(data); got != c.want {
			t.Errorf("%s: detectSOCKS(%x) = %v, want %v", c.name, data, got, c.want)
		}
	}
}

// oldDetectSOCKS is the detector before plan 182, kept here to measure its
// false positives against the same buffers.
func oldDetectSOCKS(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	switch data[0] {
	case 0x04:
		if len(data) < 7 {
			return false
		}
		cmd := data[1]
		return cmd == 0x01 || cmd == 0x02
	case 0x05:
		if len(data) < 2 {
			return false
		}
		nMethods := int(data[1])
		if len(data) < 2+nMethods {
			return false
		}
		for _, method := range data[2 : 2+nMethods] {
			if method == 0x00 || method == 0x02 {
				return true
			}
		}
	}
	return false
}

// randomFirstPacket fills buf with a random "encrypted protocol" first read of
// a realistic length: Shadowsocks AEAD / SS2022 / VMess first packets are at
// least ~50 bytes (salt or auth id + length chunk + header + tag) and usually
// carry the client's first payload (a TLS ClientHello: ~500–1800 bytes).
func randomFirstPacket(r *mrand.ChaCha8, buf []byte, pick uint64) []byte {
	var n int
	switch pick % 4 {
	case 0:
		n = 40 + int(pick>>8%200) // small: header + short payload
	case 1:
		n = 240 + int(pick>>8%400)
	default:
		n = 500 + int(pick>>8%1300) // with a ClientHello
	}
	b := buf[:n]
	_, _ = r.Read(b)
	return b
}

func TestDetectSOCKSRandomFalsePositives(t *testing.T) {
	samples := 200000
	if testing.Short() {
		samples = 20000
	}
	r := mrand.NewChaCha8([32]byte{1, 8, 2})
	buf := make([]byte, 2048)
	oldHits, newHits := 0, 0
	for i := 0; i < samples; i++ {
		data := randomFirstPacket(r, buf, r.Uint64())
		if oldDetectSOCKS(data) {
			oldHits++
		}
		if detectSOCKS(data) {
			newHits++
		}
	}
	t.Logf("random first packets: %d, old detector hits %d (1/%d), new detector hits %d", samples, oldHits, samples/max(oldHits, 1), newHits)
	if newHits != 0 {
		t.Fatalf("new detector matched %d random encrypted first packets", newHits)
	}
	if oldHits == 0 {
		t.Fatal("old detector should have matched some random packets (sample too small?)")
	}
}

// TestSOCKSFalsePositiveCorpus runs old vs new over a corpus of real captured
// first reads (FLVX_FP_CORPUS: records of uint16 big-endian length + bytes)
// and, with FLVX_FP_RANDOM=N, N random buffers. Skipped without the env.
func TestSOCKSFalsePositiveCorpus(t *testing.T) {
	path := os.Getenv("FLVX_FP_CORPUS")
	if path == "" {
		t.Skip("FLVX_FP_CORPUS not set")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var hdr [2]byte
	buf := make([]byte, 65536)
	total, oldHits, newHits, old4, old5 := 0, 0, 0, 0, 0
	for {
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			break
		}
		n := int(binary.BigEndian.Uint16(hdr[:]))
		if _, err := io.ReadFull(f, buf[:n]); err != nil {
			t.Fatal(err)
		}
		data := buf[:n]
		total++
		if oldDetectSOCKS(data) {
			oldHits++
			if data[0] == 4 {
				old4++
			} else {
				old5++
			}
		}
		if detectSOCKS(data) {
			newHits++
		}
	}
	t.Logf("corpus %s: %d first reads, old hits %d (socks5 %d, socks4 %d; 1/%d), new hits %d", path, total, oldHits, old5, old4, total/max(oldHits, 1), newHits)
	if newHits != 0 {
		t.Errorf("new detector matched %d real first packets", newHits)
	}
}

func TestProtocolBlockCountersCountBlocksOnly(t *testing.T) {
	h0, t0, s0, o0 := ProtocolBlockCounts()
	flags := protocolBlockFlags{http: 1, tls: 1, socks: 1}
	isProtocolBlocked([]byte("GET / HTTP/1.1\r\n\r\n"), flags)
	isProtocolBlocked([]byte{0x16, 0x03, 0x01, 0x02, 0x00}, flags)
	isProtocolBlocked(mustHex(t, "050100"), flags)
	isProtocolBlocked(mustHex(t, "050100"), flags)
	isProtocolBlocked(mustHex(t, "0501000000"), flags)                    // not SOCKS, other allowed
	isProtocolBlocked(mustHex(t, "050100"), protocolBlockFlags{http: 1}) // SOCKS allowed
	isProtocolBlocked([]byte{0x99, 0x01}, protocolBlockFlags{blockOther: 1})
	h1, t1, s1, o1 := ProtocolBlockCounts()
	if h1-h0 != 1 || t1-t0 != 1 || s1-s0 != 2 || o1-o0 != 1 {
		t.Fatalf("counters delta http=%d tls=%d socks=%d other=%d, want 1/1/2/1", h1-h0, t1-t0, s1-s0, o1-o0)
	}
}

func benchCorpus() [][]byte {
	r := mrand.NewChaCha8([32]byte{7})
	out := make([][]byte, 0, 1024)
	for i := 0; i < 1000; i++ {
		buf := make([]byte, 2048)
		out = append(out, randomFirstPacket(r, buf, r.Uint64()))
	}
	for _, c := range capturedSOCKSGreetings {
		b, _ := hex.DecodeString(c.hex)
		out = append(out, b)
	}
	return out
}

func BenchmarkDetectSOCKSOld(b *testing.B) {
	corpus := benchCorpus()
	b.ReportAllocs()
	b.ResetTimer()
	hits := 0
	for i := 0; i < b.N; i++ {
		if oldDetectSOCKS(corpus[i%len(corpus)]) {
			hits++
		}
	}
	_ = hits
}

func BenchmarkDetectSOCKSNew(b *testing.B) {
	corpus := benchCorpus()
	b.ReportAllocs()
	b.ResetTimer()
	hits := 0
	for i := 0; i < b.N; i++ {
		if detectSOCKS(corpus[i%len(corpus)]) {
			hits++
		}
	}
	_ = hits
}

// BenchmarkIsProtocolBlocked: the whole per-connection check with the 8 prod
// entries' flags (http=tls=socks=1), random first reads (SOCKS hits counted).
func BenchmarkIsProtocolBlocked(b *testing.B) {
	corpus := benchCorpus()[:1000]
	flags := protocolBlockFlags{http: 1, tls: 1, socks: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isProtocolBlocked(corpus[i%len(corpus)], flags)
	}
}
