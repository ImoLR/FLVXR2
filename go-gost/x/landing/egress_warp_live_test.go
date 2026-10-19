package landing

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// warpLiveAccount registers one throwaway free WARP device (same API calls as
// the panel's go-backend/internal/landingegress/warp.go) and deletes it when
// the test ends.
func warpLiveAccount(t *testing.T) *EgressWireGuard {
	t.Helper()
	const base = "https://api.cloudflareclient.com/v0a5641"
	call := func(method, path, token string, body any, out any) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+path, reader)
		req.Header.Set("User-Agent", "1.1.1.1/6.38.9-5641 (Android 16.0.0)")
		req.Header.Set("CF-Client-Version", "a-6.38.9-5641")
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("WARP API %s %s: %v", method, path, err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if res.StatusCode/100 != 2 {
			t.Fatalf("WARP API %s %s: HTTP %d", method, path, res.StatusCode)
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatalf("WARP API response: %v", err)
			}
		}
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ID     string `json:"id"`
		Token  string `json:"token"`
		Config struct {
			ClientID string `json:"client_id"`
			Peers    []struct {
				PublicKey string `json:"public_key"`
				Endpoint  struct {
					V4, V6 string
				} `json:"endpoint"`
			} `json:"peers"`
			Interface struct {
				Addresses struct{ V4, V6 string } `json:"addresses"`
			} `json:"interface"`
		} `json:"config"`
	}
	call(http.MethodPost, "/reg", "", map[string]any{"key": base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), "install_id": "", "fcm_token": "", "tos": time.Now().UTC().Format(time.RFC3339Nano), "model": "PC", "serial_number": "", "locale": "en_US", "os_version": "16.0.0", "key_type": "curve25519", "tunnel_type": "wireguard"}, &reg)
	t.Cleanup(func() { call(http.MethodDelete, "/reg/"+reg.ID, reg.Token, nil, nil) })
	if len(reg.Config.Peers) == 0 {
		t.Fatal("WARP registration without peers")
	}
	w := &EgressWireGuard{PrivateKey: base64.StdEncoding.EncodeToString(key.Bytes()), PeerPublicKey: reg.Config.Peers[0].PublicKey, AddressV4: reg.Config.Interface.Addresses.V4, AddressV6: reg.Config.Interface.Addresses.V6}
	if b, err := base64.StdEncoding.DecodeString(reg.Config.ClientID); err == nil && len(b) == 3 {
		w.Reserved = []int{int(b[0]), int(b[1]), int(b[2])}
	}
	return w
}

// TestEgressRealCoreWarpLive sends real traffic through the pinned Core's
// WireGuard outbound rendered for a fresh WARP device and checks, per IP
// version, that Cloudflare sees WARP and the forced family inside the tunnel.
// Opt-in: FLVX_WARP_LIVE=1 plus FLVX_LANDING_TEST_BINARY (network access).
func TestEgressRealCoreWarpLive(t *testing.T) {
	if os.Getenv("FLVX_WARP_LIVE") != "1" {
		t.Skip("set FLVX_WARP_LIVE=1 to use the real Cloudflare WARP service")
	}
	dir := t.TempDir()
	core := realCore(t, dir)
	account := warpLiveAccount(t)
	local := detectLocalAddrs()
	if !local.v4.IsValid() && !local.v6.IsValid() {
		t.Skip("no public source address to reach WARP")
	}
	trace := func(mode string, target string) (map[string]string, error) {
		o := EgressOutbound{Tag: "flvxo-warp", Type: "wireguard", IPVersion: mode, WireGuard: account}
		if err := o.validate(); err != nil {
			return nil, err
		}
		out, warn := o.render(local)
		if warn != "" {
			return nil, fmt.Errorf("render warning: %s", warn)
		}
		port := freePort(t)
		cfg := map[string]any{"log": map[string]any{"loglevel": "warning", "access": "none"}, "inbounds": []any{map[string]any{"tag": "in", "listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}}}, "outbounds": []any{out}}
		path := filepath.Join(dir, "warp-"+mode+".json")
		b, _ := json.Marshal(cfg)
		if err := os.WriteFile(path, b, 0600); err != nil {
			return nil, err
		}
		cmd := exec.Command(core, "run", "-config", path)
		var logs bytes.Buffer
		cmd.Stdout, cmd.Stderr = &logs, &logs
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		for i := 0; i < 50; i++ {
			if c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
				c.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		dialer, err := proxy.SOCKS5("tcp", addr, nil, proxy.Direct)
		if err != nil {
			return nil, err
		}
		client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
			return dialer.(proxy.ContextDialer).DialContext(ctx, network, a)
		}}}
		u := url.URL{Scheme: "https", Host: target, Path: "/cdn-cgi/trace"}
		res, err := client.Get(u.String())
		if err != nil {
			return nil, fmt.Errorf("%v (core log: %s)", err, strings.TrimSpace(logs.String()))
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(res.Body, 8192))
		fields := map[string]string{}
		for _, line := range strings.Split(string(body), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				fields[k] = v
			}
		}
		return fields, nil
	}
	family := func(ip string) string {
		a, err := netip.ParseAddr(ip)
		switch {
		case err != nil:
			return "?"
		case a.Unmap().Is4():
			return "v4"
		}
		return "v6"
	}
	for _, tc := range []struct{ mode, want string }{{"force_ipv4", "v4"}, {"force_ipv6", "v6"}, {"prefer_ipv4", "v4"}, {"prefer_ipv6", "v6"}} {
		fields, err := trace(tc.mode, "www.cloudflare.com")
		if err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
		if fields["warp"] != "on" && fields["warp"] != "plus" {
			t.Fatalf("%s: traffic did not leave through WARP: %v", tc.mode, fields)
		}
		if got := family(fields["ip"]); got != tc.want {
			t.Fatalf("%s: Cloudflare saw %s (%s); want %s", tc.mode, fields["ip"], got, tc.want)
		}
		t.Logf("%s: warp=%s ip=%s colo=%s", tc.mode, fields["warp"], fields["ip"], fields["colo"])
	}
	// Forced IPv4 inside the tunnel cannot reach an IPv6-only destination.
	if _, err := trace("force_ipv4", "[2606:4700:4700::1111]"); err == nil {
		t.Fatal("force_ipv4 WARP reached an IPv6 literal")
	}
	if _, err := trace("force_ipv6", "1.1.1.1"); err == nil {
		t.Fatal("force_ipv6 WARP reached an IPv4 literal")
	}
}
