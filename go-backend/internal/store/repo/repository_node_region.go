package repo

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"go-backend/internal/store/model"
)

const nodeRegionBackfillKey = "node_region_backfill_v1"

type NodeRegion struct {
	Region string
	City   string
}

func NormalizeNodeRegion(region, city string) (NodeRegion, error) {
	region = strings.ToUpper(strings.TrimSpace(region))
	city = strings.TrimSpace(city)
	if region != "" && (len(region) != 2 || region[0] < 'A' || region[0] > 'Z' || region[1] < 'A' || region[1] > 'Z') {
		return NodeRegion{}, errors.New("地区代码须为两位英文字母或留空")
	}
	if utf8.RuneCountInString(city) > 50 {
		return NodeRegion{}, errors.New("城市/备注不能超过50个字符")
	}
	return NodeRegion{Region: region, City: city}, nil
}

// BackfillNodeRegions runs once after migration. Lookups never hold a database
// transaction; the conditional write also protects an admin edit during lookup.
func (r *Repository) BackfillNodeRegions(ctx context.Context, detect func(string, string, string) string) error {
	cfg, err := r.GetConfigByName(nodeRegionBackfillKey)
	if err != nil || (cfg != nil && cfg.Value == "done") {
		return err
	}
	var nodes []model.Node
	if err := r.db.WithContext(ctx).Where("region = ?", "").Order("id ASC").Find(&nodes).Error; err != nil {
		return err
	}
	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		region := detect(node.ServerIPV4.String, node.ServerIP, node.ServerIPV6.String)
		result := r.db.WithContext(ctx).Model(&model.Node{}).Where("id = ? AND region = ?", node.ID, "").Update("region", region)
		if result.Error != nil {
			return result.Error
		}
		log.Printf("[node-region] node=%d name=%q ipv4=%q ip=%q ipv6=%q region=%q stored=%t", node.ID, node.Name,
			node.ServerIPV4.String, node.ServerIP, node.ServerIPV6.String, region, result.RowsAffected > 0)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.UpsertConfig(nodeRegionBackfillKey, "done", time.Now().UnixMilli())
}
