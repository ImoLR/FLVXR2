package repo

import (
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"

	"go-backend/internal/store/model"
)

// Plan 147: an agent's public-IP report (HTTP /api/v1/node/report-ip from old
// agents, the encrypted WS message ReportPublicIP from new ones) never
// overwrites a node address. Until fork.88 the endpoint was rejected by the
// JWT middleware (bad_token after every reconnect); before that it overwrote
// server_ip / server_ip_v4 / server_ip_v6 with whatever the agent saw.
//
// What reads these fields: tunnels' entry in_ip and hop dial addresses prefer
// server_ip_v4 / server_ip_v6 and fall back to server_ip only when both are
// empty (buildTunnelInIP, node dial selection); new native landing servers copy
// server_ip_v4 (else server_ip) into public_host; node-sync rewrites tunnels
// when any of them changes; DDNS (plan 142) owns all three on its nodes.
//
// So a report may only fill a field that is empty AND whose filling cannot
// change any address that is dialed or shown:
//   - DDNS nodes: never touched.
//   - server_ip empty (no address at all): filled with the reported IPv4
//     (else IPv6), plus the matching family field.
//   - server_ip_v4 / server_ip_v6 empty: filled only when server_ip already is
//     exactly that literal — the field just records what server_ip says. A
//     node whose main address is a domain, another IP or the other family
//     keeps its fields empty (filling them would switch tunnels from the admin's
//     address to the reported one, or add a second family).
//   - Only global unicast public addresses (no private, CGNAT, loopback,
//     link-local, ULA, documentation or reserved ranges).
// Prod check 2026-10-24 (fork.88 DB copy): no node gets any field filled.

// PublicIPReportAddr returns the canonical text of a reportable public
// address of the given family ("v4" / "v6"), or "".
func PublicIPReportAddr(raw, family string) string {
	addr, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(raw), "[]"))
	if err != nil || addr.Zone() != "" {
		return ""
	}
	addr = addr.Unmap()
	if (family == "v4") != addr.Is4() {
		return ""
	}
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return ""
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(addr) {
			return ""
		}
	}
	return addr.String()
}

var nonPublicPrefixes = func() []netip.Prefix {
	out := []netip.Prefix{}
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "2001::/32", "2002::/16"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// NodePublicIPFill is the column → value set a report may write on n (empty
// = nothing to do). See the rule above.
func NodePublicIPFill(n *model.Node, ipv4, ipv6 string) map[string]interface{} {
	if n == nil || n.DDNSEnabled {
		return nil
	}
	v4, v6 := PublicIPReportAddr(ipv4, "v4"), PublicIPReportAddr(ipv6, "v6")
	if v4 == "" && v6 == "" {
		return nil
	}
	main := strings.TrimSpace(n.ServerIP)
	empty := func(v string, valid bool) bool { return !valid || strings.TrimSpace(v) == "" }
	out := map[string]interface{}{}
	if main == "" {
		main = v4
		if main == "" {
			main = v6
		}
		out["server_ip"] = main
	}
	if v4 != "" && empty(n.ServerIPV4.String, n.ServerIPV4.Valid) && SameNodeAddress(main, v4) {
		out["server_ip_v4"] = v4
	}
	if v6 != "" && empty(n.ServerIPV6.String, n.ServerIPV6.Valid) && SameNodeAddress(main, v6) {
		out["server_ip_v6"] = v6
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// FillNodePublicIPs applies an agent report under the fill-empty rule and
// returns the columns it wrote (none in the common case: one indexed read,
// no write). The update re-checks emptiness so a concurrent admin edit wins.
func (r *Repository) FillNodePublicIPs(nodeID int64, ipv4, ipv6 string) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var n model.Node
	if err := r.db.Select("id", "server_ip", "server_ip_v4", "server_ip_v6", "ddns_enabled").First(&n, nodeID).Error; err != nil {
		return nil, err
	}
	fill := NodePublicIPFill(&n, ipv4, ipv6)
	if len(fill) == 0 {
		return nil, nil
	}
	q := r.db.Model(&model.Node{}).Where("id = ? AND ddns_enabled = ?", nodeID, false)
	cols := make([]string, 0, len(fill))
	for _, col := range []string{"server_ip", "server_ip_v4", "server_ip_v6"} {
		if _, ok := fill[col]; ok {
			q = q.Where("(" + col + " IS NULL OR " + col + " = '')")
			cols = append(cols, col)
		}
	}
	res := q.Updates(fill)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return cols, nil
}

// PublicIPReportGate drops repeated reports per node before they reach the
// database: the same addresses again within SameEvery, or any report within
// MinEvery of the previous one.
type PublicIPReportGate struct {
	mu        sync.Mutex
	last      map[int64]publicIPReport
	MinEvery  time.Duration
	SameEvery time.Duration
}

type publicIPReport struct {
	v4, v6 string
	at     time.Time
}

func NewPublicIPReportGate() *PublicIPReportGate {
	return &PublicIPReportGate{last: map[int64]publicIPReport{}, MinEvery: time.Minute, SameEvery: 30 * time.Minute}
}

// Allow records the report and says whether it should be applied.
func (g *PublicIPReportGate) Allow(nodeID int64, ipv4, ipv6 string, now time.Time) bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	prev, ok := g.last[nodeID]
	if ok && (now.Sub(prev.at) < g.MinEvery || (prev.v4 == ipv4 && prev.v6 == ipv6 && now.Sub(prev.at) < g.SameEvery)) {
		return false
	}
	if len(g.last) > 4096 {
		// Bounded: node ids are few; a flood of bogus ids starts over.
		g.last = map[int64]publicIPReport{}
	}
	g.last[nodeID] = publicIPReport{v4: ipv4, v6: ipv6, at: now}
	return true
}
