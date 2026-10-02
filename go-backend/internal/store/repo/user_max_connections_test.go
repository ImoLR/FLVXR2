package repo

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

func TestQuotaColumnsAutoMigrateWithZeroDefaults(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "quota-columns.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	for _, table := range []string{"user", "forward"} {
		var name string
		var notNull int
		var defaultValue interface{}
		rows, err := r.DB().Raw("PRAGMA table_info('" + table + "')").Rows()
		if err != nil {
			t.Fatalf("table info %s: %v", table, err)
		}
		found := false
		for rows.Next() {
			var cid, pk int
			var dataType string
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
				rows.Close()
				t.Fatalf("scan table info %s: %v", table, err)
			}
			if name == "max_client_ips" {
				found = true
				if notNull != 1 || fmt.Sprint(defaultValue) != "0" {
					rows.Close()
					t.Fatalf("%s.max_client_ips notnull/default = %d/%v, want 1/0", table, notNull, defaultValue)
				}
			}
		}
		rows.Close()
		if !found {
			t.Fatalf("%s.max_client_ips was not migrated", table)
		}
	}
}

func TestUserMaxConnectionsCreateUpdateAndList(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	now := time.Now().UnixMilli()
	userID, err := r.CreateUser("limit-user", "pwd", 1, now+86400000, 100, 1, 10, 77, 23, 1, now, 0, 0, 0, nil)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	user, err := r.GetUserByID(userID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if user == nil || user.MaxConnections != 77 {
		t.Fatalf("expected max connections 77, got %#v", user)
	}
	if user.MaxClientIps != 23 {
		t.Fatalf("expected max client IPs 23, got %d", user.MaxClientIps)
	}

	users, err := r.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	found := false
	for _, item := range users {
		if item["id"] == userID {
			found = true
			if got := item["maxConnections"]; got != 77 {
				t.Fatalf("expected list maxConnections 77, got %#v", got)
			}
			if got := item["maxClientIps"]; got != 23 {
				t.Fatalf("expected list maxClientIps 23, got %#v", got)
			}
		}
	}
	if !found {
		t.Fatalf("created user not found in list")
	}

	if err := r.UpdateUserWithoutPassword(userID, "limit-user", "remark", 100, 10, 0, 0, now+86400000, 1, 1, now, 0, 0, 0, nil); err != nil {
		t.Fatalf("update user: %v", err)
	}
	user, err = r.GetUserByID(userID)
	if err != nil {
		t.Fatalf("get updated user: %v", err)
	}
	if user == nil || user.MaxConnections != 0 {
		t.Fatalf("expected max connections reset to 0, got %#v", user)
	}
	if user.MaxClientIps != 0 {
		t.Fatalf("expected max client IPs reset to 0, got %d", user.MaxClientIps)
	}
}

func TestForwardRecordIncludesUserMaxConnections(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	now := time.Now().UnixMilli()
	userID, err := r.CreateUser("forward-limit-user", "pwd", 1, now+86400000, 100, 1, 10, 64, 12, 1, now, 0, 0, 0, nil)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	forward := model.Forward{
		UserID:         userID,
		UserName:       "forward-limit-user",
		Name:           "forward-limit",
		TunnelID:       1,
		RemoteAddr:     "127.0.0.1:80",
		Strategy:       "fifo",
		CreatedTime:    now,
		UpdatedTime:    now,
		Status:         1,
		Inx:            1,
		MaxConnections: 0,
		MaxClientIps:   4,
	}
	if err := r.DB().Create(&forward).Error; err != nil {
		t.Fatalf("create forward: %v", err)
	}

	record, err := r.GetForwardRecord(forward.ID)
	if err != nil {
		t.Fatalf("get forward record: %v", err)
	}
	if record == nil {
		t.Fatalf("expected forward record")
	}
	if record.UserMaxConnections != 64 {
		t.Fatalf("expected user max connections 64, got %d", record.UserMaxConnections)
	}
	if record.MaxClientIps != 4 || record.UserMaxClientIps != 12 {
		t.Fatalf("expected rule/user max client IPs 4/12, got %d/%d", record.MaxClientIps, record.UserMaxClientIps)
	}
}
