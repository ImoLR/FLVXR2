package landingsub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/linechain"
)

// Fetch limits (plan 177): one request on click, nothing stored.
const (
	FetchTimeout  = 10 * time.Second
	MaxBodyBytes  = 2 << 20
	MaxRedirects  = 3
	MaxURLLength  = 2048
	FetchUA       = "ClashMeta/1.18 (FLVX)"
	resolveBudget = 4 * time.Second
)

// Fetcher downloads a subscription with an SSRF guard: http/https only,
// every hop (incl. redirects) dials a pinned IP that was checked after DNS
// (public addresses only, plus the caller's Forbidden list), no proxy from
// the environment, no cookies, a hard timeout and body cap.
type Fetcher struct {
	Resolver  linechain.Resolver // nil: net.DefaultResolver
	Forbidden linechain.Forbidden
	// DialContext dials the already checked ip:port (tests inject one).
	DialContext func(ctx context.Context, network, address string) (net.Conn, error)
	Timeout     time.Duration
	// AllowAddr overrides the public-address check (test harnesses only).
	AllowAddr func(netip.Addr) bool
}

// errFetch is the generic message; internal details are never echoed.
func errFetch(msg string) error { return errors.New(msg) }

// CheckURL validates a subscription URL before any network access.
func CheckURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errFetch("请填写订阅地址")
	}
	if len(raw) > MaxURLLength {
		return nil, errFetch("订阅地址过长")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errFetch("订阅地址无效，请填写 http:// 或 https:// 开头的完整地址")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, errFetch("订阅地址只支持 http:// 或 https://")
	}
	if u.User != nil {
		return nil, errFetch("订阅地址不能包含用户名或密码")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return nil, errFetch("订阅地址端口无效")
		}
	}
	return u, nil
}

func (f *Fetcher) allowed(a netip.Addr) (string, bool) {
	a = a.Unmap()
	if f.AllowAddr != nil && f.AllowAddr(a) {
		return "", true
	}
	if linechain.BlockedAddr(a) {
		return "内网或保留地址", false
	}
	if f.Forbidden != nil {
		if why, bad := f.Forbidden(a); bad {
			return why, false
		}
	}
	return "", true
}

// pick resolves host and returns one checked address; any blocked answer
// rejects the host (a name that also points inside is not fetched).
func (f *Fetcher) pick(ctx context.Context, host string) (netip.Addr, error) {
	host = strings.Trim(host, "[]")
	if a, err := netip.ParseAddr(host); err == nil {
		if why, ok := f.allowed(a); !ok {
			return netip.Addr{}, errFetch("订阅地址指向" + why + "，不能拉取")
		}
		return a.Unmap(), nil
	}
	resolver := f.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	child, cancel := context.WithTimeout(ctx, resolveBudget)
	defer cancel()
	addrs, err := resolver.LookupNetIP(child, "ip", host)
	if err != nil || len(addrs) == 0 {
		return netip.Addr{}, errFetch("无法解析订阅地址的域名")
	}
	var pick netip.Addr
	for _, a := range addrs {
		if why, ok := f.allowed(a); !ok {
			return netip.Addr{}, errFetch("订阅地址指向" + why + "，不能拉取")
		}
		if !pick.IsValid() || (a.Unmap().Is4() && !pick.Is4()) {
			pick = a.Unmap()
		}
	}
	return pick, nil
}

// Fetch downloads the subscription body (at most MaxBodyBytes).
func (f *Fetcher) Fetch(ctx context.Context, raw string) ([]byte, error) {
	u, err := CheckURL(raw)
	if err != nil {
		return nil, err
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = FetchTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dial := f.DialContext
	if dial == nil {
		d := &net.Dialer{Timeout: 5 * time.Second}
		dial = d.DialContext
	}
	transport := &http.Transport{
		Proxy: nil,
		// Every connection (first request and each redirect) re-resolves and
		// re-checks the host, then dials exactly the checked IP: DNS
		// rebinding between check and dial cannot reach an internal address.
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ip, err := f.pick(ctx, host)
			if err != nil {
				return nil, err
			}
			return dial(ctx, network, net.JoinHostPort(ip.String(), port))
		},
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           1,
		DisableKeepAlives:      true,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  timeout,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > MaxRedirects {
				return errFetch("订阅地址跳转次数过多")
			}
			if _, err := CheckURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errFetch("订阅地址无效")
	}
	req.Header.Set("User-Agent", FetchUA)
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, friendlyFetchError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("订阅地址返回 HTTP %d，请检查订阅地址是否有效", resp.StatusCode)
	}
	if resp.ContentLength > MaxBodyBytes {
		return nil, errFetch("订阅内容超过 2 MB，无法导入")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, friendlyFetchError(err)
	}
	if len(body) > MaxBodyBytes {
		return nil, errFetch("订阅内容超过 2 MB，无法导入")
	}
	return body, nil
}

// friendlyFetchError keeps our own guard messages and hides everything else
// (no internal addresses or stack details reach the user).
func friendlyFetchError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		err = op.Err
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "订阅") || strings.HasPrefix(msg, "无法解析订阅") {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
		return errFetch("拉取订阅超时（10 秒），请稍后重试")
	}
	return errFetch("拉取订阅失败，请检查订阅地址能否访问")
}
