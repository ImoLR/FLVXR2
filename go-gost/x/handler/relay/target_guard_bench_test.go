package relay

import (
	"net"
	"testing"
)

// Per-connection cost of the exit target guard on a public target (the hot path).
func benchGuardSetup(b *testing.B) func() {
	saved := ownAddrs
	ownAddrs = &addrCache{load: func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("203.0.113.5"), Mask: net.CIDRMask(24, 32)}}, nil
	}}
	return func() { ownAddrs = saved }
}

func BenchmarkTargetGuardPublic(b *testing.B) {
	defer benchGuardSetup(b)()
	addr := &net.TCPAddr{IP: net.ParseIP("1.1.1.1").To4(), Port: 443}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if checkTargetGuard(addr) != nil {
			b.Fatal("refused")
		}
	}
}

func BenchmarkTargetGuardPublicParallel(b *testing.B) {
	defer benchGuardSetup(b)()
	addr := &net.TCPAddr{IP: net.ParseIP("1.1.1.1").To4(), Port: 443}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if checkTargetGuard(addr) != nil {
				b.Fatal("refused")
			}
		}
	})
}

// Real net.InterfaceAddrs refresh (what one connection per minute paid inline before the fix).
func BenchmarkInterfaceAddrsRefresh(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := net.InterfaceAddrs(); err != nil {
			b.Fatal(err)
		}
	}
}
