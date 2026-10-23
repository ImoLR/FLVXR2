//go:build linux

package nodeaddr

import (
	"context"
	"net/netip"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Collect reads the current interface addresses (two netlink dumps plus
// the default routes).
func Collect() (Report, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return Report{}, err
	}
	names := make(map[int]string, len(links))
	for _, l := range links {
		names[l.Attrs().Index] = l.Attrs().Name
	}
	defaults := map[int]map[int]bool{unix.AF_INET: {}, unix.AF_INET6: {}}
	for _, family := range []int{netlink.FAMILY_V4, netlink.FAMILY_V6} {
		routes, err := netlink.RouteListFiltered(family, &netlink.Route{Table: unix.RT_TABLE_MAIN}, netlink.RT_FILTER_TABLE)
		if err != nil {
			continue
		}
		for _, r := range routes {
			if r.Dst != nil {
				if ones, _ := r.Dst.Mask.Size(); ones != 0 {
					continue
				}
			}
			defaults[family][r.LinkIndex] = true
			for _, hop := range r.MultiPath {
				defaults[family][hop.LinkIndex] = true
			}
		}
	}
	list, err := netlink.AddrList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return Report{}, err
	}
	addrs := make([]Addr, 0, len(list))
	for _, a := range list {
		if a.IPNet == nil || a.Scope != unix.RT_SCOPE_UNIVERSE {
			continue
		}
		ip, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		family := unix.AF_INET6
		if ip.Is4() {
			family = unix.AF_INET
		}
		addrs = append(addrs, Addr{
			IP:           ip,
			Iface:        names[a.LinkIndex],
			DefaultRoute: defaults[family][a.LinkIndex],
			Temporary:    a.Flags&unix.IFA_F_TEMPORARY != 0,
			Deprecated:   a.Flags&unix.IFA_F_DEPRECATED != 0 || (ip.Is6() && a.PreferedLft == 0),
			Tentative:    a.Flags&(unix.IFA_F_TENTATIVE|unix.IFA_F_DADFAILED) != 0,
		})
	}
	return Filter(addrs), nil
}

// Watch calls onChange (debounced) after kernel address events until ctx
// ends. It returns when the subscription fails; the caller falls back to
// its slow timer.
func Watch(ctx context.Context, debounce time.Duration, onChange func()) error {
	ch := make(chan netlink.AddrUpdate, 64)
	done := make(chan struct{})
	defer close(done)
	if err := netlink.AddrSubscribeWithOptions(ch, done, netlink.AddrSubscribeOptions{}); err != nil {
		return err
	}
	var timer *time.Timer
	var fire <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return ctx.Err()
		case _, ok := <-ch:
			if !ok {
				return errSubscriptionClosed
			}
			if timer == nil {
				timer = time.NewTimer(debounce)
				fire = timer.C
			}
		case <-fire:
			timer, fire = nil, nil
			onChange()
		}
	}
}
