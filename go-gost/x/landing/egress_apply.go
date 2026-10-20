package landing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// EgressStatus is reported with every landing status. Errors never mark the
// inbound state as failed: counters and deletions keep working.
type EgressStatus struct {
	State    string   `json:"state"` // none | applied | restart_required | error
	Error    string   `json:"error,omitempty"`
	Ready    bool     `json:"ready"` // the running Core has the routing API
	Applied  string   `json:"applied,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Withheld []string `json:"withheld,omitempty"`
}

var errRestartRequired = errors.New("需要重启落地 Core 才能启用出站与路由")

type egressCache struct {
	key, epoch string
	at         time.Time
	plan       renderedPlan
	err        error
}

// detectLocalAddrs returns the kernel-selected source address per family.
// A UDP connect only consults the routing table; no packet is sent.
func detectLocalAddrs() localAddrs {
	var l localAddrs
	probe := func(network, target string) netip.Addr {
		c, err := net.DialTimeout(network, target, time.Second)
		if err != nil {
			return netip.Addr{}
		}
		defer c.Close()
		a := c.LocalAddr().(*net.UDPAddr).AddrPort().Addr().Unmap()
		if !a.IsValid() || a.IsLoopback() || a.IsUnspecified() || a.IsLinkLocalUnicast() {
			return netip.Addr{}
		}
		return a
	}
	l.v4 = probe("udp4", "1.1.1.1:53")
	l.v6 = probe("udp6", "[2606:4700:4700::1111]:53")
	return l
}

func hashJSON(v any) string {
	sum := sha256.Sum256([]byte(canonical(v)))
	return hex.EncodeToString(sum[:])
}

// effectiveEgress merges chain nodes into the server plan: each chain node
// gets its own upstream outbound and one rule right after the safety rule, so
// neither the server's routing profile nor the default outbound applies to
// it. Without chain nodes the plan is returned unchanged.
func (m *Manager) effectiveEgress() *Egress {
	plan := m.desired.Egress
	if !m.desired.Enabled {
		return nil
	}
	var chains []Node
	for _, n := range m.desired.Nodes {
		if n.Chain != nil {
			chains = append(chains, n)
		}
	}
	if len(chains) == 0 {
		return plan
	}
	sort.Slice(chains, func(i, j int) bool { return chains[i].ID < chains[j].ID })
	merged := &Egress{Version: EgressVersion}
	var rules []EgressRule
	for _, n := range chains {
		o := *n.Chain
		o.Tag = chainOutboundTag(n.ID)
		merged.Outbounds = append(merged.Outbounds, o)
		rules = append(rules, EgressRule{Name: "chain", InboundTags: []string{n.InboundTag}, Outbound: o.Tag})
	}
	if plan != nil {
		merged.Version = plan.Version
		merged.AllowCoreRestart = plan.AllowCoreRestart
		merged.Outbounds = append(merged.Outbounds, plan.Outbounds...)
		rules = append(rules, plan.Rules...)
		merged.ExemptSources = plan.ExemptSources
	}
	merged.Rules = rules
	return merged
}

// coveredTags lists inbound tags whose routing depends on the plan.
func (e *Egress) coveredTags() map[string]bool {
	out := map[string]bool{}
	if !e.active() {
		return out
	}
	for _, r := range e.Rules {
		for _, t := range r.InboundTags {
			out[t] = true
		}
	}
	return out
}

func (e *Egress) needsLocal() bool {
	for _, o := range e.Outbounds {
		if strings.HasPrefix(o.IPVersion, "force_") || (o.Type == "wireguard" && o.WireGuard != nil && o.WireGuard.Endpoint == "") {
			return true
		}
	}
	return false
}

// checkUpstream resolves a domain upstream once per apply. Any private,
// loopback or metadata answer disables the outbound.
func checkUpstream(ctx context.Context, o EgressOutbound) error {
	host := o.Server
	if o.Type == "wireguard" && o.WireGuard != nil && o.WireGuard.Endpoint != "" {
		host, _, _ = net.SplitHostPort(o.WireGuard.Endpoint)
	}
	if host == "" {
		return nil
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if isBlockedAddr(a) {
			return fmt.Errorf("上游地址是内网或保留地址")
		}
		return nil
	}
	child, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(child, "ip", host)
	if err != nil {
		// Unresolvable now: the Core retries at dial time with the same checks
		// applied again on the next apply. Do not block unrelated rules.
		return nil
	}
	for _, a := range addrs {
		if isBlockedAddr(a) {
			return fmt.Errorf("上游域名解析到内网或保留地址")
		}
	}
	return nil
}

// renderedPlan is the Core-side form: outbound tags carry a content hash so a
// changed outbound (including a new local address) gets a new tag and the swap
// is add → replace rules → remove, never leaving a rule without its outbound.
type renderedPlan struct {
	outbounds map[string]string // core tag -> canonical JSON
	rules     []any
	warnings  []string
	hash      string
	covered   map[string]bool
}

func (m *Manager) renderEgress(ctx context.Context, e *Egress) (renderedPlan, error) {
	p := renderedPlan{outbounds: map[string]string{}, covered: e.coveredTags()}
	if !e.active() {
		p.rules, _ = renderRules(nil, nil, nil)
		p.hash = hashJSON(map[string]any{"rules": p.rules})
		return p, nil
	}
	local := localAddrs{}
	if e.needsLocal() {
		local = m.localAddrs()
	}
	tagMap := map[string]string{"direct": "direct", "block": "block"}
	for _, o := range e.Outbounds {
		var cfg map[string]any
		if err := checkUpstream(ctx, o); err != nil {
			cfg = blackhole(o.Tag)
			p.warnings = append(p.warnings, o.Tag+": "+err.Error())
		} else {
			var warn string
			cfg, warn = o.render(local)
			if warn != "" {
				p.warnings = append(p.warnings, warn)
			}
		}
		raw := canonical(cfg)
		sum := sha256.Sum256([]byte(raw))
		coreTag := o.Tag + "-" + hex.EncodeToString(sum[:4])
		cfg["tag"] = coreTag
		p.outbounds[coreTag] = canonical(cfg)
		tagMap[o.Tag] = coreTag
	}
	if files := e.geoFiles(); len(files) > 0 {
		if err := m.ensureGeoData(ctx, files); err != nil {
			return p, err
		}
	}
	mapped := *e
	mapped.Rules = make([]EgressRule, len(e.Rules))
	for i, r := range e.Rules {
		r.Outbound = tagMap[r.Outbound]
		mapped.Rules[i] = r
	}
	geoIP := filepath.Join(m.dir, "geoip.dat")
	rules, err := renderRules(&mapped, func(code string) ([]netip.Prefix, error) { return readGeoIP(geoIP, code) }, &m.sources)
	if err != nil {
		m.sources.next = nil
		return p, err
	}
	m.sources.rotate()
	p.rules = rules
	p.hash = hashJSON(map[string]any{"outbounds": p.outbounds, "rules": rules})
	return p, nil
}

func (m *Manager) localAddrs() localAddrs {
	if m.detectLocal != nil {
		return m.detectLocal()
	}
	return detectLocalAddrs()
}

// routingReady probes RoutingService once per Core epoch. Cores started with
// a pre-egress config lack it and need one restart to gain hot rule updates.
func (m *Manager) routingReady(ctx context.Context) bool {
	if m.routingEpoch == m.epoch && m.epoch != "" {
		return m.routingOK
	}
	_, err := m.api(ctx, "lsrules")
	m.routingEpoch, m.routingOK = m.epoch, err == nil
	return m.routingOK
}

func (m *Manager) listOutboundTags(ctx context.Context) (map[string]bool, error) {
	raw, err := m.api(ctx, "lso")
	if err != nil {
		return nil, err
	}
	var listed struct {
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("invalid landing outbound list")
	}
	out := map[string]bool{}
	for _, o := range listed.Outbounds {
		out[o.Tag] = true
	}
	return out, nil
}

// applyEgress makes the running Core match the plan. It returns the possibly
// new snapshot (after an allowed one-time restart) and the error state; on
// error the previous rules stay in place.
func (m *Manager) applyEgress(ctx context.Context, snapshot coreSnapshot) (coreSnapshot, error) {
	plan := m.effectiveEgress()
	active := plan.active()
	m.egress = EgressStatus{State: "none"}
	if m.apiPort == 0 {
		return snapshot, nil
	}
	ready := m.routingReady(ctx)
	m.egress.Ready = ready
	if !active && m.applied.Egress == "" && len(m.applied.Outbounds) == 0 {
		return snapshot, nil
	}
	if !ready {
		if !active {
			// An old Core cannot hold our rules; nothing to remove.
			m.applied.Egress, m.applied.Outbounds, m.applied.EgressTags = "", nil, nil
			return snapshot, nil
		}
		if plan.AllowCoreRestart && m.restartedRevision != m.desired.Revision {
			m.restartedRevision = m.desired.Revision
			next, err := m.restartCore(ctx, snapshot)
			if err != nil {
				m.egress = EgressStatus{State: "error", Error: "重启落地 Core 失败：" + err.Error()}
				return snapshot, err
			}
			snapshot = next
			m.epoch = epochOf(snapshot)
			m.applied = appliedState{Epoch: m.epoch, Inbounds: map[string]string{}}
			ready = m.routingReady(ctx)
			m.egress.Ready = ready
		}
		if !ready {
			m.egress = EgressStatus{State: "restart_required", Error: errRestartRequired.Error()}
			return snapshot, errRestartRequired
		}
	}
	// Rendering resolves upstream names and probes local addresses; reuse the
	// result for 5 minutes (errors for 1 minute) instead of every 10 s tick.
	key := hashJSON(plan)
	c := m.egressCache
	if c.key != key || c.epoch != m.epoch || (c.err == nil && time.Since(c.at) > 5*time.Minute) || (c.err != nil && time.Since(c.at) > time.Minute) {
		rendered, err := m.renderEgress(ctx, plan)
		c = egressCache{key: key, epoch: m.epoch, at: time.Now(), plan: rendered, err: err}
		m.egressCache = c
	}
	if c.err != nil {
		m.egress = EgressStatus{State: "error", Ready: true, Error: c.err.Error()}
		return snapshot, c.err
	}
	rendered := c.plan
	m.egress.Warnings = rendered.warnings
	if rendered.hash == m.applied.Egress {
		m.egress.State, m.egress.Applied = "applied", rendered.hash
		if !active {
			m.egress.State = "none"
		}
		return snapshot, nil
	}
	present, err := m.listOutboundTags(ctx)
	if err != nil {
		m.egress = EgressStatus{State: "error", Ready: true, Error: "读取落地出站失败"}
		return snapshot, err
	}
	var add []any
	tags := make([]string, 0, len(rendered.outbounds))
	for tag := range rendered.outbounds {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		if !present[tag] {
			add = append(add, json.RawMessage(rendered.outbounds[tag]))
		}
	}
	if len(add) > 0 {
		if err := m.apiWithFile(ctx, "ado", "apply-outbounds.json", map[string]any{"outbounds": add}); err != nil {
			m.egress = EgressStatus{State: "error", Ready: true, Error: "添加落地出站失败"}
			return snapshot, err
		}
	}
	if err := m.apiWithFile(ctx, "adrules", "apply-rules.json", map[string]any{"routing": map[string]any{"rules": rendered.rules}}); err != nil {
		m.egress = EgressStatus{State: "error", Ready: true, Error: "更新落地路由规则失败"}
		return snapshot, err
	}
	var stale []string
	for tag := range present {
		if (strings.HasPrefix(tag, "flvxo-") || strings.HasPrefix(tag, "flvxc-")) && rendered.outbounds[tag] == "" {
			stale = append(stale, tag)
		}
	}
	cleaned := true
	if len(stale) > 0 {
		sort.Strings(stale)
		if _, err := m.api(ctx, "rmo", stale...); err != nil {
			// Unreferenced outbounds are harmless; the next reconcile retries.
			cleaned = false
			m.egress.Warnings = append(m.egress.Warnings, "旧出站清理失败，将在下次同步时重试")
		}
	}
	covered := make([]string, 0, len(rendered.covered))
	for tag := range rendered.covered {
		covered = append(covered, tag)
	}
	sort.Strings(covered)
	m.applied.Egress, m.applied.Outbounds, m.applied.EgressTags = rendered.hash, rendered.outbounds, covered
	if !active {
		m.applied.Egress, m.applied.Outbounds, m.applied.EgressTags = "", nil, nil
	}
	if !cleaned {
		// Force another pass; keep a non-empty outbound map so an inactive plan
		// is not skipped before the stale tags are gone.
		m.applied.Egress = ""
		if len(m.applied.Outbounds) == 0 {
			m.applied.Outbounds = map[string]string{}
			for _, tag := range stale {
				m.applied.Outbounds[tag] = "stale"
			}
		}
	}
	if err := m.writeJSON("applied.json", m.applied); err != nil {
		return snapshot, err
	}
	m.egress.State, m.egress.Applied = "applied", rendered.hash
	if !active {
		m.egress.State, m.egress.Applied = "none", ""
	}
	return snapshot, nil
}

// withheldTags: when the new plan failed, nodes newly covered by it stay
// offline rather than leaving through the default outbound (fail closed).
// Nodes the previous plan in this epoch already covered keep its rules.
func (m *Manager) withheldTags() map[string]bool {
	out := map[string]bool{}
	if m.egress.State != "error" && m.egress.State != "restart_required" {
		return out
	}
	old := map[string]bool{}
	for _, t := range m.applied.EgressTags {
		old[t] = true
	}
	covered := m.desired.Egress.coveredTags()
	for _, n := range m.desired.Nodes {
		if n.Chain != nil {
			covered[n.InboundTag] = true
		}
	}
	for t := range covered {
		if !old[t] {
			out[t] = true
		}
	}
	// A chain node without its applied upstream rule would leave through the
	// default outbound: always withhold it, even if covered before.
	for _, n := range m.desired.Nodes {
		if n.Chain != nil && m.applied.Outbounds[chainCoreTagPrefix(m.applied.Outbounds, n.ID)] == "" {
			out[n.InboundTag] = true
		}
	}
	return out
}

// chainCoreTagPrefix finds the applied Core tag of a chain node's upstream
// (tag + content hash suffix); "" when none is applied in this epoch.
func chainCoreTagPrefix(applied map[string]string, id int64) string {
	prefix := chainOutboundTag(id) + "-"
	for tag := range applied {
		if strings.HasPrefix(tag, prefix) {
			return tag
		}
	}
	return ""
}

func epochOf(s coreSnapshot) string { return fmt.Sprintf("%d", s.StartedAt.UnixNano()) }

// restartCore is used only for the confirmed one-time switch to the egress
// base config; normal plan changes are hot.
func (m *Manager) restartCore(ctx context.Context, old coreSnapshot) (coreSnapshot, error) {
	if m.direct {
		if m.proc == nil {
			return old, fmt.Errorf("测试模式下 Core 不由节点程序管理")
		}
		_ = m.proc.Kill()
		m.proc = nil
		if err := m.start(ctx); err != nil {
			return old, err
		}
	} else if err := exec.CommandContext(ctx, "systemctl", "restart", unitName).Run(); err != nil {
		return old, err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if s, err := m.snapshot(ctx); err == nil && !s.StartedAt.Equal(old.StartedAt) {
			return s, nil
		}
		select {
		case <-ctx.Done():
			return old, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return old, fmt.Errorf("Core 重启后未就绪")
}

// apiWithFile passes a config file to an API command that only accepts files.
func (m *Manager) apiWithFile(ctx context.Context, command, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := m.writeFile(name, b, 0600); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(m.dir, name))
	_, err = m.api(ctx, command, filepath.Join(m.dir, name))
	return err
}
