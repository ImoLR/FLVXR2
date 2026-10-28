package repo

import (
	"errors"

	"go-backend/internal/store/model"
)

// SetNodeAdminNotice stores a node's 节点提示 (plan 154). The caller
// normalises the text; an empty string removes the notice. Writes only this
// column, so saving a notice never touches anything the agent runs.
func (r *Repository) SetNodeAdminNotice(nodeID int64, notice string) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Model(&model.Node{}).Where("id = ?", nodeID).Update("admin_notice", notice).Error
}
