package repo

import (
	"errors"
	"fmt"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RuleLimitUnlimited reports whether a user's allowance (user.num 总转发数 or
// user.leikwan_num 利群转发数) means "no limit": 0/negative or the UI's 99999
// sentinel.
func RuleLimitUnlimited(num int) bool { return num <= 0 || num >= 99999 }

// RuleLimitError refuses a create that would push the owner past user.num.
// Rules that already exist above the limit are never touched; only new ones
// are refused.
type RuleLimitError struct{ Limit int }

func (e *RuleLimitError) Error() string {
	return fmt.Sprintf("已达到转发数上限（%d 条），请删除不用的转发后再添加", e.Limit)
}

// LeiKwanRuleLimitError (plan 280) refuses a new 利群 forward past
// user.leikwan_num (利群转发数). Existing rows above the limit stay.
type LeiKwanRuleLimitError struct{ Limit int }

func (e *LeiKwanRuleLimitError) Error() string {
	return fmt.Sprintf("已达到利群转发数上限（%d 条），请删除不用的利群转发后再添加", e.Limit)
}

// IsRuleLimitError reports whether err is a RuleLimitError.
func IsRuleLimitError(err error) bool {
	var target *RuleLimitError
	return errors.As(err, &target)
}

// countUserRules counts every forward of the user (any status, chain rules
// included) plus 「我自己的节点」 creates that have no rule row yet. Plan 280:
// the user's 利群 rules no longer count here (they have their own
// 利群转发数, user.leikwan_num); their automatic 备线 forwards belong to the
// admin and were never counted.
func countUserRules(db *gorm.DB, userID int64) (int64, error) {
	var rules, pending int64
	if err := db.Model(&model.Forward{}).Where("user_id = ?", userID).Count(&rules).Error; err != nil {
		return 0, err
	}
	if err := db.Model(&model.ForwardChain{}).Where("user_id = ? AND state = ? AND op = ? AND forward_id = 0", userID, model.ChainStateCreating, "create").Count(&pending).Error; err != nil {
		return 0, err
	}
	return rules + pending, nil
}

// CheckLeiKwanRuleLimit is the check of a new 利群 forward: user status and
// expiry first (a 利群 rule follows the user's 到期, plan 268), then the
// user's 利群转发数 (user.leikwan_num, plan 280) against the 利群 rules only.
// Callers hold leiKwanRuleMu, which serialises every 利群 create, so two
// concurrent creates of one user cannot overshoot the limit.
func (r *Repository) CheckLeiKwanRuleLimit(userID int64, nowMs int64, enforceNum bool) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	var u model.User
	if err := r.db.Select("id", "leikwan_num", "status", "exp_time").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("用户不存在")
		}
		return err
	}
	if u.Status != 1 {
		return errors.New("账号已禁用")
	}
	if u.ExpTime > 0 && u.ExpTime <= nowMs {
		return errors.New("账号已到期，不能添加利群转发")
	}
	if !enforceNum || RuleLimitUnlimited(u.LeiKwanNum) {
		return nil
	}
	var n int64
	if err := r.db.Model(&model.LeiKwanRule{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		return err
	}
	if n+1 > int64(u.LeiKwanNum) {
		return &LeiKwanRuleLimitError{Limit: u.LeiKwanNum}
	}
	return nil
}

// UserLeiKwanRuleLimit (plan 280): the user's 利群转发数 and how many 利群
// forwards they have (the 利群汇聚 user page shows 已用 x / N).
func (r *Repository) UserLeiKwanRuleLimit(userID int64) (limit int, used int64, err error) {
	if r == nil || r.db == nil {
		return 0, 0, errors.New("repository not initialized")
	}
	var u model.User
	if err := r.db.Select("id", "leikwan_num").First(&u, userID).Error; err != nil {
		return 0, 0, err
	}
	if err := r.db.Model(&model.LeiKwanRule{}).Where("user_id = ?", userID).Count(&used).Error; err != nil {
		return 0, 0, err
	}
	return u.LeiKwanNum, used, nil
}

// SetUserLeiKwanNum (plan 280) stores the user's 利群转发数. A separate
// update (not part of CreateUser) because the column's DEFAULT 3 would
// otherwise swallow an explicit 0 (= 不限) on insert.
func (r *Repository) SetUserLeiKwanNum(userID int64, n int) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Model(&model.User{}).Where("id = ?", userID).Update("leikwan_num", n).Error
}

func ruleLimitCheck(db *gorm.DB, userID int64, num int, adding int64) error {
	if RuleLimitUnlimited(num) || adding <= 0 {
		return nil
	}
	n, err := countUserRules(db, userID)
	if err != nil {
		return err
	}
	if n+adding > int64(num) {
		return &RuleLimitError{Limit: num}
	}
	return nil
}

// CheckUserRuleLimit is the cheap early check (no lock) so a user at the limit
// is refused before any side effect. The authoritative check runs again inside
// the create transaction.
func (r *Repository) CheckUserRuleLimit(userID int64, adding int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	var u model.User
	if err := r.db.Select("id", "num").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("用户不存在")
		}
		return err
	}
	return ruleLimitCheck(r.db, userID, u.Num, adding)
}

// lockUserRuleLimitTx locks the owner row (PostgreSQL FOR UPDATE; SQLite runs
// on one connection, so transactions are already serialised) and checks the
// allowance, so concurrent creates of one user cannot overshoot it.
func lockUserRuleLimitTx(tx *gorm.DB, userID int64, adding int64) error {
	var u model.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "num").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("用户不存在")
		}
		return err
	}
	return ruleLimitCheck(tx, userID, u.Num, adding)
}
