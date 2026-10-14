package repo

import (
	"errors"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

func (r *Repository) ListLeiKwanAccounts() ([]model.LeiKwanAccount, error) {
	out := []model.LeiKwanAccount{}
	err := r.db.Order("id").Find(&out).Error
	return out, err
}
func (r *Repository) GetLeiKwanAccount(id int64) (*model.LeiKwanAccount, error) {
	var a model.LeiKwanAccount
	err := r.db.First(&a, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}
func (r *Repository) SaveLeiKwanAccount(a *model.LeiKwanAccount) error {
	a.UpdatedTime = time.Now().UnixMilli()
	return r.db.Save(a).Error
}
func (r *Repository) DeleteLeiKwanAccount(id int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.LeiKwanPipe{}).Where("account_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("账号仍有管道，不能删除")
		}
		if err := tx.Where("account_id = ?", id).Delete(&model.LeiKwanCreate{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.LeiKwanAccount{}, id).Error
	})
}
func (r *Repository) ListLeiKwanPipes(accountID int64) ([]model.LeiKwanPipe, error) {
	out := []model.LeiKwanPipe{}
	q := r.db.Order("id")
	if accountID > 0 {
		q = q.Where("account_id = ?", accountID)
	}
	err := q.Find(&out).Error
	return out, err
}
func (r *Repository) GetLeiKwanPipe(id int64) (*model.LeiKwanPipe, error) {
	var p model.LeiKwanPipe
	err := r.db.First(&p, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &p, err
}
func (r *Repository) GetLeiKwanPipeByTunnel(id int64) (*model.LeiKwanPipe, error) {
	var p model.LeiKwanPipe
	err := r.db.Where("tunnel_id = ?", id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &p, err
}
func (r *Repository) GetLeiKwanPipeByForward(accountID, forwardID int64) (*model.LeiKwanPipe, error) {
	var p model.LeiKwanPipe
	err := r.db.Where("account_id = ? AND forward_id = ?", accountID, forwardID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &p, err
}
func (r *Repository) SaveLeiKwanPipe(p *model.LeiKwanPipe) error {
	p.UpdatedTime = time.Now().UnixMilli()
	return r.db.Save(p).Error
}
func (r *Repository) BindLeiKwanPipe(pipeID, tunnelID int64) error {
	q := r.db.Model(&model.LeiKwanPipe{}).Where("id = ? AND (tunnel_id IS NULL OR tunnel_id = ?)", pipeID, tunnelID).Update("tunnel_id", tunnelID)
	if q.Error != nil {
		return q.Error
	}
	if q.RowsAffected != 1 {
		return errors.New("管道已被其他隧道使用")
	}
	return nil
}
func (r *Repository) UnbindLeiKwanPipe(tunnelID int64) error {
	return r.db.Model(&model.LeiKwanPipe{}).Where("tunnel_id = ?", tunnelID).Update("tunnel_id", nil).Error
}
func (r *Repository) DeleteLeiKwanPipe(id int64) error {
	return r.db.Delete(&model.LeiKwanPipe{}, id).Error
}
func (r *Repository) PrepareLeiKwanCreate(c *model.LeiKwanCreate) error {
	c.CreatedTime = time.Now().UnixMilli()
	if err := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(c).Error; err != nil {
		return err
	}
	var saved model.LeiKwanCreate
	if err := r.db.Where("account_id = ? AND request_id = ?", c.AccountID, c.RequestID).First(&saved).Error; err != nil {
		return err
	}
	if saved.Payload != c.Payload || saved.LandingNodeID != c.LandingNodeID {
		return errors.New("同一请求标识的创建参数不能改变，请重试原请求")
	}
	*c = saved
	return nil
}
func (r *Repository) CompleteLeiKwanCreate(id, forwardID int64) error {
	return r.db.Model(&model.LeiKwanCreate{}).Where("id = ?", id).Update("forward_id", forwardID).Error
}
func (r *Repository) LeiKwanTunnelName(id int64) string {
	var name string
	r.db.Model(&model.Tunnel{}).Select("name").Where("id = ?", id).Scan(&name)
	return name
}

func (r *Repository) GetLeiKwanPipeByForwardID(forwardID int64) (*model.LeiKwanPipe, error) {
	var p model.LeiKwanPipe
	err := r.db.Where("forward_id = ?", forwardID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &p, err
}
func (r *Repository) GetLeiKwanCreate(accountID int64, requestID string) (*model.LeiKwanCreate, error) {
	var c model.LeiKwanCreate
	err := r.db.Where("account_id = ? AND request_id = ?", accountID, requestID).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &c, err
}
