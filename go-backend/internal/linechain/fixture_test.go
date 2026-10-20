package linechain

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The agent (go-gost/x/landing) validates and renders exactly what the panel
// sends in a chain node's "chain" field. This fixture is the contract: the
// agent test TestPanelChainUpstreamsAccepted loads it and runs it through the
// agent validator, the renderer and the real Core. Regenerate with
// UPDATE_CHAIN_FIXTURE=1 after reviewing the change.
func TestChainUpstreamFixtureForAgent(t *testing.T) {
	key16 := base64.StdEncoding.EncodeToString(make([]byte, 16))
	links := []string{
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:pass")) + "@8.8.8.8:8388#aead",
		"ss://2022-blake3-aes-128-gcm:" + key16 + "@[2606:4700::1111]:443#ss2022",
		"vless://d77c8b43-40ed-42cd-9150-f3f47d0f534d@vl.example.com:443?security=reality&sni=www.example.com&pbk=Ak6Ci7Ogvl2t29y1MUSF4fuAtoXtt4EnCyU_cdhn4Ws&sid=0a1b&fp=chrome&flow=xtls-rprx-vision&type=tcp#reality",
		"vless://d77c8b43-40ed-42cd-9150-f3f47d0f534d@vl.example.com:443?security=tls&sni=vl.example.com&fp=chrome&flow=xtls-rprx-vision&alpn=h2,http/1.1#tls-vision",
		"vless://d77c8b43-40ed-42cd-9150-f3f47d0f534d@ws.example.com:443?type=ws&security=tls&path=%2Fray%3Fed%3D2048&host=cdn.example.com&sni=cdn.example.com#ws",
		"vless://d77c8b43-40ed-42cd-9150-f3f47d0f534d@g.example.com:443?type=grpc&security=tls&serviceName=svc&mode=multi#grpc",
		"vless://d77c8b43-40ed-42cd-9150-f3f47d0f534d@g.example.com:443?type=grpc&security=reality&sni=www.example.com&pbk=Ak6Ci7Ogvl2t29y1MUSF4fuAtoXtt4EnCyU_cdhn4Ws&sid=&serviceName=svc#grpc-reality",
		"vless://d77c8b43-40ed-42cd-9150-f3f47d0f534d@8.8.4.4:80?type=tcp#plain",
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"vm","add":"vm.example.com","port":"443","id":"d77c8b43-40ed-42cd-9150-f3f47d0f534d","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/vm","tls":"tls"}`)),
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"9.9.9.9","port":10086,"id":"d77c8b43-40ed-42cd-9150-f3f47d0f534d","aid":0,"net":"tcp","scy":"aes-128-gcm","tls":""}`)),
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"g.example.com","port":443,"id":"d77c8b43-40ed-42cd-9150-f3f47d0f534d","aid":0,"net":"grpc","path":"gsvc","tls":"tls","sni":"g.example.com"}`)),
		"trojan://p%40ss@tj.example.com:443?sni=tj.example.com&alpn=h2#tj",
		"trojan://pass@tj.example.com:443?type=ws&path=/tj&host=tj.example.com#tj-ws",
		"trojan://pass@tj.example.com:443?type=grpc&serviceName=tj&pinSHA256=" + "aa:" + "bb" + "cc" + repeat("ee", 29) + "#tj-grpc",
		"hysteria2://auth@hy.example.com:443/?sni=hy.example.com#hy2",
		"hy2://auth:part@1.1.1.1:8443?sni=hy.example.com&obfs=salamander&obfs-password=obfs&pinSHA256=" + repeat("ab", 32) + "#hy2-obfs",
		"socks5://user:pass@1.0.0.1:1080#socks",
		"socks5://1.0.0.1:1081#socks-noauth",
		"http://1.0.0.2:3128#http",
		"https://u:p@proxy.example.com:443#https",
	}
	out := []map[string]interface{}{}
	for _, link := range links {
		u, _, err := ParseLink(link)
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		var m map[string]interface{}
		_ = json.Unmarshal([]byte(u.JSON()), &m)
		out = append(out, map[string]interface{}{"link": link, "summary": u.Summary(), "upstream": m})
	}
	path := filepath.Join("..", "..", "..", "go-gost", "x", "landing", "testdata", "panel_chain_upstreams.json")
	b, _ := json.MarshalIndent(out, "", "  ")
	if os.Getenv("UPDATE_CHAIN_FIXTURE") == "1" {
		if err := os.WriteFile(path, append(b, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("contract fixture missing (run with UPDATE_CHAIN_FIXTURE=1): %v", err)
	}
	var a, w interface{}
	_ = json.Unmarshal(b, &a)
	_ = json.Unmarshal(want, &w)
	if !reflect.DeepEqual(a, w) {
		t.Fatal("panel chain upstream JSON changed: review the agent contract and regenerate with UPDATE_CHAIN_FIXTURE=1")
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
