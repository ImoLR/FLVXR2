//go:build linux

package nftables

import (
	"fmt"
	"net"
	"reflect"
	"time"

	"github.com/go-gost/x/service"
	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const quotaTableName = "flvx_quota"

type quotaUsage struct {
	connections int
	ips         map[string]int
}

type quotaGate struct {
	ruleIPsFull          bool
	groupConnectionsFull bool
	groupIPsFull         bool
}

type quotaForward struct {
	id        int64
	quota     RuleQuota
	ports     map[string]int
	chain     *nftables.Chain
	gateChain *nftables.Chain
	sets      map[string]*nftables.Set
	setIPs    map[string]map[string]struct{}
	gate      quotaGate
	group     *service.NftQuotaGroup
}

func quotaEnabled(q RuleQuota) bool {
	return q.MaxConnections > 0 || q.MaxClientIPs > 0 || q.Group != ""
}

func quotaPollNeeded(q RuleQuota) bool {
	return q.MaxClientIPs > 0 || q.Group != ""
}

func (m *Manager) clearStaleQuota() error {
	tables, err := m.conn.ListTablesOfFamily(TableFamily)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if table.Name == quotaTableName {
			m.conn.DelTable(table)
			return m.conn.Flush()
		}
	}
	return nil
}

func (m *Manager) initQuotaTableLocked() error {
	if m.quotaTable != nil {
		return nil
	}
	table := &nftables.Table{Name: quotaTableName, Family: TableFamily}
	m.conn.AddTable(table)
	m.conn.AddChain(&nftables.Chain{Name: PreroutingChain, Table: table,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityRef(-101), Type: nftables.ChainTypeFilter})
	if err := m.conn.Flush(); err != nil {
		return err
	}
	m.quotaTable = table
	return nil
}

func (m *Manager) prepareQuotaLocked(rs *RuleState) error {
	q := rs.Quota
	if !quotaEnabled(q) {
		return nil
	}
	if q.MaxConnections < 0 || q.MaxClientIPs < 0 || q.GroupMaxConnections < -1 || q.GroupMaxClientIPs < -1 {
		return fmt.Errorf("invalid nft quota for forward %d", rs.ForwardID)
	}
	if existing := m.quotaForwards[rs.ForwardID]; existing != nil {
		if !reflect.DeepEqual(existing.quota, q) {
			return fmt.Errorf("quota mismatch across protocols of forward %d", rs.ForwardID)
		}
		return nil
	}
	if err := m.initQuotaTableLocked(); err != nil {
		return err
	}
	base := fmt.Sprintf("f%d", rs.ForwardID)
	qf := &quotaForward{id: rs.ForwardID, quota: q, ports: make(map[string]int),
		chain:     &nftables.Chain{Name: base, Table: m.quotaTable},
		gateChain: &nftables.Chain{Name: base + "_gate", Table: m.quotaTable},
		sets:      make(map[string]*nftables.Set), setIPs: make(map[string]map[string]struct{}),
	}
	m.conn.AddChain(qf.chain)
	m.conn.AddChain(qf.gateChain)
	if q.MaxConnections > 0 {
		// connlimit owns its state for the lifetime of this forward chain. Evaluate on
		// every original-direction packet so existing flows are learned; only new
		// packets are dropped when the cap is exceeded.
		exprs := []expr.Any{&expr.Connlimit{Count: uint32(q.MaxConnections), Flags: expr.NFT_CONNLIMIT_F_INV}}
		exprs = append(exprs, ctNewExpressions()...)
		exprs = append(exprs, &expr.Verdict{Kind: expr.VerdictDrop})
		m.conn.AddRule(&nftables.Rule{Table: m.quotaTable, Chain: qf.chain, Exprs: exprs})
	}
	m.conn.AddRule(&nftables.Rule{Table: m.quotaTable, Chain: qf.chain,
		Exprs: []expr.Any{&expr.Verdict{Kind: expr.VerdictJump, Chain: qf.gateChain.Name}}})
	for _, kind := range []string{"rule", "group"} {
		if (kind == "rule" && q.MaxClientIPs <= 0) || (kind == "group" && q.Group == "") {
			continue
		}
		for _, family := range []string{"4", "6"} {
			key := kind + family
			setType := nftables.TypeIPAddr
			if family == "6" {
				setType = nftables.TypeIP6Addr
			}
			set := &nftables.Set{Table: m.quotaTable, Name: base + "_" + key, KeyType: setType}
			if err := m.conn.AddSet(set, nil); err != nil {
				return err
			}
			qf.sets[key] = set
			qf.setIPs[key] = make(map[string]struct{})
		}
	}
	if err := m.conn.Flush(); err != nil {
		return err
	}
	if q.Group != "" {
		qf.group = service.AttachNftQuotaGroup(q.Group, q.GroupMaxConnections, q.GroupMaxClientIPs)
	}
	m.quotaForwards[rs.ForwardID] = qf
	// A zero budget is effective before the first DNAT rule is installed.
	if q.Group != "" {
		state, _ := service.GetQuotaGroupState(q.Group)
		initial := chooseQuotaGate(q, quotaUsage{}, state)
		if err := m.syncQuotaGateLocked(qf, initial, nil, state.ClientIPs); err != nil {
			return err
		}
	}
	return nil
}

func ctNewExpressions() []expr.Any {
	mask := binaryutil.NativeEndian.PutUint32(expr.CtStateBitNEW)
	zero := binaryutil.NativeEndian.PutUint32(0)
	return []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: mask, Xor: zero},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: zero},
	}
}

func (m *Manager) queueQuotaDispatchLocked(rs *RuleState) {
	qf := m.quotaForwards[rs.ForwardID]
	if qf == nil {
		return
	}
	proto, _ := protocolNumber(rs.Protocol)
	exprs := []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: portBytes(rs.Port)},
		&expr.Ct{Register: 1, Key: expr.CtKeyDIRECTION},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{ctDirOriginal}},
		&expr.Verdict{Kind: expr.VerdictJump, Chain: qf.chain.Name},
	}
	m.conn.AddRule(&nftables.Rule{Table: m.quotaTable, Chain: &nftables.Chain{Name: PreroutingChain, Table: m.quotaTable},
		Exprs: exprs, UserData: ruleTag{ForwardID: rs.ForwardID, Protocol: rs.Protocol, Port: rs.Port, Role: "quota", Gen: rs.Gen}.userData()})
}

func (m *Manager) queueQuotaRemovalLocked(forwardID int64, protocol string) {
	qf := m.quotaForwards[forwardID]
	if qf == nil || qf.ports[protocol] == 0 {
		return
	}
	if len(qf.ports) == 1 && len(m.quotaForwards) == 1 {
		m.conn.DelTable(m.quotaTable)
		return
	}
	base := &nftables.Chain{Name: PreroutingChain, Table: m.quotaTable}
	rules, err := m.conn.GetRules(m.quotaTable, base)
	if err != nil {
		return
	}
	for _, rule := range rules {
		tag, ok := parseRuleTag(rule)
		if ok && tag.ForwardID == forwardID && tag.Protocol == protocol && tag.Role == "quota" {
			_ = m.conn.DelRule(rule)
		}
	}
	if len(qf.ports) == 1 {
		m.conn.FlushChain(qf.chain)
		m.conn.FlushChain(qf.gateChain)
		m.conn.DelChain(qf.chain)
		m.conn.DelChain(qf.gateChain)
		for _, set := range qf.sets {
			m.conn.DelSet(set)
		}
	}
}

func (m *Manager) finishQuotaRemovalLocked(forwardID int64, protocol string) {
	qf := m.quotaForwards[forwardID]
	if qf == nil {
		return
	}
	delete(qf.ports, protocol)
	if len(qf.ports) != 0 {
		return
	}
	delete(m.quotaForwards, forwardID)
	qf.group.Detach()
	if len(m.quotaForwards) == 0 {
		m.quotaTable = nil
	}
	if qf.quota.Group != "" {
		found := false
		for _, other := range m.quotaForwards {
			if other.quota.Group == qf.quota.Group {
				found = true
				break
			}
		}
		if !found {
			service.SetNftQuotaGroupUsage(qf.quota.Group, 0, nil)
		}
	}
	m.stopQuotaPollIfIdleLocked()
}

func chooseQuotaGate(q RuleQuota, usage quotaUsage, group service.QuotaGroupState) quotaGate {
	gate := quotaGate{ruleIPsFull: q.MaxClientIPs > 0 && len(usage.ips) >= q.MaxClientIPs}
	if q.Group != "" {
		gate.groupConnectionsFull = group.MaxConnections >= 0 && group.Connections >= group.MaxConnections
		gate.groupIPsFull = group.MaxClientIPs >= 0 && len(group.ClientIPs) >= group.MaxClientIPs
	}
	return gate
}

func quotaGateAllows(gate quotaGate, ruleIPs map[string]int, groupIPs map[string]struct{}, ip string) bool {
	if gate.groupConnectionsFull {
		return false
	}
	if gate.ruleIPsFull && ruleIPs[ip] == 0 {
		return false
	}
	if gate.groupIPsFull {
		if _, active := groupIPs[ip]; !active {
			return false
		}
	}
	return true
}

func (m *Manager) startQuotaPollLocked() {
	if m.quotaPollStop != nil {
		return
	}
	needed := false
	for _, qf := range m.quotaForwards {
		if len(qf.ports) > 0 && quotaPollNeeded(qf.quota) {
			needed = true
			break
		}
	}
	if !needed {
		return
	}
	stop := make(chan struct{})
	m.quotaPollStop = stop
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := m.ReconcileQuota(); err != nil {
					fmt.Printf("⚠️ nft quota reconcile failed: %v\n", err)
				}
			case <-stop:
				return
			}
		}
	}()
}

func (m *Manager) stopQuotaPollIfIdleLocked() {
	for _, qf := range m.quotaForwards {
		if quotaPollNeeded(qf.quota) {
			return
		}
	}
	if m.quotaPollStop != nil {
		close(m.quotaPollStop)
		m.quotaPollStop = nil
	}
}

// ReconcileQuota scans conntrack only while a forward has an IP cap or group budget.
func (m *Manager) ReconcileQuota() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	needed := false
	for _, qf := range m.quotaForwards {
		if len(qf.ports) > 0 && quotaPollNeeded(qf.quota) {
			needed = true
			break
		}
	}
	if !needed {
		return nil
	}
	var flows []*netlink.ConntrackFlow
	for _, family := range []netlink.InetFamily{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		items, err := netlink.ConntrackTableList(netlink.ConntrackTable, family)
		if err != nil {
			return fmt.Errorf("read conntrack family %d: %w", family, err)
		}
		flows = append(flows, items...)
	}
	usage := attributeQuotaFlows(m.rules, flows)
	groupUsage := aggregateQuotaGroups(usage, m.quotaForwards)
	for group, current := range groupUsage {
		service.SetNftQuotaGroupUsage(group, current.connections, current.ips)
	}
	for id, qf := range m.quotaForwards {
		if !quotaPollNeeded(qf.quota) {
			continue
		}
		state, _ := service.GetQuotaGroupState(qf.quota.Group)
		gate := chooseQuotaGate(qf.quota, usage[id], state)
		if err := m.syncQuotaGateLocked(qf, gate, usage[id].ips, state.ClientIPs); err != nil {
			return err
		}
	}
	return nil
}

func aggregateQuotaGroups(usage map[int64]quotaUsage, forwards map[int64]*quotaForward) map[string]quotaUsage {
	groups := make(map[string]quotaUsage)
	for id, qf := range forwards {
		if qf.quota.Group == "" {
			continue
		}
		group := groups[qf.quota.Group]
		if group.ips == nil {
			group.ips = make(map[string]int)
		}
		group.connections += usage[id].connections
		for ip, count := range usage[id].ips {
			group.ips[ip] += count
		}
		groups[qf.quota.Group] = group
	}
	return groups
}

func attributeQuotaFlows(rules map[string]*RuleState, flows []*netlink.ConntrackFlow) map[int64]quotaUsage {
	byPort := make(map[string]*RuleState, len(rules))
	for _, rs := range rules {
		if !quotaEnabled(rs.Quota) {
			continue
		}
		proto, _ := protocolNumber(rs.Protocol)
		byPort[fmt.Sprintf("%d/%d", proto, rs.Port)] = rs
	}
	usage := make(map[int64]quotaUsage)
	for _, flow := range flows {
		if flow == nil {
			continue
		}
		rs := byPort[fmt.Sprintf("%d/%d", flow.Forward.Protocol, flow.Forward.DstPort)]
		if rs == nil || !(dnatPortFilter{proto: flow.Forward.Protocol, port: flow.Forward.DstPort}).MatchConntrackFlow(flow) || flow.Forward.SrcIP == nil {
			continue
		}
		item := usage[rs.ForwardID]
		if item.ips == nil {
			item.ips = make(map[string]int)
		}
		item.connections++
		item.ips[flow.Forward.SrcIP.String()]++
		usage[rs.ForwardID] = item
	}
	return usage
}

func (m *Manager) syncQuotaGateLocked(qf *quotaForward, gate quotaGate, ruleIPs map[string]int, groupIPs map[string]struct{}) error {
	desired := make(map[string]map[string]struct{}, len(qf.sets))
	for kind := range qf.sets {
		desired[kind] = make(map[string]struct{})
		if (kind[:4] == "rule" && !gate.ruleIPsFull) || (kind[:5] == "group" && !gate.groupIPsFull) {
			continue
		}
		if kind[:4] == "rule" {
			for ip := range ruleIPs {
				if ipFamily(ip) == kind[len(kind)-1:] {
					desired[kind][ip] = struct{}{}
				}
			}
		} else {
			for ip := range groupIPs {
				if ipFamily(ip) == kind[len(kind)-1:] {
					desired[kind][ip] = struct{}{}
				}
			}
		}
	}
	changed := gate != qf.gate
	for kind, set := range qf.sets {
		old := qf.setIPs[kind]
		for ip := range desired[kind] {
			if _, ok := old[ip]; !ok {
				if err := m.conn.SetAddElements(set, []nftables.SetElement{{Key: quotaIPBytes(ip)}}); err != nil {
					return err
				}
				changed = true
			}
		}
		for ip := range old {
			if _, ok := desired[kind][ip]; !ok {
				if err := m.conn.SetDeleteElements(set, []nftables.SetElement{{Key: quotaIPBytes(ip)}}); err != nil {
					return err
				}
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	if gate != qf.gate {
		m.conn.FlushChain(qf.gateChain)
		if gate.groupConnectionsFull {
			exprs := append(ctNewExpressions(), &expr.Verdict{Kind: expr.VerdictDrop})
			m.conn.AddRule(&nftables.Rule{Table: m.quotaTable, Chain: qf.gateChain, Exprs: exprs})
		}
		for _, kind := range []string{"rule", "group"} {
			if (kind == "rule" && !gate.ruleIPsFull) || (kind == "group" && !gate.groupIPsFull) {
				continue
			}
			for _, family := range []string{"4", "6"} {
				set := qf.sets[kind+family]
				m.conn.AddRule(&nftables.Rule{Table: m.quotaTable, Chain: qf.gateChain, Exprs: quotaIPGateExpressions(set, family)})
			}
		}
	}
	if err := m.conn.Flush(); err != nil {
		return err
	}
	qf.gate = gate
	qf.setIPs = desired
	return nil
}

func ipFamily(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	if parsed.To4() != nil {
		return "4"
	}
	return "6"
}

func quotaIPBytes(ip string) []byte {
	parsed := net.ParseIP(ip)
	if parsed.To4() != nil {
		return parsed.To4()
	}
	return parsed.To16()
}

func quotaIPGateExpressions(set *nftables.Set, family string) []expr.Any {
	familyNum := byte(unix.NFPROTO_IPV4)
	offset, length := uint32(12), uint32(4)
	if family == "6" {
		familyNum, offset, length = unix.NFPROTO_IPV6, 8, 16
	}
	exprs := ctNewExpressions()
	return append(exprs,
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{familyNum}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: length},
		&expr.Lookup{SourceRegister: 1, SetName: set.Name, SetID: set.ID, Invert: true},
		&expr.Verdict{Kind: expr.VerdictDrop},
	)
}
