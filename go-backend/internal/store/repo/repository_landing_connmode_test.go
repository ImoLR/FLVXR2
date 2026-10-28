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

// Plan 158 retired the plan-141 choice: every node limit counts 入站 (only
// where the runtime applies it, else 入站+出站 as before); a shared server's
// nodes count 入站 only with landing-user-limits-v1; the server total is
// 入站+出站 except a native total still stored as 仅入站 (until saved again).
func TestLandingConnModeResolution(t *testing.T) {
	native := &model.LandingServer{Source: "native"}
	inboundServer := &model.LandingServer{Source: "native", ConnMode: LandingConnInbound, MaxConnMode: LandingConnInbound}
	shared := &model.LandingServer{Source: "shared", ConnMode: LandingConnInbound, MaxConnMode: LandingConnInbound}
	follow := &model.LandingNode{}
	ownTotal := &model.LandingNode{ConnMode: connModePtr(LandingConnTotal)}
	ownInbound := &model.LandingNode{ConnMode: connModePtr(LandingConnInbound)}
	userLimitsCaps, _ := json.Marshal(landingcustom.Share{ID: 1, UserLimits: &landingcustom.ShareUserLimits{Versions: []int{1}}})
	sharedUL := &model.LandingServer{Source: "shared", SharedCaps: string(userLimitsCaps)}
	for _, tt := range []struct {
		name      string
		n         *model.LandingNode
		s         *model.LandingServer
		supported bool
		want      string
		effective string
	}{
		{"pre-141 row on an empty server counts 入站", follow, native, true, LandingConnInbound, LandingConnInbound},
		{"follows inbound server", follow, inboundServer, true, LandingConnInbound, LandingConnInbound},
		{"old agent keeps total", follow, inboundServer, false, LandingConnInbound, LandingConnTotal},
		{"a stored own 入站+出站 is ignored", ownTotal, inboundServer, true, LandingConnInbound, LandingConnInbound},
		{"own inbound on total server", ownInbound, native, true, LandingConnInbound, LandingConnInbound},
		{"no server", ownInbound, nil, true, LandingConnInbound, LandingConnInbound},
		{"garbage is ignored", &model.LandingNode{ConnMode: connModePtr("x")}, native, true, LandingConnInbound, LandingConnInbound},
		// Shared servers stay 入站+出站 until 妙妙屋 applies user limits.
		{"shared ignores node override", ownInbound, shared, true, LandingConnTotal, LandingConnTotal},
		{"shared ignores server mode", follow, shared, true, LandingConnTotal, LandingConnTotal},
		{"shared with user limits counts 入站", follow, sharedUL, true, LandingConnInbound, LandingConnInbound},
	} {
		if got := LandingNodeConnMode(tt.n, tt.s); got != tt.want {
			t.Fatalf("%s: configured %q want %q", tt.name, got, tt.want)
		}
		if got := LandingEffectiveConnMode(tt.n, tt.s, tt.supported); got != tt.effective {
			t.Fatalf("%s: effective %q want %q", tt.name, got, tt.effective)
		}
	}
	if LandingServerTotalConnMode(inboundServer, true) != LandingConnInbound || LandingServerTotalConnMode(inboundServer, false) != LandingConnTotal || LandingServerTotalConnMode(shared, true) != LandingConnTotal || LandingServerTotalConnMode(native, true) != LandingConnTotal {
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
	// landing-conn-mode-v1 alone is still never used (plan 147); user limits are.
	caps, _ := json.Marshal(landingcustom.Share{ID: 1, ConnModes: &landingcustom.ShareConnModes{Versions: []int{1}}})
	advertised := &model.LandingServer{Source: "shared", SharedCaps: string(caps)}
	if LandingConnModeSupported(advertised, 0) || LandingConnModeSupported(advertised, 1) || !LandingConnModeFixed(advertised) || LandingConnModeFixed(native) {
		t.Fatal("shared conn mode must stay fixed 入站+出站 with conn_modes only")
	}
	if !LandingConnModeSupported(sharedUL, 0) || !SharedUserLimitsSupported(sharedUL) || SharedUserLimitsSupported(advertised) {
		t.Fatal("user_limits v1 must enable 入站 node limits on a share")
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

// Plan 147 (user 10-24: 妙妙屋侧不动): a shared server never offers or sends
// 仅入站. The admin API refuses it on shared servers and nodes, a legacy row
// holding it is inert, the desired payload never carries conn_mode (also when
// the share advertises landing-conn-mode-v1) and stays byte-identical, and
// 「应用到现有节点」 does nothing on a shared server.
func TestSharedLandingConnModeFixed(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := extraProtocolFixture(t, dialect)
			total, err := r.CreateLandingNode(501, s.ID, "total", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := r.CreateLandingNode(501, s.ID, "legacy", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			legacy.ConnMode, legacy.MaxConnections = connModePtr(LandingConnInbound), 100
			if err := r.UpdateLandingNode(legacy); err == nil || !strings.Contains(err.Error(), "入站+出站") {
				t.Fatalf("shared node 仅入站 accepted: %v", err)
			}
			legacy.ConnMode = connModePtr(LandingConnTotal)
			if err := r.UpdateLandingNode(legacy); err != nil {
				t.Fatal(err)
			}
			if n, _ := r.GetLandingNode(legacy.ID); n.ConnMode != nil || n.MaxConnections != 100 {
				t.Fatalf("shared total override stored: %+v", n.ConnMode)
			}
			desired := func() ([]byte, int64) {
				t.Helper()
				d, err := r.SharedLandingDesired(s.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(d.Nodes)
				return raw, d.Revision
			}
			clean, rev := desired()
			// A row written by fork.86-88 with 仅入站 (none on prod) is inert.
			if err := r.db.Model(&model.LandingNode{}).Where("id = ?", legacy.ID).Update("conn_mode", LandingConnInbound).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.db.Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("conn_mode", LandingConnInbound).Error; err != nil {
				t.Fatal(err)
			}
			share := extraProtocolShare(true)
			share.ConnModes = &landingcustom.ShareConnModes{Versions: []int{1}}
			if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			got, rev2 := desired()
			if string(got) != string(clean) || strings.Contains(string(got), "conn_mode") || rev2 != rev {
				t.Fatalf("shared desired changed: rev %d→%d\n%s\n%s", rev, rev2, clean, got)
			}
			// The legacy node stays editable; saving it clears the stored mode.
			stored, _ := r.GetLandingNode(legacy.ID)
			stored.Name = "legacy-renamed"
			if err := r.UpdateLandingNode(stored); err != nil {
				t.Fatalf("legacy shared 仅入站 node not editable: %v", err)
			}
			if n, _ := r.GetLandingNode(legacy.ID); n.ConnMode != nil || n.Name != "legacy-renamed" {
				t.Fatalf("legacy mode kept: %v", n.ConnMode)
			}
			// Server save: 仅入站 refused; 「应用到现有节点」 ignored.
			if err := r.db.Model(&model.LandingServer{}).Where("id = ?", s.ID).Update("conn_mode", LandingConnTotal).Error; err != nil {
				t.Fatal(err)
			}
			srv := reloadServer(t, r, s.ID)
			srv.ConnMode = LandingConnInbound
			if err := r.SaveLandingServer(srv, nil); err == nil || !strings.Contains(err.Error(), "入站+出站") {
				t.Fatalf("shared server 仅入站 accepted: %v", err)
			}
			srv = reloadServer(t, r, s.ID)
			srv.DefaultMaxConnections = 7
			if err := r.SaveLandingServerOpts(srv, nil, LandingServerSaveOptions{ApplyConnModeToNodes: true}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []int64{total.ID, legacy.ID} {
				if n, _ := r.GetLandingNode(id); n.MaxConnections == 7 {
					t.Fatalf("apply-to-existing touched shared node %d", id)
				}
			}
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
