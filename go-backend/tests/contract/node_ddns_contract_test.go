package contract_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
)

// Plan 142 — node DDNS through the FULL router with real JWTs, SQLite +
// PostgreSQL: admin-only API (403 for users), token never returned, config
// saved with the node, rows removed with the node (DNS records stay).

type ddnsFakeAPI struct {
	mu      sync.Mutex
	created []map[string]interface{}
	deletes int
}

func (f *ddnsFakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodDelete {
		f.deletes++
	}
	switch {
	case r.URL.Path == "/user/tokens/verify":
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"t","status":"active"}}`))
	case r.URL.Path == "/zones":
		_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"z1","name":"imgamer.top","status":"active","permissions":["#dns_records:edit"]}],"result_info":{"page":1,"total_pages":1}}`))
	case r.URL.Path == "/zones/z1/dns_records" && r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
	case r.URL.Path == "/zones/z1/dns_records" && r.Method == http.MethodPost:
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.created = append(f.created, body)
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"r1"}}`))
	default:
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":7003,"message":"not found"}]}`))
	}
}

func TestNodeDDNSContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			for _, row := range []interface{}{
				&model.Node{ID: 48, Name: "Leikwanhost 沪台IPLC", ServerIP: "240e:b8f:a30:1904:ca:8aff:fec0:2813", Port: "10000-29999", Secret: "n48"},
				&model.User{ID: 802, User: "ddns-user", Flow: 100, Status: 1, RoleID: 1},
			} {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			fake := &ddnsFakeAPI{}
			api := httptest.NewServer(fake)
			defer api.Close()
			const secret = "node-ddns-contract"
			h := handler.New(r, secret, "test")
			h.SetMutationRateLimitEnabled(false)
			h.SetDDNSAPIBase(api.URL)
			router := httpserver.NewRouter(h, secret, r)
			admin, _ := auth.GenerateToken(1, "admin", 0, secret)
			user, _ := auth.GenerateToken(802, "ddns-user", 1, secret)
			call := func(token, path string, body interface{}) map[string]interface{} {
				t.Helper()
				out := requestContractEnvelope(t, router, token, "/api/v1/node/"+path, body)
				raw, _ := json.Marshal(out)
				var m map[string]interface{}
				_ = json.Unmarshal(raw, &m)
				return m
			}
			code := func(m map[string]interface{}) int { v, _ := m["code"].(float64); return int(v) }

			// Every DDNS route is admin-only.
			for path, body := range map[string]interface{}{
				"ddns/provider/list":   map[string]interface{}{},
				"ddns/provider/create": map[string]interface{}{"name": "x", "token": "t"},
				"ddns/provider/update": map[string]interface{}{"id": 1, "name": "x"},
				"ddns/provider/delete": map[string]interface{}{"id": 1},
				"ddns/provider/test":   map[string]interface{}{"token": "t"},
				"ddns/sync":            map[string]interface{}{"id": 48},
				"update":               map[string]interface{}{"id": 48, "ddns": map[string]interface{}{"enabled": true}},
			} {
				if out := call(user, path, body); code(out) != 403 {
					t.Fatalf("user %s: %+v", path, out)
				}
				if out := call("", path, body); code(out) != 401 {
					t.Fatalf("anonymous %s: %+v", path, out)
				}
			}

			const token = "cf-contract-token-abcdef"
			out := call(admin, "ddns/provider/create", map[string]interface{}{"name": "Cloudflare imgamer.top", "providerType": "cloudflare", "token": token})
			if code(out) != 0 {
				t.Fatalf("create provider: %+v", out)
			}
			listed := call(admin, "ddns/provider/list", map[string]interface{}{})
			raw, _ := json.Marshal(listed)
			if code(listed) != 0 || strings.Contains(string(raw), token) || !strings.Contains(string(raw), `"tokenSet":true`) || !strings.Contains(string(raw), `"tokenStatus":"active"`) {
				t.Fatalf("provider list: %s", raw)
			}
			pid := listed["data"].([]interface{})[0].(map[string]interface{})["id"]

			body := map[string]interface{}{"id": 48, "name": "Leikwanhost 沪台IPLC", "serverIpV4": "", "serverIpV6": "240e:b8f:a30:1904:ca:8aff:fec0:2813",
				"extraIPs": "2408:820c:750b:dc64:ca:8aff:fec0:2813", "port": "10000-29999", "tcpListenAddr": "[::]", "udpListenAddr": "[::]",
				"ddns": map[string]interface{}{"enabled": true, "providerId": pid, "records": []interface{}{
					map[string]interface{}{"domain": "leikwan-hutai-ct.imgamer.top", "type": "AAAA", "role": "main", "source": "ct"},
					map[string]interface{}{"domain": "leikwan-hutai-cu.imgamer.top", "type": "AAAA", "role": "extra", "source": "cu"},
				}}}
			if out := call(admin, "update", body); code(out) != 0 {
				t.Fatalf("node update: %+v", out)
			}
			h.WaitDDNS()
			var n model.Node
			if err := r.DB().First(&n, 48).Error; err != nil || !n.DDNSEnabled || n.ServerIPV6.String != "240e:b8f:a30:1904:ca:8aff:fec0:2813" {
				t.Fatalf("node after save (offline agent keeps the IP): %+v %v", n, err)
			}
			var recs []model.NodeDDNSRecord
			r.DB().Where("node_id = 48").Order("inx").Find(&recs)
			if len(recs) != 2 || recs[0].Status != model.DDNSStatusPending || recs[1].Role != "extra" {
				t.Fatalf("records: %+v", recs)
			}
			nodes := call(admin, "list", map[string]interface{}{})
			raw, _ = json.Marshal(nodes)
			if !strings.Contains(string(raw), `"ddns":{"addrReport":false`) || !strings.Contains(string(raw), `"providerName":"Cloudflare imgamer.top"`) || strings.Contains(string(raw), token) {
				t.Fatalf("node list lacks ddns view or leaks the token: %.400s", raw)
			}
			// Unique (domain, type) across nodes, on both dialects.
			if err := r.DB().Create(&model.NodeDDNSRecord{NodeID: 49, Domain: "leikwan-hutai-ct.imgamer.top", RecordType: "AAAA"}).Error; err == nil {
				t.Fatal("duplicate domain+type inserted")
			}
			// Provider in use cannot be deleted; node delete drops the rows.
			if out := call(admin, "ddns/provider/delete", map[string]interface{}{"id": pid}); code(out) == 0 {
				t.Fatalf("delete in use: %+v", out)
			}
			if out := call(admin, "delete", map[string]interface{}{"id": 48}); code(out) != 0 {
				t.Fatalf("node delete: %+v", out)
			}
			var left int64
			r.DB().Model(&model.NodeDDNSRecord{}).Where("node_id = 48").Count(&left)
			if left != 0 || fake.deletes != 0 {
				t.Fatalf("rows left %d, Cloudflare deletes %d", left, fake.deletes)
			}
			if out := call(admin, "ddns/provider/delete", map[string]interface{}{"id": pid}); code(out) != 0 {
				t.Fatalf("delete unused provider: %+v", out)
			}
		})
	}
}
