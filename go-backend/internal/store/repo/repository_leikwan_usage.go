package repo

// Plan 273: 利群 vendor usage / device snapshots (one row per account + kind)
// and the read-only IP → node lookup of the 利群在线设备 list.

import (
	"errors"
	"strings"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

func (r *Repository) ListLeiKwanUsageSnapshots() ([]model.LeiKwanUsageSnapshot, error) {
	out := []model.LeiKwanUsageSnapshot{}
	err := r.db.Order("account_id, kind").Limit(1024).Find(&out).Error
	return out, err
}

// SaveLeiKwanUsageSnapshot upserts the row of (account_id, kind).
func (r *Repository) SaveLeiKwanUsageSnapshot(s *model.LeiKwanUsageSnapshot) error {
	if s == nil || s.AccountID <= 0 || s.Kind == "" {
		return errors.New("invalid snapshot")
	}
	if s.ID == 0 {
		var old model.LeiKwanUsageSnapshot
		err := r.db.Select("id").Where("account_id = ? AND kind = ?", s.AccountID, s.Kind).First(&old).Error
		if err == nil {
			s.ID = old.ID
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	return r.db.Save(s).Error
}

// LeiKwanNodeRef is a node id + name.
type LeiKwanNodeRef struct {
	ID   int64
	Name string
}

// LeiKwanNodesByIPs maps each given IP to the node that has it as server_ip /
// server_ip_v4 / server_ip_v6 (lowest id wins). One query, ≤ 64 IPs.
func (r *Repository) LeiKwanNodesByIPs(ips []string) (map[string]LeiKwanNodeRef, error) {
	out := map[string]LeiKwanNodeRef{}
	clean := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip = strings.TrimSpace(ip); ip != "" && len(clean) < 64 {
			clean = append(clean, ip)
		}
	}
	if len(clean) == 0 {
		return out, nil
	}
	var rows []struct {
		ID         int64
		Name       string
		ServerIP   string
		ServerIPV4 *string `gorm:"column:server_ip_v4"`
		ServerIPV6 *string `gorm:"column:server_ip_v6"`
	}
	err := r.db.Table("node").Select("id, name, server_ip, server_ip_v4, server_ip_v6").
		Where("server_ip IN ? OR server_ip_v4 IN ? OR server_ip_v6 IN ?", clean, clean, clean).Order("id").Limit(256).Scan(&rows).Error
	if err != nil {
		return out, err
	}
	want := map[string]bool{}
	for _, ip := range clean {
		want[strings.ToLower(ip)] = true
	}
	for _, n := range rows {
		for _, ip := range []*string{&n.ServerIP, n.ServerIPV4, n.ServerIPV6} {
			if ip == nil {
				continue
			}
			k := strings.ToLower(strings.TrimSpace(*ip))
			if _, seen := out[k]; want[k] && !seen {
				out[k] = LeiKwanNodeRef{ID: n.ID, Name: n.Name}
			}
		}
	}
	return out, nil
}
