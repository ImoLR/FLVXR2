package repo

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

// Plan 230: a NAT node's egress history (node 48 Leikwanhost 沪台IPLC leaves
// through 103.246.219.39 or 114.35.6.24 per flow).
func TestMergeEgressHistory(t *testing.T) {
	now := time.Unix(1_793_980_000, 0)
	ips := func(raw string) map[string]int64 {
		t.Helper()
		out := map[string]int64{}
		for _, e := range parseEgressHistory(raw) {
			out[e.IP] = e.At
		}
		return out
	}

	raw, changed := mergeEgressHistory("", []string{"103.246.219.39", ""}, now)
	if !changed || !reflect.DeepEqual(ips(raw), map[string]int64{"103.246.219.39": now.UnixMilli()}) {
		t.Fatalf("first report: %q %v", raw, changed)
	}
	// The other egress joins; the first one stays.
	later := now.Add(2 * time.Hour)
	raw, changed = mergeEgressHistory(raw, []string{"114.35.6.24", "240e:b8f:737:1204:ca:8aff:fec0:2813"}, later)
	if !changed || len(ips(raw)) != 3 || ips(raw)["103.246.219.39"] != now.UnixMilli() {
		t.Fatalf("second report: %q %v", raw, changed)
	}
	// A known address within 6 h: no write.
	if again, changed := mergeEgressHistory(raw, []string{"103.246.219.39"}, now.Add(5*time.Hour)); changed || again != raw {
		t.Fatalf("refresh within 6 h rewrote: %q", again)
	}
	// After 6 h it is refreshed once.
	refreshed, changed := mergeEgressHistory(raw, []string{"103.246.219.39"}, now.Add(7*time.Hour))
	if !changed || ips(refreshed)["103.246.219.39"] != now.Add(7*time.Hour).UnixMilli() {
		t.Fatalf("refresh after 6 h: %q %v", refreshed, changed)
	}
	// Private / CGNAT / garbage reports never enter.
	if same, changed := mergeEgressHistory(raw, []string{"10.3.100.20", "100.64.1.1", "fe80::1", "nope"}, later); changed || same != raw {
		t.Fatalf("non-public report changed history: %q", same)
	}
	// Expiry: 14 days unseen drops the address (one write), the others stay.
	exp, changed := mergeEgressHistory(refreshed, nil, now.Add(EgressHistoryTTL+3*time.Hour))
	if !changed || !reflect.DeepEqual(sortedKeys(ips(exp)), []string{"103.246.219.39"}) {
		t.Fatalf("expiry: %q %v", exp, changed)
	}
	if gone, changed := mergeEgressHistory(exp, nil, now.Add(3*EgressHistoryTTL)); !changed || gone != "" {
		t.Fatalf("all expired: %q %v", gone, changed)
	}
	// Bound: at most 4 per family, newest kept.
	b := ""
	for i := 0; i < 6; i++ {
		b, _ = mergeEgressHistory(b, []string{"203.0.114." + string(rune('1'+i))}, now.Add(time.Duration(i)*time.Minute))
	}
	got := sortedKeys(ips(b))
	if !reflect.DeepEqual(got, []string{"203.0.114.3", "203.0.114.4", "203.0.114.5", "203.0.114.6"}) {
		t.Fatalf("bound kept %v", got)
	}
	// Corrupt or oversized JSON is treated as empty.
	if r, _ := mergeEgressHistory("{bad", []string{"203.0.114.9"}, now); !reflect.DeepEqual(sortedKeys(ips(r)), []string{"203.0.114.9"}) {
		t.Fatalf("corrupt history: %q", r)
	}
	// Output is canonical (sorted by address) so equal sets compare equal.
	var list []egressSeen
	if err := json.Unmarshal([]byte(raw), &list); err != nil || !sort.SliceIsSorted(list, func(i, j int) bool { return list[i].IP < list[j].IP }) {
		t.Fatalf("not canonical: %q %v", raw, err)
	}
}

func TestEgressHistoryIPs(t *testing.T) {
	now := time.Unix(1_793_980_000, 0)
	hist, _ := mergeEgressHistory("", []string{"114.35.6.24"}, now.Add(-time.Hour))
	old, _ := mergeEgressHistory("", []string{"203.0.114.7"}, now.Add(-EgressHistoryTTL-time.Hour))
	var a, b []egressSeen
	_ = json.Unmarshal([]byte(hist), &a)
	_ = json.Unmarshal([]byte(old), &b)
	raw, _ := json.Marshal(append(a, b...))
	n := model.Node{ReportedIPV4: "103.246.219.39", ReportedIPV6: "240e:b8f:737:1204:ca:8aff:fec0:2813", ReportedIPHistory: string(raw)}
	got := EgressHistoryIPs(n, now)
	want := []string{"103.246.219.39", "114.35.6.24", "240e:b8f:737:1204:ca:8aff:fec0:2813"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := EgressHistoryIPs(model.Node{ReportedIPV4: "10.0.0.1"}, now); len(got) != 0 {
		t.Fatalf("private reported address listed: %v", got)
	}
	// Relay IP slots: the larger family, at least one.
	if s := relaySlotsFor(got); s != 2 {
		t.Fatalf("slots %d want 2", s)
	}
	if s := relaySlotsFor(nil); s != 1 {
		t.Fatalf("slots %d want 1", s)
	}
}

func TestFillNodePublicIPsKeepsEgressHistory(t *testing.T) {
	r := orphanTestRepo(t, "sqlite")
	orphanSeed(t, r, &model.Node{ID: 748, Name: "Leikwanhost 沪台IPLC", ServerIP: "leikwan-hutai-ct.imgamer.top", ServerIPV6: ns("leikwan-hutai-ct.imgamer.top"), DDNSEnabled: true, Port: "10000-20000", Status: 1})
	load := func() model.Node {
		t.Helper()
		var n model.Node
		if err := r.db.First(&n, 748).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, v4 := range []string{"103.246.219.39", "114.35.6.24", "103.246.219.39"} {
		if filled, err := r.FillNodePublicIPs(748, v4, "240e:b8f:737:1204:ca:8aff:fec0:2813"); err != nil || len(filled) != 0 {
			t.Fatalf("report %s: %v %v", v4, filled, err)
		}
	}
	n := load()
	if n.ReportedIPV4 != "103.246.219.39" || n.ServerIP != "leikwan-hutai-ct.imgamer.top" {
		t.Fatalf("node fields: %+v", n)
	}
	got := EgressHistoryIPs(n, time.Now())
	want := []string{"103.246.219.39", "114.35.6.24", "240e:b8f:737:1204:ca:8aff:fec0:2813"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("history %v want %v (raw %q)", got, want, n.ReportedIPHistory)
	}
	// The same report again within 6 h leaves the stored history untouched.
	before := n.ReportedIPHistory
	if _, err := r.FillNodePublicIPs(748, "114.35.6.24", ""); err != nil || load().ReportedIPHistory != before {
		t.Fatalf("rewrote history: %v %q", err, load().ReportedIPHistory)
	}
}

func TestLandingHostInventoryIncludesEgress(t *testing.T) {
	r := orphanTestRepo(t, "sqlite")
	now := time.Now()
	hist, _ := mergeEgressHistory("", []string{"103.246.219.39", "114.35.6.24"}, now.Add(-time.Hour))
	stale, _ := mergeEgressHistory("", []string{"203.0.114.77"}, now.Add(-EgressHistoryTTL-time.Hour))
	orphanSeed(t, r,
		&model.Node{ID: 748, Name: "Leikwanhost 沪台IPLC", ServerIP: "leikwan-hutai-ct.imgamer.top", ServerIPV6: ns("leikwan-hutai-ct.imgamer.top"), DDNSEnabled: true, Port: "10000-20000", Status: 1, ReportedIPV4: "114.35.6.24", ReportedIPHistory: hist},
		&model.Node{ID: 710, Name: "native", ServerIP: "203.0.114.10", Port: "10000-20000", Status: 1, ReportedIPV4: "203.0.114.10", ReportedIPHistory: stale},
		&model.Node{ID: 711, Name: "remote", ServerIP: "203.0.114.20", Port: "10000-20000", Status: 1, IsRemote: 1, ReportedIPV4: "203.0.114.21"},
		&model.LandingServer{ID: 9, Source: "native", NodeID: 710, Name: "native", PublicHost: "203.0.114.10"},
	)
	inv, err := r.landingHostInventoryAt(now)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(inv.Nodes)
	sort.Strings(inv.Native)
	wantNodes := []string{"103.246.219.39", "114.35.6.24", "203.0.114.10", "leikwan-hutai-ct.imgamer.top"}
	if !reflect.DeepEqual(inv.Nodes, wantNodes) {
		t.Fatalf("nodes %v want %v", inv.Nodes, wantNodes)
	}
	if !reflect.DeepEqual(inv.Native, []string{"203.0.114.10"}) {
		t.Fatalf("native %v (egress must not count as a native landing host)", inv.Native)
	}
}

func sortedKeys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
