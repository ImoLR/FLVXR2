package repo

import (
	"encoding/json"
	"go-backend/internal/store/model"
)

// Repeated reports and reconnects preserve the last changed observation, as RA does.
func (r *Repository) UpdateNodeLeiKwan(nodeID int64, status, forwards string, now int64) error {
	if status != "absent" && status != "present" && status != "running" {
		return nil
	}
	return r.db.Model(&model.Node{}).
		Where("id = ? AND (leikwan_status <> ? OR leikwan_forwards <> ?)", nodeID, status, forwards).
		Updates(map[string]interface{}{"leikwan_status": status, "leikwan_forwards": forwards, "leikwan_checked_at": now}).Error
}
func leiKwanForwards(raw string) []model.LeiKwanDetectedForward {
	forwards := []model.LeiKwanDetectedForward{}
	_ = json.Unmarshal([]byte(raw), &forwards)
	return forwards
}
