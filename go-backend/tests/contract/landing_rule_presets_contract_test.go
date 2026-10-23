package contract_test

import (
	"fmt"
	"testing"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
)

// Plan 140 — preset library and built-in switches through the FULL router
// with real JWTs (the global auth middleware included), SQLite + PostgreSQL.

func presetsOf(t *testing.T, out response.R) []map[string]interface{} {
	t.Helper()
	if out.Code != 0 {
		t.Fatalf("preset list: %+v", out)
	}
	raw := out.Data.(map[string]interface{})["presets"].([]interface{})
	list := make([]map[string]interface{}, 0, len(raw))
	for _, p := range raw {
		list = append(list, p.(map[string]interface{}))
	}
	return list
}

func presetNames(list []map[string]interface{}) map[string]map[string]interface{} {
	out := map[string]map[string]interface{}{}
	for _, p := range list {
		out[p["name"].(string)] = p
	}
	return out
}

func TestLandingPresetLibraryContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			u := &model.User{ID: 601, User: "presets-user", Flow: 100, Status: 1, RoleID: 1}
			l := newLandingLimitsLab(t, dialect, u)
			user := l.token(u)

			// Everyone can list; the user only gets presets they can use.
			admin := presetsOf(t, l.call(l.admin, "preset/list", nil))
			if len(admin) != 10 || admin[0]["seedKey"] != "block-cn" {
				t.Fatalf("admin library: %d %+v", len(admin), admin[0])
			}
			mine := presetNames(presetsOf(t, l.call(user, "preset/list", nil)))
			if _, ok := mine["屏蔽中国大陆"]; ok || len(mine) != 9 {
				t.Fatalf("user library (来源 IP preset must be hidden): %d", len(mine))
			}
			if _, ok := mine["禁止访问内网"]; !ok {
				t.Fatal("禁止访问内网 missing for users")
			}
			if out := l.call("", "preset/list", nil); out.Code != 401 {
				t.Fatalf("anonymous list: %+v", out)
			}

			// Every write is admin-only (403 with a real non-admin JWT).
			for path, body := range map[string]interface{}{
				"egress/preset/save":    map[string]interface{}{"name": "x", "rules": nodeRules(1, "x")},
				"egress/preset/delete":  map[string]interface{}{"id": admin[0]["id"]},
				"egress/preset/reorder": map[string]interface{}{"ids": []interface{}{admin[0]["id"]}},
				"egress/preset/restore": map[string]interface{}{},
				"egress/builtin/save":   map[string]interface{}{"smtp": false, "bt": false},
			} {
				if out := l.call(user, path, body); out.Code != 403 {
					t.Fatalf("user %s: %+v", path, out)
				}
			}

			// A preset with a fixed outbound is offered to users only once the
			// template is 允许用户使用.
			out := l.call(l.admin, "egress/outbound/save", map[string]interface{}{"name": "socks", "type": "proxy", "ipVersion": "system", "settings": map[string]interface{}{"protocol": "socks", "server": "198.51.100.7", "port": 1080}})
			wantOK(t, "outbound", out)
			outID := out.Data.(map[string]interface{})["id"]
			ref := "outbound:" + formatID(outID)
			out = l.call(l.admin, "egress/preset/save", map[string]interface{}{"name": "走代理", "description": "固定出站", "rules": []map[string]interface{}{{"name": "代理", "enabled": true, "domains": []string{"geosite:openai"}, "action": ref}}})
			wantOK(t, "fixed preset", out)
			fixedID := out.Data.(map[string]interface{})["id"]
			if _, ok := presetNames(presetsOf(t, l.call(user, "preset/list", nil)))["走代理"]; ok {
				t.Fatal("preset with an unpublished outbound offered to a user")
			}
			wantOK(t, "publish", l.call(l.admin, "egress/outbound/save", map[string]interface{}{"id": outID, "name": "socks", "type": "proxy", "ipVersion": "system", "userAllowed": true, "settings": map[string]interface{}{"protocol": "socks", "server": "198.51.100.7", "port": 1080}}))
			if _, ok := presetNames(presetsOf(t, l.call(user, "preset/list", nil)))["走代理"]; !ok {
				t.Fatal("preset with a published outbound hidden from the user")
			}
			// Source matchers stay admin-only.
			wantOK(t, "source preset", l.call(l.admin, "egress/preset/save", map[string]interface{}{"name": "来源", "rules": []map[string]interface{}{{"name": "s", "enabled": true, "sourceIps": []string{"203.0.113.0/24"}, "action": "block"}}}))
			if _, ok := presetNames(presetsOf(t, l.call(user, "preset/list", nil)))["来源"]; ok {
				t.Fatal("来源 IP preset offered to a user")
			}
			wantRefused(t, "ask without flag", l.call(l.admin, "egress/preset/save", map[string]interface{}{"name": "坏", "rules": []map[string]interface{}{{"name": "a", "enabled": true, "ports": "1", "action": "ask"}}}), "插入时")

			// Reorder (fixed preset first), delete a seed, restore it once.
			all := presetsOf(t, l.call(l.admin, "preset/list", nil))
			ids := []interface{}{fixedID}
			for _, p := range all {
				if p["id"] != fixedID {
					ids = append(ids, p["id"])
				}
			}
			out = l.call(l.admin, "egress/preset/reorder", map[string]interface{}{"ids": ids})
			if list := presetsOf(t, out); list[0]["id"] != fixedID {
				t.Fatalf("reorder: %+v", list[0])
			}
			wantRefused(t, "stale reorder", l.call(l.admin, "egress/preset/reorder", map[string]interface{}{"ids": ids[1:]}), "刷新")
			var bt interface{}
			for _, p := range all {
				if p["seedKey"] == "block-bt" {
					bt = p["id"]
				}
			}
			wantOK(t, "delete", l.call(l.admin, "egress/preset/delete", map[string]interface{}{"id": bt}))
			out = l.call(l.admin, "egress/preset/restore", map[string]interface{}{})
			if presetsOf(t, out); out.Data.(map[string]interface{})["added"].(float64) != 1 {
				t.Fatalf("restore: %+v", out.Data)
			}
			out = l.call(l.admin, "egress/preset/restore", map[string]interface{}{})
			if out.Data.(map[string]interface{})["added"].(float64) != 0 {
				t.Fatalf("restore twice: %+v", out.Data)
			}

			// Built-in switches through the router; the global view reflects them.
			wantOK(t, "builtin", l.call(l.admin, "egress/builtin/save", map[string]interface{}{"smtp": true, "bt": false}))
			g := l.call(l.admin, "egress/global/get", nil)
			wantOK(t, "global get", g)
			b := g.Data.(map[string]interface{})["builtin"].(map[string]interface{})
			if b["smtp"] != true || b["bt"] != false {
				t.Fatalf("builtin view %+v", b)
			}
			if forced := g.Data.(map[string]interface{})["forced"].([]interface{}); len(forced) != 4 || forced[0].(map[string]interface{})["enabled"] != false {
				t.Fatalf("seeded forced rules: %+v", forced)
			}
		})
	}
}

func formatID(v interface{}) string { return fmt.Sprintf("%d", int64(v.(float64))) }
