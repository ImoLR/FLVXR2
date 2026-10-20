package repo

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
)

func chainRepoSeed(t *testing.T, r *Repository) {
	t.Helper()
	now := time.Now().UnixMilli()
	for _, id := range []int64{1, 3, 4} {
		orphanSeed(t, r, &model.Node{ID: id, Name: "n", Secret: "s" + string(rune('a'+id)), ServerIP: "192.0.2.9", Port: "30000-30010", CreatedTime: now, Status: 1, LandingCapable: true, LandingEgress: 1, LandingChain: 1, OwnerUserID: 1})
	}
	orphanSeed(t, r, &model.User{ID: 7, User: "u7", Pwd: "x", RoleID: 1, ExpTime: now + 86400000, Flow: 99999, FlowResetTime: 1, Num: 10, CreatedTime: now, Status: 1})
	orphanSeed(t, r, &model.Tunnel{ID: 10, Name: "t", TrafficRatio: 1, Type: 2, Protocol: "tls", Flow: 2, CreatedTime: now, UpdatedTime: now, Status: 1})
}

func chainUpstream(t *testing.T, link string) linechain.Upstream {
	t.Helper()
	u, _, err := linechain.ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// SQLite and PostgreSQL: migration, the shared free port across exits (rule
// ranges and landing ports excluded), the partial idempotency index, the
// duplicate refusal, copy retargeting and the delete cascades.
func TestForwardChainLifecycleBothDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := orphanTestRepo(t, dialect)
			chainRepoSeed(t, r)
			// Node 3 already has a public landing whose range starts at 30000:
			// its port 30000 is taken by a landing node.
			now := time.Now().UnixMilli()
			orphanSeed(t, r, &model.LandingServer{ID: 5, Source: "native", NodeID: 3, Name: "pub", PublicHost: "192.0.2.3", PortMin: 30000, PortMax: 30020, FlowType: 2, TrafficRatio: 1, DefaultExpiryMode: "follow_user", MaxNodesPerUser: 3, Protocols: `["ss2022"]`, RealityDest: "a.example.com:443", RealityServerNames: `["a.example.com"]`, CoreMemoryMB: 256, CoreCPUPercent: 50, Status: "active", Revision: 1, CreatedTime: now, UpdatedTime: now})
			orphanSeed(t, r, &model.LandingNode{ID: 1, UserID: 7, LandingServerID: 5, Name: "x", Protocol: "ss2022", Port: 30011, InboundTag: "flvxl-1", Status: "active", ServerNames: "[]", CreatedTime: now, UpdatedTime: now})
			in := ChainCreateInput{UserID: 7, TunnelID: 10, IdemKey: "idem-key-0001", Upstream: chainUpstream(t, "socks5://9.9.9.9:1080"), ExitNodeIDs: []int64{3, 4}, InFlightAfter: now + 3600000}
			c, copies, err := r.CreateForwardChain(in)
			if err != nil {
				t.Fatal(err)
			}
			// Node rule range 30000-30010 is excluded, landing port 30011 too;
			// the public server's range caps the search at 30020.
			if c.Port != 30012 || len(copies) != 2 || copies[0].Port != copies[1].Port {
				t.Fatalf("port %d copies %+v", c.Port, copies)
			}
			if c.UUID == "" || c.PublicKey == "" || c.PrivateKey == "" || c.ShortID == "" || c.Dest != "a.example.com:443" {
				t.Fatalf("credentials %+v", c)
			}
			var chainOnly []model.LandingServer
			if err := r.db.Where("chain_only = ?", true).Find(&chainOnly).Error; err != nil || len(chainOnly) != 1 || chainOnly[0].NodeID != 4 {
				t.Fatalf("chain-only exits: %+v %v", chainOnly, err)
			}
			// Same submit key → same chain; landing and forward ports see the copy.
			again, _, err := r.CreateForwardChain(in)
			if !errors.Is(err, ErrChainIdempotent) || again.ID != c.ID {
				t.Fatalf("idempotent create: %+v %v", again, err)
			}
			if used, _ := r.LandingPortInUse(4, c.Port); !used {
				t.Fatal("chain port not reserved for forwards")
			}
			if err := r.ValidateNodeLandingPortRange(4, "30000-30020"); err == nil {
				t.Fatal("rule range widened over a chain port")
			}
			// Same node on the same tunnel: refused while the first is active.
			if err := r.ActivateForwardChain(c.ID, 0); err != nil {
				t.Fatal(err)
			}
			in2 := in
			in2.IdemKey = "idem-key-0002"
			if _, _, err := r.CreateForwardChain(in2); err == nil || !strings.Contains(err.Error(), "已经在本隧道") {
				t.Fatalf("duplicate: %v", err)
			}
			// Empty keys never collide (partial unique index).
			in3 := in2
			in3.IdemKey, in3.Upstream = "", chainUpstream(t, "socks5://9.9.9.8:1080")
			in3.InFlightAfter = now - 600000
			in4 := in3
			in4.Upstream = chainUpstream(t, "socks5://9.9.9.7:1080")
			c3, _, err := r.CreateForwardChain(in3)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := r.CreateForwardChain(in4); err == nil {
				t.Fatal("second create while one is in flight was accepted")
			}
			if _, err := r.FailForwardChain(c3.ID, "x"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := r.CreateForwardChain(in4); err != nil {
				t.Fatalf("create after failure: %v", err)
			}
			// Retarget onto exit 1 only: the port moves only if needed.
			plan, err := r.PrepareChainRetarget(c.ID, []int64{1})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.CommitChainRetarget(plan, 10); err != nil {
				t.Fatal(err)
			}
			left, _ := r.ListForwardChainNodes(c.ID)
			if len(left) != 1 || left[0].NodeID != 1 || left[0].State != model.ChainNodeActive {
				t.Fatalf("after retarget: %+v", left)
			}
			// Orphans: a copy of a chain that no longer exists.
			orphanSeed(t, r, &model.ForwardChainNode{ChainID: 999, LandingServerID: chainOnly[0].ID, NodeID: 4, Port: 30019, State: model.ChainNodeActive, CreatedTime: now})
			orphans, err := r.FindChainOrphans()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, o := range orphans {
				if o.Kind == "chainNode" && o.ChainID == 999 {
					found = true
					if _, err := r.DeleteChainOrphan(o); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !found {
				t.Fatalf("orphan copy not found: %+v", orphans)
			}
			// User delete cascades every chain row.
			if err := r.DeleteUserCascade(7); err != nil {
				t.Fatal(err)
			}
			orphanCount(t, r, "forward_chain", "1 = 1", 0)
			orphanCount(t, r, "forward_chain_node", "1 = 1", 0)
		})
	}
}
