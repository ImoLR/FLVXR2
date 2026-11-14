package repo

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// Plan 139: a native landing's port range is carved out of its node's rule
// port range (node.port) so forwards never claim landing ports and
// landingFreePort always has ports to hand out.
//
// Give-back rule (plan 147): every carve is recorded on the landing server
// (landing_server.rule_carved, rule-spec syntax). When the landing range
// moves, shrinks or the landing is disabled, a port that leaves the landing
// range returns to the rule range when the landing had carved it (recorded)
// or — for landings carved before the record existed — when it lies inside the
// hull [min, max] of the node's current rule range; in both cases only while
// no landing node / chain node / Reality guard still holds it (held ports stay
// recorded and come back on a later change). So a range carved from the
// middle (10000-65535 minus 30000-39999) and one carved at an outer edge
// (rule 10000-65535, landing 60000-65535 → 10000-59999) both come back whole,
// while a landing range that never was part of the rule range (nothing
// recorded, outside the hull) is never added: ports are never invented.

type portSpan struct{ lo, hi int }

// parsePortSpans parses a rule port spec ("10000-29999,31000-65535", single
// ports allowed) into sorted, merged spans. Invalid parts are skipped the same
// way parsePortRangeSpec skips them.
func parsePortSpans(spec string) []portSpan {
	var spans []portSpan
	for _, part := range strings.Split(strings.TrimSpace(spec), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi := 0, 0
		if strings.Contains(part, "-") {
			r := strings.SplitN(part, "-", 2)
			a, err1 := strconv.Atoi(strings.TrimSpace(r[0]))
			b, err2 := strconv.Atoi(strings.TrimSpace(r[1]))
			if err1 != nil || err2 != nil || a <= 0 || b <= 0 {
				continue
			}
			if b < a {
				a, b = b, a
			}
			lo, hi = a, b
		} else {
			p, err := strconv.Atoi(part)
			if err != nil || p <= 0 {
				continue
			}
			lo, hi = p, p
		}
		spans = append(spans, portSpan{lo, hi})
	}
	return mergePortSpans(spans)
}

func mergePortSpans(spans []portSpan) []portSpan {
	if len(spans) == 0 {
		return nil
	}
	sorted := append([]portSpan(nil), spans...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].lo < sorted[j].lo })
	out := []portSpan{sorted[0]}
	for _, s := range sorted[1:] {
		last := &out[len(out)-1]
		if s.lo <= last.hi+1 {
			if s.hi > last.hi {
				last.hi = s.hi
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

func formatPortSpans(spans []portSpan) string {
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		if s.lo == s.hi {
			parts = append(parts, strconv.Itoa(s.lo))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", s.lo, s.hi))
		}
	}
	return strings.Join(parts, ",")
}

func subtractPortSpan(spans []portSpan, lo, hi int) []portSpan {
	if lo <= 0 || hi < lo {
		return spans
	}
	out := make([]portSpan, 0, len(spans)+1)
	for _, s := range spans {
		if s.hi < lo || s.lo > hi {
			out = append(out, s)
			continue
		}
		if s.lo < lo {
			out = append(out, portSpan{s.lo, lo - 1})
		}
		if s.hi > hi {
			out = append(out, portSpan{hi + 1, s.hi})
		}
	}
	return out
}

func portSpansOverlap(spans []portSpan, lo, hi int) []portSpan {
	var out []portSpan
	for _, s := range spans {
		a, b := s.lo, s.hi
		if a < lo {
			a = lo
		}
		if b > hi {
			b = hi
		}
		if a <= b {
			out = append(out, portSpan{a, b})
		}
	}
	return out
}

func formatPortRange(lo, hi int) string {
	if lo == hi {
		return strconv.Itoa(lo)
	}
	return fmt.Sprintf("%d-%d", lo, hi)
}

// landingRuleSpec computes the node rule spec after the native landing range
// changes from old ([oldLo, oldHi], 0/0 = no active range) to new ([newLo,
// newHi], 0/0 = landing disabled), plus the new carve record. carved is the
// record before the change; held are ports landing/chain listeners still own:
// they are never given back (and stay recorded). An empty spec stays empty.
func landingRuleSpec(spec, carved string, oldLo, oldHi, newLo, newHi int, held map[int]bool) (string, string) {
	spans := parsePortSpans(spec)
	record := parsePortSpans(carved)
	if len(spans) == 0 {
		return strings.TrimSpace(spec), strings.TrimSpace(carved)
	}
	hullLo, hullHi := spans[0].lo, spans[len(spans)-1].hi
	newActive := newLo > 0 && newHi >= newLo
	if oldLo > 0 && oldHi >= oldLo {
		back := portSpansOverlap([]portSpan{{oldLo, oldHi}}, hullLo, hullHi)
		back = mergePortSpans(append(back, portSpansOverlap(record, oldLo, oldHi)...))
		if newActive {
			back = subtractPortSpan(back, newLo, newHi)
		}
		for p := range held {
			back = subtractPortSpan(back, p, p)
		}
		spans = mergePortSpans(append(spans, back...))
		for _, b := range back {
			record = subtractPortSpan(record, b.lo, b.hi)
		}
	}
	if newActive {
		record = mergePortSpans(append(record, portSpansOverlap(spans, newLo, newHi)...))
		spans = subtractPortSpan(spans, newLo, newHi)
	}
	return formatPortSpans(spans), formatPortSpans(mergePortSpans(record))
}

// landingHeldPorts are ports inside a native landing's range that its own
// listeners keep using, also while the server is disabled.
func landingHeldPorts(tx *gorm.DB, s *model.LandingServer) (map[int]bool, error) {
	held := map[int]bool{}
	var ports []int
	if err := tx.Model(&model.LandingNode{}).Where("landing_server_id = ?", s.ID).Pluck("port", &ports).Error; err != nil {
		return nil, err
	}
	var chain []int
	if err := tx.Model(&model.ForwardChainNode{}).Where("landing_server_id = ?", s.ID).Pluck("port", &chain).Error; err != nil {
		return nil, err
	}
	guards, err := landingGuardPortsOnNode(tx, s.NodeID)
	if err != nil {
		return nil, err
	}
	for _, list := range [][]int{ports, chain, guards} {
		for _, p := range list {
			held[p] = true
		}
	}
	return held, nil
}

// landingPortHolders maps a node port to who uses it: forwards (规则), tunnel
// listeners (隧道), deployed inbounds and runtime resources — the sources a
// landing range must stay clear of. lo/hi = 0 loads the whole node at once
// (plan 216: the enable block search scans this map in memory).
func landingPortHolders(tx *gorm.DB, nodeID int64, lo, hi int) (map[int][]string, error) {
	type row struct {
		Port int
		Name sql.NullString
		ID   int64
	}
	holders := map[int][]string{}
	for _, q := range []struct {
		table, port, sel, join, where, kind string
	}{
		{"forward_port", "forward_port.port", "forward_port.port AS port, forward.name AS name, forward_port.forward_id AS id", "LEFT JOIN forward ON forward.id = forward_port.forward_id", "forward_port.node_id = ?", "规则"},
		{"chain_tunnel", "chain_tunnel.port", "chain_tunnel.port AS port, tunnel.name AS name, chain_tunnel.tunnel_id AS id", "LEFT JOIN tunnel ON tunnel.id = chain_tunnel.tunnel_id", "chain_tunnel.node_id = ? AND chain_tunnel.port IS NOT NULL", "隧道"},
		{"node_deployed_inbound", "listen_port", "listen_port AS port, display_name AS name, id", "", "node_id = ?", "入站"},
		{"node_runtime_resource", "port", "port, resource_key AS name, id", "", "node_id = ? AND status <> 'released' AND port > 0", "运行资源"},
		{"line_monitor_port", "port", "port, state AS name, id", "", "node_id = ?", "线路监测检测端口"}, // plan 255
	} {
		db := tx.Table(q.table).Select(q.sel)
		if q.join != "" {
			db = db.Joins(q.join)
		}
		db = db.Where(q.where, nodeID)
		if lo > 0 {
			db = db.Where(q.port+" BETWEEN ? AND ?", lo, hi)
		}
		var rows []row
		if err := db.Scan(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			label := landingHolderLabel(q.kind, r.Name.String, r.ID)
			dup := false
			for _, l := range holders[r.Port] {
				dup = dup || l == label
			}
			if !dup {
				holders[r.Port] = append(holders[r.Port], label)
			}
		}
	}
	return holders, nil
}

func landingHolderLabel(kind, name string, id int64) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Sprintf("%s #%d", kind, id)
	}
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40]) + "…"
	}
	return fmt.Sprintf("%s「%s」", kind, name)
}

func sortedPorts(holders map[int][]string) []int {
	out := make([]int, 0, len(holders))
	for p := range holders {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// landingRangeRuleConflicts lists ports in [lo, hi] that forwards, tunnel
// listeners or other node listeners already use on the node (sorted).
func landingRangeRuleConflicts(tx *gorm.DB, nodeID int64, lo, hi int) ([]int, error) {
	holders, err := landingPortHolders(tx, nodeID, lo, hi)
	if err != nil {
		return nil, err
	}
	return sortedPorts(holders), nil
}

// portHoldersLabel names who holds the given ports, grouped by holder in port
// order: 「端口 34060 被隧道「X」使用；端口 30010、30011 被规则「Y」使用」 —
// at most 3 holders, then 「等」.
func portHoldersLabel(ports []int, holders map[int][]string) string {
	var order []string
	byLabel := map[string][]int{}
	for _, p := range ports {
		labels := holders[p]
		if len(labels) == 0 {
			labels = []string{"其他监听"}
		}
		for _, l := range labels {
			if _, ok := byLabel[l]; !ok {
				order = append(order, l)
			}
			byLabel[l] = append(byLabel[l], p)
		}
	}
	shown := order
	if len(shown) > 3 {
		shown = shown[:3]
	}
	parts := make([]string, 0, len(shown))
	for _, l := range shown {
		ps := byLabel[l]
		nums := make([]string, 0, 3)
		for i, p := range ps {
			if i == 3 {
				break
			}
			nums = append(nums, strconv.Itoa(p))
		}
		label := strings.Join(nums, "、") + " "
		if len(ps) > 3 {
			label += fmt.Sprintf("等 %d 个", len(ps))
		}
		sep := ""
		if !strings.HasSuffix(l, "」") {
			sep = " "
		}
		parts = append(parts, fmt.Sprintf("端口 %s被%s%s使用", label, l, sep))
	}
	out := strings.Join(parts, "；")
	if len(order) > 3 {
		out += " 等"
	}
	return out
}

// Plan 216: enable keeps the default 30000-39999 when it is free; otherwise it
// takes the first free 1000-port block, aligned to 1000 (30000-30999 …
// 64000-64999, then 10000-10999 … 29000-29999).
const (
	landingDefaultPortMin = 30000
	landingDefaultPortMax = 39999
	landingPortBlock      = 1000
)

func landingBlockCandidates() [][2]int {
	var out [][2]int
	for _, start := range [][2]int{{30000, 65535}, {10000, 29999}} {
		for lo := start[0]; lo+landingPortBlock-1 <= start[1]; lo += landingPortBlock {
			out = append(out, [2]int{lo, lo + landingPortBlock - 1})
		}
	}
	return out
}

func portsIn(sorted []int, lo, hi int) bool {
	i := sort.SearchInts(sorted, lo)
	return i < len(sorted) && sorted[i] <= hi
}

// pickNativeLandingRange chooses the range a native landing reserves when it
// is enabled (fresh, or chain-only → public). [prefLo, prefHi] is kept when
// no forward / tunnel / inbound / runtime resource on the node uses a port in
// it (the check applyNativeLandingRange makes). Otherwise the first aligned
// 1000-port block that lies wholly inside the node's rule range (node.port;
// empty = unrestricted), leaves some rule ports, holds no used port, no
// Reality guard port and no other landing's chain-node port, and contains
// every listener the server already owns (own). note explains a non-default
// pick. One load of the node's ports, then an in-memory scan.
func pickNativeLandingRange(tx *gorm.DB, node model.Node, serverID int64, prefLo, prefHi int, own []int) (int, int, string, error) {
	holders, err := landingPortHolders(tx, node.ID, 0, 0)
	if err != nil {
		return 0, 0, "", err
	}
	used := sortedPorts(holders)
	if !portsIn(used, prefLo, prefHi) {
		return prefLo, prefHi, "", nil
	}
	busy := map[int]bool{}
	for p := range holders {
		busy[p] = true
	}
	guards, err := landingGuardPortsOnNode(tx, node.ID)
	if err != nil {
		return 0, 0, "", err
	}
	var chain []int
	if err := tx.Model(&model.ForwardChainNode{}).Where("node_id = ? AND landing_server_id <> ?", node.ID, serverID).Pluck("port", &chain).Error; err != nil {
		return 0, 0, "", err
	}
	for _, list := range [][]int{guards, chain} {
		for _, p := range list {
			busy[p] = true
		}
	}
	busyList := make([]int, 0, len(busy))
	for p := range busy {
		busyList = append(busyList, p)
	}
	sort.Ints(busyList)
	spans := parsePortSpans(node.Port)
	for _, c := range landingBlockCandidates() {
		lo, hi := c[0], c[1]
		if lo == prefLo && hi == prefHi {
			continue
		}
		if len(spans) > 0 {
			in := portSpansOverlap(spans, lo, hi)
			if len(in) != 1 || in[0].lo != lo || in[0].hi != hi || len(subtractPortSpan(spans, lo, hi)) == 0 {
				continue
			}
		}
		if portsIn(busyList, lo, hi) {
			continue
		}
		fits := true
		for _, p := range own {
			fits = fits && p >= lo && p <= hi
		}
		if !fits {
			continue
		}
		note := fmt.Sprintf("端口段 %s（%s 有端口被占用：%s）", formatPortRange(lo, hi), formatPortRange(prefLo, prefHi), portHoldersLabel(conflictsIn(used, prefLo, prefHi), holders))
		return lo, hi, note, nil
	}
	return 0, 0, "", fmt.Errorf("节点「%s」找不到空闲的 %d 端口段来启用落地：默认端口段 %s 内%s，其他端口段也有端口被占用或不在节点规则端口段 %s 内。请先修改这些规则或隧道的端口，或在节点编辑里调整规则端口段", node.Name, landingPortBlock, formatPortRange(prefLo, prefHi), portHoldersLabel(conflictsIn(used, prefLo, prefHi), holders), node.Port)
}

func conflictsIn(sorted []int, lo, hi int) []int {
	i := sort.SearchInts(sorted, lo)
	j := sort.SearchInts(sorted, hi+1)
	return sorted[i:j]
}

// applyNativeLandingRange moves the node rule range in step with a native
// landing range change (inside the caller's transaction). oldLo/oldHi = 0
// when the landing was not active before; newLo/newHi = 0 when it is not
// active afterwards. Chain-only exits (plan 108) are left alone: they never
// reserved a public range.
func applyNativeLandingRange(tx *gorm.DB, s *model.LandingServer, oldLo, oldHi, newLo, newHi int) error {
	if s.Source != "native" || s.NodeID <= 0 {
		return nil
	}
	var node model.Node
	if err := tx.Select("id", "name", "port").First(&node, s.NodeID).Error; err != nil {
		return errors.New("落地服务器节点不存在")
	}
	if newLo > 0 {
		// Ports newly entering the landing range, and rule-range ports about to
		// be carved, must not be in use by the node's rules or tunnels.
		holders, err := landingPortHolders(tx, s.NodeID, newLo, newHi)
		if err != nil {
			return err
		}
		conflicts := sortedPorts(holders)
		spans := parsePortSpans(node.Port)
		var bad []int
		for _, p := range conflicts {
			inOld := oldLo > 0 && p >= oldLo && p <= oldHi
			inRule := len(portSpansOverlap(spans, p, p)) > 0
			if !inOld || inRule {
				bad = append(bad, p)
			}
		}
		if len(bad) > 0 {
			return fmt.Errorf("落地端口段 %s 内有 %d 个端口已被节点「%s」上的规则或隧道使用：%s。不会移动用户的规则，请换一个落地端口段，或先修改这些规则的端口", formatPortRange(newLo, newHi), len(bad), node.Name, portHoldersLabel(bad, holders))
		}
	}
	held := map[int]bool{}
	if oldLo > 0 {
		var err error
		if held, err = landingHeldPorts(tx, s); err != nil {
			return err
		}
	}
	next, carved := landingRuleSpec(node.Port, s.RuleCarved, oldLo, oldHi, newLo, newHi, held)
	if carved != s.RuleCarved {
		s.RuleCarved = carved
		if s.ID > 0 {
			if err := tx.Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("rule_carved", carved).Error; err != nil {
				return err
			}
		}
	}
	if next == strings.TrimSpace(node.Port) || formatPortSpans(parsePortSpans(node.Port)) == next {
		// Unchanged ports keep the admin's own spelling of the spec.
		return nil
	}
	if next == "" {
		return fmt.Errorf("落地端口段 %s 覆盖了节点「%s」的全部规则端口 %s，请缩小落地端口段", formatPortRange(newLo, newHi), node.Name, node.Port)
	}
	return tx.Model(&model.Node{}).Where("id = ?", node.ID).Update("port", next).Error
}

// activeLandingRange is the range a native landing reserves on its node.
func activeLandingRange(s *model.LandingServer) (int, int) {
	if s == nil || s.Source != "native" || s.Status != "active" || s.ChainOnly {
		return 0, 0
	}
	return s.PortMin, s.PortMax
}
