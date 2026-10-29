//go:build linux

package landing

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// Kernel objects of the per-user landing 速率 (plan 161). Every name, handle
// and filter pref is fixed, so a restart or a crash loop can never create a
// second set:
//
//   - IFB devices flvxlr-up (client → node: the devices' ingress is redirected
//     here) and flvxlr-dn (node → client: egress), each with root HTB 7f1a:
//     (unclassified = direct, unshaped) and one class 7f1a:<slot·16> per user
//     (rate = ceil) with an fq_codel leaf <slot>: — the user's connections
//     share the class fairly.
//   - On every L3 device with a global address (not lo / IFB, not a bridge /
//     bond port, not a VLAN on top of another selected device): a clsact qdisc
//     (reused when present) and one flower filter per (port, tcp|udp, IPv4|
//     IPv6, direction) at pref 61860 (IPv4) / 61861 (IPv6), actions skbedit
//     priority 7f1a:<slot·16> + mirred egress redirect to the IFB. Only the
//     user's node ports match.
//
// A mirred redirect to a missing or down device drops the packet, so filters
// are always removed before the IFBs, and an IFB found missing / down makes
// the next tick remove every filter and rebuild. The priority left on a
// re-injected packet has major 7f1a and a minor whose low nibble is 0, so the
// device's own qdisc treats it like priority 0 (pfifo_fast / fq / mqprio bands
// use priority & 15).
const (
	rateIfbUp   = "flvxlr-up"
	rateIfbDown = "flvxlr-dn"

	rateHTBMajor = 0x7f1a
	ratePrefV4   = 61860
	ratePrefV6   = 61861

	// A full comparison with the kernel (filters on every device, classes)
	// at most this often while rates are active; the 10 s tick only checks
	// devices and the IFBs (two netlink dumps).
	rateFullCheck = time.Minute

	rateLeafFlows  = 128     // fq_codel queues per user and direction
	rateLeafMemory = 4 << 20 // bytes per user and direction under overload
	rateQuantum    = 60000   // HTB DRR quantum (no borrowing between users)

	// A user whose rate goes keeps his class and filters for this long (until
	// a later tick), with the class set to rateUnlimited: what is queued leaves
	// at once and in order, nothing is dropped or overtaken (either would
	// shrink the connection's window right when its limit is lifted). Then
	// the filters go, then the class, and the IFBs once nobody is left.
	rateDrain = 2 * time.Second
)

// rateUnlimited is the HTB rate of a class whose limit was removed (above any
// NIC; HTB carries it as a 64-bit rate).
var rateUnlimited = rateWire(uint64(maxRateMbps) * RateBytesPerMbps)

// rateWire converts a FLVX rate (payload bytes/s) into the HTB rate in bits/s.
// HTB counts whole frames (Ethernet + IP + TCP headers of every segment);
// FLVX rates are payload, as counted by the agent's own limiters. The plan-161
// lab measured payload = 0.935–0.945 of the HTB rate for TCP through the
// Core over IPv4 and IPv6, so the class gets 6 % on top.
func rateWire(bytesPerSec uint64) uint64 { return bytesPerSec * 8 * 106 / 100 }

func rateClassMinor(slot uint16) uint16 { return slot << 4 }

func rateClassHandle(slot uint16) uint32 {
	return netlink.MakeHandle(rateHTBMajor, rateClassMinor(slot))
}

// rateKey is one filter; its handle is unique within its tcf_proto (device,
// hook, protocol).
type rateKey struct {
	egress bool
	v6     bool
	udp    bool
	port   uint16
}

func (k rateKey) handle() uint32 {
	h := uint32(k.port) << 2
	if k.udp {
		return h | 2
	}
	return h | 1
}

type rateDev struct {
	name    string
	filters map[rateKey]uint16 // → slot
}

type tcRateEngine struct {
	h *netlink.Handle
	// known: this process removed whatever an earlier one left (first call).
	known    bool
	active   bool              // IFBs + roots exist
	up, dn   int               // IFB ifindex
	classes  map[uint16]uint64 // slot → HTB rate (bits/s)
	devs     map[int]*rateDev  // ifindex → device with our filters
	lastFull time.Time
	// releasing: slots whose rate went (class unlimited, filters kept) →
	// since when; cleaned up after rateDrain.
	releasing map[uint16]time.Time
	// loadModule loads ifb without its two default devices (production only).
	loadModule func()
}

func newRateEngine() rateEngine {
	h, err := netlink.NewHandle()
	if err != nil {
		return &failedRateEngine{err: err}
	}
	return &tcRateEngine{h: h, loadModule: loadIfbModule}
}

// newRateEngineAt is used by tests (a handle inside a test namespace).
func newRateEngineAt(h *netlink.Handle) *tcRateEngine { return &tcRateEngine{h: h} }

// failedRateEngine reports a netlink socket that could not be opened.
type failedRateEngine struct{ err error }

func (f *failedRateEngine) release() RateStatus { return RateStatus{State: "off"} }

func (f *failedRateEngine) ensure(want rateWant, _ time.Time) RateStatus {
	if !want.active() {
		return RateStatus{State: "off"}
	}
	return RateStatus{State: "error", Error: "内核限速不可用：" + f.err.Error(), Groups: len(want.groups), Ports: want.ports()}
}

// loadIfbModule loads the ifb module with numifbs=0 when it is not loaded
// yet; otherwise the kernel's auto-load (on the first IFB link) would also
// create its default ifb0 / ifb1. Best effort.
func loadIfbModule() {
	if _, err := os.Stat("/sys/module/ifb"); err == nil {
		return
	}
	_ = exec.Command("modprobe", "ifb", "numifbs=0").Run()
}

func (e *tcRateEngine) status(want rateWant, err error) RateStatus {
	st := RateStatus{State: "on", Groups: len(want.groups), Ports: want.ports()}
	if !want.active() {
		st.State = "off"
	}
	for _, d := range e.devs {
		st.Devices = append(st.Devices, d.name)
	}
	sort.Strings(st.Devices)
	if err != nil {
		st.State, st.Error = "error", "内核限速失败："+err.Error()
		if r := []rune(st.Error); len(r) > 300 {
			st.Error = string(r[:300])
		}
	}
	return st
}

// release removes everything at once (the agent is stopping).
func (e *tcRateEngine) release() RateStatus {
	err := e.teardown()
	if err == nil {
		e.known = true
	}
	return e.status(rateWant{}, err)
}

func (e *tcRateEngine) ensure(want rateWant, now time.Time) RateStatus {
	if !e.known {
		// Objects of an earlier process: remove them (filters first) and
		// build from scratch once.
		if err := e.teardown(); err != nil {
			return e.status(want, err)
		}
		e.known = true
	}
	if !want.active() && !e.active {
		return e.status(want, nil) // nothing of ours: no kernel call at all
	}
	scan, err := e.scan()
	if err != nil {
		return e.status(want, err)
	}
	full := now.Sub(e.lastFull) >= rateFullCheck
	if e.active && (!scan.upOK || !scan.dnOK || scan.upIdx != e.up || scan.dnIdx != e.dn || (full && !e.verify(scan))) {
		// Someone removed / downed an IFB or changed our objects: a filter
		// may be dropping the user's packets now. Start over, filters first.
		if err := e.teardown(); err != nil {
			return e.status(want, err)
		}
	}
	if full {
		e.lastFull = now
	}
	if !want.active() {
		if !e.active {
			return e.status(want, nil)
		}
		// The last rate went: every class is released (unlimited at once),
		// cleaned up on a later tick; the IFBs go with the last class.
		err := e.sync(want, scan, now)
		if err == nil && len(e.classes) == 0 && e.filterCount() == 0 {
			err = e.teardown()
		}
		return e.status(want, err)
	}
	if !e.active {
		if err := e.setup(); err != nil {
			// Never leave filters behind a half-built IFB pair.
			_ = e.teardown()
			return e.status(want, err)
		}
	}
	return e.status(want, e.sync(want, scan, now))
}

func (e *tcRateEngine) filterCount() int {
	n := 0
	for _, d := range e.devs {
		n += len(d.filters)
	}
	return n
}

// rateScan is the device state of one tick.
type rateScan struct {
	devs         map[int]string // selected devices: ifindex → name
	links        map[int]bool   // every link that exists
	upIdx, dnIdx int
	upOK, dnOK   bool
}

func (e *tcRateEngine) scan() (rateScan, error) {
	s := rateScan{devs: map[int]string{}, links: map[int]bool{}}
	links, err := e.h.LinkList()
	if err != nil {
		return s, fmt.Errorf("读取网卡失败: %w", err)
	}
	addrs, err := e.h.AddrList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return s, fmt.Errorf("读取地址失败: %w", err)
	}
	global := map[int]bool{}
	for _, a := range addrs {
		if a.Scope == unix.RT_SCOPE_UNIVERSE && a.IP != nil && a.IP.IsGlobalUnicast() {
			global[a.LinkIndex] = true
		}
	}
	candidates := map[int]netlink.Link{}
	for _, l := range links {
		a := l.Attrs()
		s.links[a.Index] = true
		up := a.Flags&net.FlagUp != 0
		switch a.Name {
		case rateIfbUp:
			s.upIdx, s.upOK = a.Index, up
			continue
		case rateIfbDown:
			s.dnIdx, s.dnOK = a.Index, up
			continue
		}
		if a.Flags&net.FlagLoopback != 0 || !up || l.Type() == "ifb" || a.MasterIndex != 0 || !global[a.Index] {
			continue
		}
		candidates[a.Index] = l
	}
	for idx, l := range candidates {
		// A VLAN (or macvlan) on top of another selected device sends through
		// it: shaping both would count its packets twice.
		switch l.Type() {
		case "vlan", "macvlan", "macvtap", "ipvlan":
			if candidates[l.Attrs().ParentIndex] != nil {
				continue
			}
		}
		s.devs[idx] = l.Attrs().Name
	}
	return s, nil
}

// setup creates the IFB pair with their root HTBs.
func (e *tcRateEngine) setup() error {
	if e.loadModule != nil {
		e.loadModule()
	}
	idx := [2]int{}
	for i, name := range []string{rateIfbUp, rateIfbDown} {
		link := &netlink.Ifb{LinkAttrs: netlink.LinkAttrs{Name: name, TxQLen: 1000}}
		if err := e.h.LinkAdd(link); err != nil && !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("创建 IFB 设备失败（内核可能不支持 ifb）: %w", err)
		}
		l, err := e.h.LinkByName(name)
		if err != nil {
			return fmt.Errorf("创建 IFB 设备失败: %w", err)
		}
		root := netlink.NewHtb(netlink.QdiscAttrs{LinkIndex: l.Attrs().Index, Handle: netlink.MakeHandle(rateHTBMajor, 0), Parent: netlink.HANDLE_ROOT})
		root.Defcls = 0 // unclassified packets go out unshaped
		if err := e.h.QdiscReplace(root); err != nil {
			return fmt.Errorf("创建 HTB 失败（内核可能不支持 htb）: %w", err)
		}
		if err := e.h.LinkSetUp(l); err != nil {
			return fmt.Errorf("启用 IFB 设备失败: %w", err)
		}
		idx[i] = l.Attrs().Index
	}
	e.up, e.dn = idx[0], idx[1]
	e.active = true
	e.classes = map[uint16]uint64{}
	e.devs = map[int]*rateDev{}
	e.releasing = map[uint16]time.Time{}
	return nil
}

// sync applies the difference between the kernel (as recorded) and want:
// stale filters first, then classes, then new filters. Partial failures are
// recorded per object and retried on the next tick.
func (e *tcRateEngine) sync(want rateWant, scan rateScan, now time.Time) error {
	wantFilters := map[rateKey]uint16{}
	wantClasses := map[uint16]uint64{}
	for _, g := range want.groups {
		wantClasses[g.slot] = rateWire(g.bytesPerSec)
		for _, set := range []struct {
			udp   bool
			ports []uint16
		}{{false, g.tcp}, {true, g.udp}} {
			for _, p := range set.ports {
				for _, egress := range []bool{false, true} {
					for _, v6 := range []bool{false, true} {
						wantFilters[rateKey{egress: egress, v6: v6, udp: set.udp, port: p}] = g.slot
					}
				}
			}
		}
	}
	var errs []error
	// 1. Filters that go: devices no longer selected, ports / slots that changed.
	for idx, d := range e.devs {
		if _, selected := scan.devs[idx]; !selected {
			if scan.links[idx] {
				if err := e.removeFilters(idx); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", d.name, err))
					continue
				}
			}
			delete(e.devs, idx)
			continue
		}
		for k, slot := range d.filters {
			if _, ok := wantFilters[k]; ok {
				continue // kept; replaced in step 3 if the port changed owner
			}
			// A port no longer limited: a removed node goes at once; the ports
			// of a user whose rate went stay until his release is over.
			if since, releasing := e.releasing[slot]; releasing && now.Sub(since) < rateDrain {
				continue
			}
			if _, stillWanted := wantClasses[slot]; !stillWanted {
				if _, releasing := e.releasing[slot]; !releasing {
					continue // released in step 2 of this pass, removed later
				}
			}
			if err := e.h.FilterDel(e.filterRef(idx, k)); err != nil && !errors.Is(err, unix.ENOENT) {
				errs = append(errs, fmt.Errorf("%s 删除过滤器失败: %w", d.name, err))
				continue
			}
			delete(d.filters, k)
		}
	}
	// 2. Classes no longer wanted: released first (unlimited, its queue
	// leaves at once and in order), deleted once released long enough and
	// without filters (step 1 removed them in the same pass).
	for slot, rate := range e.classes {
		if _, ok := wantClasses[slot]; ok {
			delete(e.releasing, slot)
			continue
		}
		since, ok := e.releasing[slot]
		if !ok {
			if rate != rateUnlimited {
				if err := e.setClass(slot, rateUnlimited, true); err != nil {
					errs = append(errs, err)
					continue
				}
				e.classes[slot] = rateUnlimited
			}
			e.releasing[slot] = now
			continue
		}
		if now.Sub(since) < rateDrain || e.slotHasFilters(slot) {
			continue
		}
		if err := e.delClass(slot); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(e.classes, slot)
		delete(e.releasing, slot)
	}
	slots := make([]uint16, 0, len(wantClasses))
	for slot := range wantClasses {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	for _, slot := range slots {
		rate := wantClasses[slot]
		cur, ok := e.classes[slot]
		if ok && cur == rate {
			continue
		}
		if err := e.setClass(slot, rate, ok); err != nil {
			errs = append(errs, err)
			continue
		}
		e.classes[slot] = rate
	}
	// 3. New filters, only for classes that exist (a filter of a missing class
	// would just pass unshaped, but keep the state exact).
	idxs := make([]int, 0, len(scan.devs))
	for idx := range scan.devs {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	keys := make([]rateKey, 0, len(wantFilters))
	for k := range wantFilters {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keyLess(keys[i], keys[j]) })
	for _, idx := range idxs {
		d := e.devs[idx]
		if d == nil {
			if err := e.ensureClsact(idx); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", scan.devs[idx], err))
				continue
			}
			d = &rateDev{name: scan.devs[idx], filters: map[rateKey]uint16{}}
			e.devs[idx] = d
		}
		for _, k := range keys {
			slot := wantFilters[k]
			if d.filters[k] == slot {
				continue
			}
			if _, ok := e.classes[slot]; !ok {
				continue
			}
			if err := e.h.FilterReplace(e.filter(idx, k, slot)); err != nil {
				errs = append(errs, fmt.Errorf("%s 添加过滤器失败（内核可能不支持 flower / mirred）: %w", d.name, err))
				break // the same error for every port of this device
			}
			d.filters[k] = slot
		}
	}
	return errors.Join(errs...)
}

func (e *tcRateEngine) slotHasFilters(slot uint16) bool {
	for _, d := range e.devs {
		for _, s := range d.filters {
			if s == slot {
				return true
			}
		}
	}
	return false
}

func keyLess(a, b rateKey) bool {
	if a.port != b.port {
		return a.port < b.port
	}
	if a.udp != b.udp {
		return !a.udp
	}
	if a.v6 != b.v6 {
		return !a.v6
	}
	return !a.egress && b.egress
}

func (e *tcRateEngine) setClass(slot uint16, rate uint64, exists bool) error {
	for _, idx := range []int{e.up, e.dn} {
		class := netlink.NewHtbClass(netlink.ClassAttrs{LinkIndex: idx, Parent: netlink.MakeHandle(rateHTBMajor, 0), Handle: rateClassHandle(slot)},
			netlink.HtbClassAttrs{Rate: rate, Ceil: rate, Quantum: rateQuantum})
		if exists {
			// In place: queued packets and the leaf are kept.
			if err := e.h.ClassChange(class); err != nil {
				return fmt.Errorf("修改限速失败: %w", err)
			}
			continue
		}
		if err := e.h.ClassReplace(class); err != nil {
			return fmt.Errorf("创建限速类失败: %w", err)
		}
		leaf := netlink.NewFqCodel(netlink.QdiscAttrs{LinkIndex: idx, Parent: rateClassHandle(slot), Handle: netlink.MakeHandle(slot, 0)})
		leaf.Flows, leaf.MemoryLimit = rateLeafFlows, rateLeafMemory
		if err := e.h.QdiscReplace(leaf); err != nil {
			return fmt.Errorf("创建 fq_codel 失败: %w", err)
		}
	}
	return nil
}

func (e *tcRateEngine) delClass(slot uint16) error {
	for _, idx := range []int{e.up, e.dn} {
		class := &netlink.GenericClass{ClassAttrs: netlink.ClassAttrs{LinkIndex: idx, Parent: netlink.MakeHandle(rateHTBMajor, 0), Handle: rateClassHandle(slot)}, ClassType: "htb"}
		// Deleting the class also deletes its leaf.
		if err := e.h.ClassDel(class); err != nil && !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("删除限速类失败: %w", err)
		}
	}
	return nil
}

func (k rateKey) parent() (uint32, uint16, uint16) {
	parent := uint32(netlink.HANDLE_MIN_INGRESS)
	if k.egress {
		parent = netlink.HANDLE_MIN_EGRESS
	}
	if k.v6 {
		return parent, ratePrefV6, unix.ETH_P_IPV6
	}
	return parent, ratePrefV4, unix.ETH_P_IP
}

func (e *tcRateEngine) filterRef(dev int, k rateKey) netlink.Filter {
	parent, pref, proto := k.parent()
	return &netlink.GenericFilter{FilterAttrs: netlink.FilterAttrs{LinkIndex: dev, Parent: parent, Priority: pref, Protocol: proto, Handle: k.handle()}, FilterType: "flower"}
}

func (e *tcRateEngine) filter(dev int, k rateKey, slot uint16) *netlink.Flower {
	parent, pref, proto := k.parent()
	target := e.up
	if k.egress {
		target = e.dn
	}
	ipProto := nl.IPPROTO_TCP
	if k.udp {
		ipProto = nl.IPPROTO_UDP
	}
	priority := rateClassHandle(slot)
	edit := netlink.NewSkbEditAction()
	edit.Priority = &priority
	f := &netlink.Flower{
		FilterAttrs: netlink.FilterAttrs{LinkIndex: dev, Parent: parent, Priority: pref, Protocol: proto, Handle: k.handle()},
		EthType:     proto,
		IPProto:     &ipProto,
		// No SkipHw: netlink v1.3.0 encodes TCA_FLOWER_FLAGS in network
		// byte order (the kernel refuses it). A NIC that cannot offload a
		// redirect into an IFB keeps the filter in software anyway.
		Actions: []netlink.Action{edit, netlink.NewMirredAction(target)},
	}
	if k.egress {
		f.SrcPort = k.port
	} else {
		f.DestPort = k.port
	}
	return f
}

// hookQdisc is the qdisc on a device's ingress slot: "clsact", "ingress"
// (legacy, another program's: no egress hook) or "".
func (e *tcRateEngine) hookQdisc(idx int) (string, error) {
	link, err := e.h.LinkByIndex(idx)
	if err != nil {
		return "", err
	}
	qdiscs, err := e.h.QdiscList(link)
	if err != nil {
		return "", err
	}
	for _, q := range qdiscs {
		if q.Attrs().Parent == netlink.HANDLE_CLSACT && (q.Type() == "clsact" || q.Type() == "ingress") {
			return q.Type(), nil
		}
	}
	return "", nil
}

func (e *tcRateEngine) ensureClsact(idx int) error {
	kind, err := e.hookQdisc(idx)
	if err != nil {
		return fmt.Errorf("读取 qdisc 失败: %w", err)
	}
	switch kind {
	case "clsact":
		return nil
	case "ingress":
		return fmt.Errorf("网卡已有其他程序的 ingress qdisc，无法在这里限速")
	}
	q := &netlink.GenericQdisc{QdiscAttrs: netlink.QdiscAttrs{LinkIndex: idx, Parent: netlink.HANDLE_CLSACT, Handle: netlink.MakeHandle(0xffff, 0)}, QdiscType: "clsact"}
	if err := e.h.QdiscAdd(q); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("创建 clsact 失败: %w", err)
	}
	return nil
}

// removeFilters deletes our filters on one device (whole tcf_protos at our
// prefs) and its clsact when nothing else is left on it.
func (e *tcRateEngine) removeFilters(idx int) error {
	kind, err := e.hookQdisc(idx)
	if err != nil {
		if _, linkErr := e.h.LinkByIndex(idx); linkErr != nil {
			return nil // device gone: its filters are gone too
		}
		return fmt.Errorf("读取 qdisc 失败: %w", err)
	}
	if kind == "" {
		return nil
	}
	link, err := e.h.LinkByIndex(idx)
	if err != nil {
		return nil
	}
	others := 0
	hooks := []uint32{netlink.HANDLE_MIN_INGRESS}
	if kind == "clsact" {
		hooks = append(hooks, netlink.HANDLE_MIN_EGRESS)
	}
	for _, parent := range hooks {
		filters, err := e.h.FilterList(link, parent)
		if err != nil {
			return fmt.Errorf("读取过滤器失败: %w", err)
		}
		ours := map[[2]uint16]bool{}
		for _, f := range filters {
			a := f.Attrs()
			if a.Priority == ratePrefV4 || a.Priority == ratePrefV6 {
				ours[[2]uint16{a.Priority, a.Protocol}] = true
			} else {
				others++
			}
		}
		for k := range ours {
			// Handle 0 = the whole tcf_proto of this pref (never pref 0:
			// that would flush every filter of the hook).
			ref := &netlink.GenericFilter{FilterAttrs: netlink.FilterAttrs{LinkIndex: idx, Parent: parent, Priority: k[0], Protocol: k[1]}, FilterType: "flower"}
			if err := e.h.FilterDel(ref); err != nil && !errors.Is(err, unix.ENOENT) {
				return fmt.Errorf("删除过滤器失败: %w", err)
			}
		}
	}
	if kind == "clsact" && others == 0 {
		q := &netlink.GenericQdisc{QdiscAttrs: netlink.QdiscAttrs{LinkIndex: idx, Parent: netlink.HANDLE_CLSACT, Handle: netlink.MakeHandle(0xffff, 0)}, QdiscType: "clsact"}
		if err := e.h.QdiscDel(q); err != nil && !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.EINVAL) {
			return fmt.Errorf("删除 clsact 失败: %w", err)
		}
	}
	return nil
}

// teardown removes every object of ours: filters on all devices first, then
// the IFBs (only when every filter is gone — never a redirect into nothing).
func (e *tcRateEngine) teardown() error {
	qdiscs, err := e.h.QdiscList(nil)
	if err != nil {
		return fmt.Errorf("读取 qdisc 失败: %w", err)
	}
	hooked := map[int]bool{}
	for _, q := range qdiscs {
		if q.Attrs().Parent == netlink.HANDLE_CLSACT {
			hooked[q.Attrs().LinkIndex] = true
		}
	}
	var errs []error
	for idx := range hooked {
		if l, err := e.h.LinkByIndex(idx); err == nil && (l.Attrs().Name == rateIfbUp || l.Attrs().Name == rateIfbDown) {
			continue
		}
		if err := e.removeFilters(idx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	for _, name := range []string{rateIfbUp, rateIfbDown} {
		l, err := e.h.LinkByName(name)
		if err != nil {
			continue
		}
		if err := e.h.LinkDel(l); err != nil && !errors.Is(err, unix.ENODEV) {
			errs = append(errs, fmt.Errorf("删除 IFB 设备失败: %w", err))
		}
	}
	e.active = false
	e.classes, e.devs, e.releasing = nil, nil, nil
	return errors.Join(errs...)
}

// verify compares the kernel with the recorded state (full check): our
// filters on every attached device and the classes on both IFBs.
func (e *tcRateEngine) verify(scan rateScan) bool {
	for idx, d := range e.devs {
		if !scan.links[idx] {
			continue // device gone; sync drops it
		}
		link, err := e.h.LinkByIndex(idx)
		if err != nil {
			return false
		}
		have := map[[3]uint32]bool{}
		for _, parent := range []uint32{netlink.HANDLE_MIN_INGRESS, netlink.HANDLE_MIN_EGRESS} {
			filters, err := e.h.FilterList(link, parent)
			if err != nil {
				return false
			}
			for _, f := range filters {
				a := f.Attrs()
				if (a.Priority == ratePrefV4 || a.Priority == ratePrefV6) && a.Handle != 0 {
					have[[3]uint32{parent, uint32(a.Priority), a.Handle}] = true
				}
			}
		}
		if len(have) != len(d.filters) {
			return false
		}
		for k := range d.filters {
			parent, pref, _ := k.parent()
			if !have[[3]uint32{parent, uint32(pref), k.handle()}] {
				return false
			}
		}
	}
	for _, idx := range []int{e.up, e.dn} {
		link, err := e.h.LinkByIndex(idx)
		if err != nil {
			return false
		}
		classes, err := e.h.ClassList(link, netlink.MakeHandle(rateHTBMajor, 0))
		if err != nil {
			return false
		}
		have := map[uint32]bool{}
		for _, c := range classes {
			if c.Type() == "htb" && c.Attrs().Handle>>16 == rateHTBMajor {
				have[c.Attrs().Handle] = true
			}
		}
		if len(have) != len(e.classes) {
			return false
		}
		for slot := range e.classes {
			if !have[rateClassHandle(slot)] {
				return false
			}
		}
	}
	return true
}
