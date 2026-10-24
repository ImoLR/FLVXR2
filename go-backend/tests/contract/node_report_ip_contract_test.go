package contract_test

import (
	"database/sql"
	"testing"

	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
)

// Plan 147: /api/v1/node/report-ip authenticates by the node secret (it used
// to be rejected by the JWT middleware — bad_token after every reconnect) and
// applies the fill-empty rule: no address an admin or DDNS set ever changes.
func TestNodeReportIPContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			const jwt = "plan147-report-ip"
			r := landingContractRepo(t, dialect)
			router := httpserver.NewRouter(handler.New(r, jwt, "test"), jwt, r)
			v := func(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
			for _, n := range []*model.Node{
				{ID: 801, Name: "literal", Secret: "s-literal", ServerIP: "203.0.114.20", Port: "10000-20000", Status: 1},
				{ID: 802, Name: "domain", Secret: "s-domain", ServerIP: "hk.example.net", ServerIPV4: v("hk.example.net"), Port: "10000-20000", Status: 1},
				{ID: 848, Name: "ddns", Secret: "s-ddns", ServerIP: "leikwan-hutai-ct.imgamer.top", ServerIPV6: v("leikwan-hutai-ct.imgamer.top"), ExtraIPs: v("leikwan-hutai-cu.imgamer.top"), DDNSEnabled: true, Port: "10000-20000", Status: 1},
			} {
				if err := r.DB().Create(n).Error; err != nil {
					t.Fatal(err)
				}
			}
			load := func(id int64) model.Node {
				t.Helper()
				var n model.Node
				if err := r.DB().First(&n, id).Error; err != nil {
					t.Fatal(err)
				}
				return n
			}
			out := requestContractEnvelope(t, router, "s-literal", "/api/v1/node/report-ip", map[string]string{"public_ip_v4": "203.0.114.20", "public_ip_v6": "2400:cb00::20"})
			if out.Code != 0 {
				t.Fatalf("literal report refused: %+v", out)
			}
			if n := load(801); n.ServerIP != "203.0.114.20" || n.ServerIPV4.String != "203.0.114.20" || n.ServerIPV6.String != "" {
				t.Fatalf("literal node: %q %q %q", n.ServerIP, n.ServerIPV4.String, n.ServerIPV6.String)
			}
			for _, c := range []struct {
				id     int64
				secret string
			}{{802, "s-domain"}, {848, "s-ddns"}} {
				before := load(c.id)
				out := requestContractEnvelope(t, router, c.secret, "/api/v1/node/report-ip", map[string]string{"public_ip": "203.0.114.30"})
				if out.Code != 0 {
					t.Fatalf("node %d report refused: %+v", c.id, out)
				}
				after := load(c.id)
				if after.ServerIP != before.ServerIP || after.ServerIPV4 != before.ServerIPV4 || after.ServerIPV6 != before.ServerIPV6 || after.ExtraIPs != before.ExtraIPs {
					t.Fatalf("node %d address changed: %+v", c.id, after)
				}
			}
			if out := requestContractEnvelope(t, router, "", "/api/v1/node/report-ip", map[string]string{"public_ip": "203.0.114.30"}); out.Code != 401 {
				t.Fatalf("no secret: %+v", out)
			}
			if out := requestContractEnvelope(t, router, "wrong", "/api/v1/node/report-ip", map[string]string{"public_ip": "203.0.114.30"}); out.Code != 401 || out.Msg != "节点认证失败" {
				t.Fatalf("unknown secret: %+v", out)
			}
		})
	}
}
