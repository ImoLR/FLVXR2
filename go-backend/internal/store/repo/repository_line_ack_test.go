package repo

import (
	"testing"
	"time"

	"go-backend/internal/store/model"
)

func TestLineComplianceAckSigNormalization(t *testing.T) {
	a := LineComplianceAckSig(10, 0, "Boil.Example.com:10020, 1.2.3.4:5")
	if a != LineComplianceAckSig(10, 0, " boil.example.com:10020\n1.2.3.4:5\n") {
		t.Fatal("case / separators / spaces changed the signature")
	}
	for name, other := range map[string]string{
		"tunnel":  LineComplianceAckSig(11, 0, "boil.example.com:10020,1.2.3.4:5"),
		"landing": LineComplianceAckSig(10, 5, "boil.example.com:10020,1.2.3.4:5"),
		"order":   LineComplianceAckSig(10, 0, "1.2.3.4:5,boil.example.com:10020"),
		"port":    LineComplianceAckSig(10, 0, "boil.example.com:10021,1.2.3.4:5"),
	} {
		if other == a {
			t.Errorf("%s change kept the signature", name)
		}
	}
	if len(a) > 80 {
		t.Fatalf("signature longer than the column: %d", len(a))
	}
}

// Plan 164: every target write path keeps the confirmation only while the
// target stays the same; a change and change back does not restore it.
func TestLineComplianceAckWritePathsBothDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			now := time.Now().UnixMilli()
			const addrA, addrB = "boil.example.com:10020", "9.9.9.9:443"
			for _, f := range []*model.Forward{
				{ID: 1, UserID: 2, UserName: "u", Name: "a", TunnelID: 10, RemoteAddr: addrA, Strategy: "fifo", CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost"},
				{ID: 2, UserID: 2, UserName: "u", Name: "b", TunnelID: 10, RemoteAddr: addrB, Strategy: "fifo", CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost"},
			} {
				if err := r.db.Create(f).Error; err != nil {
					t.Fatal(err)
				}
			}
			sig := func() string {
				t.Helper()
				var f model.Forward
				if err := r.db.Select("line_compliance_ack_sig").First(&f, 1).Error; err != nil {
					t.Fatal(err)
				}
				return f.LineComplianceAckSig
			}
			ack := func() {
				t.Helper()
				if err := r.SetForwardLineComplianceAck(1, true); err != nil {
					t.Fatal(err)
				}
			}
			update := func(tunnelID int64, addr string, landing ...int64) {
				t.Helper()
				if err := r.UpdateForward(1, "renamed", tunnelID, addr, "fifo", now, nil, 0, 0, 0, nil, false, 0, "gost", 0, "port", "", "", true, landing...); err != nil {
					t.Fatal(err)
				}
			}
			want := LineComplianceAckSig(10, 0, addrA)

			if sig() != "" {
				t.Fatal("new rules start without a confirmation")
			}
			ack()
			if sig() != want {
				t.Fatalf("ack stored %q, want %q", sig(), want)
			}
			if got, err := r.ForwardLineComplianceAckSigs([]int64{1, 2}); err != nil || len(got) != 1 || got[1] != want {
				t.Fatalf("batched read: %v %v", got, err)
			}
			// Same target (name edit), with and without the landing argument.
			update(10, addrA, 0)
			update(10, addrA)
			if sig() != want {
				t.Fatal("an edit with the same target dropped the confirmation")
			}
			// New target, then back: stays cleared.
			update(10, addrB, 0)
			if sig() != "" {
				t.Fatal("target change kept the confirmation")
			}
			update(10, addrA, 0)
			if sig() != "" {
				t.Fatal("changing the target back restored the confirmation")
			}
			// Tunnel change in the rule dialog.
			ack()
			update(11, addrA, 0)
			if sig() != "" {
				t.Fatal("tunnel change kept the confirmation")
			}
			update(10, addrA, 0)
			// Landing change in the rule dialog.
			ack()
			update(10, addrA, 7)
			if sig() != "" {
				t.Fatal("landing change kept the confirmation")
			}
			update(10, addrA, 0)
			// Batch 换隧道 / tunnel-delete migration.
			ack()
			if err := r.UpdateForwardTunnel(1, 10, now); err != nil {
				t.Fatal(err)
			}
			if sig() != want {
				t.Fatal("move to the same tunnel dropped the confirmation")
			}
			if err := r.UpdateForwardTunnel(1, 11, now); err != nil {
				t.Fatal(err)
			}
			if sig() != "" {
				t.Fatal("tunnel move kept the confirmation")
			}
			if err := r.UpdateForwardTunnel(1, 10, now); err != nil {
				t.Fatal(err)
			}
			// Chain target path.
			ack()
			if err := r.SetForwardRemoteAddr(1, addrA); err != nil || sig() != want {
				t.Fatalf("same chain target dropped the confirmation: %v", err)
			}
			if err := r.SetForwardRemoteAddr(1, "127.0.0.1:20001"); err != nil || sig() != "" {
				t.Fatalf("chain target change kept the confirmation: %v", err)
			}
			if err := r.SetForwardRemoteAddr(1, addrA); err != nil {
				t.Fatal(err)
			}
			// Landing link path.
			ack()
			if err := r.SetForwardLandingNode(1, 0); err != nil || sig() != want {
				t.Fatalf("same landing dropped the confirmation: %v", err)
			}
			if err := r.SetForwardLandingNode(1, 7); err != nil || sig() != "" {
				t.Fatalf("landing link kept the confirmation: %v", err)
			}
			if err := r.SetForwardLandingNode(1, 0); err != nil {
				t.Fatal(err)
			}
			// A failed edit's rollback restores the confirmation it cleared.
			ack()
			old, err := r.GetForwardRecord(1)
			if err != nil || old == nil || old.LineComplianceAckSig != want {
				t.Fatalf("record: %+v %v", old, err)
			}
			update(10, addrB, 0)
			r.RollbackForwardFields(1, old.UserID, old.UserName, old.Name, old.TunnelID, old.RemoteAddr, old.Strategy, old.Status, nil, 0, 0, false, "", false, now, old)
			if sig() != want {
				t.Fatal("rollback did not restore the confirmation")
			}
			// Clearing.
			if err := r.SetForwardLineComplianceAck(1, false); err != nil || sig() != "" {
				t.Fatalf("clear: %v", err)
			}
			// The other rule was never touched.
			if got, _ := r.ForwardLineComplianceAckSigs([]int64{2}); len(got) != 0 {
				t.Fatalf("rule 2 got a confirmation: %v", got)
			}
		})
	}
}
