//go:build linux

package machineprotect

import (
	"errors"
	"fmt"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// The protection table is separate from the agent's other tables (flvx, flvx_acct,
// flvx_quota), which a WebSocket reconnect rebuilds; nothing else touches it, and
// `nft delete table inet flvx_machine_protection` removes every trace.
const (
	TableName   = "flvx_machine_protection"
	chainName   = "prerouting"
	tcpSetName  = "tcp_ports"
	udpSetName  = "udp_ports"
	counterName = "dropped"
	// After conntrack (-200) so ct state is known, before DNAT (-100) so nft
	// DNAT forwards are seen with their public port.
	chainPriority = -150
)

// nftGuard owns the protection table. While not blocking the table holds only the
// two port sets and the counter: no hook, no per-packet work. Blocking adds one
// prerouting base chain; every change is one atomic netlink transaction.
type nftGuard struct {
	table    *nftables.Table
	tcpSet   *nftables.Set
	udpSet   *nftables.Set
	ports    PortSet
	blocking bool
	present  bool
	adopted  bool // a leftover table of an earlier process was replaced
	noFib    bool
	base     int64 // dropped packets of earlier incarnations of the table
	last     int64 // last counter value of the current table
}

func newGuard() guard { return &nftGuard{} }

func (g *nftGuard) conn() (*nftables.Conn, error) {
	return nftables.New()
}

func (g *nftGuard) tableRef() *nftables.Table {
	return &nftables.Table{Name: TableName, Family: nftables.TableFamilyINet}
}

func tableExists(c *nftables.Conn) (bool, error) {
	tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return false, err
	}
	for _, t := range tables {
		if t.Name == TableName {
			return true, nil
		}
	}
	return false, nil
}

func portElements(ports []int) []nftables.SetElement {
	out := make([]nftables.SetElement, 0, len(ports))
	for _, p := range ports {
		out = append(out, nftables.SetElement{Key: binaryutil.BigEndian.PutUint16(uint16(p))})
	}
	return out
}

func ifname(name string) []byte {
	b := make([]byte, 16)
	copy(b, name)
	return b
}

// rules returns the blocking chain's rules:
//
//	ct state & new == 0 return             (established/related/untracked: 1 rule)
//	iifname "lo" return
//	meta l4proto tcp th dport @tcp_ports fib daddr type local counter name dropped drop
//	meta l4proto udp th dport @udp_ports fib daddr type local counter name dropped drop
//
// fib daddr type local limits the drop to traffic for this host (gost listeners,
// nft DNAT forwards); routed traffic through the node is never touched.
func (g *nftGuard) rules(chain *nftables.Chain) []*nftables.Rule {
	zero := binaryutil.NativeEndian.PutUint32(0)
	notNew := &nftables.Rule{Table: g.table, Chain: chain, Exprs: []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: binaryutil.NativeEndian.PutUint32(expr.CtStateBitNEW), Xor: zero},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: zero},
		&expr.Verdict{Kind: expr.VerdictReturn},
	}}
	loopback := &nftables.Rule{Table: g.table, Chain: chain, Exprs: []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname("lo")},
		&expr.Verdict{Kind: expr.VerdictReturn},
	}}
	drop := func(proto byte, set *nftables.Set) *nftables.Rule {
		exprs := []expr.Any{
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
			&expr.Payload{OperationType: expr.PayloadLoad, DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
			&expr.Lookup{SourceRegister: 1, SetName: set.Name, SetID: set.ID},
		}
		if !g.noFib {
			exprs = append(exprs,
				&expr.Fib{Register: 1, FlagDADDR: true, ResultADDRTYPE: true},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(unix.RTN_LOCAL)},
			)
		}
		exprs = append(exprs,
			&expr.Objref{Type: int(nftables.ObjTypeCounter), Name: counterName},
			&expr.Verdict{Kind: expr.VerdictDrop},
		)
		return &nftables.Rule{Table: g.table, Chain: chain, Exprs: exprs}
	}
	return []*nftables.Rule{notNew, loopback, drop(unix.IPPROTO_TCP, g.tcpSet), drop(unix.IPPROTO_UDP, g.udpSet)}
}

func (g *nftGuard) chain() *nftables.Chain {
	policy := nftables.ChainPolicyAccept
	return &nftables.Chain{Name: chainName, Table: g.table, Hooknum: nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityRef(chainPriority), Type: nftables.ChainTypeFilter, Policy: &policy}
}

func (g *nftGuard) addChain(c *nftables.Conn) {
	ch := c.AddChain(g.chain())
	for _, r := range g.rules(ch) {
		c.AddRule(r)
	}
}

// build creates the whole table in one transaction.
func (g *nftGuard) build(c *nftables.Conn, ports PortSet, blocking bool) error {
	g.table = c.AddTable(g.tableRef())
	g.tcpSet = &nftables.Set{Table: g.table, Name: tcpSetName, KeyType: nftables.TypeInetService}
	g.udpSet = &nftables.Set{Table: g.table, Name: udpSetName, KeyType: nftables.TypeInetService}
	if err := c.AddSet(g.tcpSet, portElements(ports.TCP)); err != nil {
		return err
	}
	if err := c.AddSet(g.udpSet, portElements(ports.UDP)); err != nil {
		return err
	}
	c.AddObj(&nftables.NamedObj{Table: g.table, Name: counterName, Type: nftables.ObjTypeCounter, Obj: &expr.Counter{}})
	if blocking {
		g.addChain(c)
	}
	return c.Flush()
}

// Ensure brings the table to (ports, blocking). Self-heals a table removed
// behind our back (nft flush ruleset, firewall reload).
func (g *nftGuard) Ensure(ports PortSet, blocking bool) error {
	c, err := g.conn()
	if err != nil {
		return err
	}
	exists, err := tableExists(c)
	if err != nil {
		return err
	}
	if !g.adopted {
		// A table left by an earlier agent process has unknown contents: replace it.
		g.adopted = true
		if exists {
			c.DelTable(g.tableRef())
			if err := c.Flush(); err != nil {
				return err
			}
			exists = false
		}
	}
	if !exists {
		if g.present {
			g.base += g.last
		}
		g.present, g.last = false, 0
		err := g.build(c, ports, blocking)
		if err != nil && blocking && !g.noFib {
			// Kernels without nft_fib: drop on the port alone.
			g.noFib = true
			c, cerr := g.conn()
			if cerr != nil {
				return cerr
			}
			err = g.build(c, ports, blocking)
		}
		if err != nil {
			return err
		}
		g.present, g.ports, g.blocking = true, ports, blocking
		return nil
	}
	g.table = g.tableRef()
	g.tcpSet = &nftables.Set{Table: g.table, Name: tcpSetName, KeyType: nftables.TypeInetService}
	g.udpSet = &nftables.Set{Table: g.table, Name: udpSetName, KeyType: nftables.TypeInetService}
	changed := false
	diff := func(set *nftables.Set, old, want []int) error {
		add, del := portDiff(old, want)
		if len(add) > 0 {
			if err := c.SetAddElements(set, portElements(add)); err != nil {
				return err
			}
			changed = true
		}
		if len(del) > 0 {
			if err := c.SetDeleteElements(set, portElements(del)); err != nil {
				return err
			}
			changed = true
		}
		return nil
	}
	if err := diff(g.tcpSet, g.ports.TCP, ports.TCP); err != nil {
		return err
	}
	if err := diff(g.udpSet, g.ports.UDP, ports.UDP); err != nil {
		return err
	}
	if blocking != g.blocking {
		changed = true
		if blocking {
			// The sets exist from an earlier transaction: the kernel finds them by
			// name (a set ID only matters for sets created in the same batch).
			g.tcpSet.ID, g.udpSet.ID = 0, 0
			g.addChain(c)
		} else {
			ch := g.chain()
			c.FlushChain(ch)
			c.DelChain(ch)
		}
	}
	if !changed {
		return nil
	}
	if err := c.Flush(); err != nil {
		// The kernel state differs from what we believe (e.g. an element removed by
		// hand) or the kernel lacks nft_fib: rebuild the table from scratch once
		// (build falls back to a rule without fib by itself).
		g.adopted = false
		if retryErr := g.Ensure(ports, blocking); retryErr != nil {
			return fmt.Errorf("%v; rebuild: %w", err, retryErr)
		}
		return nil
	}
	g.ports, g.blocking = ports, blocking
	return nil
}

func portDiff(old, want []int) (add, del []int) {
	o := make(map[int]bool, len(old))
	for _, p := range old {
		o[p] = true
	}
	w := make(map[int]bool, len(want))
	for _, p := range want {
		w[p] = true
		if !o[p] {
			add = append(add, p)
		}
	}
	for _, p := range old {
		if !w[p] {
			del = append(del, p)
		}
	}
	return add, del
}

// Remove deletes the table if it exists. Only this guard creates the table, so
// once a leftover was cleaned up and nothing was built since, there is nothing to
// look for (no netlink call per idle sample).
func (g *nftGuard) Remove() error {
	if g.adopted && !g.present {
		return nil
	}
	c, err := g.conn()
	if err != nil {
		return err
	}
	exists, err := tableExists(c)
	if err != nil {
		return err
	}
	g.adopted = true
	if g.present {
		g.base += g.last
	}
	g.present, g.blocking, g.last, g.ports = false, false, 0, PortSet{}
	if !exists {
		return nil
	}
	c.DelTable(g.tableRef())
	return c.Flush()
}

// Dropped reads the drop counter (packets) of the current table plus earlier ones.
func (g *nftGuard) Dropped() (int64, error) {
	if !g.present {
		return g.base, nil
	}
	c, err := g.conn()
	if err != nil {
		return g.base + g.last, err
	}
	obj, err := c.GetObject(&nftables.NamedObj{Table: g.tableRef(), Name: counterName, Type: nftables.ObjTypeCounter})
	if err != nil {
		return g.base + g.last, err
	}
	named, ok := obj.(*nftables.NamedObj)
	if !ok {
		return g.base + g.last, errors.New("unexpected counter object")
	}
	counter, ok := named.Obj.(*expr.Counter)
	if !ok {
		return g.base + g.last, fmt.Errorf("unexpected counter data %T", named.Obj)
	}
	g.last = int64(counter.Packets)
	return g.base + g.last, nil
}

// Blocking reports whether the drop chain is installed.
func (g *nftGuard) Blocking() bool { return g.present && g.blocking }

// Supported reports whether nf_tables is reachable.
func (g *nftGuard) Supported() bool {
	c, err := nftables.New()
	if err != nil {
		return false
	}
	_, err = c.ListTablesOfFamily(nftables.TableFamilyINet)
	return err == nil
}
