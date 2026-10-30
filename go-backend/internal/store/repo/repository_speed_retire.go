package repo

import (
	"errors"
	"log"
	"sort"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// Plan 170: 限速规则 (speed_limit, 竞技场) and the rule's own speed are retired.
// Nothing reads the references at runtime any more; these helpers keep the
// stored data clean.

// speedLimitRefs are the columns that point at speed_limit.id.
var speedLimitRefs = []struct {
	model  interface{}
	name   string
	column string
}{
	{&model.Forward{}, "forward", "speed_id"},
	{&model.Tunnel{}, "tunnel", "speed_id"},
	{&model.UserTunnel{}, "user_tunnel", "speed_id"},
	{&model.Node{}, "node", "speed_id"},
	{&model.User{}, "user", "speed_limit_id"},
}

// DeleteSpeedLimitAndRefs deletes a 限速规则 and every reference to it in one
// transaction. It returns the forwards that used it — directly (speed_id) or
// through their user tunnel (user_tunnel.speed_id of the same user and
// tunnel) — so the caller can re-push them.
func (r *Repository) DeleteSpeedLimitAndRefs(id int64) ([]int64, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var forwardIDs []int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var direct []int64
		if err := tx.Model(&model.Forward{}).Where("speed_id = ?", id).Order("id ASC").Pluck("id", &direct).Error; err != nil {
			return err
		}
		var viaUserTunnel []int64
		if err := tx.Table("forward AS f").
			Joins("JOIN user_tunnel AS ut ON ut.user_id = f.user_id AND ut.tunnel_id = f.tunnel_id").
			Where("ut.speed_id = ?", id).
			Distinct("f.id").Order("f.id ASC").Pluck("f.id", &viaUserTunnel).Error; err != nil {
			return err
		}
		forwardIDs = uniquePositiveInt64s(append(direct, viaUserTunnel...))
		sort.Slice(forwardIDs, func(i, j int) bool { return forwardIDs[i] < forwardIDs[j] })
		for _, ref := range speedLimitRefs {
			if err := tx.Model(ref.model).Where(ref.column+" = ?", id).Update(ref.column, nil).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ?", id).Delete(&model.SpeedLimit{}).Error
	})
	if err != nil {
		return nil, err
	}
	return forwardIDs, nil
}

// SpeedRefCleanup counts the references cleared by cleanupSpeedLimitOrphans.
type SpeedRefCleanup map[string]int64

// cleanupSpeedLimitOrphans NULLs every reference to a speed_limit id that no
// longer exists (left behind by 限速规则 deletes before plan 170). Idempotent:
// a second run changes nothing. Runs once at start.
func cleanupSpeedLimitOrphans(db *gorm.DB) (SpeedRefCleanup, error) {
	out := SpeedRefCleanup{}
	if db == nil {
		return out, errors.New("nil db")
	}
	if !db.Migrator().HasTable(&model.SpeedLimit{}) {
		return out, nil
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, ref := range speedLimitRefs {
			res := tx.Model(ref.model).
				Where(ref.column+" IS NOT NULL AND "+ref.column+" NOT IN (?)", tx.Model(&model.SpeedLimit{}).Select("id")).
				Update(ref.column, nil)
			if res.Error != nil {
				return res.Error
			}
			out[ref.name+"."+ref.column] = res.RowsAffected
		}
		return nil
	})
	if err != nil {
		return SpeedRefCleanup{}, err
	}
	total := int64(0)
	for _, n := range out {
		total += n
	}
	if total > 0 {
		log.Printf("[speed-ref-cleanup] cleared references to deleted 限速规则: %v", out)
	}
	return out, nil
}
