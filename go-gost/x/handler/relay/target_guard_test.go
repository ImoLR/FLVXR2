package relay

import (
	"net"
	"testing"
)

func TestTargetGuard(t *testing.T) {
	own := []net.Addr{
		&net.IPNet{IP: net.ParseIP("203.0.113.5"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("2001:db8::5"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
	}
	saved, savedHook := ownAddrs, LocalTargetPortAllowed
	defer func() { ownAddrs, LocalTargetPortAllowed = saved, savedHook }()
	ownAddrs = &addrCache{load: func() ([]net.Addr, error) { return own, nil }}
	LocalTargetPortAllowed = func(port int) bool { return port == 30001 }

	cases := []struct {
		addr string
		ok   bool
	}{
		{"1.1.1.1:443", true},
		{"[2606:4700::1111]:443", true},
		{"8.8.8.8:53", true},
		{"127.0.0.1:10085", false}, // Core API / any loopback service
		{"127.0.0.1:30001", true},  // landing inbound (chain copy)
		{"127.1.2.3:30001", true},
		{"[::1]:10085", false},
		{"[::ffff:127.0.0.1]:22", false},
		{"0.0.0.0:22", false},
		{"10.0.0.1:80", false},
		{"10.0.0.1:30001", false}, // private but not this machine
		{"172.16.3.4:80", false},
		{"192.168.1.1:80", false},
		{"169.254.169.254:80", false},
		{"100.64.1.1:80", false},
		{"[fe80::1]:80", false},
		{"[fd00::1]:80", false},
		{"224.0.0.1:80", false},
		{"203.0.113.5:22", false},   // own public address
		{"203.0.113.5:30001", true}, // own address, landing inbound port
		{"[2001:db8::5]:22", false},
		{"203.0.113.6:22", true}, // neighbour on the public subnet
	}
	for _, c := range cases {
		addr, err := net.ResolveTCPAddr("tcp", c.addr)
		if err != nil {
			t.Fatalf("%s: %v", c.addr, err)
		}
		if got := checkTargetGuard(addr) == nil; got != c.ok {
			t.Errorf("%s: allowed=%v want %v", c.addr, got, c.ok)
		}
		uaddr, _ := net.ResolveUDPAddr("udp", c.addr)
		if got := checkTargetGuard(uaddr) == nil; got != c.ok {
			t.Errorf("udp %s: allowed=%v want %v", c.addr, got, c.ok)
		}
	}
	LocalTargetPortAllowed = nil
	if checkTargetGuard(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 30001}) == nil {
		t.Error("no landing hook: loopback must be refused")
	}
	if checkTargetGuard(nil) == nil {
		t.Error("nil remote must be refused")
	}
	for _, n := range []string{"unix", "serial"} {
		if !guardedNetwork(n) {
			t.Errorf("%s must be refused", n)
		}
	}
	if guardedNetwork("tcp") || guardedNetwork("udp") {
		t.Error("tcp/udp are checked by address")
	}
}
