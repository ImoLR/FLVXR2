package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
)

func TestNormalizeNodeAdminNotice(t *testing.T) {
	for _, tc := range []struct {
		in   interface{}
		want string
		err  bool
	}{
		{nil, "", false},
		{"", "", false},
		{"  \n ", "", false},
		{" 已关闭 RA\r\n恢复：删除文件\r ", "已关闭 RA\n恢复：删除文件", false},
		{strings.Repeat("长", 512), strings.Repeat("长", 512), false},
		{strings.Repeat("长", 513), "", true},
		{"  " + strings.Repeat("a", 512) + "  ", strings.Repeat("a", 512), false},
		{float64(1), "", true},
		{[]interface{}{"x"}, "", true},
	} {
		got, err := normalizeNodeAdminNotice(tc.in)
		if (err != nil) != tc.err || got != tc.want {
			t.Fatalf("normalize(%#v) = %q, %v; want %q, err=%v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestNodeAdminNoticeSaveListAndStrip(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addNode(1, "notice")
	call := func(body string, role int, handle func(http.ResponseWriter, *http.Request)) (int, string, json.RawMessage) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
		r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: role}))
		w := httptest.NewRecorder()
		handle(w, r)
		var out struct {
			Code int
			Msg  string
			Data json.RawMessage
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v", w.Body.String(), err)
		}
		return out.Code, out.Msg, out.Data
	}
	stored := func() model.Node {
		t.Helper()
		var n model.Node
		if err := e.r.DB().First(&n, 1).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	update := func(extra string) (int, string, json.RawMessage) {
		return call(`{"id":1,"name":"n","serverIp":"127.0.0.1","port":"1000-2000"`+extra+`}`, 0, e.h.nodeUpdate)
	}

	// Set (trim + CRLF), and a notice-only change redeploys nothing.
	code, msg, data := update(`,"adminNotice":"  已关闭 RA 自动路由\r\n恢复：networkctl reload  "`)
	if code != 0 {
		t.Fatalf("update: %d %s", code, msg)
	}
	var sync nodeSyncResult
	if err := json.Unmarshal(data, &sync); err != nil || sync.TunnelsUpdated != 0 || sync.RulesUpdated != 0 || sync.EntryAddressesUpdated != 0 || len(sync.Failures) != 0 {
		t.Fatalf("notice-only save must not redeploy: %s %v", data, err)
	}
	const want = "已关闭 RA 自动路由\n恢复：networkctl reload"
	if got := stored().AdminNotice; got != want {
		t.Fatalf("stored %q", got)
	}

	// Admin list carries it; any other role never sees the key.
	for _, role := range []int{0, 1, 2} {
		code, _, raw := call(`{}`, role, e.h.nodeList)
		if code != 0 {
			t.Fatalf("list role %d: %d", role, code)
		}
		var items []map[string]interface{}
		if err := json.Unmarshal(raw, &items); err != nil || len(items) != 1 {
			t.Fatalf("list role %d: %s %v", role, raw, err)
		}
		got, has := items[0]["adminNotice"]
		if role == 0 && got != want {
			t.Fatalf("admin list adminNotice = %#v", got)
		}
		if role != 0 && (has || bytes.Contains(raw, []byte("networkctl"))) {
			t.Fatalf("role %d sees the notice: %s", role, raw)
		}
	}

	// An absent key keeps it (older clients, API scripts).
	if code, msg, _ := update(`,"remark":"r"`); code != 0 || stored().AdminNotice != want {
		t.Fatalf("absent key: %d %s %q", code, msg, stored().AdminNotice)
	}

	// Too long or not a string: rejected before anything is written.
	if code, msg, _ := call(`{"id":1,"name":"renamed","serverIp":"127.0.0.1","port":"1000-2000","adminNotice":"`+strings.Repeat("长", 513)+`"}`, 0, e.h.nodeUpdate); code == 0 || !strings.Contains(msg, "512") {
		t.Fatalf("513 chars accepted: %d %s", code, msg)
	}
	if code, _, _ := update(`,"adminNotice":42`); code == 0 {
		t.Fatal("numeric notice accepted")
	}
	if n := stored(); n.AdminNotice != want || n.Name != "n" {
		t.Fatalf("rejected save wrote: name=%q notice=%q", n.Name, n.AdminNotice)
	}

	// Exactly 512 characters fits; empty clears.
	if code, msg, _ := update(`,"adminNotice":"` + strings.Repeat("长", 512) + `"`); code != 0 || stored().AdminNotice != strings.Repeat("长", 512) {
		t.Fatalf("512 chars: %d %s", code, msg)
	}
	if code, msg, _ := update(`,"adminNotice":"   "`); code != 0 || stored().AdminNotice != "" {
		t.Fatalf("clear: %d %s %q", code, msg, stored().AdminNotice)
	}
}

func TestNodeAdminNoticeOnCreate(t *testing.T) {
	e := newFlowTestEnv(t)
	for _, tc := range []struct {
		body string
		want string
		ok   bool
	}{
		{`{"name":"with","serverIp":"10.0.0.1","adminNotice":" 提示 "}`, "提示", true},
		{`{"name":"without","serverIp":"10.0.0.2"}`, "", true},
		{`{"name":"long","serverIp":"10.0.0.3","adminNotice":"` + strings.Repeat("x", 513) + `"}`, "", false},
	} {
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tc.body))
		r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
		w := httptest.NewRecorder()
		e.h.nodeCreate(w, r)
		var out struct{ Code int }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if (out.Code == 0) != tc.ok {
			t.Fatalf("%s → %s", tc.body, w.Body.String())
		}
		var nodes []model.Node
		e.r.DB().Where("server_ip = ?", strings.Split(strings.Split(tc.body, `"serverIp":"`)[1], `"`)[0]).Find(&nodes)
		if !tc.ok {
			if len(nodes) != 0 {
				t.Fatalf("rejected create stored a node: %s", tc.body)
			}
			continue
		}
		if len(nodes) != 1 || nodes[0].AdminNotice != tc.want {
			t.Fatalf("%s → %+v", tc.body, nodes)
		}
	}
}
