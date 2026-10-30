package repo

import (
	"database/sql"
	"testing"

	"go-backend/internal/store/model"
)

// Plan 168: the inputs of a node's entry rate tree (active forwards entering
// it with their own limit resolved like resolveForwardRuntimeInputs) and the
// agent's entry rate mode.

func TestEntryRateRulesAndMode(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := poolRepoFixture(t, dialect)
			n := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
			orphanSeed(t, r,
				&model.SpeedLimit{ID: 1, Name: "fifty", Speed: 50, Status: 1, CreatedTime: 1},
				&model.SpeedLimit{ID: 2, Name: "arena", Speed: 30, ArenaMode: 1, Status: 1, CreatedTime: 1},
				&model.SpeedLimit{ID: 3, Name: "zero", Speed: 0, Status: 1, CreatedTime: 1},
				&model.UserTunnel{ID: 10, UserID: 2, TunnelID: 1, SpeedID: n(2), Num: 1, Flow: 1, FlowResetTime: 1, ExpTime: 1, Status: 1},
				&model.UserTunnel{ID: 11, UserID: 3, TunnelID: 1, Num: 1, Flow: 1, FlowResetTime: 1, ExpTime: 1, Status: 1},
			)
			fwd := func(id, user int64, status int, mode string, speedID sql.NullInt64, dyn int) *model.Forward {
				return &model.Forward{ID: id, UserID: user, UserName: "u", Name: "f", TunnelID: 1, RemoteAddr: "1.1.1.1:1", CreatedTime: 1, UpdatedTime: 1, Status: status, Mode: mode, SpeedID: speedID, SpeedLimitEnabled: dyn > 0, SpeedLimit: dyn}
			}
			orphanSeed(t, r,
				fwd(100, 2, 1, "gost", sql.NullInt64{}, 0), // user tunnel's arena 2
				fwd(101, 2, 1, "nftables", n(1), 0),        // own 限速规则 1 (50)
				fwd(102, 3, 1, "gost", sql.NullInt64{}, 8), // dynamic 8, no user tunnel limit
				fwd(103, 3, 0, "gost", n(1), 0),            // paused: not listed
				fwd(104, 3, 1, "gost", n(3), 0),            // 限速规则 speed 0 → user tunnel's (none)
				&model.ForwardPort{ForwardID: 100, NodeID: 1, Port: 1500},
				&model.ForwardPort{ForwardID: 101, NodeID: 1, Port: 1501},
				&model.ForwardPort{ForwardID: 102, NodeID: 1, Port: 1502},
				&model.ForwardPort{ForwardID: 103, NodeID: 1, Port: 1503},
				&model.ForwardPort{ForwardID: 104, NodeID: 1, Port: 1504},
				&model.ForwardPort{ForwardID: 100, NodeID: 4, Port: 1600}, // other entry node
			)
			rows, err := r.EntryRateRules(1)
			if err != nil {
				t.Fatal(err)
			}
			got := map[int64]EntryRateRuleRow{}
			for _, row := range rows {
				got[row.ForwardID] = row
			}
			if len(got) != 4 {
				t.Fatalf("rows %+v", rows)
			}
			if g := got[100]; g.Port != 1500 || g.LimitID != 2 || g.LimitSpeed != 30 || !g.LimitArena || g.UserID != 2 {
				t.Fatalf("100 %+v", g)
			}
			if g := got[101]; g.LimitID != 1 || g.LimitSpeed != 50 || g.LimitArena || g.Mode != "nftables" {
				t.Fatalf("101 %+v", g)
			}
			if g := got[102]; !g.SpeedLimitEnabled || g.SpeedLimit != 8 || g.LimitID != 0 {
				t.Fatalf("102 %+v", g)
			}
			if g := got[104]; g.LimitID != 0 {
				t.Fatalf("104 %+v", g)
			}
			if rows, _ := r.EntryRateRules(6); len(rows) != 0 {
				t.Fatalf("unrelated node %+v", rows)
			}
			// mode / error: written only when changed, listed by mode
			if err := r.SetNodeEntryRateCapability(1, 1, ""); err != nil {
				t.Fatal(err)
			}
			if err := r.SetNodeEntryRateCapability(4, 2, "内核不支持 ifb：operation not permitted"); err != nil {
				t.Fatal(err)
			}
			modes, err := r.NodeEntryRateModes([]int64{1, 4, 5})
			if err != nil {
				t.Fatal(err)
			}
			if modes[1].EntryRateTc != 1 || modes[4].EntryRateTc != 2 || modes[4].EntryRateError == "" || modes[5].EntryRateTc != 0 || modes[1].Name != "HK-entry" {
				t.Fatalf("modes %+v", modes)
			}
			if ids, err := r.ListNodeIDsByEntryRateMode(1); err != nil || len(ids) != 1 || ids[0] != 1 {
				t.Fatalf("by mode %v %v", ids, err)
			}
			// related servers: entry node 4 falls back (kernel), node 1 is fine
			long := ""
			for i := 0; i < 400; i++ {
				long += "错"
			}
			if err := r.SetNodeEntryRateCapability(1, 1, long); err != nil {
				t.Fatal(err)
			}
			modes, _ = r.NodeEntryRateModes([]int64{1})
			if len([]rune(modes[1].EntryRateError)) != 300 {
				t.Fatalf("error not capped: %d", len([]rune(modes[1].EntryRateError)))
			}
		})
	}
}
