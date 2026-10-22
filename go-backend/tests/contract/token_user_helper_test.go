package contract_test

import (
	"fmt"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// ensureContractUser makes sure the user behind a hand-made test token exists
// and is active: since plan 135 the JWT middleware rejects tokens of deleted
// or disabled users.
func ensureContractUser(t *testing.T, r *repo.Repository, id int64, role int) {
	t.Helper()
	var n int64
	if err := r.DB().Model(&model.User{}).Where("id = ?", id).Count(&n).Error; err != nil {
		t.Fatalf("count user %d: %v", id, err)
	}
	if n > 0 {
		return
	}
	now := time.Now().UnixMilli()
	u := model.User{ID: id, User: fmt.Sprintf("token_user_%d", id), Pwd: "x", RoleID: role, ExpTime: now + 365*86400000,
		Flow: 99999, FlowResetTime: 1, Num: 99999, CreatedTime: now, Status: 1}
	if err := r.DB().Create(&u).Error; err != nil {
		t.Fatalf("create user %d: %v", id, err)
	}
}
