package socket

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Plan 310: the host report carries the LeiKwan client's own 全程 of each
// forward (status.json latest_tcp / active_udp, raw — the panel picks like
// 诊断), read in the same pass as state / policy; a changing rtt never makes
// a report due by itself.
func TestLeiKwanHostReadClientRtt(t *testing.T) {
	m := lkFixture(t, "aliyun-gz")
	now := m.now()
	ts := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339Nano) }
	write := func(id int, doc map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(doc)
		if err := os.WriteFile(filepath.Join(m.root, "run", "ultra", "forward-"+strconv.Itoa(id)+".status.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 263: UDP probes answered (11 ms) + TCP selection 107 ms (prod 11-26).
	write(263, map[string]any{"forward_id": 263, "state": "ready", "policy_state": "active", "udp_transport": "native_udp",
		"latest_tcp": map[string]any{"selection_ref": "a", "rtt_ms": 107, "selected_at": ts(30 * time.Second)},
		"active_udp": map[string]any{"selection_ref": "a", "rtt_ms": 11, "p95_rtt_ms": 12, "probe_sent": 30, "probe_received": 30, "selected_at": ts(6 * time.Second)}})
	// 264: the bogus active_udp seen on prod (60000, no probe counters).
	write(264, map[string]any{"forward_id": 264, "state": "ready", "policy_state": "active",
		"latest_tcp": map[string]any{"rtt_ms": 64, "selected_at": ts(9 * time.Second)},
		"active_udp": map[string]any{"rtt_ms": 60000, "selected_at": "not a time"}})
	_, st := m.read()
	if st.Rtt != lkHostRttVersion {
		t.Fatalf("rtt capability %d", st.Rtt)
	}
	by := map[int64]LKForward{}
	for _, f := range st.Forwards {
		by[f.ForwardID] = f
	}
	f := by[263]
	if f.State != "ready" || f.RttUDP == nil || f.RttUDP.RttMs != 11 || f.RttUDP.Sent != 30 || f.RttUDP.Recv != 30 || f.RttUDP.AgeS != 6 ||
		f.RttTCP == nil || f.RttTCP.RttMs != 107 || f.RttTCP.AgeS != 30 {
		t.Fatalf("263 %+v udp %+v tcp %+v", f, f.RttUDP, f.RttTCP)
	}
	// Raw on the wire: the 60000 stays for the panel to drop (one rule there).
	f = by[264]
	if f.RttUDP == nil || f.RttUDP.RttMs != 60000 || f.RttUDP.Sent != 0 || f.RttUDP.AgeS != -1 || f.RttTCP == nil || f.RttTCP.RttMs != 64 {
		t.Fatalf("264 udp %+v tcp %+v", f.RttUDP, f.RttTCP)
	}
	// Fixture status files without the fields: nothing invented.
	if f := by[279]; f.State == "" || f.RttUDP != nil || f.RttTCP != nil {
		t.Fatalf("279 %+v", f)
	}
	raw, _ := json.Marshal(by[263])
	var wire map[string]any
	_ = json.Unmarshal(raw, &wire)
	if _, ok := wire["rttUdp"].(map[string]any)["rttMs"]; !ok {
		t.Fatalf("wire %s", raw)
	}
	if _, ok := wire["rttTcp"]; !ok {
		t.Fatalf("wire %s", raw)
	}

	// Report key: rtt jitter alone keeps the key; a real state change moves it.
	r := &LeiKwanHostReport{V: leiKwanHostVersion, State: st}
	key := lkReportKey(r)
	st2 := st
	st2.Forwards = append([]LKForward(nil), st.Forwards...)
	for i := range st2.Forwards {
		if st2.Forwards[i].ForwardID == 263 {
			c := *st2.Forwards[i].RttUDP
			c.RttMs, c.AgeS = 13, 1
			st2.Forwards[i].RttUDP = &c
			st2.Forwards[i].RttTCP = nil
		}
	}
	if lkReportKey(&LeiKwanHostReport{V: leiKwanHostVersion, State: st2}) != key {
		t.Fatal("rtt change alone moved the report key")
	}
	if got := lkReportKey(r); got != key || by[263].RttUDP.RttMs != 11 {
		t.Fatal("lkReportKey mutated the report")
	}
	st2.Forwards[0].State = "degraded"
	if lkReportKey(&LeiKwanHostReport{V: leiKwanHostVersion, State: st2}) == key {
		t.Fatal("state change did not move the report key")
	}

	// tick(): a report that only differs in rtt is not due before the
	// cadence (traffic off here → lkReportEvery).
	m.tick(context.Background())
	if m.takeReport() == nil {
		t.Fatal("first report not due")
	}
	write(263, map[string]any{"forward_id": 263, "state": "ready", "policy_state": "active", "udp_transport": "native_udp",
		"active_udp": map[string]any{"rtt_ms": 14, "probe_sent": 30, "probe_received": 30, "selected_at": ts(time.Second)}})
	m.tick(context.Background())
	if m.takeReport() != nil {
		t.Fatal("rtt change alone made a report due")
	}
	m.mu.Lock()
	got := m.report
	m.mu.Unlock()
	for _, f := range got.State.Forwards {
		if f.ForwardID == 263 && (f.RttUDP == nil || f.RttUDP.RttMs != 14) {
			t.Fatalf("latest report not refreshed: %+v", f.RttUDP)
		}
	}
}
