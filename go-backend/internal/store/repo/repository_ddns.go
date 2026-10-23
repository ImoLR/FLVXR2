package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"go-backend/internal/store/model"
)

// Node DDNS (plan 142) data access.

// MaxNodeDDNSRecords caps the records of one node.
const MaxNodeDDNSRecords = 8

func (r *Repository) ListDNSProviders() ([]model.DNSProvider, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []model.DNSProvider
	err := r.db.Order("id ASC").Find(&out).Error
	return out, err
}

func (r *Repository) GetDNSProvider(id int64) (*model.DNSProvider, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var p model.DNSProvider
	err := r.db.Where("id = ?", id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *Repository) CreateDNSProvider(p *model.DNSProvider) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	now := time.Now().UnixMilli()
	p.ID, p.CreatedTime, p.UpdatedTime = 0, now, now
	if p.Zones == "" {
		p.Zones = "[]"
	}
	return r.db.Create(p).Error
}

// UpdateDNSProvider renames a provider and, when credentials is not empty,
// replaces them (the token check state is then reset).
func (r *Repository) UpdateDNSProvider(id int64, name, credentials string) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	updates := map[string]interface{}{"name": name, "updated_time": time.Now().UnixMilli()}
	if credentials != "" {
		updates["credentials"] = credentials
		updates["token_status"] = ""
		updates["token_expires_on"] = int64(0)
		updates["token_checked_at"] = int64(0)
		updates["token_error"] = ""
		updates["zones"] = "[]"
	}
	res := r.db.Model(&model.DNSProvider{}).Where("id = ?", id).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("DNS 服务商不存在")
	}
	return nil
}

// SetDNSProviderTokenCheck stores the result of a token verification.
func (r *Repository) SetDNSProviderTokenCheck(id int64, status string, expiresOn, checkedAt int64, errText string, zones []string) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	updates := map[string]interface{}{
		"token_status": status, "token_expires_on": expiresOn, "token_checked_at": checkedAt,
		"token_error": truncateRunes(errText, 250),
	}
	if zones != nil {
		raw, _ := json.Marshal(zones)
		updates["zones"] = string(raw)
	}
	return r.db.Model(&model.DNSProvider{}).Where("id = ?", id).Updates(updates).Error
}

// DNSProviderUsage counts the nodes using each provider.
func (r *Repository) DNSProviderUsage() (map[int64]int64, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var rows []struct {
		DDNSProviderID int64 `gorm:"column:ddns_provider_id"`
		N              int64 `gorm:"column:n"`
	}
	if err := r.db.Model(&model.Node{}).Select("ddns_provider_id, COUNT(*) AS n").
		Where("ddns_provider_id > 0").Group("ddns_provider_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, row := range rows {
		out[row.DDNSProviderID] = row.N
	}
	return out, nil
}

func (r *Repository) DeleteDNSProvider(id int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&model.Node{}).Where("ddns_provider_id = ?", id).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("还有 %d 个节点使用该 DNS 服务商，请先在节点里更换或关闭 DDNS", n)
		}
		return tx.Where("id = ?", id).Delete(&model.DNSProvider{}).Error
	})
}

func (r *Repository) GetNodeDDNSRecords(nodeID int64) ([]model.NodeDDNSRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []model.NodeDDNSRecord
	err := r.db.Where("node_id = ?", nodeID).Order("inx ASC, id ASC").Find(&out).Error
	return out, err
}

// ListAllDDNSRecords returns every record (few rows; node list + timer).
func (r *Repository) ListAllDDNSRecords() ([]model.NodeDDNSRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []model.NodeDDNSRecord
	err := r.db.Order("node_id ASC, inx ASC, id ASC").Find(&out).Error
	return out, err
}

// DDNSNodeState is the DDNS part of a node row.
type DDNSNodeState struct {
	ID             int64 `gorm:"column:id"`
	DDNSEnabled    bool  `gorm:"column:ddns_enabled"`
	DDNSProviderID int64 `gorm:"column:ddns_provider_id"`
}

// ListDDNSNodes returns the DDNS columns of every node with DDNS on.
func (r *Repository) ListDDNSNodes() ([]DDNSNodeState, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []DDNSNodeState
	err := r.db.Model(&model.Node{}).Select("id, ddns_enabled, ddns_provider_id").
		Where("ddns_enabled = ?", true).Order("id ASC").Scan(&out).Error
	return out, err
}

// FindDDNSRecordOwner returns the node owning domain+type, 0 when free.
func (r *Repository) FindDDNSRecordOwner(domain, recordType string) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}
	var rec model.NodeDDNSRecord
	err := r.db.Where("domain = ? AND record_type = ?", domain, recordType).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return rec.NodeID, err
}

// SaveNodeDDNSConfig stores the DDNS switch, provider and records of a node.
// Records are matched by domain+type: matches keep their sync state (a
// changed source or role re-queues them), others are removed or inserted.
// Removed records leave their DNS record alone.
func (r *Repository) SaveNodeDDNSConfig(nodeID int64, enabled bool, providerID int64, records []model.NodeDDNSRecord) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	now := time.Now().UnixMilli()
	return r.db.Transaction(func(tx *gorm.DB) error {
		var node model.Node
		if err := tx.Select("id", "ddns_provider_id").Where("id = ?", nodeID).First(&node).Error; err != nil {
			return err
		}
		// Another DNS account: zone / record ids of the old one mean nothing.
		providerChanged := node.DDNSProviderID != providerID && node.DDNSProviderID != 0
		if err := tx.Model(&model.Node{}).Where("id = ?", nodeID).
			Updates(map[string]interface{}{"ddns_enabled": enabled, "ddns_provider_id": providerID}).Error; err != nil {
			return err
		}
		var existing []model.NodeDDNSRecord
		if err := tx.Where("node_id = ?", nodeID).Find(&existing).Error; err != nil {
			return err
		}
		byKey := make(map[string]model.NodeDDNSRecord, len(existing))
		for _, rec := range existing {
			byKey[rec.Domain+"|"+rec.RecordType] = rec
		}
		keep := make(map[int64]bool, len(records))
		for i, rec := range records {
			key := rec.Domain + "|" + rec.RecordType
			if old, ok := byKey[key]; ok {
				keep[old.ID] = true
				updates := map[string]interface{}{
					"role": rec.Role, "source": rec.Source, "source_prefix": rec.SourcePrefix,
					"takeover": rec.Takeover, "inx": i, "updated_time": now,
				}
				if old.Role != rec.Role || old.Source != rec.Source || old.SourcePrefix != rec.SourcePrefix {
					updates["status"] = model.DDNSStatusPending
					updates["next_retry_at"] = int64(0)
					updates["fail_count"] = 0
					if old.Role != rec.Role {
						updates["applied"] = false
					}
				}
				if old.Status == model.DDNSStatusFailed && rec.Takeover && !old.Takeover {
					updates["status"] = model.DDNSStatusPending
					updates["next_retry_at"] = int64(0)
				}
				if providerChanged {
					updates["zone_id"], updates["record_id"] = "", ""
					updates["status"] = model.DDNSStatusPending
					updates["next_retry_at"] = int64(0)
					updates["fail_count"] = 0
				}
				if err := tx.Model(&model.NodeDDNSRecord{}).Where("id = ?", old.ID).Updates(updates).Error; err != nil {
					return err
				}
				continue
			}
			var owner model.NodeDDNSRecord
			err := tx.Where("domain = ? AND record_type = ? AND node_id <> ?", rec.Domain, rec.RecordType, nodeID).First(&owner).Error
			if err == nil {
				return fmt.Errorf("域名 %s 的 %s 记录已被节点 #%d 使用", rec.Domain, rec.RecordType, owner.NodeID)
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			row := model.NodeDDNSRecord{
				NodeID: nodeID, Domain: rec.Domain, RecordType: rec.RecordType, Role: rec.Role,
				Source: rec.Source, SourcePrefix: rec.SourcePrefix, Takeover: rec.Takeover, Inx: i,
				Applied: rec.Applied, Status: model.DDNSStatusPending, CreatedTime: now, UpdatedTime: now,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		for _, old := range existing {
			if !keep[old.ID] {
				if err := tx.Where("id = ?", old.ID).Delete(&model.NodeDDNSRecord{}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// UpdateDDNSRecordState writes sync state columns of one record.
func (r *Repository) UpdateDDNSRecordState(id int64, updates map[string]interface{}) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if text, ok := updates["last_error"].(string); ok {
		updates["last_error"] = truncateRunes(text, 250)
	}
	updates["updated_time"] = time.Now().UnixMilli()
	return r.db.Model(&model.NodeDDNSRecord{}).Where("id = ?", id).Updates(updates).Error
}

// ApplyDDNSDomain switches the node address field a record owns to its
// domain, once (plan 142). Only address columns change; the caller runs the
// usual node-update sync with the returned old/new rows. currentIP is the
// address the record now points to (an extra IP equal to it is replaced).
func (r *Repository) ApplyDDNSDomain(recordID int64) (oldNode, newNode *model.Node, err error) {
	if r == nil || r.db == nil {
		return nil, nil, errors.New("repository not initialized")
	}
	err = r.db.Transaction(func(tx *gorm.DB) error {
		var rec model.NodeDDNSRecord
		if err := tx.Where("id = ?", recordID).First(&rec).Error; err != nil {
			return err
		}
		var node model.Node
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", rec.NodeID).First(&node).Error; err != nil {
			return err
		}
		before := node
		next := DDNSOwnedAddresses(node, rec)
		if next.ServerIP != node.ServerIP || next.ServerIPV4 != node.ServerIPV4 || next.ServerIPV6 != node.ServerIPV6 || next.ExtraIPs != node.ExtraIPs {
			if err := tx.Model(&model.Node{}).Where("id = ?", node.ID).Updates(map[string]interface{}{
				"server_ip": next.ServerIP, "server_ip_v4": next.ServerIPV4, "server_ip_v6": next.ServerIPV6,
				"extra_ips": next.ExtraIPs, "updated_time": time.Now().UnixMilli(),
			}).Error; err != nil {
				return err
			}
			oldNode, newNode = &before, &next
		}
		return tx.Model(&model.NodeDDNSRecord{}).Where("id = ?", rec.ID).Update("applied", true).Error
	})
	return oldNode, newNode, err
}

// DDNSOwnedAddresses returns the node with the field owned by rec set to
// the record's domain. Main records take the IPv4 (A) or IPv6 (AAAA) field
// (and server_ip when it held the same value); extra records replace the
// extra IP equal to the record's current IP, or are appended.
func DDNSOwnedAddresses(node model.Node, rec model.NodeDDNSRecord) model.Node {
	domain := rec.Domain
	if rec.Role == model.DDNSRoleMain {
		field := &node.ServerIPV6
		if rec.RecordType == "A" {
			field = &node.ServerIPV4
		}
		old := strings.TrimSpace(field.String)
		if strings.EqualFold(old, domain) {
			return node
		}
		*field = sql.NullString{String: domain, Valid: true}
		if strings.TrimSpace(node.ServerIP) == "" || (old != "" && nodeAddressKey(node.ServerIP) == nodeAddressKey(old)) {
			node.ServerIP = domain
		}
		// The domain cannot stay listed as an extra entry as well.
		node.ExtraIPs = removeExtraAddress(node.ExtraIPs, domain)
		return node
	}
	extras := splitNodeIPList(node.ExtraIPs.String)
	out := make([]string, 0, len(extras)+1)
	replaced := false
	for _, token := range extras {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		key := nodeAddressKey(token)
		if key == nodeAddressKey(domain) {
			return node // already there
		}
		if !replaced && rec.CurrentIP != "" && key == nodeAddressKey(rec.CurrentIP) {
			out = append(out, domain)
			replaced = true
			continue
		}
		out = append(out, token)
	}
	if !replaced {
		out = append(out, domain)
	}
	raw, _ := json.Marshal(out)
	node.ExtraIPs = sql.NullString{String: string(raw), Valid: true}
	return node
}

func removeExtraAddress(extraIPs sql.NullString, address string) sql.NullString {
	if !extraIPs.Valid || strings.TrimSpace(extraIPs.String) == "" {
		return extraIPs
	}
	tokens := splitNodeIPList(extraIPs.String)
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token = strings.TrimSpace(token); token != "" && nodeAddressKey(token) != nodeAddressKey(address) {
			out = append(out, token)
		}
	}
	if len(out) == len(tokens) {
		return extraIPs
	}
	if len(out) == 0 {
		return sql.NullString{}
	}
	raw, _ := json.Marshal(out)
	return sql.NullString{String: string(raw), Valid: true}
}

// RewriteNodeBindIP moves explicit listen IPs (forward_port.in_ip) of a node
// from oldIP to newIP after its DDNS address changed; returns the forwards
// to redeploy. Bind IPs stay literal addresses.
func (r *Repository) RewriteNodeBindIP(nodeID int64, oldIP, newIP string) ([]int64, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	oldKey := nodeAddressKey(oldIP)
	if oldKey == "" || parseLiteralIP(newIP) == nil || oldKey == nodeAddressKey(newIP) {
		return nil, nil
	}
	var ports []model.ForwardPort
	if err := r.db.Where("node_id = ? AND in_ip IS NOT NULL AND in_ip <> ''", nodeID).Find(&ports).Error; err != nil {
		return nil, err
	}
	ids := make(map[int64]struct{})
	for _, port := range ports {
		tokens := strings.Split(port.InIP.String, ",")
		changed := false
		for i, token := range tokens {
			if nodeAddressKey(strings.TrimSpace(token)) == oldKey {
				tokens[i] = newIP
				changed = true
			}
		}
		if !changed {
			continue
		}
		if err := r.db.Model(&model.ForwardPort{}).Where("id = ?", port.ID).Update("in_ip", strings.Join(tokens, ",")).Error; err != nil {
			return nil, err
		}
		ids[port.ForwardID] = struct{}{}
	}
	return sortedIDSet(ids), nil
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
