package dialer

import (
	"context"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	xlogger "github.com/go-gost/x/logger"
)

func TestIsLoopbackTarget(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:30001": true, "127.8.9.10:1": true, "[::1]:443": true, "::1": true,
		"1.2.3.4:80": false, "example.com:443": false, "localhost:80": false, "": false, "[2001:db8::1]:80": false,
	} {
		if got := isLoopbackTarget(addr); got != want {
			t.Errorf("isLoopbackTarget(%q) = %v, want %v", addr, got, want)
		}
	}
}

// An exit relay with an egress interface configured must still reach a local
// chain landing node on 127.0.0.1 (plan 108). Binding a loopback connection to
// a physical NIC fails on Linux, so the dialer skips the binding for loopback.
func TestDialLoopbackIgnoresEgressInterface(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("SO_BINDTODEVICE needs Linux and root")
	}
	ifce := ""
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		addrs, _ := i.Addrs()
		if i.Flags&net.FlagLoopback == 0 && i.Flags&net.FlagUp != 0 && len(addrs) > 0 {
			ifce = i.Name
			break
		}
	}
	if ifce == "" {
		t.Skip("no non-loopback interface")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d := &Dialer{Interface: ifce, Logger: xlogger.Nop()}
	c, err := d.Dial(ctx, "tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("loopback dial with interface %s: %v", ifce, err)
	}
	c.Close()
}
