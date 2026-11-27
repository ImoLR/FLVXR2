package repo

import (
	"bytes"
	"encoding/json"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// The native agent's rules (go-gost/x/landing types.go, = landing-protocols-v1).
var (
	agentSecretRe    = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)
	agentMieruUser   = regexp.MustCompile(`^[A-Za-z0-9]{4,32}$`)
	panelSnellPSK    = regexp.MustCompile(`^[A-Za-z0-9]{32}$`)
	panelMieruUser   = regexp.MustCompile(`^[a-z0-9]{10}$`)
	panelMieruPass   = regexp.MustCompile(`^[A-Za-z0-9]{24}$`)
	sharedSnellMieru = `{"protocol_caps":{"versions":[1],"protocols":{"snell":{"udp":true},"mieru":{"udp":true,"transports":["TCP","UDP"]}}}}`
)

// Plan 317: Snell / Mieru on native landings, gated by landing_snell /
// landing_mieru; every client output is byte-identical to a shared node with
// the same fields (a node moved over keeps every link and subscription).
func TestNativeSnellMieruLanding(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			x := *reloadServer(t, r, s.ID)
			x.Protocols = `["ss2022","vless_reality_vision","snell","mieru"]`
			if err := r.SaveLandingServer(&x, []int64{701}); err == nil || !strings.Contains(err.Error(), SnellAgentReason) {
				t.Fatalf("old agent: Snell switch enabled: %v", err)
			}
			if err := r.db.Model(&model.Node{}).Where("id = 601").Update("landing_snell", LandingSnellVersion).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.SaveLandingServer(&x, []int64{701}); err == nil || !strings.Contains(err.Error(), MieruAgentReason) {
				t.Fatalf("old agent: Mieru switch enabled: %v", err)
			}
			if err := r.db.Model(&model.Node{}).Where("id = 601").Update("landing_mieru", LandingMieruVersion).Error; err != nil {
				t.Fatal(err)
			}
			x.SnellVersion, x.SnellObfs, x.SnellObfsHost, x.MieruTransport = 4, "tls", "WWW.Bing.com", "udp"
			if err := r.SaveLandingServer(&x, []int64{701}); err != nil {
				t.Fatal(err)
			}
			// Invalid defaults are refused on save, as on shared servers.
			bad := *reloadServer(t, r, s.ID)
			bad.SnellObfs, bad.SnellObfsHost = "http", "1.2.3.4"
			if err := r.SaveLandingServer(&bad, []int64{701}); err == nil {
				t.Fatal("IP obfs host saved")
			}
			srv := reloadServer(t, r, s.ID)
			if srv.SnellObfsHost != "www.bing.com" || srv.MieruTransport != "UDP" {
				t.Fatalf("defaults not normalized: %q %q", srv.SnellObfsHost, srv.MieruTransport)
			}
			sn, err := r.CreateLandingNode(501, s.ID, "我的 Snell", landingcustom.ProtocolSnell, false)
			if err != nil {
				t.Fatal(err)
			}
			mi, err := r.CreateLandingNode(501, s.ID, "我的 Mieru", landingcustom.ProtocolMieru, false)
			if err != nil {
				t.Fatal(err)
			}
			if !panelSnellPSK.MatchString(sn.Password) || !agentSecretRe.MatchString(sn.Password) || sn.Port < s.PortMin || sn.Port > s.PortMax {
				t.Fatalf("snell credentials %+v", sn)
			}
			if !panelMieruUser.MatchString(mi.Username) || !agentMieruUser.MatchString(mi.Username) || !panelMieruPass.MatchString(mi.Password) || !agentSecretRe.MatchString(mi.Password) || mi.Port == sn.Port {
				t.Fatalf("mieru credentials %+v", mi)
			}
			if o := LandingNodeOpts(sn); o.Version != 4 || o.Obfs != "tls" || o.ObfsHost != "www.bing.com" {
				t.Fatalf("snell opts %+v", o)
			}
			if o := LandingNodeOpts(mi); o.Transport != "UDP" {
				t.Fatalf("mieru opts %+v", o)
			}
			// Agent objects.
			if d := NativeSnellDesired(*sn); d["psk"] != sn.Password || d["version"] != 4 || d["obfs"] != "tls" || d["obfs_host"] != "www.bing.com" || len(d) != 4 {
				t.Fatalf("snell desired %v", d)
			}
			plain := *sn
			plain.ProtocolOpts = `{}`
			if d := NativeSnellDesired(plain); d["version"] != 4 || d["obfs"] != "none" || len(d) != 3 {
				t.Fatalf("snell desired for an old row %v", d)
			}
			if d := NativeMieruDesired(*mi); d["username"] != mi.Username || d["password"] != mi.Password || len(d) != 2 {
				t.Fatalf("mieru desired %v", d)
			}
			// Agent filter + withheld: only from landing_snell / landing_mieru 1 on.
			old := LandingAgentProtocols{VLESSEnc: 1, AnyTLS: 1}
			cur := LandingAgentProtocols{VLESSEnc: 1, AnyTLS: 1, Snell: 1, Mieru: 1}
			for _, p := range []string{"snell", "mieru"} {
				if !NativeLandingNotSent(old)(p) || NativeLandingNotSent(cur)(p) {
					t.Fatalf("native filter %s", p)
				}
			}
			if NativeLandingNotSent(old)("ss2022") || NativeLandingNotSent(old)("anytls") {
				t.Fatal("native filter, other protocols")
			}
			for _, n := range []*model.LandingNode{sn, mi} {
				if !LandingWithheldAgentFunc(srv, old)(n) || LandingWithheldAgentFunc(srv, cur)(n) || LandingWithheldFunc(srv)(n) || LandingNodeWithheld(srv, n) {
					t.Fatalf("native withheld %s", n.Protocol)
				}
			}
			if sig := NativeExtraSignature(0, 0, 0); sig != "" {
				t.Fatalf("empty signature %q", sig)
			}
			if sig := NativeExtraSignature(2, 0, 1); sig != ";at=2;mi=1" {
				t.Fatalf("signature %q", sig)
			}
			// Links on the native server.
			links, err := r.LandingNodeLinks(*mi)
			if err != nil || len(links) == 0 {
				t.Fatal(links, err)
			}
			u, err := url.Parse(links[0].URL)
			if err != nil || u.Scheme != "mierus" || u.User.Username() != mi.Username || u.Query().Get("protocol") != "UDP" || u.Query().Get("port") != idString(int64(mi.Port)) {
				t.Fatalf("mieru link %q %v", links[0].URL, err)
			}
			if !strings.Contains(links[0].Clash, "type: mieru") || !strings.Contains(links[0].Clash, "transport: UDP") {
				t.Fatalf("mieru clash %s", links[0].Clash)
			}
			links, err = r.LandingNodeLinks(*sn)
			if err != nil || len(links) == 0 || links[0].URL != "" || !strings.Contains(links[0].Clash, "type: snell") || !strings.Contains(links[0].Clash, "udp: true") || links[0].Surge != "" || links[0].SurgeNote != SnellSurgeNoTLSObfs {
				t.Fatalf("snell links %+v %v", links, err)
			}
			// Byte-identical to a shared node with the same fields: client
			// configs, link, subscription node and every subscription format.
			shared := *srv
			shared.Source, shared.SharedCaps = "shared", sharedSnellMieru
			httpSnell := *sn
			httpSnell.ProtocolOpts = `{"version":5,"obfs":"http","obfsHost":"www.bing.com"}`
			noneSnell := *sn
			noneSnell.ProtocolOpts = `{"version":4,"obfs":"none"}`
			tcpMieru := *mi
			tcpMieru.ProtocolOpts = `{"transport":"TCP"}`
			hosts := []string{"203.0.113.1", "2001:db8::1", "dmit-t1.example.com"}
			var native, share []sublink.Node
			for _, n := range []model.LandingNode{*sn, httpSnell, noneSnell, *mi, tcpMieru} {
				for _, host := range hosts {
					c1, s1, note1 := LandingClientConfigAt(n, srv, host, n.Port)
					c2, s2, note2 := LandingClientConfigAt(n, &shared, host, n.Port)
					if c1 == "" || c1 != c2 || s1 != s2 || note1 != note2 {
						t.Fatalf("%s @%s client config differs:\n%s\n%s\n%q %q", n.Protocol, host, c1, c2, s1, s2)
					}
					a, ok1 := subLandingNode(n, srv, host, n.Port, n.Name+" "+host)
					b, ok2 := subLandingNode(n, &shared, host, n.Port, n.Name+" "+host)
					if !ok1 || !ok2 || !reflect.DeepEqual(a, b) {
						t.Fatalf("%s @%s sub node differs: %+v / %+v", n.Protocol, host, a, b)
					}
					native, share = append(native, a), append(share, b)
				}
			}
			render := map[string]func([]sublink.Node) []byte{
				"base64":    func(n []sublink.Node) []byte { b, _ := sublink.Base64(n); return b },
				"clash":     func(n []sublink.Node) []byte { b, _ := sublink.Clash(n, sublink.ModeCNDirect, false); return b },
				"stash":     func(n []sublink.Node) []byte { b, _ := sublink.Clash(n, sublink.ModeGlobal, true); return b },
				"surge":     func(n []sublink.Node) []byte { b, _ := sublink.Surge(n, sublink.ModeCNDirect, false); return b },
				"surfboard": func(n []sublink.Node) []byte { b, _ := sublink.Surge(n, sublink.ModeCNDirect, true); return b },
				"loon":      func(n []sublink.Node) []byte { b, _ := sublink.Loon(n); return b },
				"qx":        func(n []sublink.Node) []byte { b, _ := sublink.QX(n); return b },
				"singbox": func(n []sublink.Node) []byte {
					b, _ := sublink.SingBox(n, sublink.ModeCNDirect, "https://panel.example.com")
					return b
				},
			}
			for format, fn := range render {
				a, b := fn(native), fn(share)
				if len(a) == 0 || !bytes.Equal(a, b) {
					t.Fatalf("%s subscription differs (%d / %d bytes)", format, len(a), len(b))
				}
			}
			// Shared Snell without the Core's UDP fix announces udp: false;
			// native (pinned Core has it) always true — the only difference.
			noUDP := shared
			noUDP.SharedCaps = `{"protocol_caps":{"versions":[1],"protocols":{"snell":{"udp":false}}}}`
			if c, _, _ := LandingClientConfigAt(*sn, &noUDP, "203.0.113.1", sn.Port); !strings.Contains(c, "udp: false") {
				t.Fatalf("shared snell without UDP cap: %s", c)
			}
			// A moved-over shared node (mixed-case username, 64-char secret)
			// still renders on native — the agent's rules accept it.
			moved := *mi
			moved.Username, moved.Password = "AbC4", strings.Repeat("Z9", 32)
			d := NativeMieruDesired(moved)
			if !agentMieruUser.MatchString(d["username"].(string)) || !agentSecretRe.MatchString(d["password"].(string)) {
				t.Fatalf("moved node %v", d)
			}
			raw, _ := json.Marshal(d)
			if !strings.Contains(string(raw), `"username":"AbC4"`) {
				t.Fatalf("moved node json %s", raw)
			}
			// Agent downgraded: the switches stay (unrelated saves work), new nodes refused.
			if err := r.db.Model(&model.Node{}).Where("id = 601").Updates(map[string]any{"landing_snell": 0, "landing_mieru": 0}).Error; err != nil {
				t.Fatal(err)
			}
			w := *reloadServer(t, r, s.ID)
			w.MaxNodesPerUser = 9
			if err := r.SaveLandingServer(&w, []int64{701}); err != nil {
				t.Fatalf("unrelated save after downgrade: %v", err)
			}
			for _, tc := range []struct{ protocol, reason string }{{"snell", SnellAgentReason}, {"mieru", MieruAgentReason}} {
				if _, err := r.CreateLandingNode(501, s.ID, "再来一个", tc.protocol, false); err == nil || !strings.Contains(err.Error(), tc.reason) {
					t.Fatalf("create %s on downgraded agent: %v", tc.protocol, err)
				}
			}
		})
	}
}

// SetNodeLandingCapabilities stores landing_snell / landing_mieru (versions 8
// and 9) and older reports (fewer versions) reset them to 0.
func TestNodeLandingCapabilitiesSnellMieru(t *testing.T) {
	r, _ := landingRepoFixture(t, "sqlite")
	if err := r.SetNodeLandingCapabilities(601, true, 1, 3, 3, 2, 1, 1, 3, 1, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	var n model.Node
	if err := r.db.First(&n, 601).Error; err != nil || n.LandingSnell != 1 || n.LandingMieru != 1 || n.LandingAnyTLS != 1 {
		t.Fatalf("caps %+v %v", NodeLandingProtocols(&n), err)
	}
	if err := r.SetNodeLandingCapabilities(601, true, 1, 3, 3, 2, 1, 1, 3, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.db.First(&n, 601).Error; err != nil || n.LandingSnell != 0 || n.LandingMieru != 0 || n.LandingAnyTLS != 1 {
		t.Fatalf("caps after older report %+v %v", NodeLandingProtocols(&n), err)
	}
}
