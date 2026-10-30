package handler

import (
	"context"
	"database/sql"
	"net"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// fakeNftResolver answers the plan-150 family lookups from a table.
func fakeNftResolver(t *testing.T, table map[string][]string) {
	t.Helper()
	old := nftLookupIPAddr
	nftLookupIPAddr = func(_ context.Context, host string) ([]net.IPAddr, error) {
		list, ok := table[strings.ToLower(host)]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		out := make([]net.IPAddr, 0, len(list))
		for _, s := range list {
			out = append(out, net.IPAddr{IP: net.ParseIP(s)})
		}
		return out, nil
	}
	t.Cleanup(func() { nftLookupIPAddr = old })
}

func TestNftTargetPickPrefersTheEntryFamily(t *testing.T) {
	fakeNftResolver(t, map[string][]string{
		"dual.test": {"1.2.3.4", "2001:db8::5"},
		"v4.test":   {"1.2.3.5"},
	})
	pick := func(remote string, prefer addrFamilies) string {
		t.Helper()
		targets := resolveNftTargets(remote)
		if len(targets) != 1 {
			t.Fatalf("%s: %d targets", remote, len(targets))
		}
		return targets[0].pick(prefer)
	}
	cases := []struct {
		remote string
		prefer addrFamilies
		want   string
	}{
		{"1.2.3.4:80", familyIPv6, "1.2.3.4:80"},                  // literals are never rewritten
		{"[2001:db8::1]:80", familyIPv4, "[2001:db8::1]:80"},      // (unchanged, as before)
		{"dual.test:443", 0, "1.2.3.4:443"},                       // unknown entry: resolver order (old behaviour)
		{"dual.test:443", familyIPv6, "[2001:db8::5]:443"},        // node 48: IPv6-only entry
		{"dual.test:443", familyIPv4, "1.2.3.4:443"},              //
		{"dual.test:443", familyIPv4 | familyIPv6, "1.2.3.4:443"}, // dual-stack entry keeps the first
		{"v4.test:80", familyIPv6, "1.2.3.5:80"},                  // no IPv6: first address (rule flagged elsewhere)
		{"nx.test:80", familyIPv6, "nx.test:80"},                  // unresolved stays as is
	}
	for _, tc := range cases {
		if got := pick(tc.remote, tc.prefer); got != tc.want {
			t.Errorf("pick(%s, %s) = %s, want %s", tc.remote, tc.prefer.label(), got, tc.want)
		}
	}
}

type nftFamilySeed struct {
	ipv6Only, dualStack, unresolved, chainTunnel int64 // tunnel IDs
	node48, node23, nodeX                        int64
}

// seedNftFamilyTopology mirrors prod: node 48 (only IPv6 entries, CT main +
// CU extra, both DDNS names) and node 23 (IPv4 plus a custom IPv6 entry on
// its tunnel), plus an entry whose name does not resolve.
func seedNftFamilyTopology(t *testing.T, r *repo.Repository) nftFamilySeed {
	t.Helper()
	now := time.Now().UnixMilli()
	db := r.DB()
	mk := func(v interface{}) {
		t.Helper()
		if err := db.Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
	n48 := &model.Node{Name: "Leikwanhost 沪台IPLC", Secret: "s48", ServerIP: "ct.test", ServerIPV6: str("ct.test"), ExtraIPs: str(`["cu.test"]`), Port: "10000-65535", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	n23 := &model.Node{Name: "Mkcloud 沪日", Secret: "s23", ServerIP: "163.53.55.158", ServerIPV4: str("163.53.55.158"), Port: "10000-65535", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	nx := &model.Node{Name: "nx", Secret: "sx", ServerIP: "nx.test", Port: "10000-65535", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	mk(n48)
	mk(n23)
	mk(nx)
	tun := func(name, inIP string, typ int) int64 {
		tt := &model.Tunnel{Name: name, Type: typ, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1, InIP: str(inIP)}
		mk(tt)
		return tt.ID
	}
	s := nftFamilySeed{node48: n48.ID, node23: n23.ID, nodeX: nx.ID}
	s.ipv6Only = tun("拼车hinet3 利群沪台IPLC", "ct.test,cu.test", 1)
	s.dualStack = tun("hinet 沪日", "163.53.55.158,240e:96c:7100:1fe:1c8:c8aa:4ecc:e4", 1)
	s.unresolved = tun("nx", "", 1)
	for _, ct := range []model.ChainTunnel{
		{TunnelID: s.ipv6Only, ChainType: "1", NodeID: n48.ID},
		{TunnelID: s.dualStack, ChainType: "1", NodeID: n23.ID},
		{TunnelID: s.unresolved, ChainType: "1", NodeID: nx.ID},
	} {
		ct := ct
		mk(&ct)
	}
	return s
}

func TestNftFamilyVerdict(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := sharedLandingTestRepo(t, dialect)
			h := &Handler{repo: r}
			fakeNftResolver(t, map[string][]string{
				"ct.test":    {"240e:b8f:72d:5c04:ca:8aff:fec0:2813"},
				"cu.test":    {"2408:820c:750b:dc64:ca:8aff:fec0:2813"},
				"pqstw.test": {"36.232.0.161"},
				"dual.test":  {"1.1.1.1", "2606:4700:4700::1111"},
			})
			s := seedNftFamilyTopology(t, r)
			on48 := []forwardPortRecord{{NodeID: s.node48, Port: 32873}}

			// The user's rule 155: IPv6-only entry, IPv4 target -> dead in nftables.
			for _, target := range []string{"36.232.14.79:30000", "pqstw.test:30001"} {
				fallback, notes := h.nftFamilyVerdict(s.ipv6Only, on48, target)
				if !fallback || len(notes) != 1 || !strings.Contains(notes[0], "只有 IPv6 地址，目标只有 IPv4 地址") || !strings.Contains(notes[0], "已自动改用 gost") {
					t.Fatalf("%s: fallback=%v notes=%q", target, fallback, notes)
				}
			}
			// IPv6 target: nftables works (prod rule B), nothing to say.
			if fallback, notes := h.nftFamilyVerdict(s.ipv6Only, on48, "[2602:ffb0:236:1b::a]:47151"); fallback || len(notes) != 0 {
				t.Fatalf("ipv6 target: fallback=%v notes=%q", fallback, notes)
			}
			// Dual-stack domain (prod rule D picked 1.1.1.1): the IPv6 address is installed.
			if fallback, notes := h.nftFamilyVerdict(s.ipv6Only, on48, "dual.test:53"); fallback || len(notes) != 0 {
				t.Fatalf("dual target: fallback=%v notes=%q", fallback, notes)
			}
			tunnel, err := h.getTunnelRecord(s.ipv6Only)
			if err != nil {
				t.Fatal(err)
			}
			rules, _ := h.nftRulePayloads(&forwardRecord{ID: 160, TunnelID: s.ipv6Only, RemoteAddr: "dual.test:53"}, tunnel, on48, nil, 0)
			if len(rules) != 2 || rules[0].Target != "[2606:4700:4700::1111]:53" || rules[1].Target != "[2606:4700:4700::1111]:53" {
				t.Fatalf("dual target payloads: %+v", rules)
			}
			// A listen-IP override is what users are given: an IPv4 entry there is fine.
			if fallback, notes := h.nftFamilyVerdict(s.ipv6Only, []forwardPortRecord{{NodeID: s.node48, Port: 32873, InIP: "10.3.100.20"}}, "36.232.14.79:30000"); fallback || len(notes) != 0 {
				t.Fatalf("v4 listen IP: fallback=%v notes=%q", fallback, notes)
			}

			// Node 23 shape: IPv4 works, the custom IPv6 entry cannot reach an IPv4 target.
			on23 := []forwardPortRecord{{NodeID: s.node23, Port: 35386}}
			fallback, notes := h.nftFamilyVerdict(s.dualStack, on23, "103.150.8.249:25228")
			if fallback || len(notes) != 1 || !strings.Contains(notes[0], "的 IPv6 地址不能用 nftables 转发到 IPv4 目标") {
				t.Fatalf("partial: fallback=%v notes=%q", fallback, notes)
			}
			// IPv4-only entry, IPv6 target: the case the old form refused outright.
			if fallback, _ := h.nftFamilyVerdict(s.dualStack, []forwardPortRecord{{NodeID: s.node23, Port: 35386, InIP: "163.53.55.158"}}, "[2001:db8::10]:443"); !fallback {
				t.Fatal("ipv4-only entry to ipv6 target must fall back to gost")
			}

			// Unknown entry families never change a rule.
			if fallback, notes := h.nftFamilyVerdict(s.unresolved, []forwardPortRecord{{NodeID: s.nodeX, Port: 30000}}, "36.232.14.79:30000"); fallback || len(notes) != 0 {
				t.Fatalf("unresolved entry: fallback=%v notes=%q", fallback, notes)
			}
			// Unresolvable target: nothing known, nothing changed.
			if fallback, notes := h.nftFamilyVerdict(s.ipv6Only, on48, "nx-target.test:80"); fallback || len(notes) != 0 {
				t.Fatalf("unresolved target: fallback=%v notes=%q", fallback, notes)
			}
		})
	}
}

// The entry selection must match the copied 转发版 links (same helper).
func TestEntryHostsForNodesMatchesForwardEntryAddresses(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := sharedLandingTestRepo(t, dialect)
			s := seedNftFamilyTopology(t, r)
			hosts, names, err := r.EntryHostsForNodes(s.ipv6Only, []int64{s.node48}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(hosts[s.node48], ","); got != "ct.test,cu.test" || names[s.node48] != "Leikwanhost 沪台IPLC" {
				t.Fatalf("node 48 entry hosts %q (%q)", got, names[s.node48])
			}
			hosts, _, err = r.EntryHostsForNodes(s.dualStack, []int64{s.node23}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(hosts[s.node23], ","); got != "163.53.55.158,240e:96c:7100:1fe:1c8:c8aa:4ecc:e4" {
				t.Fatalf("node 23 entry hosts %q", got)
			}
			hosts, _, err = r.EntryHostsForNodes(s.ipv6Only, []int64{s.node48}, map[int64]string{s.node48: "10.3.100.20"})
			if err != nil || strings.Join(hosts[s.node48], ",") != "10.3.100.20" {
				t.Fatalf("listen IP override: %q %v", hosts[s.node48], err)
			}
		})
	}
}
