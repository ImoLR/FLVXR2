package repo

import (
	"encoding/json"
	"errors"
	"log"
	"time"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 140: global switches of the built-in anti-abuse rules, the one-time
// seeds (disabled 全局·强制 starter rules, preset library) and the preset
// library CRUD.

// Seed marker versions stored on landing_egress_setting row 1.
const (
	landingGlobalSeedVersion = 1
	landingPresetSeedVersion = 1
)

// AbuseSwitchesFromSetting decodes the global switches (NULL = on).
func AbuseSwitchesFromSetting(s model.LandingEgressSetting) landingegress.AbuseSwitches {
	return landingegress.AbuseSwitches{
		SMTPOff: s.BuiltinSMTP != nil && !*s.BuiltinSMTP,
		BTOff:   s.BuiltinBT != nil && !*s.BuiltinBT,
	}
}

// landingSettings reads row 1 once: the global group, the switches and when
// the group was last saved. A missing row is the pre-134 default.
func landingSettings(db *gorm.DB) (landingegress.Global, landingegress.AbuseSwitches, int64, error) {
	var rows []model.LandingEgressSetting
	if err := db.Where("id = ?", 1).Limit(1).Find(&rows).Error; err != nil {
		return landingegress.Global{DefaultAction: landingegress.ActionDirect}, landingegress.AbuseSwitches{}, 0, err
	}
	if len(rows) == 0 {
		return landingegress.Global{Forced: []landingegress.Rule{}, Default: []landingegress.Rule{}, DefaultAction: landingegress.ActionDirect}, landingegress.AbuseSwitches{}, 0, nil
	}
	return GlobalFromSetting(rows[0]), AbuseSwitchesFromSetting(rows[0]), rows[0].GlobalUpdatedTime, nil
}

// GetLandingAbuseSwitches returns the global switches of the default rules.
func (r *Repository) GetLandingAbuseSwitches() (landingegress.AbuseSwitches, error) {
	_, s, _, err := landingSettings(r.db)
	return s, err
}

// SaveLandingAbuseSwitches stores the global switches (only these columns).
func (r *Repository) SaveLandingAbuseSwitches(smtp, bt bool) error {
	row := model.LandingEgressSetting{ID: 1, SourceWhitelist: "[]", GlobalForced: "[]", GlobalDefault: "[]", GlobalAction: landingegress.ActionDirect, BuiltinSMTP: &smtp, BuiltinBT: &bt,
		GlobalSeedVersion: landingGlobalSeedVersion, PresetSeedVersion: landingPresetSeedVersion, UpdatedTime: time.Now().UnixMilli()}
	return r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"builtin_smtp", "builtin_bt"})}).Create(&row).Error
}

// LandingProfilesAbuseOff lists routing profiles whose 防滥用默认 toggle is
// off (the admin 内置 list shows them next to the global switches).
func (r *Repository) LandingProfilesAbuseOff() ([]model.LandingRouteProfile, error) {
	var out []model.LandingRouteProfile
	err := r.db.Select("id", "name").Where("abuse_defaults_off = ?", true).Order("id").Find(&out).Error
	return out, err
}

// seedLandingEgressDefaults runs at startup (not on a GET: reads never write).
// Once per database it appends the disabled 全局·强制 starter rules and fills
// the preset library; the markers keep deleted seeds from coming back. The
// seeded rules are disabled, so no compiled plan changes.
func seedLandingEgressDefaults(db *gorm.DB) error {
	var forcedAdded, presetsAdded int
	err := db.Transaction(func(tx *gorm.DB) error {
		var rows []model.LandingEgressSetting
		if err := tx.Where("id = ?", 1).Limit(1).Find(&rows).Error; err != nil {
			return err
		}
		exists := len(rows) > 0
		row := model.LandingEgressSetting{ID: 1, SourceWhitelist: "[]", GlobalForced: "[]", GlobalDefault: "[]", GlobalAction: landingegress.ActionDirect}
		if exists {
			row = rows[0]
		}
		if row.GlobalSeedVersion >= landingGlobalSeedVersion && row.PresetSeedVersion >= landingPresetSeedVersion {
			return nil
		}
		updates := map[string]any{}
		if row.GlobalSeedVersion < landingGlobalSeedVersion {
			g := GlobalFromSetting(row)
			candidate := g
			candidate.Forced = landingegress.AppendSeedRules(g.Forced, landingegress.ForcedSeedRules())
			candidate.Default = append([]landingegress.Rule(nil), g.Default...)
			ids, err := outboundIDs(tx)
			if err != nil {
				return err
			}
			check := candidate
			check.Forced = append([]landingegress.Rule(nil), candidate.Forced...)
			check.Default = append([]landingegress.Rule(nil), candidate.Default...)
			if err := landingegress.NormalizeGlobal(&check, ids); err != nil {
				// Never break an existing (valid or not) global group for a seed.
				log.Printf("[landing-seed] global forced seeds skipped: %v", err)
			} else {
				forcedAdded = len(candidate.Forced) - len(g.Forced)
				raw, _ := json.Marshal(candidate.Forced)
				row.GlobalForced = string(raw)
				updates["global_forced"] = row.GlobalForced
			}
			row.GlobalSeedVersion = landingGlobalSeedVersion
			updates["global_seed_version"] = row.GlobalSeedVersion
		}
		if row.PresetSeedVersion < landingPresetSeedVersion {
			n, err := addSeedPresets(tx)
			if err != nil {
				return err
			}
			presetsAdded = n
			row.PresetSeedVersion = landingPresetSeedVersion
			updates["preset_seed_version"] = row.PresetSeedVersion
		}
		if !exists {
			row.UpdatedTime = time.Now().UnixMilli()
			return tx.Create(&row).Error
		}
		return tx.Model(&model.LandingEgressSetting{}).Where("id = ?", 1).Updates(updates).Error
	})
	if err == nil && (forcedAdded > 0 || presetsAdded > 0) {
		log.Printf("[landing-seed] global forced +%d (disabled), presets +%d", forcedAdded, presetsAdded)
	}
	return err
}

// addSeedPresets inserts every seed preset whose seed key is not in the
// library yet, after the existing entries, in seed order.
func addSeedPresets(tx *gorm.DB) (int, error) {
	var existing []model.LandingEgressPreset
	if err := tx.Select("id", "seed_key", "sort_order").Find(&existing).Error; err != nil {
		return 0, err
	}
	have := map[string]bool{}
	next := 0
	for _, p := range existing {
		if p.SeedKey != "" {
			have[p.SeedKey] = true
		}
		if p.SortOrder >= next {
			next = p.SortOrder + 1
		}
	}
	now := time.Now().UnixMilli()
	added := 0
	for _, s := range landingegress.SeedPresets() {
		if have[s.SeedKey] {
			continue
		}
		if len(existing)+added >= landingegress.MaxPresets {
			break
		}
		raw, _ := json.Marshal(presetRulesForStore(s.Rules))
		m := model.LandingEgressPreset{Name: s.Name, Description: s.Description, Rules: string(raw), AskOutbound: s.AskOutbound, InsertTop: s.InsertTop, SortOrder: next, SeedKey: s.SeedKey, CreatedTime: now, UpdatedTime: now}
		if err := tx.Create(&m).Error; err != nil {
			return added, err
		}
		next++
		added++
	}
	return added, nil
}

// presetRulesForStore drops ids and node scopes; lists are never null.
func presetRulesForStore(rules []landingegress.Rule) []landingegress.Rule {
	out := make([]landingegress.Rule, 0, len(rules))
	for _, r := range rules {
		r.ID, r.LandingNodeIDs = "", nil
		out = append(out, landingegress.FilledRule(r))
	}
	return out
}

// PresetFromModel decodes a library row.
func PresetFromModel(m model.LandingEgressPreset) landingegress.Preset {
	p := landingegress.Preset{ID: m.ID, Name: m.Name, Description: m.Description, AskOutbound: m.AskOutbound, InsertTop: m.InsertTop, SortOrder: m.SortOrder, SeedKey: m.SeedKey, Rules: []landingegress.Rule{}}
	_ = json.Unmarshal([]byte(m.Rules), &p.Rules)
	p.Rules = presetRulesForStore(p.Rules)
	return p
}

// ListLandingEgressPresets returns the library in display order.
func (r *Repository) ListLandingEgressPresets() ([]landingegress.Preset, error) {
	var rows []model.LandingEgressPreset
	if err := r.db.Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]landingegress.Preset, 0, len(rows))
	for _, m := range rows {
		out = append(out, PresetFromModel(m))
	}
	return out, nil
}

// ErrPresetNotFound is returned for an unknown preset id.
var ErrPresetNotFound = errors.New("预设不存在或已删除")

// SaveLandingEgressPreset validates and creates (ID 0, appended at the end)
// or updates a preset; sort order and seed key never change on update.
func (r *Repository) SaveLandingEgressPreset(p *landingegress.Preset) (*landingegress.Preset, error) {
	var saved model.LandingEgressPreset
	err := r.db.Transaction(func(tx *gorm.DB) error {
		ids, err := outboundIDs(tx)
		if err != nil {
			return err
		}
		if err := landingegress.NormalizePreset(p, ids); err != nil {
			return err
		}
		raw, _ := json.Marshal(presetRulesForStore(p.Rules))
		now := time.Now().UnixMilli()
		if p.ID > 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&saved, p.ID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrPresetNotFound
				}
				return err
			}
			saved.Name, saved.Description, saved.Rules, saved.AskOutbound, saved.InsertTop, saved.UpdatedTime = p.Name, p.Description, string(raw), p.AskOutbound, p.InsertTop, now
			return tx.Save(&saved).Error
		}
		var count int64
		if err := tx.Model(&model.LandingEgressPreset{}).Count(&count).Error; err != nil {
			return err
		}
		if count >= landingegress.MaxPresets {
			return errors.New("预设最多 64 个，请先删除不用的预设")
		}
		var maxOrder struct{ M *int }
		if err := tx.Model(&model.LandingEgressPreset{}).Select("MAX(sort_order) AS m").Scan(&maxOrder).Error; err != nil {
			return err
		}
		next := 0
		if maxOrder.M != nil {
			next = *maxOrder.M + 1
		}
		saved = model.LandingEgressPreset{Name: p.Name, Description: p.Description, Rules: string(raw), AskOutbound: p.AskOutbound, InsertTop: p.InsertTop, SortOrder: next, CreatedTime: now, UpdatedTime: now}
		return tx.Create(&saved).Error
	})
	if err != nil {
		return nil, err
	}
	out := PresetFromModel(saved)
	return &out, nil
}

// DeleteLandingEgressPreset removes a preset (rules already inserted from it
// stay where they are).
func (r *Repository) DeleteLandingEgressPreset(id int64) error {
	res := r.db.Delete(&model.LandingEgressPreset{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrPresetNotFound
	}
	return nil
}

// ReorderLandingEgressPresets stores a new order; ids must list every preset
// exactly once (a stale list from another tab is refused).
func (r *Repository) ReorderLandingEgressPresets(ids []int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var rows []model.LandingEgressPreset
		if err := tx.Select("id", "sort_order").Find(&rows).Error; err != nil {
			return err
		}
		current := map[int64]int{}
		for _, m := range rows {
			current[m.ID] = m.SortOrder
		}
		seen := map[int64]bool{}
		for _, id := range ids {
			if _, ok := current[id]; !ok || seen[id] {
				return errors.New("预设列表已变化，请刷新后再调整顺序")
			}
			seen[id] = true
		}
		if len(seen) != len(current) {
			return errors.New("预设列表已变化，请刷新后再调整顺序")
		}
		for i, id := range ids {
			if current[id] == i {
				continue
			}
			if err := tx.Model(&model.LandingEgressPreset{}).Where("id = ?", id).Update("sort_order", i).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// RestoreLandingEgressPresets re-adds the seed presets that are missing (by
// seed key); existing ones, edited or not, are left alone.
func (r *Repository) RestoreLandingEgressPresets() (int, error) {
	added := 0
	err := r.db.Transaction(func(tx *gorm.DB) error {
		n, err := addSeedPresets(tx)
		added = n
		return err
	})
	return added, err
}
