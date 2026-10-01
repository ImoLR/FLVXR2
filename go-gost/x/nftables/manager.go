//go:build linux

package nftables

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"golang.org/x/sys/unix"
)

const (
	TableName        = "flvx"
	TableFamily      = nftables.TableFamilyINet
	PreroutingChain  = "prerouting"
	PostroutingChain = "postrouting"

	// AccountingTableName holds the per-forward byte counters and speed limit policers.
	//
	// The DNAT rules in the "flvx" prerouting chain only see the first packet of each
	// connection (NAT chains are consulted once per conntrack entry), so they can neither
	// count traffic nor limit its speed. Every packet of a DNATed connection passes the
	// filter hooks: forward for remote targets, input/output for targets on this host.
	// Those hooks dispatch on the connection's original (pre-DNAT) destination port to a
	// small chain per forward and protocol that polices and counts each direction.
	//
	// It is a table of its own: the "flvx" table is shared with the WireGuard path chains,
	// and an older agent can be restored with `nft delete table inet flvx_acct`.
	AccountingTableName = "flvx_acct"

	// IPS_DST_NAT is the conntrack status bit set after destination NAT.
	// Keep this local because x/sys does not export nf_conntrack_common.h.
	conntrackStatusDNAT uint32 = 1 << 5

	ctDirOriginal byte = 0 // IP_CT_DIR_ORIGINAL: client -> forward target (upload)
	ctDirReply    byte = 1 // IP_CT_DIR_REPLY: forward target -> client (download)

	// accountingPriority runs the accounting hooks after the usual filter chains (iptables
	// filter 0, firewalld filter+10): packets they drop are neither counted nor policed.
	accountingPriority = 100

	ruleTagPrefix = "flvx:"
)

// Roles of the rules this manager creates; they are stored in the rule comment.
const (
	roleDNAT          = "dnat"
	roleUpload        = "up"
	roleDownload      = "down"
	roleLimitUpload   = "limit-up"
	roleLimitDownload = "limit-down"
)

type Manager struct {
	conn  *nftables.Conn
	table *nftables.Table
	rules map[string]*RuleState
	mu    sync.Mutex

	// Accounting state; acctTable is nil when the accounting table could not be set up
	// (forwarding still works, without counting and speed limits).
	acctTable *nftables.Table
	portMaps  map[string]*nftables.Set // protocol -> original dst port => goto forward chain
	gen       uint64
	// last holds the counter values already reported, by forward chain and role.
	last map[string]counterValue
	// pending holds the final traffic of removed forwards until the next CollectTraffic.
	pending []TrafficDelta
}

type RuleState struct {
	ForwardID    int64
	NodeID       int64
	UserID       int64
	UserTunnelID int64
	Protocol     string
	Port         int
	Target       string
	SpeedLimit   int
	// Gen identifies this installation of the rule; a re-added rule gets a new one.
	Gen uint64
	// AcctChain is the accounting chain of this rule ("" when accounting is unavailable).
	AcctChain string
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

// TrafficDelta is the traffic of one forward and protocol since the previous collection.
// Upload is what clients sent (conntrack original direction), download what they received.
type TrafficDelta struct {
	ForwardID     int64
	UserID        int64
	UserTunnelID  int64
	Protocol      string
	Port          int
	UploadBytes   uint64
	DownloadBytes uint64
}

type counterValue struct {
	packets uint64
	bytes   uint64
}

func NewManager() (*Manager, error) {
	conn, err := nftables.New()
	if err != nil {
		return nil, fmt.Errorf("open nftables: %w", err)
	}
	m := &Manager{
		conn:  conn,
		rules: make(map[string]*RuleState),
		last:  make(map[string]counterValue),
	}
	if err := m.initTable(); err != nil {
		return nil, fmt.Errorf("init table: %w", err)
	}
	// 清理内核中残留的旧 DNAT 规则，防止 agent 重启后重复添加
	// 面板会通过 WebSocket 重新同步所有活跃规则
	if err := m.clearStaleRules(); err != nil {
		fmt.Printf("⚠️ clear stale rules failed: %v\n", err)
	}
	if err := m.initAccounting(); err != nil {
		fmt.Printf("⚠️ nftables 流量统计/限速初始化失败，nftables 转发将不计流量、不限速: %v\n", err)
		m.acctTable = nil
		m.portMaps = nil
	}
	enableIPForwarding()
	return m, nil
}

func enableIPForwarding() {
	if err := exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run(); err != nil {
		fmt.Printf("⚠️ 设置 IPv4 转发失败: %v\n", err)
	}
	if err := exec.Command("sysctl", "-w", "net.ipv6.conf.all.forwarding=1").Run(); err != nil {
		fmt.Printf("⚠️ 设置 IPv6 转发失败: %v\n", err)
	}
}

func (m *Manager) initTable() error {
	table := &nftables.Table{
		Name:   TableName,
		Family: TableFamily,
	}
	m.conn.AddTable(table)
	if err := m.conn.Flush(); err != nil {
		return fmt.Errorf("add table: %w", err)
	}
	m.table = table
	if err := m.initChains(); err != nil {
		return fmt.Errorf("init chains: %w", err)
	}
	return nil
}

func (m *Manager) initChains() error {
	chains := []struct {
		name     string
		hook     *nftables.ChainHook
		priority *nftables.ChainPriority
	}{
		{
			name:     PreroutingChain,
			hook:     nftables.ChainHookPrerouting,
			priority: nftables.ChainPriorityNATDest,
		},
		{
			name:     PostroutingChain,
			hook:     nftables.ChainHookPostrouting,
			priority: nftables.ChainPriorityNATSource,
		},
	}
	for _, c := range chains {
		chain := &nftables.Chain{
			Name:     c.name,
			Table:    m.table,
			Hooknum:  c.hook,
			Priority: c.priority,
			Type:     nftables.ChainTypeNAT,
		}
		m.conn.AddChain(chain)
	}

	// Only masquerade connections DNATed by a prerouting rule. The legacy rule
	// was an unconditional masquerade and rewrote unrelated host traffic too,
	// including loopback DNS queries sent to systemd-resolved's 127.0.0.53 stub.
	postroutingChain := &nftables.Chain{
		Name:  PostroutingChain,
		Table: m.table,
	}
	rules, err := m.conn.GetRules(m.table, postroutingChain)
	if err != nil {
		return fmt.Errorf("get postrouting rules: %w", err)
	}
	hasScopedMasq := false
	for _, r := range rules {
		if !isMasqueradeRule(r) {
			continue
		}
		if isDNATMasqueradeRule(r) {
			hasScopedMasq = true
			continue
		}

		// Migrate the unsafe rule created by older FLVXR2 releases.
		m.conn.DelRule(r)
	}
	if !hasScopedMasq {
		m.conn.AddRule(&nftables.Rule{
			Table: m.table,
			Chain: postroutingChain,
			Exprs: newDNATMasqueradeExpressions(),
		})
	}
	return m.conn.Flush()
}

// dnatStatusExpressions match connections whose destination was NATed.
func dnatStatusExpressions() []expr.Any {
	mask := binaryutil.NativeEndian.PutUint32(conntrackStatusDNAT)
	zero := binaryutil.NativeEndian.PutUint32(0)
	return []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeySTATUS},
		&expr.Bitwise{
			SourceRegister: 1,
			DestRegister:   1,
			Len:            4,
			Mask:           mask,
			Xor:            zero,
		},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: zero},
	}
}

func newDNATMasqueradeExpressions() []expr.Any {
	return append(dnatStatusExpressions(), &expr.Masq{})
}

func isDNATMasqueradeRule(rule *nftables.Rule) bool {
	if rule == nil || !isMasqueradeRule(rule) {
		return false
	}

	var hasStatus, hasDNATMask, hasNonZeroCompare bool
	for _, e := range rule.Exprs {
		switch v := e.(type) {
		case *expr.Ct:
			if v.Key == expr.CtKeySTATUS && !v.SourceRegister {
				hasStatus = true
			}
		case *expr.Bitwise:
			if v.Len == 4 && len(v.Mask) == 4 {
				hasDNATMask = hasDNATMask || binaryutil.NativeEndian.Uint32(v.Mask)&conntrackStatusDNAT != 0
			}
		case *expr.Cmp:
			if v.Register == 1 && v.Op == expr.CmpOpNeq &&
				len(v.Data) == 4 && binaryutil.NativeEndian.Uint32(v.Data) == 0 {
				hasNonZeroCompare = true
			}
		}
	}
	return hasStatus && hasDNATMask && hasNonZeroCompare
}

// initAccounting (re)creates the accounting table. Whatever a previous agent process left
// there is dropped: the panel re-syncs every active forward.
func (m *Manager) initAccounting() error {
	table := &nftables.Table{Name: AccountingTableName, Family: TableFamily}
	m.conn.AddTable(table)
	m.conn.DelTable(table)
	if err := m.conn.Flush(); err != nil {
		return fmt.Errorf("reset accounting table: %w", err)
	}

	m.conn.AddTable(table)
	portMaps := make(map[string]*nftables.Set, 2)
	for _, protocol := range []string{"tcp", "udp"} {
		set := &nftables.Set{
			Table:    table,
			Name:     protocol + "_ports",
			IsMap:    true,
			KeyType:  nftables.TypeInetService,
			DataType: nftables.TypeVerdict,
		}
		if err := m.conn.AddSet(set, nil); err != nil {
			return fmt.Errorf("add %s port map: %w", protocol, err)
		}
		portMaps[protocol] = set
	}
	hooks := []struct {
		name string
		hook *nftables.ChainHook
	}{
		{name: "forward", hook: nftables.ChainHookForward},
		{name: "input", hook: nftables.ChainHookInput},
		{name: "output", hook: nftables.ChainHookOutput},
	}
	for _, h := range hooks {
		chain := m.conn.AddChain(&nftables.Chain{
			Name:     h.name,
			Table:    table,
			Hooknum:  h.hook,
			Priority: nftables.ChainPriorityRef(accountingPriority),
			Type:     nftables.ChainTypeFilter,
		})
		for _, protocol := range []string{"tcp", "udp"} {
			protoNum, _ := protocolNumber(protocol)
			set := portMaps[protocol]
			exprs := dnatStatusExpressions()
			exprs = append(exprs,
				&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{protoNum}},
				// The original direction tuple keeps the destination the client connected
				// to, i.e. the forward's listening port, in both directions.
				&expr.Ct{Register: 1, Key: expr.CtKeyPROTODST, Direction: uint32(ctDirOriginal)},
				&expr.Lookup{
					SourceRegister: 1,
					DestRegister:   0,
					IsDestRegSet:   true,
					SetName:        set.Name,
					SetID:          set.ID,
				},
			)
			m.conn.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: exprs})
		}
	}
	if err := m.conn.Flush(); err != nil {
		return fmt.Errorf("create accounting table: %w", err)
	}
	m.acctTable = table
	m.portMaps = portMaps
	return nil
}

// ruleTag identifies a rule created by this manager. It is stored as the rule comment.
type ruleTag struct {
	ForwardID int64
	Protocol  string
	Port      int
	Role      string
	Gen       uint64
}

func (t ruleTag) String() string {
	return fmt.Sprintf("%sfwd=%d:proto=%s:port=%d:role=%s:gen=%d", ruleTagPrefix, t.ForwardID, t.Protocol, t.Port, t.Role, t.Gen)
}

func (t ruleTag) userData() []byte {
	return userdata.AppendString(nil, userdata.TypeComment, t.String())
}

func parseRuleTag(rule *nftables.Rule) (ruleTag, bool) {
	if rule == nil || len(rule.UserData) == 0 {
		return ruleTag{}, false
	}
	comment, ok := userdata.GetString(rule.UserData, userdata.TypeComment)
	if !ok || !strings.HasPrefix(comment, ruleTagPrefix) {
		return ruleTag{}, false
	}
	var tag ruleTag
	seen := 0
	for _, field := range strings.Split(strings.TrimPrefix(comment, ruleTagPrefix), ":") {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			return ruleTag{}, false
		}
		var err error
		switch k {
		case "fwd":
			tag.ForwardID, err = strconv.ParseInt(v, 10, 64)
		case "proto":
			tag.Protocol = v
		case "port":
			tag.Port, err = strconv.Atoi(v)
		case "role":
			tag.Role = v
		case "gen":
			tag.Gen, err = strconv.ParseUint(v, 10, 64)
		default:
			continue
		}
		if err != nil {
			return ruleTag{}, false
		}
		seen++
	}
	if seen != 5 || tag.ForwardID <= 0 {
		return ruleTag{}, false
	}
	return tag, true
}

func protocolNumber(protocol string) (byte, error) {
	switch protocol {
	case "tcp":
		return unix.IPPROTO_TCP, nil
	case "udp":
		return unix.IPPROTO_UDP, nil
	default:
		return 0, fmt.Errorf("unsupported protocol: %s", protocol)
	}
}

func portBytes(port int) []byte {
	return []byte{byte(port >> 8), byte(port & 0xFF)}
}

// speedLimitBytesPerSecond converts a panel speed limit (Mbps) into the per-direction byte
// rate of the policer, the same rate gost limiters use ("$ <speed/8>MB", 1MB = 1MiB).
func speedLimitBytesPerSecond(speedLimit int) uint64 {
	if speedLimit <= 0 {
		return 0
	}
	return uint64(speedLimit) * 1024 * 1024 / 8
}

// policerBurstBytes lets a quarter second of traffic through at once, enough for TCP to
// reach the limited rate on usual round trip times.
func policerBurstBytes(rate uint64) uint32 {
	burst := rate / 4
	if burst < 64*1024 {
		burst = 64 * 1024
	}
	if burst > 1<<31 {
		burst = 1 << 31
	}
	return uint32(burst)
}

func accountingChainName(forwardID int64, protocol string, gen uint64) string {
	return fmt.Sprintf("f%d_%s_%d", forwardID, protocol, gen)
}

func lastKey(chain, role string) string {
	return chain + "/" + role
}

func (m *Manager) AddRule(forwardID, nodeID, userID, userTunnelID int64, protocol string, port int, target string, speedLimit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fmt.Printf("DEBUG AddRule: forwardID=%d protocol=%s port=%d target=%q speedLimit=%d\n", forwardID, protocol, port, target, speedLimit)

	key := ruleKey(forwardID, protocol)
	if _, exists := m.rules[key]; exists {
		return fmt.Errorf("rule already exists: %s", key)
	}
	protoNum, err := protocolNumber(protocol)
	if err != nil {
		return err
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid listen port: %d", port)
	}
	dnatAddr, dnatPort := parseTarget(target)
	ip := net.ParseIP(dnatAddr)
	if ip == nil {
		return fmt.Errorf("invalid target IP: %s", dnatAddr)
	}
	if dnatPort <= 0 || dnatPort > 65535 {
		return fmt.Errorf("invalid target port: %q", target)
	}

	m.gen++
	rs := &RuleState{
		ForwardID:    forwardID,
		NodeID:       nodeID,
		UserID:       userID,
		UserTunnelID: userTunnelID,
		Protocol:     protocol,
		Port:         port,
		Target:       target,
		SpeedLimit:   speedLimit,
		Gen:          m.gen,
	}
	dnatRule := &nftables.Rule{
		Table:    m.table,
		Chain:    &nftables.Chain{Name: PreroutingChain, Table: m.table},
		Exprs:    dnatExpressions(protoNum, port, ip, dnatPort),
		UserData: ruleTag{ForwardID: forwardID, Protocol: protocol, Port: port, Role: roleDNAT, Gen: rs.Gen}.userData(),
	}

	// Install DNAT and accounting in one transaction. If the accounting part is rejected
	// (e.g. a kernel without some expression), keep forwarding working without it.
	m.conn.AddRule(dnatRule)
	acctChain, acctErr := m.queueAccounting(rs, port)
	if acctErr == nil {
		if err := m.conn.Flush(); err == nil {
			rs.AcctChain = acctChain
			m.rules[key] = rs
			m.trackCounters(rs)
			return nil
		} else if acctChain == "" {
			return fmt.Errorf("add rule: %w", err)
		} else {
			acctErr = err
		}
		m.conn.AddRule(dnatRule)
	}
	if err := m.conn.Flush(); err != nil {
		return fmt.Errorf("add rule: %w", err)
	}
	fmt.Printf("⚠️ nftables 转发 %d/%s 已生效，但流量统计/限速规则添加失败（不计流量、不限速）: %v\n", forwardID, protocol, acctErr)
	m.rules[key] = rs
	return nil
}

func dnatExpressions(protoNum byte, port int, ip net.IP, dnatPort int) []expr.Any {
	var natFamily uint32
	var ipBytes []byte
	if ip4 := ip.To4(); ip4 != nil {
		natFamily = unix.NFPROTO_IPV4
		ipBytes = ip4
	} else {
		natFamily = unix.NFPROTO_IPV6
		ipBytes = ip.To16()
	}
	return []expr.Any{
		// Match protocol (tcp/udp) and the listening port.
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{protoNum}},
		&expr.Payload{
			DestRegister: 1,
			Base:         expr.PayloadBaseTransportHeader,
			Offset:       2,
			Len:          2,
		},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: portBytes(port)},
		// DNAT: target address in register 1, port (network byte order) in register 2.
		&expr.Immediate{Register: 1, Data: ipBytes},
		&expr.Immediate{Register: 2, Data: portBytes(dnatPort)},
		&expr.NAT{
			Type:        expr.NATTypeDestNAT,
			Family:      natFamily,
			RegAddrMin:  1,
			RegProtoMin: 2,
		},
	}
}

// queueAccounting adds the accounting chain of a rule and its port map entry to the
// pending batch. It returns "" when accounting is unavailable.
func (m *Manager) queueAccounting(rs *RuleState, port int) (string, error) {
	if m.acctTable == nil || m.portMaps == nil {
		return "", nil
	}
	set := m.portMaps[rs.Protocol]
	if set == nil {
		return "", fmt.Errorf("no port map for %s", rs.Protocol)
	}
	name := accountingChainName(rs.ForwardID, rs.Protocol, rs.Gen)
	chain := m.conn.AddChain(&nftables.Chain{Name: name, Table: m.acctTable})
	tag := func(role string) []byte {
		return ruleTag{ForwardID: rs.ForwardID, Protocol: rs.Protocol, Port: port, Role: role, Gen: rs.Gen}.userData()
	}
	rate := speedLimitBytesPerSecond(rs.SpeedLimit)
	directions := []struct {
		dir         byte
		limitRole   string
		counterRole string
	}{
		{dir: ctDirOriginal, limitRole: roleLimitUpload, counterRole: roleUpload},
		{dir: ctDirReply, limitRole: roleLimitDownload, counterRole: roleDownload},
	}
	for _, d := range directions {
		match := []expr.Any{
			&expr.Ct{Register: 1, Key: expr.CtKeyDIRECTION},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{d.dir}},
		}
		if rate > 0 {
			// Drop what exceeds the limit before it is counted: the sender retransmits it.
			m.conn.AddRule(&nftables.Rule{
				Table: m.acctTable,
				Chain: chain,
				Exprs: append(append([]expr.Any{}, match...),
					&expr.Limit{
						Type:  expr.LimitTypePktBytes,
						Rate:  rate,
						Unit:  expr.LimitTimeSecond,
						Burst: policerBurstBytes(rate),
						Over:  true,
					},
					&expr.Verdict{Kind: expr.VerdictDrop},
				),
				UserData: tag(d.limitRole),
			})
		}
		m.conn.AddRule(&nftables.Rule{
			Table:    m.acctTable,
			Chain:    chain,
			Exprs:    append(append([]expr.Any{}, match...), &expr.Counter{}),
			UserData: tag(d.counterRole),
		})
	}
	if err := m.conn.SetAddElements(set, []nftables.SetElement{{
		Key:         portBytes(port),
		VerdictData: &expr.Verdict{Kind: expr.VerdictGoto, Chain: name},
	}}); err != nil {
		return "", fmt.Errorf("add port map element: %w", err)
	}
	return name, nil
}

// trackCounters starts the reported values of a fresh accounting chain at zero, so bytes
// counted before the first collection are reported too.
func (m *Manager) trackCounters(rs *RuleState) {
	if rs.AcctChain == "" {
		return
	}
	m.last[lastKey(rs.AcctChain, roleUpload)] = counterValue{}
	m.last[lastKey(rs.AcctChain, roleDownload)] = counterValue{}
}

func (m *Manager) UpdateRule(forwardID int64, protocol string, port int, target string, speedLimit int) error {
	m.mu.Lock()
	var userID, userTunnelID, nodeID int64
	if rs, exists := m.rules[ruleKey(forwardID, protocol)]; exists {
		userID = rs.UserID
		userTunnelID = rs.UserTunnelID
		nodeID = rs.NodeID
	}
	m.mu.Unlock()

	if err := m.DeleteRule(forwardID, protocol); err != nil {
		return err
	}
	return m.AddRule(forwardID, nodeID, userID, userTunnelID, protocol, port, target, speedLimit)
}

// DeleteRule removes every rule of a forward and protocol (whatever port it used) and
// keeps the traffic counted since the last collection for the next CollectTraffic.
func (m *Manager) DeleteRule(forwardID int64, protocol string) error {
	return m.RemoveForward(forwardID, protocol, nil, false)
}

// DeleteRuleWithPort removes the rules of a forward and protocol; see RemoveForward.
func (m *Manager) DeleteRuleWithPort(forwardID int64, protocol string, port int) error {
	var ports []int
	if port > 0 {
		ports = []int{port}
	}
	return m.RemoveForward(forwardID, protocol, ports, false)
}

// RemoveForward removes the rules of a forward and protocol. Rules are identified by the
// forward id they were created for, never by port alone: another forward may use the port
// now, and the forward itself may have moved to another port. ports are only used to remove
// untagged rules left by older agents. The traffic counted since the last collection is kept
// for the next CollectTraffic. With terminate, established connections DNATed from the
// removed ports are ended too (pause/delete), as gost forwards do with TerminateConnections.
func (m *Manager) RemoveForward(forwardID int64, protocol string, ports []int, terminate bool) error {
	m.mu.Lock()
	removedPorts, err := m.deleteRuleLocked(forwardID, protocol, ports)
	m.mu.Unlock()
	if err != nil || !terminate {
		return err
	}
	protoNum, _ := protocolNumber(protocol)
	for _, port := range removedPorts {
		n, err := deleteDNATConntrackEntries(protoNum, uint16(port))
		if err != nil {
			fmt.Printf("⚠️ terminate %s connections of port %d: %v\n", protocol, port, err)
			continue
		}
		if n > 0 {
			fmt.Printf("✂️ terminated %d %s connections of forward %d (port %d)\n", n, protocol, forwardID, port)
		}
	}
	return nil
}

// deleteRuleLocked removes the forward's rules and returns the listening ports involved.
func (m *Manager) deleteRuleLocked(forwardID int64, protocol string, ports []int) ([]int, error) {
	protoNum, err := protocolNumber(protocol)
	if err != nil {
		return nil, err
	}
	key := ruleKey(forwardID, protocol)
	rs := m.rules[key]
	portSet := map[int]struct{}{}
	addPort := func(p int) {
		if p > 0 && p <= 65535 {
			portSet[p] = struct{}{}
		}
	}
	if rs != nil {
		addPort(rs.Port)
	}

	// Harvest the final counters before the accounting chains go away.
	m.harvestForward(forwardID, protocol, rs)

	prerouting := &nftables.Chain{Name: PreroutingChain, Table: m.table}
	rules, err := m.conn.GetRules(m.table, prerouting)
	if err != nil {
		return nil, fmt.Errorf("get prerouting rules: %w", err)
	}
	deleted := 0
	for _, rule := range rules {
		tag, ok := parseRuleTag(rule)
		if !ok || tag.ForwardID != forwardID || tag.Protocol != protocol {
			continue
		}
		if err := m.conn.DelRule(rule); err != nil {
			return nil, fmt.Errorf("delete rule: %w", err)
		}
		addPort(tag.Port)
		deleted++
	}
	if deleted == 0 {
		// Rules of older agents carry no tag: match protocol and port, never touching tagged
		// rules of other forwards.
		for _, port := range ports {
			for _, rule := range rules {
				if _, tagged := parseRuleTag(rule); tagged || isMasqueradeRule(rule) {
					continue
				}
				if matchProtoInRule(rule, protoNum) && matchPortInRule(rule, portBytes(port)) {
					if err := m.conn.DelRule(rule); err != nil {
						return nil, fmt.Errorf("delete rule: %w", err)
					}
					addPort(port)
					deleted++
				}
			}
		}
	}

	chains := m.queueAccountingRemoval(forwardID, protocol)
	if err := m.conn.Flush(); err != nil {
		return nil, fmt.Errorf("delete rules of forward %d/%s: %w", forwardID, protocol, err)
	}
	for _, c := range chains {
		delete(m.last, lastKey(c.name, roleUpload))
		delete(m.last, lastKey(c.name, roleDownload))
		addPort(c.port)
	}
	delete(m.rules, key)
	if deleted == 0 && len(chains) == 0 && rs == nil {
		fmt.Printf("ℹ️ no nftables rule of forward %d/%s on this node\n", forwardID, protocol)
	}
	removed := make([]int, 0, len(portSet))
	for p := range portSet {
		removed = append(removed, p)
	}
	return removed, nil
}

// queueAccountingRemoval adds the removal of a forward's accounting chains (any
// generation) and their port map entries to the pending batch.
func (m *Manager) queueAccountingRemoval(forwardID int64, protocol string) []accountingChainRef {
	if m.acctTable == nil {
		return nil
	}
	chains, err := m.forwardAccountingChains(forwardID, protocol)
	if err != nil {
		fmt.Printf("⚠️ list accounting chains of forward %d/%s: %v\n", forwardID, protocol, err)
		return nil
	}
	set := m.portMaps[protocol]
	for _, c := range chains {
		if set != nil && c.port > 0 {
			if err := m.conn.SetDeleteElements(set, []nftables.SetElement{{Key: portBytes(c.port)}}); err != nil {
				fmt.Printf("⚠️ remove port map element %s/%d: %v\n", protocol, c.port, err)
			}
		}
		chain := &nftables.Chain{Name: c.name, Table: m.acctTable}
		m.conn.FlushChain(chain)
		m.conn.DelChain(chain)
	}
	return chains
}

type accountingChainRef struct {
	name string
	port int
}

// forwardAccountingChains lists the kernel accounting chains of a forward and protocol and
// the port their map entry uses.
func (m *Manager) forwardAccountingChains(forwardID int64, protocol string) ([]accountingChainRef, error) {
	all, err := m.conn.ListChainsOfTableFamily(TableFamily)
	if err != nil {
		return nil, err
	}
	prefix := fmt.Sprintf("f%d_%s_", forwardID, protocol)
	var out []accountingChainRef
	for _, c := range all {
		if c.Table == nil || c.Table.Name != AccountingTableName || !strings.HasPrefix(c.Name, prefix) {
			continue
		}
		ref := accountingChainRef{name: c.Name}
		rules, err := m.conn.GetRules(m.acctTable, &nftables.Chain{Name: c.Name, Table: m.acctTable})
		if err == nil {
			for _, rule := range rules {
				if tag, ok := parseRuleTag(rule); ok && tag.ForwardID == forwardID {
					ref.port = tag.Port
					break
				}
			}
		}
		out = append(out, ref)
	}
	return out, nil
}

// readAccountingChain returns the upload/download counters of an accounting chain.
func (m *Manager) readAccountingChain(name string) (map[string]counterValue, error) {
	rules, err := m.conn.GetRules(m.acctTable, &nftables.Chain{Name: name, Table: m.acctTable})
	if err != nil {
		return nil, err
	}
	values := make(map[string]counterValue, 2)
	for _, rule := range rules {
		tag, ok := parseRuleTag(rule)
		if !ok || (tag.Role != roleUpload && tag.Role != roleDownload) {
			continue
		}
		for _, e := range rule.Exprs {
			if c, ok := e.(*expr.Counter); ok {
				values[tag.Role] = counterValue{packets: c.Packets, bytes: c.Bytes}
				break
			}
		}
	}
	return values, nil
}

// counterDelta returns what a counter added since it was last reported and records the new
// value. A counter below the reported value was reset; it is reported from zero.
func (m *Manager) counterDelta(chain, role string, now counterValue) uint64 {
	k := lastKey(chain, role)
	prev := m.last[k]
	m.last[k] = now
	if now.bytes >= prev.bytes {
		return now.bytes - prev.bytes
	}
	return now.bytes
}

// harvestForward queues the traffic a forward's accounting chains counted since the last
// collection, so removing the rules does not lose it.
func (m *Manager) harvestForward(forwardID int64, protocol string, rs *RuleState) {
	if m.acctTable == nil || rs == nil || rs.AcctChain == "" {
		return
	}
	values, err := m.readAccountingChain(rs.AcctChain)
	if err != nil {
		fmt.Printf("⚠️ read final counters of forward %d/%s: %v\n", forwardID, protocol, err)
		return
	}
	d := TrafficDelta{
		ForwardID:     rs.ForwardID,
		UserID:        rs.UserID,
		UserTunnelID:  rs.UserTunnelID,
		Protocol:      rs.Protocol,
		Port:          rs.Port,
		UploadBytes:   m.counterDelta(rs.AcctChain, roleUpload, values[roleUpload]),
		DownloadBytes: m.counterDelta(rs.AcctChain, roleDownload, values[roleDownload]),
	}
	if d.UploadBytes > 0 || d.DownloadBytes > 0 {
		m.pending = append(m.pending, d)
	}
}

// CollectTraffic returns the traffic counted since the previous call, per forward and
// protocol, including the final traffic of rules removed in between.
func (m *Manager) CollectTraffic() []TrafficDelta {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := m.pending
	m.pending = nil
	if m.acctTable == nil {
		return out
	}
	for _, rs := range m.rules {
		if rs.AcctChain == "" {
			continue
		}
		values, err := m.readAccountingChain(rs.AcctChain)
		if err != nil {
			continue
		}
		d := TrafficDelta{
			ForwardID:     rs.ForwardID,
			UserID:        rs.UserID,
			UserTunnelID:  rs.UserTunnelID,
			Protocol:      rs.Protocol,
			Port:          rs.Port,
			UploadBytes:   m.counterDelta(rs.AcctChain, roleUpload, values[roleUpload]),
			DownloadBytes: m.counterDelta(rs.AcctChain, roleDownload, values[roleDownload]),
		}
		if d.UploadBytes > 0 || d.DownloadBytes > 0 {
			out = append(out, d)
		}
	}
	return out
}

// TerminateConnections removes the conntrack entries of connections DNATed from the given
// listening port, so a paused or deleted forward stops carrying established connections
// (DNAT rules only apply to new connections).
func (m *Manager) TerminateConnections(protocol string, port int) (uint, error) {
	protoNum, err := protocolNumber(protocol)
	if err != nil {
		return 0, err
	}
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port: %d", port)
	}
	return deleteDNATConntrackEntries(protoNum, uint16(port))
}

func isMasqueradeRule(rule *nftables.Rule) bool {
	for _, e := range rule.Exprs {
		if _, ok := e.(*expr.Masq); ok {
			return true
		}
	}
	return false
}

func matchProtoInRule(rule *nftables.Rule, protoByte byte) bool {
	for _, e := range rule.Exprs {
		if cmp, ok := e.(*expr.Cmp); ok && cmp.Register == 1 {
			if len(cmp.Data) == 1 && cmp.Data[0] == protoByte {
				return true
			}
		}
	}
	return false
}

func matchPortInRule(rule *nftables.Rule, portBytes []byte) bool {
	if len(portBytes) != 2 {
		return false
	}
	for _, e := range rule.Exprs {
		if cmp, ok := e.(*expr.Cmp); ok && cmp.Register == 1 {
			if len(cmp.Data) == 2 && cmp.Data[0] == portBytes[0] && cmp.Data[1] == portBytes[1] {
				return true
			}
		}
	}
	return false
}

// GetCounters returns the counted bytes of the installed rules as of the last collection.
func (m *Manager) GetCounters() []CounterResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	var results []CounterResult
	for _, rs := range m.rules {
		up := m.last[lastKey(rs.AcctChain, roleUpload)]
		down := m.last[lastKey(rs.AcctChain, roleDownload)]
		results = append(results, CounterResult{
			ForwardID:    rs.ForwardID,
			UserID:       rs.UserID,
			UserTunnelID: rs.UserTunnelID,
			Protocol:     rs.Protocol,
			Port:         rs.Port,
			Packets:      up.packets + down.packets,
			Bytes:        up.bytes + down.bytes,
		})
	}
	return results
}

// ResetCounters is kept for the ResetNftablesCounters command. Counters are reported as
// deltas, so it only moves what was counted so far into the next collection.
func (m *Manager) ResetCounters() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rs := range m.rules {
		m.harvestForward(rs.ForwardID, rs.Protocol, rs)
	}
	return nil
}

func ruleKey(forwardID int64, protocol string) string {
	return fmt.Sprintf("%d_%s", forwardID, protocol)
}

func parseTarget(target string) (string, int) {
	target = strings.TrimSpace(target)
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		fmt.Printf("DEBUG parseTarget SplitHostPort(%q) failed: %v\n", target, err)
		return "", 0
	}
	port, _ := strconv.Atoi(portStr)
	return host, port
}

func CheckNftablesSupport() (bool, error) {
	conn, err := nftables.New()
	if err != nil {
		return false, fmt.Errorf("nftables not available: %w", err)
	}
	conn.CloseLasting()
	return true, nil
}

// clearStaleRules 清理内核中残留的旧 DNAT 规则（保留 MASQUERADE）
// 防止 agent 重启后重复添加规则。面板会通过 WebSocket 重新同步所有活跃规则。
func (m *Manager) clearStaleRules() error {
	preroutingChain := &nftables.Chain{
		Name:  PreroutingChain,
		Table: m.table,
	}
	rules, err := m.conn.GetRules(m.table, preroutingChain)
	if err != nil {
		return fmt.Errorf("get prerouting rules: %w", err)
	}

	deleted := 0
	for _, rule := range rules {
		// 保留 MASQUERADE 规则
		if isMasqueradeRule(rule) {
			continue
		}
		// 删除所有 DNAT 规则（面板会重新同步）
		if err := m.conn.DelRule(rule); err != nil {
			return fmt.Errorf("delete stale rule: %w", err)
		}
		deleted++
	}

	if deleted > 0 {
		fmt.Printf("🧹 Cleared %d stale DNAT rules on startup\n", deleted)
	}
	return m.conn.Flush()
}
