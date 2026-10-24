package handler

import (
	"testing"
	"time"

	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// TestLandingCoreUpgradeConfirm (plan 129): an agent that downloaded a newer
// pinned Core under a running old one reports core_update; the panel shows it,
// sends upgrade_core_epoch only after the admin confirmed (and only for that
// Core epoch), and the confirmation is consumed once a current Core reports.
func TestLandingCoreUpgradeConfirm(t *testing.T) {
	e := newFlowTestEnv(t)
	guardSeed(t, e)
	epoch, update := "1700000000000000001", "restart_required"
	var payloads []map[string]interface{}
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		payload := data.(map[string]interface{})
		payloads = append(payloads, payload)
		// The agent restarts the confirmed Core inside Apply: new epoch, current.
		if got, _ := payload["upgrade_core_epoch"].(string); got != "" && got == epoch {
			epoch, update = "1700000000000000002", ""
		}
		out := map[string]interface{}{"revision": payload["revision"], "epoch": epoch, "state": "running", "nodes": []repo.LandingCounter{}, "egress": map[string]any{"state": "applied", "ready": true}}
		if update != "" {
			out["core_update"] = update
		}
		return ws.CommandResult{Success: true, Data: out}, nil
	}
	last := func() map[string]interface{} { return payloads[len(payloads)-1] }
	view := func() map[string]any {
		t.Helper()
		s, _ := e.r.GetLandingServer(1)
		return e.h.landingServerEgressView(*s, 1, e.h.landingEgressCatalog(), 1)
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	s, _ := e.r.GetLandingServer(1)
	if s.CoreStaleEpoch != epoch || s.CoreUpgradeOK {
		t.Fatalf("stale epoch %q ok %v", s.CoreStaleEpoch, s.CoreUpgradeOK)
	}
	if _, ok := last()["upgrade_core_epoch"]; ok {
		t.Fatal("upgrade sent without confirmation")
	}
	if v := view(); v["coreUpgradeRequired"] != true || v["coreUpgradeConfirmed"] != false {
		t.Fatalf("view %v", v)
	}
	// Plain syncs never restart and keep the revision.
	rev := last()["revision"]
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if last()["revision"] != rev {
		t.Fatalf("revision moved: %v → %v", rev, last()["revision"])
	}
	// A legacy save (no field) does not confirm.
	body := serverSaveBody(t, e, 0, false)
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code != 0 {
		t.Fatal(out)
	}
	if s, _ := e.r.GetLandingServer(1); s.CoreUpgradeOK || s.CoreStaleEpoch == "" {
		t.Fatalf("legacy save touched the upgrade: %+v", s.CoreUpgradeOK)
	}
	if _, ok := last()["upgrade_core_epoch"]; ok {
		t.Fatal("upgrade sent after a legacy save")
	}
	// Confirm: the payload names exactly the stale Core, with a new revision;
	// the agent restarts it and reports a current Core, which consumes it.
	stale := epoch
	body = serverSaveBody(t, e, 0, false)
	body["coreUpgradeConfirmed"] = true
	rev = last()["revision"]
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code != 0 {
		t.Fatal(out)
	}
	var sent map[string]interface{}
	for _, p := range payloads {
		if p["upgrade_core_epoch"] == stale {
			sent = p
		}
	}
	if sent == nil || sent["revision"].(int64) <= rev.(int64) {
		t.Fatalf("confirmed upgrade not sent with a new revision: %v", sent)
	}
	s, _ = e.r.GetLandingServer(1)
	if s.CoreStaleEpoch != "" || s.CoreUpgradeOK {
		t.Fatalf("confirmation not consumed: %q %v", s.CoreStaleEpoch, s.CoreUpgradeOK)
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if _, ok := last()["upgrade_core_epoch"]; ok {
		t.Fatal("upgrade flag kept after the restart")
	}
	if v := view(); v["coreUpgradeRequired"] != false || v["coreUpgradeConfirmed"] != false {
		t.Fatalf("view after upgrade %v", v)
	}
	// Confirming with a current Core is a no-op.
	body = serverSaveBody(t, e, 0, false)
	body["coreUpgradeConfirmed"] = true
	if out := egressCall(t, e.h, "/api/v1/landing/server/save", body, 0); out.Code != 0 {
		t.Fatal(out)
	}
	if s, _ := e.r.GetLandingServer(1); s.CoreUpgradeOK {
		t.Fatal("confirmation stored without an outdated Core")
	}
	// A confirmation for a Core that restarted on its own (new epoch, still
	// outdated) is dropped: the admin is asked again for the new Core.
	epoch, update = "1700000000000000003", "restart_required"
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if err := e.r.SetLandingCoreUpgrade(1); err != nil {
		t.Fatal(err)
	}
	epoch = "1700000000000000004"
	update = "restart_required"
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		payload := data.(map[string]interface{})
		payloads = append(payloads, payload)
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"revision": payload["revision"], "epoch": epoch, "state": "running", "nodes": []repo.LandingCounter{}, "egress": map[string]any{"state": "applied", "ready": true}, "core_update": update}}, nil
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if last()["upgrade_core_epoch"] != "1700000000000000003" {
		t.Fatalf("payload must name the confirmed epoch: %v", last()["upgrade_core_epoch"])
	}
	if s, _ := e.r.GetLandingServer(1); s.CoreStaleEpoch != epoch || s.CoreUpgradeOK {
		t.Fatalf("new Core epoch must ask again: %q %v", s.CoreStaleEpoch, s.CoreUpgradeOK)
	}
	// An old agent never reports it: a stored state stays only until the
	// next report, and nothing is sent.
	e.h.nodeCommandSender = func(id int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
		payload := data.(map[string]interface{})
		payloads = append(payloads, payload)
		return ws.CommandResult{Success: true, Data: map[string]interface{}{"revision": payload["revision"], "epoch": epoch, "state": "running", "nodes": []repo.LandingCounter{}, "egress": map[string]any{"state": "applied", "ready": true}}}, nil
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.r.GetLandingServer(1); s.CoreStaleEpoch != "" {
		t.Fatalf("old agent report kept the stale state: %q", s.CoreStaleEpoch)
	}
	if _, ok := last()["upgrade_core_epoch"]; ok {
		t.Fatal("old agent got upgrade_core_epoch")
	}
	e.h.egressSyncWG.Wait()
}
