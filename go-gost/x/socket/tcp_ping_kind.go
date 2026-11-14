package socket

import (
	"errors"
	"net"
	"syscall"
)

// TcpPing error kinds (plan 255, TcpPingResponse.ErrorKind).
const (
	pingKindRefused = "refused" // reachable, nothing listening (RST / ICMP port unreachable)
	pingKindTimeout = "timeout"
	pingKindUnreach = "unreach" // host / network unreachable, no route
	pingKindDNS     = "dns"
)

// dialErrorKind classifies a failed TCP connect.
func dialErrorKind(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return pingKindRefused
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EADDRNOTAVAIL):
		return pingKindUnreach
	case errors.As(err, &dnsErr):
		return pingKindDNS
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return pingKindTimeout
	}
	return ""
}
