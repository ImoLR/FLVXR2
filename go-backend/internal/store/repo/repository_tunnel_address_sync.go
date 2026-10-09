package repo

import (
	"strings"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

// TunnelEntryInIPTx runs before replacing chain_tunnel. Unchanged entry sets
// retain the legacy save semantics, including the submitted address formatting.
func (r *Repository) TunnelEntryInIPTx(tx *gorm.DB, tunnelID int64, submitted *string, entryIDs []int64, preference string, build func([]model.Node, string) string) (string, error) {
	var oldIDs []int64
	if err := tx.Model(&model.ChainTunnel{}).Where("tunnel_id = ? AND chain_type = '1'", tunnelID).Pluck("node_id", &oldIDs).Error; err != nil {
		return "", err
	}
	oldSet, newSet := make(map[int64]bool), make(map[int64]bool)
	for _, id := range oldIDs {
		oldSet[id] = true
	}
	for _, id := range entryIDs {
		newSet[id] = true
	}
	changed := len(oldSet) != len(newSet)
	for id := range oldSet {
		changed = changed || !newSet[id]
	}
	value := ""
	if submitted != nil {
		value = *submitted
	} else if changed {
		var tunnel model.Tunnel
		if err := tx.First(&tunnel, tunnelID).Error; err != nil {
			return "", err
		}
		value = tunnel.InIP.String
	}
	if !changed && strings.TrimSpace(value) != "" {
		return value, nil
	}
	var nodes []model.Node
	ids := append(append([]int64{}, oldIDs...), entryIDs...)
	if err := tx.Where("id IN ?", ids).Find(&nodes).Error; err != nil {
		return "", err
	}
	byID := make(map[int64]model.Node, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	var entries, added []model.Node
	for _, id := range entryIDs {
		if node, ok := byID[id]; ok {
			entries = append(entries, node)
			if !oldSet[id] {
				added = append(added, node)
			}
		}
	}
	if !changed {
		return build(entries, preference), nil
	}
	normalize := func(s string) string { return strings.ToLower(strings.Trim(strings.TrimSpace(s), "[]")) }
	removedAddresses, keptAddresses := make(map[string]bool), make(map[string]bool)
	for _, node := range nodes {
		for _, address := range nodeIngressAddresses(&node) {
			if key := normalize(address); key != "" {
				if newSet[node.ID] {
					keptAddresses[key] = true
				} else if oldSet[node.ID] {
					removedAddresses[key] = true
				}
			}
		}
	}
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == '\n' || c == '\r' })
	}
	var tokens []string
	seen := make(map[string]bool)
	for _, token := range split(value) {
		token = strings.TrimSpace(token)
		key := normalize(token)
		if token == "" || removedAddresses[key] && !keptAddresses[key] {
			continue
		}
		tokens = append(tokens, token)
		seen[key] = true
	}
	for _, token := range split(build(added, preference)) {
		token = strings.TrimSpace(token)
		key := normalize(token)
		if token != "" && !seen[key] {
			tokens = append(tokens, token)
			seen[key] = true
		}
	}
	if len(tokens) == 0 {
		return build(entries, preference), nil
	}
	return strings.Join(tokens, ","), nil
}
