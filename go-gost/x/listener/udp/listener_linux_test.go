//go:build linux

package udp

import (
	"net"
	"testing"
	"time"

	"github.com/go-gost/core/listener"
	corestats "github.com/go-gost/core/observer/stats"
	xlogger "github.com/go-gost/x/logger"
	mdx "github.com/go-gost/x/metadata"
	xstats "github.com/go-gost/x/observer/stats"
)

// Plan 150: a rule's UDP listener on [::] answers a client that used a
// secondary local address (127.0.0.2 here, node 48's CU IPv6 in production)
// from that same address, and the service stats still count both directions.
func TestUDPListenerAnswersFromTheDialledAddress(t *testing.T) {
	st := xstats.NewStats(false)
	ln := NewListener(
		listener.AddrOption("[::]:0"),
		listener.ServiceOption("150_1_1_udp"),
		listener.StatsOption(st),
		listener.LoggerOption(xlogger.Nop()),
	)
	if err := ln.Init(mdx.NewMetadata(map[string]any{"keepalive": true})); err != nil {
		t.Skipf("dual-stack listener unavailable: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.UDPAddr).Port
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 512)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					if _, err := c.Write(buf[:n]); err != nil {
						return
					}
				}
			}(c)
		}
	}()

	for _, ip := range []string{"127.0.0.1", "127.0.0.2"} {
		c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP(ip), Port: port})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Write([]byte("hello-" + ip)); err != nil {
			t.Fatal(err)
		}
		_ = c.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		buf := make([]byte, 64)
		n, err := c.Read(buf)
		c.Close()
		if err != nil || string(buf[:n]) != "hello-"+ip {
			t.Fatalf("client -> %s: reply %q err %v", ip, buf[:n], err)
		}
	}
	if in, out := st.Get(corestats.KindInputBytes), st.Get(corestats.KindOutputBytes); in == 0 || out == 0 {
		t.Fatalf("service stats lost traffic: in=%d out=%d", in, out)
	}
}
