package ws

import "testing"

// Plan 168: SetEntryRates revision (one object per node; an empty tree has
// none) and the node's entry rate mode from the handshake.

func TestEntryRatesRevision(t *testing.T) {
	tree := map[string]interface{}{"version": 1, "classes": []interface{}{map[string]interface{}{"id": "u7", "group": true, "rate_mbps": 20}}, "ports": []interface{}{map[string]interface{}{"port": 1501, "tcp": true, "udp": true, "class": "u7"}}}
	revs := ExpectedRuntimeRevs("SetEntryRates", tree)
	if len(revs) != 1 || revs["entryrate:entry"] == "" {
		t.Fatalf("revs %v", revs)
	}
	sent, _ := annotateRuntimeRevs("SetEntryRates", tree).(map[string]interface{})
	if sent[RuntimeRevKey] != revs["entryrate:entry"] {
		t.Fatalf("payload not tagged: %v", sent)
	}
	tree["ports"] = []interface{}{map[string]interface{}{"port": 1502, "tcp": true, "udp": true, "class": "u7"}}
	if ExpectedRuntimeRevs("SetEntryRates", tree)["entryrate:entry"] == revs["entryrate:entry"] {
		t.Fatal("revision ignores the ports")
	}
	empty := map[string]interface{}{"version": 1, "classes": []interface{}{}, "ports": []interface{}{}}
	if r := ExpectedRuntimeRevs("SetEntryRates", empty); len(r) != 0 {
		t.Fatalf("empty tree revs %v", r)
	}
	if sent, _ := annotateRuntimeRevs("SetEntryRates", empty).(map[string]interface{}); sent[RuntimeRevKey] != nil {
		t.Fatal("empty tree tagged")
	}
}

func TestEntryRateMode(t *testing.T) {
	for _, tc := range []struct {
		ns   *nodeSession
		want int
	}{
		{nil, EntryRateModeOld},
		{&nodeSession{}, EntryRateModeOld},
		{&nodeSession{entryRateTc: 1}, EntryRateModeTc},
		{&nodeSession{entryRateTc: 1, entryRateTcErr: "内核不支持 ifb"}, EntryRateModeKernel},
	} {
		if got := entryRateMode(tc.ns); got != tc.want {
			t.Errorf("%+v → %d, want %d", tc.ns, got, tc.want)
		}
	}
	s := &Server{nodes: map[int64]*nodeSession{1: {entryRateTc: 1}, 2: {entryRateTc: 1, entryRateTcErr: "x"}}}
	if !s.EntryRateTcCapable(1) || s.EntryRateTcCapable(2) || s.EntryRateTcCapable(3) {
		t.Fatal("EntryRateTcCapable")
	}
}
