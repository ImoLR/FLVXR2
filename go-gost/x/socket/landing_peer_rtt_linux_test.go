//go:build linux

package socket

import (
	"net"
	"testing"
	"time"
)

// Plan 301: LandingPeerRtt reads the kernel rtt of the inbound connections of
// one port and groups them by remote IP.
func TestLandingPeerRttLoopback(t *testing.T) {
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
			go func(c net.Conn) {
				buf := make([]byte, 64)
				for {
					n, err := c.Read(buf)
					if err != nil {
						c.Close()
						return
					}
					_, _ = c.Write(buf[:n])
				}
			}(c)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	var conns []net.Conn
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		_, _ = c.Write([]byte("ping"))
		buf := make([]byte, 4)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Read(buf); err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	res, err := handleLandingPeerRtt(map[string]interface{}{"port": port})
	if err != nil {
		t.Fatalf("handleLandingPeerRtt: %v", err)
	}
	if res.V != 1 || res.Port != port || res.Total != len(conns) || len(res.Peers) != 1 {
		t.Fatalf("unexpected result %+v", res)
	}
	p := res.Peers[0]
	if p.IP != "127.0.0.1" || p.Conns != 3 || p.RttMs <= 0 || p.RttMs > 50 {
		t.Fatalf("unexpected peer %+v", p)
	}
	if _, err := handleLandingPeerRtt(map[string]interface{}{"port": 0}); err == nil {
		t.Fatal("port 0 accepted")
	}
}

func TestSummarizeLandingPeersOrderAndCap(t *testing.T) {
	samples := []landingPeerSample{
		{LocalPort: 443, Remote: "203.0.113.9", RttUs: 30000, MinRttUs: 28000},
		{LocalPort: 443, Remote: "203.0.113.9", RttUs: 32000, MinRttUs: 27000},
		{LocalPort: 443, Remote: "198.51.100.1", RttUs: 5000},
		{LocalPort: 8443, Remote: "198.51.100.2", RttUs: 1000},
	}
	r := summarizeLandingPeers(443, 1, samples)
	if r.Total != 3 || len(r.Peers) != 1 || !r.Truncated {
		t.Fatalf("unexpected %+v", r)
	}
	if p := r.Peers[0]; p.IP != "203.0.113.9" || p.Conns != 2 || p.RttMs != 32 || p.MinRttMs != 27 {
		t.Fatalf("unexpected peer %+v", p)
	}
}
