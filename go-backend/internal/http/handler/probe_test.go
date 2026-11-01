package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/probe"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// TestProbeSnapshotLiveData: online nodes carry the in-memory report, latency
// rows only point at shown nodes, and the bytes never hold a node's IP,
// secret, id or agent version (plan 187).
func TestProbeSnapshotLiveData(t *testing.T) {
	r, err := repo.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, n := range []model.Node{
		{ID: 41, Name: "东京", Region: "JP", Secret: "sec-41-xyz", ServerIP: "198.51.100.41", Port: "1-2", Status: 1, Version: sql.NullString{String: "3.0.27-fork.104 (debian/amd64)", Valid: true}},
		{ID: 42, Name: "香港", Region: "HK", Secret: "sec-42-xyz", ServerIP: "198.51.100.42", Port: "1-2", Status: 1},
		{ID: 43, Name: "隐藏的", Region: "SG", Secret: "sec-43-xyz", ServerIP: "198.51.100.43", Port: "1-2", Status: 1},
	} {
		n := n
		if err := r.DB().Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UnixMilli()
	for k, v := range map[string]string{"login_monitor_link": "true", "probe_nodes": `{"hidden":[43],"names":{}}`} {
		if err := r.UpsertConfig(k, v, now); err != nil {
			t.Fatal(err)
		}
	}
	h := New(r, "jwt-secret-probe", "test")
	h.probeInit().online = func(id int64) bool { return id != 42 }
	h.probeObserve(41, probe.Sample{At: now, CPU: 33.33, Mem: 61.2, Disk: 10, Load1: 0.5, NetIn: 5000, NetOut: 7000, RxTotal: 1 << 40, TxTotal: 1 << 39, Uptime: 86400 * 3, TCP: 120})
	h.probeObserve(42, probe.Sample{At: now - 600_000, CPU: 99})
	h.probeObserve(43, probe.Sample{At: now, CPU: 1})
	h.probeRecordLatency([]pathLatencyPlan{{Layers: [][]pathLatencyEdge{{
		{From: 41, To: 42, Key: pathProbeKey{From: 41, Host: "198.51.100.42", Port: 443}},
		{From: 41, To: 43, Key: pathProbeKey{From: 41, Host: "198.51.100.43", Port: 443}},
		{From: 41, To: 42, Key: pathProbeKey{From: 41, Host: "198.51.100.42", Port: 8443}},
	}}}}, map[pathProbeKey]pathProbeResult{
		{From: 41, Host: "198.51.100.42", Port: 443}:  {LatencyMS: 48.26, OK: true},
		{From: 41, Host: "198.51.100.42", Port: 8443}: {LatencyMS: 41.04, OK: true},
		{From: 41, Host: "198.51.100.43", Port: 443}:  {LatencyMS: 12, OK: true},
	}, time.Now())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/probe/snapshot", nil)
	w := httptest.NewRecorder()
	h.probeSnapshot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("snapshot %d %s", w.Code, w.Body.String())
	}
	body := w.Body.Bytes()
	for _, bad := range []string{"198.51.100.", "sec-4", "fork.104", `"id"`, "隐藏的", h.probe.keyer.Key(43), `"41"`, `"42"`} {
		if bytes.Contains(body, []byte(bad)) {
			t.Fatalf("snapshot leaks %q: %s", bad, body)
		}
	}
	var env struct {
		Data probe.Snapshot `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	s := env.Data
	if len(s.Nodes) != 2 || !s.LatencyOn {
		t.Fatalf("nodes %+v", s.Nodes)
	}
	tokyo, hk := s.Nodes[0], s.Nodes[1]
	if !tokyo.Online || tokyo.CPU != 33.3 || tokyo.TCP != 120 || tokyo.Uptime != 86400*3 || tokyo.OS != "debian" {
		t.Fatalf("tokyo %+v", tokyo)
	}
	if len(tokyo.Latency) != 1 || tokyo.Latency[0].To != hk.K || tokyo.Latency[0].MS != 41 || !tokyo.Latency[0].OK {
		t.Fatalf("tokyo latency %+v (want one row → 香港 41 ms, hidden target dropped)", tokyo.Latency)
	}
	if hk.Online || hk.CPU != 0 || hk.Seen != now-600_000 {
		t.Fatalf("offline hk %+v", hk)
	}

	// The detail series carries the same latency ring, hidden targets dropped.
	sreq := httptest.NewRequest(http.MethodGet, "/api/v1/probe/series?k="+tokyo.K+"&range=1h", nil)
	sw := httptest.NewRecorder()
	h.probeSeries(sw, sreq)
	if sw.Code != http.StatusOK || strings.Contains(sw.Body.String(), h.probe.keyer.Key(43)) {
		t.Fatalf("series %d %s", sw.Code, sw.Body.String())
	}
	var senv struct {
		Data probe.Series `json:"data"`
	}
	_ = json.Unmarshal(sw.Body.Bytes(), &senv)
	if len(senv.Data.Latency) != 1 || senv.Data.Latency[0].To != hk.K || len(senv.Data.Latency[0].Points) != 1 || senv.Data.Latency[0].Points[0][1] != 41 {
		t.Fatalf("series latency %+v", senv.Data.Latency)
	}

	// Old public stream mapping: opaque number, hidden node refused.
	if id, ok := h.probePublicNode(41); !ok || id == 41 {
		t.Fatalf("public id %d %v", id, ok)
	}
	if _, ok := h.probePublicNode(43); ok {
		t.Fatal("hidden node visible on the old public stream")
	}
}
