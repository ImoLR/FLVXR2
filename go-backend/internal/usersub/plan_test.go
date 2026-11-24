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
		Room: map[string]int{"1|z": 100},
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
	if len(p.Creates) != 1 || p.Creates[0] != (Create{UserID: 2, NodeID: 51}) {
		t.Fatalf("creates: %+v", p.Creates) // admin 1, 停用 3, 到期 4 skipped; node 60 off
	}
	if len(p.Changes) != 1 || p.Changes[0].State != "active" {
		t.Fatalf("changes: %+v", p.Changes)
	}
	// Admin-deleted tombstone blocks a new name for that pair only.
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

func TestComputeQuota(t *testing.T) {
	f := facts()
	f.Usage[51] = map[int64]bool{2: true, 5: true}
	f.Room["1|z"] = 1
	p := Compute(f)
	if len(p.Creates) != 1 || p.Capped[51] != 1 || p.Capped[59] != 1 {
		t.Fatalf("quota: creates %+v capped %+v", p.Creates, p.Capped)
	}
	delete(f.Room, "1|z")
	p = Compute(f)
	if len(p.Creates) != 0 || p.Waiting[51] != 2 {
		t.Fatalf("unknown zone: %+v %+v", p.Creates, p.Waiting)
	}
	if Room(37, 200) != 158 || Room(199, 200) != 0 {
		t.Fatal("room")
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
