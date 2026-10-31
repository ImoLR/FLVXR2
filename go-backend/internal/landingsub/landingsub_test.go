package landingsub

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	realityLink = "vless://4b2f1c84-1f7e-4f3b-9d4c-2f0f6b6a1c11@198.51.124.2:443?type=tcp&security=reality&sni=www.example.com&pbk=8eDxL1uR4ob8BdjX1sY7k2Xz4XlG0bVvUj3sZ0kq8Uo&sid=ab12&fp=chrome&flow=xtls-rprx-vision#HK-Home"
	ssLink      = "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ@198.51.124.3:8388#JP%20SS"
	socksLink   = "socks5://user:pass@198.51.124.4:1080#SOCKS"
	tuicLink    = "tuic://uuid:pass@198.51.124.5:443#TUIC"
)

func names(r *Result) []string {
	var out []string
	for _, e := range r.Entries {
		mark := ""
		if e.Err != "" {
			mark = "!"
		}
		out = append(out, mark+e.Name)
	}
	return out
}

func TestParseBase64Variants(t *testing.T) {
	list := strings.Join([]string{realityLink, ssLink, "ss://YWVzLTI1Ni1nY206eA@1.1.1.1:1#剩余流量：120 GB", "trojan://x@127.0.0.1:443#到期时间 2026-12-01", socksLink, tuicLink, "not a link", ""}, "\r\n")
	for name, enc := range map[string]*base64.Encoding{"std": base64.StdEncoding, "url": base64.URLEncoding, "raw": base64.RawStdEncoding, "rawurl": base64.RawURLEncoding} {
		body := enc.EncodeToString([]byte(list))
		// Providers wrap base64 at 76 columns.
		var wrapped strings.Builder
		for i := 0; i < len(body); i += 76 {
			end := i + 76
			if end > len(body) {
				end = len(body)
			}
			wrapped.WriteString(body[i:end] + "\n")
		}
		r, err := Parse([]byte(wrapped.String()))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := strings.Join(names(r), ",")
		if got != "HK-Home,JP SS,SOCKS,!TUIC" {
			t.Fatalf("%s: entries %q", name, got)
		}
		if r.Info != 2 || r.Invalid != 1 {
			t.Fatalf("%s: info %d invalid %d", name, r.Info, r.Invalid)
		}
		if !strings.Contains(r.Entries[3].Err, "TUIC") {
			t.Fatalf("tuic reason %q", r.Entries[3].Err)
		}
		if r.Entries[0].Upstream.Security != "reality" || r.Entries[0].Upstream.Reality == nil {
			t.Fatalf("reality not parsed: %+v", r.Entries[0].Upstream)
		}
	}
}

func TestParsePlainListAndErrors(t *testing.T) {
	r, err := Parse([]byte("\xef\xbb\xbf# comment\n" + ssLink + "\n" + socksLink + "\n"))
	if err != nil || len(r.Entries) != 2 {
		t.Fatalf("plain list: %v %v", err, names(r))
	}
	for body, want := range map[string]string{"": "为空", "<!DOCTYPE html><html></html>": "网页", "hello world": "无法识别"} {
		if _, err := Parse([]byte(body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: %v", body, err)
		}
	}
	var big strings.Builder
	for i := 0; i < MaxEntries+20; i++ {
		fmt.Fprintf(&big, "socks5://198.51.124.%d:%d#n%d\n", i%200+2, 1000+i, i)
	}
	r, err = Parse([]byte(big.String()))
	if err != nil || len(r.Entries) != MaxEntries || !r.Truncated {
		t.Fatalf("truncation: %v %d %v", err, len(r.Entries), r.Truncated)
	}
}

const clashYAML = `
port: 7890
proxies:
  - {name: "🇭🇰 HK Reality", type: vless, server: 198.51.124.2, port: 443, uuid: 4b2f1c84-1f7e-4f3b-9d4c-2f0f6b6a1c11, network: tcp, tls: true, flow: xtls-rprx-vision, servername: www.example.com, client-fingerprint: chrome, reality-opts: {public-key: 8eDxL1uR4ob8BdjX1sY7k2Xz4XlG0bVvUj3sZ0kq8Uo, short-id: ab12}}
  - name: JP SS
    type: ss
    server: 198.51.124.3
    port: 8388
    cipher: aes-256-gcm
    password: "secret"
  - name: SG VMess WS
    type: vmess
    server: vm.example.net
    port: 443
    uuid: 4b2f1c84-1f7e-4f3b-9d4c-2f0f6b6a1c12
    alterId: 0
    cipher: auto
    tls: true
    servername: vm.example.net
    network: ws
    ws-opts: {path: /ws, headers: {Host: vm.example.net}}
  - {name: US Trojan gRPC, type: trojan, server: tr.example.net, port: 443, password: pw, sni: tr.example.net, network: grpc, grpc-opts: {grpc-service-name: gun}}
  - {name: KR Hy2, type: hysteria2, server: 198.51.124.6, port: 8443, password: hpw, sni: hy.example.net, obfs: salamander, obfs-password: op}
  - {name: Home SOCKS, type: socks5, server: 198.51.124.7, port: 1080, username: u, password: p}
  - {name: Office HTTP, type: http, server: 198.51.124.8, port: 3128}
  - {name: TUIC, type: tuic, server: 198.51.124.9, port: 443, uuid: x, password: y}
  - {name: SS plugin, type: ss, server: 198.51.124.10, port: 8388, cipher: aes-256-gcm, password: x, plugin: obfs}
  - {name: Insecure Trojan, type: trojan, server: 198.51.124.11, port: 443, password: pw, skip-cert-verify: true}
  - {name: "官网：example.com", type: ss, server: 198.51.124.12, port: 8388, cipher: aes-256-gcm, password: x}
  - {name: "套餐到期：2026-12-01", type: ss, server: 198.51.124.13, port: 8388, cipher: aes-256-gcm, password: x}
  - {name: placeholder, type: ss, server: 127.0.0.1, port: 1234, cipher: aes-256-gcm, password: x}
proxy-groups: []
`

func TestParseClash(t *testing.T) {
	r, err := Parse([]byte(clashYAML))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names(r), ",")
	want := "🇭🇰 HK Reality,JP SS,SG VMess WS,US Trojan gRPC,KR Hy2,Home SOCKS,Office HTTP,!TUIC,!SS plugin,!Insecure Trojan"
	if got != want {
		t.Fatalf("entries\n got %s\nwant %s", got, want)
	}
	if r.Info != 3 {
		t.Fatalf("info = %d", r.Info)
	}
	labels := []string{}
	for _, e := range r.Entries[:7] {
		labels = append(labels, e.Upstream.ProtocolLabel())
	}
	if strings.Join(labels, ",") != "VLESS Reality,Shadowsocks,VMess WS TLS,Trojan gRPC,Hysteria2,SOCKS5,HTTP" {
		t.Fatalf("labels %v", labels)
	}
	vm := r.Entries[2].Upstream
	if vm.WS == nil || vm.WS.Path != "/ws" || vm.TLS == nil || vm.TLS.ServerName != "vm.example.net" {
		t.Fatalf("vmess ws %+v", vm)
	}
	if r.Entries[4].Upstream.Obfs != "op" || r.Entries[3].Upstream.GRPC == nil || r.Entries[3].Upstream.GRPC.ServiceName != "gun" {
		t.Fatalf("hy2/grpc fields lost: %+v %+v", r.Entries[4].Upstream, r.Entries[3].Upstream)
	}
	if !strings.Contains(r.Entries[9].Err, "跳过证书验证") {
		t.Fatalf("insecure reason %q", r.Entries[9].Err)
	}
	if _, err := Parse([]byte("proxies: [")); err == nil {
		t.Fatal("broken yaml accepted")
	}
}

func TestInfoFilter(t *testing.T) {
	for _, c := range []struct {
		name, host string
		port       int
		info       bool
	}{{"剩余流量：10GB", "198.51.124.2", 443, true}, {"距离下次重置剩余 3 天", "198.51.124.2", 443, true}, {"Traffic: 1GB", "a.example.net", 443, true}, {"香港 01", "198.51.124.2", 443, false}, {"香港 01", "1.1.1.1", 443, true}, {"x", "10.0.0.8", 443, true}, {"x", "198.51.124.2", 1, true}} {
		if got := IsInfoNode(c.name, c.host, c.port); got != c.info {
			t.Fatalf("%+v: %v", c, got)
		}
	}
}

// ---------------------------------------------------------------- fetch

type fakeResolver struct {
	answers func(host string, n int) []netip.Addr
	calls   atomic.Int32
}

func (f *fakeResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	n := int(f.calls.Add(1))
	out := f.answers(host, n)
	if len(out) == 0 {
		return nil, fmt.Errorf("no such host")
	}
	return out, nil
}

// labFetcher dials every checked address to the test server, so the guard
// decides on the fake DNS answers while the bytes come from httptest.
func labFetcher(t *testing.T, srv *httptest.Server, res *fakeResolver) (*Fetcher, *[]string) {
	t.Helper()
	dialed := &[]string{}
	target := srv.Listener.Addr().String()
	return &Fetcher{Resolver: res, Timeout: 2 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		*dialed = append(*dialed, address)
		var d net.Dialer
		return d.DialContext(ctx, network, target)
	}}, dialed
}

func public(...string) []netip.Addr { return []netip.Addr{netip.MustParseAddr("198.51.124.20")} }

func TestFetchGuard(t *testing.T) {
	big := strings.Repeat("a", MaxBodyBytes+10)
	var hops atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("credentials forwarded: %v", r.Header)
		}
		switch r.URL.Path {
		case "/ok":
			if !strings.Contains(r.UserAgent(), "ClashMeta") {
				t.Errorf("ua %q", r.UserAgent())
			}
			fmt.Fprint(w, ssLink)
		case "/big":
			fmt.Fprint(w, big)
		case "/slow":
			time.Sleep(3 * time.Second)
		case "/to-private":
			http.Redirect(w, r, "http://10.1.2.3/ok", http.StatusFound)
		case "/to-rebind":
			http.Redirect(w, r, "http://rebind.example.net/ok", http.StatusFound)
		case "/loop":
			hops.Add(1)
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/403":
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	res := &fakeResolver{answers: func(host string, n int) []netip.Addr {
		switch host {
		case "private.example.net":
			return []netip.Addr{netip.MustParseAddr("198.51.124.20"), netip.MustParseAddr("192.168.1.5")}
		case "rebind.example.net":
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}
		}
		return public()
	}}
	f, dialed := labFetcher(t, srv, res)
	ctx := context.Background()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	u := func(host, path string) string { return fmt.Sprintf("http://%s:%d%s", host, port, path) }

	if body, err := f.Fetch(ctx, u("sub.example.net", "/ok")); err != nil || string(body) != ssLink {
		t.Fatalf("ok: %v %q", err, body)
	}
	if got := (*dialed)[len(*dialed)-1]; !strings.HasPrefix(got, "198.51.124.20:") {
		t.Fatalf("not dialed to the checked ip: %s", got)
	}
	for name, c := range map[string]struct{ url, want string }{
		"private literal":     {u("10.0.0.1", "/ok"), "内网"},
		"loopback literal":    {u("127.0.0.1", "/ok"), "内网"},
		"metadata":            {u("169.254.169.254", "/latest"), "内网"},
		"any answer private":  {u("private.example.net", "/ok"), "内网"},
		"redirect to private": {u("sub.example.net", "/to-private"), "内网"},
		"redirect rebinding":  {u("sub.example.net", "/to-rebind"), "内网"},
		"redirect loop":       {u("sub.example.net", "/loop"), "跳转次数过多"},
		"too big":             {u("sub.example.net", "/big"), "2 MB"},
		"timeout":             {u("sub.example.net", "/slow"), "超时"},
		"http status":         {u("sub.example.net", "/403"), "HTTP 403"},
		"scheme":              {"ftp://sub.example.net/x", "只支持"},
		"userinfo":            {"http://a:b@sub.example.net/x", "用户名或密码"},
	} {
		before := len(*dialed)
		_, err := f.Fetch(ctx, c.url)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: err %v, want %q", name, err, c.want)
		}
		if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "dial") {
			t.Fatalf("%s: internal detail leaked: %v", name, err)
		}
		if strings.HasPrefix(name, "private") || name == "loopback literal" || name == "metadata" || name == "any answer private" {
			if len(*dialed) != before {
				t.Fatalf("%s: dialed %v", name, (*dialed)[before:])
			}
		}
	}
	if n := hops.Load(); n > MaxRedirects+1 {
		t.Fatalf("followed %d redirects", n)
	}
	// Forbidden list (FLVX nodes / panel) applies to subscriptions too.
	f.Forbidden = func(a netip.Addr) (string, bool) { return "FLVX 节点", a == netip.MustParseAddr("198.51.124.20") }
	if _, err := f.Fetch(ctx, u("sub.example.net", "/ok")); err == nil || !strings.Contains(err.Error(), "FLVX 节点") {
		t.Fatalf("forbidden: %v", err)
	}
}
