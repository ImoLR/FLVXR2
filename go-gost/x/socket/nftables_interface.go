package socket

import (
	"github.com/go-gost/x/nftables"
)

// NftablesManagerInterface defines the interface for nftables manager operations.
type NftablesManagerInterface interface {
	AddRule(forwardID, nodeID, userID, userTunnelID int64, protocol string, port int, target string, speedLimit int, limits ...nftables.RuleQuota) error
	UpdateRule(forwardID int64, protocol string, port int, target string, speedLimit int, limits ...nftables.RuleQuota) error
	ReconcileQuota() error
	DeleteRule(forwardID int64, protocol string) error
	DeleteRuleWithPort(forwardID int64, protocol string, port int) error
	GetCounters() []nftables.CounterResult
	// CollectTraffic returns the per-direction traffic counted since the previous call,
	// including the final traffic of deleted rules.
	CollectTraffic() []nftables.TrafficDelta
	ResetCounters() error
	// RemoveForward removes a forward's rules (identified by forward id, any port) and,
	// with terminate, ends its established connections.
	RemoveForward(forwardID int64, protocol string, ports []int, terminate bool) error
}
