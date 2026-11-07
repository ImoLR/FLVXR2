package ipkey

import (
	"bytes"
	"net"
	"net/netip"
	"testing"
)

func TestOf(t *testing.T) {
	cases := map[string]string{
		"192.0.2.1":          "192.0.2.1",
		" 192.0.2.1 ":        "192.0.2.1",
		"::ffff:192.0.2.1":   "192.0.2.1",
		"[::ffff:192.0.2.1]": "192.0.2.1",
		"2409:8a4c:5c4f:26e1:1111:2222:3333:4444": "2409:8a4c:5c4f:26e1::/64",
		"2409:8a4c:5c4f:26e1::1":                  "2409:8a4c:5c4f:26e1::/64",
		"[2409:8a4c:5c4f:26e1::1]":                "2409:8a4c:5c4f:26e1::/64",
		"fe80::1%eth0":                            "fe80::/64",
		"2409:8a4c:5c4f:26e1::/64":                "2409:8a4c:5c4f:26e1::/64", // Core form is a fixed point
		"2409:8a4c:5c4f:26e1::9/128":              "2409:8a4c:5c4f:26e1::/64",
		"192.0.2.1/32":                            "192.0.2.1",
		"::ffff:192.0.2.1/128":                    "192.0.2.1",
		"not-an-ip":                               "not-an-ip",
		"":                                        "",
	}
	for in, want := range cases {
		if got := Of(in); got != want {
			t.Errorf("Of(%q) = %q, want %q", in, got, want)
		}
		if got := Of(Of(in)); got != Of(in) {
			t.Errorf("Of not idempotent for %q: %q", in, got)
		}
	}
}

// The Core form (netip.PrefixFrom(addr, 64).Masked().String()) is what the
// 妙妙屋 Core reports; FLVX keys must be byte-identical to it.
func TestOfMatchesCoreForm(t *testing.T) {
	for _, in := range []string{"2001:db8::1", "2001:db8:0:0:ffff::1", "2409:8a4c:5c4f:26e1:abcd::"} {
		if got := Of(in); got != Of(Of(in)) || got[len(got)-3:] != "/64" {
			t.Fatalf("Of(%q) = %q", in, got)
		}
	}
	for _, in := range []string{"2001:db8::1", "::1", "fe80::1", "2001:db8:0:0:1::", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "2409:8a4c:5c4f:26e1:1111:2222:3333:4444"} {
		core := netip.PrefixFrom(netip.MustParseAddr(in), 64).Masked().String()
		if got := Of(in); got != core {
			t.Fatalf("Of(%q) = %q, Core form %q", in, got, core)
		}
	}
	if Of("2001:db8::1") != "2001:db8::/64" {
		t.Fatalf("Of(2001:db8::1) = %q", Of("2001:db8::1"))
	}
}

func TestOfIPAndBytes(t *testing.T) {
	if got := OfIP(net.ParseIP("192.0.2.7")); got != "192.0.2.7" {
		t.Fatalf("OfIP v4 = %q", got)
	}
	if got := OfIP(net.ParseIP("2409:8a4c:5c4f:26e1:aaaa::1")); got != "2409:8a4c:5c4f:26e1::/64" {
		t.Fatalf("OfIP v6 = %q", got)
	}
	if OfIP(nil) != "" {
		t.Fatal("OfIP(nil) not empty")
	}
	if b := Bytes("192.0.2.7"); !bytes.Equal(b, []byte{192, 0, 2, 7}) {
		t.Fatalf("Bytes v4 = %v", b)
	}
	want := net.ParseIP("2409:8a4c:5c4f:26e1::").To16()
	for _, in := range []string{"2409:8a4c:5c4f:26e1::/64", "2409:8a4c:5c4f:26e1:1:2:3:4"} {
		if b := Bytes(in); !bytes.Equal(b, want) {
			t.Fatalf("Bytes(%q) = %v", in, b)
		}
	}
	if Bytes("junk") != nil {
		t.Fatal("Bytes(junk) not nil")
	}
	if Family("2409:8a4c:5c4f:26e1::/64") != "6" || Family("192.0.2.1") != "4" || Family("::ffff:192.0.2.1") != "4" || Family("junk") != "" {
		t.Fatal("Family mismatch")
	}
	if len(Mask6) != 16 || Mask6[7] != 0xff || Mask6[8] != 0 {
		t.Fatalf("Mask6 = %x", Mask6)
	}
}

func BenchmarkOfIPv4(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Of("203.0.113.77")
	}
}

func BenchmarkOfIPv6(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Of("2409:8a4c:5c4f:26e1:1111:2222:3333:4444")
	}
}
