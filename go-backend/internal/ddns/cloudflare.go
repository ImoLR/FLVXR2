// Package ddns holds the node DDNS building blocks (plan 142): a small
// Cloudflare DNS client and the pure address-selection logic. It has no
// database or panel dependencies.
package ddns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// RecordTTL is the TTL of every record FLVX writes (user: 2 minutes, not faster).
const RecordTTL = 120

// RecordComment marks records written by FLVX in the Cloudflare dashboard.
const RecordComment = "FLVX DDNS"

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

// Client talks to the Cloudflare v4 API with a scoped API token. It never
// deletes records.
type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
	// Calls counts API requests (tests and resource measurements).
	Calls func(method, path string)
}

func NewCloudflare(token string) *Client {
	return &Client{Token: strings.TrimSpace(token), BaseURL: cloudflareAPI, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// APIError is a Cloudflare error answer (HTTP status + first error message).
type APIError struct {
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("Cloudflare HTTP %d", e.Status)
	}
	if e.Code != 0 {
		return fmt.Sprintf("Cloudflare %d: %s", e.Code, e.Message)
	}
	return "Cloudflare: " + e.Message
}

// IsAuthError reports a rejected / revoked token (check the shared token first).
func IsAuthError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden ||
			apiErr.Code == 1000 || apiErr.Code == 9109 || apiErr.Code == 10000
	}
	return false
}

type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiMessage    `json:"errors"`
	Result  json.RawMessage `json:"result"`
	Info    *struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

type apiMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body interface{}, out interface{}) (*envelope, error) {
	if c == nil || c.Token == "" {
		return nil, errors.New("未设置 API Token")
	}
	base := c.BaseURL
	if base == "" {
		base = cloudflareAPI
	}
	target := strings.TrimRight(base, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Calls != nil {
		c.Calls(method, path)
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// The token is only in a header; report the transport cause only.
		msg := err.Error()
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			msg = urlErr.Err.Error()
		}
		return nil, fmt.Errorf("连接 Cloudflare 失败：%s", strings.TrimSpace(msg))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, &APIError{Status: resp.StatusCode, Message: fmt.Sprintf("HTTP %d，返回内容无法解析", resp.StatusCode)}
	}
	if !env.Success || resp.StatusCode >= 300 {
		apiErr := &APIError{Status: resp.StatusCode}
		if len(env.Errors) > 0 {
			apiErr.Code, apiErr.Message = env.Errors[0].Code, env.Errors[0].Message
		}
		return nil, apiErr
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return nil, err
		}
	}
	return &env, nil
}

// TokenInfo is the answer of GET /user/tokens/verify.
type TokenInfo struct {
	Status    string    `json:"status"`
	ExpiresOn time.Time `json:"-"`
}

func (c *Client) VerifyToken(ctx context.Context) (TokenInfo, error) {
	var raw struct {
		Status    string `json:"status"`
		ExpiresOn string `json:"expires_on"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, nil, &raw); err != nil {
		return TokenInfo{}, err
	}
	info := TokenInfo{Status: raw.Status}
	if raw.ExpiresOn != "" {
		if t, err := time.Parse(time.RFC3339, raw.ExpiresOn); err == nil {
			info.ExpiresOn = t
		}
	}
	return info, nil
}

// Zone is one DNS zone visible to the token.
type Zone struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Permissions []string `json:"permissions"`
}

// CanEditDNS: tokens list their zone permissions; an empty list means unknown
// (treated as editable, the write itself is the final check).
func (z Zone) CanEditDNS() bool {
	if len(z.Permissions) == 0 {
		return true
	}
	for _, p := range z.Permissions {
		if p == "#dns_records:edit" {
			return true
		}
	}
	return false
}

// ListZones returns every zone the token can see (paged, at most 10 pages).
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	var all []Zone
	for page := 1; page <= 10; page++ {
		var zones []Zone
		env, err := c.do(ctx, http.MethodGet, "/zones", url.Values{"per_page": {"50"}, "page": {fmt.Sprint(page)}}, nil, &zones)
		if err != nil {
			return nil, err
		}
		all = append(all, zones...)
		if env.Info == nil || env.Info.TotalPages <= page {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

// ZoneFor picks the longest zone the domain belongs to (not the apex itself).
func ZoneFor(domain string, zones []Zone) (Zone, bool) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	best, ok := Zone{}, false
	for _, z := range zones {
		name := strings.ToLower(z.Name)
		if strings.HasSuffix(domain, "."+name) && len(name) > len(best.Name) {
			best, ok = z, true
		}
	}
	return best, ok
}

// Record is one DNS record.
type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment,omitempty"`
}

// ListRecords lists the records of one name (all types when recordType is "").
func (c *Client) ListRecords(ctx context.Context, zoneID, name, recordType string) ([]Record, error) {
	q := url.Values{"name": {strings.ToLower(name)}, "per_page": {"100"}}
	if recordType != "" {
		q.Set("type", recordType)
	}
	var out []Record
	if _, err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/dns_records", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetRecord reads one record by id.
func (c *Client) GetRecord(ctx context.Context, zoneID, recordID string) (Record, error) {
	var out Record
	_, err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(recordID), nil, nil, &out)
	return out, err
}

type recordWrite struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

// CreateRecord creates an unproxied record with the FLVX TTL.
func (c *Client) CreateRecord(ctx context.Context, zoneID, recordType, name, content string) (Record, error) {
	var out Record
	_, err := c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", nil,
		recordWrite{Type: recordType, Name: strings.ToLower(name), Content: content, TTL: RecordTTL, Proxied: false, Comment: RecordComment}, &out)
	return out, err
}

// UpdateRecord rewrites a record (content, TTL 120, never proxied).
func (c *Client) UpdateRecord(ctx context.Context, zoneID, recordID, recordType, name, content string) (Record, error) {
	var out Record
	_, err := c.do(ctx, http.MethodPut, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(recordID), nil,
		recordWrite{Type: recordType, Name: strings.ToLower(name), Content: content, TTL: RecordTTL, Proxied: false, Comment: RecordComment}, &out)
	return out, err
}
