package repo

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-backend/internal/landingcustom"
	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
	"go-backend/internal/vlessenc"
)

// Plan 253: VLESS Encryption landing nodes.

func checkVLESSEncNode(t *testing.T, n *model.LandingNode) {
	t.Helper()
	if id, err := uuid.Parse(n.UUID); err != nil || id.Version() != 4 || n.Flow != "xtls-rprx-vision" {
		t.Fatalf("uuid / flow %q %q", n.UUID, n.Flow)
	}
	pub, err := vlessenc.PublicFromPrivate(n.PrivateKey)
	if err != nil || pub != n.PublicKey {
		t.Fatalf("key pair %q %q %v", n.PublicKey, pub, err)
	}
	if o := LandingNodeOpts(n); o.Mode != "random" {
		t.Fatalf("opts %+v", o)
	}
	if d := VLESSEncDecryption(*n); d != "mlkem768x25519plus.random.600s."+n.PrivateKey || vlessenc.ValidateDecryption(d) != nil {
		t.Fatalf("decryption %q", d)
	}
	if e := VLESSEncEncryption(*n); e != "mlkem768x25519plus.random.0rtt."+n.PublicKey || vlessenc.ValidateEncryption(e) != nil {
		t.Fatalf("encryption %q", e)
	}
	if f := LandingNodeFilterFeatures(n); len(f) != 0 {
		t.Fatalf("vless_enc must have no TLS/HTTP filter feature: %v", f)
	}
}

// checkVLESSEncLink: the 复制链接 URL is the standard share link and parses
// back (「我自己的节点」 upstream / subscriptions) to the same credentials.
func checkVLESSEncLink(t *testing.T, n *model.LandingNode, raw, host string, port int) linechain.Upstream {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "vless" || u.User.Username() != n.UUID || u.Hostname() != host || u.Port() != idString(int64(port)) || u.Fragment != n.Name {
		t.Fatalf("link %q %v", raw, err)
	}
	q := u.Query()
	if q.Get("encryption") != VLESSEncEncryption(*n) || q.Get("flow") != "xtls-rprx-vision" || q.Get("security") != "none" || q.Get("type") != "tcp" || len(q) != 4 {
		t.Fatalf("link query %v", q)
	}
	up, name, err := linechain.ParseLink(raw)
	if err != nil || name != n.Name || !up.Encrypted() || up.Encryption != VLESSEncEncryption(*n) || up.UUID != n.UUID || up.Flow != "xtls-rprx-vision" || up.Network != "tcp" || up.Security != "none" || up.ProtocolLabel() != "VLESS Encryption" {
		t.Fatalf("parse back %+v %q %v", up, name, err)
	}
	return up
}

func TestNativeVLESSEncLanding(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			if strings.Contains(s.Protocols, "vless_enc") {
				t.Fatal("vless_enc on by default")
			}
			x := *reloadServer(t, r, s.ID)
			x.Protocols = `["ss2022","vless_reality_vision","vless_enc"]`
			if err := r.SaveLandingServer(&x, []int64{701}); err == nil || !strings.Contains(err.Error(), "版本过低") {
				t.Fatalf("old agent: switch enabled: %v", err)
			}
			if err := r.db.Model(&model.Node{}).Where("id = 601").Update("landing_vless_enc", LandingVLESSEncVersion).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.SaveLandingServer(&x, []int64{701}); err != nil {
				t.Fatal(err)
			}
			n, err := r.CreateLandingNode(501, s.ID, "我的 VLESS Encryption", "vless_enc", false)
			if err != nil {
				t.Fatal(err)
			}
			checkVLESSEncNode(t, n)
			links, err := r.LandingNodeLinks(*n)
			if err != nil || len(links) != 1 {
				t.Fatal(links, err)
			}
			checkVLESSEncLink(t, n, links[0].URL, "203.0.113.1", n.Port)
			c := links[0].Clash
			for _, want := range []string{"type: vless", `server: "203.0.113.1"`, `uuid: "` + n.UUID + `"`, "flow: xtls-rprx-vision", "network: tcp", "tls: false", "udp: true", `encryption: "` + VLESSEncEncryption(*n) + `"`} {
				if !strings.Contains(c, want) {
					t.Fatalf("clash lacks %q:\n%s", want, c)
				}
			}
			if links[0].Surge != "" {
				t.Fatal("Surge has no VLESS")
			}
			// What the agent gets: only from landing_vless_enc 1 on.
			if NativeLandingNotSent(1)("vless_enc") || !NativeLandingNotSent(0)("vless_enc") || NativeLandingNotSent(0)("ss2022") || !NativeLandingNotSent(1)("anytls") {
				t.Fatal("native filter")
			}
			srv := reloadServer(t, r, s.ID)
			if LandingWithheldAgentFunc(srv, 1)(n) || !LandingWithheldAgentFunc(srv, 0)(n) || LandingWithheldFunc(srv)(n) {
				t.Fatal("native withheld")
			}
			d := VLESSEncDesired(*n)
			b, _ := json.Marshal(d)
			if string(b) != `{"uuid":"`+n.UUID+`","flow":"xtls-rprx-vision","decryption":"`+VLESSEncDecryption(*n)+`"}` {
				t.Fatalf("desired object %s", b)
			}
			// Agent downgraded: the switch stays (unrelated saves work), new nodes refused.
			if err := r.db.Model(&model.Node{}).Where("id = 601").Update("landing_vless_enc", 0).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.SaveLandingServer(reloadServer(t, r, s.ID), []int64{701}); err != nil {
				t.Fatal("kept switch blocked a save:", err)
			}
			if _, err := r.CreateLandingNode(501, s.ID, "again", "vless_enc", false); err == nil || !strings.Contains(err.Error(), "版本过低") {
				t.Fatalf("create on old agent: %v", err)
			}
			// Other protocols unchanged.
			ss, err := r.CreateLandingNode(501, s.ID, "ss", "ss2022", false)
			if err != nil || ss.PrivateKey != "" || ss.ProtocolOpts != "" || !strings.HasPrefix(LandingURL(*ss, "203.0.113.1", ss.Port), "ss://") {
				t.Fatalf("ss2022 %+v %v", ss, err)
			}
		})
	}
}

func vlessEncShare(withCap bool, versions ...int) landingcustom.Share {
	s := extraProtocolShare(true)
	if withCap {
		s.ProtocolCaps.Protocols["vless_enc"] = landingcustom.ProtocolCap{UDP: true, Versions: versions}
	}
	return s
}

func TestSharedVLESSEncLanding(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := extraProtocolFixture(t, dialect)
			x := *reloadServer(t, r, s.ID)
			x.Protocols = `["ss2022","vless_reality_vision","vless_enc"]`
			if err := r.SaveLandingServer(&x, nil); err == nil || !strings.Contains(err.Error(), "Helper 需升级") {
				t.Fatalf("enabled without the share cap: %v", err)
			}
			if _, err := r.CreateLandingNode(501, s.ID, "x", "vless_enc", false); err == nil {
				t.Fatal("created without the switch")
			}
			// A share advertising only a future object version is not usable.
			if err := r.SyncSharedLandings([]landingcustom.Share{vlessEncShare(true, 2)}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if _, ok := SharedProtocolCap(reloadServer(t, r, s.ID), "vless_enc"); ok {
				t.Fatal("versions [2] accepted")
			}
			if err := r.SyncSharedLandings([]landingcustom.Share{vlessEncShare(true, 1)}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			x = *reloadServer(t, r, s.ID)
			if cp, ok := SharedProtocolCap(&x, "vless_enc"); !ok || !cp.UDP {
				t.Fatalf("cap %+v %v", cp, ok)
			}
			x.Protocols = `["ss2022","vless_reality_vision","vless_enc"]`
			if err := r.SaveLandingServer(&x, nil); err != nil {
				t.Fatal(err)
			}
			n, err := r.CreateLandingNode(501, s.ID, "共享 VE", "vless_enc", false)
			if err != nil {
				t.Fatal(err)
			}
			checkVLESSEncNode(t, n)
			ss, err := r.CreateLandingNode(501, s.ID, "共享 SS", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			links, _ := r.LandingNodeLinks(*n)
			checkVLESSEncLink(t, n, links[0].URL, "154.3.39.138", n.Port)
			d, err := r.SharedLandingDesired(s.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			nodes := desiredByID(d)
			ve, sn := nodes[idString(n.ID)], nodes[idString(ss.ID)]
			if ve.Protocol != "vless_enc" || ve.VLESSEnc == nil || *ve.VLESSEnc != (landingcustom.VLESSEnc{UUID: n.UUID, Flow: "xtls-rprx-vision", Decryption: VLESSEncDecryption(*n)}) || ve.VLESS != nil || ve.SS2022 != nil || !ve.Enabled {
				t.Fatalf("vless_enc desired %+v", ve)
			}
			raw, _ := json.Marshal(sn)
			if sn.VLESSEnc != nil || strings.Contains(string(raw), "vless_enc") {
				t.Fatalf("ss2022 payload changed: %s", raw)
			}
			raw, _ = json.Marshal(ve)
			if !strings.Contains(string(raw), `"vless_enc":{"uuid":"`+n.UUID+`","flow":"xtls-rprx-vision","decryption":"mlkem768x25519plus.random.600s.`) {
				t.Fatalf("contract JSON %s", raw)
			}
			// Helper without the cap: node withheld (kept), switch kept, rest syncs.
			if err := r.SyncSharedLandings([]landingcustom.Share{vlessEncShare(false)}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			srv := reloadServer(t, r, s.ID)
			if !strings.Contains(srv.Protocols, "vless_enc") || !LandingNodeWithheld(srv, n) || !strings.Contains(LandingWithheldReason("vless_enc"), "VLESS Encryption") {
				t.Fatalf("withheld %s", srv.Protocols)
			}
			d, _ = r.SharedLandingDesired(s.ID, nil)
			if nodes = desiredByID(d); len(d.Nodes) != 1 || nodes[idString(ss.ID)].ID == "" {
				t.Fatalf("withheld desired %+v", d.Nodes)
			}
			if _, err := r.CreateLandingNode(501, s.ID, "again", "vless_enc", false); err == nil || !strings.Contains(err.Error(), "Helper") {
				t.Fatalf("create without cap: %v", err)
			}
			if err := r.SaveLandingServer(srv, nil); err != nil {
				t.Fatal("unrelated save blocked:", err)
			}
			if err := r.SyncSharedLandings([]landingcustom.Share{vlessEncShare(true)}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			d, _ = r.SharedLandingDesired(s.ID, nil)
			if nodes = desiredByID(d); nodes[idString(n.ID)].VLESSEnc == nil || nodes[idString(n.ID)].Port != n.Port || nodes[idString(n.ID)].VLESSEnc.Decryption != VLESSEncDecryption(*n) {
				t.Fatalf("restored %+v", d.Nodes)
			}
		})
	}
}

// TestVLESSEncSubscriptions: Base64 = the link, Clash = mihomo encryption
// field; sing-box / Surge / Surfboard / Loon / QX / Stash leave it out.
func TestVLESSEncSubscriptions(t *testing.T) {
	keys, _ := vlessenc.NewKeys()
	opts, _ := json.Marshal(LandingProtocolOpts{Mode: "random"})
	n := model.LandingNode{ID: 7, Name: "VE 节点", Protocol: "vless_enc", Port: 31001, UUID: "8f7a1f8e-7c2a-4c1e-9d1b-6c0a5b3c2d10", Flow: "xtls-rprx-vision", PrivateKey: keys.PrivateKey, PublicKey: keys.PublicKey, ProtocolOpts: string(opts)}
	s := &model.LandingServer{ID: 1, Source: "native", PublicHost: "203.0.113.1"}
	node, ok := subLandingNode(n, s, s.PublicHost, n.Port, n.Name)
	if !ok || !node.Up.Encrypted() || node.ProtocolLabel() != "VLESS Encryption" {
		t.Fatalf("sub node %+v %v", node, ok)
	}
	nodes := []sublink.Node{node}
	clash, cn := sublink.Clash(nodes, sublink.ModeGlobal, false)
	if cn != 1 || !strings.Contains(string(clash), "encryption: "+VLESSEncEncryption(n)) || !strings.Contains(string(clash), "flow: xtls-rprx-vision") || !strings.Contains(string(clash), "tls: false") {
		t.Fatalf("clash %d:\n%s", cn, clash)
	}
	if _, sn := sublink.Clash(nodes, sublink.ModeGlobal, true); sn != 0 {
		t.Fatal("Stash got VLESS Encryption")
	}
	b64, bn := sublink.Base64(nodes)
	plain, err := base64.StdEncoding.DecodeString(string(b64))
	if bn != 1 || err != nil || !strings.HasPrefix(string(plain), strings.SplitN(LandingURL(n, s.PublicHost, n.Port), "#", 2)[0]) {
		t.Fatalf("base64 %d %q %v", bn, plain, err)
	}
	if uri := sublink.ShareURI(sublink.Node{Name: n.Name, Up: node.Up}); !strings.Contains(uri, "encryption="+url.QueryEscape(VLESSEncEncryption(n))) || !strings.Contains(uri, "security=none") {
		t.Fatalf("share uri %s", uri)
	}
	if _, c := sublink.SingBox(nodes, sublink.ModeGlobal, ""); c != 0 {
		t.Fatal("sing-box got VLESS Encryption")
	}
	if _, c := sublink.Surge(nodes, sublink.ModeGlobal, false); c != 0 {
		t.Fatal("Surge got VLESS")
	}
	if _, c := sublink.Surge(nodes, sublink.ModeGlobal, true); c != 0 {
		t.Fatal("Surfboard got VLESS")
	}
	if _, c := sublink.Loon(nodes); c != 0 {
		t.Fatal("Loon got VLESS Encryption")
	}
	if _, c := sublink.QX(nodes); c != 0 {
		t.Fatal("QX got VLESS Encryption")
	}
}
