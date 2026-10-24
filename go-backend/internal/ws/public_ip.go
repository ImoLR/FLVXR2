package ws

import (
	"log"
	"strings"
	"sync"
	"time"

	"go-backend/internal/store/repo"
)

var (
	publicIPGateOnce sync.Once
	publicIPGate     *repo.PublicIPReportGate
)

// applyPublicIPReport (plan 147) handles the agent's ReportPublicIP message:
// rate-limited per node, then the fill-empty rule (repo.NodePublicIPFill) — a
// report never overwrites a node address. One log line per filled node.
func (s *Server) applyPublicIPReport(nodeID int64, ipv4, ipv6 string) {
	if s == nil || s.repo == nil || nodeID <= 0 || (ipv4 == "" && ipv6 == "") {
		return
	}
	publicIPGateOnce.Do(func() { publicIPGate = repo.NewPublicIPReportGate() })
	if !publicIPGate.Allow(nodeID, ipv4, ipv6, time.Now()) {
		return
	}
	filled, err := s.repo.FillNodePublicIPs(nodeID, ipv4, ipv6)
	if err != nil {
		log.Printf("node %d public ip report not applied: %v", nodeID, err)
		return
	}
	if len(filled) > 0 {
		log.Printf("node %d public ip report filled empty %s", nodeID, strings.Join(filled, ","))
	}
}
