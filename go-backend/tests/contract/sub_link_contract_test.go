package contract_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"

	"gopkg.in/yaml.v3"
)

const (
	subUUID   = "8a4f3f6e-1c2b-4d5e-9f00-112233445566"
	subPBK    = "Y5u4vH0Qp6yG9mKZr3QZbqSx2n8Hk8TtZl1u8rH0v0E"
	subSecret = "sub-link-contract"
)

func subKey(n int) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{byte(n)}, 16))
}

// TestSubLinkContract: plan 185 subscription links on SQLite and PostgreSQL —
// CRUD, ownership, the public /s/<token> endpoint (formats, headers, browser
// page without secrets, 404 / 403), reset and the user-delete cascade.
func TestSubLinkContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			now := time.Now().UnixMilli()
			socks := linechain.Upstream{Type: "socks", IPVersion: "system", Server: "home.example.net", Port: 1080, Username: "u", Password: "socks-secret-pw"}
			rows := []interface{}{
				&model.User{ID: 801, User: "sub-a", Pwd: "x", RoleID: 1, Status: 1, Flow: 100, InFlow: 3 << 30, OutFlow: 1 << 30, ExpTime: now + 86400000, FlowResetTime: 1, Num: 10},
				&model.User{ID: 802, User: "sub-b", Pwd: "x", RoleID: 1, Status: 1, Flow: 99999, ExpTime: now + 86400000, FlowResetTime: 1, Num: 10},
				&model.Node{ID: 601, Name: "东京落地", Secret: "s601", ServerIP: "203.0.113.21", Port: "10000-19999"},
				&model.Node{ID: 602, Name: "上海联通入口", Secret: "s602", ServerIP: "198.51.100.10", Port: "20000-29999"},
				&model.Tunnel{ID: 811, Name: "沪日440", Status: 1, Type: 2, Flow: 2, TrafficRatio: 1},
				&model.ChainTunnel{TunnelID: 811, NodeID: 602, ChainType: "1"},
				&model.ChainTunnel{TunnelID: 811, NodeID: 601, ChainType: "3"},
				&model.LandingServer{ID: 11, Source: "native", NodeID: 601, Name: "东京", PublicHost: "203.0.113.21", FlowType: 2, TrafficRatio: 1, Status: "active"},
				&model.LandingNode{ID: 21, UserID: 801, LandingServerID: 11, Name: "我的落地·东京", Protocol: "vless_reality_vision", Port: 30001, InboundTag: "flvxl-21", Status: "active",
					UUID: subUUID, PublicKey: subPBK, ShortID: "6ba85179e30d4fc2", ServerNames: `["www.microsoft.com"]`, Flow: "xtls-rprx-vision"},
				&model.LandingNode{ID: 22, UserID: 801, LandingServerID: 11, Name: "游戏加速", Protocol: "ss2022", Port: 30002, InboundTag: "flvxl-22", Status: "active",
					Method: "2022-blake3-aes-128-gcm", ServerKey: subKey(1), UserKey: subKey(2)},
				&model.LandingNode{ID: 23, UserID: 802, LandingServerID: 11, Name: "B 的落地", Protocol: "ss2022", Port: 30003, InboundTag: "flvxl-23", Status: "active",
					Method: "2022-blake3-aes-128-gcm", ServerKey: subKey(3), UserKey: subKey(4)},
				&model.Forward{ID: 901, UserID: 801, UserName: "sub-a", Name: "游戏加速", TunnelID: 811, Status: 1, LandingNodeID: 22, RemoteAddr: "203.0.113.21:30002"},
				&model.ForwardPort{ForwardID: 901, NodeID: 602, Port: 20009},
				&model.Forward{ID: 902, UserID: 801, UserName: "sub-a", Name: "SSH跳板", TunnelID: 811, Status: 1, RemoteAddr: "192.0.2.1:22"},
				&model.ForwardPort{ForwardID: 902, NodeID: 602, Port: 20010},
				&model.Forward{ID: 903, UserID: 802, UserName: "sub-b", Name: "B 的规则", TunnelID: 811, Status: 1, LandingNodeID: 23, RemoteAddr: "203.0.113.21:30003"},
				&model.ForwardPort{ForwardID: 903, NodeID: 602, Port: 20011},
				&model.LandingExternalNode{ID: 31, UserID: 801, Name: "我家宽带 Socks", Protocol: "socks", Host: socks.Server, Port: socks.Port, Summary: socks.Summary(), Upstream: socks.JSON(), UpstreamHash: socks.Hash()},
			}
			for _, row := range rows {
				if err := r.DB().Create(row).Error; err != nil {
					t.Fatalf("seed %T: %v", row, err)
				}
			}
			// A synced external source (fresh, so no fetch happens).
			hy2 := linechain.Upstream{Type: "hysteria2", IPVersion: "system", Server: "hk1.provider.example", Port: 443, Password: "airport-pw", Security: "tls", TLS: &linechain.TLS{ServerName: "hk1.provider.example"}}
			cached, _ := json.Marshal([]sublink.SourceNode{{Name: "🇭🇰 香港 01", Up: hy2}, {Name: "🇭🇰 香港 01", Up: hy2}})
			src := &model.UserSubSource{ID: 41, UserID: 801, Name: "机场A", URLEnc: "x", URLHint: "sub.provider.example/…", LastOkAt: now, LastFetchAt: now, CachedNodes: string(cached), NodeCount: 2}
			if err := r.DB().Create(src).Error; err != nil {
				t.Fatal(err)
			}

			h := handler.New(r, subSecret, "test")
			router := httpserver.NewRouter(h, subSecret, r)
			a, _ := auth.GenerateToken(801, "sub-a", 1, subSecret)
			b, _ := auth.GenerateToken(802, "sub-b", 1, subSecret)
			admin, _ := auth.GenerateToken(1, "admin", 0, subSecret)
			call := func(path, token string, body interface{}) response.R {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/"+path, bytes.NewReader(raw))
				req.Header.Set("Authorization", token)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				var out response.R
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err, w.Body.String())
				}
				return out
			}
			data := func(out response.R) map[string]interface{} {
				t.Helper()
				if out.Code != 0 {
					t.Fatalf("api error: %+v", out)
				}
				b, _ := json.Marshal(out.Data)
				m := map[string]interface{}{}
				_ = json.Unmarshal(b, &m)
				return m
			}
			ip := 0
			pull := func(token, ua, query string) *httptest.ResponseRecorder {
				t.Helper()
				ip++
				req := httptest.NewRequest(http.MethodGet, "/s/"+token+query, nil)
				req.RemoteAddr = "203.0.113." + strconv.Itoa(100+ip%50) + ":4000"
				req.Header.Set("User-Agent", ua)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w
			}

			// Picker pool: A's rule link + 2 landing 直连 + external + source; B's rows absent.
			pool := data(call("sub/pool", a, map[string]interface{}{}))
			rules, _ := pool["rules"].([]interface{})
			landing, _ := pool["landing"].([]interface{})
			if len(rules) != 1 || len(landing) != 2 || len(pool["external"].([]interface{})) != 1 || len(pool["sources"].([]interface{})) != 1 || pool["portForwards"].(float64) != 1 {
				t.Fatalf("pool: %v", pool)
			}
			ruleRef := rules[0].(map[string]interface{})["ref"]
			if raw, _ := json.Marshal(pool); strings.Contains(string(raw), "socks-secret-pw") || strings.Contains(string(raw), "airport-pw") || strings.Contains(string(raw), subUUID) {
				t.Fatal("pool leaks secrets")
			}

			// Create: B's rule / landing references are dropped (ownership).
			items := []interface{}{
				ruleRef,
				map[string]interface{}{"kind": "landing_direct", "landingId": 21},
				map[string]interface{}{"kind": "external_node", "externalId": 31},
				map[string]interface{}{"kind": "source_all", "sourceId": 41},
				map[string]interface{}{"kind": "forward_link", "forwardId": 903, "entry": "602|198.51.100.10"},
				map[string]interface{}{"kind": "landing_direct", "landingId": 23},
			}
			created := data(call("sub/create", a, map[string]interface{}{"name": "日常", "items": items}))
			subID := int64(created["id"].(float64))
			token := created["token"].(string)
			if len(token) < 22 || created["format"] != "auto" || created["ruleMode"] != "cn_direct" {
				t.Fatalf("created: %v", created)
			}
			list := data(call("sub/list", a, nil))
			subs := list["subs"].([]interface{})
			card := subs[0].(map[string]interface{})
			counts := card["counts"].(map[string]interface{})
			if len(subs) != 1 || card["items"].(float64) != 4 || counts["panel"].(float64) != 2 || counts["external"].(float64) != 1 || counts["missing"].(float64) != 0 || counts["total"].(float64) != 5 {
				t.Fatalf("card: %v", card)
			}
			if bl := data(call("sub/list", b, nil)); len(bl["subs"].([]interface{})) != 0 {
				t.Fatal("B sees A's subscription")
			}
			for _, path := range []string{"sub/pool", "sub/delete", "sub/reset"} {
				if out := call(path, b, map[string]interface{}{"id": subID}); out.Code == 0 {
					t.Fatalf("B could %s A's subscription", path)
				}
			}
			if out := call("sub/update", b, map[string]interface{}{"id": subID, "name": "x"}); out.Code == 0 {
				t.Fatal("B updated A's subscription")
			}

			// Clash (auto by UA) with headers.
			w := pull(token, "clash.meta/1.19", "")
			if w.Code != 200 {
				t.Fatalf("clash pull %d %s", w.Code, w.Body.String())
			}
			var doc map[string]interface{}
			if err := yaml.Unmarshal(w.Body.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			proxies := doc["proxies"].([]interface{})
			names := []string{}
			for _, p := range proxies {
				names = append(names, p.(map[string]interface{})["name"].(string))
			}
			want := []string{"游戏加速 · 上海联通入口", "我的落地·东京（直连）", "我家宽带 Socks", "🇭🇰 香港 01 ①", "🇭🇰 香港 01 ②"}
			if strings.Join(names, "|") != strings.Join(want, "|") {
				t.Fatalf("clash names %v", names)
			}
			info := w.Header().Get("subscription-userinfo")
			if info != "upload=0; download="+strconv.FormatInt(4<<30, 10)+"; total="+strconv.FormatInt(100<<30, 10)+"; expire="+strconv.FormatInt((now+86400000)/1000, 10) {
				t.Fatalf("userinfo %q", info)
			}
			if w.Header().Get("profile-update-interval") != "12" || !strings.Contains(w.Header().Get("content-disposition"), "filename*=UTF-8''%E6%97%A5%E5%B8%B8.yaml") ||
				w.Header().Get("profile-title") != "base64:"+base64.StdEncoding.EncodeToString([]byte("日常")) {
				t.Fatalf("headers %v", w.Header())
			}
			// sing-box and Base64.
			w = pull(token, "SFA/1.12.0 (sing-box 1.12.0)", "")
			var sb map[string]interface{}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &sb) != nil || !strings.Contains(w.Header().Get("Content-Type"), "json") {
				t.Fatalf("sing-box pull %d", w.Code)
			}
			w = pull(token, "Shadowrocket/2070 CFNetwork", "")
			raw, err := base64.StdEncoding.DecodeString(w.Body.String())
			if err != nil || !strings.Contains(string(raw), "ss://") || !strings.Contains(string(raw), "vless://"+subUUID) || !strings.Contains(string(raw), "hysteria2://") {
				t.Fatalf("base64 pull: %v %s", err, raw)
			}
			// ?t= overrides the UA.
			if w = pull(token, "clash.meta", "?t=base64"); !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
				t.Fatal("?t=base64 ignored")
			}
			// Browser: small page, no secrets, no userinfo header.
			w = pull(token, "Mozilla/5.0 (Windows NT 10.0) Chrome/120 Safari/537.36", "")
			page := w.Body.String()
			if w.Code != 200 || !strings.Contains(page, "日常") || !strings.Contains(page, "5 个节点") || !strings.Contains(page, "/s/"+token) || w.Header().Get("subscription-userinfo") != "" {
				t.Fatalf("browser page %d", w.Code)
			}
			for _, secret := range []string{subUUID, subPBK, "socks-secret-pw", "airport-pw", subKey(1)} {
				if strings.Contains(page, secret) {
					t.Fatalf("browser page leaks %q", secret)
				}
			}
			// Last pull recorded (once per minute) with the client family.
			var s model.UserSubscription
			if err := r.DB().First(&s, subID).Error; err != nil || s.LastPullAt == 0 || s.LastPullClient != "Clash Meta" || s.LastPullNodes != 5 {
				t.Fatalf("last pull: %+v %v", s, err)
			}

			// 404s.
			if w = pull("AAAAAAAAAAAAAAAAAAAAAAAAA", "clash", ""); w.Code != 404 {
				t.Fatalf("unknown token %d", w.Code)
			}
			if w = pull("bad", "clash", ""); w.Code != 404 {
				t.Fatalf("bad token %d", w.Code)
			}

			// Reset: the old link dies at once.
			reset := data(call("sub/reset", a, map[string]interface{}{"id": subID}))
			newToken := reset["token"].(string)
			if newToken == token || pull(token, "clash", "").Code != 404 || pull(newToken, "clash", "").Code != 200 {
				t.Fatal("reset")
			}
			token = newToken

			// Caps: 5 per user, admins unlimited.
			for i := 0; i < 4; i++ {
				data(call("sub/create", a, map[string]interface{}{"name": "s" + strconv.Itoa(i)}))
			}
			if out := call("sub/create", a, map[string]interface{}{"name": "six"}); out.Code == 0 {
				t.Fatal("6th subscription accepted")
			}
			for i := 0; i < 6; i++ {
				data(call("sub/create", admin, map[string]interface{}{"name": "admin" + strconv.Itoa(i)}))
			}

			// Switch off: pulls 403, API refuses; back on works.
			data(call("config/update-single", admin, map[string]interface{}{"name": "sub_link_enabled", "value": "false"}))
			if w = pull(token, "clash", ""); w.Code != 403 {
				t.Fatalf("switch off pull %d", w.Code)
			}
			if out := call("sub/create", a, map[string]interface{}{"name": "x"}); out.Code == 0 {
				t.Fatal("create while switched off")
			}
			if l := data(call("sub/list", a, nil)); l["enabled"] != false {
				t.Fatal("list does not report the switch")
			}
			data(call("config/update-single", admin, map[string]interface{}{"name": "sub_link_enabled", "value": "true"}))
			if w = pull(token, "clash", ""); w.Code != 200 {
				t.Fatalf("switch on pull %d", w.Code)
			}

			// Disabled owner → 403; deleted owner → 404 (rows cascade).
			r.DB().Model(&model.User{}).Where("id = ?", 801).Update("status", 0)
			if w = pull(token, "clash", ""); w.Code != 403 {
				t.Fatalf("disabled owner %d", w.Code)
			}
			if err := r.DeleteUserCascade(801); err != nil {
				t.Fatal(err)
			}
			var left int64
			r.DB().Model(&model.UserSubscription{}).Where("user_id = ?", 801).Count(&left)
			var leftSrc int64
			r.DB().Model(&model.UserSubSource{}).Where("user_id = ?", 801).Count(&leftSrc)
			if left != 0 || leftSrc != 0 || pull(token, "clash", "").Code != 404 {
				t.Fatalf("cascade: %d subs, %d sources left", left, leftSrc)
			}
		})
	}
}
