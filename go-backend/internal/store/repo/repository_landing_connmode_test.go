package repo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

func connModePtr(mode string) *string { return &mode }

// Plan 141 mode resolution: node override → server → default 入站+出站;
// 仅入站 only where the runtime applies it; a shared total is always 入站+出站.
func TestLandingConnModeResolution(t *testing.T) {
	native := &model.LandingServer{Source: "native"}
	inboundServer := &model.LandingServer{Source: "native", ConnMode: LandingConnInbound, MaxConnMode: LandingConnInbound}
	shared := &model.LandingServer{Source: "shared", ConnMode: LandingConnInbound, MaxConnMode: LandingConnInbound}
	follow := &model.LandingNode{}
	ownTotal := &model.LandingNode{ConnMode: connModePtr(LandingConnTotal)}
	ownInbound := &model.LandingNode{ConnMode: connModePtr(LandingConnInbound)}
	for _, tt := range []struct {
		name      string
		n         *model.LandingNode
		s         *model.LandingServer
		supported bool
		want      string
		effective string
	}{
		{"pre-141 row follows empty server", follow, native, true, LandingConnTotal, LandingConnTotal},
		{"follows inbound server", follow, inboundServer, true, LandingConnInbound, LandingConnInbound},
		{"old agent keeps total", follow, inboundServer, false, LandingConnInbound, LandingConnTotal},
		{"own total beats inbound server", ownTotal, inboundServer, true, LandingConnTotal, LandingConnTotal},
		{"own inbound on total server", ownInbound, native, true, LandingConnInbound, LandingConnInbound},
		{"no server", ownInbound, nil, true, LandingConnInbound, LandingConnInbound},
		{"garbage normalizes to total", &model.LandingNode{ConnMode: connModePtr("x")}, native, true, LandingConnTotal, LandingConnTotal},
	} {
		if got := LandingNodeConnMode(tt.n, tt.s); got != tt.want {
			t.Fatalf("%s: configured %q want %q", tt.name, got, tt.want)
		}
		if got := LandingEffectiveConnMode(tt.n, tt.s, tt.supported); got != tt.effective {
			t.Fatalf("%s: effective %q want %q", tt.name, got, tt.effective)
		}
	}
	if LandingServerTotalConnMode(inboundServer, true) != LandingConnInbound || LandingServerTotalConnMode(inboundServer, false) != LandingConnTotal || LandingServerTotalConnMode(shared, true) != LandingConnTotal {
		t.Fatal("server total mode")
	}
	n := &model.LandingNode{Connections: 9, InboundConnections: 4}
	if LandingModeConnections(n, LandingConnInbound) != 4 || LandingModeConnections(n, LandingConnTotal) != 9 {
		t.Fatal("mode connections")
	}
	n.RemoteState = "removed"
	if LandingModeConnections(n, LandingConnInbound) != 0 || LandingModeConnections(n, LandingConnTotal) != 0 {
		t.Fatal("removed node still counted")
	}
	if NativeLandingConnSig(nil, false) != "" || NativeLandingConnSig([]int64{9, 3}, true) != "g,3,9" || NativeLandingConnSig([]int64{3}, false) != "3" {
		t.Fatal("conn signature")
	}
	if !LandingConnModeSupported(native, 1) || LandingConnModeSupported(native, 0) || LandingConnModeSupported(shared, 5) || LandingConnModeSupported(nil, 1) {
		t.Fatal("support gate")
	}
	caps, _ := json.Marshal(landingcustom.Share{ID: 1, ConnModes: &landingcustom.ShareConnModes{Versions: []int{1}}})
	if !LandingConnModeSupported(&model.LandingServer{Source: "shared", SharedCaps: string(caps)}, 0) {
		t.Fatal("shared conn_modes v1 not supported")
	}
}

func TestLandingConnModeServerSaveNodesAndRevision(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			s = reloadServer(t, r, s.ID)
			if s.ConnMode != LandingConnTotal || s.MaxConnMode != LandingConnTotal || s.ConnModeSig != "" {
				t.Fatalf("migration defaults: %q %q %q", s.ConnMode, s.MaxConnMode, s.ConnModeSig)
			}
			a, err := r.CreateLandingNode(501, s.ID, "a", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			b, err := r.CreateLandingNode(501, s.ID, "b", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			if a.ConnMode != nil {
				t.Fatal("new node does not follow the server")
			}
			// Own override on b, then an invalid one.
			b.ConnMode = connModePtr(LandingConnInbound)
			if err := r.UpdateLandingNode(b); err != nil {
				t.Fatal(err)
			}
			b.ConnMode = connModePtr("outbound")
			if err := r.UpdateLandingNode(b); err == nil || !strings.Contains(err.Error(), "计算方式") {
				t.Fatalf("invalid node mode accepted: %v", err)
			}
			b.ConnMode = connModePtr("")
			if err := r.UpdateLandingNode(b); err == nil {
				t.Fatal("empty node mode accepted (null means follow)")
			}
			// Switching the server without 「应用到现有节点」 pins a to total.
			s = reloadServer(t, r, s.ID)
			rev := s.Revision
			s.ConnMode, s.DefaultMaxConnections = LandingConnInbound, 100
			if err := r.SaveLandingServer(s, []int64{701}); err != nil {
				t.Fatal(err)
			}
			if got := reloadServer(t, r, s.ID); got.ConnMode != LandingConnInbound || got.Revision != rev+1 {
				t.Fatalf("server save: %q rev %d", got.ConnMode, got.Revision)
			}
			a2, _ := r.GetLandingNode(a.ID)
			b2, _ := r.GetLandingNode(b.ID)
			if a2.ConnMode == nil || *a2.ConnMode != LandingConnTotal || b2.ConnMode == nil || *b2.ConnMode != LandingConnInbound {
				t.Fatalf("pin: a=%v b=%v", a2.ConnMode, b2.ConnMode)
			}
			c, err := r.CreateLandingNode(501, s.ID, "c", "vless_reality_vision", false)
			if err != nil {
				t.Fatal(err)
			}
			srv := reloadServer(t, r, s.ID)
			if c.ConnMode != nil || LandingNodeConnMode(c, srv) != LandingConnInbound || c.MaxConnections != 100 {
				t.Fatalf("new node after switch: %v %d", c.ConnMode, c.MaxConnections)
			}
			// Saving again with the same mode pins nothing new.
			if err := r.SaveLandingServer(srv, []int64{701}); err != nil {
				t.Fatal(err)
			}
			if c2, _ := r.GetLandingNode(c.ID); c2.ConnMode != nil {
				t.Fatal("unchanged mode pinned a following node")
			}
			// 「应用到现有节点」: all follow again and get the default number.
			srv = reloadServer(t, r, s.ID)
			srv.DefaultMaxConnections = 80
			if err := r.SaveLandingServerOpts(srv, []int64{701}, LandingServerSaveOptions{ApplyConnModeToNodes: true}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []int64{a.ID, b.ID, c.ID} {
				n, _ := r.GetLandingNode(id)
				if n.ConnMode != nil || n.MaxConnections != 80 {
					t.Fatalf("apply to existing: node %d mode=%v max=%d", id, n.ConnMode, n.MaxConnections)
				}
			}
			// Validation.
			bad := reloadServer(t, r, s.ID)
			bad.MaxConnMode = "both"
			if err := r.SaveLandingServer(bad, []int64{701}); err == nil {
				t.Fatal("invalid server mode accepted")
			}
			// Agent capability and the per-request support map.
			if err := r.SetNodeLandingCapabilities(601, true, 1, 1, 2, 1); err != nil {
				t.Fatal(err)
			}
			listed, _ := r.ListLandingNodes(0, true)
			data, err := r.LandingNodeListData(listed)
			if err != nil || data.ConnSupport[s.ID] != 1 || !data.ConnSupport.Supported(data.Servers[s.ID]) || data.Servers[s.ID].Name == "" {
				t.Fatalf("list support %v %v", data, err)
			}
			if err := r.SetNodeLandingCapabilities(601, true, 1, 1, 2); err != nil {
				t.Fatal(err)
			}
			if node, _ := r.GetNodeByID(601); node.LandingConnMode != 0 {
				t.Fatal("old agent report kept the capability")
			}
			// Revision: the 仅入站 signature bumps once, unchanged input does not.
			cur := reloadServer(t, r, s.ID)
			sig := "3"
			r1, _ := r.TouchLandingRevision(s.ID, cur.EgressHash, &sig)
			r2, _ := r.TouchLandingRevision(s.ID, cur.EgressHash, &sig)
			r3, _ := r.TouchLandingRevision(s.ID, cur.EgressHash, nil)
			empty := ""
			r4, _ := r.TouchLandingRevision(s.ID, cur.EgressHash, &empty)
			if r1 != cur.Revision+1 || r2 != r1 || r3 != r1 || r4 != r1+1 || reloadServer(t, r, s.ID).ConnModeSig != "" {
				t.Fatalf("revisions %d → %d %d %d %d", cur.Revision, r1, r2, r3, r4)
			}
		})
	}
}

// Shared desired: conn_mode only for shares advertising landing-conn-mode-v1;
// 入站+出站 nodes stay byte-identical (no revision bump when the capability
// arrives); the share cap still clamps 仅入站 nodes.
func TestSharedLandingDesiredConnMode(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := extraProtocolFixture(t, dialect)
			total, err := r.CreateLandingNode(501, s.ID, "total", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			inbound, err := r.CreateLandingNode(501, s.ID, "inbound", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			inbound.ConnMode, inbound.MaxConnections = connModePtr(LandingConnInbound), 100
			if err := r.UpdateLandingNode(inbound); err != nil {
				t.Fatal(err)
			}
			desired := func() (map[string]landingcustom.Node, int64, []byte) {
				t.Helper()
				d, err := r.SharedLandingDesired(s.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				out := map[string]landingcustom.Node{}
				for _, n := range d.Nodes {
					out[n.Name] = n
				}
				raw, _ := json.Marshal(out["total"])
				return out, d.Revision, raw
			}
			before, rev, totalRaw := desired()
			if before["inbound"].ConnMode != "" || strings.Contains(string(totalRaw), "conn_mode") {
				t.Fatalf("old share got conn_mode: %+v", before["inbound"])
			}
			raw, _ := json.Marshal(before["inbound"])
			if strings.Contains(string(raw), "conn_mode") {
				t.Fatalf("old share payload carries conn_mode: %s", raw)
			}
			// Capability arrives: only the inbound node changes.
			share := extraProtocolShare(true)
			share.ConnModes = &landingcustom.ShareConnModes{Versions: []int{1}}
			if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			after, rev2, totalRaw2 := desired()
			if after["inbound"].ConnMode != landingcustom.ConnModeInbound || after["total"].ConnMode != "" || string(totalRaw) != string(totalRaw2) || rev2 != rev+1 {
				t.Fatalf("capable share: inbound=%q total=%q rev %d→%d", after["inbound"].ConnMode, after["total"].ConnMode, rev, rev2)
			}
			// Without any 仅入站 node the desired hash (and revision) is unchanged.
			inbound.ConnMode = nil
			if err := r.UpdateLandingNode(inbound); err != nil {
				t.Fatal(err)
			}
			_, rev3, _ := desired()
			share.ConnModes = nil
			if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if _, rev4, _ := desired(); rev4 != rev3 {
				t.Fatalf("capability loss bumped an all-total share: %d → %d", rev3, rev4)
			}
			// Share cap clamps 仅入站 nodes like before; validation still applies.
			inbound.ConnMode = connModePtr(LandingConnInbound)
			if err := r.UpdateLandingNode(inbound); err != nil {
				t.Fatal(err)
			}
			share.MaxConnections = 50
			if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if n, _ := r.GetLandingNode(inbound.ID); n.MaxConnections != 50 || n.ConnMode == nil {
				t.Fatalf("clamp: %+v", n)
			}
			inbound.MaxConnections = 60
			if err := r.UpdateLandingNode(inbound); err == nil {
				t.Fatal("inbound node above the share cap accepted")
			}
			srv := reloadServer(t, r, s.ID)
			srv.MaxConnMode = LandingConnInbound
			if err := r.SaveLandingServer(srv, nil); err == nil || !strings.Contains(err.Error(), "妙妙屋") {
				t.Fatalf("shared 仅入站 total accepted: %v", err)
			}
			_ = total
		})
	}
}

func TestLandingCounterInboundConnections(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, now := landingFlowFixture(t, dialect)
			ingest := func(c LandingCounter) {
				t.Helper()
				if _, e := r.ApplyFlowBatch(FlowBatch{LandingCounters: []LandingCounter{c}}, now); e != nil {
					t.Fatal(e)
				}
			}
			c := landingSnapshot("100", 10, 20)
			c.Connections, c.InboundConnections = 9, intPtrConn(4)
			ingest(c)
			if n, _ := r.GetLandingNode(1); n.Connections != 9 || n.InboundConnections != 4 {
				t.Fatalf("stored %d/%d", n.Connections, n.InboundConnections)
			}
			// Older reporter (nil) leaves the inbound column alone.
			c = landingSnapshot("100", 11, 21)
			c.Connections = 7
			ingest(c)
			if n, _ := r.GetLandingNode(1); n.Connections != 7 || n.InboundConnections != 4 {
				t.Fatalf("nil inbound overwrote: %d/%d", n.Connections, n.InboundConnections)
			}
			// A bad optional value never fails billing.
			c = landingSnapshot("100", 12, 22)
			c.InboundConnections = intPtrConn(-3)
			ingest(c)
			if n, _ := r.GetLandingNode(1); n.InboundConnections != 0 || n.UpBytes != 12 {
				t.Fatalf("negative inbound: %+v", n)
			}
		})
	}
}

func intPtrConn(v int) *int { return &v }
