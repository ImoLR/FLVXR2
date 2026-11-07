package landing

import (
	"encoding/json"
	"testing"
)

// Plan 231: the Core reports an IPv6 source as its /64 ("2001:db8::/64").
// Before, that string never parsed as an address, so an IPv6 relay (one of
// our nodes dialing over IPv6) counted as the user's own client IP.
func TestRecordGroupsMatchesIPv6RelayBySlash64(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	m.desired = userLimitsDesired(t) // relay sources 198.51.100.60, 2001:db8::60
	var snap coreSnapshot
	raw := `{"version":7,"started_at":"2026-10-28T00:00:00Z","management_groups":[{"group":"landing-user-7","inbound_current":7,"inbound_online_ips":[` +
		`{"ip":"2001:db8::/64","connections":3},{"ip":"198.51.100.60","connections":1},` +
		`{"ip":"2409:8a4c:5c4f:26e1::/64","connections":2},{"ip":"2409:8a4c:5c4f:26e2::7","connections":1}]}]}`
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatal(err)
	}
	m.recordGroups(snap)
	usage := m.GroupUsages()
	want := []string{"2409:8a4c:5c4f:26e1::/64", "2409:8a4c:5c4f:26e2::/64"}
	if len(usage) != 1 || usage[0].RelayConnections != 4 || usage[0].Connections != 3 || len(usage[0].ClientIPs) != 2 ||
		usage[0].ClientIPs[0] != want[0] || usage[0].ClientIPs[1] != want[1] {
		t.Fatalf("usage = %+v, want IPv6 relay /64 recognised, direct keys %v", usage, want)
	}
	if live := m.groupLive["landing-user-7"]; live.relayIPs != 2 || live.relayConns != 4 || live.seenIPs != 4 {
		t.Fatalf("live = %+v", live)
	}
}
