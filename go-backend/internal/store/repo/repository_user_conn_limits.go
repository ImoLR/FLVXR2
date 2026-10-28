package repo

import (
	"errors"
	"sort"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// Plan 158: per user per server 入站连接数 (user_server_quota.max_inbound_connections)
// and the inputs the quota coordinator needs to count landing inbound in the
// user totals (用户总连接数 / 总 IP 数).

// MaxUserServerConnections bounds one per-server 入站连接数.
const MaxUserServerConnections = 1_000_000

// LandingUserLimitsVersion is the agent capability (landing_user_limits) that
// groups a user's landing nodes on one Core, applies per-user budgets and
// reports per-user landing inbound usage.
const LandingUserLimitsVersion = 1

// QuotaUserLimits are a user's global caps (0 = 不限).
type QuotaUserLimits struct {
	MaxConnections int
	MaxClientIps   int
}

// QuotaLandingSlot is one user's inbound on one capable landing server.
type QuotaLandingSlot struct {
	UserID   int64
	ServerID int64 // landing_server.id
	Shared   bool
	// Native: the FLVX node running the landing Core (its agent applies the
	// budget); shared: 0.
	NodeID      int64
	NodeStatus  int
	NodeVersion string
	// RelayNodes = distinct FLVX nodes that dial this user's nodes on this
	// server for the user's own rules (linked landing targets): each keeps an
	// IP slot at the Core so a relay is never refused for a new source IP.
	RelayNodes int
}

// QuotaPlanExtras are the plan-158 inputs next to the forward targets.
type QuotaPlanExtras struct {
	// Users with a global cap or any per-server 入站连接数.
	Users map[int64]QuotaUserLimits
	// Caps: per-server 入站连接数 > 0, keyed like user_server_quota.
	Caps    map[ServerQuotaKey]int
	Landing []QuotaLandingSlot
}

// UserServerConnCaps returns the user's per-server 入站连接数 > 0.
func (r *Repository) UserServerConnCaps(userID int64) (map[ServerQuotaKey]int, error) {
	out := map[ServerQuotaKey]int{}
	if r == nil || r.db == nil || userID <= 0 {
		return out, nil
	}
	var rows []model.UserServerQuota
	if err := r.db.Select("user_id", "server_kind", "server_ref_id", "max_inbound_connections").
		Where("user_id = ? AND max_inbound_connections > 0", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, q := range rows {
		out[ServerQuotaKey{UserID: q.UserID, Kind: q.ServerKind, RefID: q.ServerRefID}] = q.MaxInboundConnections
	}
	return out, nil
}

// LandingNodeOwnerServers lists the landing servers the user has nodes on.
func (r *Repository) LandingNodeOwnerServers(userID int64) ([]int64, error) {
	var ids []int64
	if r == nil || r.db == nil {
		return ids, errors.New("repository not initialized")
	}
	err := r.db.Model(&model.LandingNode{}).Where("user_id = ?", userID).Distinct().Pluck("landing_server_id", &ids).Error
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, err
}

// LoadQuotaPlanExtras reads, in a fixed number of queries, the limited users,
// their per-server caps and their landing slots on capable servers (native
// agent with landing_user_limits, shared with user_limits v1).
func (r *Repository) LoadQuotaPlanExtras() (QuotaPlanExtras, error) {
	out := QuotaPlanExtras{Users: map[int64]QuotaUserLimits{}, Caps: map[ServerQuotaKey]int{}}
	if r == nil || r.db == nil {
		return out, errors.New("repository not initialized")
	}
	var caps []model.UserServerQuota
	if err := r.db.Select("user_id", "server_kind", "server_ref_id", "max_inbound_connections").
		Where("max_inbound_connections > 0").Find(&caps).Error; err != nil {
		return out, err
	}
	capUsers := map[int64]bool{}
	for _, q := range caps {
		out.Caps[ServerQuotaKey{UserID: q.UserID, Kind: q.ServerKind, RefID: q.ServerRefID}] = q.MaxInboundConnections
		capUsers[q.UserID] = true
	}
	var users []model.User
	query := r.db.Select("id", "max_connections", "max_client_ips").Where("max_connections > 0 OR max_client_ips > 0")
	if len(capUsers) > 0 {
		ids := make([]int64, 0, len(capUsers))
		for id := range capUsers {
			ids = append(ids, id)
		}
		query = r.db.Select("id", "max_connections", "max_client_ips").Where("max_connections > 0 OR max_client_ips > 0 OR id IN ?", ids)
	}
	if err := query.Find(&users).Error; err != nil {
		return out, err
	}
	for _, u := range users {
		out.Users[u.ID] = QuotaUserLimits{MaxConnections: max(u.MaxConnections, 0), MaxClientIps: max(u.MaxClientIps, 0)}
	}
	if len(out.Users) == 0 {
		return out, nil
	}
	// Landing slots: active nodes of limited users on capable servers.
	type pair struct {
		UserID          int64
		LandingServerID int64
	}
	var pairs []pair
	if err := r.db.Model(&model.LandingNode{}).Select("DISTINCT user_id, landing_server_id").
		Where("status = ? AND delete_pending = ?", "active", false).Scan(&pairs).Error; err != nil {
		return out, err
	}
	serverIDs := map[int64]bool{}
	for _, p := range pairs {
		if _, limited := out.Users[p.UserID]; limited {
			serverIDs[p.LandingServerID] = true
		}
	}
	if len(serverIDs) == 0 {
		return out, nil
	}
	ids := make([]int64, 0, len(serverIDs))
	for id := range serverIDs {
		ids = append(ids, id)
	}
	var servers []model.LandingServer
	if err := r.db.Select("id", "source", "node_id", "status", "shared_caps").Where("id IN ?", ids).Find(&servers).Error; err != nil {
		return out, err
	}
	nodeIDs := []int64{}
	for _, s := range servers {
		if s.Source == "native" && s.NodeID > 0 {
			nodeIDs = append(nodeIDs, s.NodeID)
		}
	}
	nodes := map[int64]model.Node{}
	if len(nodeIDs) > 0 {
		var rows []model.Node
		if err := r.db.Select("id", "status", "version", "landing_user_limits").Where("id IN ?", nodeIDs).Find(&rows).Error; err != nil {
			return out, err
		}
		for _, n := range rows {
			nodes[n.ID] = n
		}
	}
	capable := map[int64]QuotaLandingSlot{}
	for _, s := range servers {
		if s.Status != "active" {
			continue
		}
		switch s.Source {
		case "native":
			n, ok := nodes[s.NodeID]
			if !ok || n.LandingUserLimits < LandingUserLimitsVersion {
				continue
			}
			capable[s.ID] = QuotaLandingSlot{ServerID: s.ID, NodeID: s.NodeID, NodeStatus: n.Status, NodeVersion: n.Version.String}
		case "shared":
			if SharedLandingCaps(&s).UserLimits.Supports(landingcustom.UserLimitsVersion) {
				capable[s.ID] = QuotaLandingSlot{ServerID: s.ID, Shared: true}
			}
		}
	}
	if len(capable) == 0 {
		return out, nil
	}
	relays, err := r.landingRelayNodes()
	if err != nil {
		return out, err
	}
	for _, p := range pairs {
		slot, ok := capable[p.LandingServerID]
		if _, limited := out.Users[p.UserID]; !ok || !limited {
			continue
		}
		slot.UserID = p.UserID
		slot.RelayNodes = len(relays[ServerQuotaKey{UserID: p.UserID, Kind: "landing", RefID: p.LandingServerID}])
		out.Landing = append(out.Landing, slot)
	}
	sort.Slice(out.Landing, func(i, j int) bool {
		if out.Landing[i].UserID != out.Landing[j].UserID {
			return out.Landing[i].UserID < out.Landing[j].UserID
		}
		return out.Landing[i].ServerID < out.Landing[j].ServerID
	})
	return out, nil
}

// landingRelayNodes maps (user, landing server) to the FLVX nodes that dial
// the user's nodes there for the user's own active rules: entry nodes of
// port-forward tunnels (type 1), exit nodes of tunnel forwards (type 2).
func (r *Repository) landingRelayNodes() (map[ServerQuotaKey]map[int64]bool, error) {
	type row struct {
		UserID          int64
		LandingServerID int64
		NodeID          int64
	}
	var direct, exits []row
	if err := r.db.Table("forward AS f").Select("DISTINCT f.user_id AS user_id, ln.landing_server_id AS landing_server_id, fp.node_id AS node_id").
		Joins("JOIN landing_node AS ln ON ln.id = f.landing_node_id").
		Joins("JOIN tunnel AS t ON t.id = f.tunnel_id").
		Joins("JOIN forward_port AS fp ON fp.forward_id = f.id").
		Where("f.status = ? AND f.landing_node_id > 0 AND t.type = ?", 1, 1).Scan(&direct).Error; err != nil {
		return nil, err
	}
	if err := r.db.Table("forward AS f").Select("DISTINCT f.user_id AS user_id, ln.landing_server_id AS landing_server_id, ct.node_id AS node_id").
		Joins("JOIN landing_node AS ln ON ln.id = f.landing_node_id").
		Joins("JOIN tunnel AS t ON t.id = f.tunnel_id").
		Joins("JOIN chain_tunnel AS ct ON ct.tunnel_id = f.tunnel_id AND ct.chain_type = ?", "3").
		Where("f.status = ? AND f.landing_node_id > 0 AND t.type = ?", 1, 2).Scan(&exits).Error; err != nil {
		return nil, err
	}
	out := map[ServerQuotaKey]map[int64]bool{}
	for _, rw := range append(direct, exits...) {
		key := ServerQuotaKey{UserID: rw.UserID, Kind: "landing", RefID: rw.LandingServerID}
		if out[key] == nil {
			out[key] = map[int64]bool{}
		}
		out[key][rw.NodeID] = true
	}
	return out, nil
}

// LandingServerQuotaKey is the user_server_quota key of a landing server: its
// FLVX node for a native server, the server itself for a shared one.
func LandingServerQuotaKey(userID int64, s *model.LandingServer) ServerQuotaKey {
	if s != nil && s.Source == "native" {
		return ServerQuotaKey{UserID: userID, Kind: "node", RefID: s.NodeID}
	}
	id := int64(0)
	if s != nil {
		id = s.ID
	}
	return ServerQuotaKey{UserID: userID, Kind: "landing", RefID: id}
}

// attachForwardServerCaps fills ForwardRecord.UserServerConnCaps (entry node →
// 入站连接数) for the rows' users in one query.
func attachForwardServerCaps(tx *gorm.DB, rows []model.ForwardRecord, userIDs []int64) {
	if tx == nil || len(rows) == 0 || len(userIDs) == 0 {
		return
	}
	var caps []model.UserServerQuota
	if err := tx.Select("user_id", "server_ref_id", "max_inbound_connections").
		Where("user_id IN ? AND server_kind = ? AND max_inbound_connections > 0", userIDs, "node").Find(&caps).Error; err != nil {
		return
	}
	byUser := map[int64]map[int64]int{}
	for _, q := range caps {
		if byUser[q.UserID] == nil {
			byUser[q.UserID] = map[int64]int{}
		}
		byUser[q.UserID][q.ServerRefID] = q.MaxInboundConnections
	}
	for i := range rows {
		rows[i].UserServerConnCaps = byUser[rows[i].UserID]
	}
}

// QuotaUserLimitsFor returns the global caps of the given users.
func (r *Repository) QuotaUserLimitsFor(userIDs []int64) (map[int64]QuotaUserLimits, error) {
	out := map[int64]QuotaUserLimits{}
	if r == nil || r.db == nil || len(userIDs) == 0 {
		return out, nil
	}
	var users []model.User
	if err := r.db.Select("id", "max_connections", "max_client_ips").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = QuotaUserLimits{MaxConnections: max(u.MaxConnections, 0), MaxClientIps: max(u.MaxClientIps, 0)}
	}
	return out, nil
}

// ServerConnCapsFor returns, per user, the 入站连接数 on the server identified
// by key's Kind/RefID (key.UserID is ignored).
func (r *Repository) ServerConnCapsFor(userIDs []int64, key ServerQuotaKey) (map[int64]int, error) {
	out := map[int64]int{}
	if r == nil || r.db == nil || len(userIDs) == 0 || key.RefID <= 0 {
		return out, nil
	}
	var rows []model.UserServerQuota
	if err := r.db.Select("user_id", "max_inbound_connections").
		Where("user_id IN ? AND server_kind = ? AND server_ref_id = ? AND max_inbound_connections > 0", userIDs, key.Kind, key.RefID).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, q := range rows {
		out[q.UserID] = q.MaxInboundConnections
	}
	return out, nil
}
