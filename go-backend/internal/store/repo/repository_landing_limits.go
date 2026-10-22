package repo

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 131: the per-user total landing-node cap follows a global default
// unless the admin gave the user an own value.
const (
	// LandingDefaultMaxNodesKey is the vite_config key of the global default.
	LandingDefaultMaxNodesKey = "landing_default_max_nodes"
	// LandingMaxNodesLimit bounds both the global and the per-user value.
	LandingMaxNodesLimit = 10000
	// LandingMaxNodesFollowGlobal passed as maxNodes to the user setting save
	// switches the user to the global default.
	LandingMaxNodesFollowGlobal = -1

	landingMaxNodesCustomBackfillKey = "landing_user_max_nodes_custom_v1"
)

// LandingMaxNodes is a user's total landing-node cap and where it comes from.
type LandingMaxNodes struct {
	// Effective is the cap in force (meaningless when Unlimited).
	Effective int
	// Source is "custom" (own value), "global" (follows the default) or
	// "admin" (admin accounts have no total cap).
	Source string
	// Global is the current global default.
	Global int
	// Unlimited is true for admin accounts.
	Unlimited bool
}

// Allows reports whether a user owning count nodes may get one more.
func (m LandingMaxNodes) Allows(count int64) bool {
	return m.Unlimited || count < int64(m.Effective)
}

// ParseLandingDefaultMaxNodes validates a global default value (0–10000).
func ParseLandingDefaultMaxNodes(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 || n > LandingMaxNodesLimit {
		return 0, fmt.Errorf("每个用户默认可建落地节点总数须为 0–%d 的整数", LandingMaxNodesLimit)
	}
	return n, nil
}

// landingDefaultMaxNodes reads the global default; missing or unreadable
// values fall back to the old hard default so nothing changes on upgrade.
func landingDefaultMaxNodes(db *gorm.DB) (int, error) {
	var rows []model.ViteConfig
	if err := db.Select("value").Where("name = ?", LandingDefaultMaxNodesKey).Limit(1).Find(&rows).Error; err != nil {
		return DefaultLandingUserMaxNodes, err
	}
	if len(rows) == 0 {
		return DefaultLandingUserMaxNodes, nil
	}
	n, err := ParseLandingDefaultMaxNodes(rows[0].Value)
	if err != nil {
		return DefaultLandingUserMaxNodes, nil
	}
	return n, nil
}

// effectiveLandingMaxNodes is the single place that decides a user's total
// landing-node cap (creation, transfer, user settings API, server list).
func effectiveLandingMaxNodes(db *gorm.DB, u *model.User) (LandingMaxNodes, model.LandingUserSetting, error) {
	out := LandingMaxNodes{}
	global, err := landingDefaultMaxNodes(db)
	if err != nil {
		return out, model.LandingUserSetting{}, err
	}
	out.Global = global
	setting, err := landingUserSetting(db, u.ID)
	if err != nil {
		return out, setting, err
	}
	switch {
	case u.RoleID == 0:
		out.Source, out.Unlimited, out.Effective = "admin", true, LandingMaxNodesLimit
	case setting.MaxNodesCustom:
		out.Source, out.Effective = "custom", setting.MaxNodes
	default:
		out.Source, out.Effective = "global", global
	}
	return out, setting, nil
}

// EffectiveLandingMaxNodes returns the user's total landing-node cap.
func (r *Repository) EffectiveLandingMaxNodes(userID int64) (LandingMaxNodes, error) {
	var u model.User
	if err := r.db.Select("id", "role_id").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return LandingMaxNodes{}, errors.New("用户不存在")
		}
		return LandingMaxNodes{}, err
	}
	m, _, err := effectiveLandingMaxNodes(r.db, &u)
	return m, err
}

// LandingDefaultMaxNodes returns the global default total cap.
func (r *Repository) LandingDefaultMaxNodes() (int, error) {
	return landingDefaultMaxNodes(r.db)
}

// SetLandingDefaultMaxNodes stores the global default. Users following it
// pick it up at once; lowering it never touches existing nodes.
func (r *Repository) SetLandingDefaultMaxNodes(n int) error {
	if n < 0 || n > LandingMaxNodesLimit {
		return fmt.Errorf("每个用户默认可建落地节点总数须为 0–%d 的整数", LandingMaxNodesLimit)
	}
	return r.UpsertConfig(LandingDefaultMaxNodesKey, strconv.Itoa(n), time.Now().UnixMilli())
}

// backfillLandingUserMaxNodesCustom runs once when the flag column arrives:
// rows still holding the old hard default 5 cannot be told apart from "never
// touched" and follow the global default (initially 5 too); every other
// value stays the user's own value.
func backfillLandingUserMaxNodesCustom(db *gorm.DB) error {
	var marked int64
	err := db.Transaction(func(tx *gorm.DB) error {
		var cfg model.ViteConfig
		err := tx.Where("name = ?", landingMaxNodesCustomBackfillKey).First(&cfg).Error
		if err == nil && cfg.Value == "done" {
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		result := tx.Model(&model.LandingUserSetting{}).Where("max_nodes <> ?", DefaultLandingUserMaxNodes).Update("max_nodes_custom", true)
		if result.Error != nil {
			return result.Error
		}
		marked = result.RowsAffected
		cfg.Name, cfg.Value, cfg.Time = landingMaxNodesCustomBackfillKey, "done", time.Now().UnixMilli()
		return tx.Save(&cfg).Error
	})
	if err == nil && marked > 0 {
		log.Printf("[landing-max-nodes-backfill] custom=%d (others follow the global default)", marked)
	}
	return err
}

// LandingTransfer is the outcome of an owner change.
type LandingTransfer struct {
	Node       model.LandingNode
	FromUserID int64
	FromUser   string
	ToUser     string
}

// TransferLandingNode moves a landing node to another owner (admin only, the
// handler checks). Port, credentials, limits and counters stay on the node.
// Lock order follows flow ingestion (landing node before user rows) and
// creation (user before server), so creates and transfers for the target are
// serialized on its user row and both caps count the moved node.
func (r *Repository) TransferLandingNode(nodeID, targetUserID int64) (*LandingTransfer, error) {
	out := &LandingTransfer{}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		n := &out.Node
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(n, nodeID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("落地节点不存在")
			}
			return err
		}
		if n.DeletePending {
			return errors.New("节点正在删除，不能转移")
		}
		if n.UserID == targetUserID {
			return errors.New("节点已属于该用户")
		}
		var target model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&target, targetUserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("目标用户不存在")
			}
			return err
		}
		now := time.Now().UnixMilli()
		if target.Status != 1 || (target.ExpTime > 0 && target.ExpTime <= now) {
			return errors.New("目标用户已停用或到期")
		}
		var s model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, n.LandingServerID).Error; err != nil {
			return errors.New("落地服务器不存在")
		}
		if s.ChainOnly {
			return errors.New("线路机专用落地节点不能转移")
		}
		if target.RoleID != 0 {
			ok, err := landingPermission(tx, target.ID, s.ID)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("该用户无权使用此落地服务器：请先在用户编辑「落地」的「可用落地服务器」中授权，或把用户加入可见用户组")
			}
		}
		limit, _, err := effectiveLandingMaxNodes(tx, &target)
		if err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.LandingNode{}).Where("user_id = ?", target.ID).Count(&count).Error; err != nil {
			return err
		}
		if !limit.Allows(count) {
			return fmt.Errorf("目标用户已达到落地节点总数上限（%d 个）", limit.Effective)
		}
		if err := tx.Model(&model.LandingNode{}).Where("user_id = ? AND landing_server_id = ?", target.ID, s.ID).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(s.MaxNodesPerUser) {
			return fmt.Errorf("目标用户在该服务器的节点已达到每人上限（%d 个）", s.MaxNodesPerUser)
		}
		// The landing billing exemption belongs to the owner's own rules; a
		// rule of anyone but the new owner must be detached first.
		if err := landingNodeForeignRuleError(tx, n.ID, target.ID); err != nil {
			return err
		}
		var from model.User
		if err := tx.Select("id", "user").First(&from, n.UserID).Error; err == nil {
			out.FromUser = from.User
		}
		out.FromUserID, out.ToUser = n.UserID, target.User
		n.UserID, n.UpdatedTime = target.ID, now
		if err := tx.Model(&model.LandingNode{}).Where("id = ?", n.ID).Updates(map[string]interface{}{"user_id": target.ID, "updated_time": now}).Error; err != nil {
			return err
		}
		return tx.Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("revision", gorm.Expr("revision + 1")).Error
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// landingNodeForeignRuleError names (bounded) the rules of users other than
// keepUserID that reference the node.
func landingNodeForeignRuleError(tx *gorm.DB, id, keepUserID int64) error {
	q := func() *gorm.DB {
		return tx.Model(&model.Forward{}).Where("landing_node_id = ? AND user_id <> ?", id, keepUserID)
	}
	var count int64
	if err := q().Count(&count).Error; err != nil || count == 0 {
		return err
	}
	var names []string
	if err := q().Order("id ASC").Limit(5).Pluck("name", &names).Error; err != nil {
		return err
	}
	label := "「" + strings.Join(names, "」、「") + "」"
	if count > 5 {
		label += fmt.Sprintf("等 %d 条", count)
	}
	return fmt.Errorf("该节点仍被原所属用户的规则 %s 引用，请先删除或修改规则后再转移", label)
}

// LandingCustomMaxNodesUserCount counts users with an own total cap (they do
// not follow the global default).
func (r *Repository) LandingCustomMaxNodesUserCount() (int64, error) {
	var n int64
	err := r.db.Model(&model.LandingUserSetting{}).Where("max_nodes_custom = ?", true).Count(&n).Error
	return n, err
}
