package ws

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 265: a LeiKwan port the panel already uses on that node is logged
// once per node/port per window; nothing is moved.
func TestLeiKwanConflictLogOncePerWindow(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "lkc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, v := range []interface{}{
		&model.Node{ID: 61, Name: "火山上海", Secret: "s61", ServerIP: "198.51.100.61", Port: "10000-65535", Status: 1},
		&model.ForwardPort{ForwardID: 9, NodeID: 61, Port: 10015},
	} {
		if err := r.DB().Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	s := NewServer(r, "jwt")
	report := &leiKwanReport{Status: "running", Forwards: []model.LeiKwanDetectedForward{{ForwardID: 264, ListenPort: 10015, Running: true}, {ForwardID: 263, ListenPort: 10018, Running: true}}}
	s.recordLeiKwan(&nodeSession{nodeID: 61}, report)
	s.recordLeiKwan(&nodeSession{nodeID: 61}, report) // reconnect: same list, logged already
	out := buf.String()
	if strings.Count(out, "port 10015") != 1 || strings.Contains(out, "port 10018") {
		t.Fatalf("log=%q", out)
	}
	var fp model.ForwardPort
	r.DB().Where("forward_id = 9").First(&fp)
	if fp.Port != 10015 {
		t.Fatal("existing rule moved")
	}
	now := time.Now()
	if got := leiKwanConflictsToLog(62, []int{1, 2}, now); len(got) != 2 {
		t.Fatal(got)
	}
	if got := leiKwanConflictsToLog(62, []int{1, 2, 3}, now.Add(time.Hour)); len(got) != 1 || got[0] != 3 {
		t.Fatal(got)
	}
	if got := leiKwanConflictsToLog(62, []int{1}, now.Add(leiKwanConflictLogEvery+time.Minute)); len(got) != 1 {
		t.Fatal(got)
	}
}
