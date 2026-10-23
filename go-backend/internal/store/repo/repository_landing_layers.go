package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 134: the global rule group (landing_egress_setting row 1), node rules
// (landing_node.route_*) and the node-rule caps (global defaults in
// vite_config, per-user own values in landing_user_setting).

const (
	LandingRuleMaxNodeKey  = "landing_node_rule_max_per_node"
	LandingRuleMaxTotalKey = "landing_node_rule_max_per_user"
	// Initial global defaults (user decision 2026-10-22).
	DefaultLandingRuleMaxNode  = 5
	DefaultLandingRuleMaxTotal = 10
	// LandingRuleCapLimit bounds the per-node value (the hard per-node list
	// size); totals may go higher.
	LandingRuleTotalLimit = 1000
	// LandingRuleCapFollowGlobal passed as a cap switches the user to the
	// global default.
	LandingRuleCapFollowGlobal = -1
)

// GlobalFromSetting decodes the global group of the settings row.
func GlobalFromSetting(s model.LandingEgressSetting) landingegress.Global {
	g := landingegress.Global{Forced: []landingegress.Rule{}, Default: []landingegress.Rule{}, DefaultAction: s.GlobalAction}
	_ = json.Unmarshal([]byte(s.GlobalForced), &g.Forced)
	_ = json.Unmarshal([]byte(s.GlobalDefault), &g.Default)
	if g.DefaultAction == "" {
		g.DefaultAction = landingegress.ActionDirect
	}
	return g
}

func landingGlobal(db *gorm.DB) (landingegress.Global, int64, error) {
	g, _, updated, err := landingSettings(db)
	return g, updated, err
}

// GetLandingGlobal returns the global group and when it was last saved.
func (r *Repository) GetLandingGlobal() (landingegress.Global, int64, error) {
	return landingGlobal(r.db)
}

// GetLandingGlobalSettings also returns the built-in rule switches (plan
// 140), from the same single row read.
func (r *Repository) GetLandingGlobalSettings() (landingegress.Global, landingegress.AbuseSwitches, int64, error) {
	return landingSettings(r.db)
}

// SaveLandingGlobal validates and stores the global group (only the global
// columns of row 1; the source whitelist stays).
func (r *Repository) SaveLandingGlobal(g *landingegress.Global) (int64, error) {
	now := time.Now().UnixMilli()
	err := r.db.Transaction(func(tx *gorm.DB) error {
		ids, err := outboundIDs(tx)
		if err != nil {
			return err
		}
		if err := landingegress.NormalizeGlobal(g, ids); err != nil {
			return err
		}
		forced, _ := json.Marshal(g.Forced)
		deflt, _ := json.Marshal(g.Default)
		row := model.LandingEgressSetting{ID: 1, SourceWhitelist: "[]", GlobalForced: string(forced), GlobalDefault: string(deflt), GlobalAction: g.DefaultAction, GlobalUpdatedTime: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"global_forced", "global_default", "global_action", "global_updated_time"})}).Create(&row).Error
	})
	return now, err
}

// NodeRulesFromModel decodes a node's own rules and default outbound.
func NodeRulesFromModel(n model.LandingNode) ([]landingegress.Rule, string) {
	rules := []landingegress.Rule{}
	if n.RouteRules != "" {
		_ = json.Unmarshal([]byte(n.RouteRules), &rules)
	}
	return rules, n.RouteDefault
}

// LandingRuleCaps is a user's node-rule caps and where they come from.
type LandingRuleCaps struct {
	PerNode, Total             int
	PerNodeSource, TotalSource string // custom | global | admin
	GlobalPerNode, GlobalTotal int
	Unlimited                  bool // admin accounts
}

func parseRuleCap(value string, max int, label string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 || n > max {
		return 0, fmt.Errorf("%s须为 0–%d 的整数", label, max)
	}
	return n, nil
}

// ParseLandingRuleMaxNode / ParseLandingRuleMaxTotal validate a cap value.
func ParseLandingRuleMaxNode(value string) (int, error) {
	return parseRuleCap(value, landingegress.MaxNodeRules, "每个节点最多节点规则数")
}

func ParseLandingRuleMaxTotal(value string) (int, error) {
	return parseRuleCap(value, LandingRuleTotalLimit, "每个用户最多节点规则数")
}

// landingRuleCapDefaults reads the global caps; missing or invalid values
// fall back to the initial defaults (5 / 10).
func landingRuleCapDefaults(db *gorm.DB) (int, int, error) {
	perNode, total := DefaultLandingRuleMaxNode, DefaultLandingRuleMaxTotal
	var rows []model.ViteConfig
	if err := db.Select("name", "value").Where("name IN ?", []string{LandingRuleMaxNodeKey, LandingRuleMaxTotalKey}).Find(&rows).Error; err != nil {
		return perNode, total, err
	}
	for _, row := range rows {
		switch row.Name {
		case LandingRuleMaxNodeKey:
			if n, err := ParseLandingRuleMaxNode(row.Value); err == nil {
				perNode = n
			}
		case LandingRuleMaxTotalKey:
			if n, err := ParseLandingRuleMaxTotal(row.Value); err == nil {
				total = n
			}
		}
	}
	return perNode, total, nil
}

// effectiveLandingRuleCaps is the single place deciding a user's node-rule caps.
func effectiveLandingRuleCaps(db *gorm.DB, u *model.User) (LandingRuleCaps, model.LandingUserSetting, error) {
	out := LandingRuleCaps{}
	perNode, total, err := landingRuleCapDefaults(db)
	if err != nil {
		return out, model.LandingUserSetting{}, err
	}
	out.GlobalPerNode, out.GlobalTotal = perNode, total
	setting, err := landingUserSetting(db, u.ID)
	if err != nil {
		return out, setting, err
	}
	out.PerNode, out.PerNodeSource = perNode, "global"
	if setting.RuleMaxNodeCustom {
		out.PerNode, out.PerNodeSource = setting.RuleMaxNode, "custom"
	}
	out.Total, out.TotalSource = total, "global"
	if setting.RuleMaxTotalCustom {
		out.Total, out.TotalSource = setting.RuleMaxTotal, "custom"
	}
	if u.RoleID == 0 {
		out.Unlimited, out.PerNodeSource, out.TotalSource = true, "admin", "admin"
	}
	return out, setting, nil
}

// EffectiveLandingRuleCaps returns a user's node-rule caps.
func (r *Repository) EffectiveLandingRuleCaps(userID int64) (LandingRuleCaps, error) {
	var u model.User
	if err := r.db.Select("id", "role_id").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return LandingRuleCaps{}, errors.New("用户不存在")
		}
		return LandingRuleCaps{}, err
	}
	caps, _, err := effectiveLandingRuleCaps(r.db, &u)
	return caps, err
}

// LandingRuleCapDefaults returns the global caps.
func (r *Repository) LandingRuleCapDefaults() (int, int, error) {
	return landingRuleCapDefaults(r.db)
}

// SetLandingRuleCapDefaults stores the global caps; lowering them keeps
// existing rules (users just cannot add more until under the cap).
func (r *Repository) SetLandingRuleCapDefaults(perNode, total int) error {
	if _, err := ParseLandingRuleMaxNode(strconv.Itoa(perNode)); err != nil {
		return err
	}
	if _, err := ParseLandingRuleMaxTotal(strconv.Itoa(total)); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if err := r.UpsertConfig(LandingRuleMaxNodeKey, strconv.Itoa(perNode), now); err != nil {
		return err
	}
	return r.UpsertConfig(LandingRuleMaxTotalKey, strconv.Itoa(total), now)
}

// SetLandingUserRuleCaps stores a user's own caps (LandingRuleCapFollowGlobal
// = follow the global default). nil keeps the current choice.
func (r *Repository) SetLandingUserRuleCaps(userID int64, perNode, total *int) error {
	if perNode == nil && total == nil {
		return nil
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Select("id").First(&user, userID).Error; err != nil {
			return errors.New("用户不存在")
		}
		setting, err := landingUserSetting(tx, userID)
		if err != nil {
			return err
		}
		values := map[string]interface{}{}
		if perNode != nil {
			switch {
			case *perNode == LandingRuleCapFollowGlobal:
				values["rule_max_node"], values["rule_max_node_custom"] = DefaultLandingRuleMaxNode, false
			case *perNode < 0 || *perNode > landingegress.MaxNodeRules:
				return fmt.Errorf("每个节点最多节点规则数须为 0–%d 的整数", landingegress.MaxNodeRules)
			default:
				values["rule_max_node"], values["rule_max_node_custom"] = *perNode, true
			}
		}
		if total != nil {
			switch {
			case *total == LandingRuleCapFollowGlobal:
				values["rule_max_total"], values["rule_max_total_custom"] = DefaultLandingRuleMaxTotal, false
			case *total < 0 || *total > LandingRuleTotalLimit:
				return fmt.Errorf("每个用户最多节点规则数须为 0–%d 的整数", LandingRuleTotalLimit)
			default:
				values["rule_max_total"], values["rule_max_total_custom"] = *total, true
			}
		}
		var count int64
		if err := tx.Model(&model.LandingUserSetting{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			// The row carries the plan 131 defaults; create it first.
			setting.UserID = userID
			if err := tx.Create(&setting).Error; err != nil {
				return err
			}
		}
		return tx.Model(&model.LandingUserSetting{}).Where("user_id = ?", userID).Updates(values).Error
	})
}

// LandingUserRuleTotal counts a user's node rules over all their nodes
// (delete-pending nodes excluded).
func landingUserRuleTotal(db *gorm.DB, userID, exceptNode int64) (int64, error) {
	var total int64
	err := db.Model(&model.LandingNode{}).Select("COALESCE(SUM(route_rule_count), 0)").Where("user_id = ? AND id <> ? AND delete_pending = ?", userID, exceptNode, false).Scan(&total).Error
	return total, err
}

// LandingUserRuleTotal is landingUserRuleTotal for views.
func (r *Repository) LandingUserRuleTotal(userID int64) (int64, error) {
	return landingUserRuleTotal(r.db, userID, 0)
}

// ErrLandingRuleCap marks a refusal by the node-rule caps.
type ErrLandingRuleCap struct{ msg string }

func (e ErrLandingRuleCap) Error() string { return e.msg }

// NodeRulesSave is one node-rule save request.
type NodeRulesSave struct {
	NodeID  int64
	Rules   []landingegress.Rule
	Default string
	// Admin: the caller is an admin (not capped, any template); otherwise
	// the caller must own the node and may use published templates only
	// (unchanged rules keep what an admin set).
	Admin  bool
	UserID int64
}

// SaveLandingNodeRules validates and stores a node's own rules in one
// transaction with the node and its owner locked (node before user, like flow
// ingestion and owner transfer), so parallel saves never exceed the caps.
// Lowering a cap keeps existing rules: a save is refused only when it adds
// rules beyond the cap.
func (r *Repository) SaveLandingNodeRules(req NodeRulesSave) (*model.LandingNode, error) {
	var saved model.LandingNode
	err := r.db.Transaction(func(tx *gorm.DB) error {
		n := &saved
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(n, req.NodeID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("落地节点不存在")
			}
			return err
		}
		if !req.Admin && n.UserID != req.UserID {
			return errors.New("无权访问该落地节点")
		}
		if n.DeletePending {
			return errors.New("节点正在删除，不能修改路由规则")
		}
		var s model.LandingServer
		if err := tx.Select("id", "chain_only").First(&s, n.LandingServerID).Error; err != nil {
			return errors.New("落地服务器不存在")
		}
		if s.ChainOnly {
			return errors.New("线路机专用节点不能设置节点规则")
		}
		var owner model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "role_id").First(&owner, n.UserID).Error; err != nil {
			return errors.New("节点所属用户不存在")
		}
		var outs []model.LandingOutbound
		if err := tx.Select("id", "user_allowed").Find(&outs).Error; err != nil {
			return err
		}
		all := map[int64]bool{}
		published := map[int64]bool{}
		for _, o := range outs {
			all[o.ID] = true
			published[o.ID] = o.UserAllowed
		}
		rules, def, err := landingegress.NormalizeNodeRules(req.Rules, req.Default, all)
		if err != nil {
			return err
		}
		oldRules, oldDefault := NodeRulesFromModel(*n)
		if !req.Admin {
			kept := map[string]string{}
			for _, rule := range oldRules {
				kept[rule.ID] = rule.Action
			}
			for i, rule := range rules {
				if id, ok := landingegress.ActionOutbound(rule.Action); ok && !published[id] && kept[rule.ID] != rule.Action {
					return fmt.Errorf("第 %d 条规则的出站未开放给用户使用", i+1)
				}
			}
			if id, ok := landingegress.ActionOutbound(def); ok && !published[id] && def != oldDefault {
				return errors.New("该默认出站未开放给用户使用")
			}
		}
		if !req.Admin && owner.RoleID != 0 {
			caps, _, err := effectiveLandingRuleCaps(tx, &owner)
			if err != nil {
				return err
			}
			count := len(rules)
			if caps.Total == 0 && count > n.RouteRuleCount {
				return ErrLandingRuleCap{"管理员未开放节点规则"}
			}
			if count > caps.PerNode && count > n.RouteRuleCount {
				return ErrLandingRuleCap{fmt.Sprintf("每个节点最多 %d 条节点规则", caps.PerNode)}
			}
			others, err := landingUserRuleTotal(tx, owner.ID, n.ID)
			if err != nil {
				return err
			}
			if others+int64(count) > int64(caps.Total) && count > n.RouteRuleCount {
				return ErrLandingRuleCap{fmt.Sprintf("节点规则总数已达上限（每个用户最多 %d 条，其他节点已用 %d 条）", caps.Total, others)}
			}
		}
		raw, _ := json.Marshal(rules)
		now := time.Now().UnixMilli()
		n.RouteRules, n.RouteRuleCount, n.RouteDefault, n.UpdatedTime = string(raw), len(rules), def, now
		return tx.Model(&model.LandingNode{}).Where("id = ?", n.ID).Updates(map[string]interface{}{"route_rules": n.RouteRules, "route_rule_count": n.RouteRuleCount, "route_default": def, "updated_time": now}).Error
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

// landingRuleTransferError refuses moving a node whose rules exceed the
// target user's caps.
func landingRuleTransferError(tx *gorm.DB, n *model.LandingNode, target *model.User) error {
	if n.RouteRuleCount == 0 || target.RoleID == 0 {
		return nil
	}
	caps, _, err := effectiveLandingRuleCaps(tx, target)
	if err != nil {
		return err
	}
	others, err := landingUserRuleTotal(tx, target.ID, n.ID)
	if err != nil {
		return err
	}
	if n.RouteRuleCount > caps.PerNode || others+int64(n.RouteRuleCount) > int64(caps.Total) {
		return fmt.Errorf("该节点有 %d 条节点规则，超过目标用户的节点规则上限（每节点 %d 条 / 总计 %d 条，已用 %d 条），请先删减规则再转移", n.RouteRuleCount, caps.PerNode, caps.Total, others)
	}
	return nil
}

// landingOutboundRefs reports what besides profiles references a template:
// the global group and node rules (node names).
func landingOutboundRefs(tx *gorm.DB, id int64) (bool, []string, error) {
	ref := "outbound:" + strconv.FormatInt(id, 10)
	g, _, err := landingGlobal(tx)
	if err != nil {
		return false, nil, err
	}
	global := g.DefaultAction == ref
	// Disabled global rules still reference it.
	for _, rule := range append(append([]landingegress.Rule{}, g.Forced...), g.Default...) {
		global = global || rule.Action == ref
	}
	var nodes []model.LandingNode
	if err := tx.Select("id", "name", "route_rules", "route_default").Where("route_rule_count > 0 OR route_default <> ''").Find(&nodes).Error; err != nil {
		return false, nil, err
	}
	names := []string{}
	for _, n := range nodes {
		rules, def := NodeRulesFromModel(n)
		used := def == ref
		for _, rule := range rules {
			used = used || rule.Action == ref
		}
		if used {
			names = append(names, n.Name)
		}
	}
	return global, names, nil
}

// LandingServersUsingLayers lists servers whose global layers or node rules
// reference any of the given templates (all servers for a global reference).
func (r *Repository) LandingServersUsingLayers(outboundID int64) ([]int64, error) {
	ref := "outbound:" + strconv.FormatInt(outboundID, 10)
	g, _, err := landingGlobal(r.db)
	if err != nil {
		return nil, err
	}
	for _, a := range landingegress.Actions(append(append([]landingegress.Rule{}, g.Forced...), g.Default...), g.DefaultAction) {
		if a == ref {
			var ids []int64
			err := r.db.Model(&model.LandingServer{}).Order("id").Pluck("id", &ids).Error
			return ids, err
		}
	}
	var nodes []model.LandingNode
	if err := r.db.Select("id", "landing_server_id", "route_rules", "route_default").Where("route_rule_count > 0 OR route_default <> ''").Find(&nodes).Error; err != nil {
		return nil, err
	}
	set := map[int64]bool{}
	out := []int64{}
	for _, n := range nodes {
		rules, def := NodeRulesFromModel(n)
		for _, a := range landingegress.Actions(rules, def) {
			if a == ref && !set[n.LandingServerID] {
				set[n.LandingServerID] = true
				out = append(out, n.LandingServerID)
			}
		}
	}
	return out, nil
}

// LandingNodeRuleCount reports how many nodes carry node rules or an own
// default (for views).
func (r *Repository) LandingNodeRuleNodes() (int64, error) {
	var n int64
	err := r.db.Model(&model.LandingNode{}).Where("route_rule_count > 0 OR route_default <> ''").Count(&n).Error
	return n, err
}
