package usersub

import (
	"regexp"
	"testing"

	"go-backend/internal/store/model"
)

const now = int64(1_800_000_000_000)

func facts() *Facts {
	return &Facts{
		Now: now,
		Nodes: map[int64]Node{
			51: {ID: 51, Enabled: true, MainDomain: "vmsilo-huri.imgamer.top", MainSynced: true, ZoneKey: "1|z"},
			59: {ID: 59, Enabled: true, MainDomain: "vmsilo-humei.imgamer.top", MainSynced: true, ZoneKey: "1|z"},
			60: {ID: 60, Enabled: false, MainDomain: "x.imgamer.top", MainSynced: true, ZoneKey: "1|z"},
		},
		Usage: map[int64]map[int64]bool{51: {2: true, 3: true, 4: true, 1: true}, 59: {2: true}, 60: {2: true}},
		Users: map[int64]User{
			1: {ID: 1, Admin: true, Status: 1},
			2: {ID: 2, Status: 1},
			3: {ID: 3, Status: 0},                   // 停用
			4: {ID: 4, Status: 1, ExpTime: now - 1}, // 到期 (job not run yet)
			5: {ID: 5, Status: 1},
		},
	}
}

func name(id, user, node int64, state, reason string) model.UserSubdomain {
	return model.UserSubdomain{ID: id, UserID: user, NodeID: node, OriginNodeID: node, State: state, Reason: reason, Label: "abc"}
}

func TestDesiredStates(t *testing.T) {
	f := facts()
	cases := []struct {
		n             model.UserSubdomain
		state, reason string
	}{
		{name(1, 2, 51, "active", ""), "active", ""},
		{name(2, 3, 51, "active", ""), "paused", "disabled"},
		{name(3, 4, 51, "active", ""), "paused", "expired"},
		{name(4, 99, 51, "active", ""), "retired", "user_deleted"},
		{name(5, 2, 77, "active", ""), "retired", "node_deleted"},
		{name(6, 2, 60, "active", ""), "paused", "node"},
		{name(7, 5, 51, "active", ""), "active", ""},                 // round 2: no forward on the node → stays live
		{name(10, 5, 51, "paused", "unused"), "active", ""},          // round-1 paused(unused) row comes back
		{name(11, 2, 59, "paused", "disabled"), "active", ""},        // 启用
		{name(12, 4, 59, "paused", "disabled"), "paused", "expired"}, // expired wins over the job's 停用
		{name(8, 3, 51, "paused", "disabled"), "paused", "disabled"},
		{name(9, 2, 51, "paused", "expired"), "active", ""}, // 续期
	}
	for _, c := range cases {
		state, reason, since := Desired(f, c.n)
		if state != c.state || reason != c.reason || since != 0 {
			t.Errorf("name %d: got %s/%s/%d want %s/%s", c.n.ID, state, reason, since, c.state, c.reason)
		}
	}
	// A name never needs its user to keep a forward on the node: empty
	// usage changes nothing for live names, only stops new ones.
	f.Usage = map[int64]map[int64]bool{}
	if s, r, _ := Desired(f, name(13, 2, 51, "active", "")); s != "active" || r != "" {
		t.Fatalf("no usage: %s/%s", s, r)
	}
	if p := Compute(f); len(p.Creates) != 0 || len(p.Changes) != 0 {
		t.Fatalf("no usage: %+v", p)
	}
}

func TestComputeCreatesAndSkips(t *testing.T) {
	f := facts()
	// user 2 already has a paused name on 59 → no new name there.
	f.Names = []model.UserSubdomain{name(1, 2, 59, "paused", "disabled")}
	p := Compute(f)
	// Round 3: every non-admin user using the node gets his name reserved at
	// once — 停用 3 / 到期 4 paused; admin 1 skipped; node 60 off.
	want := []Create{{UserID: 2, NodeID: 51, State: "active"}, {UserID: 3, NodeID: 51, State: "paused", Reason: "disabled"},
		{UserID: 4, NodeID: 51, State: "paused", Reason: "expired"}}
	if len(p.Creates) != len(want) {
		t.Fatalf("creates: %+v", p.Creates)
	}
	for i := range want {
		if p.Creates[i] != want[i] {
			t.Fatalf("create %d: %+v want %+v", i, p.Creates[i], want[i])
		}
	}
	if len(p.Changes) != 1 || p.Changes[0].State != "active" {
		t.Fatalf("changes: %+v", p.Changes)
	}
	// Admin-deleted tombstone blocks a new name for that pair only.
	f.Usage = map[int64]map[int64]bool{51: {2: true}, 59: {2: true}}
	f.Names = []model.UserSubdomain{{ID: 3, UserID: 2, NodeID: 51, State: "retired", Reason: "admin", Blocked: true}}
	p = Compute(f)
	if len(p.Creates) != 1 || p.Creates[0].NodeID != 59 {
		t.Fatalf("blocked: %+v", p.Creates)
	}
	// A moved-in name (origin 51, now → 59) covers (user, 59).
	moved := name(4, 2, 59, "active", "")
	moved.OriginNodeID = 51
	f.Names = []model.UserSubdomain{moved}
	p = Compute(f)
	if len(p.Creates) != 1 || p.Creates[0].NodeID != 51 {
		t.Fatalf("moved: %+v", p.Creates)
	}
}

// Round 3: the cap limits Cloudflare records, never the reservation.
func TestAdmitAndHidden(t *testing.T) {
	rows := []model.UserSubdomain{{ID: 7}, {ID: 3}, {ID: 9, ReadyAt: 5}, {ID: 5}}
	admit, capped, unknown := Admit(rows, 2, true)
	if len(admit) != 2 || admit[0] != 9 || admit[1] != 3 || len(capped) != 2 || capped[0] != 5 || capped[1] != 7 || len(unknown) != 0 {
		t.Fatalf("room 2: %v %v %v", admit, capped, unknown) // ready-before first, then oldest
	}
	admit, capped, _ = Admit(rows, 0, true)
	if len(admit) != 0 || len(capped) != 4 {
		t.Fatalf("room 0: %v %v", admit, capped)
	}
	admit, capped, unknown = Admit(rows, 0, false)
	if len(admit) != 1 || admit[0] != 9 || len(capped) != 0 || len(unknown) != 3 {
		t.Fatalf("unknown room: %v %v %v", admit, capped, unknown)
	}
	if Room(37, 200) != 158 || Room(199, 200) != 0 {
		t.Fatal("room")
	}
	cases := []struct {
		n    model.UserSubdomain
		hide bool
	}{
		{model.UserSubdomain{State: "active", Status: "pending"}, false},
		{model.UserSubdomain{State: "active", Status: "capped"}, true},
		{model.UserSubdomain{State: "active", Status: "failed", FailCount: 2}, false},
		{model.UserSubdomain{State: "active", Status: "failed", FailCount: FailShow}, true},
		{model.UserSubdomain{State: "active", Status: "capped", ReadyAt: 1}, false}, // the user already has it
		{model.UserSubdomain{State: "active", Status: "failed", FailCount: 9, ReadyAt: 1}, false},
		{model.UserSubdomain{State: "paused", Reason: "expired", Status: "synced"}, false},
		{model.UserSubdomain{State: "retired", Blocked: true, Status: "capped"}, false},
	}
	for i, c := range cases {
		if Hidden(c.n) != c.hide {
			t.Errorf("case %d: Hidden=%v", i, !c.hide)
		}
	}
}

func TestNewLabel(t *testing.T) {
	re := regexp.MustCompile(`^[a-km-np-z][a-km-np-z2-9]{6}$`)
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		l := NewLabel()
		if !re.MatchString(l) {
			t.Fatalf("label %q", l)
		}
		seen[l] = true
	}
	if len(seen) < 1990 {
		t.Fatalf("labels repeat too often: %d", len(seen))
	}
}
