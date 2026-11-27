package contract_test

import (
	"strings"
	"testing"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
)

// Plan 318: 「VPS 厂商」 belongs to the node — required on node/create (admin
// API), multi (seller + upstream cloud), stored as keys + 「其他」 name, shown
// only to admins (every node API is admin-only); the vendor options API; an
// existing database gains the columns and a native landing's plan-316 vendor
// moves to its node on startup (shares and nodes with a pick untouched).
func TestNodeVendorContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			const secret = "node-vendor"
			open, _ := landingReopenableRepo(t, dialect)
			r := open()
			defer func() { r.Close() }()
			router := httpserver.NewRouter(handler.New(r, secret, "test"), secret, r)
			admin := mustAdminToken(t, secret)
			ensureContractUser(t, r, 2, 1)
			user, err := auth.GenerateToken(2, "token_user_2", 1, secret)
			if err != nil {
				t.Fatal(err)
			}
			listed := func(name string) map[string]interface{} {
				t.Helper()
				out := requestContractEnvelope(t, router, admin, "/api/v1/node/list", map[string]interface{}{})
				if out.Code != 0 {
					t.Fatalf("list: %+v", out)
				}
				for _, row := range mustContractSlice(t, out.Data, "node list") {
					if item := row.(map[string]interface{}); item["name"] == name {
						return item
					}
				}
				t.Fatalf("node %q not listed", name)
				return nil
			}
			stored := func(name string) model.Node {
				t.Helper()
				var n model.Node
				if err := r.DB().Where("name = ?", name).Take(&n).Error; err != nil {
					t.Fatal(err)
				}
				return n
			}

			// Required on create.
			if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", map[string]interface{}{"name": "火山广州", "serverIpV4": "192.0.2.52"}); out.Code == 0 || !strings.Contains(out.Msg, "VPS 厂商") {
				t.Fatalf("create without vendor = %+v", out)
			}
			if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", map[string]interface{}{"name": "火山广州", "serverIpV4": "192.0.2.52", "vendors": []string{"leikwan", "volcengine"}}); out.Code != 0 {
				t.Fatalf("create = %+v", out)
			}
			item := listed("火山广州")
			if vs, ok := item["vendors"].([]interface{}); !ok || len(vs) != 2 || vs[0] != "leikwan" || vs[1] != "volcengine" || item["vendorOther"] != "" {
				t.Fatalf("listed pick %+v %+v", item["vendors"], item["vendorOther"])
			}
			if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", map[string]interface{}{"name": "小厂", "serverIpV4": "192.0.2.53", "vendorOther": " 某小厂 "}); out.Code != 0 {
				t.Fatalf("create 其他 = %+v", out)
			}
			if n := stored("小厂"); n.Vendors != "" || n.VendorOther != "某小厂" {
				t.Fatalf("stored 其他 %q %q", n.Vendors, n.VendorOther)
			}
			// Update: an empty pick is refused, absent keeps it.
			n := stored("火山广州")
			base := map[string]interface{}{"id": n.ID, "name": n.Name, "serverIp": n.ServerIP, "serverIpV4": "192.0.2.52", "port": n.Port}
			update := func(extra map[string]interface{}) (int, string) {
				t.Helper()
				body := map[string]interface{}{}
				for k, v := range base {
					body[k] = v
				}
				for k, v := range extra {
					body[k] = v
				}
				out := requestContractEnvelope(t, router, admin, "/api/v1/node/update", body)
				return out.Code, out.Msg
			}
			if code, msg := update(map[string]interface{}{"vendors": []string{}, "vendorOther": ""}); code == 0 || !strings.Contains(msg, "VPS 厂商") {
				t.Fatalf("empty pick: %d %s", code, msg)
			}
			if code, msg := update(map[string]interface{}{"remark": "r"}); code != 0 || stored("火山广州").Vendors != "leikwan,volcengine" {
				t.Fatalf("absent keeps: %d %s %q", code, msg, stored("火山广州").Vendors)
			}
			// Options (admin): the 17-vendor seed list.
			out := requestContractEnvelope(t, router, admin, "/api/v1/node/vendor/options", map[string]interface{}{})
			if out.Code != 0 {
				t.Fatalf("options %+v", out)
			}
			opts := out.Data.(map[string]interface{})["vendors"].([]interface{})
			if len(opts) != len(landingegress.SeedVendors()) || len(opts) != 17 {
				t.Fatalf("options %d", len(opts))
			}
			// Non-admins never reach any of it.
			for _, path := range []string{"/api/v1/node/list", "/api/v1/node/vendor/options", "/api/v1/node/update", "/api/v1/node/create"} {
				if out := requestContractEnvelope(t, router, user, path, map[string]interface{}{"id": n.ID, "name": "x", "serverIp": "192.0.2.9", "vendors": []string{"dmit"}}); out.Code != 403 {
					t.Fatalf("non-admin %s = %+v", path, out)
				}
			}
			if stored("火山广州").Vendors != "leikwan,volcengine" {
				t.Fatal("non-admin request changed the pick")
			}

			// Startup migration: a native landing's plan-316 vendor moves to
			// its node (only when the node has no pick); shares untouched.
			if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, port, created_time, status) VALUES('Bestvm US', 's-bvm', '192.0.2.17', '1000-2000', 1, 1)`).Error; err != nil {
				t.Fatal(err)
			}
			bvm := stored("Bestvm US")
			for _, s := range []model.LandingServer{
				{NodeID: bvm.ID, Source: "native", Name: "Bestvm US", PublicHost: "192.0.2.17", Status: "active", Vendor: "bestvm"},
				{NodeID: stored("小厂").ID, Source: "native", Name: "小厂", PublicHost: "192.0.2.53", Status: "active", Vendor: "dmit"},
				{NodeID: 0, Source: "shared", Name: "Boil Hinet 158", PublicHost: "192.0.2.158", Status: "active", Vendor: "boil"},
			} {
				s := s
				if err := r.DB().Create(&s).Error; err != nil {
					t.Fatal(err)
				}
			}
			r.Close()
			r = open()
			if n := stored("Bestvm US"); n.Vendors != "bestvm" || n.VendorOther != "" {
				t.Fatalf("migrated %q %q", n.Vendors, n.VendorOther)
			}
			if n := stored("小厂"); n.Vendors != "" || n.VendorOther != "某小厂" {
				t.Fatalf("a node with a pick was overwritten: %q %q", n.Vendors, n.VendorOther)
			}
			var share model.LandingServer
			if err := r.DB().Where("name = ?", "Boil Hinet 158").Take(&share).Error; err != nil || share.Vendor != "boil" {
				t.Fatalf("share %q %v", share.Vendor, err)
			}

			// An existing database without the columns (fork.176) gains them.
			r.Close()
			r = open()
			for _, col := range []string{"vendors", "vendor_other"} {
				if err := r.DB().Exec(`ALTER TABLE node DROP COLUMN ` + col).Error; err != nil {
					t.Fatalf("simulate fork.176 schema: %v", err)
				}
			}
			r.Close()
			r = open()
			// Bestvm US gets its vendor back from the landing (mirror).
			if n := stored("Bestvm US"); n.Vendors != "bestvm" {
				t.Fatalf("re-migrated %q", n.Vendors)
			}
			if n := stored("火山广州"); n.Vendors != "" || n.VendorOther != "" {
				t.Fatalf("dropped columns came back with %q %q", n.Vendors, n.VendorOther)
			}
		})
	}
}
