package repo

import (
	"errors"
	"fmt"
	"go-backend/internal/landingcustom"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 157 共享流量池: several servers of one user share one billed-bytes limit.
// Membership uses the per-server quota identity (ServerQuotaKey): kind "node"
// = an FLVX node (also a native landing server), "landing" = a shared server.

const (
	TrafficPoolMaxPerUser    = 50
	TrafficPoolMaxMembers    = 200
	TrafficPoolNameMaxRunes  = 32
	trafficPoolMaxLimitBytes = int64(1) << 60
)

// PoolFlow is one billed flow of an upload for pool accounting.
//   - Forward flow: Tiers = path tiers (entry, relays, exit), In/Out = billed
//     bytes of the whole path. Landing = the user's own landing server when the
//     rule targets it, LandingIn/Out = the premium billed on that server key.
//   - Landing counter: only Landing + LandingIn/Out (the Core's billed bytes).
type PoolFlow struct {
	UserID                int64
	Tiers                 [][]int64
	In, Out               int64
	Landing               ServerQuotaKey
	LandingIn, LandingOut int64
}

// poolFlowShare is what one flow adds to one pool: each flow counts once.
// When the pool holds the user's landing server, the Core counter already
// brings that flow's landing bill, so only the premium is added here (path
// total = premium + landing bill). Otherwise per tier the member shares are
// summed (a load-balanced tier's members together carry the whole flow) and
// the largest tier wins (entry + relay + exit all in the pool = once).
func poolFlowShare(f PoolFlow, members map[ServerQuotaKey]bool) FlowCounterDelta {
	if f.Landing.Kind != "" && members[f.Landing] {
		return FlowCounterDelta{In: f.LandingIn, Out: f.LandingOut}
	}
	var best FlowCounterDelta
	for _, ids := range f.Tiers {
		count := int64(len(ids))
		var sum FlowCounterDelta
		for i, id := range ids {
			if members[ServerQuotaKey{UserID: f.UserID, Kind: "node", RefID: id}] {
				sum.In += tierShare(f.In, count, i)
				sum.Out += tierShare(f.Out, count, i)
			}
		}
		if sum.In > best.In {
			best.In = sum.In
		}
		if sum.Out > best.Out {
			best.Out = sum.Out
		}
	}
	return best
}

func poolMemberKey(m model.UserTrafficPoolMember) ServerQuotaKey {
	return ServerQuotaKey{UserID: m.UserID, Kind: m.ServerKind, RefID: m.ServerRefID}
}

// applyTrafficPoolsTx runs inside the flow transaction after the per-server
// quotas (lock order: landing nodes → user_server_quota → user_traffic_pool).
// One indexed query finds the pools of the upload's users; nothing else runs
// when none of them has a pool.
func applyTrafficPoolsTx(tx *gorm.DB, flows []PoolFlow, now time.Time) error {
	if len(flows) == 0 {
		return nil
	}
	seen := map[int64]bool{}
	userIDs := make([]int64, 0, 4)
	for _, f := range flows {
		if f.UserID > 0 && !seen[f.UserID] {
			seen[f.UserID] = true
			userIDs = append(userIDs, f.UserID)
		}
	}
	if len(userIDs) == 0 {
		return nil
	}
	var members []model.UserTrafficPoolMember
	if err := tx.Where("user_id IN ?", userIDs).Find(&members).Error; err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}
	pools := map[int64]map[int64]map[ServerQuotaKey]bool{} // user → pool → members
	for _, m := range members {
		if pools[m.UserID] == nil {
			pools[m.UserID] = map[int64]map[ServerQuotaKey]bool{}
		}
		if pools[m.UserID][m.PoolID] == nil {
			pools[m.UserID][m.PoolID] = map[ServerQuotaKey]bool{}
		}
		pools[m.UserID][m.PoolID][poolMemberKey(m)] = true
	}
	deltas := map[int64]FlowCounterDelta{}
	for _, f := range flows {
		for poolID, set := range pools[f.UserID] {
			d := poolFlowShare(f, set)
			if d.In <= 0 && d.Out <= 0 {
				continue
			}
			sum := deltas[poolID]
			sum.In += max(d.In, 0)
			sum.Out += max(d.Out, 0)
			deltas[poolID] = sum
		}
	}
	periods := map[int64]int64{}
	for _, id := range sortedFlowKeys(deltas) {
		var p model.UserTrafficPool
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		period, ok := periods[p.UserID]
		if !ok {
			var u model.User
			if err := tx.Select("id", "flow_reset_time").First(&u, p.UserID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			} else if err != nil {
				return err
			}
			period = LandingPeriodKey(u.FlowResetTime, now)
			periods[p.UserID] = period
		}
		if p.PeriodKey != period {
			p.UsedIn, p.UsedOut, p.PeriodKey = 0, 0, period
		}
		d := deltas[id]
		p.UsedIn += d.In
		p.UsedOut += d.Out
		p.Paused = p.LimitBytes > 0 && p.UsedIn+p.UsedOut >= p.LimitBytes
		if err := tx.Model(&p).Updates(map[string]interface{}{"used_in": p.UsedIn, "used_out": p.UsedOut, "period_key": p.PeriodKey, "paused": p.Paused, "updated_time": now.UnixMilli()}).Error; err != nil {
			return err
		}
	}
	return nil
}

// reconcileTrafficPoolsTx rolls periods / limit changes of every pool (rows
// rewritten only when something changed) and marks the members of exhausted
// pools in paused, the same map per-server quota pauses use.
func reconcileTrafficPoolsTx(tx *gorm.DB, byUser map[int64]*model.User, paused map[ServerQuotaKey]bool, now time.Time) error {
	var pools []model.UserTrafficPool
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id ASC").Find(&pools).Error; err != nil {
		return err
	}
	if len(pools) == 0 {
		return nil
	}
	exhausted := map[int64]bool{}
	for i := range pools {
		p := &pools[i]
		u := byUser[p.UserID]
		if u == nil {
			continue
		}
		period := LandingPeriodKey(u.FlowResetTime, now)
		changed := false
		if p.PeriodKey != period {
			p.UsedIn, p.UsedOut, p.PeriodKey = 0, 0, period
			changed = true
		}
		want := p.LimitBytes > 0 && p.UsedIn+p.UsedOut >= p.LimitBytes
		if want != p.Paused {
			p.Paused = want
			changed = true
		}
		if changed {
			if err := tx.Model(p).Updates(map[string]interface{}{"used_in": p.UsedIn, "used_out": p.UsedOut, "period_key": p.PeriodKey, "paused": p.Paused, "updated_time": now.UnixMilli()}).Error; err != nil {
				return err
			}
		}
		exhausted[p.ID] = p.Paused
	}
	var members []model.UserTrafficPoolMember
	if err := tx.Order("id ASC").Find(&members).Error; err != nil {
		return err
	}
	for _, m := range members {
		if exhausted[m.PoolID] {
			paused[poolMemberKey(m)] = true
		}
	}
	return nil
}

// trafficPoolBlocksForward: a paused pool holds an entry/relay/exit node of the rule.
func trafficPoolBlocksForward(db *gorm.DB, f *model.Forward) (bool, error) {
	var count int64
	err := db.Table("user_traffic_pool_member AS m").
		Joins("JOIN user_traffic_pool AS p ON p.id = m.pool_id").
		Where("m.user_id = ? AND p.paused = ? AND m.server_kind = 'node' AND (m.server_ref_id IN (SELECT node_id FROM forward_port WHERE forward_id = ?) OR m.server_ref_id IN (SELECT node_id FROM chain_tunnel WHERE tunnel_id = ? AND chain_type IN ('2','3')))", f.UserID, true, f.ID, f.TunnelID).
		Count(&count).Error
	return count > 0, err
}

// TrafficPoolServer names one pool member (or related-server candidate).
type TrafficPoolServer struct {
	ServerKind  string `json:"serverKind"`
	ServerRefID int64  `json:"serverRefId"`
	ServerName  string `json:"serverName,omitempty"`
}

// TrafficPoolView is a pool with usage of the current period.
type TrafficPoolView struct {
	ID         int64               `json:"id"`
	Name       string              `json:"name"`
	LimitBytes int64               `json:"limitBytes"`
	UsedIn     int64               `json:"usedIn"`
	UsedOut    int64               `json:"usedOut"`
	Paused     bool                `json:"paused"`
	Members    []TrafficPoolServer `json:"members"`
}

// TrafficPoolInput is one pool of the admin save (ID 0 = new pool).
type TrafficPoolInput struct {
	ID         int64               `json:"id"`
	Name       string              `json:"name"`
	LimitBytes int64               `json:"limitBytes"`
	Members    []TrafficPoolServer `json:"members"`
}

// serverNames resolves display names for node / shared-landing keys in two queries.
func serverNames(db *gorm.DB, keys []TrafficPoolServer) (map[string]string, error) {
	var nodeIDs, landingIDs []int64
	for _, k := range keys {
		if k.ServerKind == "node" {
			nodeIDs = append(nodeIDs, k.ServerRefID)
		} else if k.ServerKind == "landing" {
			landingIDs = append(landingIDs, k.ServerRefID)
		}
	}
	out := map[string]string{}
	if len(nodeIDs) > 0 {
		var rows []model.Node
		if err := db.Select("id", "name").Where("id IN ?", nodeIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, n := range rows {
			out[fmt.Sprintf("node:%d", n.ID)] = n.Name
		}
	}
	if len(landingIDs) > 0 {
		var rows []model.LandingServer
		if err := db.Select("id", "name").Where("id IN ?", landingIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, s := range rows {
			out[fmt.Sprintf("landing:%d", s.ID)] = s.Name
		}
	}
	return out, nil
}

// UserTrafficPools returns the user's pools; usage of an elapsed period reads 0.
// withMembers=false (the user's own read-only view) skips the member query.
func (r *Repository) UserTrafficPools(userID int64, withMembers bool, now time.Time) ([]TrafficPoolView, error) {
	out := []TrafficPoolView{}
	if r == nil || r.db == nil || userID <= 0 {
		return out, nil
	}
	var pools []model.UserTrafficPool
	if err := r.db.Where("user_id = ?", userID).Order("id ASC").Find(&pools).Error; err != nil {
		return nil, err
	}
	if len(pools) == 0 {
		return out, nil
	}
	var u model.User
	if err := r.db.Select("id", "flow_reset_time").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, nil
		}
		return nil, err
	}
	period := LandingPeriodKey(u.FlowResetTime, now)
	index := map[int64]int{}
	for _, p := range pools {
		v := TrafficPoolView{ID: p.ID, Name: p.Name, LimitBytes: p.LimitBytes, Members: []TrafficPoolServer{}}
		if p.PeriodKey == period {
			v.UsedIn, v.UsedOut = p.UsedIn, p.UsedOut
			v.Paused = p.Paused
		}
		index[p.ID] = len(out)
		out = append(out, v)
	}
	if !withMembers {
		return out, nil
	}
	var members []model.UserTrafficPoolMember
	if err := r.db.Where("user_id = ?", userID).Order("server_kind, server_ref_id").Find(&members).Error; err != nil {
		return nil, err
	}
	keys := make([]TrafficPoolServer, 0, len(members))
	for _, m := range members {
		keys = append(keys, TrafficPoolServer{ServerKind: m.ServerKind, ServerRefID: m.ServerRefID})
	}
	names, err := serverNames(r.db, keys)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		i, ok := index[m.PoolID]
		if !ok {
			continue
		}
		out[i].Members = append(out[i].Members, TrafficPoolServer{ServerKind: m.ServerKind, ServerRefID: m.ServerRefID, ServerName: names[fmt.Sprintf("%s:%d", m.ServerKind, m.ServerRefID)]})
	}
	return out, nil
}

// replaceUserTrafficPoolsTx makes the user's pools exactly pools: listed ids
// are updated (usage kept), unlisted pools are deleted, ID 0 creates a pool.
func replaceUserTrafficPoolsTx(tx *gorm.DB, user *model.User, pools []TrafficPoolInput, now time.Time) error {
	if len(pools) > TrafficPoolMaxPerUser {
		return fmt.Errorf("每个用户最多 %d 个共享流量池", TrafficPoolMaxPerUser)
	}
	var existing []model.UserTrafficPool
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", user.ID).Order("id ASC").Find(&existing).Error; err != nil {
		return err
	}
	byID := map[int64]*model.UserTrafficPool{}
	for i := range existing {
		byID[existing[i].ID] = &existing[i]
	}
	names := map[string]bool{}
	seenIDs := map[int64]bool{}
	seenMembers := map[string]bool{}
	var nodeIDs, landingIDs []int64
	for i := range pools {
		p := &pools[i]
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			return errors.New("请填写流量池名称")
		}
		if utf8.RuneCountInString(p.Name) > TrafficPoolNameMaxRunes {
			return fmt.Errorf("流量池名称最多 %d 个字", TrafficPoolNameMaxRunes)
		}
		if names[p.Name] {
			return errors.New("流量池名称重复：" + p.Name)
		}
		names[p.Name] = true
		if p.LimitBytes < 0 || p.LimitBytes > trafficPoolMaxLimitBytes {
			return errors.New("流量池限额无效")
		}
		if p.ID != 0 {
			if byID[p.ID] == nil || seenIDs[p.ID] {
				return errors.New("流量池不存在")
			}
			seenIDs[p.ID] = true
		}
		// An empty pool has no effect. Allowed so that a pool emptied by a node
		// deletion never blocks saving the user's other settings.
		if len(p.Members) > TrafficPoolMaxMembers {
			return fmt.Errorf("每个流量池最多 %d 台服务器", TrafficPoolMaxMembers)
		}
		for _, m := range p.Members {
			if (m.ServerKind != "node" && m.ServerKind != "landing") || m.ServerRefID <= 0 {
				return errors.New("流量池服务器无效")
			}
			key := fmt.Sprintf("%s:%d", m.ServerKind, m.ServerRefID)
			if seenMembers[key] {
				return errors.New("一台服务器只能加入该用户的一个流量池")
			}
			seenMembers[key] = true
			if m.ServerKind == "node" {
				nodeIDs = append(nodeIDs, m.ServerRefID)
			} else {
				landingIDs = append(landingIDs, m.ServerRefID)
			}
		}
	}
	if len(nodeIDs) > 0 {
		var count int64
		if err := tx.Model(&model.Node{}).Where("id IN ?", nodeIDs).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(nodeIDs)) {
			return errors.New("服务器不存在")
		}
	}
	// Plan 159: a member on a share revoked meanwhile (dialog opened before)
	// is dropped instead of failing the whole save.
	revoked := map[int64]bool{}
	if len(landingIDs) > 0 {
		var servers []model.LandingServer
		if err := tx.Select("id", "source", "shared_caps").Where("id IN ? AND source = ?", landingIDs, "shared").Find(&servers).Error; err != nil {
			return err
		}
		if len(servers) != len(landingIDs) {
			return errors.New("共享服务器不存在")
		}
		for i := range servers {
			if SharedLandingCaps(&servers[i]).Status == "revoked" {
				revoked[servers[i].ID] = true
			}
		}
	}
	if err := tx.Where("user_id = ?", user.ID).Delete(&model.UserTrafficPoolMember{}).Error; err != nil {
		return err
	}
	for _, p := range existing {
		if !seenIDs[p.ID] {
			if err := tx.Delete(&model.UserTrafficPool{}, p.ID).Error; err != nil {
				return err
			}
		}
	}
	nowMs := now.UnixMilli()
	period := LandingPeriodKey(user.FlowResetTime, now)
	for _, in := range pools {
		id := in.ID
		if id == 0 {
			p := model.UserTrafficPool{UserID: user.ID, Name: in.Name, LimitBytes: in.LimitBytes, PeriodKey: period, CreatedTime: nowMs, UpdatedTime: nowMs}
			if err := tx.Create(&p).Error; err != nil {
				return err
			}
			id = p.ID
		} else if old := byID[id]; old.Name != in.Name || old.LimitBytes != in.LimitBytes {
			if err := tx.Model(&model.UserTrafficPool{}).Where("id = ?", id).Updates(map[string]interface{}{"name": in.Name, "limit_bytes": in.LimitBytes, "updated_time": nowMs}).Error; err != nil {
				return err
			}
		}
		if len(in.Members) == 0 {
			continue
		}
		rows := make([]model.UserTrafficPoolMember, 0, len(in.Members))
		for _, m := range in.Members {
			if m.ServerKind == "landing" && revoked[m.ServerRefID] {
				continue
			}
			rows = append(rows, model.UserTrafficPoolMember{PoolID: id, UserID: user.ID, ServerKind: m.ServerKind, ServerRefID: m.ServerRefID})
		}
		if len(rows) == 0 {
			continue
		}
		if err := tx.Create(&rows).Error; err != nil {
			return err
		}
	}
	return nil
}

// RelatedServer is a server related to one user, for the admin pickers.
type RelatedServer struct {
	TrafficPoolServer
	Entry   bool `json:"entry"`
	Landing bool `json:"landing"`
	// Plan 159 (速率): RateOldAgent = entry node whose agent lacks the
	// userRate capability (there, rules with their own limit are not counted
	// in the shared rate); SharedRate = shared server applying a user 速率.
	RateOldAgent bool `json:"rateOldAgent,omitempty"`
	SharedRate   bool `json:"sharedRate,omitempty"`
	// Plan 161 (FLVX-native landing servers): LandingRateOldAgent = the
	// server's agent lacks the landingRate capability (its landing nodes are
	// not limited yet); LandingRateError = the agent could not apply the rate
	// in the kernel (its reason).
	LandingRateOldAgent bool   `json:"landingRateOldAgent,omitempty"`
	LandingRateError    string `json:"landingRateError,omitempty"`
	// Plan 168 (entry servers): EntryRateFallback = the node still limits
	// the old way ("agent" = agent without tc entry limits, "kernel" = its
	// kernel cannot); EntryRateError = probe reason / tc error.
	EntryRateFallback string `json:"entryRateFallback,omitempty"`
	EntryRateError    string `json:"entryRateError,omitempty"`
}

// UserRelatedServers lists the servers related to the user: entry servers of
// the user's granted tunnels (per-entry group restrictions honoured) and of the
// user's existing rules (入口), plus landing servers the user may use or has
// nodes on (落地; native = its node, shared = the server). Admins: everything.
func (r *Repository) UserRelatedServers(userID int64) ([]RelatedServer, error) {
	out := []RelatedServer{}
	if r == nil || r.db == nil || userID <= 0 {
		return out, nil
	}
	var u model.User
	if err := r.db.Select("id", "role_id").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, nil
		}
		return nil, err
	}
	admin := u.RoleID == 0
	entries := map[int64]bool{}
	if admin {
		var ids []int64
		if err := r.db.Model(&model.ChainTunnel{}).Where("chain_type = '1'").Distinct().Pluck("node_id", &ids).Error; err != nil {
			return nil, err
		}
		for _, id := range ids {
			entries[id] = true
		}
	} else {
		type row struct {
			TunnelID int64
			NodeID   int64
		}
		var rows []row
		if err := r.db.Table("chain_tunnel AS c").Select("c.tunnel_id, c.node_id").
			Joins("JOIN user_tunnel AS ut ON ut.tunnel_id = c.tunnel_id").
			Where("ut.user_id = ? AND c.chain_type = '1'", userID).Find(&rows).Error; err != nil {
			return nil, err
		}
		// Only tunnels granted through a group may restrict entries; check those.
		var restricted []int64
		if err := r.db.Table("user_tunnel AS ut").Distinct().
			Joins("JOIN group_permission_grant AS g ON g.user_tunnel_id = ut.id AND g.created_by_group = 1").
			Where("ut.user_id = ?", userID).Pluck("ut.tunnel_id", &restricted).Error; err != nil {
			return nil, err
		}
		allowed := map[int64]map[int64]bool{}
		for _, tid := range restricted {
			ids, limited, err := r.TunnelEntryRestriction(userID, tid, false)
			if err != nil {
				return nil, err
			}
			if limited {
				allowed[tid] = map[int64]bool{}
				for _, id := range ids {
					allowed[tid][id] = true
				}
			}
		}
		for _, rw := range rows {
			if set, ok := allowed[rw.TunnelID]; ok && !set[rw.NodeID] {
				continue
			}
			entries[rw.NodeID] = true
		}
		var ruleNodes []int64
		if err := r.db.Table("forward_port AS fp").Distinct().
			Joins("JOIN forward AS f ON f.id = fp.forward_id").
			Where("f.user_id = ?", userID).Pluck("fp.node_id", &ruleNodes).Error; err != nil {
			return nil, err
		}
		for _, id := range ruleNodes {
			entries[id] = true
		}
	}
	var servers []model.LandingServer
	q := r.db.Select("id", "node_id", "source", "name", "shared_caps", "rate_error").Where("chain_only = ?", false)
	if !admin {
		q = q.Where("("+landingAccessCond("landing_server.id")+" OR landing_server.id IN (SELECT landing_server_id FROM landing_node WHERE user_id = ?))", userID, userID, userID)
	}
	if err := q.Order("id ASC").Find(&servers).Error; err != nil {
		return nil, err
	}
	byKey := map[string]*RelatedServer{}
	add := func(kind string, ref int64) *RelatedServer {
		key := fmt.Sprintf("%s:%d", kind, ref)
		if v := byKey[key]; v != nil {
			return v
		}
		v := &RelatedServer{TrafficPoolServer: TrafficPoolServer{ServerKind: kind, ServerRefID: ref}}
		byKey[key] = v
		return v
	}
	for id := range entries {
		if id > 0 {
			add("node", id).Entry = true
		}
	}
	landingNames := map[string]string{}
	rateErrors := map[int64]string{} // native landing node → agent rate error
	for _, s := range servers {
		kind, ref := "landing", s.ID
		if s.Source == "native" {
			if s.NodeID <= 0 {
				continue
			}
			kind, ref = "node", s.NodeID
		} else if s.Source != "shared" || SharedLandingCaps(&s).Status == "revoked" {
			continue // plan 159: a revoked share is gone
		}
		v := add(kind, ref)
		v.Landing = true
		if s.Source == "shared" {
			v.SharedRate = SharedLandingCaps(&s).UserLimits.Supports(landingcustom.UserLimitsRateVersion)
		} else {
			rateErrors[ref] = s.RateError
		}
		landingNames[fmt.Sprintf("%s:%d", kind, ref)] = s.Name
	}
	keys := make([]TrafficPoolServer, 0, len(byKey))
	for _, v := range byKey {
		keys = append(keys, v.TrafficPoolServer)
	}
	names, err := serverNames(r.db, keys)
	if err != nil {
		return nil, err
	}
	for key, v := range byKey {
		v.ServerName = names[key]
		if v.ServerName == "" {
			v.ServerName = landingNames[key]
		}
		if v.ServerKind == "node" && v.ServerName == "" {
			continue // node deleted meanwhile
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ServerKind != out[j].ServerKind {
			return out[i].ServerKind > out[j].ServerKind // node before landing
		}
		return out[i].ServerRefID < out[j].ServerRefID
	})
	var entryNodes []int64
	for _, v := range out {
		if v.ServerKind == "node" && v.Entry {
			entryNodes = append(entryNodes, v.ServerRefID)
		}
	}
	if caps, err := r.NodeUserRateCapabilities(entryNodes); err == nil {
		for i := range out {
			if out[i].ServerKind == "node" && out[i].Entry {
				out[i].RateOldAgent = caps[out[i].ServerRefID] < UserRateVersion
			}
		}
	}
	if modes, err := r.NodeEntryRateModes(entryNodes); err == nil {
		for i := range out {
			n, ok := modes[out[i].ServerRefID]
			if out[i].ServerKind != "node" || !out[i].Entry || !ok || n.IsRemote == 1 {
				continue
			}
			switch n.EntryRateTc {
			case 1:
				out[i].EntryRateError = n.EntryRateError
			case 2:
				out[i].EntryRateFallback, out[i].EntryRateError = "kernel", n.EntryRateError
			default:
				out[i].EntryRateFallback = "agent"
			}
		}
	}
	// Plan 161: native landing servers (their node's row) — agent capability
	// and the last kernel error the agent reported.
	var landingNodes []int64
	for _, v := range out {
		if v.ServerKind == "node" && v.Landing {
			landingNodes = append(landingNodes, v.ServerRefID)
		}
	}
	if len(landingNodes) > 0 {
		var rows []model.Node
		if err := r.db.Select("id", "landing_rate").Where("id IN ?", landingNodes).Find(&rows).Error; err == nil {
			capable := map[int64]bool{}
			for _, n := range rows {
				capable[n.ID] = n.LandingRate >= LandingRateVersion
			}
			for i := range out {
				if out[i].ServerKind == "node" && out[i].Landing {
					out[i].LandingRateOldAgent = !capable[out[i].ServerRefID]
					if capable[out[i].ServerRefID] {
						out[i].LandingRateError = rateErrors[out[i].ServerRefID]
					}
				}
			}
		}
	}
	return out, nil
}
