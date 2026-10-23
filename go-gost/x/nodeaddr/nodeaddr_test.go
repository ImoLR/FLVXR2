package nodeaddr

import (
	"fmt"
	"net/netip"
	"reflect"
	"testing"
)

func a(ip, iface string, opts ...func(*Addr)) Addr {
	out := Addr{IP: netip.MustParseAddr(ip), Iface: iface}
	for _, o := range opts {
		o(&out)
	}
	return out
}

func def(x *Addr)  { x.DefaultRoute = true }
func tmp(x *Addr)  { x.Temporary = true }
func depr(x *Addr) { x.Deprecated = true }
func tent(x *Addr) { x.Tentative = true }

func TestFilterKeepsStablePublicAddresses(t *testing.T) {
	got := Filter([]Addr{
		a("fe80::ca:8aff:fec0:2813", "eth0"),                      // link-local
		a("fd00::1", "eth0"),                                      // ULA
		a("2001:db8::5", "eth0"),                                  // documentation
		a("2408:820c:750b:dc64:1111:2222:3333:4444", "eth0", tmp), // privacy
		a("240e:b8f:a30:1904:ca:8aff:fec0:2813", "eth0", depr),    // old prefix, deprecated
		a("240e:b8f:72d:5c04:ca:8aff:fec0:9999", "eth0", tent),    // DAD running
		a("2408:820c:750b:dc64:ca:8aff:fec0:2813", "eth1"),
		a("240e:b8f:72d:5c04:ca:8aff:fec0:2813", "eth0", def),
		a("2400:1111::1", "docker0"),  // container bridge
		a("2400:2222::1", "wg0"),      // tunnel
		a("10.3.100.20", "eth0", def), // private
		a("100.64.1.2", "eth0"),       // CGNAT
		a("203.0.113.9", "eth0"),      // TEST-NET-3
		a("8.8.4.4", "eth1"),
		a("1.1.1.1", "eth0", def),
		a("1.1.1.1", "eth0", def), // duplicate
	})
	want := Report{
		V4: []string{"1.1.1.1", "8.8.4.4"},
		V6: []string{"240e:b8f:72d:5c04:ca:8aff:fec0:2813", "2408:820c:750b:dc64:ca:8aff:fec0:2813"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Filter = %+v, want %+v", got, want)
	}
}

func TestFilterCapsAndNeverNil(t *testing.T) {
	empty := Filter(nil)
	if empty.V4 == nil || empty.V6 == nil || len(empty.V4)+len(empty.V6) != 0 {
		t.Fatalf("empty report = %#v", empty)
	}
	var many []Addr
	for i := 0; i < 40; i++ {
		many = append(many, a(fmt.Sprintf("2400:cb00::%x", i+1), "eth0"))
	}
	if got := Filter(many); len(got.V6) != MaxPerFamily {
		t.Fatalf("cap: %d addresses", len(got.V6))
	}
}

func TestReportEqualIsOrderSensitive(t *testing.T) {
	x := Report{V6: []string{"2400::1", "2400::2"}}
	y := Report{V6: []string{"2400::2", "2400::1"}}
	if x.Equal(y) || !x.Equal(Report{V6: []string{"2400::1", "2400::2"}}) {
		t.Fatal("Equal must compare in order")
	}
}
