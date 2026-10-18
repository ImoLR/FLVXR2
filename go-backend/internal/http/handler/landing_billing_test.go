package handler

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func seedHandlerLanding(t *testing.T, e *flowTestEnv, owner int64, mode int, ratio float64) {
	t.Helper()
	e.addNode(2, "landing-secret")
	for _, v := range []interface{}{&model.LandingServer{ID: 1, NodeID: 2, Source: "native", Name: "landing", PublicHost: "203.0.113.2", FlowType: mode, TrafficRatio: ratio, Status: "active"}, &model.LandingNode{ID: 1, UserID: owner, LandingServerID: 1, Name: "node", Protocol: "ss2022", Port: 30001, InboundTag: "flvxl-1", Status: "active"}} {
		if err := e.r.DB().Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestLandingBillingWorkedExamples(t *testing.T) {
	const gb int64 = 1 << 30
	for _, source := range []string{"native", "shared"} {
		for _, tc := range []struct {
			name           string
			tRatio, lRatio float64
			owner          int64
			rule           bool
			want           int64
		}{{"double ratio1 via rule billed once", 1, 1, 2, true, 2 * gb}, {"direct double ratio1", 1, 1, 2, false, 2 * gb}, {"premium tunnel max T L", 3, 1, 2, true, 6 * gb}, {"premium landing max T L", 1, 3, 2, true, 6 * gb}, {"other owner landing no exemption", 1, 1, 3, true, 2 * gb}} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				e := newFlowTestEnv(t)
				e.addUser(2)
				if tc.owner != 2 {
					e.addUser(tc.owner)
				}
				e.addTunnel(1, 2, tc.tRatio)
				e.addUserTunnel(10, 2, 1)
				e.addForward(20, 2, 1)
				seedHandlerLanding(t, e, tc.owner, 2, tc.lRatio)
				if source == "shared" {
					e.exec("UPDATE landing_server SET source='shared',node_id=0,remote_server_id=7 WHERE id=1")
				}
				e.exec("UPDATE forward SET landing_node_id=1 WHERE id=20")
				var items []flowItem
				if tc.rule {
					items = []flowItem{{N: "20_2_10_tcp", D: gb / 4, U: gb * 3 / 4}}
				}
				counter := repo.LandingCounter{ID: 1, NodeID: 2, InboundTag: "flvxl-1", Epoch: "100", UpBytes: gb / 4, DownBytes: gb * 3 / 4}
				reporter := int64(2)
				if source == "shared" {
					reporter = 0
					counter.NodeID = 0
					counter.ServerID = 1
					counter.CollectedAt = time.Now().UnixMilli()
				}
				// Separate real-agent reporters: entry upload followed by landing upload.
				if _, err := e.h.commitFlowItems(1, items); err != nil {
					t.Fatal(err)
				}
				if _, err := e.h.commitFlowWithLanding(reporter, nil, []repo.LandingCounter{counter}); err != nil {
					t.Fatal(err)
				}
				e.expectFlows("user", 2, tc.want/4, tc.want*3/4)
				if got := e.monthlyQuotaUsed(2); got != tc.want {
					t.Fatalf("quota got %d want %d", got, tc.want)
				}
				if tc.rule {
					e.expectFlows("forward", 20, int64(float64(gb/4)*tc.tRatio)*2, int64(float64(gb*3/4)*tc.tRatio)*2)
				}
				if tc.owner == 2 {
					var q model.UserServerQuota
					kind, ref := "node", int64(2)
					if source == "shared" {
						kind, ref = "landing", 1
					}
					e.r.DB().Where("user_id=2 AND server_kind=? AND server_ref_id=?", kind, ref).First(&q)
					if q.UsedIn+q.UsedOut != tc.want {
						t.Fatalf("landing host quota %+v want %d", q, tc.want)
					}
				}
			})
		}
	}
}

func TestLandingUploadObjectAndIdempotentCounters(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	body, _ := json.Marshal(map[string]interface{}{"flows": []flowItem{}, "landing_counters": []repo.LandingCounter{{ID: 1, InboundTag: "flvxl-1", Epoch: "100", UpBytes: 10, DownBytes: 20}}})
	for i := 0; i < 2; i++ {
		// Native agents encrypt the object. Plain object support is deliberately not
		// needed by old agents; encryption envelope keeps their retry semantics.
		cipher := getOrCreateFlowCrypto("landing-secret")
		encrypted, err := cipher.Encrypt(body)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]interface{}{"encrypted": true, "data": encrypted})
		req := httptest.NewRequest("POST", "/flow/upload?secret=landing-secret", bytes.NewReader(raw))
		w := httptest.NewRecorder()
		e.h.flowUpload(w, req)
		e.h.flowEnforceWG.Wait()
		if w.Code != 200 || w.Body.String() != "ok" {
			t.Fatalf("response %d %s", w.Code, w.Body.String())
		}
	}
	e.expectFlows("user", 2, 20, 40)
}

func TestLandingForwardQuotaPauseAllEntries(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	e.addTunnel(1, 2, 1)
	e.addUserTunnel(10, 2, 1)
	e.addForward(20, 2, 1)
	for _, id := range []int64{1, 2, 3} {
		e.addNode(id, "test-node")
	}
	e.exec("INSERT INTO forward_port (forward_id,node_id,port) VALUES (20,1,11000),(20,2,11000)")
	e.exec("INSERT INTO chain_tunnel (tunnel_id,chain_type,node_id) VALUES (1,'3',3)")
	e.exec("UPDATE node SET default_user_quota_bytes=100 WHERE id=3")
	if _, err := e.h.commitFlowItems(1, []flowItem{{N: "20_2_10_tcp", D: 40, U: 10}}); err != nil {
		t.Fatal(err)
	}
	result, err := e.r.ReconcileLandingPolicy(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.PauseForwardIDs) != 1 || result.PauseForwardIDs[0] != 20 {
		t.Fatalf("pause %+v", result)
	}
	e.exec("UPDATE node SET default_user_quota_bytes=200 WHERE id=3")
	result, err = e.r.ReconcileLandingPolicy(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ResumeForwardIDs) != 1 || result.ResumeForwardIDs[0] != 20 {
		t.Fatalf("resume %+v", result)
	}
}
