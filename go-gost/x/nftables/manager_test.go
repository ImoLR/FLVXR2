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
)

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
	if os.Getenv("FLVXR2_NFT_INTEGRATION") != "1" {
		t.Skip("set FLVXR2_NFT_INTEGRATION=1 inside an isolated network namespace")
	}

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("initialize nftables manager: %v", err)
	}
	defer manager.conn.CloseLasting()
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
