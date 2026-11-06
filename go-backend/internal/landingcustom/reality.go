package landingcustom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

// RealityProbeRequest is the probe body (Custom v1.4.88: targets host:port,
// timeout_ms 500–5000).
type RealityProbeRequest struct {
	Targets   []string `json:"targets"`
	TimeoutMs int      `json:"timeout_ms,omitempty"`
}

// RealityProbeIP / RealityProbeResult mirror contract §1 (Custom reports
// milliseconds as floats; -1 = the IP did not answer).
type RealityProbeIP struct {
	IP    string  `json:"ip"`
	TCPMs float64 `json:"tcp_ms"`
}

type RealityProbeResult struct {
	Target         string           `json:"target"`
	Feasible       bool             `json:"feasible"`
	Reason         string           `json:"reason,omitempty"`
	LatencyMs      float64          `json:"latency_ms"`
	BestIP         string           `json:"best_ip,omitempty"`
	IPs            []RealityProbeIP `json:"ips"`
	TLS13          bool             `json:"tls13"`
	X25519         bool             `json:"x25519"`
	H2             bool             `json:"h2"`
	CertValid      bool             `json:"cert_valid"`
	ServerNames    []string         `json:"server_names"`
	CertChainBytes int              `json:"cert_chain_bytes"`
}

// RealityProbeJob is Custom's probe job: POST /shares/{id}/reality-probe
// queues it on the share's Helper (picked up with its next upload), GET
// /shares/{id}/reality-probe/{probe_id} polls it. State queued | running |
// done | error.
type RealityProbeJob struct {
	ProbeID string               `json:"probe_id"`
	State   string               `json:"state"`
	Error   string               `json:"error,omitempty"`
	Results []RealityProbeResult `json:"results"`
}

// Finished reports a final state.
func (j RealityProbeJob) Finished() bool { return j.State == "done" || j.State == "error" }

// StartRealityProbe queues a 测速 on the share's machine.
func (c *Client) StartRealityProbe(ctx context.Context, id int64, req RealityProbeRequest) (RealityProbeJob, error) {
	var out RealityProbeJob
	err := c.request(ctx, http.MethodPost, fmt.Sprintf("/shares/%d/reality-probe", id), req, &out)
	if err == nil && (out.ProbeID == "" || len(out.ProbeID) > 64) {
		err = errors.New("妙妙屋测速任务格式错误")
	}
	out.Results = sanitizeRealityResults(out.Results)
	return out, err
}

// RealityProbeStatus polls a queued 测速.
func (c *Client) RealityProbeStatus(ctx context.Context, id int64, probeID string) (RealityProbeJob, error) {
	var out RealityProbeJob
	err := c.request(ctx, http.MethodGet, fmt.Sprintf("/shares/%d/reality-probe/%s", id, url.PathEscape(probeID)), nil, &out)
	out.Results = sanitizeRealityResults(out.Results)
	if len(out.Error) > 200 {
		out.Error = out.Error[:200]
	}
	return out, err
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
		if len(r.ServerNames) > 32 {
			r.ServerNames = r.ServerNames[:32]
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

// RealityPin is one pinned dest host in share status (contract §3; Custom
// reports float milliseconds, Fallback = no IP answered, the first DNS
// answer is used as before).
type RealityPin struct {
	Host      string   `json:"host"`
	Port      int      `json:"port,omitempty"`
	PinnedIP  string   `json:"pinned_ip,omitempty"`
	PinnedMs  float64  `json:"pinned_ms,omitempty"`
	BestIP    string   `json:"best_ip,omitempty"`
	BestMs    float64  `json:"best_ms,omitempty"`
	CheckedAt int64    `json:"checked_at"`
	Nodes     []string `json:"nodes,omitempty"`
	Fallback  bool     `json:"fallback,omitempty"`
	Error     string   `json:"error,omitempty"`
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
	Domains    []RealityPoolDomain `json:"domains"`
	FetchedAt  int64               `json:"fetched_at"` // 0 = never fetched (refreshing / unavailable)
	Reason     string              `json:"reason,omitempty"`
	Refreshing bool                `json:"refreshing,omitempty"`
}

// UnmarshalJSON accepts domains as objects or plain strings.
func (p *RealityPool) UnmarshalJSON(b []byte) error {
	var v struct {
		Domains    []json.RawMessage `json:"domains"`
		FetchedAt  int64             `json:"fetched_at"`
		Reason     string            `json:"reason"`
		Error      string            `json:"error"`
		Refreshing bool              `json:"refreshing"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.FetchedAt, p.Reason, p.Refreshing, p.Domains = v.FetchedAt, v.Reason, v.Refreshing, nil
	if p.Reason == "" {
		p.Reason = v.Error
	}
	if len(p.Reason) > 200 {
		p.Reason = p.Reason[:200]
	}
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
