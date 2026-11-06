package repo

import (
	"log"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// ChainOnlyCoreCPUPercent (plan 225, P2-7): the Core CPU cap of a chain-only
// exit (「我自己的节点」 host, never public). The 50 % default capped a chain at
// about 12 MB/s on Bestvm US; 1000 % is no practical cap on these machines.
// Native public landing servers keep their caps (user decision 11-06).
const ChainOnlyCoreCPUPercent = 1000

// chainOnlyCoreCPUMigration raises the CPU cap of chain-only servers still on
// the old 50 % default (an admin-edited value is kept) and bumps their
// revision so the agent applies it (systemd set-property, no Core restart).
// Idempotent: afterwards no row matches.
func chainOnlyCoreCPUMigration(db *gorm.DB) error {
	var rows []model.LandingServer
	if err := db.Select("id", "name", "core_cpu_percent").
		Where("source = ? AND chain_only = ? AND core_cpu_percent = ?", "native", true, 50).
		Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, s := range rows {
			if err := tx.Model(&model.LandingServer{}).Where("id = ? AND chain_only = ? AND core_cpu_percent = ?", s.ID, true, 50).
				Updates(map[string]any{"core_cpu_percent": ChainOnlyCoreCPUPercent, "revision": gorm.Expr("revision + 1")}).Error; err != nil {
				return err
			}
			log.Printf("[chain-core-cpu] chain-only server %d %q: core_cpu_percent 50→%d", s.ID, s.Name, ChainOnlyCoreCPUPercent)
		}
		return nil
	})
}
