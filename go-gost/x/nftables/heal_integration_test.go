//go:build linux

package nftables

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-gost/x/logsafe"
)

func nftTableExists(t *testing.T, name string) bool {
	t.Helper()
	return exec.Command("nft", "list", "table", "inet", name).Run() == nil
}

func flushRuleset(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("nft", "flush", "ruleset").CombinedOutput(); err != nil {
		t.Fatalf("nft flush ruleset: %v: %s", err, out)
	}
	for _, table := range []string{TableName, AccountingTableName, quotaTableName} {
		if nftTableExists(t, table) {
			t.Fatalf("table %s survived the flush", table)
		}
	}
}

func captureLogs(m *Manager) func() []string {
	var mu sync.Mutex
	var lines []string
	m.mu.Lock()
	m.logs = logsafe.NewRoutineLogger(func(s string) { mu.Lock(); lines = append(lines, s); mu.Unlock() })
	m.mu.Unlock()
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), lines...) }
}

// Something outside the agent removes its nft objects (`nft flush ruleset` from a firewall
// reload, ...). The manager notices on its next traffic collection or quota reconcile and
// rebuilds everything from its in-memory rules: DNAT forwarding, accounting and IP caps work
// again, attempts are spaced by the backoff, and nothing is logged per attempt.
func TestNftSelfHealAfterRulesetFlush(t *testing.T) {
	clientNS, targetNS := requirePeerNamespaces(t)
	m := newIntegrationManager(t)
	logs := captureLogs(m)
	const firstIP, secondIP = "10.231.1.2", "10.231.1.3"
	startPyServer(t, targetNS, echoServerPy, targetIP4, "41031")
	startPyServer(t, targetNS, echoServerPy, targetIP4, "41032")
	if err := m.AddRule(231, 1, 7, 1, "tcp", 31031, targetIP4+":41031", 0, RuleQuota{MaxClientIPs: 1}); err != nil {
		t.Fatal(err)
	}
	if err := m.AddRule(232, 1, 7, 1, "tcp", 31032, targetIP4+":41032", 0); err != nil {
		t.Fatal(err)
	}
	defer m.RemoveForward(231, "tcp", nil, false)
	defer m.RemoveForward(232, "tcp", nil, false)

	t.Run("traffic collection detects and rebuilds", func(t *testing.T) {
		flushRuleset(t)
		if probeQuotaConnection(t, clientNS, firstIP, 31032) {
			t.Fatal("forward still worked without its DNAT rule")
		}
		m.CollectTraffic()
		for _, table := range []string{TableName, AccountingTableName, quotaTableName} {
			if !nftTableExists(t, table) {
				t.Fatalf("table %s not rebuilt", table)
			}
		}
		if len(m.rules) != 2 || len(m.unrestored) != 0 || m.quotaPollStop == nil {
			t.Fatalf("rules %d unrestored %d poll %v", len(m.rules), len(m.unrestored), m.quotaPollStop != nil)
		}
		if !probeQuotaConnection(t, clientNS, firstIP, 31032) {
			t.Fatal("uncapped forward not restored")
		}
		a := holdQuotaConnection(t, clientNS, firstIP, 31031)
		if err := m.ReconcileQuota(); err != nil {
			t.Fatal(err)
		}
		if probeQuotaConnection(t, clientNS, secondIP, 31031) {
			t.Fatal("IP cap not restored: second source IP passed")
		}
		a.ping(t)
		if collected := collectFor(t, m, 232, "tcp"); collected.UploadBytes == 0 {
			t.Fatal("accounting not restored")
		}
	})

	t.Run("repeated removal waits for the backoff", func(t *testing.T) {
		flushRuleset(t)
		m.CollectTraffic()
		if nftTableExists(t, TableName) {
			t.Fatal("rebuilt again before the backoff elapsed")
		}
		base := time.Now()
		m.mu.Lock()
		m.now = func() time.Time { return base.Add(3 * time.Second) }
		m.mu.Unlock()
		m.CollectTraffic()
		if !nftTableExists(t, TableName) || !nftTableExists(t, quotaTableName) {
			t.Fatal("not rebuilt after the backoff")
		}
		m.mu.Lock()
		m.now = time.Now
		m.healNext = time.Time{}
		m.mu.Unlock()
	})

	t.Run("quota gate change detects and rebuilds", func(t *testing.T) {
		a := holdQuotaConnection(t, clientNS, firstIP, 31031)
		if err := m.ReconcileQuota(); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("nft", "delete", "table", "inet", quotaTableName).CombinedOutput(); err != nil {
			t.Fatalf("delete quota table: %v: %s", err, out)
		}
		// Without its objects the IP cap is not enforced: a second source IP gets through.
		b := holdQuotaConnection(t, clientNS, secondIP, 31031)
		if err := m.ReconcileQuota(); err != nil {
			t.Fatalf("reconcile after the quota table vanished: %v", err)
		}
		if !nftTableExists(t, quotaTableName) {
			t.Fatal("quota table not rebuilt")
		}
		b.close()
		time.Sleep(3 * time.Second) // the RST flow leaves conntrack
		for i := 0; i < 2; i++ {
			if err := m.ReconcileQuota(); err != nil {
				t.Fatal(err)
			}
		}
		if probeQuotaConnection(t, clientNS, secondIP, 31031) {
			t.Fatal("IP cap not enforced after the rebuild")
		}
		a.ping(t)
	})

	if lines := logs(); len(lines) != 0 {
		t.Fatalf("successful rebuilds must not log failures: %q", lines)
	}
}

// The reporter replaces its manager on every WebSocket reconnect. The replaced manager must
// stop its quota poll for good and must not touch (or "heal") the tables the new manager owns.
func TestReplacedManagerStopsItsQuotaPoll(t *testing.T) {
	_, targetNS := requirePeerNamespaces(t)
	startPyServer(t, targetNS, echoServerPy, targetIP4, "41033")
	old, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	logs := captureLogs(old)
	if err := old.AddRule(233, 1, 7, 1, "tcp", 31033, targetIP4+":41033", 0, RuleQuota{MaxClientIPs: 1}); err != nil {
		t.Fatal(err)
	}
	old.mu.Lock()
	done := old.quotaPollDone
	old.mu.Unlock()
	if done == nil {
		t.Fatal("quota poll not started")
	}

	old.Close()
	select {
	case <-done:
	default:
		t.Fatal("replaced manager's quota poll still running")
	}
	current := newIntegrationManager(t) // clears the old manager's tables
	if nftTableExists(t, quotaTableName) {
		t.Fatal("new manager kept the old quota table")
	}
	time.Sleep(2500 * time.Millisecond)
	if err := old.ReconcileQuota(); err != nil {
		t.Fatal(err)
	}
	old.CollectTraffic()
	if nftTableExists(t, quotaTableName) || len(current.rules) != 0 {
		t.Fatal("closed manager re-created objects behind the new manager")
	}
	for _, line := range logs() {
		if strings.Contains(line, "失败") {
			t.Fatalf("closed manager logged failures: %q", line)
		}
	}
}
