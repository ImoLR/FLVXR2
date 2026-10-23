package repo

import (
	"testing"
	"time"

	"go-backend/internal/store/model"
)

// Plan 139: prod state of shared node 103 — removed + ACKed, but Custom keeps
// reporting a frozen connections=29 — must finalize after a bounded grace,
// while in-use, unACKed and referenced nodes stay.
func TestSharedLandingFinalizeStaleConnections(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			now := time.Now().UnixMilli()
			server := &model.LandingServer{ID: 6, Source: "shared", RemoteServerID: 16, Name: "PQS TW", PublicHost: "198.51.100.16", PortMin: 30000, PortMax: 30999, FlowType: 2, TrafficRatio: 1, DefaultExpiryMode: "follow_user", MaxNodesPerUser: 3, Protocols: `["ss2022"]`, Status: "active", Revision: 35, SharedPushedRevision: 35, SharedAppliedRevision: 35, CreatedTime: 1, UpdatedTime: 1, SharedCaps: `{"server_id":16,"status":"active","port_min":30000,"port_max":30999,"protocols":["ss2022"]}`}
			pending := func(id int64, port int, remote string, conns int, updated int64) *model.LandingNode {
				return &model.LandingNode{ID: id, UserID: 8, LandingServerID: 6, Name: "n", Protocol: "ss2022", Port: port, InboundTag: "flvxl-x", Status: "disabled", DeletePending: true, RemoteState: remote, Connections: conns, LastCounterAt: now, CreatedTime: 1, UpdatedTime: updated}
			}
			n103 := pending(103, 30000, "removed", 29, now-10*1000)
			n103.InboundTag = "flvxl-103"
			n105 := &model.LandingNode{ID: 105, UserID: 8, LandingServerID: 6, Name: "🇨🇳 HiNet", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-105", Status: "active", RemoteState: "active", Connections: 39, LastCounterAt: now, CreatedTime: 1, UpdatedTime: 1}
			n106 := pending(106, 30002, "removed", 29, now-time.Hour.Milliseconds())
			n106.InboundTag = "flvxl-106"
			n107 := pending(107, 30003, "active", 0, now-time.Hour.Milliseconds())
			n107.InboundTag = "flvxl-107"
			orphanSeed(t, r, server, n103, n105, n106, n107, &model.Forward{ID: 156, UserID: 8, Name: "TW", LandingNodeID: 106})
			exists := func(id int64) bool {
				var c int64
				r.db.Model(&model.LandingNode{}).Where("id = ?", id).Count(&c)
				return c == 1
			}
			if err := r.FinalizeSharedLandingDeletions(6); err != nil {
				t.Fatal(err)
			}
			if !exists(103) {
				t.Fatal("finalized inside the grace with connections reported")
			}
			// Grace over (deleted 4 min ago, still 29 reported) → finalized.
			r.db.Model(&model.LandingNode{}).Where("id = 103").Update("updated_time", now-4*time.Minute.Milliseconds())
			// The 妙妙屋 side has not applied the latest revision yet → wait.
			r.db.Model(&model.LandingServer{}).Where("id = 6").Update("shared_applied_revision", 34)
			if err := r.FinalizeSharedLandingDeletions(6); err != nil || !exists(103) {
				t.Fatalf("finalized before ACK: %v", err)
			}
			r.db.Model(&model.LandingServer{}).Where("id = 6").Update("shared_applied_revision", 35)
			if err := r.FinalizeSharedLandingDeletions(6); err != nil {
				t.Fatal(err)
			}
			if exists(103) {
				t.Fatal("stale connections still block the deletion")
			}
			if !exists(105) || !exists(106) || !exists(107) {
				t.Fatalf("finalized a node that must stay: 105=%v 106=%v 107=%v", exists(105), exists(106), exists(107))
			}
			var kept model.LandingNode
			r.db.First(&kept, 105)
			if kept.Status != "active" || kept.Connections != 39 || LandingLiveConnections(&kept) != 39 {
				t.Fatalf("node 105 changed: %+v", kept)
			}
			if LandingLiveConnections(n106) != 0 {
				t.Fatal("removed node still counts connections")
			}
		})
	}
}
