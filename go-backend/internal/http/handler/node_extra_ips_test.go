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
)

func adminPost(t *testing.T, fn http.HandlerFunc, body interface{}) (int, string, json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
	w := httptest.NewRecorder()
	fn(w, r)
	var payload struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return payload.Code, payload.Msg, payload.Data
}

func storedExtraIPs(t *testing.T, e *flowTestEnv, name string) (string, bool) {
	t.Helper()
	var row struct {
		ExtraIPs *string
	}
	if err := e.r.DB().Raw(`SELECT extra_ips FROM node WHERE name = ?`, name).Scan(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ExtraIPs == nil {
		return "", false
	}
	return *row.ExtraIPs, true
}

func TestNodeCreateValidatesAndNormalizesExtraIPs(t *testing.T) {
	e := newFlowTestEnv(t)
	body := func(name, extra string) map[string]interface{} {
		return map[string]interface{}{"name": name, "serverIp": "2001:db8::1", "serverIpV4": "", "serverIpV6": "2001:db8::1",
			"port": "1000-2000", "extraIPs": extra}
	}
	if code, msg, _ := adminPost(t, e.h.nodeCreate, body("two-v6", " 2001:DB8::2\n2001:db8::2,203.0.113.9 ")); code != 0 {
		t.Fatalf("create failed: %d %s", code, msg)
	}
	if got, ok := storedExtraIPs(t, e, "two-v6"); !ok || got != `["2001:db8::2","203.0.113.9"]` {
		t.Fatalf("stored extra_ips = %q (%v)", got, ok)
	}
	if code, msg, _ := adminPost(t, e.h.nodeCreate, body("plain", "")); code != 0 {
		t.Fatalf("create without extras failed: %d %s", code, msg)
	}
	if got, ok := storedExtraIPs(t, e, "plain"); ok {
		t.Fatalf("empty extras must stay NULL, got %q", got)
	}
	for extra, want := range map[string]string{
		"2001:db8:0::1":   "与节点主 IP 重复",
		"hk.example.test": "不是有效的",
		"::1":             "回环",
	} {
		code, msg, _ := adminPost(t, e.h.nodeCreate, body("bad", extra))
		if code == 0 || !strings.Contains(msg, want) {
			t.Fatalf("extra %q: code=%d msg=%q, want %q", extra, code, msg, want)
		}
	}
	var count int64
	e.r.DB().Raw(`SELECT COUNT(*) FROM node WHERE name = 'bad'`).Scan(&count)
	if count != 0 {
		t.Fatalf("rejected nodes were saved: %d", count)
	}
}

func TestNodeUpdateValidatesExtraIPsBeforeSaving(t *testing.T) {
	e := newNodeSyncEnv(t)
	body := nodeSyncBody("entry", "192.0.2.1")
	body["extraIPs"] = "192.0.2.1"
	code, msg, _ := adminPost(t, e.h.nodeUpdate, body)
	if code == 0 || !strings.Contains(msg, "与节点主 IP 重复") {
		t.Fatalf("primary duplicate accepted: %d %q", code, msg)
	}
	body["extraIPs"] = "2001:db8::2, 2001:db8::3"
	body["name"] = "renamed"
	var result nodeSyncResult
	nodeSyncRequest(t, e.h.nodeUpdate, body, &result)
	if got, _ := storedExtraIPs(t, e, "renamed"); got != `["2001:db8::2","2001:db8::3"]` {
		t.Fatalf("stored extra_ips = %q", got)
	}
	// Adding extra IPs leaves every saved entry address alone.
	if result.EntryAddressesUpdated != 0 || result.TunnelsUpdated != 0 || result.RulesUpdated != 0 || len(result.Failures) != 0 {
		t.Fatalf("adding extras propagated: %+v", result)
	}
}

func TestExtraIPsAreNeverDialedImplicitly(t *testing.T) {
	from := &nodeRecord{Name: "entry", ServerIPv4: "192.0.2.1"}
	to := &nodeRecord{Name: "exit", ServerIP: "192.0.2.2", ServerIPv4: "192.0.2.2", ExtraIPs: `["198.51.100.7"]`}
	for _, tc := range []struct{ preference, connectType string }{{"", ""}, {"", "lan"}, {"auto", ""}, {"v4", ""}} {
		host, _, err := selectTunnelDialHost(from, to, tc.preference, tc.connectType)
		if err != nil || host != "192.0.2.2" {
			t.Fatalf("preference %q/%q dialed %q (%v), want primary", tc.preference, tc.connectType, host, err)
		}
	}
	host, family, err := selectTunnelDialHost(from, to, "", "", "198.51.100.7")
	if err != nil || host != "198.51.100.7" || family != "v4" {
		t.Fatalf("custom connect IP on an extra address = %q %q %v", host, family, err)
	}
	to.IntranetIP = "10.0.0.2"
	if host, family, _ := selectTunnelDialHost(from, to, "", "lan"); host != "10.0.0.2" || family != "lan" {
		t.Fatalf("intranet dial changed: %q %q", host, family)
	}
}
