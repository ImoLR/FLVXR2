package repo

import (
	"path/filepath"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

// Regression: ListActiveNftablesForwards used to Find into []model.ForwardRecord,
// which has no table, so GORM queried "forward_records" and failed with
// "no such table: forward_records".
func TestListActiveNftablesForwardsQueriesForwardTable(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer r.Close()

	now := time.Now().UnixMilli()
	insert := func(name, mode, remote string, status int, userID, tunnelID int64) int64 {
		t.Helper()
		f := model.Forward{
			UserID:      userID,
			UserName:    "u",
			Name:        name,
			TunnelID:    tunnelID,
			RemoteAddr:  remote,
			Strategy:    "fifo",
			Status:      status,
			Mode:        mode,
			CreatedTime: now,
			UpdatedTime: now,
		}
		if err := r.db.Create(&f).Error; err != nil {
			t.Fatalf("insert forward %s: %v", name, err)
		}
		return f.ID
	}

	activeA := insert("nft-active-a", "nftables", "example.com:443", 1, 7, 11)
	insert("nft-paused", "nftables", "paused.example.com:80", 0, 7, 11)
	insert("gost-active", "gost", "gost.example.com:80", 1, 7, 11)
	activeB := insert("nft-active-b", "nftables", "1.2.3.4:53,example.org:53", 1, 8, 12)

	rows, err := r.ListActiveNftablesForwards()
	if err != nil {
		t.Fatalf("ListActiveNftablesForwards: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 active nftables forwards, got %d: %#v", len(rows), rows)
	}
	if rows[0].ID != activeA || rows[1].ID != activeB {
		t.Fatalf("unexpected ids: got %d,%d want %d,%d", rows[0].ID, rows[1].ID, activeA, activeB)
	}
	want := []struct {
		userID, tunnelID int64
		remote, name     string
	}{
		{7, 11, "example.com:443", "nft-active-a"},
		{8, 12, "1.2.3.4:53,example.org:53", "nft-active-b"},
	}
	for i, w := range want {
		got := rows[i]
		if got.UserID != w.userID || got.TunnelID != w.tunnelID || got.RemoteAddr != w.remote ||
			got.Name != w.name || got.Mode != "nftables" || got.Status != 1 {
			t.Fatalf("row %d mismatch: %#v", i, got)
		}
	}
}
