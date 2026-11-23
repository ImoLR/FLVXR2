// Package usersub holds the pure decision logic of 用户专属域名 (plan 303):
// from the panel's facts (nodes, who uses which node, user state, existing
// names) it decides which name should be live, paused or retired, and which
// users need a new name. No database, no network.
package usersub

import (
	"crypto/rand"
	"sort"

	"go-backend/internal/store/model"
)

// UnusedGrace: a name whose user left the node is paused at once (record
// deleted) and retired only after this long, so a forward that is deleted
// and created again (or a group swap) gives the user back the same name.
const UnusedGraceMs = int64(24 * 60 * 60 * 1000)

// CapReserve: records kept free for the admin's own DDNS records.
const CapReserve = 5

// Node is what the planner needs to know about one node.
type Node struct {
	ID int64
	// Enabled: the 用户专属域名 toggle is on and DDNS is on.
	Enabled bool
	// MainDomain: the DDNS main domain names point at ("" = none);
	// MainSynced: its record exists in Cloudflare.
	MainDomain string
	MainSynced bool
	// ZoneKey groups nodes sharing one DNS zone (provider + zone) for the
	// record quota; "" = unknown yet.
	ZoneKey string
}

// Effective: names on this node may have a live record.
func (n Node) Effective() bool { return n.Enabled && n.MainDomain != "" && n.MainSynced }

type User struct {
	ID      int64
	Admin   bool
	Status  int
	ExpTime int64
}

// Facts is everything the planner reads.
type Facts struct {
	Now   int64
	Nodes map[int64]Node
	// Usage: node id → user ids with a forward port / native landing node on it.
	Usage map[int64]map[int64]bool
	Users map[int64]User
	// Names: every non-retired name plus the admin-deleted (blocked) tombstones.
	Names []model.UserSubdomain
	// Room: zone key → how many more records may be created (missing key =
	// unknown → creation waits for the first zone check).
	Room map[string]int
}

// Change is a new lifecycle state for one existing name.
type Change struct {
	ID          int64
	State       string
	Reason      string
	UnusedSince int64
	RetiredAt   int64
}

// Create asks for a new name for (user, node).
type Create struct {
	UserID int64
	NodeID int64
}

type Plan struct {
	Changes []Change
	Creates []Create
	// Capped: node id → users that got no name because the zone is full.
	Capped map[int64]int
	// Waiting: node id → users waiting for the zone's first check.
	Waiting map[int64]int
}

// Desired returns the lifecycle state a name should be in.
func Desired(f *Facts, n model.UserSubdomain) (state, reason string, unusedSince int64) {
	u, ok := f.Users[n.UserID]
	if !ok {
		return model.UserSubdomainRetired, model.UserSubdomainReasonUserDeleted, 0
	}
	node, ok := f.Nodes[n.NodeID]
	if !ok {
		return model.UserSubdomainRetired, model.UserSubdomainReasonNodeDeleted, 0
	}
	if !node.Effective() {
		return model.UserSubdomainPaused, model.UserSubdomainReasonNode, n.UnusedSince
	}
	if !f.Usage[n.NodeID][n.UserID] {
		since := n.UnusedSince
		if since == 0 {
			since = f.Now
		}
		if f.Now-since >= UnusedGraceMs {
			return model.UserSubdomainRetired, model.UserSubdomainReasonUnused, since
		}
		return model.UserSubdomainPaused, model.UserSubdomainReasonUnused, since
	}
	if u.ExpTime > 0 && u.ExpTime < f.Now {
		return model.UserSubdomainPaused, model.UserSubdomainReasonExpired, 0
	}
	if u.Status != 1 {
		return model.UserSubdomainPaused, model.UserSubdomainReasonDisabled, 0
	}
	return model.UserSubdomainActive, "", 0
}

// UserActive: a user that may get a new name.
func UserActive(u User, now int64) bool {
	return !u.Admin && u.Status == 1 && !(u.ExpTime > 0 && u.ExpTime < now)
}

// Compute builds the plan. Deterministic: creates are ordered by node, user.
func Compute(f *Facts) Plan {
	p := Plan{Capped: map[int64]int{}, Waiting: map[int64]int{}}
	have := map[[2]int64]bool{}    // user, target node → a live or paused name
	blocked := map[[2]int64]bool{} // admin-deleted pairs
	for _, n := range f.Names {
		if n.State == model.UserSubdomainRetired {
			if n.Blocked {
				blocked[[2]int64{n.UserID, n.NodeID}] = true
			}
			continue
		}
		state, reason, since := Desired(f, n)
		if state != model.UserSubdomainRetired {
			have[[2]int64{n.UserID, n.NodeID}] = true
		}
		if state == n.State && reason == n.Reason && since == n.UnusedSince {
			continue
		}
		c := Change{ID: n.ID, State: state, Reason: reason, UnusedSince: since}
		if state == model.UserSubdomainRetired {
			c.RetiredAt = f.Now
		}
		p.Changes = append(p.Changes, c)
	}
	nodeIDs := make([]int64, 0, len(f.Nodes))
	for id := range f.Nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	room := map[string]int{}
	for k, v := range f.Room {
		room[k] = v
	}
	for _, nodeID := range nodeIDs {
		node := f.Nodes[nodeID]
		if !node.Effective() {
			continue
		}
		users := make([]int64, 0, len(f.Usage[nodeID]))
		for uid := range f.Usage[nodeID] {
			users = append(users, uid)
		}
		sort.Slice(users, func(i, j int) bool { return users[i] < users[j] })
		for _, uid := range users {
			u, ok := f.Users[uid]
			key := [2]int64{uid, nodeID}
			if !ok || !UserActive(u, f.Now) || have[key] || blocked[key] {
				continue
			}
			left, known := room[node.ZoneKey]
			switch {
			case node.ZoneKey == "" || !known:
				p.Waiting[nodeID]++
				continue
			case left <= 0:
				p.Capped[nodeID]++
				continue
			}
			room[node.ZoneKey] = left - 1
			have[key] = true
			p.Creates = append(p.Creates, Create{UserID: uid, NodeID: nodeID})
		}
	}
	return p
}

// Room: records that may still be created in a zone of this size.
func Room(used, quota int) int {
	left := quota - CapReserve - used
	if left < 0 {
		return 0
	}
	return left
}

const labelAlphabet = "abcdefghijkmnpqrstuvwxyz23456789" // no l/o/0/1: easy to read back

// NewLabel returns a random 7-character label starting with a letter.
func NewLabel() string {
	const n = 7
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	out := make([]byte, n)
	for i, b := range buf {
		if i == 0 {
			out[i] = labelAlphabet[int(b)%24] // letters only
			continue
		}
		out[i] = labelAlphabet[int(b)%len(labelAlphabet)]
	}
	return string(out)
}
