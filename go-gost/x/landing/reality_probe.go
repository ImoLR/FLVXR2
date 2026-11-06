package landing

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Plan 227: Reality 伪装域名 测速 from the landing machine itself (the panel
// sends RealityProbe) and the fastest-IP pin of guarded Reality dests (see
// reality_pin.go). RealityVersion is reported as landing_reality:
//   - 1 = the RealityProbe command + Status.RealityPins (plan 227);
//   - 2 = quiet sockets (plan 229): every probe / ping / pin socket is closed
//     with SO_LINGER 0 (RST, no TIME_WAIT on this machine), at most 2 Reality
//     sockets are open at once on the machine, RealityProbeRequest.Concurrency
//     and RealityProbeResponse.Stats.
const RealityVersion = 2

const (
	realityProbeMaxCandidates = 64
	realityProbeConcurrency   = 2 // candidates at once (default and max); third-party sites
	realityPingConcurrency    = 2 // TCP pings of one host at once
	realitySocketCap          = 2 // Reality sockets (probe + pin) open at once on the machine
	realityMaxIPs             = 8 // public IPv4 addresses tried per host
	realityConnectTimeout     = time.Second
	realityProbeDefaultTO     = 3 * time.Second
	realityProbeMaxTO         = 10 * time.Second
)

// realitySockets is the machine-wide cap shared by the probe and the pins.
var realitySockets = make(chan struct{}, realitySocketCap)

// RealityProbeRequest is the RealityProbe command payload.
type RealityProbeRequest struct {
	Candidates []string `json:"candidates"` // host or host:port (default 443)
	TimeoutMs  int      `json:"timeout_ms"` // TLS handshake budget per candidate
	// Concurrency = candidates probed at once: 1 = one domain at a time (the
	// panel's re-sample passes), 0 / ≥ 2 = realityProbeConcurrency. Agents
	// before landing_reality 2 ignore it (≤ 4 at once).
	Concurrency int `json:"concurrency,omitempty"`
}

// RealityProbeIP is one resolved address of a candidate.
type RealityProbeIP struct {
	IP    string `json:"ip"`
	TCPMs int    `json:"tcp_ms"` // TCP connect time; -1 = failed
}

// RealityProbeResult is one candidate's result (contract reality-pick-v1 §1).
type RealityProbeResult struct {
	Target         string           `json:"target"`
	Feasible       bool             `json:"feasible"`
	Reason         string           `json:"reason,omitempty"`
	LatencyMs      int              `json:"latency_ms"` // TCP connect + TLS handshake to best_ip
	BestIP         string           `json:"best_ip,omitempty"`
	IPs            []RealityProbeIP `json:"ips"`
	TLS13          bool             `json:"tls13"`
	X25519         bool             `json:"x25519"`
	H2             bool             `json:"h2"`
	CertValid      bool             `json:"cert_valid"`
	ServerNames    []string         `json:"server_names"`
	CertChainBytes int              `json:"cert_chain_bytes"`
}

// RealityProbeResponse is the RealityProbe command response.
type RealityProbeResponse struct {
	Results []RealityProbeResult `json:"results"`
	Stats   *RealityProbeStats   `json:"stats,omitempty"` // landing_reality ≥ 2
}

// RealityProbeStats is what one probe cost on the machine.
type RealityProbeStats struct {
	Sockets    int   `json:"sockets"`     // TCP connects attempted (pings + TLS)
	MaxOpen    int   `json:"max_open"`    // most of this probe's sockets open at once
	DurationMs int64 `json:"duration_ms"` // whole probe
	Linger0    bool  `json:"linger0"`     // sockets closed with RST (no TIME_WAIT)
}

// realityNet is what the probe and the pin use from the network; tests swap it.
type realityNet struct {
	lookup func(ctx context.Context, host string) ([]net.IP, error)
	dial   func(ctx context.Context, addr string) (net.Conn, error)
	roots  *x509.CertPool // nil = system roots
	public func(ip net.IP) bool
	// sockets caps the Reality sockets open at once (nil = no cap, tests).
	sockets chan struct{}
	// count, when set, records this probe's sockets.
	count *socketCount
}

func defaultRealityNet() realityNet {
	d := &net.Dialer{}
	return realityNet{
		lookup: func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip4", host)
		},
		dial:    func(ctx context.Context, addr string) (net.Conn, error) { return d.DialContext(ctx, "tcp", addr) },
		public:  publicIP,
		sockets: realitySockets,
	}
}

type socketCount struct {
	mu                sync.Mutex
	total, open, peak int
}

func (c *socketCount) add(d int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.total++
	}
	c.open += d
	if c.open > c.peak {
		c.peak = c.open
	}
}

// quietClose closes a probe socket with SO_LINGER 0: the kernel sends RST and
// keeps no TIME_WAIT (a normal close by us, the side that closes first, would
// keep each socket 60 s in TIME_WAIT). Only our own measuring sockets get
// this; user connections are never touched.
func quietClose(c net.Conn) {
	if c == nil {
		return
	}
	setLinger0(c)
	_ = c.Close()
}

// realityQuietClose = SO_LINGER 0 on close; only the manual measuring tool
// turns it off (before / after comparison).
var realityQuietClose = true

func setLinger0(c net.Conn) {
	if !realityQuietClose {
		return
	}
	if l, ok := c.(interface{ SetLinger(int) error }); ok {
		_ = l.SetLinger(0)
	}
}

// connect takes a socket slot (machine-wide cap), then dials with timeout.
// The wait for a slot is not part of the budget or the measured time: start
// is taken once the slot is ours. done closes the socket with RST and gives
// the slot back (no-op after an error); the caller must call it.
func (rn realityNet) connect(ctx context.Context, addr string, timeout time.Duration) (conn net.Conn, start time.Time, done func(), err error) {
	if rn.sockets != nil {
		select {
		case rn.sockets <- struct{}{}:
		case <-ctx.Done():
			return nil, time.Time{}, func() {}, ctx.Err()
		}
	}
	release := func() {
		if rn.sockets != nil {
			<-rn.sockets
		}
	}
	rn.count.add(1)
	start = time.Now()
	dctx, cancel := context.WithTimeout(ctx, timeout)
	conn, err = rn.dial(dctx, addr)
	cancel()
	if err != nil {
		rn.count.add(-1)
		release()
		return nil, start, func() {}, err
	}
	var once sync.Once
	return conn, start, func() {
		once.Do(func() {
			quietClose(conn)
			rn.count.add(-1)
			release()
		})
	}, nil
}

var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range blockedDestinations {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

// publicIP rejects loopback/private/CGNAT/link-local/multicast/reserved
// addresses (the Core's own safety list): a dest must never be pointed at the
// machine itself or its network (DNS rebinding).
func publicIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// splitRealityTarget accepts host or host:port; IP literals are refused (the
// SNI must be a domain).
func splitRealityTarget(target string) (string, int, error) {
	target = strings.TrimSpace(strings.ToLower(target))
	host, portText := target, "443"
	if h, p, err := net.SplitHostPort(target); err == nil {
		host, portText = h, p
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("端口无效")
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /\\@\r\n\t") {
		return "", 0, errors.New("域名无效")
	}
	if net.ParseIP(host) != nil {
		return "", 0, errors.New("需要域名，不能是 IP")
	}
	if !strings.Contains(host, ".") {
		return "", 0, errors.New("域名无效")
	}
	return host, port, nil
}

// resolvePublic returns up to realityMaxIPs distinct public IPv4 addresses of
// host in DNS order.
func (rn realityNet) resolvePublic(ctx context.Context, host string) ([]string, error) {
	ips, err := rn.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	private := false
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		if !rn.public(v4) {
			private = true
			continue
		}
		s := v4.String()
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) == realityMaxIPs {
			break
		}
	}
	if len(out) == 0 {
		if private {
			return nil, errors.New("只解析到内网 / 保留地址")
		}
		return nil, errors.New("没有 IPv4 地址")
	}
	return out, nil
}

// connectAll measures one TCP connect per IP (≤ 8, 1 s each), ≤ 2 at once
// and within the machine-wide socket cap. The socket is closed right away
// with RST: no bytes are sent, no TIME_WAIT is left.
func (rn realityNet) connectAll(ctx context.Context, ips []string, port int) []RealityProbeIP {
	out := make([]RealityProbeIP, len(ips))
	sem := make(chan struct{}, realityPingConcurrency)
	var wg sync.WaitGroup
	for i, ip := range ips {
		out[i] = RealityProbeIP{IP: ip, TCPMs: -1}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return out
		}
		wg.Add(1)
		go func(i int, ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i].TCPMs = rn.ping(ctx, ip, port)
		}(i, ip)
	}
	wg.Wait()
	return out
}

// ping is one TCP connect (1 s from when the socket slot is ours); -1 =
// failed.
func (rn realityNet) ping(ctx context.Context, ip string, port int) int {
	_, start, done, err := rn.connect(ctx, net.JoinHostPort(ip, strconv.Itoa(port)), realityConnectTimeout)
	if err != nil {
		return -1
	}
	ms := msCeil(time.Since(start))
	done()
	return ms
}

// msCeil rounds up so a sub-millisecond connect still reads as 1 ms (0 would
// look like "not measured").
func msCeil(d time.Duration) int {
	ms := int((d + time.Millisecond - 1) / time.Millisecond)
	if ms < 1 {
		ms = 1
	}
	return ms
}

func fastestIP(ips []RealityProbeIP) (string, int) {
	best, ms := "", -1
	for _, ip := range ips {
		if ip.TCPMs >= 0 && (ms < 0 || ip.TCPMs < ms) {
			best, ms = ip.IP, ip.TCPMs
		}
	}
	return best, ms
}

// usableSANs keeps the certificate's non-wildcard DNS names, host first.
func usableSANs(host string, names []string) []string {
	out := []string{host}
	seen := map[string]bool{host: true}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || strings.HasPrefix(n, "*.") || seen[n] || strings.ContainsAny(n, " /:") {
			continue
		}
		seen[n] = true
		out = append(out, n)
		if len(out) == 16 {
			break
		}
	}
	return out
}

// probeOne tests one candidate like 3x-ui's reality_scan: resolve, TCP to
// every IP, then one TLS handshake (SNI = host) to the fastest IP. Feasible =
// TLS 1.3 + X25519 + h2 + a certificate valid for the host.
func (rn realityNet) probeOne(ctx context.Context, target string, timeout time.Duration) RealityProbeResult {
	host, port, err := splitRealityTarget(target)
	res := RealityProbeResult{Target: strings.TrimSpace(target), IPs: []RealityProbeIP{}, ServerNames: []string{}}
	if err != nil {
		res.Reason = err.Error()
		return res
	}
	res.Target = net.JoinHostPort(host, strconv.Itoa(port))
	lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	ips, err := rn.resolvePublic(lctx, host)
	cancel()
	if err != nil {
		res.Reason = "解析失败：" + shortErr(err)
		return res
	}
	res.IPs = rn.connectAll(ctx, ips, port)
	best, _ := fastestIP(res.IPs)
	if best == "" {
		res.Reason = "所有 IP 都连不上"
		return res
	}
	res.BestIP = best
	conn, start, done, err := rn.connect(ctx, net.JoinHostPort(best, strconv.Itoa(port)), timeout)
	if err != nil {
		res.Reason = "连接失败：" + shortErr(err)
		return res
	}
	// Budget = connect + handshake, counted from the slot (like before).
	hctx, cancel := context.WithDeadline(ctx, start.Add(timeout))
	defer cancel()
	_ = conn.SetDeadline(start.Add(timeout))
	tc := tls.Client(conn, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // verified below against the roots, with and without the name
		NextProtos:         []string{"h2", "http/1.1"},
		CurvePreferences:   []tls.CurveID{tls.X25519},
		MinVersion:         tls.VersionTLS12,
	})
	// Close: close_notify after a finished handshake (like a browser), then
	// RST instead of FIN (SO_LINGER 0) → no TIME_WAIT here.
	defer func() {
		setLinger0(conn)
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		_ = tc.Close()
		done()
	}()
	if err := tc.HandshakeContext(hctx); err != nil {
		res.Reason = "TLS 握手失败：" + shortErr(err)
		return res
	}
	res.LatencyMs = msCeil(time.Since(start))
	st := tc.ConnectionState()
	res.TLS13 = st.Version == tls.VersionTLS13
	res.H2 = st.NegotiatedProtocol == "h2"
	// Only X25519 is offered, so a finished handshake used it (TLS 1.3) or
	// an ECDHE suite over it (1.2, already infeasible).
	res.X25519 = res.TLS13
	if len(st.PeerCertificates) > 0 {
		leaf := st.PeerCertificates[0]
		for _, c := range st.PeerCertificates {
			res.CertChainBytes += len(c.Raw)
		}
		res.ServerNames = usableSANs(host, leaf.DNSNames)
		opts := x509.VerifyOptions{Roots: rn.roots, Intermediates: x509.NewCertPool(), DNSName: host}
		for _, c := range st.PeerCertificates[1:] {
			opts.Intermediates.AddCert(c)
		}
		if _, err := leaf.Verify(opts); err == nil {
			res.CertValid = true
		} else {
			res.Reason = "证书不可信：" + shortErr(err)
		}
	} else {
		res.Reason = "没有证书"
	}
	if !res.CertValid {
		res.ServerNames = []string{}
	}
	res.Feasible = res.TLS13 && res.H2 && res.X25519 && res.CertValid
	if !res.Feasible && res.Reason == "" {
		switch {
		case !res.TLS13:
			res.Reason = "不支持 TLS 1.3"
		case !res.H2:
			res.Reason = "不支持 HTTP/2 (h2)"
		}
	}
	return res
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// ProbeReality probes the candidates, ≤ 2 at once (1 when asked), and returns
// them feasible first, then by latency (failures last, in request order).
func ProbeReality(ctx context.Context, req RealityProbeRequest) (RealityProbeResponse, error) {
	return defaultRealityNet().probe(ctx, req)
}

func (rn realityNet) probe(ctx context.Context, req RealityProbeRequest) (RealityProbeResponse, error) {
	var targets []string
	seen := map[string]bool{}
	for _, c := range req.Candidates {
		key := strings.ToLower(strings.TrimSpace(c))
		if host, port, err := splitRealityTarget(c); err == nil {
			key = net.JoinHostPort(host, strconv.Itoa(port))
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, c)
	}
	if len(targets) == 0 {
		return RealityProbeResponse{Results: []RealityProbeResult{}}, errors.New("没有候选域名")
	}
	if len(targets) > realityProbeMaxCandidates {
		return RealityProbeResponse{}, fmt.Errorf("候选域名最多 %d 个", realityProbeMaxCandidates)
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = realityProbeDefaultTO
	}
	if timeout > realityProbeMaxTO {
		timeout = realityProbeMaxTO
	}
	workers := realityProbeConcurrency
	if req.Concurrency == 1 {
		workers = 1
	}
	if rn.count == nil {
		rn.count = &socketCount{}
	}
	began := time.Now()
	results := make([]RealityProbeResult, len(targets))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] = RealityProbeResult{Target: t, Reason: "已取消", IPs: []RealityProbeIP{}, ServerNames: []string{}}
				return
			}
			defer func() { <-sem }()
			results[i] = rn.probeOne(ctx, t, timeout)
		}(i, t)
	}
	wg.Wait()
	sortRealityResults(results)
	rn.count.mu.Lock()
	stats := &RealityProbeStats{Sockets: rn.count.total, MaxOpen: rn.count.peak, DurationMs: time.Since(began).Milliseconds(), Linger0: realityQuietClose}
	rn.count.mu.Unlock()
	return RealityProbeResponse{Results: results, Stats: stats}, nil
}

func sortRealityResults(results []RealityProbeResult) {
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.Feasible != b.Feasible {
			return a.Feasible
		}
		if (a.LatencyMs > 0) != (b.LatencyMs > 0) {
			return a.LatencyMs > 0
		}
		return a.LatencyMs < b.LatencyMs
	})
}
