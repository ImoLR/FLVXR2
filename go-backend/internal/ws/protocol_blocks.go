package ws

// ProtocolBlocks (plan 182) are the agent's protocol-filter counters:
// connections closed since the agent started, per kind. Agents send them in
// the first metric of a session and then only on change; the panel keeps the
// latest per session in memory (no DB write) for the admin node card.
type ProtocolBlocks struct {
	HTTP  uint64 `json:"http"`
	TLS   uint64 `json:"tls"`
	SOCKS uint64 `json:"socks"`
	Other uint64 `json:"other"`
}

func (s *Server) recordProtocolBlocks(ns *nodeSession, p *ProtocolBlocks) {
	if ns == nil || p == nil {
		return
	}
	v := *p
	ns.protocolBlocks.Store(&v)
}

// ProtocolBlocks returns a copy of the node's counters from its live session,
// or nil (offline, old agent, or nothing reported yet).
func (s *Server) ProtocolBlocks(nodeID int64) *ProtocolBlocks {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	ns := s.nodes[nodeID]
	s.mu.RUnlock()
	if ns == nil {
		return nil
	}
	p := ns.protocolBlocks.Load()
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
