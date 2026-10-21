package socket

// Agent → panel WebSocket failover (plan 122).
//
// Every dial round starts with exactly what the agent always did: the panel
// host name goes to the Go dialer (family policy + Happy Eyeballs) on the
// primary scheme. Only when that attempt fails after TCP (TLS / WS handshake
// stalls or resets) or DNS fails does the round pin the other resolved IPs one
// by one, and then the fallback scheme the same way. A scheme is never kept
// after a transient failure; failed (scheme, IP) pairs only cool down for
// panelFailoverTTL. A node whose primary scheme works therefore behaves
// exactly as before: one native attempt, one session, no extra traffic.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-gost/x/logsafe"
	"github.com/go-gost/x/panelconn"
	"github.com/gorilla/websocket"
)

const (
	panelProbeTimeout  = 10 * time.Second
	panelKnownIPMaxAge = 24 * time.Hour
	panelHelloPayload  = "flvx-hello"
)

// Tunables are variables only so tests can shorten them.
var (
	panelDialAttemptTimeout = 10 * time.Second // per attempt: DNS + TCP + TLS + upgrade (was HandshakeTimeout)
	panelHelloTimeout       = 20 * time.Second // first pong after the hello ping (the panel pings first at 15 s)
	panelPongStallTimeout   = 45 * time.Second // no pong to our pings (same bound as the panel's pong wait)
	panelHealthyAfter       = 60 * time.Second // a session this old (with pongs) proves the candidate
	panelFailoverTTL        = 5 * time.Minute  // cooldown of a failed candidate = re-probe period while degraded
	panelDialRoundBudget    = 60 * time.Second // one dial round never takes longer than this
	panelTLSConfig          *tls.Config        // tests only (nil = system roots)
	panelLookupNetIP        = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, network, host)
	}
	panelDialNative = func(ctx context.Context, network, address string) (net.Conn, error) {
		return panelconn.Default.DialContext(ctx, network, address)
	}
	panelDialPinned = func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{Timeout: panelDialAttemptTimeout, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", address)
	}
	panelProbeDo = func(req *http.Request) (*http.Response, error) {
		return (&http.Client{Transport: panelconn.Default, Timeout: panelProbeTimeout}).Do(req)
	}
)

// panelTarget is the panel address from config.json split for dialing.
type panelTarget struct {
	hostPort string // host[:port] exactly as it goes into the URL
	host     string // host name or IP literal (no brackets)
	primary  string // "wss" or "ws"
	fallback string
}

func parsePanelTarget(addr string) panelTarget {
	hostPort, explicit := normalizeReporterAddress(addr)
	if hostPort == "" {
		hostPort = strings.TrimSpace(addr)
	}
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	t := panelTarget{hostPort: hostPort, host: host, primary: "wss", fallback: "ws"}
	if mapToWebSocketScheme(explicit) == "ws" {
		t.primary, t.fallback = "ws", "wss"
	}
	return t
}

type dialFailure int

const (
	failTCP        dialFailure = iota // DNS ok, no TCP connection to any address (Go tried them all)
	failDNS                           // name did not resolve
	failAfterTCP                      // TCP up, then TLS / upgrade stalled, reset or timed out
	failHTTPStatus                    // the server answered the upgrade with a status (line works)
	failNoTLS                         // wss against a port that does not speak TLS (deterministic)
)

func classifyDialFailure(err error, resp *http.Response, tcpUp bool) dialFailure {
	var rhe tls.RecordHeaderError
	if errors.As(err, &rhe) {
		return failNoTLS
	}
	if resp != nil {
		return failHTTPStatus
	}
	if tcpUp {
		return failAfterTCP
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return failDNS
	}
	return failTCP
}

// panelFailover keeps what the dial rounds learn. It lives as long as the
// process; nothing is persisted.
type panelFailover struct {
	mu       sync.Mutex
	now      func() time.Time
	cooldown map[string]time.Time                // scheme|hostPort|ip → until
	lastGood map[string]netip.Addr               // scheme|hostPort → ip of the last healthy session
	known    map[string]map[netip.Addr]time.Time // host → ips seen in resolutions / sessions
	noTLS    map[string]bool                     // hostPort → wss is not spoken there
	attempts int                                 // dial attempts since the last healthy session
}

func newPanelFailover() *panelFailover {
	return &panelFailover{
		now:      time.Now,
		cooldown: make(map[string]time.Time),
		lastGood: make(map[string]netip.Addr),
		known:    make(map[string]map[netip.Addr]time.Time),
		noTLS:    make(map[string]bool),
	}
}

func candKey(scheme, hostPort string, ip netip.Addr) string {
	return scheme + "|" + hostPort + "|" + ip.String()
}

// schemes returns the scheme order for this round: primary first unless the
// primary was found not to speak TLS (then it behaves like before: ws first).
func (f *panelFailover) schemes(t panelTarget) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.primary == "wss" && f.noTLS[t.hostPort] {
		return []string{"ws", "wss"}
	}
	return []string{t.primary, t.fallback}
}

// effectivePrimary is the scheme a healthy session should use.
func (f *panelFailover) effectivePrimary(t panelTarget) string {
	return f.schemes(t)[0]
}

func (f *panelFailover) markNoTLS(hostPort string) {
	f.mu.Lock()
	f.noTLS[hostPort] = true
	f.mu.Unlock()
}

func (f *panelFailover) pruneLocked(now time.Time) {
	for k, until := range f.cooldown {
		if !now.Before(until) {
			delete(f.cooldown, k)
		}
	}
	for host, ips := range f.known {
		for ip, at := range ips {
			if now.Sub(at) > panelKnownIPMaxAge {
				delete(ips, ip)
			}
		}
		if len(ips) == 0 {
			delete(f.known, host)
		}
	}
}

// hasCooldown reports whether any address of this scheme failed recently; the
// round then skips the native attempt and orders pinned IPs instead.
func (f *panelFailover) hasCooldown(scheme, hostPort string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	prefix := scheme + "|" + hostPort + "|"
	for k, until := range f.cooldown {
		if strings.HasPrefix(k, prefix) && now.Before(until) {
			return true
		}
	}
	return false
}

func (f *panelFailover) fail(scheme, hostPort string, ip netip.Addr) {
	if !ip.IsValid() {
		return
	}
	f.mu.Lock()
	f.pruneLocked(f.now())
	f.cooldown[candKey(scheme, hostPort, ip)] = f.now().Add(panelFailoverTTL)
	f.mu.Unlock()
}

// healthy records a session that proved itself: its IP becomes the first pinned
// choice and its cooldown is lifted.
func (f *panelFailover) healthy(scheme, hostPort, host string, ip netip.Addr) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts = 0
	if !ip.IsValid() {
		return
	}
	delete(f.cooldown, candKey(scheme, hostPort, ip))
	f.lastGood[scheme+"|"+hostPort] = ip
	f.rememberLocked(host, ip)
}

// originDown: the primary scheme got a 5xx (the panel or its origin is down or
// restarting) and this process already had a healthy session on it. The fallback
// scheme reaches the same origin, so trying it only races the restart and can
// leave a healthy node on ws (seen at the fork.65 panel restart on nodes 21/53
// with the old agent); retry the primary after the backoff instead. Without a
// healthy primary session yet, the round falls back as before.
func (f *panelFailover) originDown(schemeIndex int, scheme, hostPort string, resp *http.Response) bool {
	if schemeIndex != 0 || resp == nil || resp.StatusCode < 500 {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastGood[scheme+"|"+hostPort].IsValid()
}

// clearScheme lifts every cooldown of a scheme (the primary answered a probe).
func (f *panelFailover) clearScheme(scheme, hostPort string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := scheme + "|" + hostPort + "|"
	for k := range f.cooldown {
		if strings.HasPrefix(k, prefix) {
			delete(f.cooldown, k)
		}
	}
}

func (f *panelFailover) rememberLocked(host string, ip netip.Addr) {
	if _, err := netip.ParseAddr(host); err == nil {
		return
	}
	m := f.known[host]
	if m == nil {
		m = make(map[netip.Addr]time.Time)
		f.known[host] = m
	}
	m[ip.Unmap()] = f.now()
}

func (f *panelFailover) countAttempt() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	return f.attempts
}

func (f *panelFailover) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// resolve returns the panel IPs allowed by the family policy, in dial order
// before cooldown / last-good sorting. A failed lookup falls back to IPs seen
// in the last 24 h (the native attempt already failed on the same lookup).
func (f *panelFailover) resolve(ctx context.Context, host, mode string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return filterFamily([]netip.Addr{ip.Unmap()}, mode), nil
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	ips, err := panelLookupNetIP(lctx, panelconn.LookupNetwork(mode), host)
	cancel()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneLocked(f.now())
	if err == nil && len(ips) > 0 {
		for _, ip := range ips {
			f.rememberLocked(host, ip)
		}
	} else {
		ips = nil
		for ip := range f.known[host] {
			ips = append(ips, ip)
		}
		sort.Slice(ips, func(i, j int) bool { return ips[i].Less(ips[j]) })
		if len(ips) == 0 {
			if err == nil {
				err = fmt.Errorf("no address for %s", host)
			}
			return nil, err
		}
	}
	return orderFamilies(filterFamily(dedupe(ips), mode), mode), nil
}

func dedupe(ips []netip.Addr) []netip.Addr {
	seen := make(map[netip.Addr]bool, len(ips))
	out := ips[:0:0]
	for _, ip := range ips {
		ip = ip.Unmap()
		if !seen[ip] {
			seen[ip] = true
			out = append(out, ip)
		}
	}
	return out
}

func filterFamily(ips []netip.Addr, mode string) []netip.Addr {
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		switch {
		case mode == panelconn.IPv4Only && !ip.Is4():
		case mode == panelconn.IPv6Only && ip.Is4():
		default:
			out = append(out, ip)
		}
	}
	return out
}

// orderFamilies: prefer_ipv4 = all IPv4 then IPv6; auto = interleaved starting
// with IPv4 (pinned dialing only happens after something failed, and a broken
// IPv6 path is the most common failure); single-family modes keep DNS order.
func orderFamilies(ips []netip.Addr, mode string) []netip.Addr {
	var v4, v6 []netip.Addr
	for _, ip := range ips {
		if ip.Is4() {
			v4 = append(v4, ip)
		} else {
			v6 = append(v6, ip)
		}
	}
	if mode == panelconn.PreferIPv4 {
		return append(v4, v6...)
	}
	out := make([]netip.Addr, 0, len(ips))
	for i := 0; i < len(v4) || i < len(v6); i++ {
		if i < len(v4) {
			out = append(out, v4[i])
		}
		if i < len(v6) {
			out = append(out, v6[i])
		}
	}
	return out
}

// order sorts pinned IPs: no cooldown first (last healthy IP leading), then the
// cooled-down ones by expiry so the oldest failure is retried first.
func (f *panelFailover) order(scheme, hostPort string, ips []netip.Addr) []netip.Addr {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	good := f.lastGood[scheme+"|"+hostPort]
	until := func(ip netip.Addr) time.Time {
		u := f.cooldown[candKey(scheme, hostPort, ip)]
		if !now.Before(u) {
			return time.Time{}
		}
		return u
	}
	out := append([]netip.Addr(nil), ips...)
	sort.SliceStable(out, func(i, j int) bool {
		ui, uj := until(out[i]), until(out[j])
		if ui.IsZero() != uj.IsZero() {
			return ui.IsZero()
		}
		if !ui.IsZero() && !ui.Equal(uj) {
			return ui.Before(uj)
		}
		return out[i] == good && out[j] != good
	})
	return out
}

// panelDialResult is one established WebSocket with what it is made of.
type panelDialResult struct {
	conn   *websocket.Conn
	url    string
	scheme string
	ip     netip.Addr // remote IP (also for the native attempt)
	native bool       // the host name went to the Go dialer
}

func addrIP(a net.Addr) netip.Addr {
	if a == nil {
		return netip.Addr{}
	}
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

// attempt dials one candidate. ip invalid = native (host name to the policy dialer).
func (w *WebSocketReporter) panelAttempt(ctx context.Context, base websocket.Dialer, rawURL string, ip netip.Addr) (*websocket.Conn, *http.Response, netip.Addr, error) {
	var tcpIP netip.Addr
	d := base
	d.HandshakeTimeout = 0 // the attempt context below bounds the whole dial
	if panelTLSConfig != nil {
		d.TLSClientConfig = panelTLSConfig.Clone()
	}
	d.NetDialContext = func(dctx context.Context, network, address string) (net.Conn, error) {
		var c net.Conn
		var err error
		if ip.IsValid() {
			_, port, perr := net.SplitHostPort(address)
			if perr != nil {
				return nil, perr
			}
			c, err = panelDialPinned(dctx, net.JoinHostPort(ip.String(), port))
		} else {
			c, err = panelDialNative(dctx, network, address)
		}
		if err == nil {
			tcpIP = addrIP(c.RemoteAddr())
		}
		return c, err
	}
	actx, cancel := context.WithTimeout(ctx, panelDialAttemptTimeout)
	defer cancel()
	conn, resp, err := wsDial(actx, &d, rawURL)
	return conn, resp, tcpIP, err
}

// dialPanelRound walks the candidates of one round and returns the first
// WebSocket that completes its handshake. query starts with "/".
func (w *WebSocketReporter) dialPanelRound(ctx context.Context, base websocket.Dialer, t panelTarget, query string) (*panelDialResult, error) {
	f := w.failover
	start := f.now()
	var errs []string
	attempts := 0
	note := func(rawURL string, ip netip.Addr, err error, resp *http.Response) {
		msg := formatWebSocketDialError(err, resp)
		if u, e := url.Parse(rawURL); e == nil {
			msg = logsafe.Text(msg, u.Query().Get("secret"))
		}
		where := sanitizeWebSocketURL(rawURL)
		if ip.IsValid() {
			where += " @" + ip.String()
		}
		errs = append(errs, where+" => "+msg)
	}
	var ips []netip.Addr
	resolved := false
	schemes := f.schemes(t)
	for si, scheme := range schemes {
		if ctx.Err() != nil {
			break
		}
		rawURL := scheme + "://" + t.hostPort + query
		tried := make(map[netip.Addr]bool)
		// Every scheme gets at least its first attempt; further pinned attempts
		// stop once the round budget is spent.
		first := true
		budgetLeft := func() bool {
			if first {
				return true
			}
			return f.now().Sub(start) < panelDialRoundBudget
		}
		pinned := false
		if !f.hasCooldown(scheme, t.hostPort) {
			first = false
			attempts++
			f.countAttempt()
			conn, resp, tcpIP, err := w.panelAttempt(ctx, base, rawURL, netip.Addr{})
			if err == nil {
				return &panelDialResult{conn: conn, url: rawURL, scheme: scheme, ip: tcpIP, native: true}, nil
			}
			note(rawURL, tcpIP, err, resp)
			switch classifyDialFailure(err, resp, tcpIP.IsValid()) {
			case failNoTLS:
				f.markNoTLS(t.hostPort)
			case failHTTPStatus:
				// The server answered: the line works, other IPs would get the same answer.
				if f.originDown(si, scheme, t.hostPort, resp) {
					return nil, fmt.Errorf("连接WebSocket失败（本轮尝试%d次）: %s（面板暂不可用，稍后重试 %s）", attempts, strings.Join(errs, " | "), strings.ToUpper(scheme))
				}
			case failTCP:
				// Every address was already tried by the dialer.
			case failDNS:
				pinned = true
			case failAfterTCP:
				tried[tcpIP] = true
				f.fail(scheme, t.hostPort, tcpIP)
				pinned = true
			}
		} else {
			pinned = true
		}
		if pinned && panelProxyActive(rawURL) {
			// Through an HTTP(S)_PROXY the dialer talks to the proxy, not the panel:
			// pinning panel IPs would bypass it, so only the native attempt runs.
			pinned = false
		}
		if pinned && ctx.Err() == nil {
			if !resolved {
				var err error
				ips, err = f.resolve(ctx, t.host, panelconn.Default.EffectiveMode())
				resolved = true
				if err != nil {
					errs = append(errs, "resolve "+t.host+" => "+logsafe.Error(err))
				}
			}
		pinnedLoop:
			for _, ip := range f.order(scheme, t.hostPort, ips) {
				if tried[ip] {
					continue
				}
				if ctx.Err() != nil || !budgetLeft() {
					break
				}
				first = false
				attempts++
				f.countAttempt()
				conn, resp, tcpIP, err := w.panelAttempt(ctx, base, rawURL, ip)
				if err == nil {
					return &panelDialResult{conn: conn, url: rawURL, scheme: scheme, ip: ip}, nil
				}
				note(rawURL, ip, err, resp)
				switch classifyDialFailure(err, resp, tcpIP.IsValid()) {
				case failNoTLS:
					f.markNoTLS(t.hostPort)
					break pinnedLoop
				case failHTTPStatus:
					if f.originDown(si, scheme, t.hostPort, resp) {
						return nil, fmt.Errorf("连接WebSocket失败（本轮尝试%d次）: %s（面板暂不可用，稍后重试 %s）", attempts, strings.Join(errs, " | "), strings.ToUpper(scheme))
					}
					break pinnedLoop
				default:
					f.fail(scheme, t.hostPort, ip)
				}
			}
		}
		if si < len(schemes)-1 && len(errs) > 0 && ctx.Err() == nil {
			runtimeLogs.Logf("websocket-fallback", scheme+">"+schemes[si+1],
				"⚠️ WebSocket连接失败，准备从 %s 回退到 %s: %s", strings.ToUpper(scheme), strings.ToUpper(schemes[si+1]), errs[len(errs)-1])
		}
	}
	if len(errs) == 0 {
		errs = append(errs, "no candidate")
	}
	return nil, fmt.Errorf("连接WebSocket失败（本轮尝试%d次）: %s", attempts, strings.Join(errs, " | "))
}

// panelProxyActive reports whether the environment routes this URL through a
// proxy (websocket.DefaultDialer uses http.ProxyFromEnvironment).
func panelProxyActive(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	}
	p, err := http.ProxyFromEnvironment(&http.Request{URL: u})
	return err == nil && p != nil
}

// probePrimary asks the primary scheme for /flow/test while a session runs on
// a fallback; true means switching back is worth a reconnect.
func probePrimary(ctx context.Context, t panelTarget, primary string) error {
	scheme := "https"
	if primary == "ws" {
		scheme = "http"
	}
	pctx, cancel := context.WithTimeout(ctx, panelProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, scheme+"://"+t.hostPort+"/flow/test", nil)
	if err != nil {
		return err
	}
	req.Close = true
	req.Header.Set("User-Agent", "FLVX-Panel-Probe/1.0")
	resp, err := panelProbeDo(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// panelSessionInfo describes the live panel session (set under connMutex).
type panelSessionInfo struct {
	target    panelTarget
	scheme    string
	ip        netip.Addr
	startedAt time.Time
}

type sessionReason string

const (
	sessionEnded     sessionReason = ""          // read/write error, EOF or close frame
	sessionNoHello   sessionReason = "hello"     // hello ping never answered
	sessionStalled   sessionReason = "stall"     // our pings stopped getting pongs
	sessionSwitch    sessionReason = "switch"    // primary answered the probe
	sessionRequested sessionReason = "requested" // reconnectPanel (settings change)
	sessionStopped   sessionReason = "stopped"
)

func (r sessionReason) text() string {
	switch r {
	case sessionNoHello:
		return fmt.Sprintf("面板 %.0f 秒内没回应", panelHelloTimeout.Seconds())
	case sessionStalled:
		return "面板回应中断"
	default:
		return "连接很快断开"
	}
}

// sessionOutcome is how a panel session ended.
type sessionOutcome struct {
	session  panelSessionInfo
	reason   sessionReason
	helloOK  bool
	healthy  bool // hello answered and lived ≥ panelHealthyAfter
	duration time.Duration
}

var errPanelNoReply = errors.New("panel did not answer pings")
