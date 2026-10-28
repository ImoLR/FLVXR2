package repo

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LandingCounter is a cumulative Core snapshot. NodeID is set from the authenticated
// agent, never from its payload. Epoch is the Core's monotonically increasing start time.
type LandingCounter struct {
	ID            int64                    `json:"id"`
	InboundTag    string                   `json:"inbound_tag"`
	Epoch         string                   `json:"epoch"`
	UpBytes       int64                    `json:"up_bytes"`
	DownBytes     int64                    `json:"down_bytes"`
	Connections   int                      `json:"connections"`
	OnlineIPCount *int                     `json:"online_ip_count,omitempty"`
	OnlineIPs     []landingcustom.OnlineIP `json:"online_ips,omitempty"`
	// Plan 141: the Core's live inbound (client) connections; nil = the
	// reporter does not know them (older agent / Helper).
	InboundConnections *int  `json:"inbound_connections,omitempty"`
	NodeID             int64 `json:"-"`
	ServerID           int64 `json:"-"` // Set only by the authenticated Custom poller.
	CollectedAt        int64 `json:"-"`
}

type ServerQuotaKey struct {
	UserID int64
	Kind   string
	RefID  int64
}

func LandingRate(n *model.LandingNode, s *model.LandingServer) (int64, float64) {
	mode, ratio := s.FlowType, s.TrafficRatio
	if n.FlowType != nil {
		mode = *n.FlowType
	}
	if n.TrafficRatio != nil {
		ratio = *n.TrafficRatio
	}
	if mode <= 0 {
		mode = 1
	}
	if ratio <= 0 {
		ratio = 1
	}
	return int64(mode), ratio
}

// Preserve the panel's per-direction truncation before the flow-mode multiplier.
func landingBill(v int64, mode int64, ratio float64) int64 { return int64(float64(v)*ratio) * mode }

func landingQuotaKey(userID int64, s *model.LandingServer) ServerQuotaKey {
	if s.Source == "native" {
		return ServerQuotaKey{userID, "node", s.NodeID}
	}
	return ServerQuotaKey{userID, "landing", s.ID}
}

func addServerDelta(m map[ServerQuotaKey]FlowCounterDelta, key ServerQuotaKey, in, out int64) {
	d := m[key]
	d.In += in
	d.Out += out
	m[key] = d
}

// LandingPeriodKey follows the user's reset day, clamped to the last day of each
// month. A disabled reset day keeps a single lifetime period.
func LandingPeriodKey(resetDay int64, now time.Time) int64 {
	if resetDay <= 0 {
		return 1
	}
	year, month, _ := now.Date()
	boundary := func(y int, m time.Month) time.Time {
		day := int(resetDay)
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, now.Location()).Day()
		if day > last {
			day = last
		}
		return time.Date(y, m, day, 0, 0, 0, 0, now.Location())
	}
	start := boundary(year, month)
	if now.Before(start) {
		start = boundary(year, month-1)
	}
	return int64(start.Year()*10000 + int(start.Month())*100 + start.Day())
}

func applyLandingCountersTx(tx *gorm.DB, batch *FlowBatch, now time.Time) error {
	sort.SliceStable(batch.LandingCounters, func(i, j int) bool { return batch.LandingCounters[i].ID < batch.LandingCounters[j].ID })
	for _, c := range batch.LandingCounters {
		if c.ID <= 0 || (c.NodeID <= 0 && c.ServerID <= 0) || c.UpBytes < 0 || c.DownBytes < 0 || c.Connections < 0 {
			return errors.New("invalid landing counter")
		}
		epoch, err := strconv.ParseInt(c.Epoch, 10, 64)
		if c.ServerID == 0 && (err != nil || epoch <= 0) {
			return errors.New("invalid landing epoch")
		}
		var n model.LandingNode
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&n, c.ID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		var s model.LandingServer
		if err = tx.First(&s, n.LandingServerID).Error; err != nil {
			return err
		}
		up, down := c.UpBytes, c.DownBytes
		if c.ServerID > 0 {
			if s.Source != "shared" || s.ID != c.ServerID || c.NodeID != 0 || n.InboundTag != c.InboundTag {
				return errors.New("shared landing counter ownership mismatch")
			}
			if c.CollectedAt <= 0 {
				return errors.New("invalid shared landing collection time")
			}
			if c.CollectedAt <= n.LastCounterAt {
				continue
			}
			// Custom persists lifetime counters across Core epochs. A decrease in
			// either direction is a reset of that direction, not a new Core bill.
			if up >= n.LastSeenUp {
				up -= n.LastSeenUp
			}
			if down >= n.LastSeenDown {
				down -= n.LastSeenDown
			}
			n.LastCounterAt = c.CollectedAt
		} else {
			if s.Source != "native" || s.NodeID != c.NodeID || n.InboundTag != c.InboundTag {
				return errors.New("landing counter ownership mismatch")
			}
			oldEpoch, _ := strconv.ParseInt(n.CounterEpoch, 10, 64)
			if epoch < oldEpoch {
				continue
			}
			if epoch == oldEpoch {
				if up < n.LastSeenUp || down < n.LastSeenDown {
					continue
				}
				up -= n.LastSeenUp
				down -= n.LastSeenDown
			}
		}
		var user model.User
		if err = tx.First(&user, n.UserID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			return err
		}
		period := LandingPeriodKey(user.FlowResetTime, now)
		if n.PeriodKey != period {
			n.BilledIn = 0
			n.BilledOut = 0
			n.PeriodKey = period
		}
		mode, ratio := LandingRate(&n, &s)
		in, out := landingBill(up, mode, ratio), landingBill(down, mode, ratio)
		n.UpBytes += up
		n.DownBytes += down
		n.BilledIn += in
		n.BilledOut += out
		n.LastSeenUp = c.UpBytes
		n.LastSeenDown = c.DownBytes
		n.CounterEpoch = c.Epoch
		n.Connections = c.Connections
		updates := map[string]interface{}{"up_bytes": n.UpBytes, "down_bytes": n.DownBytes, "billed_in": n.BilledIn, "billed_out": n.BilledOut, "period_key": period, "last_seen_up": n.LastSeenUp, "last_seen_down": n.LastSeenDown, "counter_epoch": c.Epoch, "connections": c.Connections, "last_counter_at": n.LastCounterAt}
		// Plan 141: same row write, one more column; a bad optional value never
		// fails the billing batch.
		if c.InboundConnections != nil {
			updates["inbound_connections"] = max(*c.InboundConnections, 0)
		}
		if err = tx.Model(&n).Updates(updates).Error; err != nil {
			return err
		}
		d := batch.Users[n.UserID]
		d.In += in
		d.Out += out
		batch.Users[n.UserID] = d
		batch.QuotaUsage[n.UserID] += in + out
		key := landingQuotaKey(n.UserID, &s)
		addServerDelta(batch.ServerQuotas, key, in, out)
		batch.PoolFlows = append(batch.PoolFlows, PoolFlow{UserID: n.UserID, Landing: key, LandingIn: in, LandingOut: out})
	}
	return nil
}

func serverQuotaDefaultTx(tx *gorm.DB, key ServerQuotaKey) (int64, error) {
	if key.Kind == "node" {
		var n model.Node
		err := tx.First(&n, key.RefID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return n.DefaultUserQuotaBytes, err
	}
	if key.Kind == "landing" {
		var s model.LandingServer
		err := tx.First(&s, key.RefID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return s.DefaultUserQuotaBytes, err
	}
	return 0, errors.New("invalid server kind")
}

func applyServerQuotasTx(tx *gorm.DB, deltas map[ServerQuotaKey]FlowCounterDelta, now time.Time) error {
	keys := make([]ServerQuotaKey, 0, len(deltas))
	for key := range deltas {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.UserID != b.UserID {
			return a.UserID < b.UserID
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.RefID < b.RefID
	})
	for _, key := range keys {
		if key.UserID <= 0 || key.RefID <= 0 {
			continue
		}
		var user model.User
		if err := tx.First(&user, key.UserID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			return err
		}
		period := LandingPeriodKey(user.FlowResetTime, now)
		q := model.UserServerQuota{UserID: key.UserID, ServerKind: key.Kind, ServerRefID: key.RefID, PeriodKey: period, CreatedTime: now.UnixMilli()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&q).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND server_kind = ? AND server_ref_id = ?", key.UserID, key.Kind, key.RefID).First(&q).Error; err != nil {
			return err
		}
		if !q.LimitOverride {
			limit, err := serverQuotaDefaultTx(tx, key)
			if err != nil {
				return err
			}
			q.LimitBytes = limit
		}
		if q.PeriodKey != period {
			q.UsedIn = 0
			q.UsedOut = 0
			q.PeriodKey = period
		}
		d := deltas[key]
		if d.In < 0 || d.Out < 0 {
			return errors.New("negative server quota delta")
		}
		q.UsedIn += d.In
		q.UsedOut += d.Out
		q.Paused = q.LimitBytes > 0 && q.UsedIn+q.UsedOut >= q.LimitBytes
		q.UpdatedTime = now.UnixMilli()
		if err := tx.Save(&q).Error; err != nil {
			return err
		}
	}
	return nil
}

// forwardServerTiers attributes entry to the actual reporter. Relay services
// are shared per tunnel, so intermediate/exit round-robin tiers are estimates.
func (r *Repository) forwardServerTiers(tunnelID, entryNodeID int64) ([][]int64, error) {
	var chain []model.ChainTunnel
	if err := r.db.Where("tunnel_id = ?", tunnelID).Order("id ASC").Find(&chain).Error; err != nil {
		return nil, err
	}
	tiers := map[string]map[int64]bool{}
	if entryNodeID > 0 {
		tiers["entry"] = map[int64]bool{entryNodeID: true}
	}
	for _, c := range chain {
		if c.ChainType != "2" && c.ChainType != "3" {
			continue
		}
		tier := fmt.Sprintf("%s:%d", c.ChainType, c.Inx.Int64)
		if c.ChainType == "3" {
			tier = "exit"
		}
		if tiers[tier] == nil {
			tiers[tier] = map[int64]bool{}
		}
		tiers[tier][c.NodeID] = true
	}
	names := make([]string, 0, len(tiers))
	for name := range tiers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([][]int64, 0, len(tiers))
	for _, name := range names {
		ids := make([]int64, 0, len(tiers[name]))
		for id := range tiers[name] {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		out = append(out, ids)
	}
	return out, nil
}

func (r *Repository) ForwardServerWeights(tunnelID, entryNodeID int64) (map[int64]float64, error) {
	tiers, err := r.forwardServerTiers(tunnelID, entryNodeID)
	if err != nil {
		return nil, err
	}
	weights := map[int64]float64{}
	for _, ids := range tiers {
		weight := 1 / float64(len(ids))
		for _, id := range ids {
			if weight > weights[id] {
				weights[id] = weight
			}
		}
	}
	return weights, nil
}

// ForwardServerTiers exposes the path tiers so one flow item reads its chain
// once for both the per-server usage and the plan-157 pool coverage.
func (r *Repository) ForwardServerTiers(tunnelID, entryNodeID int64) ([][]int64, error) {
	return r.forwardServerTiers(tunnelID, entryNodeID)
}

// ForwardServerUsage divides each tier evenly, assigning integer remainders in
// stable node order so no billed byte disappears. A repeated server counts once.
func (r *Repository) ForwardServerUsage(tunnelID, entryNodeID, in, out int64) (map[int64]FlowCounterDelta, error) {
	tiers, err := r.forwardServerTiers(tunnelID, entryNodeID)
	if err != nil {
		return nil, err
	}
	return ServerUsageFromTiers(tiers, in, out), nil
}

// tierShare is node i's part of a tier of count nodes (remainders to the
// lowest positions, like ServerUsageFromTiers).
func tierShare(total, count int64, i int) int64 {
	v := total / count
	if int64(i) < total%count {
		v++
	}
	return v
}

// ServerUsageFromTiers is ForwardServerUsage on already loaded tiers.
func ServerUsageFromTiers(tiers [][]int64, in, out int64) map[int64]FlowCounterDelta {
	usage := map[int64]FlowCounterDelta{}
	for _, ids := range tiers {
		count := int64(len(ids))
		for i, id := range ids {
			a, b := tierShare(in, count, i), tierShare(out, count, i)
			d := usage[id]
			if a > d.In {
				d.In = a
			}
			if b > d.Out {
				d.Out = b
			}
			usage[id] = d
		}
	}
	return usage
}
