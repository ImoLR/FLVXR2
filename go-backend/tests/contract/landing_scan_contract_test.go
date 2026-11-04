package contract_test

import (
	"errors"
	"testing"
	"time"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 202 「禁止端口扫描」 on SQLite and PostgreSQL: settings defaults and
// validation, a hit disables its node once (event unique per server + seq,
// seen mark), a second hit on a blocked node only logs, 解除禁用 records who
// lifted it, and the event table keeps 30 days and at most 5000 rows.
func TestLandingScanGuardContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			db := r.DB()
			now := time.Now()
			for _, v := range []interface{}{
				&model.User{ID: 2, User: "alice", Pwd: "x", RoleID: 1, Status: 1, Flow: -1},
				&model.LandingServer{ID: 1, NodeID: 9, Source: "native", Name: "Breadcloud JP", PublicHost: "203.0.113.2", PortMin: 30000, PortMax: 30010, FlowType: 2, TrafficRatio: 1, Status: "active", Protocols: `["ss2022"]`, CreatedTime: 1, UpdatedTime: 1},
				&model.LandingNode{ID: 21, UserID: 2, LandingServerID: 1, Name: "jp-1", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-21", Status: "active", CreatedTime: 1, UpdatedTime: 1},
				&model.LandingNode{ID: 22, UserID: 2, LandingServerID: 1, Name: "jp-2", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-22", Status: "active", CreatedTime: 1, UpdatedTime: 1},
			} {
				if err := db.Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}

			// Settings: defaults while unset, validated save, NULL = ON.
			g, err := r.GetLandingScanGuard()
			if err != nil || g != landingegress.ScanGuardDefaults() || !g.On {
				t.Fatalf("defaults %+v %v", g, err)
			}
			if err := r.SaveLandingScanGuard(landingegress.ScanGuard{On: true, Horizontal: 3, HorizontalWeb: 500, Vertical: 30, Total: 1000}); err == nil {
				t.Fatal("horizontal 3 saved")
			}
			want := landingegress.ScanGuard{On: false, Horizontal: 120, HorizontalWeb: 600, Vertical: 40, Total: 2000}
			if err := r.SaveLandingScanGuard(want); err != nil {
				t.Fatal(err)
			}
			if g, _ := r.GetLandingScanGuard(); g != want {
				t.Fatalf("saved %+v", g)
			}

			hit := func(tag string, seq int64, kind string) repo.LandingScanHit {
				return repo.LandingScanHit{InboundTag: tag, Type: kind, Count: 100, Port: 22, WindowS: 60, At: now, Seq: seq}
			}
			const seq = int64(1793709225987084)
			notices, advanced, err := r.RecordLandingScanStatus(1, "on", []repo.LandingScanHit{hit("flvxl-21", seq, "horizontal"), hit("flvxl-404", seq+1, "total")}, now)
			if err != nil || len(notices) != 1 || !advanced || notices[0].NodeID != 21 || notices[0].ServerName != "Breadcloud JP" {
				t.Fatalf("record %+v %v %v", notices, advanced, err)
			}
			n, _ := r.GetLandingNode(21)
			if n.Status != "disabled" || n.AutoDisabled || n.ScanBlockedAt == 0 || repo.LandingScanBlockOf(n).Reason() != "同一端口 22 在 60 秒内连接了 100 个不同 IP" {
				t.Fatalf("node %+v", n)
			}
			s, _ := r.GetLandingServer(1)
			if s.ScanSeenSeq != seq+1 || s.ScanState != "on" {
				t.Fatalf("server seen %d state %q", s.ScanSeenSeq, s.ScanState)
			}
			// The same report again (lost answer): nothing new.
			notices, advanced, err = r.RecordLandingScanStatus(1, "on", []repo.LandingScanHit{hit("flvxl-21", seq, "horizontal")}, now)
			if err != nil || len(notices) != 0 || advanced {
				t.Fatalf("repeat %+v %v %v", notices, advanced, err)
			}
			// A later hit on the blocked node only logs (first reason kept).
			notices, _, err = r.RecordLandingScanStatus(1, "on", []repo.LandingScanHit{hit("flvxl-21", seq+7, "vertical")}, now)
			if err != nil || len(notices) != 0 {
				t.Fatalf("second hit %+v %v", notices, err)
			}
			if n, _ := r.GetLandingNode(21); repo.LandingScanBlockOf(n).Type != "horizontal" {
				t.Fatal("first reason replaced")
			}
			events, total, err := r.ListLandingScanEvents(0, 50)
			if err != nil || total != 2 || events[0].Blocked || !events[1].Blocked || events[1].UserName != "alice" || events[1].ServerName != "Breadcloud JP" || !events[1].NodeBlocked {
				t.Fatalf("events %+v %d %v", events, total, err)
			}
			if blocked, evs, _ := r.LandingScanCounts(); blocked != 1 || evs != 2 {
				t.Fatalf("counts %d %d", blocked, evs)
			}

			// 解除禁用 (moves the server revision: the node status is not in the egress hash).
			before, _ := r.GetLandingServer(1)
			out, err := r.LiftLandingScanBlock(21, "admin", true, now)
			if err != nil || out.Status != "active" || out.ScanBlockedAt != 0 {
				t.Fatalf("lift %+v %v", out, err)
			}
			if after, _ := r.GetLandingServer(1); after.Revision != before.Revision+1 {
				t.Fatalf("lift kept revision %d -> %d", before.Revision, after.Revision)
			}
			events, _, _ = r.ListLandingScanEvents(21, 50)
			if events[1].UnblockedBy != "admin" || events[1].UnblockedAt == 0 || events[0].UnblockedBy != "" {
				t.Fatalf("lift log %+v", events)
			}
			if _, err := r.LiftLandingScanBlock(21, "admin", true, now); !errors.Is(err, repo.ErrNotScanBlocked) {
				t.Fatalf("second lift %v", err)
			}

			// Retention: 40-day-old rows go, the table stays at 5000 rows.
			old := now.Add(-40 * 24 * time.Hour).UnixMilli()
			rows := make([]model.LandingScanEvent, 0, 5100)
			for i := 0; i < 5100; i++ {
				created := now.UnixMilli()
				if i < 50 {
					created = old
				}
				rows = append(rows, model.LandingScanEvent{LandingServerID: 2, Seq: int64(1000 + i), LandingNodeID: 22, Type: "total", CreatedTime: created})
			}
			if err := db.CreateInBatches(&rows, 500).Error; err != nil {
				t.Fatal(err)
			}
			if _, _, err := r.RecordLandingScanStatus(1, "on", []repo.LandingScanHit{hit("flvxl-22", seq+20, "total")}, now); err != nil {
				t.Fatal(err)
			}
			var count, stale int64
			db.Model(&model.LandingScanEvent{}).Count(&count)
			db.Model(&model.LandingScanEvent{}).Where("created_time < ?", now.Add(-30*24*time.Hour).UnixMilli()).Count(&stale)
			if count != 5000 || stale != 0 {
				t.Fatalf("retention count %d stale %d", count, stale)
			}
			if events, _, _ := r.ListLandingScanEvents(22, 1); len(events) != 1 || events[0].Seq != seq+20 {
				t.Fatal("newest event pruned")
			}
		})
	}
}
