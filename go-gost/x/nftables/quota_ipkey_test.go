//go:build linux

package nftables

import (
	"bytes"
	"net"
	"testing"

	"github.com/go-gost/x/service"
	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Plan 231: conntrack sources are keyed like the 妙妙屋 Core (IPv6 = its /64).
func TestConntrackAttributionKeysIPv6BySlash64(t *testing.T) {
	rules := map[string]*RuleState{
		"61/tcp": {ForwardID: 61, UserID: 9, Protocol: "tcp", Port: 31061, Quota: RuleQuota{Group: "user-9", MaxClientIPs: 2}},
		"62/tcp": {ForwardID: 62, UserID: 9, Protocol: "tcp", Port: 31062, Quota: RuleQuota{Group: "user-9"}},
	}
	flows := []*netlink.ConntrackFlow{
		quotaTestFlow(unix.IPPROTO_TCP, 31061, "2409:8a4c:5c4f:26e1::1"),
		quotaTestFlow(unix.IPPROTO_TCP, 31061, "2409:8a4c:5c4f:26e1::2"),
		quotaTestFlow(unix.IPPROTO_TCP, 31061, "2409:8a4c:5c4f:26e1:aaaa::3"),
		quotaTestFlow(unix.IPPROTO_TCP, 31061, "198.51.100.1"),
		quotaTestFlow(unix.IPPROTO_TCP, 31062, "2409:8a4c:5c4f:26e2::1"),
		quotaTestFlow(unix.IPPROTO_TCP, 31062, "2409:8a4c:5c4f:26e1::1"),
	}
	const a, b = "2409:8a4c:5c4f:26e1::/64", "2409:8a4c:5c4f:26e2::/64"
	usage := attributeQuotaFlows(rules, flows)
	if u := usage[61]; u.connections != 4 || len(u.ips) != 2 || u.ips[a] != 3 || u.ips["198.51.100.1"] != 1 || len(u.addrs[a]) != 3 || u.addrs["198.51.100.1"] != nil {
		t.Fatalf("forward 61 = %+v", u)
	}
	groups := aggregateQuotaGroups(usage, map[int64]*quotaForward{61: {quota: rules["61/tcp"].Quota}, 62: {quota: rules["62/tcp"].Quota}})
	g := groups["user-9"]
	if g.connections != 6 || len(g.ips) != 3 || g.ips[a] != 4 || g.ips[b] != 1 || len(g.addrs[a]) != 3 || g.addrs[a]["2409:8a4c:5c4f:26e1::1"] != 2 {
		t.Fatalf("group = %+v", g)
	}
	// Live attribution (all forwards, every metric report) keeps only keys.
	if live := attributeLiveFlows(rules, flows)[61]; len(live.ips) != 2 || live.addrs != nil {
		t.Fatalf("live = %+v", live)
	}
}

func TestQuotaGateKeysIPv6BySlash64(t *testing.T) {
	q := RuleQuota{MaxClientIPs: 1, Group: "user-9"}
	usage := quotaUsage{connections: 2, ips: map[string]int{"2409:8a4c:5c4f:26e1::/64": 2}}
	state := service.QuotaGroupState{MaxConnections: -1, MaxClientIPs: 1, CountedIPs: 1,
		ClientIPs: map[string]struct{}{"2409:8a4c:5c4f:26e1::/64": {}, "2001:db8:1::/64": {}}}
	gate := chooseQuotaGate(q, usage, state)
	if !gate.ruleIPsFull || !gate.groupIPsFull {
		t.Fatalf("gate = %+v", gate)
	}
	for ip, want := range map[string]bool{
		"2409:8a4c:5c4f:26e1::9":    true,  // same /64 as the active one
		"2409:8a4c:5c4f:26e1:1:2::": true,  // privacy address rotation
		"2409:8a4c:5c4f:26e2::1":    false, // another /64
		"198.51.100.1":              false,
	} {
		if got := quotaGateAllows(gate, usage.ips, state.ClientIPs, ip); got != want {
			t.Errorf("allows(%s) = %v, want %v", ip, got, want)
		}
	}
	// A shared /64 (counted on another node) passes the group gate but not the rule cap.
	if quotaGateAllows(gate, usage.ips, state.ClientIPs, "2001:db8:1::5") {
		t.Error("shared /64 passed a full rule IP cap")
	}
	gate.ruleIPsFull = false
	if !quotaGateAllows(gate, usage.ips, state.ClientIPs, "2001:db8:1::5") {
		t.Error("shared /64 rejected by the group gate")
	}
}

func TestQuotaGateExpressionsMaskIPv6(t *testing.T) {
	set := &nftables.Set{Name: "f1_group6", ID: 7}
	var masks []*expr.Bitwise
	for _, e := range quotaIPGateExpressions(set, "6") {
		if b, ok := e.(*expr.Bitwise); ok && b.Len == 16 { // the ct state check masks 4 bytes
			masks = append(masks, b)
		}
	}
	if len(masks) != 1 || masks[0].Len != 16 || !bytes.Equal(masks[0].Mask, net.ParseIP("ffff:ffff:ffff:ffff::").To16()) || !bytes.Equal(masks[0].Xor, make([]byte, 16)) {
		t.Fatalf("IPv6 gate mask = %+v", masks)
	}
	for _, e := range quotaIPGateExpressions(set, "4") {
		if b, ok := e.(*expr.Bitwise); ok && b.Len != 4 {
			t.Fatal("IPv4 gate must not mask the source")
		}
	}
	if b := quotaIPBytes("2409:8a4c:5c4f:26e1::/64"); !bytes.Equal(b, net.ParseIP("2409:8a4c:5c4f:26e1::").To16()) {
		t.Fatalf("set element = %x", b)
	}
	if b := quotaIPBytes("198.51.100.1"); !bytes.Equal(b, []byte{198, 51, 100, 1}) {
		t.Fatalf("v4 set element = %x", b)
	}
	if ipFamily("2409:8a4c:5c4f:26e1::/64") != "6" || ipFamily("198.51.100.1") != "4" {
		t.Fatal("ipFamily of keys")
	}
}
