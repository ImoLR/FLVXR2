package repo

import (
	"errors"

	"go-backend/internal/store/model"
	"go-backend/internal/sublink"

	"gorm.io/gorm"
)

// Plan 261 「规则集链接」: stored rule sets of 分流 link entries. Every query
// but the two that need the rules leaves the rules column out.

// SubRuleSetContent is the result of one successful download.
type SubRuleSetContent struct {
	Rules                                       string
	Total, Domain, Suffix, Keyword, IP, Ignored int
	Hash                                        string
}

func subRuleSetMeta(db *gorm.DB) *gorm.DB { return db.Omit("rules") }

func firstOrNil(err error, s *model.UserSubRuleSet) (*model.UserSubRuleSet, error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// GetSubRuleSet loads one set without its rules (nil = none).
func (r *Repository) GetSubRuleSet(id int64) (*model.UserSubRuleSet, error) {
	var s model.UserSubRuleSet
	return firstOrNil(subRuleSetMeta(r.db).Where("id = ?", id).Take(&s).Error, &s)
}

// SubRuleSetByURL: the user's set of one URL (keyed hash), nil = none.
func (r *Repository) SubRuleSetByURL(userID int64, urlHash string) (*model.UserSubRuleSet, error) {
	var s model.UserSubRuleSet
	return firstOrNil(subRuleSetMeta(r.db).Where("user_id = ? AND url_hash = ?", userID, urlHash).Take(&s).Error, &s)
}

// SubRuleSetByPublicID loads a set by its public id without its rules.
func (r *Repository) SubRuleSetByPublicID(publicID string) (*model.UserSubRuleSet, error) {
	var s model.UserSubRuleSet
	return firstOrNil(subRuleSetMeta(r.db).Where("public_id = ?", publicID).Take(&s).Error, &s)
}

// SubRuleSetRules is the stored rule text of one set.
func (r *Repository) SubRuleSetRules(id int64) (string, error) {
	var rows []string
	if err := r.db.Model(&model.UserSubRuleSet{}).Where("id = ?", id).Limit(1).Pluck("rules", &rows).Error; err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", gorm.ErrRecordNotFound
	}
	return rows[0], nil
}

// SubRuleSetsByIDs loads the user's own sets among ids (no rules); other
// users' ids are never returned.
func (r *Repository) SubRuleSetsByIDs(userID int64, ids []int64) (map[int64]model.UserSubRuleSet, error) {
	out := map[int64]model.UserSubRuleSet{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.UserSubRuleSet
	if err := subRuleSetMeta(r.db).Where("user_id = ? AND id IN ?", userID, ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, s := range rows {
		out[s.ID] = s
	}
	return out, nil
}

// CountSubRuleSets: how many sets the user has stored.
func (r *Repository) CountSubRuleSets(userID int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.UserSubRuleSet{}).Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

// CreateSubRuleSet inserts s unless the user already has cap sets (count
// and insert in one transaction).
func (r *Repository) CreateSubRuleSet(s *model.UserSubRuleSet, cap int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if cap > 0 {
			var n int64
			if err := tx.Model(&model.UserSubRuleSet{}).Where("user_id = ?", s.UserID).Count(&n).Error; err != nil {
				return err
			}
			if n >= int64(cap) {
				return ErrSubCap
			}
		}
		return tx.Create(s).Error
	})
}

// SaveSubRuleSetOK stores a successful download (replaces the rules, clears
// the error and the failure count).
func (r *Repository) SaveSubRuleSetOK(id int64, c SubRuleSetContent, at int64) error {
	return r.db.Model(&model.UserSubRuleSet{}).Where("id = ?", id).Updates(map[string]interface{}{
		"rules": c.Rules, "rule_count": c.Total, "count_domain": c.Domain, "count_suffix": c.Suffix, "count_keyword": c.Keyword,
		"count_ip": c.IP, "ignored": c.Ignored, "content_hash": c.Hash, "fetched_at": at, "last_try_at": at, "last_error": "",
		"fail_count": 0, "updated_time": at,
	}).Error
}

// SaveSubRuleSetFail records a failed download; the last good rules stay.
func (r *Repository) SaveSubRuleSetFail(id int64, msg string, at int64) error {
	return r.db.Model(&model.UserSubRuleSet{}).Where("id = ?", id).Updates(map[string]interface{}{
		"last_try_at": at, "last_error": msg, "fail_count": gorm.Expr("fail_count + 1"), "updated_time": at,
	}).Error
}

// SubRuleSetRefs: the set ids the user's subscriptions reference.
func subRuleSetRefs(tx *gorm.DB, userID int64) (map[int64]bool, error) {
	var subs []model.UserSubscription
	if err := tx.Select("id", "routing").Where("user_id = ?", userID).Find(&subs).Error; err != nil {
		return nil, err
	}
	refs := map[int64]bool{}
	for _, s := range subs {
		for _, e := range sublink.ParseRouting(s.Routing) {
			if e.Type == sublink.RouteLink && e.Ref > 0 {
				refs[e.Ref] = true
			}
		}
	}
	return refs, nil
}

// SubRuleSetUses counts, per set of the user, the subscriptions using it.
func (r *Repository) SubRuleSetUses(userID int64) (map[int64]int, error) {
	var subs []model.UserSubscription
	if err := r.db.Select("id", "routing").Where("user_id = ?", userID).Find(&subs).Error; err != nil {
		return nil, err
	}
	out := map[int64]int{}
	for _, s := range subs {
		seen := map[int64]bool{}
		for _, e := range sublink.ParseRouting(s.Routing) {
			if e.Type == sublink.RouteLink && e.Ref > 0 && !seen[e.Ref] {
				seen[e.Ref] = true
				out[e.Ref]++
			}
		}
	}
	return out, nil
}

// GCSubRuleSets deletes the user's sets no subscription references, except
// those created at or after keepFrom (ms; just fetched, not saved yet). It
// returns the deleted public ids. Cheap: two queries plus one delete.
func (r *Repository) GCSubRuleSets(userID int64, keepFrom int64) ([]string, error) {
	var gone []string
	err := r.db.Transaction(func(tx *gorm.DB) error {
		refs, err := subRuleSetRefs(tx, userID)
		if err != nil {
			return err
		}
		var rows []model.UserSubRuleSet
		if err := tx.Select("id", "public_id", "created_time").Where("user_id = ? AND created_time < ?", userID, keepFrom).Find(&rows).Error; err != nil {
			return err
		}
		var ids []int64
		for _, s := range rows {
			if !refs[s.ID] {
				ids = append(ids, s.ID)
				gone = append(gone, s.PublicID)
			}
		}
		if len(ids) == 0 {
			return nil
		}
		return tx.Where("user_id = ? AND id IN ?", userID, ids).Delete(&model.UserSubRuleSet{}).Error
	})
	if err != nil {
		return nil, err
	}
	return gone, nil
}
