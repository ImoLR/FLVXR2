package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

func TestLandingManualTargetRequiresCurrentGrant(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	for _, row := range []interface{}{&model.UserGroup{ID: 1, Name: "landing", Status: 1}, &model.UserGroupUser{UserGroupID: 1, UserID: 2}, &model.LandingServerUserGroup{LandingServerID: 1, UserGroupID: 1}} {
		if err := e.r.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	target := "203.0.113.2:30001"
	if remote, id, _, err := e.h.resolveNativeLandingTarget(map[string]interface{}{}, 2, 0, target, false); err != nil || id != 1 || remote != target {
		t.Fatalf("granted target: %s %d %v", remote, id, err)
	}
	if err := e.r.DB().Where("landing_server_id = ?", 1).Delete(&model.LandingServerUserGroup{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.h.resolveNativeLandingTarget(map[string]interface{}{}, 2, 0, target, false); err == nil {
		t.Fatal("manual target bypassed revoked grant")
	}
	if _, _, _, err := e.h.resolveNativeLandingTarget(map[string]interface{}{"landingNodeId": float64(1)}, 2, 0, target, false); err == nil {
		t.Fatal("explicit target bypassed revoked grant")
	}
	if remote, id, _, err := e.h.resolveNativeLandingTarget(map[string]interface{}{}, 3, 0, target, false); err != nil || id != 0 || remote != target {
		t.Fatalf("another owner's target changed: %s %d %v", remote, id, err)
	}
}

func TestLandingBatchTunnelChangeRequiresCurrentGrant(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.addTunnel(1, 2, 1)
	e.addTunnel(2, 2, 1)
	e.addUserTunnel(10, 2, 1)
	e.addUserTunnel(11, 2, 2)
	e.addForward(20, 2, 1)
	e.exec("UPDATE forward SET landing_node_id=1, remote_addr='203.0.113.2:30001' WHERE id=20")
	e.h.nodeCommandSender = func(int64, string, interface{}, time.Duration) (ws.CommandResult, error) {
		t.Fatal("revoked landing grant sent a node command")
		return ws.CommandResult{}, nil
	}
	req := httptest.NewRequest("POST", "/api/v1/forward/batch-change-tunnel", strings.NewReader(`{"forwardIds":[20],"targetTunnelId":2}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "2", RoleID: 1}))
	w := httptest.NewRecorder()
	e.h.forwardBatchChangeTunnel(w, req)
	if !strings.Contains(w.Body.String(), "无权使用该落地服务器") {
		t.Fatalf("batch did not reject revoked grant: %s", w.Body.String())
	}
	f, err := e.r.GetForwardRecord(20)
	if err != nil || f == nil || f.TunnelID != 1 || f.LandingNodeID != 1 {
		t.Fatalf("rejected batch mutated rule: %+v %v", f, err)
	}
}

func TestLandingServerQuotaSurvivesUserQuotaRelease(t *testing.T) {
	for _, serverBlocked := range []bool{false, true} {
		name := "ordinary rule resumes"
		if serverBlocked {
			name = "server quota still blocks"
		}
		t.Run(name, func(t *testing.T) {
			e := newFlowTestEnv(t)
			e.addUser(2)
			e.addTunnel(1, 2, 1)
			e.addUserTunnel(10, 2, 1)
			e.addForward(20, 2, 1)
			e.addNode(2, "quota-node")
			e.exec("UPDATE forward SET status=0 WHERE id=20")
			e.exec("INSERT INTO forward_port (forward_id,node_id,port) VALUES (20,2,1200)")
			if serverBlocked {
				q := model.UserServerQuota{UserID: 2, ServerKind: "node", ServerRefID: 2, LimitBytes: 10, LimitOverride: true, UsedIn: 20, Paused: true}
				if err := e.r.DB().Create(&q).Error; err != nil {
					t.Fatal(err)
				}
			}
			commands := 0
			e.h.nodeCommandSender = func(_ int64, method string, _ interface{}, _ time.Duration) (ws.CommandResult, error) {
				commands++
				if method != "ResumeService" {
					t.Fatalf("unexpected command %s", method)
				}
				return ws.CommandResult{Success: true}, nil
			}
			e.h.applyUserQuotaRelease(&repo.UserQuotaRelease{UserID: 2, ForwardIDs: []int64{20}, UnblockUser: true}, e.now)
			if serverBlocked {
				if e.forwardStatus(20) != 0 || commands != 0 {
					t.Fatalf("user quota release bypassed server quota, commands=%d", commands)
				}
			} else if e.forwardStatus(20) != 1 || commands == 0 {
				t.Fatalf("ordinary rule release changed, commands=%d", commands)
			}
		})
	}
}

func TestLandingUserQuotaResetRestoresImmediately(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	now := time.Now()
	for _, row := range []interface{}{&model.UserGroup{ID: 1, Name: "landing", Status: 1}, &model.UserGroupUser{UserGroupID: 1, UserID: 2}, &model.LandingServerUserGroup{LandingServerID: 1, UserGroupID: 1}, &model.UserQuota{UserID: 2, DailyLimitGB: 1, DailyUsedBytes: bytesPerGB, DisabledByQuota: 1, DayKey: int64(now.Year()*10000 + int(now.Month())*100 + now.Day()), MonthKey: int64(now.Year()*100 + int(now.Month()))}} {
		if err := e.r.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	e.exec("UPDATE landing_node SET status='paused_quota' WHERE id=1")
	w := httptest.NewRecorder()
	e.h.userQuotaReset(w, httptest.NewRequest("POST", "/api/v1/user/quota/reset", strings.NewReader(`{"userId":2,"scope":"all"}`)))
	n, err := e.r.GetLandingNode(1)
	if err != nil || n == nil || n.Status != "active" || n.Port != 30001 {
		t.Fatalf("manual quota reset did not restore same node: %+v %v; %s", n, err, w.Body.String())
	}
}
