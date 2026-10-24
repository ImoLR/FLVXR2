package repo

import (
	"database/sql"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

func TestLandingRuleSpecMath(t *testing.T) {
	cases := []struct {
		name                       string
		spec, carved               string
		oldLo, oldHi, newLo, newHi int
		held                       []int
		want, wantCarved           string
	}{
		{"carve middle", "10000-65535", "", 0, 0, 30000, 39999, nil, "10000-29999,40000-65535", "30000-39999"},
		{"carve node55", "10000-65535", "", 0, 0, 30001, 39999, nil, "10000-30000,40000-65535", "30001-39999"},
		{"carve comma spec (already disjoint)", "10000-29999,31000-65535", "", 0, 0, 30000, 30999, nil, "10000-29999,31000-65535", ""},
		{"carve across comma spec", "1000-2000,10000-35000,36000-50000", "", 0, 0, 30000, 39999, nil, "1000-2000,10000-29999,40000-50000", "30000-35000,36000-39999"},
		{"carve low edge", "10000-65535", "", 0, 0, 10000, 19999, nil, "20000-65535", "10000-19999"},
		{"carve high edge", "10000-65535", "", 0, 0, 60000, 65535, nil, "10000-59999", "60000-65535"},
		{"carve single port remains", "10000-30000", "", 0, 0, 10001, 30000, nil, "10000", "10001-30000"},
		{"outside rule range is no-op", "10000-29999", "", 0, 0, 40000, 49999, nil, "10000-29999", ""},
		{"partly outside records only the carved part", "10000-45000", "", 0, 0, 40000, 49999, nil, "10000-39999", "40000-45000"},
		{"empty spec stays empty", "", "", 0, 0, 30000, 39999, nil, "", ""},
		{"disable gives back middle", "10000-29999,40000-65535", "30000-39999", 30000, 39999, 0, 0, nil, "10000-65535", ""},
		{"disable gives back middle (pre-147, hull)", "10000-29999,40000-65535", "", 30000, 39999, 0, 0, nil, "10000-65535", ""},
		{"disable keeps held ports recorded", "10000-29999,40000-65535", "30000-39999", 30000, 39999, 0, 0, []int{30000, 30005}, "10000-29999,30001-30004,30006-65535", "30000,30005"},
		{"disable outside hull gives nothing", "10000-29999", "", 30000, 39999, 0, 0, nil, "10000-29999", ""},
		{"disable high edge recorded", "10000-59999", "60000-65535", 60000, 65535, 0, 0, nil, "10000-65535", ""},
		{"disable low edge recorded", "20000-65535", "10000-19999", 10000, 19999, 0, 0, nil, "10000-65535", ""},
		{"disable high edge pre-147 cannot know", "10000-59999", "", 60000, 65535, 0, 0, nil, "10000-59999", ""},
		{"disable never-carved edge invents nothing", "10000-59999", "", 60000, 65535, 0, 0, []int{60001}, "10000-59999", ""},
		{"disable edge comma spec", "1000-2000,10000-59999", "60000-65535", 60000, 65535, 0, 0, nil, "1000-2000,10000-65535", ""},
		{"disable edge partly carved", "10000-45000", "", 0, 0, 40000, 49999, nil, "10000-39999", "40000-45000"},
		{"disable gives back only the carved part", "10000-39999", "40000-45000", 40000, 49999, 0, 0, nil, "10000-45000", ""},
		{"shrink at high edge", "10000-59999", "60000-65535", 60000, 65535, 62000, 65535, nil, "10000-61999", "62000-65535"},
		{"grow at high edge carves again", "10000-61999", "62000-65535", 62000, 65535, 60000, 65535, nil, "10000-59999", "60000-65535"},
		{"move range", "10000-29999,40000-65535", "30000-39999", 30000, 39999, 35000, 44999, nil, "10000-34999,45000-65535", "35000-44999"},
		{"move edge range to middle", "10000-59999", "60000-65535", 60000, 65535, 30000, 30999, nil, "10000-29999,31000-65535", "30000-30999"},
		{"shrink range", "10000-29999,40000-65535", "30000-39999", 30000, 39999, 30000, 30999, nil, "10000-29999,31000-65535", "30000-30999"},
		{"unchanged range", "10000-29999,40000-65535", "30000-39999", 30000, 39999, 30000, 39999, nil, "10000-29999,40000-65535", "30000-39999"},
		{"re-enable keeps held record", "10000-29999,30001-65535", "30000", 0, 0, 30000, 39999, nil, "10000-29999,40000-65535", "30000-39999"},
	}
	for _, c := range cases {
		held := map[int]bool{}
		for _, p := range c.held {
			held[p] = true
		}
		got, carved := landingRuleSpec(c.spec, c.carved, c.oldLo, c.oldHi, c.newLo, c.newHi, held)
		if got != c.want || carved != c.wantCarved {
			t.Errorf("%s: got %q carved %q, want %q carved %q", c.name, got, carved, c.want, c.wantCarved)
		}
	}
	if got := formatPortSpans(parsePortSpans(" 300 , 100-200, 201-250,5")); got != "5,100-250,300" {
		t.Fatalf("normalize: %q", got)
	}
}

func nativePortsFixture(t *testing.T, dialect, spec string) *Repository {
	t.Helper()
	r := orphanTestRepo(t, dialect)
	orphanSeed(t, r, &model.Node{ID: 655, Name: "NexKR", ServerIP: "203.0.113.55", Port: spec, LandingCapable: true, Status: 1})
	return r
}

func nodeSpec(t *testing.T, r *Repository, id int64) string {
	t.Helper()
	var n model.Node
	if err := r.db.First(&n, id).Error; err != nil {
		t.Fatal(err)
	}
	return n.Port
}

func TestNativeLandingRangeCarveAndGiveBack(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := nativePortsFixture(t, dialect, "10000-65535")
			s, err := r.EnableLandingServer(655)
			if err != nil {
				t.Fatal(err)
			}
			if got := nodeSpec(t, r, 655); got != "10000-29999,40000-65535" {
				t.Fatalf("enable carve: %q", got)
			}
			n, err := r.CreateLandingNode(1, s.ID, "first", "ss2022", true)
			if err != nil || n.Port != 30000 {
				t.Fatalf("first node: %+v %v", n, err)
			}
			// Node edit may not re-widen the rule range over the landing range.
			err = r.ValidateNodeLandingPortRange(655, "10000-65535")
			if err == nil || !strings.Contains(err.Error(), "端口 30000-39999 已留给落地") || !strings.Contains(err.Error(), "10000-29999,40000-65535") {
				t.Fatalf("node edit overlap: %v", err)
			}
			if err := r.ValidateNodeLandingPortRange(655, "10000-29999,40000-60000"); err != nil {
				t.Fatalf("disjoint node edit refused: %v", err)
			}
			// Shrink: the ports that left the landing range come back.
			s.PortMin, s.PortMax = 30000, 30999
			if err := r.SaveLandingServer(s, nil); err != nil {
				t.Fatal(err)
			}
			if got := nodeSpec(t, r, 655); got != "10000-29999,31000-65535" {
				t.Fatalf("shrink give-back: %q", got)
			}
			// Moving over a port a forward already uses is refused, nothing moves.
			orphanSeed(t, r, &model.ForwardPort{ForwardID: 902, NodeID: 655, Port: 45001}, &model.ChainTunnel{TunnelID: 901, NodeID: 655, ChainType: "2", Port: sql.NullInt64{Int64: 45002, Valid: true}})
			s.PortMin, s.PortMax = 30000, 49999
			err = r.SaveLandingServer(s, nil)
			if err == nil || !strings.Contains(err.Error(), "45001、45002（共 2 个）") {
				t.Fatalf("conflict refusal: %v", err)
			}
			if got := nodeSpec(t, r, 655); got != "10000-29999,31000-65535" {
				t.Fatalf("refused save changed spec: %q", got)
			}
			// Disable gives the range back, except the port the landing node holds.
			fresh, _ := r.GetLandingServer(s.ID)
			fresh.Status = "disabled"
			if err := r.SaveLandingServer(fresh, nil); err != nil {
				t.Fatal(err)
			}
			if got := nodeSpec(t, r, 655); got != "10000-29999,30001-65535" {
				t.Fatalf("disable give-back: %q", got)
			}
			if err := r.ValidateNodeLandingPortRange(655, "10000-29999,30001-65535"); err != nil {
				t.Fatalf("disabled landing still reserves range: %v", err)
			}
			// Re-enable carves again.
			fresh, _ = r.GetLandingServer(s.ID)
			fresh.Status = "active"
			if err := r.SaveLandingServer(fresh, nil); err != nil {
				t.Fatal(err)
			}
			if got := nodeSpec(t, r, 655); got != "10000-29999,31000-65535" {
				t.Fatalf("re-enable carve: %q", got)
			}
		})
	}
}

func TestNativeLandingEnableRefusesUsedPorts(t *testing.T) {
	r := nativePortsFixture(t, "sqlite", "10000-65535")
	orphanSeed(t, r, &model.ForwardPort{ForwardID: 1, NodeID: 655, Port: 30010})
	_, err := r.EnableLandingServer(655)
	if err == nil || !strings.Contains(err.Error(), "30010") || !strings.Contains(err.Error(), "NexKR") {
		t.Fatalf("enable over a used port: %v", err)
	}
	if got := nodeSpec(t, r, 655); got != "10000-65535" {
		t.Fatalf("refused enable changed spec: %q", got)
	}
	var count int64
	r.db.Model(&model.LandingServer{}).Count(&count)
	if count != 0 {
		t.Fatal("refused enable created a server")
	}
}

func TestLandingNoFreePortMessage(t *testing.T) {
	r := nativePortsFixture(t, "sqlite", "10000-29999")
	s, err := r.EnableLandingServer(655)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy state: rule range widened behind the panel's back.
	if err := r.db.Model(&model.Node{}).Where("id = 655").Update("port", "10000-65535").Error; err != nil {
		t.Fatal(err)
	}
	_, err = r.CreateLandingNode(1, s.ID, "x", "ss2022", true)
	if err == nil || !strings.Contains(err.Error(), "落地端口段 30000-39999 已全部被节点「NexKR」的规则端口段 10000-65535 占用") {
		t.Fatalf("message: %v", err)
	}
}

// Plan 147: a landing range at an outer edge of the rule range comes back on
// disable / move because the carve is recorded; a range the node never had in
// its rule range is never added (nothing recorded, outside the hull).
func TestNativeLandingEdgeGiveBack(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := nativePortsFixture(t, dialect, "1000-2000,10000-65535")
			s, err := r.EnableLandingServer(655)
			if err != nil {
				t.Fatal(err)
			}
			if s.RuleCarved != "30000-39999" || nodeSpec(t, r, 655) != "1000-2000,10000-29999,40000-65535" {
				t.Fatalf("enable: carved %q spec %q", s.RuleCarved, nodeSpec(t, r, 655))
			}
			save := func(lo, hi int, status string) {
				t.Helper()
				cur, _ := r.GetLandingServer(s.ID)
				cur.PortMin, cur.PortMax, cur.Status = lo, hi, status
				if err := r.SaveLandingServer(cur, nil); err != nil {
					t.Fatal(err)
				}
			}
			carved := func() string {
				t.Helper()
				cur, _ := r.GetLandingServer(s.ID)
				return cur.RuleCarved
			}
			save(60000, 65535, "active")
			if got := nodeSpec(t, r, 655); got != "1000-2000,10000-59999" || carved() != "60000-65535" {
				t.Fatalf("move to high edge: %q carved %q", got, carved())
			}
			save(60000, 65535, "disabled")
			if got := nodeSpec(t, r, 655); got != "1000-2000,10000-65535" || carved() != "" {
				t.Fatalf("disable high edge: %q carved %q", got, carved())
			}
			save(60000, 65535, "active")
			save(62000, 65535, "active")
			if got := nodeSpec(t, r, 655); got != "1000-2000,10000-61999" || carved() != "62000-65535" {
				t.Fatalf("shrink at edge: %q carved %q", got, carved())
			}
			save(30000, 30999, "active")
			if got := nodeSpec(t, r, 655); got != "1000-2000,10000-29999,31000-65535" || carved() != "30000-30999" {
				t.Fatalf("edge → middle: %q carved %q", got, carved())
			}

			// A range the node never had: rule 10000-59999, landing 60000-65535.
			r2 := nativePortsFixture(t, dialect, "10000-59999")
			s2, err := r2.EnableLandingServer(655)
			if err != nil {
				t.Fatal(err)
			}
			cur, _ := r2.GetLandingServer(s2.ID)
			cur.PortMin, cur.PortMax = 60000, 65535
			if err := r2.SaveLandingServer(cur, nil); err != nil {
				t.Fatal(err)
			}
			if got := nodeSpec(t, r2, 655); got != "10000-59999" {
				t.Fatalf("never-had range: %q", got)
			}
			cur, _ = r2.GetLandingServer(s2.ID)
			cur.Status = "disabled"
			if err := r2.SaveLandingServer(cur, nil); err != nil {
				t.Fatal(err)
			}
			if got := nodeSpec(t, r2, 655); got != "10000-59999" {
				t.Fatalf("invented ports on disable: %q", got)
			}
		})
	}
}
