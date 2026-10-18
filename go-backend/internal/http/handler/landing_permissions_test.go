package handler

import (
	"go-backend/internal/store/model"
	"testing"
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
