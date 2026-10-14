package ws

import "sort"

// ServiceInventorySnapshot distinguishes an empty report from no report, and
// only returns data from the current WS session. Callers must check freshness.
func (s *Server) ServiceInventorySnapshot(nodeID int64) (names []string, updatedAt int64, online bool) {
	if s == nil {
		return nil, 0, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	online = s.nodes[nodeID] != nil
	conns := s.serviceConnections[nodeID]
	if conns == nil {
		return nil, 0, online
	}
	names = make([]string, 0, len(conns))
	for name := range conns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, s.serviceConnUpdateTime[nodeID], online
}
