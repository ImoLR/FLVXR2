package repo

import (
	"fmt"
	"strings"

	"go-backend/internal/store/model"
)

func NormalizePanelIPFamily(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "", "auto":
		return "auto", nil
	case "prefer_ipv4", "ipv4_only", "ipv6_only":
		return strings.TrimSpace(value), nil
	default:
		return "", fmt.Errorf("连接面板必须为 auto、prefer_ipv4、ipv4_only 或 ipv6_only")
	}
}

// An unset panel preference accepts the installer's initial choice. Once saved
// in the panel (including explicit auto), the panel is authoritative on reconnect.
func (r *Repository) ResolvePanelIPFamily(nodeID int64, reported string) (string, error) {
	mode, err := NormalizePanelIPFamily(reported)
	if err != nil {
		return "", err
	}
	if mode != "auto" {
		if err := r.db.Model(&model.Node{}).Where("id = ? AND panel_ip_family = ''", nodeID).Update("panel_ip_family", mode).Error; err != nil {
			return "", err
		}
	}
	node, err := r.GetNodeByID(nodeID)
	if err != nil {
		return "", err
	}
	if node == nil {
		return "", fmt.Errorf("node not found")
	}
	return NormalizePanelIPFamily(node.PanelIPFamily)
}
