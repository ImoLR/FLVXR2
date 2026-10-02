package repo

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

func openUserFlowResetRepo(t *testing.T) *Repository {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func mustExecUserFlowReset(t *testing.T, r *Repository, query string, args ...interface{}) {
	t.Helper()
	if err := r.DB().Exec(query, args...).Error; err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// seedUserFlowResetData creates two users (2 resets on day 15, 3 on day 20), each with a
// user_tunnel and forwards; forward 22 has no traffic.
func seedUserFlowResetData(t *testing.T, r *Repository, nowMs int64) {
	t.Helper()
	old := nowMs - 3*24*3600*1000
	mustExecUserFlowReset(t, r, `INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx)
		VALUES(1, 't1', 1.0, 1, 'tls', 2, ?, ?, 1, NULL, 0)`, nowMs, nowMs)
	mustExecUserFlowReset(t, r, `INSERT INTO user(id, user, pwd, role_id, exp_time, flow, in_flow, out_flow, flow_reset_time, num, created_time, updated_time, status)
		VALUES(2, 'u2', 'x', 1, 2727251700000, 100, 1000, 2000, 15, 10, ?, ?, 1)`, old, old)
	mustExecUserFlowReset(t, r, `INSERT INTO user(id, user, pwd, role_id, exp_time, flow, in_flow, out_flow, flow_reset_time, num, created_time, updated_time, status)
		VALUES(3, 'u3', 'x', 1, 2727251700000, 100, 5000, 6000, 20, 10, ?, ?, 1)`, old, old)
	mustExecUserFlowReset(t, r, `INSERT INTO user_tunnel(id, user_id, tunnel_id, speed_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status)
		VALUES(10, 2, 1, NULL, 10, 100, 300, 400, 0, 2727251700000, 1)`)
	mustExecUserFlowReset(t, r, `INSERT INTO user_tunnel(id, user_id, tunnel_id, speed_id, num, flow, in_flow, out_flow, flow_reset_time, exp_time, status)
		VALUES(11, 3, 1, NULL, 10, 100, 700, 800, 0, 2727251700000, 1)`)
	for _, f := range []struct {
		id, userID, in, out int64
		userName, name      string
	}{
		{20, 2, 100, 200, "u2", "f20"},
		{21, 2, 30, 40, "u2", "f21"},
		{22, 2, 0, 0, "u2", "f22"},
		{30, 3, 500, 600, "u3", "f30"},
	} {
		mustExecUserFlowReset(t, r, `INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx)
			VALUES(?, ?, ?, ?, 1, '1.1.1.1:443', 'fifo', ?, ?, ?, ?, 1, 0)`, f.id, f.userID, f.userName, f.name, f.in, f.out, old, old)
	}
}

func forwardFlowOf(t *testing.T, r *Repository, id int64) (int64, int64) {
	t.Helper()
	var f model.Forward
	if err := r.DB().Select("in_flow", "out_flow").Where("id = ?", id).First(&f).Error; err != nil {
		t.Fatalf("load forward %d: %v", id, err)
	}
	return f.InFlow, f.OutFlow
}

func forwardResetLogsOf(t *testing.T, r *Repository, forwardID int64) []model.ForwardTrafficResetLog {
	t.Helper()
	var logs []model.ForwardTrafficResetLog
	if err := r.DB().Where("forward_id = ?", forwardID).Order("id ASC").Find(&logs).Error; err != nil {
		t.Fatalf("load reset logs of forward %d: %v", forwardID, err)
	}
	return logs
}

func TestResetUserFlowByUserAlsoResetsForwards(t *testing.T) {
	r := openUserFlowResetRepo(t)
	nowMs := time.Now().UnixMilli()
	seedUserFlowResetData(t, r, nowMs)

	if err := r.ResetUserFlowByUser(2, nowMs, 1, "admin"); err != nil {
		t.Fatalf("reset user: %v", err)
	}

	var user model.User
	if err := r.DB().Where("id = 2").First(&user).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.InFlow != 0 || user.OutFlow != 0 {
		t.Fatalf("user flow not cleared: in=%d out=%d", user.InFlow, user.OutFlow)
	}
	var ut model.UserTunnel
	if err := r.DB().Where("id = 10").First(&ut).Error; err != nil {
		t.Fatalf("load user_tunnel: %v", err)
	}
	if ut.InFlow != 0 || ut.OutFlow != 0 {
		t.Fatalf("user_tunnel flow not cleared: in=%d out=%d", ut.InFlow, ut.OutFlow)
	}
	for _, id := range []int64{20, 21, 22} {
		if in, out := forwardFlowOf(t, r, id); in != 0 || out != 0 {
			t.Fatalf("forward %d flow not cleared: in=%d out=%d", id, in, out)
		}
	}

	logs := forwardResetLogsOf(t, r, 20)
	if len(logs) != 1 {
		t.Fatalf("expected 1 reset log for forward 20, got %d", len(logs))
	}
	got := logs[0]
	if got.InFlowBefore != 100 || got.OutFlowBefore != 200 || got.UserID != 2 || got.UserName != "u2" ||
		got.ForwardName != "f20" || got.OperatorID != 1 || got.OperatorName != "admin" || got.ResetTime != nowMs ||
		!strings.HasPrefix(got.Reason, UserResetForwardReasonPrefix) {
		t.Fatalf("unexpected reset log for forward 20: %+v", got)
	}
	if logs := forwardResetLogsOf(t, r, 21); len(logs) != 1 || logs[0].InFlowBefore != 30 || logs[0].OutFlowBefore != 40 {
		t.Fatalf("unexpected reset logs for forward 21: %+v", logs)
	}
	if logs := forwardResetLogsOf(t, r, 22); len(logs) != 0 {
		t.Fatalf("forward without traffic must not get a reset log, got %+v", logs)
	}

	// The other user's rules, user_tunnel and total stay untouched.
	if in, out := forwardFlowOf(t, r, 30); in != 500 || out != 600 {
		t.Fatalf("other user's forward changed: in=%d out=%d", in, out)
	}
	if logs := forwardResetLogsOf(t, r, 30); len(logs) != 0 {
		t.Fatalf("other user's forward got a reset log: %+v", logs)
	}
	var other model.User
	if err := r.DB().Where("id = 3").First(&other).Error; err != nil {
		t.Fatalf("load other user: %v", err)
	}
	if other.InFlow != 5000 || other.OutFlow != 6000 {
		t.Fatalf("other user changed: in=%d out=%d", other.InFlow, other.OutFlow)
	}

	var history []model.UserQuotaHistory
	if err := r.DB().Where("user_id = 2").Find(&history).Error; err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 1 || history[0].InFlowBefore != 1000 || history[0].OutFlowBefore != 2000 {
		t.Fatalf("unexpected user history: %+v", history)
	}

	if err := r.ResetUserFlowByUser(999, nowMs, 1, "admin"); err == nil {
		t.Fatalf("expected an error for a missing user")
	}
}

func TestResetUserFlowByUserTunnelKeepsForwards(t *testing.T) {
	r := openUserFlowResetRepo(t)
	nowMs := time.Now().UnixMilli()
	seedUserFlowResetData(t, r, nowMs)

	r.ResetUserFlowByUserTunnel(10)

	if in, out := forwardFlowOf(t, r, 20); in != 100 || out != 200 {
		t.Fatalf("user_tunnel reset must not touch forwards: in=%d out=%d", in, out)
	}
	if logs := forwardResetLogsOf(t, r, 20); len(logs) != 0 {
		t.Fatalf("user_tunnel reset must not write forward reset logs: %+v", logs)
	}
}

func TestResetUserMonthlyFlowAlsoResetsForwards(t *testing.T) {
	r := openUserFlowResetRepo(t)
	nowMs := time.Now().UnixMilli()
	seedUserFlowResetData(t, r, nowMs)

	snapshots, err := r.ResetUserMonthlyFlow(15, 30, nowMs)
	if err != nil {
		t.Fatalf("monthly reset: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].UserID != 2 || snapshots[0].InFlow != 1000 || snapshots[0].OutFlow != 2000 {
		t.Fatalf("unexpected snapshots: %+v", snapshots)
	}

	var user model.User
	if err := r.DB().Where("id = 2").First(&user).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.InFlow != 0 || user.OutFlow != 0 {
		t.Fatalf("user flow not cleared: in=%d out=%d", user.InFlow, user.OutFlow)
	}
	for _, id := range []int64{20, 21} {
		if in, out := forwardFlowOf(t, r, id); in != 0 || out != 0 {
			t.Fatalf("forward %d flow not cleared: in=%d out=%d", id, in, out)
		}
		logs := forwardResetLogsOf(t, r, id)
		if len(logs) != 1 || logs[0].OperatorName != "system" || logs[0].ResetTime != nowMs ||
			!strings.HasPrefix(logs[0].Reason, UserResetForwardReasonPrefix) {
			t.Fatalf("unexpected reset logs for forward %d: %+v", id, logs)
		}
	}
	// user_tunnel keeps its own reset day.
	var ut model.UserTunnel
	if err := r.DB().Where("id = 10").First(&ut).Error; err != nil {
		t.Fatalf("load user_tunnel: %v", err)
	}
	if ut.InFlow != 300 || ut.OutFlow != 400 {
		t.Fatalf("monthly user reset must not touch user_tunnel: in=%d out=%d", ut.InFlow, ut.OutFlow)
	}
	// User 3 (reset day 20) is not due.
	if in, out := forwardFlowOf(t, r, 30); in != 500 || out != 600 {
		t.Fatalf("forward of a user that is not due changed: in=%d out=%d", in, out)
	}

	// The history rows carry the real user id.
	r.RecordFlowResetHistory(snapshots, 202603, nowMs, "自动周期归零")
	var history []model.UserQuotaHistory
	if err := r.DB().Where("reset_reason = ?", "自动周期归零").Find(&history).Error; err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 1 || history[0].UserID != 2 || history[0].UsedBytes != 3000 {
		t.Fatalf("unexpected monthly history: %+v", history)
	}
}

func TestResetUserFlowToBaseAlsoResetsForwards(t *testing.T) {
	r := openUserFlowResetRepo(t)
	nowMs := time.Now().UnixMilli()
	seedUserFlowResetData(t, r, nowMs)

	if err := r.ResetUserFlowToBase(3, 50, nowMs); err != nil {
		t.Fatalf("reset to base: %v", err)
	}
	var user model.User
	if err := r.DB().Where("id = 3").First(&user).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.InFlow != 0 || user.OutFlow != 0 || user.Flow != 50 {
		t.Fatalf("unexpected user after reset to base: in=%d out=%d flow=%d", user.InFlow, user.OutFlow, user.Flow)
	}
	if in, out := forwardFlowOf(t, r, 30); in != 0 || out != 0 {
		t.Fatalf("forward 30 flow not cleared: in=%d out=%d", in, out)
	}
	if logs := forwardResetLogsOf(t, r, 30); len(logs) != 1 || logs[0].InFlowBefore != 500 || logs[0].OutFlowBefore != 600 {
		t.Fatalf("unexpected reset logs for forward 30: %+v", logs)
	}
	if in, out := forwardFlowOf(t, r, 20); in != 100 || out != 200 {
		t.Fatalf("other user's forward changed: in=%d out=%d", in, out)
	}
}

func TestUserResetKeepsForwardResetLogBounded(t *testing.T) {
	r := openUserFlowResetRepo(t)
	nowMs := time.Now().UnixMilli()
	seedUserFlowResetData(t, r, nowMs)

	for i := 0; i < 35; i++ {
		if err := r.DB().Model(&model.Forward{}).Where("id = 20").
			Updates(map[string]interface{}{"in_flow": 10, "out_flow": 10}).Error; err != nil {
			t.Fatalf("set forward flow: %v", err)
		}
		if err := r.ResetUserFlowByUser(2, nowMs+int64(i), 1, "admin"); err != nil {
			t.Fatalf("reset %d: %v", i, err)
		}
	}
	if logs := forwardResetLogsOf(t, r, 20); len(logs) > 31 {
		t.Fatalf("forward reset log not bounded: %d rows", len(logs))
	}
}
