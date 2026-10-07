package repo

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

func TestNodeHalfYearRenewalReaders(t *testing.T) {
	for _, cycle := range []string{"halfyear", "halfYear", "HALFYEAR"} {
		t.Run(cycle, func(t *testing.T) {
			r, err := Open(filepath.Join(t.TempDir(), "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.Close() })
			now := time.Now()
			anchor := time.Date(now.Year(), now.Month(), 15, 12, 0, 0, 0, now.Location()).AddDate(0, 1, 0)
			node := model.Node{
				Name:         "half-year",
				Status:       1,
				RenewalCycle: sql.NullString{String: cycle, Valid: true},
				ExpiryTime:   sql.NullInt64{Int64: anchor.UnixMilli(), Valid: true},
			}
			if err := r.db.Create(&node).Error; err != nil {
				t.Fatal(err)
			}

			if err := r.RefreshNodeExpiryReminder(node.ID); err != nil {
				t.Fatal(err)
			}
			var refreshed model.Node
			if err := r.db.First(&refreshed, node.ID).Error; err != nil {
				t.Fatal(err)
			}
			want := anchor.AddDate(0, 6, 0).UnixMilli()
			if refreshed.ExpiryTime.Int64 != want || refreshed.RenewalCycle.String != cycle {
				t.Fatalf("refresh got expiry=%d cycle=%q, want expiry=%d cycle=%q", refreshed.ExpiryTime.Int64, refreshed.RenewalCycle.String, want, cycle)
			}

			due, err := r.ListNodesWithTrafficResetDue(time.UnixMilli(want))
			if err != nil || len(due) != 1 || due[0].ID != node.ID {
				t.Fatalf("traffic reset due=%v err=%v", due, err)
			}

			results, err := r.AdvanceNodeRenewalCycles(want + 1)
			if err != nil || len(results) != 1 {
				t.Fatalf("advance results=%v err=%v", results, err)
			}
			if err := r.db.First(&refreshed, node.ID).Error; err != nil {
				t.Fatal(err)
			}
			want = anchor.AddDate(0, 12, 0).UnixMilli()
			if refreshed.ExpiryTime.Int64 != want || refreshed.RenewalCycle.String != cycle {
				t.Fatalf("advance got expiry=%d cycle=%q, want expiry=%d cycle=%q", refreshed.ExpiryTime.Int64, refreshed.RenewalCycle.String, want, cycle)
			}
		})
	}
}
