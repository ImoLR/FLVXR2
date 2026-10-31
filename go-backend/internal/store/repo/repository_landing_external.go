package repo

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 177 「外部节点」 / 「链式代理」 data access.

// ErrExternalNodeNotFound: missing or not the caller's.
var ErrExternalNodeNotFound = errors.New("外部节点不存在")

// LandingExternalUse is a landing node that chains through an external node.
type LandingExternalUse struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	LandingServerID int64  `json:"landingServerId"`
	ExternalID      int64  `json:"-"`
}

// ListLandingExternalNodes returns a user's external nodes (oldest first).
func (r *Repository) ListLandingExternalNodes(userID int64) ([]model.LandingExternalNode, error) {
	var out []model.LandingExternalNode
	err := r.db.Where("user_id = ?", userID).Order("id ASC").Find(&out).Error
	return out, err
}

// LandingExternalUses lists, per external node of the user, the landing
// nodes chaining through it (one query).
func (r *Repository) LandingExternalUses(userID int64) (map[int64][]LandingExternalUse, error) {
	var rows []model.LandingNode
	if err := r.db.Select("id", "name", "landing_server_id", "chain_external_id").Where("user_id = ? AND chain_external_id > 0", userID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := map[int64][]LandingExternalUse{}
	for _, n := range rows {
		out[n.ChainExternalID] = append(out[n.ChainExternalID], LandingExternalUse{ID: n.ID, Name: n.Name, LandingServerID: n.LandingServerID, ExternalID: n.ChainExternalID})
	}
	return out, nil
}

// GetLandingExternalNode loads one external node of the given owner.
func (r *Repository) GetLandingExternalNode(id, userID int64) (*model.LandingExternalNode, error) {
	var n model.LandingExternalNode
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&n).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrExternalNodeNotFound
		}
		return nil, err
	}
	return &n, nil
}

// LandingExternalNodesByIDs batch-loads external nodes (landing lists).
func (r *Repository) LandingExternalNodesByIDs(ids []int64) (map[int64]model.LandingExternalNode, error) {
	out := map[int64]model.LandingExternalNode{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.LandingExternalNode
	if err := r.db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, n := range rows {
		out[n.ID] = n
	}
	return out, nil
}

// ExternalCreateResult reports what CreateLandingExternalNodes did.
type ExternalCreateResult struct {
	Created []model.LandingExternalNode
	// Duplicates: names of items the user already has (same upstream).
	Duplicates []string
	// OverCap: items not added because the user reached the cap.
	OverCap int
}

// CreateLandingExternalNodes adds external nodes for a user in one
// transaction with the user row locked, so parallel adds never pass the cap
// (cap <= 0: unlimited). Items whose upstream the user already has (or that
// repeat within the batch) are skipped as duplicates.
func (r *Repository) CreateLandingExternalNodes(userID int64, items []model.LandingExternalNode, cap int) (*ExternalCreateResult, error) {
	res := &ExternalCreateResult{}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var u model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&u, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("用户不存在")
			}
			return err
		}
		var existing []model.LandingExternalNode
		if err := tx.Select("id", "name", "upstream_hash").Where("user_id = ?", userID).Find(&existing).Error; err != nil {
			return err
		}
		seen := map[string]string{}
		for _, e := range existing {
			seen[e.UpstreamHash] = e.Name
		}
		count := len(existing)
		now := time.Now().UnixMilli()
		for _, it := range items {
			if name, dup := seen[it.UpstreamHash]; dup {
				if name == "" {
					name = it.Name
				}
				res.Duplicates = append(res.Duplicates, name)
				continue
			}
			if cap > 0 && count >= cap {
				res.OverCap++
				continue
			}
			it.ID, it.UserID, it.CreatedTime, it.UpdatedTime = 0, userID, now, now
			if err := tx.Create(&it).Error; err != nil {
				return err
			}
			seen[it.UpstreamHash] = it.Name
			count++
			res.Created = append(res.Created, it)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// RenameLandingExternalNode renames one of the owner's external nodes.
func (r *Repository) RenameLandingExternalNode(id, userID int64, name string) error {
	res := r.db.Model(&model.LandingExternalNode{}).Where("id = ? AND user_id = ?", id, userID).Updates(map[string]interface{}{"name": name, "updated_time": time.Now().UnixMilli()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrExternalNodeNotFound
	}
	return nil
}

// DeleteLandingExternalNode deletes an unused external node. While landing
// nodes chain through it, it is kept and they are returned (names for the
// refusal message).
func (r *Repository) DeleteLandingExternalNode(id, userID int64) ([]LandingExternalUse, error) {
	var inUse []LandingExternalUse
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var n model.LandingExternalNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, userID).First(&n).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrExternalNodeNotFound
			}
			return err
		}
		var rows []model.LandingNode
		if err := tx.Select("id", "name", "landing_server_id").Where("chain_external_id = ?", id).Order("id ASC").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			for _, x := range rows {
				inUse = append(inUse, LandingExternalUse{ID: x.ID, Name: x.Name, LandingServerID: x.LandingServerID, ExternalID: id})
			}
			return nil
		}
		return tx.Delete(&model.LandingExternalNode{}, id).Error
	})
	return inUse, err
}

// ExternalInUseError formats the delete refusal with the landing node names.
func ExternalInUseError(uses []LandingExternalUse) error {
	names := make([]string, 0, len(uses))
	for i, u := range uses {
		if i == 5 {
			names = append(names, fmt.Sprintf("等 %d 个", len(uses)))
			break
		}
		names = append(names, "「"+u.Name+"」")
	}
	return fmt.Errorf("这个外部节点正被落地节点 %s 用作链式代理，请先把它们改回直连或换一个外部节点再删除", strings.Join(names, "、"))
}

// SetLandingNodeChain switches a landing node's 链式代理 (0 = 直连). The
// external node must belong to the landing node's owner; it is checked again
// here inside the transaction (the handler checked before probing).
func (r *Repository) SetLandingNodeChain(nodeID, externalID int64) (*model.LandingNode, error) {
	var out model.LandingNode
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&out, nodeID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("落地节点不存在")
			}
			return err
		}
		if out.DeletePending {
			return errors.New("节点正在删除")
		}
		if externalID > 0 {
			var count int64
			if err := tx.Model(&model.LandingExternalNode{}).Where("id = ? AND user_id = ?", externalID, out.UserID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return ErrExternalNodeNotFound
			}
		}
		if out.ChainExternalID == externalID {
			return nil
		}
		now := time.Now().UnixMilli()
		if err := tx.Model(&model.LandingNode{}).Where("id = ?", nodeID).Updates(map[string]interface{}{"chain_external_id": externalID, "updated_time": now}).Error; err != nil {
			return err
		}
		out.ChainExternalID, out.UpdatedTime = externalID, now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// LandingPublicHosts lists every landing server's public host (shared mmwx
// servers have no FLVX node row), so external nodes never point at one of
// our own landings.
func (r *Repository) LandingPublicHosts() ([]string, error) {
	var hosts []string
	err := r.db.Model(&model.LandingServer{}).Where("public_host <> ''").Pluck("public_host", &hosts).Error
	return hosts, err
}

// SharedChainSupported: the share takes FLVX egress plans (landing-egress-v1)
// and its owner allows 自定义出站与路由 — what 链式代理 needs on a shared server.
func SharedChainSupported(s *model.LandingServer) bool {
	e := SharedLandingCaps(s).Egress
	return e.Supports(landingegress.Version) && e.Allowed
}
