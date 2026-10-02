//go:build !linux

package nftables

import "errors"

type Manager struct{}

type RuleState struct{}

type RuleQuota struct {
	MaxConnections      int
	MaxClientIPs        int
	Group               string
	GroupMaxConnections int
	GroupMaxClientIPs   int
}

type CounterResult struct {
	ForwardID    int64  `json:"forward_id"`
	UserID       int64  `json:"user_id"`
	UserTunnelID int64  `json:"user_tunnel_id"`
	Protocol     string `json:"protocol"`
	Port         int    `json:"port"`
	Packets      uint64 `json:"packets"`
	Bytes        uint64 `json:"bytes"`
}

func NewManager() (*Manager, error) {
	return nil, errors.New("nftables not supported on this platform")
}

func (m *Manager) initTable() error {
	return errors.New("nftables not supported on this platform")
}

func (m *Manager) initChains() error {
	return errors.New("nftables not supported on this platform")
}

func (m *Manager) AddRule(forwardID, nodeID, userID, userTunnelID int64, protocol string, port int, target string, speedLimit int, limits ...RuleQuota) error {
	return errors.New("nftables not supported on this platform")
}

func (m *Manager) UpdateRule(forwardID int64, protocol string, port int, target string, speedLimit int, limits ...RuleQuota) error {
	return errors.New("nftables not supported on this platform")
}

func (m *Manager) ReconcileQuota() error { return nil }

func (m *Manager) DeleteRule(forwardID int64, protocol string) error {
	return errors.New("nftables not supported on this platform")
}

func (m *Manager) DeleteRuleWithPort(forwardID int64, protocol string, port int) error {
	return errors.New("nftables not supported on this platform")
}

func (m *Manager) GetCounters() []CounterResult {
	return nil
}

// TrafficDelta is the traffic of one forward and protocol since the previous collection.
type TrafficDelta struct {
	ForwardID     int64
	UserID        int64
	UserTunnelID  int64
	Protocol      string
	Port          int
	UploadBytes   uint64
	DownloadBytes uint64
}

func (m *Manager) CollectTraffic() []TrafficDelta {
	return nil
}

func (m *Manager) RemoveForward(forwardID int64, protocol string, ports []int, terminate bool) error {
	return errors.New("nftables not supported on this platform")
}

func (m *Manager) TerminateConnections(protocol string, port int) (uint, error) {
	return 0, errors.New("nftables not supported on this platform")
}

func (m *Manager) ResetCounters() error {
	return errors.New("nftables not supported on this platform")
}

func CheckNftablesSupport() (bool, error) {
	return false, errors.New("nftables not supported on this platform")
}
