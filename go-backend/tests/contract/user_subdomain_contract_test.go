package contract_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
	"go-backend/internal/usersub"
)

// Plan 303 — 用户专属域名 through the full router (real JWTs), SQLite +
// PostgreSQL, against a stateful fake Cloudflare zone: names created for
// users on toggled nodes, paused (record deleted, name kept) on 停用 / 到期,
// back with the same name, retired on delete (never for "no forward on the
// node", round 2), 改指 / 换新名 / 删除 /
// 重新分配, toggle off/on, wildcard refusal, quota cap, hand-out in the
// forward list, and records the panel did not write are never touched.

type usFakeCF struct {
	mu      sync.Mutex
	recs    map[string]map[string]interface{}
	next    int
	quota   int
	deleted []string
	writes  int
}

func newUSFakeCF() *usFakeCF {
	f := &usFakeCF{recs: map[string]map[string]interface{}{}, quota: 200}
	f.add("AAAA", "vmsilo-huri.imgamer.top", "240e:96c:7100:1fe:1c8:c8aa:4ecc:e4", "FLVX DDNS")
	f.add("AAAA", "vmsilo-humei.imgamer.top", "240e:96c:7100:1fe:1c8:c8aa:4ecc:8f", "FLVX DDNS")
	f.add("A", "mmwx.imgamer.top", "43.199.143.188", "")
	f.add("CNAME", "flvxx.imgamer.top", "tunnel.example.com", "")
	return f
}

func (f *usFakeCF) add(typ, name, content, comment string) string {
	f.next++
	id := fmt.Sprintf("rec%03d", f.next)
	f.recs[id] = map[string]interface{}{"id": id, "type": typ, "name": name, "content": content, "ttl": 120, "proxied": false, "comment": comment}
	return id
}

func (f *usFakeCF) byName(name string) []map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]interface{}
	for _, r := range f.recs {
		if r["name"] == name {
			out = append(out, r)
		}
	}
	return out
}

func (f *usFakeCF) userCNAMEs() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for _, r := range f.recs {
		if r["type"] == "CNAME" && r["comment"] == "flvx-user" {
			out[r["name"].(string)] = r["content"].(string)
		}
	}
	return out
}

func (f *usFakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	defer f.mu.Unlock()
	ok := func(result interface{}, info interface{}) {
		body := map[string]interface{}{"success": true, "errors": []interface{}{}, "result": result}
		if info != nil {
			body["result_info"] = info
		}
		_ = json.NewEncoder(w).Encode(body)
	}
	fail := func(status, code int, msg string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "errors": []interface{}{map[string]interface{}{"code": code, "message": msg}}})
	}
	p := r.URL.Path
	switch {
	case p == "/user/tokens/verify":
		ok(map[string]interface{}{"id": "t", "status": "active"}, nil)
	case p == "/zones":
		ok([]interface{}{map[string]interface{}{"id": "z1", "name": "imgamer.top", "status": "active", "permissions": []string{"#dns_records:edit"}}}, map[string]interface{}{"page": 1, "total_pages": 1})
	case p == "/zones/z1/dns_records/usage":
		ok(map[string]interface{}{"record_quota": f.quota, "record_usage": len(f.recs)}, nil)
	case p == "/zones/z1/dns_records" && r.Method == http.MethodGet:
		name, typ := r.URL.Query().Get("name"), r.URL.Query().Get("type")
		ids := make([]string, 0, len(f.recs))
		for id := range f.recs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		list := []interface{}{}
		for _, id := range ids {
			rec := f.recs[id]
			if (name == "" || rec["name"] == name) && (typ == "" || rec["type"] == typ) {
				list = append(list, rec)
			}
		}
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if per <= 0 {
			per = 100
		}
		if page <= 0 {
			page = 1
		}
		total := len(list)
		pages := (total + per - 1) / per
		lo, hi := (page-1)*per, page*per
		if lo > total {
			lo = total
		}
		if hi > total {
			hi = total
		}
		ok(list[lo:hi], map[string]interface{}{"page": page, "total_pages": pages, "total_count": total})
	case p == "/zones/z1/dns_records" && r.Method == http.MethodPost:
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(f.recs) >= f.quota {
			fail(400, 81045, "Record quota exceeded.")
			return
		}
		f.writes++
		id := f.add(body["type"].(string), body["name"].(string), body["content"].(string), fmt.Sprint(body["comment"]))
		ok(f.recs[id], nil)
	case strings.HasPrefix(p, "/zones/z1/dns_records/"):
		id := strings.TrimPrefix(p, "/zones/z1/dns_records/")
		rec, found := f.recs[id]
		if !found {
			fail(404, 81044, "Record does not exist.")
			return
		}
		switch r.Method {
		case http.MethodGet:
			ok(rec, nil)
		case http.MethodPut:
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.writes++
			for _, k := range []string{"type", "name", "content", "comment"} {
				rec[k] = body[k]
			}
			ok(rec, nil)
		case http.MethodDelete:
			f.deleted = append(f.deleted, rec["name"].(string))
			delete(f.recs, id)
			ok(map[string]interface{}{"id": id}, nil)
		}
	default:
		fail(404, 7003, "not found")
	}
}

func TestUserSubdomainContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			db := r.DB()
			mustCreate := func(rows ...interface{}) {
				t.Helper()
				for _, row := range rows {
					if err := db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			mustCreate(
				&model.Node{ID: 51, Name: "vmsilo 沪日首发", ServerIP: "vmsilo-huri.imgamer.top", ServerIPV6: sql.NullString{String: "vmsilo-huri.imgamer.top", Valid: true}, Port: "10000-65535", Secret: "n51", DDNSEnabled: true, DDNSProviderID: 1},
				&model.Node{ID: 59, Name: "vmsilo 沪美", ServerIP: "vmsilo-humei.imgamer.top", ServerIPV6: sql.NullString{String: "vmsilo-humei.imgamer.top", Valid: true}, Port: "10000-65535", Secret: "n59", DDNSEnabled: true, DDNSProviderID: 1},
				&model.User{ID: 801, User: "fanscool", Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 100, FlowResetTime: 1, Num: 10, Status: 1},
				&model.User{ID: 802, User: "ns-robot", Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 100, FlowResetTime: 1, Num: 10, Status: 1},
				&model.User{ID: 803, User: "stopped", Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 100, FlowResetTime: 1, Num: 10, Status: 0},
			)
			fake := newUSFakeCF()
			api := httptest.NewServer(fake)
			defer api.Close()
			const secret = "user-subdomain-contract"
			h := handler.New(r, secret, "test")
			h.SetMutationRateLimitEnabled(false)
			h.SetDDNSAPIBase(api.URL)
			router := httpserver.NewRouter(h, secret, r)
			admin, _ := auth.GenerateToken(1, "admin", 0, secret)
			userTok, _ := auth.GenerateToken(801, "fanscool", 1, secret)
			call := func(token, path string, body interface{}) map[string]interface{} {
				t.Helper()
				out := requestContractEnvelope(t, router, token, "/api/v1/"+path, body)
				raw, _ := json.Marshal(out)
				var m map[string]interface{}
				_ = json.Unmarshal(raw, &m)
				return m
			}
			code := func(m map[string]interface{}) int { v, _ := m["code"].(float64); return int(v) }

			// Provider + already synced DDNS main records (as on prod after plan 302).
			if out := call(admin, "node/ddns/provider/create", map[string]interface{}{"name": "Cloudflare imgamer.top", "providerType": "cloudflare", "token": "cf-token-303-abcdef"}); code(out) != 0 {
				t.Fatalf("provider: %+v", out)
			}
			for _, rec := range []model.NodeDDNSRecord{
				{NodeID: 51, Domain: "vmsilo-huri.imgamer.top", RecordType: "AAAA", Role: "main", Source: "ct", ZoneID: "z1", RecordID: "rec001", CurrentIP: "240e:96c:7100:1fe:1c8:c8aa:4ecc:e4", TTL: 120, Applied: true, Status: "synced", LastSyncAt: now},
				{NodeID: 59, Domain: "vmsilo-humei.imgamer.top", RecordType: "AAAA", Role: "main", Source: "ct", ZoneID: "z1", RecordID: "rec002", CurrentIP: "240e:96c:7100:1fe:1c8:c8aa:4ecc:8f", TTL: 120, Applied: true, Status: "synced", LastSyncAt: now},
			} {
				rec := rec
				mustCreate(&rec)
			}
			tunnel := model.Tunnel{Name: "沪日IEPL -> JP", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1}
			mustCreate(&tunnel)
			fwd := func(uid int64, name string, node int64, port int) int64 {
				t.Helper()
				f := model.Forward{UserID: uid, UserName: fmt.Sprint(uid), Name: name, TunnelID: tunnel.ID, RemoteAddr: "127.0.0.1:1", Strategy: "fifo", CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost"}
				mustCreate(&f)
				mustCreate(&model.ForwardPort{ForwardID: f.ID, NodeID: node, Port: port})
				return f.ID
			}
			fwd(801, "沪日", 51, 52168)
			fwd(801, "沪日2", 51, 64872)
			f802 := fwd(802, "robot", 51, 60099)
			fwd(803, "stopped", 51, 30001)
			fwd(1, "admin-test", 51, 30002)

			nodeSave := func(id int64, domain string, on bool) map[string]interface{} {
				return call(admin, "node/update", map[string]interface{}{"id": id, "name": map[int64]string{51: "vmsilo 沪日首发", 59: "vmsilo 沪美"}[id],
					"serverIpV4": "", "serverIpV6": domain, "port": "10000-65535", "tcpListenAddr": "[::]", "udpListenAddr": "[::]",
					"ddns": map[string]interface{}{"enabled": true, "providerId": 1, "userSubdomain": on, "records": []interface{}{
						map[string]interface{}{"domain": domain, "type": "AAAA", "role": "main", "source": "ct"},
					}}})
			}
			reconcile := func() { h.WaitDDNS(); h.UserSubdomainReconcileNow(true) }
			names := func(where string, args ...interface{}) []model.UserSubdomain {
				var rows []model.UserSubdomain
				db.Where(where, args...).Order("id").Find(&rows)
				return rows
			}

			// Admin only.
			if out := call(userTok, "node/user-subdomain/list", map[string]interface{}{"nodeId": 51}); code(out) != 403 {
				t.Fatalf("user list: %+v", out)
			}

			// 1. Toggle on 沪日首发 → fanscool + ns-robot get a name; 停用 / admin don't.
			if out := nodeSave(51, "vmsilo-huri.imgamer.top", true); code(out) != 0 {
				t.Fatalf("toggle on: %+v", out)
			}
			reconcile()
			live := names("state = ?", "active")
			if len(live) != 2 || live[0].UserID != 801 || live[1].UserID != 802 || !live[0].Live() || !live[1].Live() {
				t.Fatalf("names after toggle: %+v", live)
			}
			fan, robot := live[0], live[1]
			cn := fake.userCNAMEs()
			if len(cn) != 2 || cn[fan.Domain] != "vmsilo-huri.imgamer.top" || !strings.HasSuffix(fan.Domain, ".imgamer.top") || fan.Domain == robot.Domain {
				t.Fatalf("cf cnames: %+v", cn)
			}
			if strings.Contains(fan.Domain, "fanscool") || len(fan.Label) != 7 {
				t.Fatalf("label leaks the user / wrong length: %s", fan.Domain)
			}

			// Hand-out: the owner's forward list shows his name; admin sees each owner's.
			list := call(userTok, "forward/list", map[string]interface{}{})
			raw, _ := json.Marshal(list)
			if !strings.Contains(string(raw), fan.Domain+":52168") || strings.Contains(string(raw), robot.Domain) {
				t.Fatalf("user forward list: %.600s", raw)
			}
			list = call(admin, "forward/list", map[string]interface{}{})
			raw, _ = json.Marshal(list)
			if !strings.Contains(string(raw), robot.Domain+":60099") || !strings.Contains(string(raw), "vmsilo-huri.imgamer.top:30002") {
				t.Fatalf("admin forward list: %.900s", raw)
			}
			x, _ := r.LoadUserEntryNames([]int64{801})
			if uh := x.Resolve(801, 51, "vmsilo-huri.imgamer.top"); uh.Host != fan.Domain || !uh.V6Only || !uh.Name || uh.Tag != "" {
				t.Fatalf("resolve (IPv6-only main → the name for every client): %+v", uh)
			}
			if name, _ := x.Lookup(801, 51, "[240e:96c:7100:1fe:1c8:c8aa:4ecc:e4]"); name != fan.Domain {
				t.Fatalf("lookup literal entry: %q", name)
			}
			if name, _ := x.Lookup(802, 51, "vmsilo-huri.imgamer.top"); name != "" {
				t.Fatal("user 802 not loaded")
			}

			// Node list pill + detail list.
			nl := call(admin, "node/list", map[string]interface{}{})
			raw, _ = json.Marshal(nl)
			if !strings.Contains(string(raw), `"userSubdomain":{"active":2,`) || !strings.Contains(string(raw), `"effective":true,"failed":0,"mainDomain":"vmsilo-huri.imgamer.top"`) {
				t.Fatalf("node list pill: %.1500s", raw)
			}
			detail := call(admin, "node/user-subdomain/list", map[string]interface{}{"nodeId": 51})
			raw, _ = json.Marshal(detail)
			if code(detail) != 0 || !strings.Contains(string(raw), `"userName":"fanscool"`) || !strings.Contains(string(raw), `"port":64872`) || !strings.Contains(string(raw), `"quota":200`) {
				t.Fatalf("detail: %.1500s", raw)
			}
			prov := call(admin, "node/ddns/provider/list", map[string]interface{}{"stats": true})
			raw, _ = json.Marshal(prov)
			if !strings.Contains(string(raw), `"names":2`) || !strings.Contains(string(raw), `"used":6`) {
				t.Fatalf("provider stats: %.900s", raw)
			}

			// 2. 停用 → record deleted, name kept; 启用 → same name back.
			db.Model(&model.User{}).Where("id = 802").Update("status", 0)
			reconcile()
			if got := names("id = ?", robot.ID)[0]; got.State != "paused" || got.Reason != "disabled" || got.RecordID != "" || len(fake.byName(robot.Domain)) != 0 {
				t.Fatalf("disabled: %+v %v", got, fake.byName(robot.Domain))
			}
			db.Model(&model.User{}).Where("id = 802").Update("status", 1)
			reconcile()
			if got := names("id = ?", robot.ID)[0]; !got.Live() || got.Domain != robot.Domain || len(fake.byName(robot.Domain)) != 1 {
				t.Fatalf("re-enabled: %+v", got)
			}
			// 到期 (expiry job not run yet) → paused; 续期 → back.
			db.Model(&model.User{}).Where("id = 801").Update("exp_time", now-1000)
			reconcile()
			if got := names("id = ?", fan.ID)[0]; got.State != "paused" || got.Reason != "expired" || len(fake.byName(fan.Domain)) != 0 {
				t.Fatalf("expired: %+v", got)
			}
			// Round 2: the name stays (record deleted on purpose), tagged.
			list = call(userTok, "forward/list", map[string]interface{}{})
			raw, _ = json.Marshal(list)
			if !strings.Contains(string(raw), fan.Domain+":52168") || !strings.Contains(string(raw), `"entryNameTags":{"`+fan.Domain+`":"expired"}`) || strings.Contains(string(raw), "vmsilo-huri.imgamer.top:52168") {
				t.Fatalf("expired name not kept + tagged: %.900s", raw)
			}
			db.Model(&model.User{}).Where("id = 801").Update("exp_time", now+30*86400000)
			reconcile()
			if got := names("id = ?", fan.ID)[0]; !got.Live() || got.Domain != fan.Domain {
				t.Fatalf("renewed: %+v", got)
			}

			// 3. Wildcard refuses the toggle on 沪美; without it the toggle works.
			wild := fake.add("A", "*.imgamer.top", "192.0.2.1", "")
			if out := nodeSave(59, "vmsilo-humei.imgamer.top", true); code(out) == 0 || !strings.Contains(fmt.Sprint(out["msg"]), "通配") {
				t.Fatalf("wildcard not refused: %+v", out)
			}
			fake.mu.Lock()
			delete(fake.recs, wild)
			fake.mu.Unlock()
			if out := nodeSave(59, "vmsilo-humei.imgamer.top", true); code(out) != 0 {
				t.Fatalf("toggle 59: %+v", out)
			}
			reconcile()
			if rows := names("node_id = 59 AND state = ?", "active"); len(rows) != 0 {
				t.Fatalf("nobody uses 沪美 yet: %+v", rows)
			}

			// 4. 改指 ns-robot → 沪美: refused without a forward there, then OK.
			out := call(admin, "node/user-subdomain/repoint", map[string]interface{}{"id": robot.ID, "nodeId": 59})
			if code(out) == 0 || !strings.Contains(fmt.Sprint(out["msg"]), "没有转发") {
				t.Fatalf("repoint without forward: %+v", out)
			}
			fwd(802, "robot-hm", 59, 60099)
			out = call(admin, "node/user-subdomain/repoint", map[string]interface{}{"id": robot.ID, "nodeId": 59})
			if code(out) != 0 || fake.userCNAMEs()[robot.Domain] != "vmsilo-humei.imgamer.top" {
				t.Fatalf("repoint: %+v %+v", out, fake.userCNAMEs())
			}
			// The moved name covers (robot, 沪美); robot still has 60099 on
			// 沪日首发, so the same pass gives him a new own name there.
			rows := names("user_id = 802 AND state = ?", "active")
			if len(rows) != 2 || rows[0].NodeID != 59 || rows[0].OriginNodeID != 51 || rows[1].NodeID != 51 {
				t.Fatalf("after repoint: %+v", rows)
			}
			d51 := call(admin, "node/user-subdomain/list", map[string]interface{}{"nodeId": 51})
			raw, _ = json.Marshal(d51)
			if !strings.Contains(string(raw), `"moved":"out"`) {
				t.Fatalf("moved-out not listed on origin: %.900s", raw)
			}
			d59 := call(admin, "node/user-subdomain/list", map[string]interface{}{"nodeId": 59})
			raw, _ = json.Marshal(d59)
			if !strings.Contains(string(raw), `"moved":"in"`) || !strings.Contains(string(raw), `"originNodeName":"vmsilo 沪日首发"`) {
				t.Fatalf("moved-in not listed: %.900s", raw)
			}
			robot51 := rows[1]

			// 5. 换新名: old retired + deleted in CF, new label created.
			out = call(admin, "node/user-subdomain/rename", map[string]interface{}{"id": robot51.ID})
			if code(out) != 0 {
				t.Fatalf("rename: %+v", out)
			}
			if old := names("id = ?", robot51.ID)[0]; old.State != "retired" || old.Reason != "renamed" || len(fake.byName(robot51.Domain)) != 0 {
				t.Fatalf("renamed old: %+v", old)
			}
			fresh := names("user_id = 802 AND node_id = 51 AND state = ?", "active")
			if len(fresh) != 1 || fresh[0].Domain == robot51.Domain || !fresh[0].Live() {
				t.Fatalf("renamed new: %+v", fresh)
			}

			// 6. 删除: retired + blocked → no auto re-create; 重新分配 → a new name.
			out = call(admin, "node/user-subdomain/delete", map[string]interface{}{"id": fresh[0].ID})
			reconcile()
			if code(out) != 0 || len(names("user_id = 802 AND node_id = 51 AND state <> ?", "retired")) != 0 || len(fake.byName(fresh[0].Domain)) != 0 {
				t.Fatalf("admin delete: %+v", out)
			}
			// Round 2: he keeps the dead name (tag deleted), never the main domain.
			list = call(admin, "forward/list", map[string]interface{}{})
			raw, _ = json.Marshal(list)
			if !strings.Contains(string(raw), fresh[0].Domain+":60099") || !strings.Contains(string(raw), `"`+fresh[0].Domain+`":"deleted"`) || strings.Contains(string(raw), "vmsilo-huri.imgamer.top:60099") {
				t.Fatalf("admin-deleted name not kept: %.1200s", raw)
			}
			out = call(admin, "node/user-subdomain/allow", map[string]interface{}{"id": fresh[0].ID})
			again := names("user_id = 802 AND node_id = 51 AND state = ?", "active")
			if code(out) != 0 || len(again) != 1 || again[0].Domain == fresh[0].Domain || !again[0].Live() {
				t.Fatalf("allow: %+v %+v", out, again)
			}

			// 7. Round 2: the last forward on the node gone → the name stays
			// live (no pause / retire while the user exists and is active).
			db.Where("forward_id = ?", f802).Delete(&model.ForwardPort{})
			db.Where("id = ?", f802).Delete(&model.Forward{})
			reconcile()
			if got := names("id = ?", again[0].ID)[0]; !got.Live() || got.Reason != "" || len(fake.byName(got.Domain)) != 1 {
				t.Fatalf("no forward on the node: %+v", got)
			}

			// 8. User deleted → retired, record gone, label never reused.
			out = call(admin, "user/delete", map[string]interface{}{"id": 802})
			if code(out) != 0 {
				t.Fatalf("user delete: %+v", out)
			}
			reconcile()
			if got := names("id = ?", robot.ID)[0]; got.State != "retired" || got.Reason != "user_deleted" || len(fake.byName(robot.Domain)) != 0 {
				t.Fatalf("user deleted: %+v", got)
			}

			// 9. Toggle off → records deleted, names kept; on again → the same names.
			if out := nodeSave(51, "vmsilo-huri.imgamer.top", false); code(out) != 0 {
				t.Fatalf("toggle off: %+v", out)
			}
			reconcile()
			if got := names("id = ?", fan.ID)[0]; got.State != "paused" || got.Reason != "node" || len(fake.byName(fan.Domain)) != 0 {
				t.Fatalf("toggle off: %+v", got)
			}
			nodeSave(51, "vmsilo-huri.imgamer.top", true)
			reconcile()
			if got := names("id = ?", fan.ID)[0]; !got.Live() || got.Domain != fan.Domain {
				t.Fatalf("toggle on again: %+v", got)
			}

			// 10. Quota: full zone → no new name, node ⚠ capped.
			fake.mu.Lock()
			fake.quota = len(fake.recs) + usersub.CapReserve
			fake.mu.Unlock()
			mustCreate(&model.User{ID: 804, User: "late", Pwd: "x", RoleID: 1, ExpTime: now + 30*86400000, Flow: 100, FlowResetTime: 1, Num: 10, Status: 1})
			fwd(804, "late", 51, 30003)
			reconcile()
			if rows := names("user_id = 804"); len(rows) != 0 {
				t.Fatalf("created past the cap: %+v", rows)
			}
			// No name yet: the main domain (never the IP), ⚠ tag for the admin only.
			list = call(admin, "forward/list", map[string]interface{}{})
			raw, _ = json.Marshal(list)
			if !strings.Contains(string(raw), "vmsilo-huri.imgamer.top:30003") || !strings.Contains(string(raw), `"vmsilo-huri.imgamer.top":"fallback"`) {
				t.Fatalf("fallback not tagged for the admin: %.1500s", raw)
			}
			lateTok, _ := auth.GenerateToken(804, "late", 1, secret)
			list = call(lateTok, "forward/list", map[string]interface{}{})
			raw, _ = json.Marshal(list)
			if !strings.Contains(string(raw), "vmsilo-huri.imgamer.top:30003") || strings.Contains(string(raw), "entryNameTags") {
				t.Fatalf("fallback for the user: %.900s", raw)
			}
			nl = call(admin, "node/list", map[string]interface{}{})
			raw, _ = json.Marshal(nl)
			if !strings.Contains(string(raw), `"capped":1`) || !strings.Contains(string(raw), `"attention":true`) {
				t.Fatalf("cap not shown: %.1500s", raw)
			}

			// Drift: a record removed in the dashboard comes back on the full pass.
			fake.mu.Lock()
			for id, rec := range fake.recs {
				if rec["name"] == fan.Domain {
					delete(fake.recs, id)
				}
			}
			fake.mu.Unlock()
			reconcile()
			if len(fake.byName(fan.Domain)) != 1 {
				t.Fatal("drift not repaired")
			}

			// Records the panel did not write were never deleted or changed.
			for _, name := range []string{"vmsilo-huri.imgamer.top", "vmsilo-humei.imgamer.top", "mmwx.imgamer.top", "flvxx.imgamer.top"} {
				recs := fake.byName(name)
				if len(recs) != 1 || recs[0]["comment"] == "flvx-user" {
					t.Fatalf("foreign record %s touched: %+v", name, recs)
				}
			}
			for _, d := range fake.deleted {
				if !strings.HasSuffix(d, ".imgamer.top") || strings.HasPrefix(d, "vmsilo-") || d == "mmwx.imgamer.top" || d == "flvxx.imgamer.top" {
					t.Fatalf("deleted a foreign record %s", d)
				}
			}
			var labels []string
			db.Model(&model.UserSubdomain{}).Pluck("label", &labels)
			seen := map[string]bool{}
			for _, l := range labels {
				if seen[l] {
					t.Fatalf("label reused: %s", l)
				}
				seen[l] = true
			}
		})
	}
}
