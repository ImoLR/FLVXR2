package handler

import (
	"testing"
	"time"

	"go-backend/internal/probe"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 268: the 备线 picker shows 前置 → backup-entry latency only from data
// the panel already has (either edge direction), flags > 30 ms as far, and
// adds nothing when there is no data.
func TestLeiKwanBackupTunnelViewsLatency(t *testing.T) {
	h := &Handler{}
	h.probeInit().latency.Record([]probe.Edge{
		{From: 58, To: 2, MS: 12, OK: true}, // 百度云广州 → Mkcloud 深港ixp 1
		{From: 7, To: 60, MS: 41, OK: true}, // reverse direction only: entry 7 → 阿里广州
	}, time.Now())
	tunnels := []repo.LeiKwanTunnelBrief{
		{ID: 105, Name: "ixp1 端口转发", Type: 1, EntryNames: "Mkcloud 深港ixp 1", EntryNodeIDs: []int64{2}},
		{ID: 9, Name: "far", Type: 2, EntryNames: "Far、百度云广州", EntryNodeIDs: []int64{7, 58}},
		{ID: 11, Name: "none", Type: 1, EntryNames: "X", EntryNodeIDs: []int64{99}},
	}
	fronts := []leiKwanFront{{ID: 58, Name: "百度云广州"}, {ID: 60, Name: "阿里广州"}}
	views := h.leiKwanBackupTunnelViews(tunnels, fronts)
	if len(views[0].FrontLatency) != 1 || views[0].FrontLatency[0].MS != 12 || views[0].FrontLatency[0].Far || views[0].FrontLatency[0].EntryName != "Mkcloud 深港ixp 1" {
		t.Fatalf("105: %+v", views[0].FrontLatency)
	}
	got := map[[2]int64]leiKwanFrontLatency{}
	for _, l := range views[1].FrontLatency {
		got[[2]int64{l.FrontID, l.EntryID}] = l
	}
	if l := got[[2]int64{58, 58}]; !l.Same {
		t.Fatalf("front == entry should be same: %+v", views[1].FrontLatency)
	}
	if l := got[[2]int64{60, 7}]; l.MS != 41 || !l.Far || l.EntryName != "Far" {
		t.Fatalf("reverse edge / far: %+v", views[1].FrontLatency)
	}
	if len(views[2].FrontLatency) != 0 {
		t.Fatalf("no data must add nothing: %+v", views[2].FrontLatency)
	}
}

func TestLeiKwanPlanBackup(t *testing.T) {
	line := model.LeiKwanLine{ID: 1, BackupTunnelID: 90}
	backups := map[int64]repo.LeiKwanBackupForward{10: {ID: 10, TunnelID: 105}, 11: {ID: 11, TunnelID: 90}}
	cases := []struct {
		x    model.LeiKwanRule
		on   bool
		line model.LeiKwanLine
		want leiKwanBackupPlan
	}{
		{model.LeiKwanRule{BackupForwardID: 148, BackupMode: "linked"}, true, line, leiKwanBackupLinked},
		{model.LeiKwanRule{BackupForwardID: 10, BackupMode: "auto"}, true, line, leiKwanBackupMove},
		{model.LeiKwanRule{BackupForwardID: 11, BackupMode: "auto"}, true, line, leiKwanBackupKeep},
		{model.LeiKwanRule{BackupForwardID: 12, BackupMode: "auto"}, true, line, leiKwanBackupMove}, // backup forward gone
		{model.LeiKwanRule{}, true, line, leiKwanBackupCreate},
		{model.LeiKwanRule{}, false, line, leiKwanBackupKeep},
		{model.LeiKwanRule{BackupForwardID: 10, BackupMode: "auto"}, false, line, leiKwanBackupKeep},
		{model.LeiKwanRule{BackupForwardID: 10, BackupMode: "auto"}, true, model.LeiKwanLine{ID: 1}, leiKwanBackupRemove},
	}
	for i, c := range cases {
		if got := leiKwanPlanBackup(c.x, c.line, c.on, backups); got != c.want {
			t.Fatalf("case %d: got %d want %d", i, got, c.want)
		}
	}
}
