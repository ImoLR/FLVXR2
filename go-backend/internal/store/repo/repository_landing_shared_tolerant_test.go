package repo

import (
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 243 fixtures: the four business shares (remote ids 5/10/12/16).
func tolerantShares() []landingcustom.Share {
	mk := func(id int64, name, ip string) landingcustom.Share {
		return landingcustom.Share{ID: id, Name: name, Hosts: landingcustom.Hosts{IPv4: ip}, Status: "active", PortMin: 30000, PortMax: 30999, Protocols: []string{"ss2022", "vless_reality_vision"}}
	}
	return []landingcustom.Share{mk(5, "Boil Hinet 马年", "198.51.100.5"), mk(10, "Dmit T1", "198.51.100.10"), mk(12, "Boil Hinet 158", "198.51.100.12"), mk(16, "PQS TW", "198.51.100.16")}
}

func tolerantRow(t *testing.T, r *Repository, remote int64) model.LandingServer {
	t.Helper()
	var s model.LandingServer
	if err := r.db.Where("source = ? AND remote_server_id = ?", "shared", remote).First(&s).Error; err != nil {
		t.Fatalf("row for remote %d: %v", remote, err)
	}
	return s
}

func tolerantSeed(t *testing.T, dialect string) *Repository {
	t.Helper()
	r := poolRepoFixture(t, dialect)
	if err := r.SyncSharedLandings(tolerantShares(), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return r
}

func without(shares []landingcustom.Share, remote ...int64) []landingcustom.Share {
	drop := map[int64]bool{}
	for _, id := range remote {
		drop[id] = true
	}
	out := []landingcustom.Share{}
	for _, s := range shares {
		if !drop[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

// One invalid item (each validation rule) no longer rejects the list: the valid
// items apply in the same round, the bad one keeps its last good config and
// shows the item's problem.
func TestSharedListSkipsInvalidItemOnly(t *testing.T) {
	bad := map[string]func(*landingcustom.Share){
		"ports-low":     func(s *landingcustom.Share) { s.PortMin = 80 },
		"ports-reverse": func(s *landingcustom.Share) { s.PortMax = s.PortMin - 1 },
		"ports-high":    func(s *landingcustom.Share) { s.PortMax = 70000 },
		"limit":         func(s *landingcustom.Share) { s.TrafficLimit = -1 },
		"used":          func(s *landingcustom.Share) { s.TrafficUsed = -1 },
		"conns":         func(s *landingcustom.Share) { s.MaxConnections = -1 },
		"status":        func(s *landingcustom.Share) { s.Status = "deleted" },
		"protocols":     func(s *landingcustom.Share) { s.Protocols = nil },
		"hosts":         func(s *landingcustom.Share) { s.Hosts = landingcustom.Hosts{}; s.Name = "" },
	}
	for _, dialect := range []string{"sqlite", "postgres"} {
		for name, mutate := range bad {
			t.Run(dialect+"/"+name, func(t *testing.T) {
				r := tolerantSeed(t, dialect)
				before := tolerantRow(t, r, 12)
				shares := tolerantShares()
				for i := range shares {
					shares[i].TrafficLimit = 1 << 40 // a change every valid row must take
				}
				mutate(&shares[2]) // remote 12
				rep, err := r.SyncSharedLandingList(shares, time.Now().UnixMilli(), NewSharedVanishTracker())
				if err != nil {
					t.Fatalf("list rejected: %v", err)
				}
				for _, remote := range []int64{5, 10, 16} {
					s := tolerantRow(t, r, remote)
					if SharedLandingCaps(&s).TrafficLimit != 1<<40 || s.SharedSyncError != "" {
						t.Fatalf("remote %d not applied: caps %s err %q", remote, s.SharedCaps, s.SharedSyncError)
					}
				}
				after := tolerantRow(t, r, 12)
				if after.SharedCaps != before.SharedCaps || after.PublicHost != before.PublicHost || after.Name != before.Name {
					t.Fatalf("bad item changed its row: %s -> %s", before.SharedCaps, after.SharedCaps)
				}
				if !strings.Contains(after.SharedSyncError, "已跳过") || rep.Skipped[after.ID] != after.SharedSyncError {
					t.Fatalf("bad item error = %q, report %v", after.SharedSyncError, rep.Skipped)
				}
				if len(rep.Revoked) != 0 || len(rep.Held) != 0 || len(rep.Missing) != 0 {
					t.Fatalf("an invalid item must not count as vanished: %+v", rep)
				}
			})
		}
	}
}

func TestSharedListBadIDDuplicateAndUnknown(t *testing.T) {
	r := tolerantSeed(t, "sqlite")
	shares := tolerantShares()
	dup := shares[1] // remote 10 twice
	dup.Name = "other"
	unknown := landingcustom.Share{ID: 7, Status: "active", PortMin: 30000, PortMax: 30999, Protocols: []string{"ss2022"}} // hostless, no row
	shares = append(shares, dup, landingcustom.Share{ID: 0}, unknown)
	rep, err := r.SyncSharedLandingList(shares, time.Now().UnixMilli(), NewSharedVanishTracker())
	if err != nil {
		t.Fatal(err)
	}
	if s := tolerantRow(t, r, 10); s.Name != "Dmit T1" || !strings.Contains(s.SharedSyncError, "重复") {
		t.Fatalf("duplicate: %q %q", s.Name, s.SharedSyncError)
	}
	if rep.SkippedRemote[0] == "" || rep.SkippedRemote[7] == "" {
		t.Fatalf("remote-only skips not reported: %v", rep.SkippedRemote)
	}
	orphanCount(t, r, "landing_server", "source = ? AND remote_server_id = ?", 0, "shared", 7)
	if s := tolerantRow(t, r, 5); s.SharedSyncError != "" {
		t.Fatalf("good row flagged: %q", s.SharedSyncError)
	}
	// The incident shape: the item is fixed next round → the error clears via
	// the handler; the row applies normally again.
	if _, err := r.SyncSharedLandingList(tolerantShares(), time.Now().UnixMilli(), NewSharedVanishTracker()); err != nil {
		t.Fatal(err)
	}
}

// Vanished 1 of 4: revoked only after ≥3 successful rounds spanning ≥3 min, via
// the explicit revoked path (per-server rows deleted, caps revoked, nodes kept).
func TestSharedVanishOneRevokesAfterThreshold(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := tolerantSeed(t, dialect)
			s := tolerantRow(t, r, 16)
			if err := r.SaveLandingUserSettingFull(2, LandingMaxNodesFollowGlobal, []model.UserServerQuota{
				{ServerKind: "landing", ServerRefID: s.ID, FollowDefault: true, RateOverride: true, RateMbps: 40},
			}, 0, nil, &[]TrafficPoolInput{{Name: "p", LimitBytes: 1 << 30, Members: []TrafficPoolServer{{ServerKind: "landing", ServerRefID: s.ID}}}}); err != nil {
				t.Fatal(err)
			}
			orphanSeed(t, r, &model.LandingNode{LandingServerID: s.ID, UserID: 2, Name: "n", Protocol: "ss2022", Port: 30001, Status: "active", CreatedTime: 1, UpdatedTime: 1})
			tr := NewSharedVanishTracker()
			t0 := time.Now()
			list := without(tolerantShares(), 16)
			for i, step := range []time.Duration{0, time.Minute, 2 * time.Minute} {
				rep, err := r.SyncSharedLandingList(list, t0.Add(step).UnixMilli(), tr)
				if err != nil || len(rep.Revoked) != 0 {
					t.Fatalf("round %d: revoked early %+v %v", i, rep, err)
				}
				row := tolerantRow(t, r, 16)
				if SharedLandingCaps(&row).Status != "active" || !strings.Contains(row.SharedSyncError, "已没有该共享") {
					t.Fatalf("round %d: %s %q", i, SharedLandingCaps(&row).Status, row.SharedSyncError)
				}
			}
			rep, err := r.SyncSharedLandingList(list, t0.Add(3*time.Minute).UnixMilli(), tr)
			if err != nil || len(rep.Revoked) != 1 || rep.Revoked[0] != s.ID {
				t.Fatalf("not revoked at 3 min: %+v %v", rep, err)
			}
			row := tolerantRow(t, r, 16)
			if c := SharedLandingCaps(&row); c.Status != "revoked" || c.PortMin != 30000 || row.PublicHost != s.PublicHost {
				t.Fatalf("revoked caps %s host %s", row.SharedCaps, row.PublicHost)
			}
			orphanCount(t, r, "user_server_quota", "server_kind = ? AND server_ref_id = ?", 0, "landing", s.ID)
			orphanCount(t, r, "user_traffic_pool_member", "server_kind = ? AND server_ref_id = ?", 0, "landing", s.ID)
			orphanCount(t, r, "landing_node", "landing_server_id = ?", 1, s.ID) // nodes kept (history), like explicit revoke
			for _, remote := range []int64{5, 10, 12} {
				if c := SharedLandingCaps(&model.LandingServer{SharedCaps: tolerantRow(t, r, remote).SharedCaps}); c.Status != "active" {
					t.Fatalf("remote %d touched: %s", remote, c.Status)
				}
			}
			// Terminal: reappearing as active keeps it revoked (existing rule).
			if _, err = r.SyncSharedLandingList(tolerantShares(), t0.Add(4*time.Minute).UnixMilli(), tr); err != nil {
				t.Fatal(err)
			}
			row = tolerantRow(t, r, 16)
			if SharedLandingCaps(&row).Status != "revoked" {
				t.Fatalf("revoked share resurrected")
			}
			// Startup orphan cleanup stays a no-op afterwards.
			if got, err := r.CleanupServerRowOrphans(); err != nil || got.Quotas != 0 || got.Members != 0 {
				t.Fatalf("cleanup after vanish revoke = %+v %v", got, err)
			}
		})
	}
}

// The vanish revoke and an explicit `status=revoked` share one path: same
// resulting caps (except the share's own fields), same per-server cleanup.
func TestSharedVanishSamePathAsExplicitRevoke(t *testing.T) {
	r := tolerantSeed(t, "sqlite")
	explicit := tolerantShares()
	explicit[0].Status = "revoked"
	if err := r.SyncSharedLandings(explicit, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	a := tolerantRow(t, r, 5)
	tr := NewSharedVanishTracker()
	t0 := time.Now()
	list := without(tolerantShares(), 10)
	list[0].Status = "revoked"
	for i := 0; i <= 3; i++ {
		if _, err := r.SyncSharedLandingList(list, t0.Add(time.Duration(i)*time.Minute).UnixMilli(), tr); err != nil {
			t.Fatal(err)
		}
	}
	b := tolerantRow(t, r, 10)
	ca, cb := SharedLandingCaps(&a), SharedLandingCaps(&b)
	if ca.Status != "revoked" || cb.Status != "revoked" || a.Status != b.Status || a.MaxConnections != b.MaxConnections || a.Protocols != b.Protocols {
		t.Fatalf("paths differ: %+v / %+v", a, b)
	}
	// An explicit revocation with otherwise unusable fields still revokes.
	r2 := tolerantSeed(t, "sqlite")
	broken := tolerantShares()
	broken[3] = landingcustom.Share{ID: 16, Status: "revoked"}
	if _, err := r2.SyncSharedLandingList(broken, time.Now().UnixMilli(), NewSharedVanishTracker()); err != nil {
		t.Fatal(err)
	}
	if row := tolerantRow(t, r2, 16); SharedLandingCaps(&row).Status != "revoked" || row.PublicHost != "198.51.100.16" {
		t.Fatalf("explicit hostless revoke: %s %s", row.SharedCaps, row.PublicHost)
	}
}

// 3 of 4 vanish at once, or an empty list: nothing is revoked however long it
// lasts; held rows carry the error (and the handler's ⚠).
func TestSharedVanishMassGuard(t *testing.T) {
	cases := map[string][]int64{"three-of-four": {5, 10, 12}, "empty": {5, 10, 12, 16}}
	for name, gone := range cases {
		t.Run(name, func(t *testing.T) {
			r := tolerantSeed(t, "sqlite")
			tr := NewSharedVanishTracker()
			t0 := time.Now()
			list := without(tolerantShares(), gone...)
			for i := 0; i < 10; i++ {
				rep, err := r.SyncSharedLandingList(list, t0.Add(time.Duration(i)*time.Minute).UnixMilli(), tr)
				if err != nil || len(rep.Revoked) != 0 || len(rep.Held) != len(gone) {
					t.Fatalf("round %d: %+v %v", i, rep, err)
				}
			}
			for _, remote := range gone {
				row := tolerantRow(t, r, remote)
				if SharedLandingCaps(&row).Status != "active" || !strings.Contains(row.SharedSyncError, "为防误撤销") {
					t.Fatalf("remote %d: %s %q", remote, SharedLandingCaps(&row).Status, row.SharedSyncError)
				}
			}
			// Custom recovers: everything is back, nothing was lost, holds end.
			rep, err := r.SyncSharedLandingList(tolerantShares(), t0.Add(11*time.Minute).UnixMilli(), tr)
			if err != nil || len(rep.Held) != 0 || len(rep.Revoked) != 0 {
				t.Fatalf("recovery: %+v %v", rep, err)
			}
		})
	}
	// Exactly half (2 of 4) is not "more than half": counted normally.
	r := tolerantSeed(t, "sqlite")
	tr := NewSharedVanishTracker()
	t0 := time.Now()
	var rep SharedListReport
	for i := 0; i <= 3; i++ {
		rep, _ = r.SyncSharedLandingList(without(tolerantShares(), 12, 16), t0.Add(time.Duration(i)*time.Minute).UnixMilli(), tr)
	}
	if len(rep.Revoked) != 2 {
		t.Fatalf("2 of 4 should revoke after the threshold: %+v", rep)
	}
}

// not_found ×3 (spanning ≥3 min) revokes at the next list round even while the
// list still carries a (broken) item; a success in between resets the streak.
func TestSharedVanishNotFoundStreak(t *testing.T) {
	r := tolerantSeed(t, "sqlite")
	s := tolerantRow(t, r, 12)
	tr := NewSharedVanishTracker()
	t0 := time.Now()
	list := tolerantShares()
	list[2].Hosts = landingcustom.Hosts{} // the incident: hostless stale item
	tr.NotFound(s.ID, t0)
	tr.NotFound(s.ID, t0.Add(15*time.Second))
	tr.StatusOK(s.ID) // reset
	tr.NotFound(s.ID, t0.Add(time.Minute))
	tr.NotFound(s.ID, t0.Add(90*time.Second))
	tr.NotFound(s.ID, t0.Add(2*time.Minute))
	if rep, _ := r.SyncSharedLandingList(list, t0.Add(2*time.Minute).UnixMilli(), tr); len(rep.Revoked) != 0 {
		t.Fatalf("revoked before 3 min of not_found: %+v", rep)
	}
	tr.NotFound(s.ID, t0.Add(4*time.Minute))
	rep, err := r.SyncSharedLandingList(list, t0.Add(4*time.Minute).UnixMilli(), tr)
	if err != nil || len(rep.Revoked) != 1 || rep.Revoked[0] != s.ID {
		t.Fatalf("not_found streak did not revoke: %+v %v", rep, err)
	}
	if row := tolerantRow(t, r, 12); SharedLandingCaps(&row).Status != "revoked" {
		t.Fatalf("status %s", SharedLandingCaps(&row).Status)
	}
}

// A share that reappears before the threshold starts from zero next time.
func TestSharedVanishReappearResets(t *testing.T) {
	r := tolerantSeed(t, "sqlite")
	tr := NewSharedVanishTracker()
	t0 := time.Now()
	at := func(m int) int64 { return t0.Add(time.Duration(m) * time.Minute).UnixMilli() }
	list := without(tolerantShares(), 5)
	for _, m := range []int{0, 1, 2} {
		if rep, _ := r.SyncSharedLandingList(list, at(m), tr); len(rep.Revoked) != 0 {
			t.Fatal("early")
		}
	}
	rep, _ := r.SyncSharedLandingList(tolerantShares(), at(3), tr) // back
	if len(rep.Missing) != 0 {
		t.Fatalf("still missing after reappearing: %+v", rep)
	}
	for _, m := range []int{4, 5, 6} {
		if rep, _ := r.SyncSharedLandingList(list, at(m), tr); len(rep.Revoked) != 0 {
			t.Fatalf("minute %d: counter was not reset", m)
		}
	}
	if rep, _ := r.SyncSharedLandingList(list, at(7), tr); len(rep.Revoked) != 1 {
		t.Fatalf("fresh streak should revoke at 3 rounds / 3 min: %+v", rep)
	}
}
