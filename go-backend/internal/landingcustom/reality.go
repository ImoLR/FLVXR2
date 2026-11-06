package landingcustom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Contract reality-pick-v1 (plan 227): Reality 伪装域名 测速 from the shared
// server (Helper), the Helper's fastest-IP pins and 妙妙屋's 「Reality 共享
// 域名池」. Every part is optional: a share without the capability keeps
// today's behaviour and FLVX shows 「共享服务器需要妙妙屋升级后才能测速」.

// RealityProbeVersion / RealityPinVersion are the contract versions FLVX speaks.
const (
	RealityProbeVersion = 1
	RealityPinVersion   = 1
)

// ShareVersions is a capability object {"versions":[…]}.
type ShareVersions struct {
	Versions []int `json:"versions"`
}

// Supports reports whether the capability lists version v.
func (c *ShareVersions) Supports(v int) bool { return c != nil && containsInt(c.Versions, v) }

func sanitizeVersions(c *ShareVersions) *ShareVersions {
	if c == nil {
		return nil
	}
	versions := []int{}
	for _, v := range c.Versions {
		if v > 0 && v < 1000 && !containsInt(versions, v) && len(versions) < 8 {
			versions = append(versions, v)
		}
	}
	if len(versions) == 0 {
		return nil
	}
	return &ShareVersions{Versions: versions}
}

// RealityProbeRequest is the probe body.
type RealityProbeRequest struct {
	Candidates []string `json:"candidates"`
	TimeoutMs  int      `json:"timeout_ms,omitempty"`
}

// RealityProbeIP / RealityProbeResult mirror contract §1.
type RealityProbeIP struct {
	IP    string `json:"ip"`
	TCPMs int    `json:"tcp_ms"`
}

type RealityProbeResult struct {
	Target         string           `json:"target"`
	Feasible       bool             `json:"feasible"`
	Reason         string           `json:"reason,omitempty"`
	LatencyMs      int              `json:"latency_ms"`
	BestIP         string           `json:"best_ip,omitempty"`
	IPs            []RealityProbeIP `json:"ips"`
	TLS13          bool             `json:"tls13"`
	X25519         bool             `json:"x25519"`
	H2             bool             `json:"h2"`
	CertValid      bool             `json:"cert_valid"`
	ServerNames    []string         `json:"server_names"`
	CertChainBytes int              `json:"cert_chain_bytes"`
}

// ProbeReality runs a 测速 on the share's machine (sync, ≤ ~100 s).
func (c *Client) ProbeReality(ctx context.Context, id int64, req RealityProbeRequest) ([]RealityProbeResult, error) {
	var raw json.RawMessage
	slow := &http.Client{Timeout: 120 * time.Second, CheckRedirect: c.http.CheckRedirect}
	if err := c.requestWith(ctx, slow, http.MethodPost, fmt.Sprintf("/shares/%d/reality-probe", id), req, &raw); err != nil {
		return nil, err
	}
	return decodeRealityResults(raw)
}

// decodeRealityResults accepts {"results":[…]} or a bare list.
func decodeRealityResults(raw json.RawMessage) ([]RealityProbeResult, error) {
	var wrapped struct {
		Results []RealityProbeResult `json:"results"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Results != nil {
		return sanitizeRealityResults(wrapped.Results), nil
	}
	var list []RealityProbeResult
	if json.Unmarshal(raw, &list) == nil {
		return sanitizeRealityResults(list), nil
	}
	return nil, errors.New("妙妙屋测速结果格式错误")
}

func sanitizeRealityResults(in []RealityProbeResult) []RealityProbeResult {
	out := make([]RealityProbeResult, 0, len(in))
	for _, r := range in {
		r.Target = strings.TrimSpace(r.Target)
		if r.Target == "" || len(r.Target) > 260 {
			continue
		}
		if len(r.Reason) > 200 {
			r.Reason = r.Reason[:200]
		}
		if len(r.IPs) > 16 {
			r.IPs = r.IPs[:16]
		}
		if len(r.ServerNames) > 16 {
			r.ServerNames = r.ServerNames[:16]
		}
		if r.IPs == nil {
			r.IPs = []RealityProbeIP{}
		}
		if r.ServerNames == nil {
			r.ServerNames = []string{}
		}
		out = append(out, r)
		if len(out) == 64 {
			break
		}
	}
	return out
}

// RealityPin is one pinned dest host in share status (contract §3).
type RealityPin struct {
	Host      string `json:"host"`
	Port      int    `json:"port,omitempty"`
	PinnedIP  string `json:"pinned_ip,omitempty"`
	PinnedMs  int    `json:"pinned_ms,omitempty"`
	BestIP    string `json:"best_ip,omitempty"`
	BestMs    int    `json:"best_ms,omitempty"`
	CheckedAt int64  `json:"checked_at"`
	Error     string `json:"error,omitempty"`
}

// RealityPins never fails to decode: a malformed optional report must not
// stop the status poll (billing). nil = not reported.
type RealityPins []RealityPin

func (p *RealityPins) UnmarshalJSON(b []byte) error {
	var list []RealityPin
	if json.Unmarshal(b, &list) != nil {
		var wrapped struct {
			Pins []RealityPin `json:"pins"`
		}
		if json.Unmarshal(b, &wrapped) != nil {
			*p = nil
			return nil
		}
		list = wrapped.Pins
	}
	out := RealityPins{}
	for _, pin := range list {
		if pin.Host == "" || len(pin.Host) > 253 {
			continue
		}
		out = append(out, pin)
		if len(out) == 32 {
			break
		}
	}
	*p = out
	return nil
}

// RealityPoolDomain is one 妙妙屋 共享域名池 entry.
type RealityPoolDomain struct {
	Domain string `json:"domain"`
}

// RealityPool is GET /reality-pool. Reason explains an empty list (no
// license, official unreachable); it is not an error.
type RealityPool struct {
	Domains   []RealityPoolDomain `json:"domains"`
	FetchedAt int64               `json:"fetched_at"`
	Reason    string              `json:"reason,omitempty"`
}

// UnmarshalJSON accepts domains as objects or plain strings.
func (p *RealityPool) UnmarshalJSON(b []byte) error {
	var v struct {
		Domains   []json.RawMessage `json:"domains"`
		FetchedAt int64             `json:"fetched_at"`
		Reason    string            `json:"reason"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.FetchedAt, p.Reason, p.Domains = v.FetchedAt, v.Reason, nil
	for _, raw := range v.Domains {
		var d RealityPoolDomain
		if json.Unmarshal(raw, &d) != nil || d.Domain == "" {
			var s string
			if json.Unmarshal(raw, &s) != nil {
				continue
			}
			d.Domain = s
		}
		p.Domains = append(p.Domains, d)
		if len(p.Domains) == 500 {
			break
		}
	}
	return nil
}

// RealityPool fetches 妙妙屋's shared Reality domain pool.
func (c *Client) RealityPool(ctx context.Context) (RealityPool, error) {
	var out RealityPool
	err := c.request(ctx, http.MethodGet, "/reality-pool", nil, &out)
	return out, err
}
