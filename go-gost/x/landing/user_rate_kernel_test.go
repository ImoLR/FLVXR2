//go:build linux

package landing

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// Plan 161: the tc engine against the real kernel, inside a throw-away
// network namespace (root only; nothing on the host is touched apart from
// loading the ifb module). Proves: exactly one set of objects (also after
// restarts and a crash loop), the device selection, in-place rate changes,
// port / group removal, removal order (no filter ever points at a missing
// IFB), foreign filters and clsact left alone, recovery after someone
// deletes an IFB, and that nothing is left after the last rate goes.

type rateNS struct {
	t    *testing.T
	name string
	h    *netlink.Handle
}

func newRateNS(t *testing.T) *rateNS {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root (network namespaces)")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("needs iproute2")
	}
	name := "flvxrt-" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "-")
	if len(name) > 30 {
		name = name[:30]
	}
	_ = exec.Command("ip", "netns", "del", name).Run()
	if out, err := exec.Command("ip", "netns", "add", name).CombinedOutput(); err != nil {
		t.Fatalf("netns add: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", name).Run() })
	ns, err := netns.GetFromName(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ns.Close() })
	h, err := netlink.NewHandleAt(ns)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	r := &rateNS{t: t, name: name, h: h}
	r.sh("ip", "-n", name, "link", "set", "lo", "up")
	return r
}

func (r *rateNS) sh(args ...string) {
	r.t.Helper()
	if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
		r.t.Fatalf("%v: %v %s", args, err, out)
	}
}

// dummy adds an up dummy device with the given addresses.
func (r *rateNS) dummy(name string, addrs ...string) int {
	r.t.Helper()
	r.sh("ip", "-n", r.name, "link", "add", name, "type", "dummy")
	for _, a := range addrs {
		r.sh("ip", "-n", r.name, "addr", "add", a, "dev", name, "nodad")
	}
	r.sh("ip", "-n", r.name, "link", "set", name, "up")
	l, err := r.h.LinkByName(name)
	if err != nil {
		r.t.Fatal(err)
	}
	return l.Attrs().Index
}

type rateFilterView struct {
	pref, proto uint16
	handle      uint32
	egress      bool
	mirredTo    int
	priority    uint32
}

// ours lists our flower filters on one device.
func (r *rateNS) ours(dev string) []rateFilterView {
	r.t.Helper()
	l, err := r.h.LinkByName(dev)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []rateFilterView
	for _, parent := range []uint32{netlink.HANDLE_MIN_INGRESS, netlink.HANDLE_MIN_EGRESS} {
		filters, err := r.h.FilterList(l, parent)
		if err != nil {
			if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT) {
				continue // no clsact
			}
			r.t.Fatalf("filter list %s: %v", dev, err)
		}
		for _, f := range filters {
			a := f.Attrs()
			if (a.Priority != ratePrefV4 && a.Priority != ratePrefV6) || a.Handle == 0 {
				continue
			}
			v := rateFilterView{pref: a.Priority, proto: a.Protocol, handle: a.Handle, egress: parent == netlink.HANDLE_MIN_EGRESS}
			if fl, ok := f.(*netlink.Flower); ok {
				for _, act := range fl.Actions {
					switch act := act.(type) {
					case *netlink.MirredAction:
						v.mirredTo = act.Ifindex
					case *netlink.SkbEditAction:
						if act.Priority != nil {
							v.priority = *act.Priority
						}
					}
				}
			}
			out = append(out, v)
		}
	}
	return out
}

func (r *rateNS) hasClsact(dev string) bool {
	r.t.Helper()
	l, err := r.h.LinkByName(dev)
	if err != nil {
		r.t.Fatal(err)
	}
	qs, err := r.h.QdiscList(l)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, q := range qs {
		if q.Type() == "clsact" {
			return true
		}
	}
	return false
}

func (r *rateNS) ifb(name string) (int, bool) {
	l, err := r.h.LinkByName(name)
	if err != nil {
		return 0, false
	}
	return l.Attrs().Index, true
}

// classes maps our HTB class handles on one IFB to their rate (bytes/s).
func (r *rateNS) classes(ifb string) map[uint32]uint64 {
	r.t.Helper()
	l, err := r.h.LinkByName(ifb)
	if err != nil {
		r.t.Fatal(err)
	}
	cs, err := r.h.ClassList(l, netlink.MakeHandle(rateHTBMajor, 0))
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[uint32]uint64{}
	for _, c := range cs {
		if h, ok := c.(*netlink.HtbClass); ok && h.Handle>>16 == rateHTBMajor {
			out[h.Handle] = h.Rate
		}
	}
	return out
}

func (r *rateNS) leaves(ifb string) map[uint32]string {
	r.t.Helper()
	l, err := r.h.LinkByName(ifb)
	if err != nil {
		r.t.Fatal(err)
	}
	qs, err := r.h.QdiscList(l)
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[uint32]string{}
	for _, q := range qs {
		if q.Attrs().Parent != netlink.HANDLE_ROOT {
			out[q.Attrs().Parent] = q.Type()
		}
	}
	return out
}

func rateTestWant(groups ...rateGroupWant) rateWant {
	w := rateWant{groups: groups}
	rateSlots(w.groups)
	return w
}

func TestUserRateKernelNetns(t *testing.T) {
	r := newRateNS(t)
	eth := r.dummy("eth0", "11.0.0.1/24", "2001:db8:1::1/64")
	r.dummy("wg0", "10.9.0.1/24")
	r.dummy("noaddr")            // no global address: not shaped
	r.dummy("ll0", "fe80::1/64") // link-local only: not shaped
	r.sh("ip", "-n", r.name, "link", "add", "br0", "type", "bridge")
	r.sh("ip", "-n", r.name, "link", "add", "port0", "type", "dummy")
	r.sh("ip", "-n", r.name, "link", "set", "port0", "master", "br0")
	r.sh("ip", "-n", r.name, "addr", "add", "11.0.5.1/24", "dev", "br0")
	r.sh("ip", "-n", r.name, "addr", "add", "11.0.6.1/24", "dev", "port0") // bridge port: never shaped
	r.sh("ip", "-n", r.name, "link", "set", "br0", "up")
	r.sh("ip", "-n", r.name, "link", "set", "port0", "up")
	r.sh("ip", "-n", r.name, "link", "add", "link", "eth0", "name", "eth0.7", "type", "vlan", "id", "7")
	r.sh("ip", "-n", r.name, "addr", "add", "11.0.7.1/24", "dev", "eth0.7") // VLAN on a shaped device: not twice
	r.sh("ip", "-n", r.name, "link", "set", "eth0.7", "up")

	e := newRateEngineAt(r.h)
	now := time.Unix(1_800_000_000, 0)
	want := rateTestWant(
		rateGroupWant{group: "landing-user-7", bytesPerSec: 20 * RateBytesPerMbps, tcp: []uint16{31001, 31002}, udp: []uint16{31001}},
		rateGroupWant{group: "landing-user-9", bytesPerSec: 50 * RateBytesPerMbps, tcp: []uint16{31003}},
	)
	st := e.ensure(want, now)
	if st.State != "on" || st.Error != "" {
		if strings.Contains(st.Error, "ifb") {
			t.Skipf("kernel without ifb: %s", st.Error)
		}
		t.Fatalf("status %+v", st)
	}
	if strings.Join(st.Devices, ",") != "br0,eth0,wg0" || st.Groups != 2 || st.Ports != 4 {
		t.Fatalf("devices / counts %+v", st)
	}
	up, ok1 := r.ifb(rateIfbUp)
	dn, ok2 := r.ifb(rateIfbDown)
	if !ok1 || !ok2 {
		t.Fatal("IFB devices missing")
	}
	// 4 (port, proto) pairs × IPv4/IPv6 × in/out = 16 filters per device.
	check := func(stage string, perDev int) {
		t.Helper()
		for _, dev := range []string{"eth0", "wg0", "br0"} {
			fs := r.ours(dev)
			if len(fs) != perDev {
				t.Fatalf("%s: %s has %d filters, want %d", stage, dev, len(fs), perDev)
			}
			for _, f := range fs {
				target := up
				if f.egress {
					target = dn
				}
				if f.mirredTo != target || f.priority>>16 != rateHTBMajor || f.priority&0xf != 0 {
					t.Fatalf("%s: %s filter %+v (want mirred %d)", stage, dev, f, target)
				}
			}
		}
		for _, dev := range []string{"noaddr", "ll0", "port0", "eth0.7"} {
			if fs := r.ours(dev); len(fs) != 0 || r.hasClsact(dev) {
				t.Fatalf("%s: %s must not be touched (%d filters)", stage, dev, len(fs))
			}
		}
	}
	check("first", 16)
	wantRates := map[uint32]uint64{rateClassHandle(7): rateWire(20*RateBytesPerMbps) / 8, rateClassHandle(9): rateWire(50*RateBytesPerMbps) / 8}
	for _, ifb := range []string{rateIfbUp, rateIfbDown} {
		got := r.classes(ifb)
		if len(got) != 2 || got[rateClassHandle(7)] != wantRates[rateClassHandle(7)] || got[rateClassHandle(9)] != wantRates[rateClassHandle(9)] {
			t.Fatalf("%s classes %v want %v", ifb, got, wantRates)
		}
		if l := r.leaves(ifb); l[rateClassHandle(7)] != "fq_codel" || l[rateClassHandle(9)] != "fq_codel" {
			t.Fatalf("%s leaves %v", ifb, l)
		}
	}

	// Same state again: nothing changes (same IFB indexes, same filters).
	if st := e.ensure(want, now.Add(10*time.Second)); st.State != "on" {
		t.Fatalf("steady %+v", st)
	}
	if u, _ := r.ifb(rateIfbUp); u != up {
		t.Fatal("IFB recreated in steady state")
	}
	check("steady", 16)

	// Rate change: class changed in place (leaf kept), filters untouched.
	want.groups[0].bytesPerSec = 40 * RateBytesPerMbps
	if st := e.ensure(want, now.Add(20*time.Second)); st.State != "on" {
		t.Fatalf("change %+v", st)
	}
	if got := r.classes(rateIfbDown)[rateClassHandle(7)]; got != rateWire(40*RateBytesPerMbps)/8 {
		t.Fatalf("changed rate %d", got)
	}
	if l := r.leaves(rateIfbDown); l[rateClassHandle(7)] != "fq_codel" {
		t.Fatal("leaf lost on a rate change")
	}
	check("rate change", 16)

	// Port 31002 removed (node deleted): its 4 filters per device go.
	want.groups[0].tcp = []uint16{31001}
	if st := e.ensure(want, now.Add(30*time.Second)); st.State != "on" || st.Ports != 3 {
		t.Fatalf("port removal %+v", st)
	}
	check("port removal", 12)

	// Restart (new engine, same kernel): leftovers removed, one set rebuilt.
	for i := 0; i < 5; i++ { // a crash loop
		e = newRateEngineAt(r.h)
		if st := e.ensure(want, now.Add(time.Duration(40+i)*time.Second)); st.State != "on" {
			t.Fatalf("restart %d %+v", i, st)
		}
	}
	up, _ = r.ifb(rateIfbUp)
	dn, _ = r.ifb(rateIfbDown)
	check("after restarts", 12)
	links, _ := r.h.LinkList()
	ifbs := 0
	for _, l := range links {
		if l.Type() == "ifb" {
			ifbs++
		}
	}
	if ifbs != 2 {
		t.Fatalf("%d IFB devices after a crash loop", ifbs)
	}

	// Someone deletes an IFB: the next tick must not leave filters pointing
	// at nothing; it rebuilds.
	r.sh("ip", "-n", r.name, "link", "del", rateIfbDown)
	if st := e.ensure(want, now.Add(60*time.Second)); st.State != "on" {
		t.Fatalf("after IFB delete %+v", st)
	}
	up, _ = r.ifb(rateIfbUp)
	dn, _ = r.ifb(rateIfbDown)
	check("after IFB delete", 12)

	// Someone removes our filters on eth0: found by the full check.
	r.sh("tc", "-n", r.name, "filter", "del", "dev", "eth0", "egress", "pref", "61860", "protocol", "ip")
	if st := e.ensure(want, now.Add(65*time.Second)); st.State != "on" {
		t.Fatalf("partial %+v", st)
	}
	if n := len(r.ours("eth0")); n == 12 {
		t.Fatal("full check ran before its interval") // 60 s since the last full check: not yet
	}
	if st := e.ensure(want, now.Add(130*time.Second)); st.State != "on" {
		t.Fatalf("full check %+v", st)
	}
	up, _ = r.ifb(rateIfbUp)
	dn, _ = r.ifb(rateIfbDown)
	check("after full check", 12)

	// A foreign filter on eth0 keeps its clsact when our rates go.
	r.sh("tc", "-n", r.name, "filter", "add", "dev", "eth0", "ingress", "pref", "100", "protocol", "all", "matchall", "action", "pass")

	// Group 9 removed: released first — class unlimited, filters kept, so
	// whatever is queued leaves at once and in order — then removed on a
	// later tick (filters, then the class).
	want.groups = want.groups[:1]
	if st := e.ensure(want, now.Add(140*time.Second)); st.State != "on" || st.Groups != 1 {
		t.Fatalf("group removal %+v", st)
	}
	check("released (filters kept)", 12)
	if got := r.classes(rateIfbDown); len(got) != 2 || got[rateClassHandle(9)] != rateUnlimited/8 || got[rateClassHandle(7)] != rateWire(40*RateBytesPerMbps)/8 {
		t.Fatalf("released class must be unlimited, the other unchanged: %v", got)
	}
	if st := e.ensure(want, now.Add(141*time.Second)); st.State != "on" {
		t.Fatalf("release tick %+v", st)
	}
	check("still released before the drain time", 12)
	if st := e.ensure(want, now.Add(150*time.Second)); st.State != "on" {
		t.Fatalf("cleanup tick %+v", st)
	}
	check("after cleanup", 8)
	if got := r.classes(rateIfbUp); len(got) != 1 || r.classes(rateIfbDown)[rateClassHandle(9)] != 0 {
		t.Fatalf("classes after cleanup %v", got)
	}
	// Re-added during a release: back to its rate, nothing rebuilt.
	want.groups = append(want.groups, rateGroupWant{group: "landing-user-9", slot: 9, bytesPerSec: 50 * RateBytesPerMbps, tcp: []uint16{31003}})
	if st := e.ensure(want, now.Add(151*time.Second)); st.State != "on" {
		t.Fatalf("re-add %+v", st)
	}
	want.groups = want.groups[:1]
	if st := e.ensure(want, now.Add(152*time.Second)); st.State != "on" {
		t.Fatalf("release again %+v", st)
	}
	want.groups = append(want.groups, rateGroupWant{group: "landing-user-9", slot: 9, bytesPerSec: 50 * RateBytesPerMbps, tcp: []uint16{31003}})
	if st := e.ensure(want, now.Add(153*time.Second)); st.State != "on" {
		t.Fatalf("re-add while released %+v", st)
	}
	check("re-added while released", 12)
	if got := r.classes(rateIfbDown)[rateClassHandle(9)]; got != rateWire(50*RateBytesPerMbps)/8 {
		t.Fatalf("re-added class rate %d", got)
	}
	want.groups = want.groups[:1]
	e.ensure(want, now.Add(154*time.Second))
	e.ensure(want, now.Add(158*time.Second))
	check("removed again", 8)

	// The last rate goes: released (unlimited, filters kept), then everything
	// is removed on a later tick; the foreign filter stays.
	if st := e.ensure(rateWant{}, now.Add(160*time.Second)); st.State != "off" || st.Error != "" {
		t.Fatalf("off %+v", st)
	}
	if n := len(r.ours("eth0")); n != 8 {
		t.Fatalf("eth0 filters while released: %d", n)
	}
	if got := r.classes(rateIfbDown)[rateClassHandle(7)]; got != rateUnlimited/8 {
		t.Fatalf("last class not released: %d", got)
	}
	if st := e.ensure(rateWant{}, now.Add(170*time.Second)); st.State != "off" || st.Error != "" {
		t.Fatalf("off after drain %+v", st)
	}
	if _, ok := r.ifb(rateIfbUp); ok {
		t.Fatal("IFB left behind")
	}
	if _, ok := r.ifb(rateIfbDown); ok {
		t.Fatal("IFB left behind")
	}
	for _, dev := range []string{"eth0", "wg0", "br0"} {
		if n := len(r.ours(dev)); n != 0 {
			t.Fatalf("%s keeps %d filters", dev, n)
		}
	}
	if !r.hasClsact("eth0") || r.hasClsact("wg0") || r.hasClsact("br0") {
		t.Fatal("clsact: the foreign one must stay, ours must go")
	}
	l, _ := r.h.LinkByName("eth0")
	fs, _ := r.h.FilterList(l, netlink.HANDLE_MIN_INGRESS)
	foreign := 0
	for _, f := range fs {
		if f.Attrs().Priority == 100 && f.Attrs().Handle != 0 {
			foreign++
		}
	}
	if foreign != 1 {
		t.Fatalf("foreign filter lost (%d)", foreign)
	}
	// Idle: no netlink work at all.
	if st := e.ensure(rateWant{}, now.Add(180*time.Second)); st.State != "off" {
		t.Fatalf("idle %+v", st)
	}
	_ = eth
}

// A device with another program's legacy ingress qdisc cannot get our egress
// hook: reported, the other devices are still shaped, nothing is changed there.
func TestUserRateKernelForeignIngressNetns(t *testing.T) {
	r := newRateNS(t)
	r.dummy("eth0", "11.0.0.1/24")
	r.dummy("eth1", "11.0.1.1/24")
	r.sh("tc", "-n", r.name, "qdisc", "add", "dev", "eth1", "ingress")
	e := newRateEngineAt(r.h)
	want := rateTestWant(rateGroupWant{group: "landing-user-3", bytesPerSec: 10 * RateBytesPerMbps, tcp: []uint16{40001}})
	st := e.ensure(want, time.Unix(1_800_000_000, 0))
	if strings.Contains(st.Error, "ifb") {
		t.Skipf("kernel without ifb: %s", st.Error)
	}
	if st.State != "error" || !strings.Contains(st.Error, "eth1") || !strings.Contains(st.Error, "ingress qdisc") {
		t.Fatalf("status %+v", st)
	}
	if n := len(r.ours("eth0")); n != 4 {
		t.Fatalf("eth0 filters %d", n)
	}
	if n := len(r.ours("eth1")); n != 0 {
		t.Fatalf("eth1 filters %d", n)
	}
	if st := e.release(); st.State != "off" {
		t.Fatalf("release %+v", st)
	}
	if _, ok := r.ifb(rateIfbDown); ok {
		t.Fatal("IFB left behind after release")
	}
	out, _ := exec.Command("tc", "-n", r.name, "qdisc", "show", "dev", "eth1").CombinedOutput()
	if !strings.Contains(string(out), "ingress") {
		t.Fatalf("foreign ingress qdisc removed: %s", out)
	}
}
