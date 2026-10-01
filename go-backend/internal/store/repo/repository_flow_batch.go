package repo

import (
	"errors"
	"sort"
	"time"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// FlowCounterDelta is an increment of the in_flow (upload) and out_flow (download) columns.
type FlowCounterDelta struct {
	In  int64
	Out int64
}

// FlowBatch is the billed effect of one agent flow upload.
type FlowBatch struct {
	Forwards    map[int64]FlowCounterDelta
	Users       map[int64]FlowCounterDelta
	UserTunnels map[int64]FlowCounterDelta
	// QuotaUsage maps a user id to the billed bytes added to its daily/monthly quota.
	// Users listed with 0 bytes still get their quota windows rolled.
	QuotaUsage map[int64]int64
	// PeerShares maps a peer share id to the raw bytes added to its current_flow.
	PeerShares map[int64]int64
}

// Empty reports whether the batch writes nothing.
func (b FlowBatch) Empty() bool {
	return len(b.Forwards) == 0 && len(b.Users) == 0 && len(b.UserTunnels) == 0 &&
		len(b.QuotaUsage) == 0 && len(b.PeerShares) == 0
}

// ApplyFlowBatch writes a whole flow upload in one transaction: either every counter of the
// batch is updated or none is, so an agent may resend a rejected upload without the bytes
// being counted twice. Rows are updated in ascending id order to keep lock order stable.
// It returns the updated (normalized) quota view of every user in QuotaUsage.
func (r *Repository) ApplyFlowBatch(batch FlowBatch, now time.Time) (map[int64]*model.UserQuotaView, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	views := make(map[int64]*model.UserQuotaView, len(batch.QuotaUsage))
	if batch.Empty() {
		return views, nil
	}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := addFlowCountersTx(tx, &model.Forward{}, batch.Forwards); err != nil {
			return err
		}
		if err := addFlowCountersTx(tx, &model.User{}, batch.Users); err != nil {
			return err
		}
		if err := addFlowCountersTx(tx, &model.UserTunnel{}, batch.UserTunnels); err != nil {
			return err
		}
		for _, userID := range sortedFlowKeys(batch.QuotaUsage) {
			if userID <= 0 {
				continue
			}
			view, err := r.addUserQuotaUsageTx(tx, userID, batch.QuotaUsage[userID], now)
			if err != nil {
				return err
			}
			views[userID] = view
		}
		for _, shareID := range sortedFlowKeys(batch.PeerShares) {
			delta := batch.PeerShares[shareID]
			if shareID <= 0 || delta <= 0 {
				continue
			}
			if err := tx.Model(&model.PeerShare{}).Where("id = ?", shareID).
				UpdateColumns(map[string]interface{}{
					"current_flow": gorm.Expr("current_flow + ?", delta),
					"updated_time": now.UnixMilli(),
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for userID, view := range views {
		views[userID] = normalizeUserQuotaView(view, now)
	}
	return views, nil
}

func addFlowCountersTx(tx *gorm.DB, table interface{}, deltas map[int64]FlowCounterDelta) error {
	for _, id := range sortedFlowKeys(deltas) {
		delta := deltas[id]
		if id <= 0 || (delta.In == 0 && delta.Out == 0) {
			continue
		}
		if err := tx.Model(table).Where("id = ?", id).
			UpdateColumns(map[string]interface{}{
				"in_flow":  gorm.Expr("in_flow + ?", delta.In),
				"out_flow": gorm.Expr("out_flow + ?", delta.Out),
			}).Error; err != nil {
			return err
		}
	}
	return nil
}

func sortedFlowKeys[V any](m map[int64]V) []int64 {
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
