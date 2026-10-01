//go:build linux

package nftables

import (
	"net"
	"testing"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestRuleTagRoundTrip(t *testing.T) {
	want := ruleTag{ForwardID: 42, Protocol: "udp", Port: 31001, Role: roleDownload, Gen: 7}
	got, ok := parseRuleTag(&nftables.Rule{UserData: want.userData()})
	if !ok || got != want {
		t.Fatalf("parseRuleTag(%q) = %+v, %v; want %+v", want.String(), got, ok, want)
	}

	foreign := []*nftables.Rule{
		{},
		{UserData: ruleTag{Protocol: "tcp", Port: 1, Role: roleUpload, Gen: 1}.userData()}, // no forward id
		{UserData: []byte{0, 5, 'h', 'e', 'l', 'l', 0}},                                  // another comment
	}
	for i, rule := range foreign {
		if tag, ok := parseRuleTag(rule); ok {
			t.Fatalf("foreign rule %d parsed as %+v", i, tag)
		}
	}
}

func TestSpeedLimitMatchesGostLimiterRate(t *testing.T) {
	// The panel turns S Mbps into the gost limiter "$ S/8 MB S/8 MB" (1 MB = 1 MiB).
	if got := speedLimitBytesPerSecond(8); got != 1024*1024 {
		t.Fatalf("8 Mbps = %d B/s, want %d", got, 1024*1024)
	}
	if got := speedLimitBytesPerSecond(0); got != 0 {
		t.Fatalf("no limit = %d B/s, want 0", got)
	}
	if got := policerBurstBytes(1024 * 1024); got != 256*1024 {
		t.Fatalf("burst at 1 MiB/s = %d, want %d", got, 256*1024)
	}
	if got := policerBurstBytes(1000); got != 64*1024 {
		t.Fatalf("minimum burst = %d, want %d", got, 64*1024)
	}
}

func TestDNATPortFilterOnlyMatchesDNATedConnections(t *testing.T) {
	f := dnatPortFilter{proto: unix.IPPROTO_TCP, port: 31001}
	dnated := &netlink.ConntrackFlow{
		Forward: netlink.IPTuple{Protocol: unix.IPPROTO_TCP, SrcIP: net.ParseIP("10.0.0.2"), SrcPort: 40000, DstIP: net.ParseIP("10.0.0.1"), DstPort: 31001},
		Reverse: netlink.IPTuple{Protocol: unix.IPPROTO_TCP, SrcIP: net.ParseIP("10.0.1.2"), SrcPort: 41001, DstIP: net.ParseIP("10.0.1.1"), DstPort: 40000},
	}
	if !f.MatchConntrackFlow(dnated) {
		t.Fatal("DNATed connection of the forward port not matched")
	}
	own := &netlink.ConntrackFlow{ // this host connecting to a remote service on the same port
		Forward: netlink.IPTuple{Protocol: unix.IPPROTO_TCP, SrcIP: net.ParseIP("10.0.0.1"), SrcPort: 50000, DstIP: net.ParseIP("192.0.2.9"), DstPort: 31001},
		Reverse: netlink.IPTuple{Protocol: unix.IPPROTO_TCP, SrcIP: net.ParseIP("192.0.2.9"), SrcPort: 31001, DstIP: net.ParseIP("10.0.0.1"), DstPort: 50000},
	}
	if f.MatchConntrackFlow(own) {
		t.Fatal("connection that was not DNATed matched")
	}
	udp := *dnated
	udp.Forward.Protocol = unix.IPPROTO_UDP
	if f.MatchConntrackFlow(&udp) {
		t.Fatal("other protocol matched")
	}
}
