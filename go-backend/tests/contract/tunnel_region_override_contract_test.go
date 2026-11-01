package contract_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

// Plan 184 「显示地区」: an admin-set region replaces the automatic exit region
// everywhere users pick tunnels; admins also get the real exit behind it;
// users never do. Runs on SQLite and (with FLVX_POSTGRES_TEST_DSN) PostgreSQL.
func TestTunnelRegionOverrideContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			const secret = "tunnel-region-override"
			repository := landingContractRepo(t, dialect)
			router := httpserver.NewRouter(handler.New(repository, secret, "test"), secret, repository)
			admin := mustAdminToken(t, secret)
			ensureContractUser(t, repository, 1, 0)
			ensureContractUser(t, repository, 2, 1)
			userToken, err := auth.GenerateToken(2, "normal_user", 1, secret)
			if err != nil {
				t.Fatal(err)
			}
			for _, node := range []model.Node{
				{ID: 1001, Name: "SECRET_ENTRY_CN", Secret: "s1", ServerIP: "198.51.100.77", ServerIPV4: sql.NullString{String: "198.51.100.77", Valid: true}, Port: "30000-30100", Status: 1, Region: "CN", RegionCity: "深圳", TCPListenAddr: "[::]", UDPListenAddr: "[::]"},
				{ID: 1002, Name: "SECRET_EXIT_HK", Secret: "s2", ServerIP: "198.51.100.88", ServerIPV4: sql.NullString{String: "198.51.100.88", Valid: true}, Port: "31000-31100", Status: 1, Region: "HK", TCPListenAddr: "[::]", UDPListenAddr: "[::]"},
			} {
				node := node
				if err := repository.DB().Create(&node).Error; err != nil {
					t.Fatal(err)
				}
			}
			call := func(token, path string, body interface{}) response.R {
				return requestContractEnvelope(t, router, token, path, body)
			}
			tunnelBody := func(name string, typ int, extra map[string]interface{}) map[string]interface{} {
				body := map[string]interface{}{"name": name, "type": typ, "flow": 1, "trafficRatio": 1, "status": 1,
					"inNodeId": []map[string]interface{}{{"nodeId": 1001, "protocol": "tls"}}}
				if typ == 2 {
					body["outNodeId"] = []map[string]interface{}{{"nodeId": 1002, "protocol": "tls"}}
				}
				for k, v := range extra {
					body[k] = v
				}
				return body
			}
			idOf := func(name string) int64 {
				var tunnel model.Tunnel
				if err := repository.DB().Where("name = ?", name).First(&tunnel).Error; err != nil {
					t.Fatalf("tunnel %s: %v", name, err)
				}
				return tunnel.ID
			}
			stored := func(id int64) string {
				var tunnel model.Tunnel
				if err := repository.DB().First(&tunnel, id).Error; err != nil {
					t.Fatal(err)
				}
				return tunnel.RegionOverride
			}
			adminItem := func(id int64) map[string]interface{} {
				out := call(admin, "/api/v1/tunnel/list", nil)
				if out.Code != 0 {
					t.Fatalf("admin list = %+v", out)
				}
				for _, raw := range mustContractSlice(t, out.Data, "admin tunnels") {
					item := raw.(map[string]interface{})
					if item["id"] == float64(id) {
						return item
					}
				}
				t.Fatalf("tunnel %d missing from admin list", id)
				return nil
			}
			regions := func(v interface{}) string {
				parts := []string{}
				for _, r := range v.([]interface{}) {
					parts = append(parts, r.(string))
				}
				return strings.Join(parts, ",")
			}

			// Create: HK exit shown under 台湾 (lower case accepted); invalid codes rejected.
			if out := call(admin, "/api/v1/tunnel/create", tunnelBody("hkix-to-hinet", 2, map[string]interface{}{"regionOverride": " tw "})); out.Code != 0 {
				t.Fatalf("create with override = %+v", out)
			}
			hk := idOf("hkix-to-hinet")
			if got := stored(hk); got != "TW" {
				t.Fatalf("stored override = %q", got)
			}
			for _, bad := range []interface{}{"ZZ", "EU", "TWN", 7} {
				if out := call(admin, "/api/v1/tunnel/create", tunnelBody("bad-region", 2, map[string]interface{}{"regionOverride": bad})); out.Code == 0 {
					t.Fatalf("create accepted region %v", bad)
				}
			}
			var badCount int64
			repository.DB().Model(&model.Tunnel{}).Where("name = ?", "bad-region").Count(&badCount)
			if badCount != 0 {
				t.Fatal("rejected create left a tunnel")
			}
			// 端口转发 without override: automatic = entry region.
			if out := call(admin, "/api/v1/tunnel/create", tunnelBody("port-forward", 1, nil)); out.Code != 0 {
				t.Fatalf("create port forward = %+v", out)
			}
			pf := idOf("port-forward")

			item := adminItem(hk)
			if regions(item["exitRegions"]) != "TW" || item["regionOverride"] != "TW" || regions(item["autoExitRegions"]) != "HK" {
				t.Fatalf("admin view of override = %+v", item)
			}
			item = adminItem(pf)
			if regions(item["exitRegions"]) != "CN" || item["regionOverride"] != "" || regions(item["autoExitRegions"]) != "CN" {
				t.Fatalf("admin view of automatic = %+v", item)
			}

			// /tunnel/region: admin only, validated, DB only.
			if dialect == "sqlite" {
				// A redeploy deletes and re-adds chain rows: the region endpoint must not.
				if err := repository.DB().Exec("CREATE TRIGGER reject_region_redeploy BEFORE DELETE ON chain_tunnel BEGIN SELECT RAISE(ABORT, 'unexpected redeploy'); END").Error; err != nil {
					t.Fatal(err)
				}
			}
			if out := call(userToken, "/api/v1/tunnel/region", map[string]interface{}{"id": pf, "regionOverride": "HK"}); out.Code != 403 {
				t.Fatalf("non-admin region = %+v", out)
			}
			if out := call(admin, "/api/v1/tunnel/region", map[string]interface{}{"id": pf, "regionOverride": "hk"}); out.Code != 0 {
				t.Fatalf("region = %+v", out)
			}
			for _, body := range []map[string]interface{}{
				{"id": pf, "regionOverride": "EU"},
				{"id": pf},
				{"id": 999999, "regionOverride": "JP"},
				{"id": 0, "regionOverride": "JP"},
			} {
				if out := call(admin, "/api/v1/tunnel/region", body); out.Code == 0 {
					t.Fatalf("region accepted %+v", body)
				}
			}
			if got := stored(pf); got != "HK" {
				t.Fatalf("port forward override = %q", got)
			}
			if regions(adminItem(pf)["exitRegions"]) != "HK" {
				t.Fatal("port forward not under HK")
			}
			if out := call(admin, "/api/v1/tunnel/region", map[string]interface{}{"id": pf, "regionOverride": ""}); out.Code != 0 || stored(pf) != "" {
				t.Fatalf("back to automatic = %+v", out)
			}
			if dialect == "sqlite" {
				if err := repository.DB().Exec("DROP TRIGGER reject_region_redeploy").Error; err != nil {
					t.Fatal(err)
				}
			}

			// /tunnel/update: absent = keep; '' = automatic; a code = set.
			update := func(extra map[string]interface{}) {
				body := tunnelBody("hkix-to-hinet", 2, extra)
				body["id"] = hk
				if out := call(admin, "/api/v1/tunnel/update", body); out.Code != 0 {
					t.Fatalf("update %v = %+v", extra, out)
				}
			}
			update(nil)
			if got := stored(hk); got != "TW" {
				t.Fatalf("update without the key lost the override: %q", got)
			}
			update(map[string]interface{}{"regionOverride": ""})
			if got := stored(hk); got != "" || regions(adminItem(hk)["exitRegions"]) != "HK" {
				t.Fatalf("update to automatic: %q", got)
			}
			update(map[string]interface{}{"regionOverride": "TW"})
			body := tunnelBody("hkix-to-hinet", 2, map[string]interface{}{"regionOverride": "XX1"})
			body["id"] = hk
			if out := call(admin, "/api/v1/tunnel/update", body); out.Code == 0 || stored(hk) != "TW" {
				t.Fatalf("update with an invalid region = %+v", out)
			}

			// Users see only the chosen region: no real exit, no override field, no node data.
			if err := repository.DB().Create(&model.UserTunnel{UserID: 2, TunnelID: hk, Status: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if out := call(userToken, "/api/v1/tunnel/list", nil); out.Code != 403 {
				t.Fatalf("user admin list = %+v", out)
			}
			out := call(userToken, "/api/v1/tunnel/user/tunnel", nil)
			if out.Code != 0 {
				t.Fatalf("user tunnels = %+v", out)
			}
			raw, _ := json.Marshal(out.Data)
			for _, private := range []string{`"HK"`, "autoExitRegions", "regionOverride", "SECRET_", "198.51.100."} {
				if strings.Contains(string(raw), private) {
					t.Fatalf("user tunnel list leaks %q: %s", private, raw)
				}
			}
			userItems := mustContractSlice(t, out.Data, "user tunnels")
			if len(userItems) != 1 || regions(userItems[0].(map[string]interface{})["exitRegions"]) != "TW" {
				t.Fatalf("user tunnel region = %s", raw)
			}
			// The admin rule picker groups by the chosen region too.
			out = call(admin, "/api/v1/tunnel/user/tunnel", nil)
			for _, rawItem := range mustContractSlice(t, out.Data, "admin picker") {
				it := rawItem.(map[string]interface{})
				if it["id"] == float64(hk) && regions(it["exitRegions"]) != "TW" {
					t.Fatalf("admin picker region = %+v", it)
				}
			}
		})
	}
}
