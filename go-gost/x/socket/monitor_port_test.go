package socket

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/go-gost/x/config"
	"github.com/go-gost/x/landing"
	"github.com/go-gost/x/monitorport"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestDialErrorKind(t *testing.T) {
	op := func(e error) error {
		return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", e)}
	}
	for _, c := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{op(syscall.ECONNREFUSED), pingKindRefused},
		{op(syscall.EHOSTUNREACH), pingKindUnreach},
		{op(syscall.ENETUNREACH), pingKindUnreach},
		{op(syscall.EADDRNOTAVAIL), pingKindUnreach},
		{&net.OpError{Op: "dial", Net: "tcp", Err: timeoutErr{}}, pingKindTimeout},
		{context.DeadlineExceeded, pingKindTimeout},
		{&net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}, pingKindDNS},
		{errors.New("something else"), ""},
	} {
		if got := dialErrorKind(c.err); got != c.want {
			t.Errorf("%v → %q, want %q", c.err, got, c.want)
		}
	}
}

func TestTcpPingReportsKind(t *testing.T) {
	// refused: a port nothing listens on
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := l.Addr().(*net.TCPAddr).Port
	l.Close()
	res, err := (&WebSocketReporter{}).handleTcpPing(map[string]interface{}{"ip": "127.0.0.1", "port": closed, "count": 1, "timeout": 1000})
	if err != nil || res.Success || res.ErrorKind != pingKindRefused {
		t.Fatalf("closed port: %+v %v", res, err)
	}
	// dns
	_, _, kind, err := tcpPingHostKind("flvx-plan255.invalid", 80, 1, 500)
	if err == nil || kind != pingKindDNS {
		t.Fatalf("dns: kind %q err %v", kind, err)
	}
	// a check port: connects (its FIN close never turns into a failure)
	m := monitorport.New(monitorport.Options{
		Listen: func(p int) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)) },
		Logf:   func(string, ...interface{}) {},
	})
	defer m.CloseAll()
	if r := m.Open(closed); r.State != monitorport.StateOpen {
		t.Fatalf("open %+v", r)
	}
	for i := 0; i < 20; i++ {
		res, _ := (&WebSocketReporter{}).handleTcpPing(map[string]interface{}{"ip": "127.0.0.1", "port": closed, "count": 1, "timeout": 1000})
		if !res.Success || res.ErrorKind != "" {
			t.Fatalf("ping %d to the check port: %+v", i, res)
		}
	}
}

func TestListenPort(t *testing.T) {
	for in, want := range map[string]int{":123": 123, "0.0.0.0:2000": 2000, "[::]:3000": 3000, "4000": 4000, "": 0, "x": 0} {
		if got := listenPort(in); got != want {
			t.Errorf("%q → %d, want %d", in, got, want)
		}
	}
}

func TestMonitorPortInUseSeesConfiguredServices(t *testing.T) {
	prev := config.Global()
	defer config.Set(prev)
	config.Set(&config.Config{Services: []*config.ServiceConfig{{Name: "paused_rule", Addr: "[::]:23456"}}})
	w := &WebSocketReporter{}
	if busy, why := w.monitorPortInUse(23456); !busy || why == "" {
		t.Fatalf("configured service port not in use: %v %q", busy, why)
	}
	if busy, _ := w.monitorPortInUse(23457); busy {
		t.Fatal("free port reported in use")
	}
}

func TestYieldToServicesAndLanding(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	m := monitorport.Default()
	if r := m.Open(p); r.State != monitorport.StateOpen {
		t.Fatalf("open %+v", r)
	}
	yieldMonitorPortsToServices([]config.ServiceConfig{{Name: "rule_1", Addr: ":" + strconv.Itoa(p)}})
	if len(m.Ports()) != 0 {
		t.Fatal("service did not take the port back")
	}
	if r := m.Open(p); r.State != monitorport.StateOpen {
		t.Fatalf("reopen %+v", r)
	}
	yieldMonitorPortsToLanding(landing.Desired{Nodes: []landing.Node{{ID: 1, Port: p}}})
	if len(m.Ports()) != 0 {
		t.Fatal("landing inbound did not take the port back")
	}
	deadline := time.Now().Add(time.Second)
	for {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err == nil {
			ln.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("port not free after yield: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
