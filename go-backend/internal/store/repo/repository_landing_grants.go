package repo

import (
	"errors"
	"log"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const landingServerUserBackfillKey = "landing_server_user_backfill_v1"

// landingAccessCond is the single visibility rule for non-admin users: a direct
// grant OR membership of an enabled user group granted on the server. Both
// EXISTS probes hit primary keys/indexes, so lists stay one query.
func landingAccessCond(serverColumn string) string {
	return "(EXISTS (SELECT 1 FROM landing_server_user AS lu WHERE lu.landing_server_id = " + serverColumn + " AND lu.user_id = ?)" +
		" OR EXISTS (SELECT 1 FROM landing_server_user_group AS lg JOIN user_group_user AS gu ON gu.user_group_id = lg.user_group_id JOIN user_group AS ug ON ug.id = lg.user_group_id AND ug.status = 1 WHERE lg.landing_server_id = " + serverColumn + " AND gu.user_id = ?))"
}

// One-time: before direct grants existed, admins assigned shared servers by
// adding a landing-kind server quota, which granted nothing. Turn those rows
// into grants once; later revokes must not be undone by a restart.
func backfillLandingServerUsers(db *gorm.DB) error {
	var created int64
	err := db.Transaction(func(tx *gorm.DB) error {
		var cfg model.ViteConfig
		err := tx.Where("name = ?", landingServerUserBackfillKey).First(&cfg).Error
		if err == nil && cfg.Value == "done" {
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now().UnixMilli()
		result := tx.Exec(`INSERT INTO landing_server_user (landing_server_id, user_id, created_time)
SELECT DISTINCT q.server_ref_id, q.user_id, ? FROM user_server_quota AS q
JOIN landing_server AS s ON s.id = q.server_ref_id
JOIN "user" AS u ON u.id = q.user_id
WHERE q.server_kind = 'landing' AND NOT EXISTS (SELECT 1 FROM landing_server_user AS lu WHERE lu.landing_server_id = q.server_ref_id AND lu.user_id = q.user_id)`, now)
		if result.Error != nil {
			return result.Error
		}
		created = result.RowsAffected
		cfg.Name, cfg.Value, cfg.Time = landingServerUserBackfillKey, "done", now
		return tx.Save(&cfg).Error
	})
	if err == nil && created > 0 {
		log.Printf("[landing-grant-backfill] granted=%d from landing server quotas", created)
	}
	return err
}

// LandingUserServerIDs lists the servers granted directly to a user.
func (r *Repository) LandingUserServerIDs(userID int64) ([]int64, error) {
	out := []int64{}
	err := r.db.Model(&model.LandingServerUser{}).Where("user_id = ?", userID).Order("landing_server_id").Pluck("landing_server_id", &out).Error
	return out, err
}

// LandingServerDirectUserCount counts direct grants for the admin server view.
func (r *Repository) LandingServerDirectUserCount(serverID int64) (int64, error) {
	var count int64
	err := r.db.Model(&model.LandingServerUser{}).Where("landing_server_id = ?", serverID).Count(&count).Error
	return count, err
}

// replaceLandingUserGrantsTx makes the user's direct grants exactly serverIDs.
func replaceLandingUserGrantsTx(tx *gorm.DB, userID int64, serverIDs []int64, now int64) error {
	want := map[int64]bool{}
	for _, id := range serverIDs {
		if id <= 0 {
			return errors.New("落地服务器不存在")
		}
		want[id] = true
	}
	ids := make([]int64, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		var count int64
		if err := tx.Model(&model.LandingServer{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(ids)) {
			return errors.New("落地服务器不存在")
		}
	}
	q := tx.Where("user_id = ?", userID)
	if len(ids) > 0 {
		q = q.Where("landing_server_id NOT IN ?", ids)
	}
	if err := q.Delete(&model.LandingServerUser{}).Error; err != nil {
		return err
	}
	return grantLandingServersTx(tx, userID, ids, now)
}

func grantLandingServersTx(tx *gorm.DB, userID int64, serverIDs []int64, now int64) error {
	for _, id := range serverIDs {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.LandingServerUser{LandingServerID: id, UserID: userID, CreatedTime: now}).Error; err != nil {
			return err
		}
	}
	return nil
}
