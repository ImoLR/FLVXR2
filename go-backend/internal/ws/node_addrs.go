package ws

import "strings"

// Node DDNS (plan 142): agents with the addrReport capability put their
// interface addresses into a metric message when the set changed (and in
// the first metric of every session). The panel hands each new set to the
// DDNS updater; nothing is stored here beyond the last key per session.

// NodeAddrs is an agent address report: global IPv6 and public IPv4 on the
// node's interfaces, default-route interface first.
type NodeAddrs struct {
	V4 []string `json:"v4"`
	V6 []string `json:"v6"`
}

func (a NodeAddrs) key() string {
	return strings.Join(a.V4, ",") + "|" + strings.Join(a.V6, ",")
}

// SetNodeAddrsHook registers the callback for new address reports.
func (s *Server) SetNodeAddrsHook(fn func(nodeID int64, addrs NodeAddrs)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onNodeAddrs = fn
	s.mu.Unlock()
}

// recordAddrs runs in the node's read loop. Bounded input: at most 32
// addresses per family are passed on.
func (s *Server) recordAddrs(ns *nodeSession, addrs *NodeAddrs) {
	if addrs == nil || ns == nil {
		return
	}
	report := NodeAddrs{V4: capList(addrs.V4, 32), V6: capList(addrs.V6, 32)}
	key := report.key()
	if ns.addrsKey == key {
		return
	}
	ns.addrsKey = key
	s.mu.RLock()
	hook := s.onNodeAddrs
	s.mu.RUnlock()
	if hook != nil {
		// Called inline (keeps report order); the hook must not block.
		hook(ns.nodeID, report)
	}
}

func capList(in []string, n int) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && len(v) <= 45 && len(out) < n {
			out = append(out, v)
		}
	}
	return out
}

// NodeDDNSSession describes the node's current agent session for DDNS:
// online, whether the agent reports addresses, and the client IP the panel
// sees (through cloudflared: Cf-Connecting-Ip).
func (s *Server) NodeDDNSSession(nodeID int64) (online, addrReport bool, clientIP string) {
	if s == nil {
		return false, false, ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := s.nodes[nodeID]
	if ns == nil {
		return false, false, ""
	}
	return true, ns.addrReport > 0, ns.clientIP
}
