package contract_test

import (
	"errors"
	"fmt"
	"strings"
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

// Plan 205 on SQLite and PostgreSQL: a share's hits go through the same
// records (shared source), its desired carries the native scan_guard object
// only while the share advertises it (in the content hash), a user with an
// open block cannot create (also after deleting the node, also via the rule
// page's auto-create) while an admin can, retention never drops an open block,
// and 解除禁用 from the record ends the restriction.
func TestLandingScanSharedAndUserBlockContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			db := r.DB()
			now := time.Now()
			caps := `{"server_id":7,"name":"Dmit T1","hosts":{"ipv4":"203.0.113.7"},"status":"active","port_min":30000,"port_max":30010,"protocols":["ss2022"]}`
			for _, v := range []interface{}{
				&model.User{ID: 2, User: "alice", Pwd: "x", RoleID: 1, Status: 1, Flow: -1},
				&model.User{ID: 3, User: "bob", Pwd: "x", RoleID: 1, Status: 1, Flow: -1},
				&model.LandingServer{ID: 5, Source: "shared", RemoteServerID: 7, Name: "Dmit T1", PublicHost: "203.0.113.7", PortMin: 30000, PortMax: 30010, FlowType: 2, TrafficRatio: 1, Status: "active",
					Protocols: `["ss2022"]`, SharedCaps: caps, MaxNodesPerUser: 8, Revision: 3, CreatedTime: 1, UpdatedTime: 1},
				&model.LandingServerUser{LandingServerID: 5, UserID: 2},
				&model.LandingServerUser{LandingServerID: 5, UserID: 3},
				&model.LandingNode{ID: 31, UserID: 2, LandingServerID: 5, Name: "dmit-1", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-31", Status: "active", CreatedTime: 1, UpdatedTime: 1},
				&model.LandingNode{ID: 32, UserID: 3, LandingServerID: 5, Name: "dmit-2", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-32", Status: "active", CreatedTime: 1, UpdatedTime: 1},
			} {
				if err := db.Create(v).Error; err != nil {
					t.Fatal(err)
				}
			}
			if dialect == "postgres" {
				db.Exec("SELECT setval(pg_get_serial_sequence('landing_node','id'), 100)")
			}

			// Without the capability: no scan_guard, hash input unchanged.
			d, err := r.SharedLandingDesired(5, nil)
			if err != nil || d.ScanGuard != nil {
				t.Fatalf("desired without capability %+v %v", d.ScanGuard, err)
			}
			rev := d.Revision
			// Advertised: native object, identities = every node of the desired.
			if err := db.Model(&model.LandingServer{}).Where("id = 5").Update("shared_caps", strings.TrimSuffix(caps, "}")+`,"scan_guard":{"versions":[1]}}`).Error; err != nil {
				t.Fatal(err)
			}
			d, err = r.SharedLandingDesired(5, nil)
			if err != nil || d.ScanGuard == nil || d.Revision != rev+1 || d.ScanGuard["enabled"] != true || strings.Join(d.ScanGuard["identities"].([]string), ",") != "flvxl-31,flvxl-32" || d.ScanGuard["seen"].(int64) != 0 {
				t.Fatalf("desired with capability %+v rev %d→%d %v", d.ScanGuard, rev, d.Revision, err)
			}
			if again, _ := r.SharedLandingDesired(5, nil); again.Revision != d.Revision {
				t.Fatal("unchanged scan_guard moved the revision")
			}

			// A relayed hit: node disabled, seen mark, the next desired moves.
			const seq = int64(1793709225987084)
			hit := repo.LandingScanHit{ID: 31, InboundTag: "flvxl-31", Type: "horizontal", Count: 100, Port: 22, WindowS: 60, At: now, Seq: seq}
			notices, advanced, err := r.RecordLandingScanStatus(5, "on", []repo.LandingScanHit{hit}, now)
			if err != nil || len(notices) != 1 || !advanced || notices[0].NodeID != 31 || notices[0].UserID != 2 {
				t.Fatalf("shared record %+v %v %v", notices, advanced, err)
			}
			if n, _ := r.GetLandingNode(31); n.Status != "disabled" || n.ScanBlockedAt == 0 {
				t.Fatalf("shared node not disabled %+v", n)
			}
			d2, _ := r.SharedLandingDesired(5, nil)
			if d2.Revision <= d.Revision || d2.ScanGuard["seen"].(int64) != seq {
				t.Fatalf("seen not in desired %+v", d2.ScanGuard)
			}

			// Owner restricted (create + rule auto-create); others and admins not.
			const want = "你的落地节点因检测到端口扫描（同一端口 22 在 60 秒内连接了 100 个不同 IP）已被禁用，暂不能新建落地节点，如有疑问请联系管理员"
			if b, err := r.LandingScanUserBlock(2); err != nil || b == nil || b.Seq != seq {
				t.Fatalf("user block %+v %v", b, err)
			}
			if _, err := r.CreateLandingNode(2, 5, "again", "ss2022", false); err == nil || err.Error() != want {
				t.Fatalf("owner create: %v", err)
			}
			if _, _, err := r.ReuseOrCreateLandingNode(2, 5, "规则落地", "ss2022", false); err == nil || err.Error() != want {
				t.Fatalf("owner auto-create: %v", err)
			}
			if _, err := r.CreateLandingNode(3, 5, "bob-2", "ss2022", false); err != nil {
				t.Fatalf("other user: %v", err)
			}
			byAdmin, err := r.CreateLandingNode(2, 5, "by-admin", "ss2022", true)
			if err != nil {
				t.Fatalf("admin for the owner: %v", err)
			}

			// Deleting the blocked node keeps the record and the restriction;
			// retention keeps an open block even when it is old.
			if err := r.DeleteLandingNode(31); err != nil {
				t.Fatal(err)
			}
			if _, err := r.CreateLandingNode(2, 5, "after-delete", "ss2022", false); err == nil || err.Error() != want {
				t.Fatalf("owner create after delete: %v", err)
			}
			if n, err := r.LandingScanDeletedOpen(); err != nil || n != 1 {
				t.Fatalf("deleted open %d %v", n, err)
			}
			old := now.Add(-40 * 24 * time.Hour).UnixMilli()
			db.Model(&model.LandingScanEvent{}).Where("landing_node_id = 31").Update("created_time", old)
			if _, _, err := r.RecordLandingScanStatus(5, "on", []repo.LandingScanHit{{InboundTag: fmt.Sprintf("flvxl-%d", byAdmin.ID), Type: "total", Count: 1000, WindowS: 300, At: now, Seq: seq + 1}}, now); err != nil {
				t.Fatal(err)
			}
			var kept int64
			db.Model(&model.LandingScanEvent{}).Where("landing_node_id = 31").Count(&kept)
			if kept != 1 {
				t.Fatal("retention dropped an open block")
			}
			// Lift both: the deleted node's record, then the admin-made node.
			events, _, _ := r.ListLandingScanEvents(31, 5)
			if n, err := r.LiftLandingScanEvent(events[0].ID, "admin", now); err != nil || n != nil {
				t.Fatalf("lift deleted %+v %v", n, err)
			}
			if _, err := r.LiftLandingScanEvent(events[0].ID, "admin", now); !errors.Is(err, repo.ErrScanEventNotOpen) {
				t.Fatalf("second lift %v", err)
			}
			if _, err := r.CreateLandingNode(2, 5, "still", "ss2022", false); err == nil {
				t.Fatal("owner created while the admin-made node is blocked")
			}
			events, _, _ = r.ListLandingScanEvents(byAdmin.ID, 5)
			if n, err := r.LiftLandingScanEvent(events[0].ID, "admin", now); err != nil || n == nil || n.Status != "active" {
				t.Fatalf("lift existing %+v %v", n, err)
			}
			if b, _ := r.LandingScanUserBlock(2); b != nil {
				t.Fatalf("still blocked %+v", b)
			}
			if _, err := r.CreateLandingNode(2, 5, "free", "ss2022", false); err != nil {
				t.Fatalf("owner create after lift: %v", err)
			}
			// Old closed records are pruned again.
			if _, _, err := r.RecordLandingScanStatus(5, "on", []repo.LandingScanHit{{InboundTag: "flvxl-32", Type: "total", Count: 1000, WindowS: 300, At: now, Seq: seq + 2}}, now); err != nil {
				t.Fatal(err)
			}
			db.Model(&model.LandingScanEvent{}).Where("landing_node_id = 31").Count(&kept)
			if kept != 0 {
				t.Fatal("closed old record kept")
			}
		})
	}
}
