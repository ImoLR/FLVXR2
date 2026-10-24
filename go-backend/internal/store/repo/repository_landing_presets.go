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
// Global: 1 = plan-140 全局·强制 seeds appended; 2 = plan-143 防滥用 seeds
// moved from 全局·强制 into 内置; 3 = plan-149 大陆 seeds switched on and
// 禁止大陆直连 (SS nodes) added. Preset: 1 = library seeded; 2 = plan-149
// 「屏蔽中国大陆」 preset scoped 禁止大陆直连 to SS nodes. None re-runs.
const (
	landingGlobalSeedVersion = 3
	landingPresetSeedVersion = 2
)

// AbuseSwitchesFromSetting decodes the global switches: SMTP / BT NULL = on,
// mail ports / Telnet-SMB NULL = off (plan 143).
func AbuseSwitchesFromSetting(s model.LandingEgressSetting) landingegress.AbuseSwitches {
	return landingegress.AbuseSwitches{
		SMTPOff:     s.BuiltinSMTP != nil && !*s.BuiltinSMTP,
		BTOff:       s.BuiltinBT != nil && !*s.BuiltinBT,
		MailOn:      s.BuiltinMailPorts != nil && *s.BuiltinMailPorts,
		TelnetSMBOn: s.BuiltinTelnetSMB != nil && *s.BuiltinTelnetSMB,
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
func (r *Repository) SaveLandingAbuseSwitches(s landingegress.AbuseSwitches) error {
	smtp, bt := s.On(landingegress.AbuseSMTP), s.On(landingegress.AbuseBT)
	mail, telnet := s.On(landingegress.AbuseMail), s.On(landingegress.AbuseTelnetSMB)
	row := model.LandingEgressSetting{ID: 1, SourceWhitelist: "[]", GlobalForced: "[]", GlobalDefault: "[]", GlobalAction: landingegress.ActionDirect,
		BuiltinSMTP: &smtp, BuiltinBT: &bt, BuiltinMailPorts: &mail, BuiltinTelnetSMB: &telnet,
		GlobalSeedVersion: landingGlobalSeedVersion, PresetSeedVersion: landingPresetSeedVersion, UpdatedTime: time.Now().UnixMilli()}
	return r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"builtin_smtp", "builtin_bt", "builtin_mail_ports", "builtin_telnet_smb"})}).Create(&row).Error
}

// LandingProfilesAbuseOff lists routing profiles whose 防滥用默认 toggle is
// off (the admin 内置 list shows them next to the global switches).
func (r *Repository) LandingProfilesAbuseOff() ([]model.LandingRouteProfile, error) {
	var out []model.LandingRouteProfile
	err := r.db.Select("id", "name").Where("abuse_defaults_off = ?", true).Order("id").Find(&out).Error
	return out, err
}

// seedLandingEgressDefaults runs at startup (not on a GET: reads never write).
// Once per database it appends the 全局·强制 starter rules (plan 149: the
// three 大陆 rules, enabled) and fills the preset library; the markers keep
// deleted seeds from coming back.
//
// Plan 143 (global marker 2, once): the 防滥用 seeds leave 全局·强制 for the
// 内置 rows. A 强制 rule blocking exactly what a seed blocks (any id / name)
// sets that row's switch to its enabled state and is removed; no such rule →
// the switch stays off. Every node keeps blocking the same traffic (the 内置
// rule only moves ahead of the 强制 rules). resync reports that compiled
// plans changed, so the caller pushes every landing server once.
func seedLandingEgressDefaults(db *gorm.DB) (resync bool, err error) {
	var forcedAdded, presetsAdded int
	var moved *landingegress.AbuseSeedMove
	err = db.Transaction(func(tx *gorm.DB) error {
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
		if row.GlobalSeedVersion < 1 {
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
		}
		if row.GlobalSeedVersion < 2 {
			var forced []landingegress.Rule
			mail, telnet := false, false
			if err := json.Unmarshal([]byte(row.GlobalForced), &forced); err != nil {
				// Unreadable list: leave it alone, the new rows stay off.
				log.Printf("[landing-seed] 防滥用 move skipped, 全局·强制 unreadable: %v", err)
			} else {
				rest, mv := landingegress.MoveAbuseSeeds(forced)
				moved = &mv
				mail, telnet = mv.On[landingegress.AbuseMail], mv.On[landingegress.AbuseTelnetSMB]
				if len(rest) != len(forced) {
					raw, _ := json.Marshal(rest)
					row.GlobalForced = string(raw)
					updates["global_forced"] = row.GlobalForced
				}
				// Enabled rules moved: the plans change (order only).
				resync = mail || telnet
			}
			row.BuiltinMailPorts, row.BuiltinTelnetSMB = &mail, &telnet
			updates["builtin_mail_ports"], updates["builtin_telnet_smb"] = mail, telnet
		}
		if row.GlobalSeedVersion < 3 {
			changed, err := enableCNSeeds(tx, &row)
			if err != nil {
				return err
			}
			if changed {
				updates["global_forced"] = row.GlobalForced
				resync = true
			}
		}
		if row.GlobalSeedVersion < landingGlobalSeedVersion {
			row.GlobalSeedVersion = landingGlobalSeedVersion
			updates["global_seed_version"] = row.GlobalSeedVersion
		}
		if row.PresetSeedVersion < 1 {
			n, err := addSeedPresets(tx)
			if err != nil {
				return err
			}
			presetsAdded = n
		} else if row.PresetSeedVersion < 2 {
			if err := updateBlockCNPreset(tx); err != nil {
				return err
			}
		}
		if row.PresetSeedVersion < landingPresetSeedVersion {
			row.PresetSeedVersion = landingPresetSeedVersion
			updates["preset_seed_version"] = row.PresetSeedVersion
		}
		if !exists {
			row.UpdatedTime = time.Now().UnixMilli()
			return tx.Create(&row).Error
		}
		return tx.Model(&model.LandingEgressSetting{}).Where("id = ?", 1).Updates(updates).Error
	})
	if err != nil {
		return false, err
	}
	if forcedAdded > 0 || presetsAdded > 0 {
		log.Printf("[landing-seed] global forced +%d (disabled), presets +%d", forcedAdded, presetsAdded)
	}
	if moved != nil && (len(moved.Found) > 0 || len(moved.Kept) > 0) {
		log.Printf("[landing-seed] 防滥用 moved to 内置: mail=%t (found %t) telnet=%t (found %t), edited seeds kept in 全局·强制: %v",
			moved.On[landingegress.AbuseMail], moved.Found[landingegress.AbuseMail], moved.On[landingegress.AbuseTelnetSMB], moved.Found[landingegress.AbuseTelnetSMB], moved.Kept)
	}
	return resync, nil
}

// enableCNSeeds (plan 149, global marker 3, once): the 大陆 seeds of
// 全局·强制 are switched on when they still carry the original matchers and
// 禁止大陆直连 (source geoip:cn, 节点协议 SS) is added after them unless a rule
// with the same signature exists. Edited seeds are left alone, deleted ones
// are not re-added, a full list gets nothing; all of it is logged. A list
// that does not validate afterwards is left unchanged.
func enableCNSeeds(tx *gorm.DB, row *model.LandingEgressSetting) (bool, error) {
	var forced []landingegress.Rule
	if err := json.Unmarshal([]byte(row.GlobalForced), &forced); err != nil {
		log.Printf("[landing-seed] 大陆 seeds skipped, 全局·强制 unreadable: %v", err)
		return false, nil
	}
	next, res := landingegress.EnableCNSeeds(forced)
	log.Printf("[landing-seed] 大陆 seeds: enabled %v, 禁止大陆直连 added=%t, edited (kept) %v, deleted (not re-added) %v, list full=%t",
		res.Enabled, res.Added, res.Edited, res.Missing, res.Full)
	if len(res.Enabled) == 0 && !res.Added {
		return false, nil
	}
	ids, err := outboundIDs(tx)
	if err != nil {
		return false, err
	}
	g := GlobalFromSetting(*row)
	check := g
	check.Forced = append([]landingegress.Rule(nil), next...)
	check.Default = append([]landingegress.Rule(nil), g.Default...)
	if err := landingegress.NormalizeGlobal(&check, ids); err != nil {
		log.Printf("[landing-seed] 大陆 seeds not applied, the global group would not validate: %v", err)
		return false, nil
	}
	raw, _ := json.Marshal(next)
	row.GlobalForced = string(raw)
	return true, nil
}

// updateBlockCNPreset (plan 149, preset marker 2, once) replaces the rules
// (and the description, when unchanged) of the 「屏蔽中国大陆」 library
// preset with the plan-149 ones, but only while the rules are exactly the
// pre-149 seed; an edited or deleted preset is left alone.
func updateBlockCNPreset(tx *gorm.DB) error {
	var rows []model.LandingEgressPreset
	if err := tx.Where("seed_key = ?", "block-cn").Limit(1).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		log.Printf("[landing-seed] 屏蔽中国大陆 preset deleted, not updated")
		return nil
	}
	p := PresetFromModel(rows[0])
	legacy := landingegress.LegacyBlockCNRules()
	same := len(p.Rules) == len(legacy)
	for i := 0; same && i < len(legacy); i++ {
		same = landingegress.RuleSignature(p.Rules[i]) == landingegress.RuleSignature(legacy[i])
	}
	if !same {
		log.Printf("[landing-seed] 屏蔽中国大陆 preset edited by the admin, kept as is")
		return nil
	}
	raw, _ := json.Marshal(presetRulesForStore(landingegress.BlockCNRules()))
	updates := map[string]any{"rules": string(raw), "updated_time": time.Now().UnixMilli()}
	if rows[0].Description == landingegress.LegacyBlockCNDescription {
		updates["description"] = landingegress.BlockCNDescription
	}
	if err := tx.Model(&model.LandingEgressPreset{}).Where("id = ?", rows[0].ID).Updates(updates).Error; err != nil {
		return err
	}
	log.Printf("[landing-seed] 屏蔽中国大陆 preset: 禁止大陆直连 now SS nodes only")
	return nil
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
