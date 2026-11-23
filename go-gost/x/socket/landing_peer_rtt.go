package socket

import (
	"fmt"
	"sort"
)

// Plan 301 利群诊断 「专线出口 → 落地」: measured on the native landing, not
// estimated. One-shot, read-only: the kernel's own TCP rtt (tcp_info via
// sock_diag) of the ESTABLISHED inbound connections on one landing port,
// grouped by remote IP. The panel drops the FLVX nodes' own IPs (备线 tunnel
// exits) and reads the busiest remaining peer as the 利群 relay exit. No
// probes, no new connections, nothing kept.

const (
	landingPeerDefaultMax = 16
	landingPeerMaxPeers   = 64
)

type landingPeerReq struct {
	Port int `json:"port"`
	Max  int `json:"max"`
}

type landingPeer struct {
	IP       string  `json:"ip"`
	Conns    int     `json:"conns"`
	RttMs    float64 `json:"rttMs"`    // median smoothed rtt of the peer's connections
	MinRttMs float64 `json:"minRttMs"` // lowest kernel min_rtt (path floor), 0 = unknown
}

type landingPeerResp struct {
	V         int           `json:"v"`
	Port      int           `json:"port"`
	Peers     []landingPeer `json:"peers"`
	Total     int           `json:"total"` // established inbound connections on the port
	Truncated bool          `json:"truncated,omitempty"`
}

// landingPeerSample: one ESTABLISHED socket (local port, remote IP, rtt µs).
type landingPeerSample struct {
	LocalPort int
	Remote    string
	RttUs     uint32
	MinRttUs  uint32
}

func handleLandingPeerRtt(data interface{}) (*landingPeerResp, error) {
	var req landingPeerReq
	if err := decodeCommandData(data, &req); err != nil {
		return nil, err
	}
	if req.Port <= 0 || req.Port > 65535 {
		return nil, fmt.Errorf("port must be 1–65535")
	}
	if req.Max <= 0 {
		req.Max = landingPeerDefaultMax
	}
	if req.Max > landingPeerMaxPeers {
		req.Max = landingPeerMaxPeers
	}
	samples, err := landingPeerSamples(req.Port)
	if err != nil {
		return nil, err
	}
	return summarizeLandingPeers(req.Port, req.Max, samples), nil
}

func summarizeLandingPeers(port, max int, samples []landingPeerSample) *landingPeerResp {
	type acc struct {
		conns  int
		rtts   []uint32
		minRtt uint32
	}
	byIP := map[string]*acc{}
	total := 0
	for _, s := range samples {
		if s.LocalPort != port || s.Remote == "" {
			continue
		}
		total++
		a := byIP[s.Remote]
		if a == nil {
			a = &acc{}
			byIP[s.Remote] = a
		}
		a.conns++
		if s.RttUs > 0 {
			a.rtts = append(a.rtts, s.RttUs)
		}
		if s.MinRttUs > 0 && (a.minRtt == 0 || s.MinRttUs < a.minRtt) {
			a.minRtt = s.MinRttUs
		}
	}
	out := &landingPeerResp{V: 1, Port: port, Peers: []landingPeer{}, Total: total}
	for ip, a := range byIP {
		p := landingPeer{IP: ip, Conns: a.conns}
		if len(a.rtts) > 0 {
			sort.Slice(a.rtts, func(i, j int) bool { return a.rtts[i] < a.rtts[j] })
			p.RttMs = float64(a.rtts[len(a.rtts)/2]) / 1000
		}
		p.MinRttMs = float64(a.minRtt) / 1000
		out.Peers = append(out.Peers, p)
	}
	sort.Slice(out.Peers, func(i, j int) bool {
		if out.Peers[i].Conns != out.Peers[j].Conns {
			return out.Peers[i].Conns > out.Peers[j].Conns
		}
		return out.Peers[i].IP < out.Peers[j].IP
	})
	if len(out.Peers) > max {
		out.Peers, out.Truncated = out.Peers[:max], true
	}
	return out
}
