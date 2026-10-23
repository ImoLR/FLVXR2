package repo

import (
	"database/sql"
	"errors"
	"sort"
	"strings"

	"gorm.io/gorm"

	"go-backend/internal/store/model"
)

type NodeEntryAddressSyncResult struct {
	TunnelIDs  []int64
	ForwardIDs []int64
	// BindForwardIDs lists rules whose explicit listen IP changed and must be
	// redeployed; ForwardIDs also contains rules that only display the address.
	BindForwardIDs        []int64
	EntryAddressesUpdated int
}

// RewriteNodeEntryAddresses updates saved ingress addresses after the node row
// has been updated. The callback uses the same address builder as tunnel saves.
func (r *Repository) RewriteNodeEntryAddresses(oldNode, newNode *model.Node, buildInIP func([]model.Node, string) string) (NodeEntryAddressSyncResult, error) {
	var result NodeEntryAddressSyncResult
	if r == nil || r.db == nil {
		return result, errors.New("repository not initialized")
	}
	if oldNode == nil || newNode == nil || oldNode.ID != newNode.ID {
		return result, errors.New("invalid node address update")
	}
	oldAddresses := nodeIngressAddresses(oldNode)
	newAddresses := nodeIngressAddresses(newNode)
	extraReplacements := nodeExtraIPReplacements(oldNode, newNode)
	if oldAddresses == newAddresses && len(extraReplacements) == 0 {
		return result, nil
	}
	replacements := make(map[string]string)
	for i, oldAddress := range oldAddresses {
		if oldAddress == "" {
			continue
		}
		// Explicit address families take precedence when server_ip duplicates one.
		if _, exists := replacements[oldAddress]; !exists {
			replacements[oldAddress] = newAddresses[i]
		}
	}
	forwardIDs := make(map[int64]struct{})
	bindForwardIDs := make(map[int64]struct{})
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var tunnels []model.Tunnel
		entryTunnelIDs := tx.Model(&model.ChainTunnel{}).Select("tunnel_id").
			Where("node_id = ? AND chain_type = ?", newNode.ID, "1")
		if err := tx.Where("id IN (?)", entryTunnelIDs).Order("id ASC").Find(&tunnels).Error; err != nil {
			return err
		}
		for _, tunnel := range tunnels {
			inIP := rewriteNodeAddressTokens(tunnel.InIP.String, replacements, extraReplacements)
			if inIP == "" {
				if buildInIP == nil {
					return errors.New("tunnel ingress address builder unavailable")
				}
				var entries []model.Node
				if err := tx.Model(&model.Node{}).Select("node.*").
					Joins("JOIN chain_tunnel ON chain_tunnel.node_id = node.id").
					Where("chain_tunnel.tunnel_id = ? AND chain_tunnel.chain_type = ?", tunnel.ID, "1").
					Order("chain_tunnel.inx ASC, chain_tunnel.id ASC").Find(&entries).Error; err != nil {
					return err
				}
				inIP = buildInIP(entries, tunnel.IPPreference)
			}
			if inIP == tunnel.InIP.String {
				continue
			}
			if err := tx.Model(&model.Tunnel{}).Where("id = ?", tunnel.ID).Update("in_ip", inIP).Error; err != nil {
				return err
			}
			result.TunnelIDs = append(result.TunnelIDs, tunnel.ID)
			result.EntryAddressesUpdated++
		}

		var ports []model.ForwardPort
		if err := tx.Where("node_id = ?", newNode.ID).Order("id ASC").Find(&ports).Error; err != nil {
			return err
		}
		for _, port := range ports {
			// Empty values inherit the tunnel address; keep that inheritance intact.
			if !port.InIP.Valid || strings.TrimSpace(port.InIP.String) == "" {
				continue
			}
			// Listen IPs stay literal: an address replaced by a hostname (a
			// DDNS domain, plan 142) keeps its bind IP.
			inIP := rewriteNodeAddressTokens(port.InIP.String, literalReplacements(replacements), literalReplacements(extraReplacements))
			if inIP == port.InIP.String {
				continue
			}
			// An emptied value falls back to the default listen-all address.
			if err := tx.Model(&model.ForwardPort{}).Where("id = ?", port.ID).Update("in_ip", inIP).Error; err != nil {
				return err
			}
			forwardIDs[port.ForwardID] = struct{}{}
			bindForwardIDs[port.ForwardID] = struct{}{}
			result.EntryAddressesUpdated++
		}
		if len(result.TunnelIDs) > 0 {
			var inheritedForwardIDs []int64
			if err := tx.Model(&model.ForwardPort{}).
				Joins("JOIN forward ON forward.id = forward_port.forward_id").
				Where("forward.tunnel_id IN (?) AND (forward_port.in_ip IS NULL OR TRIM(forward_port.in_ip) = '')", result.TunnelIDs).
				Distinct("forward_port.forward_id").Pluck("forward_port.forward_id", &inheritedForwardIDs).Error; err != nil {
				return err
			}
			for _, id := range inheritedForwardIDs {
				forwardIDs[id] = struct{}{}
			}
		}
		return nil
	})
	if err != nil {
		return NodeEntryAddressSyncResult{}, err
	}
	result.ForwardIDs = sortedIDSet(forwardIDs)
	result.BindForwardIDs = sortedIDSet(bindForwardIDs)
	return result, nil
}

// literalReplacements drops replacements whose target is a hostname.
func literalReplacements(in map[string]string) map[string]string {
	if len(in) == 0 {
		return in
	}
	out := make(map[string]string, len(in))
	for from, to := range in {
		if to == "" || parseLiteralIP(to) != nil {
			out[from] = to
		}
	}
	return out
}

func sortedIDSet(set map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// nodeExtraIPReplacements maps every extra IP that left the node to its
// replacement. When as many extra IPs were added as removed they are paired in
// order (an edited address); otherwise removed addresses map to "" (dropped).
// Addresses that moved between the primary fields and the extra list stay.
func nodeExtraIPReplacements(oldNode, newNode *model.Node) map[string]string {
	oldExtras := ParseNodeExtraIPs(oldNode.ExtraIPs.String)
	newExtras := ParseNodeExtraIPs(newNode.ExtraIPs.String)
	if len(oldExtras) == 0 {
		return nil
	}
	present := func(extras []string, node *model.Node) map[string]bool {
		set := make(map[string]bool, len(extras)+3)
		for _, address := range extras {
			set[address] = true
		}
		for _, address := range nodeIngressAddresses(node) {
			if address != "" {
				set[nodeAddressKey(address)] = true
			}
		}
		return set
	}
	oldAll, newAll := present(oldExtras, oldNode), present(newExtras, newNode)
	var removed, added []string
	for _, address := range oldExtras {
		if !newAll[address] {
			removed = append(removed, address)
		}
	}
	for _, address := range newExtras {
		if !oldAll[address] {
			added = append(added, address)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	out := make(map[string]string, len(removed))
	for i, address := range removed {
		if len(added) == len(removed) {
			out[address] = added[i]
		} else {
			out[address] = ""
		}
	}
	return out
}

func nodeIngressAddresses(node *model.Node) [3]string {
	trimNullable := func(value sql.NullString) string {
		if !value.Valid {
			return ""
		}
		return strings.TrimSpace(value.String)
	}
	return [3]string{trimNullable(node.ServerIPV4), trimNullable(node.ServerIPV6), strings.TrimSpace(node.ServerIP)}
}

func rewriteNodeAddressTokens(value string, replacements, extraReplacements map[string]string) string {
	seen := make(map[string]struct{})
	tokens := make([]string, 0)
	for _, token := range strings.Split(value, ",") {
		token = strings.TrimSpace(token)
		if replacement, ok := replacements[token]; ok {
			token = replacement
		} else if replacement, ok := extraReplacements[nodeAddressKey(token)]; ok && token != "" {
			token = replacement
		}
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return strings.Join(tokens, ",")
}
