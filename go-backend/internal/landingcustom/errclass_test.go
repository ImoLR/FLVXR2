package landingcustom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// Plan 271: every transport failure gets its class and its own text; the
// URL / cause never reaches the text.
func TestTransportClasses(t *testing.T) {
	wrap := func(err error) error { return &url.Error{Op: "Get", URL: "https://mmwxc.example/api?token=flvx_secret", Err: err} }
	cases := []struct {
		err  error
		want string
	}{
		{wrap(&net.DNSError{Err: "no such host", Name: "mmwxc.example"}), ClassDNS},
		{wrap(&net.DNSError{Err: "i/o timeout", Name: "mmwxc.example", IsTimeout: true}), ClassDNS},
		{wrap(&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}), ClassRefused},
		{wrap(&net.OpError{Op: "read", Err: syscall.ECONNRESET}), ClassReset},
		{wrap(&net.OpError{Op: "dial", Err: syscall.ENETUNREACH}), ClassUnreachable},
		{wrap(timeoutErr{}), ClassTimeout},
		{wrap(context.DeadlineExceeded), ClassTimeout},
		{wrap(context.Canceled), ClassCanceled},
		{wrap(io.EOF), ClassEOF},
		{wrap(errors.New("tls: handshake failure")), ClassTLS},
		{wrap(errors.New("something else")), ClassNet},
	}
	seen := map[string]string{}
	for _, c := range cases {
		got := classifyTransport(c.err)
		if got != c.want {
			t.Errorf("%v → %s, want %s", c.err, got, c.want)
		}
		te := &TransportError{Class: got, cause: c.err}
		if strings.Contains(te.Error(), "flvx_secret") || strings.Contains(te.Error(), "mmwxc") {
			t.Fatalf("text leaks the URL: %q", te.Error())
		}
		if ErrorClass(fmt.Errorf("wrapped: %w", te)) != got || !IsTransient(te) {
			t.Fatalf("class / transient of %v", te)
		}
		if prev, ok := seen[te.Error()]; ok && prev != got {
			t.Fatalf("classes %s and %s share the text %q", prev, got, te.Error())
		}
		seen[te.Error()] = got
	}
	if IsTransient(&TransportError{Class: ClassDecode}) {
		t.Fatal("a 200 with a bad body is not a network flap")
	}
	for status, transient := range map[int]bool{502: true, 503: true, 522: true, 429: true, 401: false, 404: false, 409: false, 422: false} {
		if IsTransient(&APIError{Status: status}) != transient || ErrorClass(&APIError{Status: status}) != fmt.Sprintf("http %d", status) {
			t.Fatalf("http %d transient/class", status)
		}
	}
	if ErrorClass(errors.New("local")) != "other" || IsTransient(errors.New("local")) {
		t.Fatal("local error class")
	}
}

// Real requests: a closed port is `refused`, a client timeout is `timeout`,
// a hung-up connection is `eof`/`reset`, a 502 stays `http 502`.
func TestTransportClassesLive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	ln.Close()
	c, _ := New(closed, "flvx_fixture")
	if _, err := c.Shares(context.Background()); ErrorClass(err) != ClassRefused || !strings.Contains(err.Error(), "拒绝连接") {
		t.Fatalf("closed port: %v (%s)", err, ErrorClass(err))
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer slow.Close()
	c, _ = New(slow.URL, "flvx_fixture")
	c.http.Timeout = 50 * time.Millisecond
	if _, err := c.Shares(context.Background()); ErrorClass(err) != ClassTimeout || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("slow server: %v (%s)", err, ErrorClass(err))
	}

	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer hang.Close()
	c, _ = New(hang.URL, "flvx_fixture")
	if _, err := c.Shares(context.Background()); ErrorClass(err) != ClassEOF && ErrorClass(err) != ClassReset {
		t.Fatalf("hang-up: %v (%s)", err, ErrorClass(err))
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer bad.Close()
	c, _ = New(bad.URL, "flvx_fixture")
	if _, err := c.Shares(context.Background()); ErrorClass(err) != "http 502" || !IsTransient(err) {
		t.Fatalf("502: %v (%s)", err, ErrorClass(err))
	}
}
