package repo

import (
	"fmt"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

// Plan 318: the VPS vendors belong to the node (the VPS). A native landing
// server blocks the union of its node's vendors' sites; mmwx shares keep
// landing_server.vendor. landing_server.vendor of a native server mirrors the
// node's first vendor so fork.176 renders the same rule after a rollback.

// NodeVendorPick is a node's stored choice.
type NodeVendorPick struct {
	Keys  []string
	Other string
}

// Empty: neither a vendor nor 「其他」 (admin ⚠ 未设置).
func (p NodeVendorPick) Empty() bool { return len(p.Keys) == 0 && p.Other == "" }

// NodeVendorPickOf reads a node's columns.
func NodeVendorPickOf(n *model.Node) NodeVendorPick {
	if n == nil {
		return NodeVendorPick{Keys: []string{}}
	}
	return NodeVendorPick{Keys: landingegress.ParseNodeVendors(n.Vendors), Other: n.VendorOther}
}

// NodeVendorMirror is the landing_server.vendor value of a native server on
// the node: its first vendor (empty = none).
func NodeVendorMirror(n *model.Node) string {
	if keys := NodeVendorPickOf(n).Keys; len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// nodeVendorMigration (every start, idempotent): a native landing's plan-316
// vendor moves to its node when the node has no choice yet (neither vendors
// nor 「其他」). Nodes saved with a choice are never touched again.
func nodeVendorMigration(db *gorm.DB) error {
	var servers []model.LandingServer
	if err := db.Select("id", "node_id", "vendor").Where("source = ? AND node_id > 0 AND vendor <> ''", "native").Order("id").Find(&servers).Error; err != nil {
		return err
	}
	for _, s := range servers {
		if err := db.Model(&model.Node{}).Where("id = ? AND vendors = '' AND vendor_other = ''", s.NodeID).Update("vendors", s.Vendor).Error; err != nil {
			return err
		}
	}
	return nil
}

// NormalizeNodeVendorPick validates an admin pick against the current vendor
// list (see landingegress.NormalizeNodeVendors).
func (r *Repository) NormalizeNodeVendorPick(keys []string, other string) (NodeVendorPick, error) {
	vs, err := landingVendorSettings(r.db)
	if err != nil {
		return NodeVendorPick{}, err
	}
	k, o, err := landingegress.NormalizeNodeVendors(vs.Vendors, keys, other)
	if err != nil {
		return NodeVendorPick{}, err
	}
	return NodeVendorPick{Keys: k, Other: o}, nil
}

// SetNodeVendors stores a node's (already normalized) pick and the mirror on
// its native landing server. serverID = that server (0 = none) so the caller
// re-syncs it when changed.
func (r *Repository) SetNodeVendors(nodeID int64, pick NodeVendorPick) (serverID int64, changed bool, err error) {
	raw := landingegress.FormatNodeVendors(pick.Keys)
	err = r.db.Transaction(func(tx *gorm.DB) error {
		var n model.Node
		if err := tx.Select("id", "vendors", "vendor_other").First(&n, nodeID).Error; err != nil {
			return err
		}
		var servers []model.LandingServer
		if err := tx.Select("id").Where("source = ? AND node_id = ?", "native", nodeID).Order("id").Limit(1).Find(&servers).Error; err != nil {
			return err
		}
		if len(servers) > 0 {
			serverID = servers[0].ID
		}
		if n.Vendors == raw && n.VendorOther == pick.Other {
			return nil
		}
		changed = true
		if err := tx.Model(&model.Node{}).Where("id = ?", nodeID).Updates(map[string]any{"vendors": raw, "vendor_other": pick.Other}).Error; err != nil {
			return err
		}
		mirror := ""
		if len(pick.Keys) > 0 {
			mirror = pick.Keys[0]
		}
		return tx.Model(&model.LandingServer{}).Where("source = ? AND node_id = ?", "native", nodeID).Update("vendor", mirror).Error
	})
	return serverID, changed, err
}

// NodeVendorRef is one node in the vendor admin views.
type NodeVendorRef struct {
	ID       int64
	Name     string
	IsRemote bool
	Pick     NodeVendorPick
}

// NodeVendorRefs lists every node with its pick (id order).
func (r *Repository) NodeVendorRefs() ([]NodeVendorRef, error) {
	var nodes []model.Node
	if err := r.db.Select("id", "name", "is_remote", "vendors", "vendor_other").Order("inx ASC, id ASC").Find(&nodes).Error; err != nil {
		return nil, err
	}
	out := make([]NodeVendorRef, 0, len(nodes))
	for i := range nodes {
		out = append(out, NodeVendorRef{ID: nodes[i].ID, Name: nodes[i].Name, IsRemote: nodes[i].IsRemote != 0, Pick: NodeVendorPickOf(&nodes[i])})
	}
	return out, nil
}

// checkVendorListAgainstNodes: a vendor list may only be saved when every
// node's picked keys still exist and each node's union still fits one rule.
func checkVendorListAgainstNodes(db *gorm.DB, list []landingegress.Vendor) error {
	var nodes []model.Node
	if err := db.Select("id", "name", "vendors").Where("vendors <> ''").Order("id").Find(&nodes).Error; err != nil {
		return err
	}
	for i := range nodes {
		keys := NodeVendorPickOf(&nodes[i]).Keys
		v, missing, ok := landingegress.CombineVendors(list, keys)
		if len(missing) > 0 {
			return fmt.Errorf("节点「%s」还在使用被删除的厂商，请先在节点页编辑这个节点改掉", nodes[i].Name)
		}
		if ok && len(v.Domains) > landingegress.MaxRuleVendorDomains {
			return fmt.Errorf("节点「%s」所选厂商的域名合计 %d 个，超过上限 %d 个", nodes[i].Name, len(v.Domains), landingegress.MaxRuleVendorDomains)
		}
	}
	return nil
}
