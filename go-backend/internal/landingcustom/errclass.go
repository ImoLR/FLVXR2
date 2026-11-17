package landingcustom

// Plan 271: a failed Custom request says what kind of failure it was, so the
// panel log can tell a DNS hiccup on the panel host from a Cloudflare 52x or
// a Custom 5xx, and the UI no longer shows one 「妙妙屋连接失败」 for all.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
)

// Transport error classes (no HTTP response was read).
const (
	ClassDNS         = "dns"
	ClassTimeout     = "timeout"
	ClassRefused     = "refused"
	ClassReset       = "reset"
	ClassUnreachable = "unreachable"
	ClassTLS         = "tls"
	ClassEOF         = "eof"
	ClassCanceled    = "canceled"
	ClassNet         = "net"
	ClassRead        = "read"
	ClassDecode      = "decode"
)

var transportText = map[string]string{
	ClassDNS:         "妙妙屋域名解析失败（DNS），请检查面板主机的网络或 DNS",
	ClassTimeout:     "连接妙妙屋超时，请检查网络",
	ClassRefused:     "妙妙屋拒绝连接（服务未运行或端口未开放）",
	ClassReset:       "与妙妙屋的连接被重置",
	ClassUnreachable: "妙妙屋网络不可达，请检查面板主机的路由",
	ClassTLS:         "与妙妙屋的 TLS 握手失败（证书或中间网络异常）",
	ClassEOF:         "妙妙屋连接意外断开（没有收到响应）",
	ClassCanceled:    "妙妙屋请求已取消",
	ClassNet:         "妙妙屋连接失败，请检查地址或网络",
	ClassRead:        "妙妙屋响应读取失败",
	ClassDecode:      "妙妙屋响应格式错误",
}

// TransportError is a Custom request that got no usable HTTP answer. The
// cause is never shown (it holds the URL); only the class.
type TransportError struct {
	Class string
	cause error
}

func (e *TransportError) Error() string {
	if t, ok := transportText[e.Class]; ok {
		return t
	}
	return transportText[ClassNet]
}

func (e *TransportError) Unwrap() error { return e.cause }

// Transient: a failure that may clear by itself (network, timeouts, 5xx, 429)
// as opposed to a verdict of Custom about the request (4xx).
func (e *TransportError) Transient() bool { return e.Class != ClassDecode }

// classifyTransport maps an http.Client / body read error to a class.
func classifyTransport(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ClassDNS
	}
	if errors.Is(err, context.Canceled) {
		return ClassCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ClassTimeout
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return ClassRefused
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, syscall.ECONNABORTED):
		return ClassReset
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return ClassUnreachable
	}
	var recErr tls.RecordHeaderError
	var alert tls.AlertError
	var unknownCA x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalid x509.CertificateInvalidError
	var certVerify *tls.CertificateVerificationError
	if errors.As(err, &recErr) || errors.As(err, &alert) || errors.As(err, &unknownCA) || errors.As(err, &hostErr) || errors.As(err, &invalid) || errors.As(err, &certVerify) {
		return ClassTLS
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ClassTimeout
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ClassEOF
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:"):
		return ClassTLS
	case strings.Contains(msg, "connection reset"):
		return ClassReset
	case strings.Contains(msg, "connection refused"):
		return ClassRefused
	case strings.Contains(msg, "server closed idle connection"), strings.HasSuffix(msg, "EOF"):
		return ClassEOF
	}
	return ClassNet
}

// ErrorClass names the failure of a Custom call for logs: a transport class,
// `http <code>` (Custom or a proxy in front of it answered), or `other`
// (a local error such as a refused payload).
func ErrorClass(err error) string {
	if err == nil {
		return ""
	}
	var te *TransportError
	if errors.As(err, &te) {
		return te.Class
	}
	var api *APIError
	if errors.As(err, &api) {
		return fmt.Sprintf("http %d", api.Status)
	}
	return "other"
}

// IsTransient: the failure may clear by itself — transport errors, HTTP 5xx
// (incl. Cloudflare 52x) and 429. 4xx verdicts (401/404/409/422) are not.
func IsTransient(err error) bool {
	var te *TransportError
	if errors.As(err, &te) {
		return te.Transient()
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.Status >= 500 || api.Status == 429
	}
	return false
}
