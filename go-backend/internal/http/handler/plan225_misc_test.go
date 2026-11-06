package handler

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestRedactDiagnosisItem(t *testing.T) {
	hop := map[string]interface{}{"toChainType": 3, "targetIp": "155.254.127.160", "targetPort": 41234,
		"message": "连接 155.254.127.160:41234 失败: dial tcp [2001:db8::1]:41234: i/o timeout", "description": "入口(Dmit Echo)->出口(Bestvm US)"}
	redactDiagnosisItem(hop)
	if hop["targetIp"] != "" || hop["targetPort"] != 0 {
		t.Fatalf("hop address kept: %v", hop)
	}
	msg := hop["message"].(string)
	if strings.Contains(msg, "155.254") || strings.Contains(msg, "2001:db8") || strings.Contains(msg, "41234") {
		t.Fatalf("message keeps an address: %s", msg)
	}
	if hop["description"] != "入口(Dmit Echo)->出口(Bestvm US)" {
		t.Fatal("node names must stay")
	}
	target := map[string]interface{}{"fromChainType": 3, "targetIp": "example.com", "targetPort": 443, "message": "ok"}
	redactDiagnosisItem(target)
	if target["targetIp"] != "example.com" || target["targetPort"] != 443 {
		t.Fatal("the user's own target must stay visible")
	}
	payload := map[string]interface{}{"results": []map[string]interface{}{{"toChainType": 2, "targetIp": "1.2.3.4", "targetPort": 5}}}
	redactDiagnosisPayload(payload)
	if payload["results"].([]map[string]interface{})[0]["targetIp"] != "" {
		t.Fatal("payload not redacted")
	}
}

func TestPanelSelfIPs(t *testing.T) {
	if a, ok := parseTraceIP(strings.NewReader("fl=1\nh=1.1.1.1\nip=104.145.236.29\nts=1\n")); !ok || a.String() != "104.145.236.29" {
		t.Fatalf("trace parse %v %v", a, ok)
	}
	if _, ok := parseTraceIP(strings.NewReader("ip=10.0.0.1\n")); ok {
		t.Fatal("private trace ip accepted")
	}
	c := &panelSelfIPCache{fetch: func(context.Context) []netip.Addr {
		return []netip.Addr{netip.MustParseAddr("104.145.236.29")}
	}}
	if got := c.get(); len(got) != 0 {
		t.Fatalf("first get must not block: %v", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(c.get()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	saved := panelSelfIPs
	defer func() { panelSelfIPs = saved }()
	panelSelfIPs = c
	h := &Handler{}
	_ = h
	if got := c.get(); len(got) != 1 || got[0].String() != "104.145.236.29" {
		t.Fatalf("cached %v", got)
	}
}
