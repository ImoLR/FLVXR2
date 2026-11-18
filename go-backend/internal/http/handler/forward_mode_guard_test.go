package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"go-backend/internal/store/model"
)

// Plan 282: nftables (kernel DNAT, no 协议过滤) only for an admin's own rules.
func TestForwardNftablesAdminOnly(t *testing.T) {
	bindDialects(t, func(t *testing.T, dialect string) {
		e := newBindEnv(t, dialect, false)
		mode := func(fid int64) string {
			t.Helper()
			f, err := e.r.GetForwardRecord(fid)
			if err != nil || f == nil {
				t.Fatalf("forward %d: %v", fid, err)
			}
			return f.Mode
		}
		refused := func(what string, code int, msg string) {
			t.Helper()
			if code == 0 || msg != nftAdminOnlyMsg {
				t.Fatalf("%s: want refusal, got %d %q", what, code, msg)
			}
		}
		count := func() int64 {
			var n int64
			e.r.DB().Table("forward").Count(&n)
			return n
		}

		// Normal user: create with nftables → refused, nothing stored.
		before := count()
		code, msg, _ := e.call(e.h.forwardCreate, "7", 1, `{"name":"x","tunnelId":25,"remoteAddr":"1.1.1.1:443","inPort":16001,"mode":"nftables"}`)
		refused("user create nft", code, msg)
		if count() != before {
			t.Fatalf("refused create stored a row")
		}

		// Normal user: gost create ok, then update to nftables → refused.
		own := e.createRule("7", 1, `,"inPort":16002,"mode":"gost"`)
		code, msg, _ = e.call(e.h.forwardUpdate, "7", 1, fmt.Sprintf(`{"id":%d,"mode":"nftables"}`, own))
		refused("user update gost→nft", code, msg)
		if got := mode(own); got != "gost" {
			t.Fatalf("user rule mode %q after refused update", got)
		}
		// Plain user edit (UI always sends gost) still works.
		if code, msg, _ := e.call(e.h.forwardUpdate, "7", 1, fmt.Sprintf(`{"id":%d,"name":"renamed","mode":"gost"}`, own)); code != 0 {
			t.Fatalf("user gost edit: %d %s", code, msg)
		}

		// Admin: own nftables rule ok.
		adminRule := e.createRule("1", 0, `,"inPort":16003,"mode":"nftables"`)
		if got := mode(adminRule); got != "nftables" {
			t.Fatalf("admin own rule mode %q, want nftables", got)
		}
		// Admin creating an nftables rule for a normal user → refused.
		code, msg, _ = e.call(e.h.forwardCreate, "1", 0, `{"name":"x","userId":7,"tunnelId":25,"remoteAddr":"1.1.1.1:443","inPort":16004,"mode":"nftables"}`)
		refused("admin create nft for user", code, msg)
		// Admin switching a user's rule to nftables → refused.
		code, msg, _ = e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"mode":"nftables"}`, own))
		refused("admin update user rule → nft", code, msg)

		// Legacy user-owned nftables rule (made before plan 282): an edit
		// that keeps the mode (no "mode" field) is refused; saving as gost
		// (admin conversion or the user's own UI save) converts it.
		legacy := e.createRule("7", 1, `,"inPort":16005,"mode":"gost"`)
		if err := e.r.DB().Table("forward").Where("id = ?", legacy).Update("mode", "nftables").Error; err != nil {
			t.Fatal(err)
		}
		code, msg, _ = e.call(e.h.forwardUpdate, "7", 1, fmt.Sprintf(`{"id":%d,"name":"keep"}`, legacy))
		refused("user edit keeping legacy nft", code, msg)
		code, msg, _ = e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"name":"keep"}`, legacy))
		refused("admin edit keeping legacy user nft", code, msg)
		if code, msg, _ := e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"mode":"gost"}`, legacy)); code != 0 {
			t.Fatalf("admin convert legacy → gost: %d %s", code, msg)
		}
		if got := mode(legacy); got != "gost" {
			t.Fatalf("legacy rule mode %q after conversion", got)
		}

		// Admin keeps their own nftables rule editable.
		if code, msg, _ := e.call(e.h.forwardUpdate, "1", 0, fmt.Sprintf(`{"id":%d,"name":"admin-nft"}`, adminRule)); code != 0 {
			t.Fatalf("admin own nft edit: %d %s", code, msg)
		}

		// WG 隧道 stays admin-only.
		code, msg, _ = e.call(e.h.forwardCreate, "7", 1, `{"name":"x","tunnelId":25,"remoteAddr":"1.1.1.1:443","inPort":16006,"mode":"wg_path","wgPathId":1}`)
		if code == 0 || !strings.Contains(msg, "WG 隧道规则仅管理员") {
			t.Fatalf("user wg create: %d %q", code, msg)
		}
		code, msg, _ = e.call(e.h.forwardUpdate, "7", 1, fmt.Sprintf(`{"id":%d,"mode":"wg_path","wgPathId":1}`, own))
		if code == 0 || !strings.Contains(msg, "WG 隧道规则仅管理员") {
			t.Fatalf("user wg update: %d %q", code, msg)
		}
	})
}

func TestEnsureForwardModeAllowedUnit(t *testing.T) {
	var h *Handler
	if err := h.ensureForwardModeAllowed("gost", 1, 7); err != nil {
		t.Fatalf("gost refused: %v", err)
	}
	if err := h.ensureForwardModeAllowed(" NFTables ", 0, 1); err == nil {
		t.Fatalf("nft without repo must be refused")
	}
	if err := h.ensureForwardModeAllowed("nftables", 1, 1); err == nil {
		t.Fatalf("non-admin actor allowed nft")
	}
}

func TestNormalizeForwardImportModes(t *testing.T) {
	bindDialects(t, func(t *testing.T, dialect string) {
		e := newBindEnv(t, dialect, false)
		raw := map[string]interface{}{
			"forward": []interface{}{
				map[string]interface{}{"name": "user-nft", "user_id": float64(7), "mode": "nftables"},
				map[string]interface{}{"name": "admin-nft", "user_id": float64(1), "mode": "nftables"},
				map[string]interface{}{"name": "user-gost", "user_id": float64(8), "mode": "gost"},
				map[string]interface{}{"name": "new-user-nft", "user_id": float64(99), "mode": "nftables"},
				map[string]interface{}{"name": "new-admin-nft", "user_id": float64(98), "mode": "nftables"},
			},
			"user": []interface{}{
				map[string]interface{}{"id": float64(98), "role_id": float64(0)},
			},
		}
		got := e.h.normalizeForwardImportModes(raw, []string{"forward", "user"})
		if strings.Join(got, ",") != "user-nft,new-user-nft" {
			t.Fatalf("converted %v", got)
		}
		modes := []string{}
		for _, item := range raw["forward"].([]interface{}) {
			modes = append(modes, item.(map[string]interface{})["mode"].(string))
		}
		if strings.Join(modes, ",") != "gost,nftables,gost,gost,nftables" {
			t.Fatalf("modes after normalize %v", modes)
		}
	})
}

// Batch change-tunnel keeps the mode, so a legacy user nftables rule must not
// move (it would land on another tunnel's entries with no 协议过滤).
func TestForwardBatchChangeTunnelNftUser(t *testing.T) {
	bindDialects(t, func(t *testing.T, dialect string) {
		e := newBindEnv(t, dialect, false)
		far := e.now + 86400000*30
		e.create(&model.Tunnel{ID: 26, Name: "second", TrafficRatio: 1, Type: 1, Protocol: "tls", Flow: 1, CreatedTime: e.now, UpdatedTime: e.now, Status: 1,
			InIP: sql.NullString{String: "192.0.2.23", Valid: true}})
		e.create(&model.ChainTunnel{TunnelID: 26, ChainType: "1", NodeID: 23, Inx: nullInt(0)})
		e.create(&model.UserTunnel{ID: 71, UserID: 7, TunnelID: 26, Num: 10, Flow: 99999, FlowResetTime: 1, ExpTime: far, Status: 1})
		legacy := e.createRule("7", 1, `,"inPort":16101,"mode":"gost"`)
		if err := e.r.DB().Table("forward").Where("id = ?", legacy).Update("mode", "nftables").Error; err != nil {
			t.Fatal(err)
		}
		for _, actor := range []struct {
			sub  string
			role int
		}{{"7", 1}, {"1", 0}} {
			code, msg, data := e.call(e.h.forwardBatchChangeTunnel, actor.sub, actor.role, fmt.Sprintf(`{"forwardIds":[%d],"targetTunnelId":26}`, legacy))
			var res batchOperationResult
			_ = json.Unmarshal(data, &res)
			if code != 0 || res.SuccessCount != 0 || res.FailCount != 1 || len(res.Failures) != 1 || res.Failures[0].Reason != nftAdminOnlyMsg {
				t.Fatalf("actor %s: %d %s %+v", actor.sub, code, msg, res)
			}
		}
		f, _ := e.r.GetForwardRecord(legacy)
		if f == nil || f.TunnelID != 25 {
			t.Fatalf("legacy rule moved: %+v", f)
		}
		// A gost rule still moves.
		plain := e.createRule("7", 1, `,"inPort":16102,"mode":"gost"`)
		code, msg, data := e.call(e.h.forwardBatchChangeTunnel, "7", 1, fmt.Sprintf(`{"forwardIds":[%d],"targetTunnelId":26}`, plain))
		var res batchOperationResult
		_ = json.Unmarshal(data, &res)
		if code != 0 || res.SuccessCount != 1 {
			t.Fatalf("gost move: %d %s %+v", code, msg, res)
		}
	})
}
