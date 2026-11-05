package repo

import (
	"log"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// sharedCoreUnlimitedMigration (plan 223): shared landing servers' Core
// memory / CPU limits → 0 / 0 = 不限制. FLVX never sends them to 妙妙屋 (the
// sharer decides), so the old 256 MiB / 50 % were display-only. Native rows
// are untouched. Idempotent: after the first start no row matches, so the
// cost is one SELECT over the (few) landing_server rows per start.
func sharedCoreUnlimitedMigration(db *gorm.DB) error {
	var rows []model.LandingServer
	if err := db.Select("id", "name", "core_memory_mb", "core_cpu_percent").
		Where("source = ? AND (core_memory_mb <> 0 OR core_cpu_percent <> 0)", "shared").
		Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, s := range rows {
			if err := tx.Model(&model.LandingServer{}).Where("id = ? AND source = ?", s.ID, "shared").
				Updates(map[string]any{"core_memory_mb": 0, "core_cpu_percent": 0}).Error; err != nil {
				return err
			}
			log.Printf("[shared-core-unlimited] server %d %q: core_memory_mb %d→0, core_cpu_percent %d→0 (不限制)", s.ID, s.Name, s.CoreMemoryMB, s.CoreCPUPercent)
		}
		return nil
	})
}
