package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"go-backend/internal/landingegress"
)

// Plan 149: 禁止大陆直连 (seeded, enabled) reaches only SS nodes; a node's
// 路由规则 view lists only the global rules that apply to it. Plan 219: FLVX
// nodes are no longer exempt — a mainland node connecting directly to a
// landing is blocked like any other mainland source (relayed traffic arrives
// from an overseas exit), and the 来源白名单 is gone.
func TestLandingCNDirectScopedToSS(t *testing.T) {
	e, _ := layersEnv(t)
	// The shipped seeds (layersEnv starts from the pre-149 disabled state).
	raw, _ := json.Marshal(landingegress.ForcedSeedRules())
	e.exec("UPDATE landing_egress_setting SET global_forced = ? WHERE id = 1", string(raw))
	e.exec("UPDATE landing_node SET protocol='vless_reality_vision' WHERE id=1")
	// A mainland entry node behind NAT: its configured IPv4 is private, the
	// agent reports the real egress.
	e.addNode(3, "entry")
	e.exec("UPDATE node SET server_ip='10.7.177.91', server_ip_v4='10.7.177.91', server_ip_v6='2408:8748:a100:59::1' WHERE id=3")
	if _, err := e.r.FillNodePublicIPs(3, "112.64.1.9", "2408:8748:a100:59:aaaa::7"); err != nil {
		t.Fatal(err)
	}
	_, plan := compiledHash(t, e.h, 1)
	direct := findRule(plan, "禁止大陆直连")
	if direct == nil || strings.Join(direct.InboundTags, ",") != "flvxl-2" || strings.Join(direct.SourceIP, ",") != "geoip:cn" || direct.Outbound != landingegress.ActionBlock {
		t.Fatalf("禁止大陆直连: %+v", direct)
	}
	for _, name := range []string{"屏蔽大陆 IP", "屏蔽大陆网站"} {
		if r := findRule(plan, name); r == nil || strings.Join(r.InboundTags, ",") != "flvxl-1,flvxl-2" || r.Builtin != landingegress.BuiltinForced {
			t.Fatalf("%s: %+v", name, r)
		}
	}
	if raw, _ := json.Marshal(plan); strings.Contains(string(raw), "exempt") || strings.Contains(string(raw), "112.64.1.9") || strings.Contains(string(raw), "2408:8748") {
		t.Fatalf("plan exempts FLVX node addresses: %s", raw)
	}
	// 路由规则 views: the VLESS node (owner 2) does not list 禁止大陆直连, the
	// SS node (owner 3) does, with its 节点协议 scope.
	forcedNames := func(uid, node int64) (string, map[string]any) {
		view := dataMap(t, layerCall(t, e.h, "/api/v1/landing/node/rules/get", map[string]any{"id": node}, uid, 1))
		names := []string{}
		var scoped map[string]any
		for _, r := range view["global"].(map[string]any)["forced"].([]any) {
			m := r.(map[string]any)
			names = append(names, m["name"].(string))
			if m["inboundProtocols"] != nil {
				scoped = m
			}
		}
		return strings.Join(names, ","), scoped
	}
	if got, _ := forcedNames(2, 1); got != "屏蔽大陆 IP,屏蔽大陆网站" {
		t.Fatalf("vless node forced: %s", got)
	}
	got, scoped := forcedNames(3, 2)
	if got != "屏蔽大陆 IP,屏蔽大陆网站,禁止大陆直连" || scoped == nil || scoped["name"] != "禁止大陆直连" {
		t.Fatalf("ss node forced: %s %+v", got, scoped)
	}
	e.h.WaitEgressSync()
}
