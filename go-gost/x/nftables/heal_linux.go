//go:build linux

package nftables

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/go-gost/x/logsafe"
	"github.com/go-gost/x/service"
	"golang.org/x/sys/unix"
)

const (
	// healMaxBackoff caps the spacing of self-heal attempts; healQuietReset restarts the
	// spacing at its minimum after this long without an attempt.
	healMaxBackoff = 5 * time.Minute
	healQuietReset = 10 * time.Minute
)

var (
	errManagerClosed   = errors.New("nftables manager closed (replaced by a newer one)")
	errRulesUnrestored = errors.New("nft rules waiting to be restored")
)

// isMissingObject reports whether a netlink error says that a table, chain, set or rule the
// manager relies on does not exist (any more).
func isMissingObject(err error) bool {
	return errors.Is(err, unix.ENOENT)
}

// Close stops the manager's background quota poll and releases its quota groups. It never
// touches the kernel: the manager created after it (NewManager clears and re-creates the
// tables) owns the nft objects from then on. A closed manager no longer reconciles, heals or
// adds rules. Close waits until the poll goroutine has exited.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	stop, done := m.quotaPollStop, m.quotaPollDone
	m.quotaPollStop, m.quotaPollDone = nil, nil
	groups := make(map[string]struct{})
	for _, qf := range m.quotaForwards {
		if qf.quota.Group != "" {
			groups[qf.quota.Group] = struct{}{}
		}
	}
	for group := range groups {
		service.SetNftQuotaGroupUsage(group, 0, nil)
	}
	for _, qf := range m.quotaForwards {
		qf.group.Detach()
	}
	m.quotaForwards = make(map[int64]*quotaForward)
	m.unrestored = make(map[string]*RuleState)
	m.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}

// healLocked re-creates every nft object of the manager from its in-memory rules after they
// disappeared from the kernel behind its back (`nft flush ruleset`, a firewall reload, ...).
// Attempts are spaced by an exponential backoff (2 s … 5 min, restarting after 10 quiet
// minutes), so a host that keeps removing them, or a kernel that refuses them, costs at most
// one rebuild per backoff step. Failures are logged through the throttled logger and say
// that forwarding / IP caps are not in effect.
func (m *Manager) healLocked(cause error) error {
	if m.closed {
		return nil
	}
	now := m.now()
	if now.Before(m.healNext) {
		return fmt.Errorf("nft 对象缺失，%s 后重建: %w", m.healNext.Sub(now).Round(time.Second), cause)
	}
	if !m.healLast.IsZero() && now.Sub(m.healLast) >= healQuietReset {
		m.healBackoff.Success()
	}
	m.healLast = now
	m.healNext = now.Add(m.healBackoff.Failure())
	restored, total, err := m.rebuildLocked()
	if err != nil {
		m.logs.Record("nft self-heal", "node", fmt.Sprintf(
			"nft 对象缺失（%s），按内存状态重建失败（%d/%d 条规则已恢复），未恢复的 nftables 转发与 IP 限制当前未生效: %s",
			logsafe.OneLine(cause), restored, total, logsafe.OneLine(err)))
		return fmt.Errorf("nft 对象重建失败，IP 限制当前未生效: %w", err)
	}
	m.logs.Record("nft self-heal", "node", "")
	fmt.Printf("🔧 nft 对象缺失（%s），已按内存状态重建 %d 条规则\n", logsafe.OneLine(cause), restored)
	return nil
}

// rebuildLocked resets the manager's kernel objects the way NewManager does and re-adds
// every known rule (installed or still waiting for a restore). Rules that cannot be added
// stay in m.unrestored for the next attempt; the panel replacing or deleting such a rule
// drops it from there.
func (m *Manager) rebuildLocked() (restored, total int, err error) {
	want := make(map[string]*RuleState, len(m.rules)+len(m.unrestored))
	for key, rs := range m.unrestored {
		want[key] = rs
	}
	for key, rs := range m.rules {
		want[key] = rs
	}
	total = len(want)
	groups := make(map[string]struct{})
	for _, qf := range m.quotaForwards {
		if qf.quota.Group != "" {
			groups[qf.quota.Group] = struct{}{}
		}
		qf.group.Detach()
	}
	m.quotaForwards = make(map[int64]*quotaForward)
	m.quotaTable = nil
	m.rules = make(map[string]*RuleState)
	m.last = make(map[string]counterValue)
	m.liveUsage = nil
	m.unrestored = want
	defer func() {
		for group := range groups {
			inUse := false
			for _, qf := range m.quotaForwards {
				inUse = inUse || qf.quota.Group == group
			}
			if !inUse {
				service.SetNftQuotaGroupUsage(group, 0, nil)
			}
		}
		m.stopQuotaPollIfIdleLocked()
	}()

	if err := m.initTable(); err != nil {
		return 0, total, fmt.Errorf("init table: %w", err)
	}
	if err := m.clearStaleRules(); err != nil {
		return 0, total, fmt.Errorf("clear stale rules: %w", err)
	}
	if err := m.clearStaleQuota(); err != nil {
		return 0, total, fmt.Errorf("clear stale quota: %w", err)
	}
	if err := m.initAccounting(); err != nil {
		fmt.Printf("⚠️ nftables 流量统计/限速重建失败，nftables 转发将不计流量、不限速: %v\n", logsafe.OneLine(err))
		m.acctTable = nil
		m.portMaps = nil
	}
	keys := make([]string, 0, len(want))
	for key := range want {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var errs []error
	for _, key := range keys {
		rs := want[key]
		if err := m.addRuleLocked(rs.ForwardID, rs.NodeID, rs.UserID, rs.UserTunnelID, rs.Protocol, rs.Port, rs.Target, rs.SpeedLimit, rs.Quota); err != nil {
			errs = append(errs, fmt.Errorf("forward %d/%s: %w", rs.ForwardID, rs.Protocol, err))
			continue
		}
		delete(m.unrestored, key)
		restored++
	}
	return restored, total, errors.Join(errs...)
}
