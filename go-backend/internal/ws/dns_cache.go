package ws

import "strings"

// DNSCacheReport (plan 240) is the agent's local DNS cache check: mode dnsmasq | resolved | minimal | none,
// ok=false only when the cache is missing or broken. Agents re-check at most every 10 minutes and send the
// last result in every metric; the panel keeps it in the live session (memory only, no DB write).
type DNSCacheReport struct {
	Mode      string `json:"mode"`
	OK        bool   `json:"ok"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt int64  `json:"checked_at,omitempty"`
}

// DNSCacheView is what the admin node list shows. Supported=false: online agent without plan 240 (未知).
type DNSCacheView struct {
	Supported bool   `json:"supported"`
	Mode      string `json:"mode,omitempty"`
	OK        bool   `json:"ok"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt int64  `json:"checkedAt,omitempty"`
}

func sanitizeDNSCache(p *DNSCacheReport) *DNSCacheReport {
	if p == nil {
		return nil
	}
	v := *p
	switch v.Mode {
	case "dnsmasq", "resolved", "minimal", "none":
	default:
		v.Mode = "none"
		v.OK = false
	}
	v.Reason = strings.TrimSpace(v.Reason)
	if r := []rune(v.Reason); len(r) > 120 {
		v.Reason = string(r[:120])
	}
	return &v
}

func (s *Server) recordDNSCache(ns *nodeSession, p *DNSCacheReport) {
	if ns == nil || p == nil {
		return
	}
	ns.dnsCacheState.Store(sanitizeDNSCache(p))
}

// DNSCache returns the node's DNS cache state from its live session, or nil when the node is offline.
func (s *Server) DNSCache(nodeID int64) *DNSCacheView {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	ns := s.nodes[nodeID]
	s.mu.RUnlock()
	if ns == nil {
		return nil
	}
	view := &DNSCacheView{Supported: ns.dnsCache > 0}
	if p := ns.dnsCacheState.Load(); p != nil {
		view.Supported = true
		view.Mode, view.OK, view.Reason, view.CheckedAt = p.Mode, p.OK, p.Reason, p.CheckedAt
	}
	return view
}

// DNSCacheCapable: the agent announced dnsCache=1 (runs DNSCacheEnsure).
func (s *Server) DNSCacheCapable(nodeID int64) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := s.nodes[nodeID]
	return ns != nil && ns.dnsCache > 0
}

// SetDNSCache stores the state returned by a DNSCacheEnsure reply (so the list is fresh before the next metric).
func (s *Server) SetDNSCache(nodeID int64, p *DNSCacheReport) {
	if s == nil || p == nil {
		return
	}
	s.mu.RLock()
	ns := s.nodes[nodeID]
	s.mu.RUnlock()
	s.recordDNSCache(ns, p)
}
