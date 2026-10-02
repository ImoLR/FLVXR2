//go:build linux

package nftables

import (
	"net"
	"testing"

	"github.com/go-gost/x/service"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func quotaTestFlow(proto uint8, port uint16, ip string) *netlink.ConntrackFlow {
	return &netlink.ConntrackFlow{
		Forward: netlink.IPTuple{Protocol: proto, SrcIP: net.ParseIP(ip), SrcPort: 40000, DstIP: net.ParseIP("10.231.1.1"), DstPort: port},
		Reverse: netlink.IPTuple{Protocol: proto, SrcIP: net.ParseIP("10.231.2.2"), SrcPort: 42000, DstIP: net.ParseIP(ip), DstPort: 40000},
	}
}

func TestConntrackAttributionAcrossPortsProtocolsAndUser(t *testing.T) {
	rules := map[string]*RuleState{
		"41/tcp": {ForwardID: 41, UserID: 7, Protocol: "tcp", Port: 31001, Quota: RuleQuota{Group: "user-7"}},
		"41/udp": {ForwardID: 41, UserID: 7, Protocol: "udp", Port: 31001, Quota: RuleQuota{Group: "user-7"}},
		"42/tcp": {ForwardID: 42, UserID: 7, Protocol: "tcp", Port: 31002, Quota: RuleQuota{Group: "user-7"}},
	}
	other := quotaTestFlow(unix.IPPROTO_TCP, 31001, "198.51.100.9")
	other.Reverse.SrcIP = net.ParseIP("10.231.1.1")
	other.Reverse.SrcPort = 31001
	usage := attributeQuotaFlows(rules, []*netlink.ConntrackFlow{
		quotaTestFlow(unix.IPPROTO_TCP, 31001, "198.51.100.1"),
		quotaTestFlow(unix.IPPROTO_UDP, 31001, "198.51.100.1"),
		quotaTestFlow(unix.IPPROTO_TCP, 31002, "198.51.100.1"),
		quotaTestFlow(unix.IPPROTO_TCP, 31002, "198.51.100.2"),
		quotaTestFlow(unix.IPPROTO_TCP, 31999, "198.51.100.3"), other,
	})
	if usage[41].connections != 2 || len(usage[41].ips) != 1 || usage[42].connections != 2 || len(usage[42].ips) != 2 {
		t.Fatalf("forward attribution: %+v", usage)
	}
	groups := aggregateQuotaGroups(usage, map[int64]*quotaForward{
		41: {quota: RuleQuota{Group: "user-7"}}, 42: {quota: RuleQuota{Group: "user-7"}},
	})
	if groups["user-7"].connections != 4 || len(groups["user-7"].ips) != 2 || groups["user-7"].ips["198.51.100.1"] != 3 {
		t.Fatalf("user attribution: %+v", groups)
	}
}

func TestQuotaGateDecisions(t *testing.T) {
	usage := quotaUsage{connections: 2, ips: map[string]int{"192.0.2.1": 2}}
	state := service.QuotaGroupState{MaxConnections: -1, MaxClientIPs: -1, Connections: 2, ClientIPs: map[string]struct{}{"192.0.2.1": {}}}
	q := RuleQuota{MaxClientIPs: 1, Group: "user-7"}
	gate := chooseQuotaGate(q, usage, state)
	if !gate.ruleIPsFull || !quotaGateAllows(gate, usage.ips, state.ClientIPs, "192.0.2.1") || quotaGateAllows(gate, usage.ips, state.ClientIPs, "192.0.2.2") {
		t.Fatalf("forward IP gate = %+v", gate)
	}
	q.MaxClientIPs = 0
	state.MaxConnections = 2
	gate = chooseQuotaGate(q, usage, state)
	if !gate.groupConnectionsFull || quotaGateAllows(gate, usage.ips, state.ClientIPs, "192.0.2.1") {
		t.Fatalf("full user connection pool = %+v", gate)
	}
	state.MaxConnections = -1
	state.MaxClientIPs = 1
	gate = chooseQuotaGate(q, usage, state)
	if !gate.groupIPsFull || !quotaGateAllows(gate, usage.ips, state.ClientIPs, "192.0.2.1") || quotaGateAllows(gate, usage.ips, state.ClientIPs, "192.0.2.2") {
		t.Fatalf("full user IP pool = %+v", gate)
	}
	state.MaxClientIPs = 0
	gate = chooseQuotaGate(q, quotaUsage{}, service.QuotaGroupState{MaxConnections: -1, MaxClientIPs: 0})
	if !gate.groupIPsFull || quotaGateAllows(gate, nil, nil, "192.0.2.1") {
		t.Fatalf("zero pool must be full: %+v", gate)
	}
	state.MaxClientIPs = -1
	gate = chooseQuotaGate(q, usage, state)
	if gate.groupConnectionsFull || gate.groupIPsFull {
		t.Fatalf("-1 pool must be unlimited: %+v", gate)
	}
}

func TestLiveConntrackAttributionIncludesUncappedForward(t *testing.T) {
	rules := map[string]*RuleState{
		"51/tcp": {ForwardID: 51, Protocol: "tcp", Port: 31001},
		"51/udp": {ForwardID: 51, Protocol: "udp", Port: 31001},
	}
	flows := []*netlink.ConntrackFlow{
		quotaTestFlow(unix.IPPROTO_TCP, 31001, "198.51.100.1"),
		quotaTestFlow(unix.IPPROTO_UDP, 31001, "198.51.100.1"),
		quotaTestFlow(unix.IPPROTO_TCP, 31001, "2001:db8::1"),
	}
	if got := attributeQuotaFlows(rules, flows); len(got) != 0 {
		t.Fatalf("quota-only attribution unexpectedly included unlimited rule: %v", got)
	}
	got := attributeLiveFlows(rules, flows)[51]
	if got.connections != 3 || got.ips["198.51.100.1"] != 2 || got.ips["2001:db8::1"] != 1 {
		t.Fatalf("live attribution = %+v", got)
	}
}
