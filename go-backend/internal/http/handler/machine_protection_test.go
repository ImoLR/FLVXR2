package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func TestParseMachineProtection(t *testing.T) {
	base := repo.DefaultMachineProtection()
	if got, present, err := parseMachineProtection(map[string]interface{}{"name": "x"}, base); present || err != nil || got != base {
		t.Fatal("absent keys must keep the base")
	}
	got, present, err := parseMachineProtection(map[string]interface{}{"machineProtectEnabled": false, "machineMaxTotal": float64(0), "machineCloseWaitSeconds": "30"}, base)
	if !present || err != nil || got != (repo.MachineProtection{Enabled: false, MaxTotal: 0, TWBuckets: 500, CloseWaitSeconds: 30}) {
		t.Fatalf("overlay %+v %v", got, err)
	}
	for _, bad := range []map[string]interface{}{
		{"machineMaxTotal": float64(99)}, {"machineMaxTotal": float64(1_000_001)}, {"machineTwBuckets": float64(50)},
		{"machineCloseWaitSeconds": float64(3601)}, {"machineMaxTotal": float64(-1)}, {"machineMaxTotal": 12.5},
		{"machineMaxTotal": "1k"}, {"machineProtectEnabled": "maybe"}, {"machineProtectEnabled": float64(1)}, {"machineTwBuckets": nil},
	} {
		if got, _, err := parseMachineProtection(bad, base); err == nil || got != base {
			t.Fatalf("%v accepted (%+v)", bad, got)
		}
	}
}

func adminRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	return r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
}

// Shared landing servers: Custom's status may carry the server's whole-machine
// state; the 监控 「落地」 card shows it, an absent or malformed object = unknown
// and never breaks the status poll.
func TestSharedLandingMachinePassthrough(t *testing.T) {
	e := newFlowTestEnv(t)
	statusBody := `{"core":{"state":"running"},"collected_at":%d,"nodes":[],"machine":{"conns":{"total":640,"tcp":600,"udp":40,"established":200,"time_wait":350,"close_wait":10,"other_tcp":40},"protect":{"owner":"mmwx","enabled":true,"max_total":1500,"tw_buckets":500,"close_wait_seconds":10,"blocking":false,"dropped_total":5},"updated_at":"%s"}}`
	body := func() string {
		return strings.NewReplacer("%d", strings.TrimSpace(jsonInt(time.Now().UnixMilli())), "%s", time.Now().UTC().Format(time.RFC3339)).Replace(statusBody)
	}
	current := body()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case landingcustom.Prefix + "/shares":
			json.NewEncoder(w).Encode(map[string]any{"shares": []landingcustom.Share{{ID: 7, Name: "shared", Hosts: landingcustom.Hosts{IPv4: "203.0.113.7"}, Status: "active", PortMin: 30000, PortMax: 30100, Protocols: []string{"ss2022"}, Core: landingcustom.Core{State: "running"}}}})
		case landingcustom.Prefix + "/landing-hosts":
			w.Write([]byte("{}"))
		case landingcustom.Prefix + "/shares/7/desired":
			var d landingcustom.Desired
			json.NewDecoder(r.Body).Decode(&d)
			json.NewEncoder(w).Encode(map[string]any{"accepted_revision": d.Revision})
		case landingcustom.Prefix + "/shares/7/status":
			w.Write([]byte(current))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	if err := e.r.SaveLandingIntegration(srv.URL, "flvx_fixture"); err != nil {
		t.Fatal(err)
	}
	monitor := func() map[string]interface{} {
		t.Helper()
		w := httptest.NewRecorder()
		e.h.landingMonitor(w, adminRequest(t, http.MethodPost, "/api/v1/landing/monitor"))
		var out struct {
			Code int
			Data struct {
				Servers []map[string]interface{} `json:"servers"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Code != 0 || len(out.Data.Servers) != 1 {
			t.Fatalf("monitor %s %v", w.Body.String(), err)
		}
		return out.Data.Servers[0]
	}
	poll := func() {
		t.Helper()
		e.r.DB().Model(&model.LandingServer{}).Where("source='shared'").Update("shared_last_sync_at", 0)
		e.h.syncSharedIntegration(context.Background(), true)
		var s model.LandingServer
		e.r.DB().Where("source='shared'").Take(&s)
		if s.SharedSyncError != "" {
			t.Fatalf("status poll failed: %s", s.SharedSyncError)
		}
	}
	poll()
	server := monitor()
	machine, _ := server["machine"].(map[string]interface{})
	if machine == nil {
		t.Fatalf("no machine object: %v", server)
	}
	conns := machine["conns"].(map[string]interface{})
	prot := machine["protect"].(map[string]interface{})
	if conns["total"] != float64(640) || conns["timeWait"] != float64(350) || prot["owner"] != "mmwx" || prot["maxTotal"] != float64(1500) || prot["droppedTotal"] != float64(5) {
		t.Fatalf("machine %v", machine)
	}
	var shared model.LandingServer
	e.r.DB().Where("source='shared'").Take(&shared)
	if v := e.h.sharedMachineView(shared.ID, false, time.Now()); v == nil || v.Protect.TWBuckets != nil || v.Conns.Total != 640 {
		t.Fatalf("user view %+v", v)
	}
	// Alias spelling accepted.
	current = strings.Replace(strings.Replace(body(), `"conns"`, `"machine_conns"`, 1), `"protect"`, `"machine_protect"`, 1)
	poll()
	if m, _ := monitor()["machine"].(map[string]interface{}); m == nil || m["conns"] == nil || m["protect"] == nil {
		t.Fatalf("alias keys not accepted: %v", m)
	}
	// Malformed object: poll succeeds, machine unknown.
	current = strings.Replace(body(), `"total":640`, `"total":"lots"`, 1)
	poll()
	if m := monitor()["machine"]; m != nil {
		t.Fatalf("malformed machine shown: %v", m)
	}
	// Absent (older Custom/Helper): unknown.
	current = `{"core":{"state":"running"},"collected_at":` + jsonInt(time.Now().UnixMilli()) + `,"nodes":[]}`
	poll()
	if m := monitor()["machine"]; m != nil {
		t.Fatalf("absent machine shown: %v", m)
	}
	// A Helper that stopped updating goes stale (3 min).
	current = strings.Replace(body(), time.Now().UTC().Format(time.RFC3339), time.Now().Add(-4*time.Minute).UTC().Format(time.RFC3339), 1)
	poll()
	if m := monitor()["machine"]; m != nil {
		t.Fatalf("stale machine shown: %v", m)
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
