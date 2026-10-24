package repo

import (
	"database/sql"
	"sort"
)

// Rule picker 线路分组 (plan 144). Each tunnel list item gets the enabled
// tunnel_groups (permission groups, not tunnel_group_new) covering its
// options; an option is (tunnel, entry key). Options no group covers are
// shown under 「其他」 by the client.

// TunnelPickerGroup is one 线路分组 of a tunnel list item. EntryKeys are the
// item's entryGroups keys the group covers; empty means every entry (only
// used for tunnels without entry nodes).
type TunnelPickerGroup struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Color     string   `json:"color"`
	Inx       int      `json:"inx"`
	EntryKeys []string `json:"entryKeys"`
}

const (
	pickerSourceAdmin   = "admin"
	pickerSourceGrant   = "grant"
	pickerSourcePackage = "package"
)

type tunnelPickerGroupRow struct {
	TunnelID int64
	GroupID  int64
	Name     string
	Color    sql.NullString
	Inx      int
	Source   string
	Member   int
	NodeID   sql.NullInt64
}

func groupPickerRows(rows []tunnelPickerGroupRow) map[int64][]tunnelPickerGroupRow {
	out := make(map[int64][]tunnelPickerGroupRow)
	for _, row := range rows {
		out[row.TunnelID] = append(out[row.TunnelID], row)
	}
	return out
}

// adminTunnelPickerGroupRows: every enabled group's tunnels with their
// per-entry grant rows, one query.
func (r *Repository) adminTunnelPickerGroupRows() (map[int64][]tunnelPickerGroupRow, error) {
	var rows []tunnelPickerGroupRow
	err := r.db.Raw(`SELECT m.tunnel_id AS tunnel_id, tg.id AS group_id, tg.name AS name, tg.color AS color, COALESCE(tg.inx, 0) AS inx,
	'admin' AS source, 1 AS member, e.node_id AS node_id
FROM tunnel_group_tunnel m
JOIN tunnel_group tg ON tg.id = m.tunnel_group_id
LEFT JOIN tunnel_group_tunnel_entry e ON e.tunnel_group_id = m.tunnel_group_id AND e.tunnel_id = m.tunnel_id
WHERE tg.status = 1`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return groupPickerRows(rows), nil
}

// userTunnelPickerGroupRows mirrors how a user gets tunnels through groups:
// persisted group_permission_grant rows on the user's user_tunnel (the same
// rows TunnelEntryRestriction trusts) and active subscription packages'
// tunnel groups. Only groups that grant this user something are returned.
func (r *Repository) userTunnelPickerGroupRows(userID int64) (map[int64][]tunnelPickerGroupRow, error) {
	var rows []tunnelPickerGroupRow
	err := r.db.Raw(`SELECT ut.tunnel_id AS tunnel_id, tg.id AS group_id, tg.name AS name, tg.color AS color, COALESCE(tg.inx, 0) AS inx,
	'grant' AS source, CASE WHEN m.id IS NULL THEN 0 ELSE 1 END AS member, e.node_id AS node_id
FROM user_tunnel ut
JOIN group_permission_grant g ON g.user_tunnel_id = ut.id
JOIN tunnel_group tg ON tg.id = g.tunnel_group_id
LEFT JOIN tunnel_group_tunnel m ON m.tunnel_group_id = tg.id AND m.tunnel_id = ut.tunnel_id
LEFT JOIN tunnel_group_tunnel_entry e ON e.tunnel_group_id = tg.id AND e.tunnel_id = ut.tunnel_id
WHERE ut.user_id = ? AND tg.status = 1
UNION ALL
SELECT ut.tunnel_id AS tunnel_id, tg.id AS group_id, tg.name AS name, tg.color AS color, COALESCE(tg.inx, 0) AS inx,
	'package' AS source, 1 AS member, e.node_id AS node_id
FROM package_subscription ps
JOIN subscription_package_tunnel_group spg ON spg.package_id = ps.package_id
JOIN tunnel_group tg ON tg.id = spg.tunnel_group_id
JOIN tunnel_group_tunnel m ON m.tunnel_group_id = tg.id
JOIN user_tunnel ut ON ut.user_id = ps.user_id AND ut.tunnel_id = m.tunnel_id
LEFT JOIN tunnel_group_tunnel_entry e ON e.tunnel_group_id = tg.id AND e.tunnel_id = m.tunnel_id
WHERE ps.user_id = ? AND ps.status = 1 AND tg.status = 1`, userID, userID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return groupPickerRows(rows), nil
}

// buildTunnelPickerGroups turns one tunnel's group rows into the groups
// covering its visible entry nodes. restricted mirrors
// TunnelEntryRestriction: a tunnel the user got through a group only exposes
// the entries the groups list, so a granting group without entry rows covers
// nothing.
func buildTunnelPickerGroups(entries []tunnelEntryNode, admin, restricted bool, rows []tunnelPickerGroupRow) []TunnelPickerGroup {
	type acc struct {
		group  TunnelPickerGroup
		all    bool
		nodes  map[int64]bool
		member bool
	}
	byGroup := make(map[int64]*acc)
	order := make([]int64, 0)
	// Entry rows are per (group, tunnel); collect them per source first.
	type sourceKey struct {
		group  int64
		source string
	}
	sourceNodes := make(map[sourceKey]map[int64]bool)
	sourceMember := make(map[sourceKey]bool)
	for _, row := range rows {
		a := byGroup[row.GroupID]
		if a == nil {
			a = &acc{group: TunnelPickerGroup{ID: row.GroupID, Name: row.Name, Color: row.Color.String, Inx: row.Inx}, nodes: map[int64]bool{}}
			byGroup[row.GroupID] = a
			order = append(order, row.GroupID)
		}
		k := sourceKey{row.GroupID, row.Source}
		if sourceNodes[k] == nil {
			sourceNodes[k] = map[int64]bool{}
		}
		if row.NodeID.Valid {
			sourceNodes[k][row.NodeID.Int64] = true
		}
		if row.Member == 1 {
			sourceMember[k] = true
			a.member = true
		}
	}
	for k, nodes := range sourceNodes {
		a := byGroup[k.group]
		for id := range nodes {
			a.nodes[id] = true
		}
		if len(nodes) > 0 {
			continue
		}
		switch k.source {
		case pickerSourceAdmin, pickerSourcePackage:
			a.all = true
		case pickerSourceGrant:
			a.all = !restricted && sourceMember[k]
		}
	}

	out := make([]TunnelPickerGroup, 0, len(order))
	for _, id := range order {
		a := byGroup[id]
		if len(entries) == 0 {
			// No entry nodes: the single 默认入口 option belongs to the group
			// whenever the group still contains the tunnel.
			if a.member || a.all || len(a.nodes) > 0 {
				a.group.EntryKeys = []string{}
				out = append(out, a.group)
			}
			continue
		}
		keys := make([]string, 0, len(entries))
		seen := make(map[string]bool)
		for _, node := range entries {
			if !a.all && !a.nodes[node.ID] {
				continue
			}
			_, key := node.entryLabelKey(admin)
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			continue
		}
		a.group.EntryKeys = keys
		out = append(out, a.group)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Inx != out[j].Inx {
			return out[i].Inx < out[j].Inx
		}
		return out[i].ID < out[j].ID
	})
	return out
}
