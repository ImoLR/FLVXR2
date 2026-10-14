package ws

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func TestIPv6RAChangeOnlyPersistence(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "ra.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, sql := range []string{
		"INSERT INTO node(id,name,secret,server_ip,port,created_time,status) VALUES(1,'ra','secret','::1','1000',0,1)",
		"CREATE TABLE ra_writes(value TEXT)",
		"CREATE TRIGGER count_ra AFTER UPDATE OF ipv6_ra_status ON node BEGIN INSERT INTO ra_writes VALUES(new.ipv6_ra_status); END",
	} {
		if err := r.DB().Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := NewServer(r, "jwt")
	record := func(ns *nodeSession, status, detail string) { s.recordIPv6RA(ns, &status, detail) }
	ns := &nodeSession{nodeID: 1}
	var info SystemInfo
	if err := json.Unmarshal([]byte(`{"uptime":1}`), &info); err != nil {
		t.Fatal(err)
	}
	s.recordIPv6RA(ns, info.IPv6RAStatus, info.IPv6RADetail)
	items, err := r.ListNodes(nil)
	if err != nil || items[0]["ipv6RaStatus"] != "" || items[0]["ipv6RaCheckedAt"] != int64(0) {
		t.Fatalf("old agent defaults: %v %v", items, err)
	}
	for _, report := range [][2]string{{"warn", "等待 RA"}, {"warn", "等待 RA"}, {"", ""}, {"bad", ""}, {"ok", "eth0=2"}, {"ok", "eth0=2;eth1=2"}} {
		record(ns, report[0], report[1])
	}
	var before, after model.Node
	r.DB().First(&before, 1)
	record(&nodeSession{nodeID: 1}, "ok", "eth0=2;eth1=2")
	r.DB().First(&after, 1)
	var count int64
	r.DB().Table("ra_writes").Count(&count)
	if count != 4 || after.IPv6RAStatus != "ok" || after.IPv6RADetail != "eth0=2;eth1=2" || after.IPv6RACheckedAt == 0 || after.IPv6RACheckedAt != before.IPv6RACheckedAt {
		t.Fatalf("writes=%d before=%+v after=%+v", count, before, after)
	}
	items, err = r.ListNodes(nil)
	if err != nil || items[0]["ipv6RaStatus"] != after.IPv6RAStatus || items[0]["ipv6RaDetail"] != after.IPv6RADetail || items[0]["ipv6RaCheckedAt"] != after.IPv6RACheckedAt {
		t.Fatalf("list: %v %v", items, err)
	}
	record(ns, "error", strings.Repeat("测", 600))
	r.DB().First(&after, 1)
	if len([]rune(after.IPv6RADetail)) != 300 {
		t.Fatal("detail not bounded")
	}
	// Old agents omit the field and must not clear an existing observation.
	s.recordIPv6RA(&nodeSession{nodeID: 1}, info.IPv6RAStatus, info.IPv6RADetail)
	r.DB().First(&after, 1)
	if after.IPv6RAStatus != "error" {
		t.Fatal("old agent cleared RA state")
	}
	// The first empty report after reconnect must clear stale persisted errors.
	if err := json.Unmarshal([]byte(`{"ipv6_ra_status":""}`), &info); err != nil {
		t.Fatal(err)
	}
	reconnected := &nodeSession{nodeID: 1}
	s.recordIPv6RA(reconnected, info.IPv6RAStatus, info.IPv6RADetail)
	r.DB().First(&after, 1)
	if after.IPv6RAStatus != "" || after.IPv6RADetail != "" {
		t.Fatal("explicit empty did not clear RA state")
	}
	for i := 0; i < 3; i++ {
		s.recordIPv6RA(reconnected, info.IPv6RAStatus, info.IPv6RADetail)
	}
	r.DB().Table("ra_writes").Count(&count)
	if count != 6 {
		t.Fatalf("empty report wrote repeatedly: %d", count)
	}
	items, err = r.ListNodes(nil)
	if err != nil || items[0]["ipv6RaStatus"] != "" {
		t.Fatalf("node list retained error: %v %v", items, err)
	}
}

func TestIPv6RADetailNotBroadcast(t *testing.T) {
	for _, raw := range []string{
		`{"uptime":1,"ipv6_ra_status":"error","ipv6_ra_detail":"eth0 manager"}`,
		`{"uptime":1,"ipv6_ra_detail":"eth0 manager","quotaGroups":[]}`,
	} {
		filtered := metricDataForBroadcast([]byte(raw))
		if strings.Contains(filtered, "ipv6_ra_") || strings.Contains(filtered, "quotaGroups") || !strings.Contains(filtered, `"uptime":1`) {
			t.Fatal(filtered)
		}
	}
}
