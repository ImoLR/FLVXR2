package landing

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Plan 161: per-user 速率 on FLVX-native landing servers.
//
// The panel sends one rate per user (UserRates.Groups, "landing-user-<id>")
// and tags each of the user's nodes with it (Node.RateGroup). The pinned Core
// has no bandwidth limiter, so the agent shapes in the kernel: all traffic of
// the user's public node ports on this machine shares ONE limit per
// direction (client → node = upload, node → client = download). It is an HTB
// class per user behind two IFB devices with fq_codel inside the class, so the
// user's connections share the rate fairly (plan 161 lab: nftables policing,
// the 妙妙屋 Helper's method, split two downloads 12.6 / 7.3 at 20 Mbps; HTB +
// fq_codel 9.4 / 9.4). Only the user's node ports are classified: other ports,
// users and services on the machine are never touched; a rate change never
// restarts the Core or closes a connection. See user_rate_linux.go.

// LandingRateVersion is advertised in the WebSocket handshake (landingRate=1):
// this agent applies user_rates / rate_group. Older agents ignore both, so
// the panel only sends them to agents advertising it.
const LandingRateVersion = 1

// RateBytesPerMbps is FLVX's unit for every 速率 / 限速 (Mbps = MiB/s × 8).
const RateBytesPerMbps = 131072

const (
	maxRateMbps = 100000
	// maxRateSlots bounds the per-user classes on one server (HTB minor ids
	// are slot<<4, see rateSlots).
	maxRateSlots = 4095
)

// UserRates is the desired-state part (only sent to agents advertising
// LandingRateVersion).
type UserRates struct {
	Version int         `json:"version"`
	Groups  []RateGroup `json:"groups"`
}

// RateGroup is one user's 速率 on this server (both directions).
type RateGroup struct {
	Group    string `json:"group"` // landing-user-<id>
	RateMbps int    `json:"rate_mbps"`
}

func (u *UserRates) validate(nodes []Node) error {
	if u == nil {
		for _, n := range nodes {
			if n.RateGroup != "" {
				return fmt.Errorf("landing node rate group without user rates")
			}
		}
		return nil
	}
	if u.Version != LandingRateVersion || len(u.Groups) > maxRateSlots {
		return fmt.Errorf("invalid landing user rates")
	}
	groups := map[string]bool{}
	for _, g := range u.Groups {
		if !validUserGroupName(g.Group) || groups[g.Group] || g.RateMbps < 1 || g.RateMbps > maxRateMbps {
			return fmt.Errorf("invalid landing user rate")
		}
		groups[g.Group] = true
	}
	for _, n := range nodes {
		if n.RateGroup != "" && (!groups[n.RateGroup] || n.Chain != nil || n.Listen != "") {
			return fmt.Errorf("invalid landing node rate group")
		}
	}
	return nil
}

// rateWant is what the kernel should enforce: one entry per user with at
// least one public enabled node, sorted by group.
type rateWant struct {
	groups []rateGroupWant
}

type rateGroupWant struct {
	group       string
	slot        uint16 // HTB class minor = slot<<4 (see rateClassMinor)
	bytesPerSec uint64 // FLVX unit (payload); the shaper adds the wire allowance
	tcp, udp    []uint16
}

func (w rateWant) active() bool { return len(w.groups) > 0 }

func (w rateWant) ports() int {
	n := 0
	for _, g := range w.groups {
		n += len(g.tcp) + len(g.udp)
	}
	return n
}

// rateWant derives the kernel state from the desired state: the public
// (non-loopback) enabled nodes of each rated user; SS2022 serves TCP and UDP,
// VLESS Reality TCP only (as Desired.publicPorts).
func (d Desired) rateWant() rateWant {
	if !d.Enabled || d.UserRates == nil {
		return rateWant{}
	}
	rates := map[string]int{}
	for _, g := range d.UserRates.Groups {
		rates[g.Group] = g.RateMbps
	}
	byGroup := map[string]*rateGroupWant{}
	for _, n := range d.Nodes {
		rate := rates[n.RateGroup]
		if n.RateGroup == "" || rate <= 0 || !n.Enabled || n.Listen != "" || n.Port < 1 || n.Port > 65535 {
			continue
		}
		g := byGroup[n.RateGroup]
		if g == nil {
			g = &rateGroupWant{group: n.RateGroup, bytesPerSec: uint64(rate) * RateBytesPerMbps}
			byGroup[n.RateGroup] = g
		}
		g.tcp = append(g.tcp, uint16(n.Port))
		if n.Protocol == "ss2022" {
			g.udp = append(g.udp, uint16(n.Port))
		}
	}
	out := rateWant{}
	for _, g := range byGroup {
		sort.Slice(g.tcp, func(i, j int) bool { return g.tcp[i] < g.tcp[j] })
		sort.Slice(g.udp, func(i, j int) bool { return g.udp[i] < g.udp[j] })
		out.groups = append(out.groups, *g)
	}
	sort.Slice(out.groups, func(i, j int) bool { return groupUserID(out.groups[i].group) < groupUserID(out.groups[j].group) })
	rateSlots(out.groups)
	return out
}

// groupUserID is the numeric user id of a validated group name.
func groupUserID(group string) uint64 {
	id, _ := strconv.ParseUint(strings.TrimPrefix(group, UserGroupPrefix), 10, 64)
	return id
}

// rateSlots gives every group a stable class slot: its user id when that is
// 1…maxRateSlots and still free, otherwise the highest free slot. Groups are
// sorted by user id, so the same set always gets the same slots.
func rateSlots(groups []rateGroupWant) {
	used := map[uint16]bool{}
	var rest []int
	for i := range groups {
		id := groupUserID(groups[i].group)
		if id >= 1 && id <= maxRateSlots && !used[uint16(id)] {
			groups[i].slot = uint16(id)
			used[uint16(id)] = true
			continue
		}
		rest = append(rest, i)
	}
	next := uint16(maxRateSlots)
	for _, i := range rest {
		for next > 0 && used[next] {
			next--
		}
		if next == 0 {
			break // validate() caps the groups at maxRateSlots
		}
		groups[i].slot = next
		used[next] = true
	}
}

// RateStatus is reported in the landing status (plan 161): "off" = nothing
// to enforce, "on" = applied, "error" = the kernel refused (Error says why;
// nothing of ours is left that could drop traffic).
type RateStatus struct {
	State   string   `json:"state"`
	Error   string   `json:"error,omitempty"`
	Groups  int      `json:"groups"`
	Ports   int      `json:"ports"`
	Devices []string `json:"devices,omitempty"`
}

// rateEngine is the kernel side (user_rate_linux.go; a stub elsewhere).
type rateEngine interface {
	// ensure makes the kernel match want; cheap when nothing changed. Objects
	// that are no longer wanted may stay a few seconds to drain (rateDrain).
	ensure(want rateWant, now time.Time) RateStatus
	// release removes everything at once (graceful agent stop).
	release() RateStatus
}

// applyRates brings the kernel in line with the desired state. Called after
// every apply and on the 10 s tick, independent of the Core's state (the
// shaping needs only the ports). Caller holds m.mu.
func (m *Manager) applyRates() {
	if m.ratesReleased {
		return // the agent is stopping
	}
	if m.rates == nil {
		m.rates = newRateEngine()
	}
	var want rateWant
	if m.loaded {
		want = m.desired.rateWant()
	}
	st := m.rates.ensure(want, m.clock())
	m.rateStatus = st
	m.logger().Record("landing user rate", "kernel", st.Error)
}

// ReleaseRates removes the kernel objects when the agent stops gracefully, so
// a rollback to an older agent (which knows nothing about them) or an
// uninstall leaves nothing behind; the next start rebuilds them from
// desired.json within its first tick. The landing Core keeps running
// meanwhile (unshaped while the agent is down). Waits at most timeout for a
// reconcile in progress, then gives up (the next start cleans up anyway).
func (m *Manager) ReleaseRates(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for !m.mu.TryLock() {
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer m.mu.Unlock()
	m.ratesReleased = true
	if m.rates == nil {
		return // never applied by this process
	}
	m.rateStatus = m.rates.release()
	m.logger().Record("landing user rate", "kernel", m.rateStatus.Error)
}
