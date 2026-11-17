package socket

// Plan 272 利群规则「诊断」: one-shot, on the panel's request only. On a 利群入口
// host the agent reads what the LeiKwan client already keeps:
//   - /run/leikwan-fast-failover/forward-<id>.offered.json — the (≤ 4)
//     relay ingresses the client was offered {at, offered:[{endpoint, ref}]};
//   - …/forward-<id>.lines.json — ref → endpoint of earlier offers;
//   - /run/ultra/forward-<id>.status.json — latest_tcp / active_udp with the
//     selection_ref of the ingress in use and the client's own rtt_ms (entry →
//     landing through the line).
// and times a TCP handshake to each ingress (count ≤ 5, one connection at a
// time, RST close, the rest of a target skipped after a timeout). Nothing is
// written, no vendor socket is touched, nothing keeps running afterwards.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	lkDiagMaxTargets = 6
	lkDiagMaxCount   = 5
)

// LeiKwanDiagRequest is the panel's LeiKwanDiag command.
type LeiKwanDiagRequest struct {
	ForwardID int64 `json:"forwardId"`
	Count     int   `json:"count"`
	TimeoutMs int   `json:"timeoutMs"`
}

// LKDiagIngress: one relay ingress of the forward, timed from this host.
type LKDiagIngress struct {
	Endpoint string  `json:"endpoint"`
	InUse    string  `json:"inUse,omitempty"` // "tcp" | "udp" | "tcp+udp"
	Offered  bool    `json:"offered"`
	OK       bool    `json:"ok"`
	AvgMs    float64 `json:"avgMs"`
	Loss     float64 `json:"loss"` // %
	Sent     int     `json:"sent"`
	Error    string  `json:"error,omitempty"`
}

// LKDiagClient: the LeiKwan client's own measurement (entry → landing).
type LKDiagClient struct {
	RttMs float64 `json:"rttMs"`
	P95Ms float64 `json:"p95Ms,omitempty"`
	Sent  int     `json:"sent,omitempty"`
	Recv  int     `json:"recv,omitempty"`
	AgeS  int64   `json:"ageS"` // seconds since the client selected / measured it (-1 unknown)
}

// LeiKwanDiagResponse goes back to the panel.
type LeiKwanDiagResponse struct {
	V         int             `json:"v"`
	ForwardID int64           `json:"forwardId"`
	Found     bool            `json:"found"` // the client has status / offer files for it
	State     string          `json:"state,omitempty"`
	Ingresses []LKDiagIngress `json:"ingresses"`
	TCP       *LKDiagClient   `json:"tcp,omitempty"`
	UDP       *LKDiagClient   `json:"udp,omitempty"`
	OfferAgeS int64           `json:"offerAgeS"`
}

type lkDiagOffer struct {
	Endpoint string `json:"endpoint"`
	Ref      string `json:"ref"`
}

// lkDiagParseOffered reads offered.json: the current offer and its time.
func lkDiagParseOffered(raw []byte) ([]lkDiagOffer, float64) {
	var doc struct {
		At      float64       `json:"at"`
		Offered []lkDiagOffer `json:"offered"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil, 0
	}
	out := make([]lkDiagOffer, 0, len(doc.Offered))
	for _, o := range doc.Offered {
		if lkDiagEndpointOK(o.Endpoint) {
			out = append(out, o)
		}
	}
	return out, doc.At
}

// lkDiagParseLines reads lines.json: ref → endpoint of earlier offers.
func lkDiagParseLines(raw []byte) map[string]string {
	var doc map[string]struct {
		Endpoint string `json:"endpoint"`
	}
	out := map[string]string{}
	if json.Unmarshal(raw, &doc) != nil {
		return out
	}
	for ref, v := range doc {
		if lkDiagEndpointOK(v.Endpoint) {
			out[ref] = v.Endpoint
		}
	}
	return out
}

type lkDiagSel struct {
	Ref    string  `json:"selection_ref"`
	Rtt    float64 `json:"rtt_ms"`
	P95    float64 `json:"p95_rtt_ms"`
	Sent   int     `json:"probe_sent"`
	Recv   int     `json:"probe_received"`
	Select string  `json:"selected_at"`
}

// lkDiagParseStatus reads the forward's status.json.
func lkDiagParseStatus(raw []byte) (state string, tcp, udp *lkDiagSel) {
	var doc struct {
		State string     `json:"state"`
		TCP   *lkDiagSel `json:"latest_tcp"`
		UDP   *lkDiagSel `json:"active_udp"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return "", nil, nil
	}
	return doc.State, doc.TCP, doc.UDP
}

// lkDiagEndpointOK: an "IP:port" (no host names: nothing is resolved).
func lkDiagEndpointOK(ep string) bool {
	host, port, err := net.SplitHostPort(ep)
	if err != nil || net.ParseIP(host) == nil {
		return false
	}
	p, err := strconv.Atoi(port)
	return err == nil && p > 0 && p < 65536
}

func lkDiagClient(s *lkDiagSel, now time.Time) *LKDiagClient {
	if s == nil || s.Rtt <= 0 {
		return nil
	}
	c := &LKDiagClient{RttMs: s.Rtt, P95Ms: s.P95, Sent: s.Sent, Recv: s.Recv, AgeS: -1}
	if t, err := time.Parse(time.RFC3339Nano, s.Select); err == nil {
		c.AgeS = int64(now.Sub(t).Seconds())
		if c.AgeS < 0 {
			c.AgeS = 0
		}
	}
	return c
}

// lkDiagDial is the handshake used by the probe (the lab swaps it).
var lkDiagDial = func(ctx context.Context, endpoint string, timeout time.Duration) (time.Duration, error) {
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return 0, err
	}
	elapsed := time.Since(start)
	closeProbeConn(conn, false)
	return elapsed, nil
}

// lkDiagRun builds the answer from the host files below root ("/" on a host).
func lkDiagRun(ctx context.Context, root string, req LeiKwanDiagRequest, now time.Time) (*LeiKwanDiagResponse, error) {
	if req.ForwardID <= 0 {
		return nil, errors.New("缺少利群转发 ID")
	}
	count := req.Count
	if count <= 0 {
		count = 3
	}
	if count > lkDiagMaxCount {
		count = lkDiagMaxCount
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	if timeout < 200*time.Millisecond {
		timeout = 200 * time.Millisecond
	}
	if timeout > 3*time.Second {
		timeout = 3 * time.Second
	}
	read := func(rel string) ([]byte, bool) {
		return lkReadLimited(filepath.Join(root, rel))
	}
	id := strconv.FormatInt(req.ForwardID, 10)
	out := &LeiKwanDiagResponse{V: 1, ForwardID: req.ForwardID, Ingresses: []LKDiagIngress{}, OfferAgeS: -1}
	var offers []lkDiagOffer
	if raw, ok := read("run/leikwan-fast-failover/forward-" + id + ".offered.json"); ok {
		var at float64
		offers, at = lkDiagParseOffered(raw)
		out.Found = true
		if at > 0 {
			out.OfferAgeS = int64(now.Sub(time.Unix(int64(at), 0)).Seconds())
		}
	}
	refs := map[string]string{}
	for _, o := range offers {
		refs[o.Ref] = o.Endpoint
	}
	if raw, ok := read("run/leikwan-fast-failover/forward-" + id + ".lines.json"); ok {
		for ref, ep := range lkDiagParseLines(raw) {
			if _, ok := refs[ref]; !ok {
				refs[ref] = ep
			}
		}
	}
	var tcp, udp *lkDiagSel
	if raw, ok := read("run/ultra/forward-" + id + ".status.json"); ok {
		out.Found = true
		out.State, tcp, udp = lkDiagParseStatus(raw)
		out.TCP, out.UDP = lkDiagClient(tcp, now), lkDiagClient(udp, now)
	}
	// Targets: the offer (in its order), then in-use ingresses not offered any more.
	inUse := map[string][]string{}
	if tcp != nil && refs[tcp.Ref] != "" {
		inUse[refs[tcp.Ref]] = append(inUse[refs[tcp.Ref]], "tcp")
	}
	if udp != nil && refs[udp.Ref] != "" {
		inUse[refs[udp.Ref]] = append(inUse[refs[udp.Ref]], "udp")
	}
	seen := map[string]bool{}
	for _, o := range offers {
		if !seen[o.Endpoint] && len(out.Ingresses) < lkDiagMaxTargets {
			seen[o.Endpoint] = true
			out.Ingresses = append(out.Ingresses, LKDiagIngress{Endpoint: o.Endpoint, Offered: true})
		}
	}
	extra := make([]string, 0, len(inUse))
	for ep := range inUse {
		if !seen[ep] {
			extra = append(extra, ep)
		}
	}
	sort.Strings(extra)
	for _, ep := range extra {
		if len(out.Ingresses) < lkDiagMaxTargets {
			seen[ep] = true
			out.Ingresses = append(out.Ingresses, LKDiagIngress{Endpoint: ep})
		}
	}
	for i := range out.Ingresses {
		in := &out.Ingresses[i]
		in.InUse = strings.Join(inUse[in.Endpoint], "+")
		var sum time.Duration
		okN := 0
		for n := 0; n < count; n++ {
			if ctx.Err() != nil {
				break
			}
			if n > 0 {
				time.Sleep(100 * time.Millisecond)
			}
			in.Sent++
			d, err := lkDiagDial(ctx, in.Endpoint, timeout)
			if err != nil {
				in.Error = lkDiagErr(err)
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break // a dead ingress is not hammered: the rest count as lost
				}
				continue
			}
			sum += d
			okN++
		}
		lost := count - okN
		in.Loss = float64(lost) * 100 / float64(count)
		if okN > 0 {
			in.OK = true
			in.AvgMs = float64(sum.Microseconds()) / 1000 / float64(okN)
			in.AvgMs = float64(int64(in.AvgMs*10+0.5)) / 10
			if lost == 0 {
				in.Error = ""
			}
		}
	}
	return out, nil
}

func lkDiagErr(err error) string {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return "连接超时"
	case strings.Contains(err.Error(), "refused"):
		return "连接被拒绝"
	case strings.Contains(err.Error(), "unreachable"):
		return "网络不可达"
	}
	msg := err.Error()
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}

// handleLeiKwanDiag decodes the command and runs it below root.
func handleLeiKwanDiag(ctx context.Context, root string, data interface{}) (*LeiKwanDiagResponse, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var req LeiKwanDiagRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("诊断参数无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return lkDiagRun(ctx, root, req, time.Now())
}

// lkReadLimited reads a small host file (≤ 1 MiB).
func lkReadLimited(path string) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	return data, err == nil
}
