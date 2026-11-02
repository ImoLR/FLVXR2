package repo

// Plan 196 「Telegram 通知」 bot: bound chats, sent reminders, 「已续费」 and the
// node list the reminders are computed from.

import (
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"go-backend/internal/store/model"
)

// HiddenConfigPrefix: vite_config keys of the Telegram bot (token, HMAC key,
// profile, settings, last run). They never leave the panel through the
// generic config API or a backup export; the bot card has its own API that
// never returns the token.
const HiddenConfigPrefix = "tg_bot_"

// IsHiddenConfigKey reports whether a vite_config key is bot-internal.
func IsHiddenConfigKey(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), HiddenConfigPrefix)
}

// ErrTGBotChatLimit: the chat limit is reached.
var ErrTGBotChatLimit = errors.New("tg bot chat limit reached")

func (r *Repository) TGBotListChats() ([]model.TGBotChat, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []model.TGBotChat
	err := r.db.Order("id ASC").Find(&out).Error
	return out, err
}

// TGBotAddChat binds a chat (max chats in total). An already bound chat only
// gets its title refreshed; added reports whether a new row was created.
func (r *Repository) TGBotAddChat(c model.TGBotChat, max int) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("repository not initialized")
	}
	added := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.TGBotChat
		err := tx.Where("chat_id = ?", c.ChatID).First(&existing).Error
		if err == nil {
			return tx.Model(&model.TGBotChat{}).Where("id = ?", existing.ID).Updates(map[string]interface{}{"title": c.Title, "kind": c.Kind}).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var n int64
		if err := tx.Model(&model.TGBotChat{}).Count(&n).Error; err != nil {
			return err
		}
		if max > 0 && n >= int64(max) {
			return ErrTGBotChatLimit
		}
		c.ID = 0
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
		added = true
		return nil
	})
	return added, err
}

// TGBotDeleteChat unbinds one chat; ok is false when it was not bound.
func (r *Repository) TGBotDeleteChat(chatID int64) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("repository not initialized")
	}
	res := r.db.Where("chat_id = ?", chatID).Delete(&model.TGBotChat{})
	return res.RowsAffected > 0, res.Error
}

// TGBotDeleteAllChats unbinds every chat (the token now belongs to another bot).
func (r *Repository) TGBotDeleteAllChats() error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Where("1 = 1").Delete(&model.TGBotChat{}).Error
}

// TGBotSetChatError records ("" clears) the last permanent send failure.
func (r *Repository) TGBotSetChatError(chatID int64, msg string, at int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if msg == "" {
		at = 0
	}
	return r.db.Model(&model.TGBotChat{}).Where("chat_id = ?", chatID).
		Updates(map[string]interface{}{"last_error": truncateUTF8(msg, 200), "last_error_at": at}).Error
}

// TGBotListSent: sent reminders whose due date is at or after minDue.
func (r *Repository) TGBotListSent(minDue int64) ([]model.TGBotSent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []model.TGBotSent
	err := r.db.Select("id", "machine_key", "due_ms", "days", "chat_id", "message_id", "sent_time").
		Where("due_ms >= ?", minDue).Find(&out).Error
	return out, err
}

// TGBotSentFor: every sent copy of the reminders of one machine and due date.
func (r *Repository) TGBotSentFor(key string, due int64) ([]model.TGBotSent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []model.TGBotSent
	err := r.db.Where("machine_key = ? AND due_ms = ?", key, due).Order("id ASC").Limit(100).Find(&out).Error
	return out, err
}

// TGBotInsertSent stores a sent reminder (a duplicate key is ignored).
func (r *Repository) TGBotInsertSent(s model.TGBotSent) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	s.ID = 0
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&s).Error
}

// TGBotListAcks: 「已续费」 rows whose due date is at or after minDue.
func (r *Repository) TGBotListAcks(minDue int64) ([]model.TGBotAck, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var out []model.TGBotAck
	err := r.db.Where("due_ms >= ?", minDue).Find(&out).Error
	return out, err
}

// TGBotInsertAck records 「已续费」; inserted is false when it already was.
func (r *Repository) TGBotInsertAck(a model.TGBotAck) (bool, error) {
	if r == nil || r.db == nil {
		return false, errors.New("repository not initialized")
	}
	a.ID = 0
	a.ByName = truncateUTF8(a.ByName, 64)
	res := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&a)
	return res.RowsAffected > 0, res.Error
}

// TGBotPrune drops sent / ack rows of due dates before the cut-off.
func (r *Repository) TGBotPrune(before int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	if err := r.db.Where("due_ms < ?", before).Delete(&model.TGBotSent{}).Error; err != nil {
		return err
	}
	return r.db.Where("due_ms < ?", before).Delete(&model.TGBotAck{}).Error
}

// ListRenewalNodes: every node (local and remote) with what the reminders
// need: name, expiry anchor, cycle and the addresses to match a 救命版 machine.
func (r *Repository) ListRenewalNodes() ([]ProbeNodeRow, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var nodes []model.Node
	err := r.db.Select("id", "inx", "name", "region", "expiry_time", "renewal_cycle", "server_ip", "server_ip_v4", "server_ip_v6", "extra_ips").
		Order("inx ASC, id ASC").Find(&nodes).Error
	if err != nil {
		return nil, err
	}
	out := make([]ProbeNodeRow, 0, len(nodes))
	for _, n := range nodes {
		row := ProbeNodeRow{ID: n.ID, Inx: n.Inx, Name: n.Name, Region: n.Region, RenewalCycle: n.RenewalCycle.String,
			ServerIP: n.ServerIP, ServerIPV4: n.ServerIPV4.String, ServerIPV6: n.ServerIPV6.String, ExtraIPs: n.ExtraIPs.String}
		if n.ExpiryTime.Valid && n.ExpiryTime.Int64 > 0 {
			row.ExpiryTime = n.ExpiryTime.Int64
		}
		out = append(out, row)
	}
	return out, nil
}

// MarkNodeRenewalDismissed is 「已续费」 on the 节点 page's own reminder: the
// existing expiry_reminder_dismissed flag, valid until untilSec (unix
// seconds, the due date of the renewed cycle). The node's dates are not
// touched (rolling cycles, plan 194).
func (r *Repository) MarkNodeRenewalDismissed(nodeID, untilSec int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Model(&model.Node{}).Where("id = ?", nodeID).Updates(map[string]interface{}{
		"expiry_reminder_dismissed":       1,
		"expiry_reminder_dismissed_until": untilSec,
	}).Error
}

func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}
