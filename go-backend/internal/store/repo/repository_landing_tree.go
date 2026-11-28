package repo

import (
	"strings"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
)

// Plan 327: data of the 我的节点 tree that the existing lists do not carry —
// the external nodes of one owner (admins: of every owner, with the owner
// names) and which plan-108 线路机 chain forward came from which external
// node. A chain keeps a copy of the node (its upstream), not the node id, so
// the match is made here, always within one owner: the same node data
// (credentials included; the tag and IP version a chain sets are ignored),
// else the only node of that owner with the same protocol and address.
// Chains of a pasted link that is not in the list stay unmatched.

// LandingTreeExternal is one external node with its decoded upstream.
type LandingTreeExternal struct {
	Node model.LandingExternalNode
	Up   linechain.Upstream
}

// LandingTreeChain is a plan-108 chain forward of the tree (no secrets).
type LandingTreeChain struct {
	ForwardID  int64
	UserID     int64
	ExternalID int64 // 0 = no external node of the owner matches
}

// LandingTreeData is what POST /api/v1/landing/tree returns.
type LandingTreeData struct {
	Externals []LandingTreeExternal
	Chains    []LandingTreeChain
	UserNames map[int64]string
}

// treeUpstreamKey: the node data a chain copy and its external node share.
func treeUpstreamKey(up linechain.Upstream) string {
	up.Tag = ""
	up.IPVersion = ""
	return up.Hash()
}

// treeAddrKey: protocol + canonical address (fallback match).
func treeAddrKey(up linechain.Upstream) string {
	return strings.ToLower(up.Type) + "|" + ExternalAddrKey(up.Server, up.Port)
}

// LandingTreeData loads the tree data of ownerID, or of every user when all
// is set (callers allow that for admins only). Three queries at most.
func (r *Repository) LandingTreeData(ownerID int64, all bool) (*LandingTreeData, error) {
	out := &LandingTreeData{Externals: []LandingTreeExternal{}, Chains: []LandingTreeChain{}, UserNames: map[int64]string{}}
	var nodes []model.LandingExternalNode
	q := r.db.Order("user_id ASC, id ASC")
	if !all {
		q = q.Where("user_id = ?", ownerID)
	}
	if err := q.Find(&nodes).Error; err != nil {
		return nil, err
	}
	var chains []model.ForwardChain
	cq := r.db.Select("id", "user_id", "forward_id", "upstream", "state").Where("forward_id > 0").Order("forward_id ASC")
	if !all {
		cq = cq.Where("user_id = ?", ownerID)
	}
	if err := cq.Find(&chains).Error; err != nil {
		return nil, err
	}
	byKey := map[int64]map[string]int64{}    // owner → node data → node id
	byAddr := map[int64]map[string][]int64{} // owner → protocol+address → node ids
	owners := map[int64]bool{}
	for _, n := range nodes {
		up, ok := ExternalUpstream(n)
		if !ok {
			continue
		}
		out.Externals = append(out.Externals, LandingTreeExternal{Node: n, Up: up})
		owners[n.UserID] = true
		if byKey[n.UserID] == nil {
			byKey[n.UserID], byAddr[n.UserID] = map[string]int64{}, map[string][]int64{}
		}
		if k := treeUpstreamKey(up); byKey[n.UserID][k] == 0 {
			byKey[n.UserID][k] = n.ID
		}
		a := treeAddrKey(up)
		byAddr[n.UserID][a] = append(byAddr[n.UserID][a], n.ID)
	}
	for _, c := range chains {
		item := LandingTreeChain{ForwardID: c.ForwardID, UserID: c.UserID}
		if up, err := linechain.FromJSON(c.Upstream); err == nil && up.Type != "" {
			if id := byKey[c.UserID][treeUpstreamKey(up)]; id > 0 {
				item.ExternalID = id
			} else if ids := byAddr[c.UserID][treeAddrKey(up)]; len(ids) == 1 {
				item.ExternalID = ids[0]
			}
		}
		out.Chains = append(out.Chains, item)
		owners[c.UserID] = true
	}
	if all && len(owners) > 0 {
		ids := make([]int64, 0, len(owners))
		for id := range owners {
			ids = append(ids, id)
		}
		names, err := r.UserNamesByIDs(ids)
		if err != nil {
			return nil, err
		}
		out.UserNames = names
	}
	return out, nil
}
