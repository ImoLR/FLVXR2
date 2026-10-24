package repo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Reality guard ports (plan 127). Every VLESS Reality listener of a native
// landing server (public landing nodes and loopback chain copies) gets a
// loopback helper port; listeners with the same dest and serverNames share
// one. The ports are stored on the rows (landing_node.guard_port,
// forward_chain_node.guard_port), so deleting a node or copy frees its port
// and nothing is left behind; every port-conflict check counts them.

// landingGuardPortsOnNode lists the guard ports of every native landing
// server and chain copy on a node.
func landingGuardPortsOnNode(tx *gorm.DB, nodeID int64) ([]int, error) {
	var ports []int
	if err := tx.Model(&model.LandingNode{}).Joins("JOIN landing_server ON landing_server.id = landing_node.landing_server_id").
		Where("landing_server.node_id = ? AND landing_server.source = ? AND landing_node.guard_port > 0", nodeID, "native").
		Pluck("landing_node.guard_port", &ports).Error; err != nil {
		return nil, err
	}
	var chain []int
	if err := tx.Model(&model.ForwardChainNode{}).Where("node_id = ? AND guard_port > 0", nodeID).Pluck("guard_port", &chain).Error; err != nil {
		return nil, err
	}
	return append(ports, chain...), nil
}

func guardGroupKey(dest string, names []string) string {
	norm := make([]string, 0, len(names))
	for _, n := range names {
		norm = append(norm, strings.ToLower(strings.TrimSpace(n)))
	}
	sort.Strings(norm)
	return strings.TrimSpace(dest) + "|" + strings.Join(norm, ",")
}

type guardMember struct {
	chain bool
	id    int64 // landing_node.id or forward_chain_node.id
	tag   string
	key   string
	port  int
}

// EnsureLandingGuardPorts makes the stored guard ports of a native server
// match its switch: on → every Reality listener has the port of its
// (dest, serverNames) group, allocated once and kept stable; off → all ports
// released. It returns inbound tag → guard port for the desired state, plus
// a warning when the port range has no room (those listeners stay unguarded).
func (r *Repository) EnsureLandingGuardPorts(serverID int64) (map[string]int, []string, error) {
	out := map[string]int{}
	var warnings []string
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var s model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, serverID).Error; err != nil {
			return err
		}
		if s.Source != "native" {
			return nil
		}
		var nodes []model.LandingNode
		if err := tx.Where("landing_server_id = ? AND protocol = ?", serverID, "vless_reality_vision").Order("id").Find(&nodes).Error; err != nil {
			return err
		}
		var copies []model.ForwardChainNode
		if err := tx.Where("landing_server_id = ?", serverID).Order("id").Find(&copies).Error; err != nil {
			return err
		}
		if s.RealityGuardOff {
			if err := tx.Model(&model.LandingNode{}).Where("landing_server_id = ? AND guard_port <> 0", serverID).Update("guard_port", 0).Error; err != nil {
				return err
			}
			return tx.Model(&model.ForwardChainNode{}).Where("landing_server_id = ? AND guard_port <> 0", serverID).Update("guard_port", 0).Error
		}
		members := make([]guardMember, 0, len(nodes)+len(copies))
		for _, n := range nodes {
			var names []string
			_ = json.Unmarshal([]byte(n.ServerNames), &names)
			members = append(members, guardMember{id: n.ID, tag: n.InboundTag, key: guardGroupKey(n.Dest, names), port: n.GuardPort})
		}
		if len(copies) > 0 {
			ids := make([]int64, 0, len(copies))
			for _, c := range copies {
				ids = append(ids, c.ChainID)
			}
			var chains []model.ForwardChain
			if err := tx.Where("id IN ?", ids).Find(&chains).Error; err != nil {
				return err
			}
			byID := map[int64]model.ForwardChain{}
			for _, c := range chains {
				byID[c.ID] = c
			}
			for _, c := range copies {
				ch, ok := byID[c.ChainID]
				if !ok {
					continue
				}
				members = append(members, guardMember{chain: true, id: c.ID, tag: fmt.Sprintf("flvxl-%d", model.ChainAgentIDBase+c.ID), key: guardGroupKey(ch.Dest, ChainServerNames(ch)), port: c.GuardPort})
			}
		}
		if len(members) == 0 {
			return nil
		}
		// Steady state (every sync): all listeners already carry their group's
		// port, in range, one port per group → nothing to query or write. Every
		// allocator counts guard ports, so a stored port cannot have become a
		// conflict since it was handed out.
		if steady, ports := guardSteady(members, s); steady {
			for k, v := range ports {
				out[k] = v
			}
			return nil
		}
		// Everything a guard must avoid: the node's rule range, landing and
		// chain listeners, forwards, tunnels, deployments, mmwx-shared ports on
		// the same host and guards of other servers on the node.
		used, err := usedPortsBaseTx(tx, s, 0)
		if err != nil {
			return err
		}
		shared, err := sharedLandingPortsOnNodeHost(tx, s.NodeID)
		if err != nil {
			return err
		}
		var others []int
		if err := tx.Model(&model.LandingNode{}).Joins("JOIN landing_server ON landing_server.id = landing_node.landing_server_id").
			Where("landing_server.node_id = ? AND landing_server.id <> ? AND landing_node.guard_port > 0", s.NodeID, serverID).Pluck("landing_node.guard_port", &others).Error; err != nil {
			return err
		}
		var otherChain []int
		if err := tx.Model(&model.ForwardChainNode{}).Where("node_id = ? AND landing_server_id <> ? AND guard_port > 0", s.NodeID, serverID).Pluck("guard_port", &otherChain).Error; err != nil {
			return err
		}
		for _, list := range [][]int{shared, others, otherChain} {
			for _, p := range list {
				used[p] = true
			}
		}
		// Keep each group's port while it stays valid (in range, conflict-free,
		// not claimed by another group); the first member decides.
		inRange := func(p int) bool {
			if s.ChainOnly {
				return p >= 1024 && p <= 65535
			}
			return p >= s.PortMin && p <= s.PortMax
		}
		groupPort := map[string]int{}
		portGroup := map[int]string{}
		for _, m := range members {
			if m.port <= 0 || !inRange(m.port) || used[m.port] {
				continue
			}
			if _, ok := groupPort[m.key]; ok {
				continue
			}
			if _, taken := portGroup[m.port]; taken {
				continue
			}
			groupPort[m.key], portGroup[m.port] = m.port, m.key
		}
		lo, hi := s.PortMin, s.PortMax
		if s.ChainOnly {
			lo, hi = 1024, 65535
		}
		next := hi
		alloc := func() int {
			for ; next >= lo; next-- {
				if !used[next] && portGroup[next] == "" {
					p := next
					next--
					return p
				}
			}
			return 0
		}
		full := false
		for _, m := range members {
			if _, ok := groupPort[m.key]; ok {
				continue
			}
			p := alloc()
			if p == 0 {
				full = true
				groupPort[m.key] = 0
				continue
			}
			groupPort[m.key], portGroup[p] = p, m.key
		}
		if full {
			warnings = append(warnings, "落地端口段没有空闲端口给 Reality 防盗，部分 Reality 节点暂未开启防盗")
		}
		for _, m := range members {
			p := groupPort[m.key]
			if p != m.port {
				var res *gorm.DB
				if m.chain {
					res = tx.Model(&model.ForwardChainNode{}).Where("id = ?", m.id).Update("guard_port", p)
				} else {
					res = tx.Model(&model.LandingNode{}).Where("id = ?", m.id).Update("guard_port", p)
				}
				if res.Error != nil {
					return res.Error
				}
			}
			if p > 0 {
				out[m.tag] = p
			}
		}
		return nil
	})
	return out, warnings, err
}

// SetLandingRealityGuard switches the Reality guard of a server (admin).
func (r *Repository) SetLandingRealityGuard(serverID int64, enabled bool) error {
	return r.db.Model(&model.LandingServer{}).Where("id = ?", serverID).Update("reality_guard_off", !enabled).Error
}

// RecordLandingGuardState stores the agent's guard report when it changed.
func (r *Repository) RecordLandingGuardState(serverID int64, state string, nodes int) error {
	return r.db.Model(&model.LandingServer{}).Where("id = ? AND (guard_state <> ? OR guard_nodes <> ?)", serverID, state, nodes).
		Updates(map[string]any{"guard_state": state, "guard_nodes": nodes}).Error
}

// guardSteady reports whether the stored ports need no change and returns
// tag → port in that case.
func guardSteady(members []guardMember, s model.LandingServer) (bool, map[string]int) {
	groupPort, portGroup, out := map[string]int{}, map[int]string{}, map[string]int{}
	for _, m := range members {
		if m.port <= 0 || (s.ChainOnly && (m.port < 1024 || m.port > 65535)) || (!s.ChainOnly && (m.port < s.PortMin || m.port > s.PortMax)) {
			return false, nil
		}
		if p, ok := groupPort[m.key]; ok && p != m.port {
			return false, nil
		}
		if k, ok := portGroup[m.port]; ok && k != m.key {
			return false, nil
		}
		groupPort[m.key], portGroup[m.port], out[m.tag] = m.port, m.key, m.port
	}
	return true, out
}

// SetLandingCoreUpgrade records the admin's one-shot confirmation to restart a
// native server's outdated Core onto the pinned binary (plan 129); a no-op
// unless the agent currently reports an outdated Core.
func (r *Repository) SetLandingCoreUpgrade(serverID int64) error {
	return r.db.Model(&model.LandingServer{}).Where("id = ? AND source = ? AND core_stale_epoch <> ?", serverID, "native", "").Update("core_upgrade_ok", true).Error
}

// RecordLandingCoreUpdate stores the epoch of an outdated running Core ("" =
// current) when it changed. A different epoch (the Core restarted) consumes a
// pending confirmation, so a later outdated Core asks the admin again.
func (r *Repository) RecordLandingCoreUpdate(serverID int64, staleEpoch string) error {
	return r.db.Model(&model.LandingServer{}).Where("id = ? AND core_stale_epoch <> ?", serverID, staleEpoch).
		Updates(map[string]any{"core_stale_epoch": staleEpoch, "core_upgrade_ok": false}).Error
}
