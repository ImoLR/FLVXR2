package socket

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDedupeLoggerKeepsFirstStateChangeAndPeriodicSummary(t *testing.T) {
	now := time.Unix(100, 0)
	var messages []string
	logger := newDedupeLogger(time.Minute, func(message string) {
		messages = append(messages, message)
	})
	logger.now = func() time.Time { return now }

	logger.Logf("dns:example", "timeout", "lookup failed: timeout")
	logger.Logf("dns:example", "timeout", "lookup failed: timeout")
	if len(messages) != 1 {
		t.Fatalf("first error should be emitted and duplicate suppressed, got %v", messages)
	}

	logger.Logf("dns:example", "refused", "lookup failed: refused")
	if len(messages) != 2 || messages[1] != "lookup failed: refused" {
		t.Fatalf("state change should be emitted immediately, got %v", messages)
	}

	logger.Logf("dns:example", "refused", "lookup failed: refused")
	now = now.Add(time.Minute)
	logger.Logf("dns:example", "refused", "lookup failed: refused")
	if len(messages) != 3 || messages[2] != "lookup failed: refused（已抑制 1 条相同日志）" {
		t.Fatalf("expected periodic suppression summary, got %v", messages)
	}
}

func TestNetworkErrorFingerprintIgnoresEphemeralDNSPort(t *testing.T) {
	first := &net.DNSError{Err: "read udp 127.0.0.1:1111->127.0.0.53:53: i/o timeout", IsTimeout: true}
	second := &net.DNSError{Err: "read udp 127.0.0.1:2222->127.0.0.53:53: i/o timeout", IsTimeout: true}
	if got, want := networkErrorFingerprint("probe", first), networkErrorFingerprint("probe", second); got != want {
		t.Fatalf("ephemeral source ports should share a fingerprint: %q != %q", got, want)
	}
}

func TestDedupeLoggerResetMakesRegressionVisible(t *testing.T) {
	var messages []string
	logger := newDedupeLogger(time.Hour, func(message string) {
		messages = append(messages, message)
	})
	logger.Logf("websocket", "down", "connection failed")
	logger.Logf("websocket", "down", "connection failed")
	logger.Reset("websocket")
	logger.Logf("websocket", "down", "connection failed")
	if len(messages) != 2 {
		t.Fatalf("failure after recovery should be visible, got %v", messages)
	}
}

func TestDedupeLoggerRecoveryIsImmediateAndRoutineSuccessIsSilent(t *testing.T) {
	var messages []string
	logger := newDedupeLogger(time.Hour, func(message string) {
		messages = append(messages, message)
	})

	logger.Recoverf("probe", "probe recovered")
	if len(messages) != 0 {
		t.Fatalf("routine success must stay silent, got %v", messages)
	}

	logger.Logf("probe", "timeout", "probe failed")
	logger.Logf("probe", "timeout", "probe failed")
	logger.Recoverf("probe", "probe recovered")
	if got, want := strings.Join(messages, "|"), "probe failed|probe recovered"; got != want {
		t.Fatalf("expected first failure and one recovery, got %q", got)
	}

	logger.Recoverf("probe", "probe recovered")
	logger.Logf("probe", "timeout", "probe failed again")
	if got, want := strings.Join(messages, "|"), "probe failed|probe recovered|probe failed again"; got != want {
		t.Fatalf("repeated success should stay silent and regression should be visible, got %q", got)
	}
}

func TestHardenServiceUnitLoggingIsIdempotent(t *testing.T) {
	input := `[Unit]
Description=flvxx Proxy Service

[Service]
ExecStart=/etc/flvxx/flvxx
StandardOutput=null
StandardError=null

[Install]
WantedBy=multi-user.target
`
	got := hardenServiceUnitLogging(input)
	for _, want := range []string{
		"StandardOutput=journal",
		"StandardError=journal",
		"LogRateLimitIntervalSec=30s",
		"LogRateLimitBurst=200",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in hardened unit:\n%s", want, got)
		}
	}
	if second := hardenServiceUnitLogging(got); second != got {
		t.Fatalf("hardening must be idempotent:\nfirst=%s\nsecond=%s", got, second)
	}
}

func TestTCPPingHostSupportsDomainAndIPFamilies(t *testing.T) {
	t.Run("domain", func(t *testing.T) {
		addrs, err := net.LookupHost("localhost")
		if err != nil || len(addrs) == 0 {
			t.Fatalf("resolve localhost: addrs=%v err=%v", addrs, err)
		}
		probeTCPAddress(t, "localhost", addrs[0])
	})

	t.Run("ipv4", func(t *testing.T) {
		probeTCPAddress(t, "127.0.0.1", "127.0.0.1")
	})

	t.Run("ipv6", func(t *testing.T) {
		probeTCPAddress(t, "::1", "::1")
	})
}

func TestTCPPingHostConfiguredDomain(t *testing.T) {
	target := os.Getenv("FLVXR2_TCP_PROBE_TARGET")
	if target == "" {
		t.Skip("set FLVXR2_TCP_PROBE_TARGET for an isolated resolver integration test")
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatalf("parse configured target %q: %v", target, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse configured port %q: %v", portText, err)
	}

	average, loss, err := tcpPingHost(host, port, 2, 1000)
	if err != nil {
		t.Fatalf("probe configured domain %s: %v", target, err)
	}
	if average < 0 || loss != 0 {
		t.Fatalf("unexpected configured-domain result: average=%f loss=%f", average, loss)
	}
}

func probeTCPAddress(t *testing.T, probeHost, listenHost string) {
	t.Helper()
	network := "tcp4"
	if ip := net.ParseIP(listenHost); ip != nil && ip.To4() == nil {
		network = "tcp6"
	}

	listener, err := net.Listen(network, net.JoinHostPort(listenHost, "0"))
	if err != nil {
		if network == "tcp6" {
			t.Skipf("IPv6 loopback is unavailable: %v", err)
		}
		t.Fatalf("listen on %s: %v", listenHost, err)
	}
	defer listener.Close()

	const attempts = 2
	accepted := make(chan error, 1)
	go func() {
		for i := 0; i < attempts; i++ {
			conn, err := listener.Accept()
			if err != nil {
				accepted <- err
				return
			}
			_ = conn.Close()
		}
		accepted <- nil
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	average, loss, err := tcpPingHost(probeHost, port, attempts, 1000)
	if err != nil {
		t.Fatalf("probe %s: %v", probeHost, err)
	}
	if average < 0 || loss != 0 {
		t.Fatalf("unexpected probe result: average=%f loss=%f", average, loss)
	}
	if err := <-accepted; err != nil {
		t.Fatalf("accept probe connection: %v", err)
	}
}
