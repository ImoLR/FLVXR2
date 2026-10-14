package ws

import (
	"encoding/json"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeiKwanChangeOnlyPersistence(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "lk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, sql := range []string{"INSERT INTO node(id,name,secret,server_ip,port,created_time,status) VALUES(1,'lk','secret','127.0.0.1','1000',0,1)", "CREATE TABLE lk_writes(value TEXT)", "CREATE TRIGGER count_lk AFTER UPDATE OF leikwan_status ON node BEGIN INSERT INTO lk_writes VALUES(new.leikwan_status); END"} {
		if err := r.DB().Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := NewServer(r, "jwt")
	ns := &nodeSession{nodeID: 1}
	s.recordLeiKwan(ns, nil)
	items, _ := r.ListNodes(nil)
	if items[0]["leikwanStatus"] != "" || items[0]["leikwanCheckedAt"] != int64(0) {
		t.Fatal(items)
	}
	var info SystemInfo
	if err := json.Unmarshal([]byte(`{"leikwan":{"status":"running","token":"must-discard","forwards":[{"forwardId":264,"listenPort":10015,"running":true,"token":"discard"},{"forwardId":263,"listenPort":10018,"running":true}]}}`), &info); err != nil {
		t.Fatal(err)
	}
	s.recordLeiKwan(ns, info.LeiKwan)
	s.recordLeiKwan(ns, info.LeiKwan)
	var before, after model.Node
	r.DB().First(&before, 1)
	info.LeiKwan.Forwards[0], info.LeiKwan.Forwards[1] = info.LeiKwan.Forwards[1], info.LeiKwan.Forwards[0]
	s.recordLeiKwan(&nodeSession{nodeID: 1}, info.LeiKwan)
	r.DB().First(&after, 1)
	var count int64
	r.DB().Table("lk_writes").Count(&count)
	if count != 1 || before.LeiKwanCheckedAt != after.LeiKwanCheckedAt || after.LeiKwanCheckedAt == 0 || strings.Contains(after.LeiKwanForwards, "token") {
		t.Fatalf("writes=%d before=%s after=%s", count, before.LeiKwanForwards, after.LeiKwanForwards)
	}
	info.LeiKwan.Forwards[0].Running = false
	s.recordLeiKwan(ns, info.LeiKwan)
	s.recordLeiKwan(ns, &leiKwanReport{Status: "absent"})
	s.recordLeiKwan(ns, nil)
	s.recordLeiKwan(ns, &leiKwanReport{Status: "invalid"})
	r.DB().First(&after, 1)
	r.DB().Table("lk_writes").Count(&count)
	if count != 3 || after.LeiKwanStatus != "absent" || after.LeiKwanForwards != "[]" {
		t.Fatalf("writes=%d status=%s forwards=%s", count, after.LeiKwanStatus, after.LeiKwanForwards)
	}
}
func TestLeiKwanNotBroadcast(t *testing.T) {
	filtered := metricDataForBroadcast([]byte(`{"uptime":1,"leikwan":{"status":"running","forwards":[{"forwardId":263,"listenPort":10018}]}}`))
	if strings.Contains(filtered, "leikwan") || strings.Contains(filtered, "10018") || !strings.Contains(filtered, `"uptime":1`) {
		t.Fatal(filtered)
	}
}
