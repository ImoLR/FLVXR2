package repo

import (
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
// Give-back rule (nothing new is stored): when the landing range moves,
// shrinks or the landing is disabled, the ports that leave the landing range
// return to the rule range only if they lie inside the hull [min, max] of the
// node's current rule range and no landing node / chain node / Reality guard
// still holds them. A landing range carved from the middle of the rule range
// (the normal case, e.g. 10000-65535 minus 30000-39999) therefore comes back
// whole; one that sat at an outer edge (rule 10000-65535, landing
// 60000-65535 → rule 10000-59999) is not re-added automatically because the
// panel cannot tell it apart from a landing range that never was part of the
// rule range. Ports are never invented outside the node's own bounds.

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
// newHi], 0/0 = landing disabled). held are ports landing/chain listeners
// still own; they are never given back. An empty spec stays empty.
func landingRuleSpec(spec string, oldLo, oldHi, newLo, newHi int, held map[int]bool) string {
	spans := parsePortSpans(spec)
	if len(spans) == 0 {
		return strings.TrimSpace(spec)
	}
	hullLo, hullHi := spans[0].lo, spans[len(spans)-1].hi
	if oldLo > 0 && oldHi >= oldLo {
		back := portSpansOverlap([]portSpan{{oldLo, oldHi}}, hullLo, hullHi)
		if newLo > 0 && newHi >= newLo {
			back = subtractPortSpan(back, newLo, newHi)
		}
		for p := range held {
			back = subtractPortSpan(back, p, p)
		}
		spans = mergePortSpans(append(spans, back...))
	}
	if newLo > 0 && newHi >= newLo {
		spans = subtractPortSpan(spans, newLo, newHi)
	}
	return formatPortSpans(spans)
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

// landingRangeRuleConflicts lists ports in [lo, hi] that forwards, tunnel
// listeners or other node listeners already use on the node (sorted).
func landingRangeRuleConflicts(tx *gorm.DB, nodeID int64, lo, hi int) ([]int, error) {
	set := map[int]bool{}
	for _, q := range []struct {
		m      interface{}
		column string
		where  string
	}{
		{&model.ForwardPort{}, "port", "node_id = ? AND port BETWEEN ? AND ?"},
		{&model.ChainTunnel{}, "port", "node_id = ? AND port IS NOT NULL AND port BETWEEN ? AND ?"},
		{&model.NodeDeployedInbound{}, "listen_port", "node_id = ? AND listen_port BETWEEN ? AND ?"},
		{&model.NodeRuntimeResource{}, "port", "node_id = ? AND status <> 'released' AND port BETWEEN ? AND ?"},
	} {
		var ports []int
		if err := tx.Model(q.m).Where(q.where, nodeID, lo, hi).Pluck(q.column, &ports).Error; err != nil {
			return nil, err
		}
		for _, p := range ports {
			set[p] = true
		}
	}
	out := make([]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Ints(out)
	return out, nil
}

func portListLabel(ports []int) string {
	shown := ports
	if len(shown) > 5 {
		shown = shown[:5]
	}
	parts := make([]string, len(shown))
	for i, p := range shown {
		parts[i] = strconv.Itoa(p)
	}
	label := strings.Join(parts, "、")
	if len(ports) > 5 {
		label += fmt.Sprintf(" 等共 %d 个", len(ports))
	} else {
		label += fmt.Sprintf("（共 %d 个）", len(ports))
	}
	return label
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
		conflicts, err := landingRangeRuleConflicts(tx, s.NodeID, newLo, newHi)
		if err != nil {
			return err
		}
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
			return fmt.Errorf("落地端口段 %s 内的端口 %s已被节点「%s」上的规则或隧道使用；不会移动用户的规则，请换一个落地端口段，或先修改这些规则的端口", formatPortRange(newLo, newHi), portListLabel(bad), node.Name)
		}
	}
	held := map[int]bool{}
	if oldLo > 0 {
		var err error
		if held, err = landingHeldPorts(tx, s); err != nil {
			return err
		}
	}
	next := landingRuleSpec(node.Port, oldLo, oldHi, newLo, newHi, held)
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
