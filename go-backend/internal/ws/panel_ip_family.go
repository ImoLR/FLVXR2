package ws

import (
	"fmt"
	"time"
)

// SyncPanelIPFamily uses the existing command channel. The handshake field is
// also the capability marker, so older agents never receive an unknown command.
func (s *Server) SyncPanelIPFamily(nodeID int64) error {
	s.mu.RLock()
	ns := s.nodes[nodeID]
	reported := ""
	if ns != nil {
		reported = ns.panelIPFamily
	}
	s.mu.RUnlock()
	if reported == "" {
		return nil
	}
	mode, err := s.repo.ResolvePanelIPFamily(nodeID, reported)
	if err != nil || mode == reported {
		return err
	}
	result, err := s.SendCommand(nodeID, "SetPanelIPFamily", map[string]interface{}{"panelIpFamily": mode}, 5*time.Second)
	if err != nil {
		return err
	}
	if !result.Success {
		return fmt.Errorf("%s", result.Message)
	}
	s.mu.Lock()
	if s.nodes[nodeID] == ns {
		ns.panelIPFamily = mode
	}
	s.mu.Unlock()
	return nil
}
