package repo

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

type tunnelEntryNode struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Region     string `json:"region"`
	RegionCity string `json:"regionCity"`
}

type tunnelEntryGroup struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Region string `json:"region"`
}

type tunnelRegionRow struct {
	TunnelID   int64
	TunnelType int
	ChainType  string
	NodeID     int64
	Name       string
	Region     string
	RegionCity string
}

type tunnelRegionInfo struct {
	EntryNodes  []tunnelEntryNode
	ExitRegions []string
}

// One query for the entire list, including tunnels with several entries/exits.
func (r *Repository) loadTunnelRegions(ids []int64) (map[int64]tunnelRegionInfo, error) {
	if len(ids) == 0 {
		return map[int64]tunnelRegionInfo{}, nil
	}
	var rows []tunnelRegionRow
	err := r.db.Table("chain_tunnel AS c").
		Select("c.tunnel_id, t.type AS tunnel_type, c.chain_type, c.node_id, n.name, n.region, n.region_city").
		Joins("JOIN tunnel AS t ON t.id = c.tunnel_id").
		Joins("LEFT JOIN node AS n ON n.id = c.node_id").
		Where("c.tunnel_id IN ? AND c.chain_type IN ?", ids, []string{"1", "3"}).
		Order("c.tunnel_id ASC, c.chain_type ASC, c.inx ASC, c.id ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return deriveTunnelRegions(ids, rows), nil
}

func deriveTunnelRegions(ids []int64, rows []tunnelRegionRow) map[int64]tunnelRegionInfo {
	result := make(map[int64]tunnelRegionInfo, len(ids))
	for _, id := range ids {
		result[id] = tunnelRegionInfo{EntryNodes: []tunnelEntryNode{}, ExitRegions: []string{}}
	}
	for _, row := range rows {
		info := result[row.TunnelID]
		if row.ChainType == "1" {
			found := false
			for _, entry := range info.EntryNodes {
				if entry.ID == row.NodeID {
					found = true
					break
				}
			}
			if !found {
				info.EntryNodes = append(info.EntryNodes, tunnelEntryNode{ID: row.NodeID, Name: row.Name, Region: row.Region, RegionCity: row.RegionCity})
			}
		}
		if (row.TunnelType == 1 && row.ChainType == "1") || (row.TunnelType == 2 && row.ChainType == "3") {
			found := false
			for _, region := range info.ExitRegions {
				if region == row.Region {
					found = true
					break
				}
			}
			if !found {
				info.ExitRegions = append(info.ExitRegions, row.Region)
			}
		}
		result[row.TunnelID] = info
	}
	for id, info := range result {
		if len(info.ExitRegions) == 0 {
			info.ExitRegions = []string{""}
		}
		sort.SliceStable(info.ExitRegions, func(i, j int) bool {
			a, b := info.ExitRegions[i], info.ExitRegions[j]
			if regionOrder(a) != regionOrder(b) {
				return regionOrder(a) < regionOrder(b)
			}
			return regionName(a) < regionName(b)
		})
		result[id] = info
	}
	return result
}

func regionOrder(code string) int {
	for i, c := range []string{"HK", "TW", "JP", "SG", "KR", "US"} {
		if c == code {
			return i
		}
	}
	if code == "" {
		return 7
	}
	return 6
}

func regionName(code string) string {
	common := map[string]string{
		"HK": "香港", "TW": "台湾", "JP": "日本", "SG": "新加坡", "KR": "韩国", "US": "美国", "CN": "中国大陆", "MO": "澳门",
		"GB": "英国", "DE": "德国", "NL": "荷兰", "FR": "法国", "CA": "加拿大", "AU": "澳大利亚", "RU": "俄罗斯",
		"MY": "马来西亚", "TH": "泰国", "VN": "越南", "PH": "菲律宾", "ID": "印度尼西亚", "IN": "印度", "TR": "土耳其", "AE": "阿联酋",
	}
	if name := common[code]; name != "" {
		return name
	}
	region, err := language.ParseRegion(code)
	if err != nil || code == "" {
		return code
	}
	return display.Chinese.Regions().Name(region)
}

func (info tunnelRegionInfo) entryGroups(admin bool) []tunnelEntryGroup {
	groups := make([]tunnelEntryGroup, 0, len(info.EntryNodes))
	seen := make(map[string]bool)
	for _, node := range info.EntryNodes {
		label, key := node.Name, "node-"+strconv.FormatInt(node.ID, 10)
		if !admin {
			label = strings.TrimSpace(node.RegionCity)
			if label == "" {
				label = regionName(node.Region)
			}
			if label == "" {
				label = "默认入口"
			}
			// Only public display fields feed the key; node IDs and names never do.
			digest := sha256.Sum256([]byte(node.Region + "\x00" + label))
			key = fmt.Sprintf("entry-%x", digest[:12])
		}
		if !seen[key] {
			groups = append(groups, tunnelEntryGroup{Key: key, Label: label, Region: node.Region})
			seen[key] = true
		}
	}
	return groups
}
