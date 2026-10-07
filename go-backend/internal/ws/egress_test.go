package ws

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"go-backend/internal/store/repo"
)

func TestSystemInfoEgressOnlyWritesOnChange(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "egress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, sql := range []string{
		"INSERT INTO node(id,name,secret,server_ip,port,created_time,status) VALUES(1,'egress','secret','::1','1000',0,1)",
		"CREATE TABLE egress_writes(value TEXT)",
		"CREATE TRIGGER count_egress AFTER UPDATE OF egress_detected ON node BEGIN INSERT INTO egress_writes VALUES(new.egress_detected); END",
	} {
		if err := r.DB().Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := NewServer(r, "jwt")
	ns := &nodeSession{nodeID: 1}
	for _, family := range []string{"dual", "dual", "", "bad", "v4", "v4"} {
		raw, _ := json.Marshal(map[string]interface{}{"uptime": 1, "egress_ip_family": family})
		var info SystemInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			t.Fatal(err)
		}
		s.recordEgressDetection(ns, info.EgressIPFamily)
	}
	// A reconnect must not rewrite the same value or its timestamp.
	before, _ := r.GetNodeRecord(1)
	s.recordEgressDetection(&nodeSession{nodeID: 1}, "v4")
	after, _ := r.GetNodeRecord(1)
	var count int64
	r.DB().Table("egress_writes").Count(&count)
	if count != 2 || after.EgressDetected != "v4" || before.EgressDetectedAt != after.EgressDetectedAt || after.EgressDetectedAt == 0 {
		t.Fatalf("writes=%d before=%+v after=%+v", count, before, after)
	}
}
