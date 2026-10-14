// Package leikwan implements the documented v1 API. It never manages client
// internals, and only sends explicit, typed fields accepted by the platform.
package leikwan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const BaseURL = "https://v3.leikwanhost.com"
const maxResponseBytes = 2 << 20

var sharedTransport = &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext, MaxIdleConns: 8, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 7 * time.Second}

type Target struct {
	Source string `json:"source,omitempty"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
}
type RouteRef struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}
type Forward struct {
	ID             int64    `json:"id"`
	ServiceID      int64    `json:"service_id"`
	Route          RouteRef `json:"route"`
	ListenPort     int      `json:"listen_port"`
	Transport      string   `json:"transport"`
	NativeOutbound bool     `json:"native_outbound"`
	Target         Target   `json:"target"`
	State          string   `json:"state"`
	Revision       int64    `json:"revision"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}
type Availability struct {
	Available         bool    `json:"available"`
	HealthyNodeCount  int     `json:"healthy_node_count"`
	UnavailableReason *string `json:"unavailable_reason"`
}
type Route struct {
	ID          int64                   `json:"id"`
	Label       string                  `json:"label"`
	Description string                  `json:"description"`
	Badge       string                  `json:"badge"`
	Transports  map[string]Availability `json:"transports"`
}
type Options struct {
	Transports         []string `json:"transports"`
	ForwardLimit       int      `json:"forward_limit"`
	ActiveForwardCount int      `json:"active_forward_count"`
	Capabilities       struct {
		AllowManage bool `json:"allow_manage_forwards"`
		AllowDelete bool `json:"allow_delete_forwards"`
	} `json:"capabilities"`
	Routes []Route `json:"routes"`
}
type Overview struct {
	Options  Options   `json:"options"`
	Forwards []Forward `json:"forwards"`
}
type CreateRequest struct {
	RouteID    int64  `json:"route_id"`
	ListenPort *int   `json:"listen_port"`
	Transport  string `json:"transport"`
	Target     Target `json:"target"`
}
type APIError struct {
	Status     int
	Code       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Status == 429 {
		return fmt.Sprintf("利群请求限速，请在 %d 秒后重试", int(e.RetryAfter.Seconds())+1)
	}
	return fmt.Sprintf("利群请求失败 (%d: %s)", e.Status, e.Code)
}

type accountState struct{ nextRead, timeWrite, blockedUntil time.Time }
type cacheEntry struct {
	value      Overview
	err        error
	until      time.Time
	done       chan struct{}
	generation uint64
}
type Client struct {
	base                           string
	http                           *http.Client
	mu                             sync.Mutex
	rates                          map[string]*accountState
	cache                          map[string]*cacheEntry
	readSpacing, writeSpacing, ttl time.Duration
}

func NewClient(base string, transport http.RoundTripper) *Client {
	if base == "" {
		base = BaseURL
	}
	if transport == nil {
		transport = sharedTransport
	}
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, rates: map[string]*accountState{}, cache: map[string]*cacheEntry{}, readSpacing: 600 * time.Millisecond, writeSpacing: 2200 * time.Millisecond, ttl: 15 * time.Second}
}
func tokenKey(token string) string {
	v := sha256.Sum256([]byte(token))
	return hex.EncodeToString(v[:])
}
func (c *Client) wait(ctx context.Context, token string, write bool) error {
	key := tokenKey(token)
	for {
		c.mu.Lock()
		s := c.rates[key]
		if s == nil {
			if len(c.rates) >= 128 {
				c.mu.Unlock()
				return errors.New("利群账号请求状态已达上限")
			}
			s = &accountState{}
			c.rates[key] = s
		}
		now := time.Now()
		next := s.nextRead
		if write && s.timeWrite.After(next) {
			next = s.timeWrite
		}
		if s.blockedUntil.After(next) {
			next = s.blockedUntil
		}
		if !next.After(now) {
			s.nextRead = now.Add(c.readSpacing)
			if write {
				s.timeWrite = now.Add(c.writeSpacing)
			}
			c.mu.Unlock()
			return nil
		}
		delay := time.Until(next)
		c.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *Client) request(ctx context.Context, token, method, path, etag, key string, body interface{}, out interface{}) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.wait(ctx, token, method != http.MethodGet); err != nil {
		return "", err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		if len(raw) > 16<<10 {
			return "", errors.New("利群请求过大")
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return "", errors.New("利群请求配置错误")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("利群服务请求失败，请稍后重试")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", errors.New("读取利群响应失败")
	}
	if len(raw) > maxResponseBytes {
		return "", errors.New("利群响应超过大小限制")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &payload)
		code := payload.Error.Code
		if code == "" {
			code = "request_failed"
		} // Never echo arbitrary platform messages (which could contain a token).
		if strings.Contains(code, token) || len(code) > 80 || strings.IndexFunc(code, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r == '_') }) >= 0 {
			code = "request_failed"
		}
		apiErr := &APIError{Status: resp.StatusCode, Code: code}
		if resp.StatusCode == 429 {
			delay := time.Minute
			if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds >= 0 {
				delay = time.Duration(seconds) * time.Second
			} else if date, e := http.ParseTime(resp.Header.Get("Retry-After")); e == nil {
				delay = time.Until(date)
				if delay < 0 {
					delay = 0
				}
			}
			apiErr.RetryAfter = delay
			c.mu.Lock()
			s := c.rates[tokenKey(token)]
			if s != nil {
				s.blockedUntil = time.Now().Add(delay)
			}
			c.mu.Unlock()
		}
		return "", apiErr
	}
	if out != nil && resp.StatusCode != 204 {
		if err := json.Unmarshal(raw, out); err != nil {
			return "", errors.New("利群响应格式错误")
		}
	}
	return resp.Header.Get("ETag"), nil
}
func (c *Client) Overview(ctx context.Context, token string, sid int64) (Overview, error) {
	key := tokenKey(token) + ":" + strconv.FormatInt(sid, 10)
	for {
		c.mu.Lock()
		e := c.cache[key]
		if e == nil {
			if len(c.cache) >= 128 {
				c.mu.Unlock()
				return Overview{}, errors.New("利群服务缓存已达上限")
			}
			e = &cacheEntry{}
			c.cache[key] = e
		}
		if e.done != nil {
			done := e.done
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return Overview{}, ctx.Err()
			case <-done:
			}
			continue
		}
		if time.Now().Before(e.until) {
			value, err := e.value, e.err
			c.mu.Unlock()
			return value, err
		}
		e.done = make(chan struct{})
		generation := e.generation
		c.mu.Unlock()
		value, err := c.fetchOverview(ctx, token, sid)
		c.mu.Lock()
		e.value, e.err = value, err
		ttl := c.ttl
		if err != nil {
			ttl = 3 * time.Second
		}
		if e.generation == generation {
			e.until = time.Now().Add(ttl)
		}
		close(e.done)
		e.done = nil
		c.mu.Unlock()
		return value, err
	}
}
func (c *Client) fetchOverview(ctx context.Context, token string, sid int64) (Overview, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out Overview
	prefix := fmt.Sprintf("/api/v1/user/services/%d", sid)
	if _, err := c.request(ctx, token, "GET", prefix+"/forward-options", "", "", nil, &out.Options); err != nil {
		return out, err
	}
	var list struct {
		Forwards []Forward `json:"forwards"`
	}
	_, err := c.request(ctx, token, "GET", prefix+"/forwards", "", "", nil, &list)
	out.Forwards = list.Forwards
	if out.Forwards == nil {
		out.Forwards = []Forward{}
	}
	return out, err
}
func (c *Client) Invalidate(token string, sid int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.cache[tokenKey(token)+":"+strconv.FormatInt(sid, 10)]; e != nil {
		e.until = time.Time{}
		e.generation++
	}
}
func (c *Client) GetForward(ctx context.Context, token string, id int64) (Forward, string, error) {
	var out struct {
		Forward Forward `json:"forward"`
	}
	etag, err := c.request(ctx, token, "GET", fmt.Sprintf("/api/v1/user/forwards/%d", id), "", "", nil, &out)
	return out.Forward, etag, err
}
func (c *Client) CreateForward(ctx context.Context, token string, sid int64, key string, body CreateRequest) (Forward, string, error) {
	var out struct {
		Forward Forward `json:"forward"`
	}
	etag, err := c.request(ctx, token, "POST", fmt.Sprintf("/api/v1/user/services/%d/forwards", sid), "", key, body, &out)
	c.Invalidate(token, sid)
	return out.Forward, etag, err
}
func (c *Client) PatchTarget(ctx context.Context, token string, id int64, etag string, target Target) (Forward, string, error) {
	var out struct {
		Forward Forward `json:"forward"`
	}
	newTag, err := c.request(ctx, token, "PATCH", fmt.Sprintf("/api/v1/user/forwards/%d", id), etag, "", struct {
		Target Target `json:"target"`
	}{target}, &out)
	return out.Forward, newTag, err
}
func (c *Client) DeleteForward(ctx context.Context, token string, id int64, etag string) error {
	_, err := c.request(ctx, token, "DELETE", fmt.Sprintf("/api/v1/user/forwards/%d", id), etag, "", nil, nil)
	return err
}
