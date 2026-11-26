package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 316 「屏蔽本机厂商官网」: the admin's vendor list, its global switch and
// each landing server's vendor.

// VendorsFromSetting decodes the stored list; "" (never saved) or an
// unreadable value is the built-in seed list.
func VendorsFromSetting(s model.LandingEgressSetting) (list []landingegress.Vendor, custom bool) {
	if strings.TrimSpace(s.Vendors) == "" {
		return landingegress.SeedVendors(), false
	}
	if err := json.Unmarshal([]byte(s.Vendors), &list); err != nil {
		return landingegress.SeedVendors(), false
	}
	if list == nil {
		list = []landingegress.Vendor{}
	}
	return list, true
}

// VendorBlockOnFromSetting: NULL = on.
func VendorBlockOnFromSetting(s model.LandingEgressSetting) bool {
	return s.BuiltinVendor == nil || *s.BuiltinVendor
}

// LandingVendorSettings is the vendor part of settings row 1.
type LandingVendorSettings struct {
	Vendors []landingegress.Vendor
	On      bool
	// Custom: the admin saved a list (false = built-in seed list).
	Custom bool
}

func landingVendorSettings(db *gorm.DB) (LandingVendorSettings, error) {
	var rows []model.LandingEgressSetting
	if err := db.Select("id", "builtin_vendor", "vendors").Where("id = ?", 1).Limit(1).Find(&rows).Error; err != nil {
		return LandingVendorSettings{Vendors: landingegress.SeedVendors(), On: true}, err
	}
	if len(rows) == 0 {
		return LandingVendorSettings{Vendors: landingegress.SeedVendors(), On: true}, nil
	}
	list, custom := VendorsFromSetting(rows[0])
	return LandingVendorSettings{Vendors: list, On: VendorBlockOnFromSetting(rows[0]), Custom: custom}, nil
}

// GetLandingVendorSettings returns the vendor list and the global switch.
func (r *Repository) GetLandingVendorSettings() (LandingVendorSettings, error) {
	return landingVendorSettings(r.db)
}

// upsertLandingSetting writes only the given columns of row 1 (the row is
// created with the defaults when missing).
func (r *Repository) upsertLandingSetting(row model.LandingEgressSetting, cols []string) error {
	row.ID = 1
	if row.SourceWhitelist == "" {
		row.SourceWhitelist = "[]"
	}
	if row.GlobalForced == "" {
		row.GlobalForced = "[]"
	}
	if row.GlobalDefault == "" {
		row.GlobalDefault = "[]"
	}
	if row.GlobalAction == "" {
		row.GlobalAction = landingegress.ActionDirect
	}
	row.GlobalSeedVersion, row.PresetSeedVersion = landingGlobalSeedVersion, landingPresetSeedVersion
	row.UpdatedTime = time.Now().UnixMilli()
	return r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns(cols)}).Create(&row).Error
}

// SaveLandingVendorSwitch stores the global switch.
func (r *Repository) SaveLandingVendorSwitch(on bool) error {
	return r.upsertLandingSetting(model.LandingEgressSetting{BuiltinVendor: &on}, []string{"builtin_vendor"})
}

// SaveLandingVendors validates and stores the list. A vendor still assigned
// to a landing server cannot be removed.
func (r *Repository) SaveLandingVendors(list []landingegress.Vendor) ([]landingegress.Vendor, error) {
	norm, err := landingegress.NormalizeVendors(list)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for _, v := range norm {
		keys[v.Key] = true
	}
	var used []model.LandingServer
	if err := r.db.Select("id", "name", "vendor").Where("vendor <> ''").Find(&used).Error; err != nil {
		return nil, err
	}
	for _, s := range used {
		if !keys[s.Vendor] {
			return nil, fmt.Errorf("落地服务器「%s」还在使用被删除的厂商，请先在它的「设置」里改掉", s.Name)
		}
	}
	raw, _ := json.Marshal(norm)
	if err := r.upsertLandingSetting(model.LandingEgressSetting{Vendors: string(raw)}, []string{"vendors"}); err != nil {
		return nil, err
	}
	return norm, nil
}

// SetLandingServerVendor assigns a vendor ("" = none); like a profile change
// (SetLandingServerEgress) the caller re-syncs the server, which pushes the
// new plan to the agent / Helper.
func (r *Repository) SetLandingServerVendor(id int64, key string) error {
	if key != "" {
		vs, err := landingVendorSettings(r.db)
		if err != nil {
			return err
		}
		if _, ok := landingegress.FindVendor(vs.Vendors, key); !ok {
			return errors.New("厂商不存在")
		}
	}
	return r.db.Model(&model.LandingServer{}).Where("id = ?", id).Updates(map[string]any{"vendor": key, "updated_time": time.Now().UnixMilli()}).Error
}

// LandingServerVendorRef is one server in the 内置 admin row.
type LandingServerVendorRef struct {
	ID     int64
	NodeID int64
	Name   string
	Source string
	Vendor string
	Nodes  int
}

// LandingServerVendorRefs lists active servers with their vendor and live
// (not deleted) node count.
func (r *Repository) LandingServerVendorRefs() ([]LandingServerVendorRef, error) {
	var servers []model.LandingServer
	if err := r.db.Select("id", "node_id", "name", "source", "vendor", "chain_only").Where("status = ?", "active").Order("id").Find(&servers).Error; err != nil {
		return nil, err
	}
	type count struct {
		LandingServerID int64
		N               int
	}
	var counts []count
	if err := r.db.Model(&model.LandingNode{}).Select("landing_server_id, count(*) as n").Where("delete_pending = ?", false).Group("landing_server_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	by := map[int64]int{}
	for _, c := range counts {
		by[c.LandingServerID] = c.N
	}
	out := make([]LandingServerVendorRef, 0, len(servers))
	for _, s := range servers {
		out = append(out, LandingServerVendorRef{ID: s.ID, NodeID: s.NodeID, Name: s.Name, Source: s.Source, Vendor: s.Vendor, Nodes: by[s.ID]})
	}
	return out, nil
}
