package repo

import (
	"errors"
	"fmt"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RuleLimitUnlimited reports whether a user's rule allowance (user.num, 规则数)
// means "no limit": 0/negative or the UI's 99999 sentinel.
func RuleLimitUnlimited(num int) bool { return num <= 0 || num >= 99999 }

// RuleLimitError refuses a create that would push the owner past user.num.
// Rules that already exist above the limit are never touched; only new ones
// are refused.
type RuleLimitError struct{ Limit int }

func (e *RuleLimitError) Error() string {
	return fmt.Sprintf("已达到规则数上限（%d 条），请删除不用的规则后再新建", e.Limit)
}

// IsRuleLimitError reports whether err is a RuleLimitError.
func IsRuleLimitError(err error) bool {
	var target *RuleLimitError
	return errors.As(err, &target)
}

// countUserRules counts every rule of the user (any status, chain rules
// included) plus 「我自己的节点」 creates that have no rule row yet.
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
