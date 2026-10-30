//go:build linux

package landing

import (
	"errors"
	"fmt"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// rateProbeDev is a throw-away IFB used once at agent start (plan 168).
const rateProbeDev = "flvxlr-pr"

// ProbeEntryRate checks once whether this kernel can run the entry shaper:
// an IFB with HTB + fq_codel, a clsact hook and a flower filter with skbedit +
// mirred (the exact objects the shaper uses), all on a private device that is
// deleted again. "" = usable; otherwise a short reason (LXC / OpenVZ, missing
// module) and the panel keeps the gost limiter / nftables policer path.
func ProbeEntryRate() string {
	loadIfbModule()
	h, err := netlink.NewHandle()
	if err != nil {
		return "netlink 不可用：" + err.Error()
	}
	defer h.Close()
	return probeEntryRateWith(h)
}

func probeEntryRateWith(h *netlink.Handle) string {
	if l, err := h.LinkByName(rateProbeDev); err == nil {
		_ = h.LinkDel(l) // left by a crashed earlier probe
	}
	if err := h.LinkAdd(&netlink.Ifb{LinkAttrs: netlink.LinkAttrs{Name: rateProbeDev, TxQLen: 1000}}); err != nil {
		return "内核不支持 ifb：" + err.Error()
	}
	l, err := h.LinkByName(rateProbeDev)
	if err != nil {
		return "内核不支持 ifb：" + err.Error()
	}
	defer func() { _ = h.LinkDel(l) }()
	idx := l.Attrs().Index
	root := netlink.NewHtb(netlink.QdiscAttrs{LinkIndex: idx, Handle: netlink.MakeHandle(rateHTBMajor, 0), Parent: netlink.HANDLE_ROOT})
	if err := h.QdiscReplace(root); err != nil {
		return "内核不支持 htb：" + err.Error()
	}
	class := netlink.NewHtbClass(netlink.ClassAttrs{LinkIndex: idx, Parent: netlink.MakeHandle(rateHTBMajor, 0), Handle: rateClassHandle(1)},
		netlink.HtbClassAttrs{Rate: 8_000_000, Ceil: 8_000_000, Quantum: rateQuantum})
	if err := h.ClassReplace(class); err != nil {
		return "内核不支持 htb：" + err.Error()
	}
	leaf := netlink.NewFqCodel(netlink.QdiscAttrs{LinkIndex: idx, Parent: rateClassHandle(1), Handle: netlink.MakeHandle(1, 0)})
	leaf.Flows, leaf.MemoryLimit = rateLeafFlows, rateLeafMemory
	if err := h.QdiscReplace(leaf); err != nil {
		return "内核不支持 fq_codel：" + err.Error()
	}
	hook := &netlink.GenericQdisc{QdiscAttrs: netlink.QdiscAttrs{LinkIndex: idx, Parent: netlink.HANDLE_CLSACT, Handle: netlink.MakeHandle(0xffff, 0)}, QdiscType: "clsact"}
	if err := h.QdiscAdd(hook); err != nil && !errors.Is(err, unix.EEXIST) {
		return "内核不支持 clsact：" + err.Error()
	}
	ipProto := nl.IPPROTO_TCP
	priority := rateClassHandle(1)
	edit := netlink.NewSkbEditAction()
	edit.Priority = &priority
	f := &netlink.Flower{
		FilterAttrs: netlink.FilterAttrs{LinkIndex: idx, Parent: netlink.HANDLE_MIN_INGRESS, Priority: 61870, Protocol: unix.ETH_P_IP, Handle: 1},
		EthType:     unix.ETH_P_IP,
		IPProto:     &ipProto,
		DestPort:    9,
		Actions:     []netlink.Action{edit, netlink.NewMirredAction(idx)},
	}
	if err := h.FilterReplace(f); err != nil {
		return fmt.Sprintf("内核不支持 flower / skbedit / mirred：%v", err)
	}
	return ""
}
