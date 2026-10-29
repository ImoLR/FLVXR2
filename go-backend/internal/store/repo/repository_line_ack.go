package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// 线路机「已确认合规」(plan 164). An admin may confirm that the hand-typed
// target of a rule on a 线路机 tunnel really is VLESS Reality Vision. The
// confirmation stores the signature of the target it was given for, so it
// stops counting as soon as the target, landing or tunnel changes — whichever
// path changed it. The target write paths below also clear it explicitly, so
// changing a target away and back does not bring an old confirmation back.

const lineAckSigVersion = "v1:"

// LineComplianceAckSig is the signature of a rule target: tunnel, landing
// node and the normalized address list (order kept, case-insensitive).
func LineComplianceAckSig(tunnelID, landingNodeID int64, remoteAddr string) string {
	parts := strings.FieldsFunc(remoteAddr, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
	addrs := make([]string, 0, len(parts))
	for _, part := range parts {
		if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
			addrs = append(addrs, p)
		}
	}
	sum := sha256.Sum256([]byte(strconv.FormatInt(tunnelID, 10) + "|" + strconv.FormatInt(landingNodeID, 10) + "|" + strings.Join(addrs, ",")))
	return lineAckSigVersion + hex.EncodeToString(sum[:20])
}

// SetForwardLineComplianceAck stores (ack) or clears the confirmation for
// the rule's current target. Eligibility is checked by the caller; the
// signature is taken from the row inside the same transaction.
func (r *Repository) SetForwardLineComplianceAck(forwardID int64, ack bool) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var f model.Forward
		if err := tx.Select("id", "tunnel_id", "landing_node_id", "remote_addr").First(&f, forwardID).Error; err != nil {
			return err
		}
		sig := ""
		if ack {
			sig = LineComplianceAckSig(f.TunnelID, f.LandingNodeID, f.RemoteAddr)
		}
		return tx.Model(&model.Forward{}).Where("id = ?", forwardID).Update("line_compliance_ack_sig", sig).Error
	})
}

// ForwardLineComplianceAckSigs returns the stored confirmations of the given
// rules (rules without one are left out). One query.
func (r *Repository) ForwardLineComplianceAckSigs(ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if r == nil || r.db == nil || len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID                   int64
		LineComplianceAckSig string
	}
	if err := r.db.Model(&model.Forward{}).Select("id", "line_compliance_ack_sig").
		Where("id IN ? AND line_compliance_ack_sig <> ''", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.LineComplianceAckSig
	}
	return out, nil
}

// lineAckKeepExpr keeps the confirmation only when the UPDATE leaves the
// target as it was (the CASE sees the old row in SQLite and PostgreSQL).
// landingNodeID < 0 means the statement does not touch landing_node_id.
func lineAckKeepExpr(tunnelID int64, remoteAddr string, landingNodeID int64) interface{} {
	if landingNodeID < 0 {
		return gorm.Expr("CASE WHEN tunnel_id = ? AND remote_addr = ? THEN line_compliance_ack_sig ELSE '' END", tunnelID, remoteAddr)
	}
	return gorm.Expr("CASE WHEN tunnel_id = ? AND remote_addr = ? AND landing_node_id = ? THEN line_compliance_ack_sig ELSE '' END", tunnelID, remoteAddr, landingNodeID)
}
