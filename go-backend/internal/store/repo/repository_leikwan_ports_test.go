package repo

import (
	"database/sql"
	"sort"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

// Plan 265: every port picker skips the node's LeiKwan client ports.
const lkPortsJSON = `[{"forwardId":264,"listenPort":10015,"running":true},{"forwardId":281,"listenPort":10016,"running":false},{"forwardId":282,"listenPort":10017,"running":true},{"forwardId":318,"listenPort":10044,"running":true}]`

func lkPortsRepo(t *testing.T, dialect string) *Repository {
	t.Helper()
	r := orphanTestRepo(t, dialect)
	now := time.Now().UnixMilli()
	orphanSeed(t, r,
		&model.Node{ID: 60, Name: "阿里广州", Secret: "s60", ServerIP: "198.51.100.60", Port: "10014-10019", Status: 1, CreatedTime: now, LeiKwanStatus: "running", LeiKwanForwards: lkPortsJSON},
		&model.Node{ID: 12, Name: "Bestvm TW", Secret: "s12", ServerIP: "198.51.100.12", Port: "10014-10019", Status: 1, CreatedTime: now},
	)
	return r
}

func TestLeiKwanReservedFromRaw(t *testing.T) {
	got := LeiKwanReservedFromRaw(`[{"forwardId":1,"listenPort":10015,"running":true},{"forwardId":2,"listenPort":10016,"running":false},{"forwardId":3,"listenPort":0},{"forwardId":4,"listenPort":70000}]`)
	if len(got) != 2 || !got[10015] || !got[10016] {
		t.Fatalf("got %v", got)
	}
	for _, raw := range []string{"", "[]", "not json"} {
		if got := LeiKwanReservedFromRaw(raw); len(got) != 0 {
			t.Fatalf("%q -> %v", raw, got)
		}
	}
}

func TestLeiKwanPortsSkippedByPickers(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := lkPortsRepo(t, dialect)
			db := r.DB()
			// Lowest free: 10014, then 10018 (10015–10017 are LeiKwan, incl. the stopped 10016).
			if p, err := r.PickNodePortTx(db, 60, map[int64]int{}, 0); err != nil || p != 10014 {
				t.Fatalf("lowest pick %d %v", p, err)
			}
			orphanSeed(t, r, &model.ForwardPort{ForwardID: 501, NodeID: 60, Port: 10014})
			if p, err := r.PickNodePortTx(db, 60, map[int64]int{}, 0); err != nil || p != 10018 {
				t.Fatalf("second lowest pick %d %v", p, err)
			}
			for i := 0; i < 40; i++ {
				p, err := r.PickRandomNodePortTx(db, 60, map[int64]int{}, 0)
				if err != nil || (p != 10018 && p != 10019) {
					t.Fatalf("random pick %d %v", p, err)
				}
			}
			// A node without LeiKwan still gets 10015.
			if p, err := r.PickNodePortTx(db, 12, map[int64]int{}, 0); err != nil || p != 10014 {
				t.Fatalf("plain node pick %d %v", p, err)
			}
			used, err := r.GetUsedPortsOnNodeAsMap(60)
			if err != nil || !used[10015] || !used[10016] || !used[10017] || !used[10044] || !used[10014] || used[10018] {
				t.Fatalf("used map %v %v", used, err)
			}
			if plain, err := r.GetUsedPortsOnNodeAsMap(12); err != nil || len(plain) != 0 {
				t.Fatalf("plain used map %v %v", plain, err)
			}
			list, err := r.ListUsedPortsOnNode(60)
			sort.Ints(list)
			if err != nil || len(list) != 5 || list[0] != 10014 || list[4] != 10044 {
				t.Fatalf("used list %v %v", list, err)
			}
			// Line monitor auto check ports.
			for i := 0; i < 40; i++ {
				p, err := pickLineMonitorPortTx(db, 60, nil)
				if err != nil || (p != 10018 && p != 10019) {
					t.Fatalf("check port %d %v", p, err)
				}
			}
			// Landing (native) and chain listeners on the same machine.
			s := model.LandingServer{ID: 7, Source: "native", NodeID: 60, Name: "lk", PublicHost: "198.51.100.60", PortMin: 10015, PortMax: 10030}
			orphanSeed(t, r, &s)
			if p, err := landingFreePort(db, s); err != nil || p != 10020 {
				t.Fatalf("landing pick %d %v", p, err) // 10015-10019 = rule range, 10044 outside
			}
			r.DB().Model(&model.Node{}).Where("id = ?", 60).Update("port", "20000-20010")
			if p, err := landingFreePort(db, s); err != nil || p != 10018 {
				t.Fatalf("landing pick w/o rule overlap %d %v", p, err)
			}
			chainUsed, err := usedPortsTx(db, s, 0)
			if err != nil || !chainUsed[10016] || !chainUsed[10044] {
				t.Fatalf("chain used %v", err)
			}
			// Shared landing on a machine that is also this LeiKwan node.
			shared := model.LandingServer{ID: 8, Source: "shared", Name: "mmwx", PublicHost: "198.51.100.60", PortMin: 10015, PortMax: 10030}
			busy, err := sharedMachineBusyPorts(db, shared)
			if err != nil || !busy[10015] || !busy[10016] || !busy[10044] {
				t.Fatalf("shared busy %v %v", busy, err)
			}
		})
	}
}

func TestLeiKwanPortBlocksAndHolders(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := lkPortsRepo(t, dialect)
			db := r.DB()
			orphanSeed(t, r,
				&model.ForwardPort{ForwardID: 701, NodeID: 60, Port: 10015},
				&model.ChainTunnel{TunnelID: 803, ChainType: "3", NodeID: 60, Port: sql.NullInt64{Int64: 10017, Valid: true}},
			)
			for _, tc := range []struct {
				port    int
				forward int64
				want    bool
			}{{10016, 0, true}, {10016, 701, true}, {10015, 701, false}, {10015, 702, true}, {10018, 0, false}} {
				got, err := r.LeiKwanPortBlocksForwardTx(db, 60, tc.port, tc.forward)
				if err != nil || got != tc.want {
					t.Fatalf("forward %+v -> %v %v", tc, got, err)
				}
			}
			if got, _ := r.LeiKwanPortBlocksForwardTx(db, 12, 10016, 0); got {
				t.Fatal("plain node blocked")
			}
			if got, _ := r.LeiKwanPortBlocksTunnelTx(db, 60, 10017, 803); got {
				t.Fatal("tunnel's own port refused")
			}
			if got, _ := r.LeiKwanPortBlocksTunnelTx(db, 60, 10017, 0); !got {
				t.Fatal("new tunnel port allowed")
			}
			// HasOtherForwardOnNodePortTx (resolvePortForNewEntryNode, self-occupied rebind).
			if got, _ := r.HasOtherForwardOnNodePortTx(db, 60, 10015, 701); got {
				t.Fatal("own grandfathered port reported busy")
			}
			if got, _ := r.HasOtherForwardOnNodePortTx(db, 60, 10016, 701); !got {
				t.Fatal("LeiKwan port reported free")
			}
			holders, err := r.LeiKwanPortHolders(60, []int{10015, 10016, 10017, 10044})
			sort.Ints(holders)
			if err != nil || len(holders) != 2 || holders[0] != 10015 || holders[1] != 10017 {
				t.Fatalf("holders %v %v", holders, err)
			}
			byHost, err := r.LeiKwanReservedPortsByHost("198.51.100.60")
			if err != nil || len(byHost) != 4 || !byHost[10016] {
				t.Fatalf("by host %v %v", byHost, err)
			}
			if other, _ := r.LeiKwanReservedPortsByHost("198.51.100.12"); len(other) != 0 {
				t.Fatalf("plain host %v", other)
			}
		})
	}
}
