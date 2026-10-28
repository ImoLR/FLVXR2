package contract_test

import (
	"strings"
	"testing"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
)

// The notice the batch-1028 release sets on node 39 (plan 154).
const node39Notice = "已关闭本机 IPv6 RA 自动路由（2026-10-28）：上游自动学到的路由器 fe80::9a03:9bff:fe03:fcc7 不转发，导致约一半 IPv6 目标连不上，" +
	"为修复此问题已关闭，只保留静态网关 2407:cdc0:d003::1。恢复：删除 /etc/systemd/network/10-netplan-eth0.network.d/90-flvx-no-ra.conf " +
	"后执行 networkctl reload。"

// 节点提示 (plan 154): admin-only text on the node, round-trips through
// node/create, node/update and node/list on SQLite and PostgreSQL; other roles
// cannot reach it; an existing database gains the column (empty) on startup.
func TestNodeAdminNoticeContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			const secret = "node-admin-notice"
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

			listNotice := func(name string) (string, bool) {
				t.Helper()
				out := requestContractEnvelope(t, router, admin, "/api/v1/node/list", map[string]interface{}{})
				if out.Code != 0 {
					t.Fatalf("list: %+v", out)
				}
				for _, row := range mustContractSlice(t, out.Data, "node list") {
					item := row.(map[string]interface{})
					if item["name"] == name {
						v, ok := item["adminNotice"].(string)
						return v, ok
					}
				}
				t.Fatalf("node %q not listed", name)
				return "", false
			}
			stored := func(name string) model.Node {
				t.Helper()
				var n model.Node
				if err := r.DB().Where("name = ?", name).Take(&n).Error; err != nil {
					t.Fatal(err)
				}
				return n
			}

			// Create with a notice (trim + CRLF), and without one.
			out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", map[string]interface{}{
				"name": "sg", "serverIpV4": "192.0.2.39", "adminNotice": "  第一行\r\n第二行  ",
			})
			if out.Code != 0 {
				t.Fatalf("create: %+v", out)
			}
			if got, ok := listNotice("sg"); !ok || got != "第一行\n第二行" {
				t.Fatalf("created notice %q %v", got, ok)
			}
			if out := requestContractEnvelope(t, router, admin, "/api/v1/node/create", map[string]interface{}{"name": "plain", "serverIpV4": "192.0.2.40"}); out.Code != 0 {
				t.Fatalf("create plain: %+v", out)
			}
			if got, ok := listNotice("plain"); !ok || got != "" {
				t.Fatalf("plain node notice %q %v (want present and empty)", got, ok)
			}

			sg := stored("sg")
			base := map[string]interface{}{"id": sg.ID, "name": "sg", "serverIp": sg.ServerIP, "serverIpV4": "192.0.2.39", "port": sg.Port}
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

			// The real node-39 text round-trips byte for byte.
			if code, msg := update(map[string]interface{}{"adminNotice": node39Notice}); code != 0 {
				t.Fatalf("update node39 text: %d %s", code, msg)
			}
			if got, _ := listNotice("sg"); got != node39Notice {
				t.Fatalf("round trip:\n got %q\nwant %q", got, node39Notice)
			}
			// Absent key keeps it.
			if code, msg := update(map[string]interface{}{"remark": "r"}); code != 0 || stored("sg").AdminNotice != node39Notice {
				t.Fatalf("absent key: %d %s %q", code, msg, stored("sg").AdminNotice)
			}
			// 512 characters (multi-byte) fit the PostgreSQL varchar(512); 513 are refused, nothing written.
			full := strings.Repeat("提", 512)
			if code, msg := update(map[string]interface{}{"adminNotice": full}); code != 0 {
				t.Fatalf("512 chars: %d %s", code, msg)
			}
			if got, _ := listNotice("sg"); got != full {
				t.Fatalf("512 chars not stored intact (%d runes)", len([]rune(got)))
			}
			if code, msg := update(map[string]interface{}{"name": "renamed", "adminNotice": full + "a"}); code == 0 || !strings.Contains(msg, "512") {
				t.Fatalf("513 chars accepted: %d %s", code, msg)
			}
			if n := stored("sg"); n.AdminNotice != full {
				t.Fatal("refused save changed the notice")
			}
			// Empty clears.
			if code, msg := update(map[string]interface{}{"adminNotice": ""}); code != 0 || stored("sg").AdminNotice != "" {
				t.Fatalf("clear: %d %s", code, msg)
			}
			if code, _ := update(map[string]interface{}{"adminNotice": node39Notice}); code != 0 {
				t.Fatal("re-set")
			}

			// Non-admins: every node API is refused, so the text never reaches them.
			for _, path := range []string{"/api/v1/node/list", "/api/v1/node/update", "/api/v1/node/create", "/api/v1/node/check-status"} {
				out := requestContractEnvelope(t, router, user, path, map[string]interface{}{"id": sg.ID, "name": "x", "serverIp": "192.0.2.9", "adminNotice": "hijack"})
				if out.Code != 403 {
					t.Fatalf("non-admin %s = %+v", path, out)
				}
			}
			if stored("sg").AdminNotice != node39Notice {
				t.Fatal("non-admin request changed the notice")
			}

			// An existing database without the column (fork.93) gains it, rows get ''.
			r.Close()
			r = open()
			if err := r.DB().Exec(`ALTER TABLE node DROP COLUMN admin_notice`).Error; err != nil {
				t.Fatalf("simulate fork.93 schema: %v", err)
			}
			r.Close()
			r = open()
			if n := stored("sg"); n.AdminNotice != "" || n.Name != "sg" {
				t.Fatalf("migrated row: name=%q notice=%q", n.Name, n.AdminNotice)
			}
			if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, port, created_time, status) VALUES('raw', 's-raw', '192.0.2.41', '1000-2000', 1, 1)`).Error; err != nil {
				t.Fatal(err)
			}
			if n := stored("raw"); n.AdminNotice != "" {
				t.Fatalf("raw insert notice %q", n.AdminNotice)
			}
			if err := r.SetNodeAdminNotice(stored("raw").ID, "ok"); err != nil || stored("raw").AdminNotice != "ok" {
				t.Fatalf("set after migration: %v", err)
			}
		})
	}
}
