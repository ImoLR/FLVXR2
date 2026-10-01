//go:build linux

package nftables

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

func requireNFTIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("FLVXR2_NFT_INTEGRATION") != "1" {
		t.Skip("set FLVXR2_NFT_INTEGRATION=1 inside an isolated network namespace")
	}
}

func removeTestTable(t *testing.T, manager *Manager) {
	t.Helper()
	manager.conn.DelTable(manager.table)
	if manager.acctTable != nil {
		manager.conn.DelTable(manager.acctTable)
	}
	if err := manager.conn.Flush(); err != nil {
		t.Errorf("remove test table: %v", err)
	}
	manager.conn.CloseLasting()
}

func TestDNATMasqueradeRuleIsScoped(t *testing.T) {
	rule := &nftables.Rule{Exprs: newDNATMasqueradeExpressions()}
	if !isDNATMasqueradeRule(rule) {
		t.Fatal("expected generated masquerade rule to require DNAT status")
	}

	if len(rule.Exprs) != 4 {
		t.Fatalf("expected status, mask, compare and masquerade expressions, got %d", len(rule.Exprs))
	}
	mask, ok := rule.Exprs[1].(*expr.Bitwise)
	if !ok {
		t.Fatalf("expected bitwise DNAT mask, got %T", rule.Exprs[1])
	}
	if got := binaryutil.NativeEndian.Uint32(mask.Mask); got != conntrackStatusDNAT {
		t.Fatalf("expected DNAT status mask %#x, got %#x", conntrackStatusDNAT, got)
	}
}

func TestUnconditionalMasqueradeRuleIsRejected(t *testing.T) {
	rule := &nftables.Rule{Exprs: []expr.Any{&expr.Masq{}}}
	if isDNATMasqueradeRule(rule) {
		t.Fatal("unconditional masquerade must be migrated")
	}
}

func TestManagerDoesNotMasqueradeLoopbackTraffic(t *testing.T) {
	requireNFTIntegration(t)

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("initialize nftables manager: %v", err)
	}
	defer removeTestTable(t, manager)
	postrouting := &nftables.Chain{Name: PostroutingChain, Table: manager.table}
	rules, err := manager.conn.GetRules(manager.table, postrouting)
	if err != nil {
		t.Fatalf("read generated postrouting rules: %v", err)
	}
	if len(rules) != 1 || !isDNATMasqueradeRule(rules[0]) {
		t.Fatalf("expected one DNAT-scoped masquerade rule, got %#v", rules)
	}

	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.53"), Port: 0})
	if err != nil {
		t.Fatalf("listen on loopback DNS stub address: %v", err)
	}
	defer server.Close()
	if err := server.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	client, err := net.DialUDP("udp4", nil, server.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("dial loopback DNS stub address: %v", err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("dns-query")); err != nil {
		t.Fatalf("write loopback datagram: %v", err)
	}

	buf := make([]byte, 64)
	_, source, err := server.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read loopback datagram: %v", err)
	}
	if !source.IP.IsLoopback() {
		t.Fatalf("host-local traffic was masqueraded: source=%s", source)
	}
}

func TestManagerMigratesLegacyMasquerade(t *testing.T) {
	requireNFTIntegration(t)

	seed := &nftables.Conn{}
	table := seed.AddTable(&nftables.Table{Name: TableName, Family: TableFamily})
	chain := seed.AddChain(&nftables.Chain{
		Name:     PostroutingChain,
		Table:    table,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: nftables.ChainPriorityNATSource,
		Type:     nftables.ChainTypeNAT,
	})
	seed.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: []expr.Any{&expr.Masq{}}})
	if err := seed.Flush(); err != nil {
		t.Fatalf("seed legacy masquerade: %v", err)
	}
	seed.CloseLasting()

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("initialize nftables manager: %v", err)
	}
	defer removeTestTable(t, manager)
	rules, err := manager.conn.GetRules(manager.table, &nftables.Chain{Name: PostroutingChain, Table: manager.table})
	if err != nil {
		t.Fatalf("read migrated postrouting rules: %v", err)
	}
	if len(rules) != 1 || !isDNATMasqueradeRule(rules[0]) {
		t.Fatalf("legacy masquerade was not replaced by one DNAT-scoped rule: %#v", rules)
	}
}

func TestManagerInstallsIPv4AndIPv6TCPAndUDPRules(t *testing.T) {
	requireNFTIntegration(t)

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("initialize nftables manager: %v", err)
	}
	defer removeTestTable(t, manager)

	tests := []struct {
		id       int64
		protocol string
		port     int
		target   string
		family   uint32
	}{
		{id: 1, protocol: "tcp", port: 31001, target: "192.0.2.10:41001", family: unix.NFPROTO_IPV4},
		{id: 2, protocol: "udp", port: 31002, target: "192.0.2.11:41002", family: unix.NFPROTO_IPV4},
		{id: 3, protocol: "tcp", port: 31003, target: "[2001:db8::10]:41003", family: unix.NFPROTO_IPV6},
		{id: 4, protocol: "udp", port: 31004, target: "[2001:db8::11]:41004", family: unix.NFPROTO_IPV6},
	}
	for _, tc := range tests {
		if err := manager.AddRule(tc.id, 1, 1, 1, tc.protocol, tc.port, tc.target, 0); err != nil {
			t.Fatalf("add %s rule for %s: %v", tc.protocol, tc.target, err)
		}
	}

	rules, err := manager.conn.GetRules(manager.table, &nftables.Chain{Name: PreroutingChain, Table: manager.table})
	if err != nil {
		t.Fatalf("read prerouting rules: %v", err)
	}
	for _, tc := range tests {
		proto := byte(unix.IPPROTO_TCP)
		if tc.protocol == "udp" {
			proto = byte(unix.IPPROTO_UDP)
		}
		port := []byte{byte(tc.port >> 8), byte(tc.port)}
		found := false
		for _, rule := range rules {
			if !matchProtoInRule(rule, proto) || !matchPortInRule(rule, port) {
				continue
			}
			for _, item := range rule.Exprs {
				if nat, ok := item.(*expr.NAT); ok && nat.Type == expr.NATTypeDestNAT && nat.Family == tc.family {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("missing %s DNAT rule on port %d for family %d", tc.protocol, tc.port, tc.family)
		}
	}

	postrouting, err := manager.conn.GetRules(manager.table, &nftables.Chain{Name: PostroutingChain, Table: manager.table})
	if err != nil {
		t.Fatalf("read postrouting rules: %v", err)
	}
	if len(postrouting) != 1 || !isDNATMasqueradeRule(postrouting[0]) {
		t.Fatalf("return path is not covered by one DNAT-scoped masquerade rule: %#v", postrouting)
	}
}
