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
	// Records (plan 305): the DDNS domains names point at — the main domain
	// (Key "") first, then every other DDNS domain of the node (Key = that
	// domain). nil = the main domain only.
	Records []Record
}

// Record is one DDNS domain of a node a user name may point at.
type Record struct {
	Key    string // "" = the main domain
	Domain string
	Synced bool // its record exists in Cloudflare (a CNAME to it resolves)
}

// Effective: names on this node may have a live record.
func (n Node) Effective() bool { return n.Enabled && n.MainDomain != "" && n.MainSynced }

// Targets: the node's name targets (main first).
func (n Node) Targets() []Record {
	if len(n.Records) == 0 {
		return []Record{{Key: "", Domain: n.MainDomain, Synced: n.MainSynced}}
	}
	return n.Records
}

// Target: the record a name with this key points at (ok=false: gone).
func (n Node) Target(key string) (Record, bool) {
	for _, r := range n.Targets() {
		if r.Key == key {
			return r, true
		}
	}
	return Record{}, false
}

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
	// Usage: node id → user ids with a forward port / native landing node on
	// it (only these get a new name; an existing name does not need it).
	// Plan 305: forwards / landings kept on their old address (已有转发保持原
	// 地址) do not count.
	Usage map[int64]map[int64]bool
	Users map[int64]User
	// Names: every non-retired name plus the admin-deleted (blocked) tombstones.
	Names []model.UserSubdomain
	// Access (plan 321): node id → users who still need the node (a grant
	// whose tunnel enters there, a forward port, a native landing / landing
	// access, a 利群 grant entry). A name of a user without access there is
	// retired ("no longer on the node"). nil = not loaded (rule off).
	Access map[int64]map[int64]bool
}

// Change is a new lifecycle state for one existing name.
type Change struct {
	ID          int64
	State       string
	Reason      string
	UnusedSince int64
	RetiredAt   int64
}

// Create asks for a new name for (user, node, record), stored in State /
// Reason (round 3: a 到期 / 停用 user gets his name reserved too, paused).
type Create struct {
	UserID int64
	NodeID int64
	// RecordKey (plan 305): the DDNS domain the name points at ("" = main).
	RecordKey string
	State     string
	Reason    string
}

// PairKey identifies one name slot: user, node, DDNS record ("" = main).
type PairKey struct {
	UserID int64
	NodeID int64
	Record string
}

type Plan struct {
	Changes []Change
	Creates []Create
}

// FailShow: a fresh name (never verified) whose record failed this many
// times is no longer handed out ("入口域名创建失败"); config / auth errors
// count as FailShow at once.
const FailShow = 3

// Hidden (round 3): a name that must not be handed out at all — it was never
// verified in Cloudflare and cannot be created now (zone at the record cap,
// or creating it keeps failing). Names that were ready once stay visible.
func Hidden(n model.UserSubdomain) bool {
	if n.ReadyAt > 0 || n.State == model.UserSubdomainRetired {
		return false
	}
	switch n.Status {
	case model.UserSubdomainSyncCapped:
		return true
	case model.UserSubdomainSyncFailed:
		return n.FailCount >= FailShow
	}
	return false
}

// Admit decides which names that need a new Cloudflare record may get one
// now in a zone with room free records (known=false: the zone could not be
// checked). Names that were ready before go first (their record was counted
// until it was deleted), then the oldest. Fresh names beyond the room are
// capped; with an unknown room fresh names wait (unknown) while names that
// were ready before are tried anyway.
func Admit(rows []model.UserSubdomain, room int, known bool) (admit, capped, unknown []int64) {
	sorted := append([]model.UserSubdomain(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := sorted[i].ReadyAt > 0, sorted[j].ReadyAt > 0
		if ri != rj {
			return ri
		}
		return sorted[i].ID < sorted[j].ID
	})
	for _, n := range sorted {
		switch {
		case !known && n.ReadyAt > 0:
			admit = append(admit, n.ID)
		case !known:
			unknown = append(unknown, n.ID)
		case room > 0:
			room--
			admit = append(admit, n.ID)
		default:
			capped = append(capped, n.ID)
		}
	}
	return admit, capped, unknown
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
	// Plan 321 (user 11-28): when nothing of his needs the node any more
	// (last grant removed, last forward / landing gone) the name follows the
	// plan-303 rule for "no longer on the node": record deleted, retired.
	if f.Access != nil && !f.Access[n.NodeID][n.UserID] {
		return model.UserSubdomainRetired, model.UserSubdomainReasonNoAccess, 0
	}
	if !node.Effective() {
		return model.UserSubdomainPaused, model.UserSubdomainReasonNode, 0
	}
	// Plan 305: a name for a DDNS domain the node no longer has (or whose
	// record is not in Cloudflare yet) has nothing to point at.
	if rec, ok := node.Target(n.RecordDomain); !ok || !rec.Synced || rec.Domain == "" {
		return model.UserSubdomainPaused, model.UserSubdomainReasonNode, 0
	}
	// Round 2 (user 11-24): no pause for "no forwards on this node" — the
	// name lives while its user exists and is not expired / disabled (a
	// round-1 paused(unused) row comes back to active).
	if u.ExpTime > 0 && u.ExpTime < f.Now {
		return model.UserSubdomainPaused, model.UserSubdomainReasonExpired, 0
	}
	if u.Status != 1 {
		return model.UserSubdomainPaused, model.UserSubdomainReasonDisabled, 0
	}
	return model.UserSubdomainActive, "", 0
}

// UserActive: not an admin, enabled and not expired.
func UserActive(u User, now int64) bool {
	return !u.Admin && u.Status == 1 && !(u.ExpTime > 0 && u.ExpTime < now)
}

// Compute builds the plan. Deterministic: creates are ordered by node, user,
// record (main first).
// Round 3: every non-admin user using an effective node gets his name at
// once (any state: 到期 / 停用 users get it paused); the record cap only
// limits Cloudflare records (Admit), never the reservation.
func Compute(f *Facts) Plan {
	p := Plan{}
	have := map[PairKey]bool{}    // user, target node, record → a live or paused name
	blocked := map[PairKey]bool{} // admin-deleted slots
	for _, n := range f.Names {
		key := PairKey{n.UserID, n.NodeID, n.RecordDomain}
		if n.State == model.UserSubdomainRetired {
			if n.Blocked {
				blocked[key] = true
			}
			continue
		}
		state, reason, since := Desired(f, n)
		if state != model.UserSubdomainRetired {
			have[key] = true
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
			if !ok || u.Admin {
				continue
			}
			// Plan 305: one name per DDNS domain of the node (synced ones).
			for _, rec := range node.Targets() {
				key := PairKey{uid, nodeID, rec.Key}
				if !rec.Synced || rec.Domain == "" || have[key] || blocked[key] {
					continue
				}
				have[key] = true
				state, reason, _ := Desired(f, model.UserSubdomain{UserID: uid, NodeID: nodeID, RecordDomain: rec.Key})
				if state == model.UserSubdomainRetired {
					continue // plan 321: never reserve a name that would retire at once
				}
				p.Creates = append(p.Creates, Create{UserID: uid, NodeID: nodeID, RecordKey: rec.Key, State: state, Reason: reason})
			}
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
