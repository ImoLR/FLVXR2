package repo

import (
	"database/sql"
	"errors"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

func sqlNullStringLocal(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// CreateNodeGroup creates a new node group.
func (r *Repository) CreateNodeGroup(name, description, color string, inx int) (*model.NodeGroup, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}

	now := time.Now().Unix()
	group := &model.NodeGroup{
		Name:        name,
		Description: sqlNullStringLocal(description),
		Color:       color,
		Inx:         inx,
		CreatedTime: now,
	}

	if err := r.db.Create(group).Error; err != nil {
		return nil, err
	}

	return group, nil
}

// UpdateNodeGroup updates an existing node group.
func (r *Repository) UpdateNodeGroup(id int64, name, description, color string, inx int) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}

	updates := map[string]interface{}{
		"name":  name,
		"color": color,
		"inx":   inx,
	}
	if description != "" {
		updates["description"] = sqlNullStringLocal(description)
	}
	updates["updated_time"] = time.Now().Unix()

	return r.db.Model(&model.NodeGroup{}).Where("id = ?", id).Updates(updates).Error
}

// DeleteNodeGroup deletes a node group by ID.
func (r *Repository) DeleteNodeGroup(id int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		// Set nodes' group_id to NULL
		if err := tx.Model(&model.Node{}).Where("group_id = ?", id).Update("group_id", nil).Error; err != nil {
			return err
		}

		// Delete the group
		return tx.Delete(&model.NodeGroup{}, id).Error
	})
}

// ListNodeGroups returns all node groups ordered by inx and id.
func (r *Repository) ListNodeGroups() ([]model.NodeGroup, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}

	var groups []model.NodeGroup
	err := r.db.Order("inx ASC, id ASC").Find(&groups).Error
	return groups, err
}

// GetNodeGroupByID returns a node group by ID.
func (r *Repository) GetNodeGroupByID(id int64) (*model.NodeGroup, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}

	var group model.NodeGroup
	err := r.db.First(&group, id).Error
	if err != nil {
		return nil, err
	}

	return &group, nil
}

// AssignNodeToGroup assigns a node to a group (or removes from group if groupID is nil).
func (r *Repository) AssignNodeToGroup(nodeID, groupID *int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}

	updates := map[string]interface{}{
		"group_id": groupID,
	}
	if groupID == nil {
		updates["group_id"] = gorm.Expr("NULL")
	}

	return r.db.Model(&model.Node{}).Where("id = ?", nodeID).Updates(updates).Error
}

// GetNodesByGroupID returns all nodes belonging to a group.
func (r *Repository) GetNodesByGroupID(groupID int64) ([]model.Node, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}

	var nodes []model.Node
	err := r.db.Where("group_id = ?", groupID).Order("inx ASC, id ASC").Find(&nodes).Error
	return nodes, err
}

// GetNodeGroupCount returns the number of nodes in a group.
func (r *Repository) GetNodeGroupCount(groupID int64) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}

	var count int64
	err := r.db.Model(&model.Node{}).Where("group_id = ?", groupID).Count(&count).Error
	return count, err
}

// SetNodeGroupMembers (plan 324, the 节点 page 利群入口 group): renames the
// group (name "" = keep) and moves nodes in (group_id = id, replacing another
// group) and out (group_id NULL, only while still in this group), all in one
// transaction.
func (r *Repository) SetNodeGroupMembers(id int64, name string, add, remove []int64) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if name != "" {
			if err := tx.Model(&model.NodeGroup{}).Where("id = ?", id).Updates(map[string]interface{}{"name": name, "updated_time": time.Now().Unix()}).Error; err != nil {
				return err
			}
		}
		if len(add) > 0 {
			if err := tx.Model(&model.Node{}).Where("id IN ?", add).Update("group_id", id).Error; err != nil {
				return err
			}
		}
		if len(remove) > 0 {
			if err := tx.Model(&model.Node{}).Where("id IN ? AND group_id = ?", remove, id).Update("group_id", gorm.Expr("NULL")).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
