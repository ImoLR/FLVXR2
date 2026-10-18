package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/http/response"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

func deleteHandlerLanding(t *testing.T, h *Handler) response.R {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/landing/node/delete", strings.NewReader(`{"id":1}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "2", RoleID: 1}))
	w := httptest.NewRecorder()
	h.landingNodeDelete(w, req)
	var out response.R
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return out
}

func TestLandingDeleteFlushesFinalCounter(t *testing.T) {
	for _, periodicFirst := range []bool{false, true} {
		name := "final delta"
		if periodicFirst {
			name = "concurrent periodic duplicate"
		}
		t.Run(name, func(t *testing.T) {
			e := newFlowTestEnv(t)
			e.addUser(2)
			seedHandlerLanding(t, e, 2, 2, 1)
			e.exec("UPDATE node SET landing_capable=? WHERE id=2", true)
			counter := repo.LandingCounter{ID: 1, InboundTag: "flvxl-1", Epoch: "100", UpBytes: 10, DownBytes: 20}
			if _, err := e.h.commitFlowWithLanding(2, nil, []repo.LandingCounter{counter}); err != nil {
				t.Fatal(err)
			}
			counter.UpBytes, counter.DownBytes = 15, 25
			calls := 0
			e.h.nodeCommandSender = func(id int64, method string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
				calls++
				if id != 2 || method != "ApplyLandingDesired" {
					t.Fatalf("unexpected command %d %s", id, method)
				}
				payload := data.(map[string]interface{})
				nodes := payload["nodes"].([]map[string]interface{})
				if calls == 1 {
					if len(nodes) != 1 || nodes[0]["enabled"] != false {
						t.Fatalf("delete did not disable before final collection: %+v", nodes)
					}
					n, err := e.r.GetLandingNode(1)
					if err != nil || n == nil || n.Status != "disabled" || n.Port != 30001 {
						t.Fatalf("identity released before collection: %+v %v", n, err)
					}
					if periodicFirst {
						if _, err := e.h.commitFlowWithLanding(2, nil, []repo.LandingCounter{counter}); err != nil {
							t.Fatal(err)
						}
					}
				} else if len(nodes) != 0 {
					t.Fatalf("deleted inbound still desired: %+v", nodes)
				}
				return ws.CommandResult{Success: true, Data: map[string]interface{}{"revision": payload["revision"], "epoch": "100", "state": "running", "nodes": []repo.LandingCounter{counter}}}, nil
			}
			if out := deleteHandlerLanding(t, e.h); out.Code != 0 {
				t.Fatalf("delete: %+v", out)
			}
			if n, err := e.r.GetLandingNode(1); err != nil || n != nil {
				t.Fatalf("node not deleted: %+v %v", n, err)
			}
			e.expectFlows("user", 2, 30, 50)
			if got := e.monthlyQuotaUsed(2); got != 80 {
				t.Fatalf("final quota = %d, want 80", got)
			}
			if calls != 2 {
				t.Fatalf("apply calls = %d, want disable and remove", calls)
			}
		})
	}
}

func TestLandingDeleteRetainsIdentityUntilConfirmed(t *testing.T) {
	for _, failure := range []string{"offline", "command", "missing counter", "connections", "revision", "invalid epoch"} {
		t.Run(failure, func(t *testing.T) {
			e := newFlowTestEnv(t)
			e.addUser(2)
			seedHandlerLanding(t, e, 2, 2, 1)
			e.exec("UPDATE node SET landing_capable=? WHERE id=2", true)
			if failure == "offline" {
				e.exec("UPDATE node SET status=0 WHERE id=2")
			}
			fail := true
			e.h.nodeCommandSender = func(_ int64, _ string, data interface{}, _ time.Duration) (ws.CommandResult, error) {
				payload := data.(map[string]interface{})
				counter := repo.LandingCounter{ID: 1, InboundTag: "flvxl-1", Epoch: "100", UpBytes: 15, DownBytes: 25}
				reply := map[string]interface{}{"revision": payload["revision"], "epoch": "100", "state": "running"}
				if fail {
					switch failure {
					case "command":
						return ws.CommandResult{}, errors.New("unreachable")
					case "connections":
						counter.Connections = 1
					case "revision":
						reply["revision"] = int64(-1)
					case "invalid epoch":
						counter.Epoch = "invalid"
					}
				}
				if !fail || failure != "missing counter" {
					reply["nodes"] = []repo.LandingCounter{counter}
				}
				return ws.CommandResult{Success: true, Data: reply}, nil
			}
			if out := deleteHandlerLanding(t, e.h); out.Code == 0 {
				t.Fatal("unconfirmed delete succeeded")
			}
			n, err := e.r.GetLandingNode(1)
			if err != nil || n == nil || n.Status != "disabled" || n.AutoDisabled || n.Port != 30001 || n.InboundTag != "flvxl-1" {
				t.Fatalf("failed delete lost identity: %+v %v", n, err)
			}
			e.expectFlows("user", 2, 0, 0)
			fail = false
			e.exec("UPDATE node SET status=1 WHERE id=2")
			if out := deleteHandlerLanding(t, e.h); out.Code != 0 {
				t.Fatalf("retry: %+v", out)
			}
			e.expectFlows("user", 2, 30, 50)
		})
	}
}

func TestLandingDeleteReferencedNodeDoesNotDisable(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.addTunnel(1, 2, 1)
	e.addForward(20, 2, 1)
	e.exec("UPDATE forward SET landing_node_id=1 WHERE id=20")
	e.h.nodeCommandSender = func(int64, string, interface{}, time.Duration) (ws.CommandResult, error) {
		t.Fatal("referenced node sent a destructive command")
		return ws.CommandResult{}, nil
	}
	if out := deleteHandlerLanding(t, e.h); out.Code == 0 {
		t.Fatal("referenced node deletion succeeded")
	}
	n, err := e.r.GetLandingNode(1)
	if err != nil || n == nil || n.Status != "active" {
		t.Fatalf("referenced node changed: %+v %v", n, err)
	}
}
