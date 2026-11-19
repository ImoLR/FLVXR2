package repo

import (
	"errors"
	"strings"

	"go-backend/internal/store/model"
)

// SetLandingServerRegionOverride stores the 「显示地区」 of a 落地服务器
// (plan 286; ” = automatic). A column write only: no revision bump, nothing
// is pushed to the agent or 妙妙屋. Returns false when no row changed (gone,
// or the value was already stored on drivers that report 0 affected rows).
func (r *Repository) SetLandingServerRegionOverride(id int64, code string) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("repository not initialized")
	}
	normalized, err := NormalizeTunnelRegionOverride(code)
	if err != nil {
		return false, err
	}
	res := r.db.Model(&model.LandingServer{}).Where("id = ?", id).Update("region_override", normalized)
	return res.RowsAffected > 0, res.Error
}

// NodeRegions returns node.region (upper-case, ” = unset) for the given node
// ids in one query (plan 286: native 落地服务器 region groups).
func (r *Repository) NodeRegions(ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if r == nil || r.db == nil || len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID     int64
		Region string
	}
	if err := r.db.Model(&model.Node{}).Select("id", "region").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return out, err
	}
	for _, row := range rows {
		out[row.ID] = strings.ToUpper(strings.TrimSpace(row.Region))
	}
	return out, nil
}
