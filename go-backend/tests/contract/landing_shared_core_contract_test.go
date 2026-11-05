package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 223: shared landing servers' Core memory / CPU = 0 / 0 (不限制, decided
// by 妙妙屋) through the admin API; native servers keep their limits and the
// ≥64 MiB / ≥1 % check.
func TestLandingSharedCoreUnlimitedContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			if err := r.DB().Create(&model.Node{ID: 601, Name: "native", ServerIP: "203.0.113.1", Port: "10000-19999", LandingCapable: true}).Error; err != nil {
				t.Fatal(err)
			}
			h := handler.New(r, "shared-core", "test")
			router := httpserver.NewRouter(h, "shared-core", r)
			admin, _ := auth.GenerateToken(1, "admin", 0, "shared-core")
			call := func(path string, body interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/landing/"+path, bytes.NewReader(raw))
				req.Header.Set("Authorization", admin)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err, w.Body.String())
				}
				return out
			}
			list := func() map[string]map[string]interface{} {
				t.Helper()
				out := call("server/list", map[string]interface{}{})
				if out.Code != 0 {
					t.Fatalf("list %+v", out)
				}
				m := map[string]map[string]interface{}{}
				for _, item := range out.Data.([]interface{}) {
					v := item.(map[string]interface{})
					m[v["source"].(string)] = v
				}
				return m
			}
			limits := func(v map[string]interface{}) [2]float64 {
				return [2]float64{v["coreMemoryMb"].(float64), v["coreCpuPercent"].(float64)}
			}
			if out := call("server/enable", map[string]interface{}{"nodeId": 601}); out.Code != 0 {
				t.Fatalf("enable %+v", out)
			}
			if err := r.SyncSharedLandings([]landingcustom.Share{{ID: 3, Name: "Boil Hinet 马年", Hosts: landingcustom.Hosts{IPv4: "114.41.205.96"}, Status: "active", PortMin: 30000, PortMax: 30999, MaxConnections: 500, Core: landingcustom.Core{State: "running"}, Protocols: []string{"ss2022", "vless_reality_vision"}}}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			servers := list()
			if got := limits(servers["shared"]); got != [2]float64{0, 0} {
				t.Fatalf("shared server limits %v, want 不限制 0/0", got)
			}
			if got := limits(servers["native"]); got != [2]float64{256, 50} {
				t.Fatalf("native server limits %v, want 256/50", got)
			}

			save := func(v map[string]interface{}, mem, cpu int) response.R {
				t.Helper()
				// The fields the settings page posts (api/landing.ts saveLandingServer).
				body := map[string]interface{}{}
				for _, k := range []string{"id", "nodeId", "name", "publicHost", "portMin", "portMax", "flowType", "trafficRatio", "maxConnections",
					"defaultMaxConnections", "defaultMaxOnlineIps", "defaultTrafficLimitBytes", "defaultUserQuotaBytes", "defaultExpiryMode", "defaultExpiryDays",
					"defaultExpiresAt", "maxNodesPerUser", "protocols", "realityDest", "realityServerNames", "availableUntil", "status"} {
					body[k] = v[k]
				}
				body["coreMemoryMb"], body["coreCpuPercent"], body["userGroupIds"] = mem, cpu, []int64{}
				return call("server/save", body)
			}
			// Shared: saving the settings form works with 0/0 and ignores any
			// value a stale client sends.
			if out := save(servers["shared"], 0, 0); out.Code != 0 {
				t.Fatalf("save shared 0/0 %+v", out)
			}
			if out := save(list()["shared"], 512, 80); out.Code != 0 {
				t.Fatalf("save shared 512/80 %+v", out)
			}
			if got := limits(list()["shared"]); got != [2]float64{0, 0} {
				t.Fatalf("shared server limits after save %v, want 0/0", got)
			}
			// Native: unchanged rules.
			if out := save(list()["native"], 0, 0); out.Code == 0 {
				t.Fatalf("native save with 0/0 accepted")
			}
			if out := save(list()["native"], 300, 60); out.Code != 0 {
				t.Fatalf("save native 300/60 %+v", out)
			}
			if got := limits(list()["native"]); got != [2]float64{300, 60} {
				t.Fatalf("native server limits after save %v, want 300/60", got)
			}
		})
	}
}
