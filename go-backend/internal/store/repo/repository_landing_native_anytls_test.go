package repo

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net/url"
	"strings"
	"testing"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 274: AnyTLS on native landings, gated by the agent's landing_anytls.
func TestNativeAnyTLSLanding(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			x := *reloadServer(t, r, s.ID)
			x.Protocols = `["ss2022","vless_reality_vision","anytls"]`
			if err := r.SaveLandingServer(&x, []int64{701}); err == nil || !strings.Contains(err.Error(), AnyTLSAgentReason) {
				t.Fatalf("old agent: AnyTLS switch enabled: %v", err)
			}
			// Snell / Mieru stay shared-only.
			y := *reloadServer(t, r, s.ID)
			y.Protocols = `["ss2022","snell"]`
			if err := r.SaveLandingServer(&y, []int64{701}); err == nil || !strings.Contains(err.Error(), "Snell / Mieru 目前只支持妙妙屋共享服务器") {
				t.Fatalf("native snell: %v", err)
			}
			if err := r.db.Model(&model.Node{}).Where("id = 601").Update("landing_anytls", LandingAnyTLSVersion).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.SaveLandingServer(&x, []int64{701}); err != nil {
				t.Fatal(err)
			}
			srv0 := reloadServer(t, r, s.ID)
			n, err := r.CreateLandingNode(501, s.ID, "我的 AnyTLS", landingcustom.ProtocolAnyTLS, false)
			if err != nil {
				t.Fatal(err)
			}
			if !validAnyTLSPasswordForTest(n.Password) || n.TLSCertPEM == "" || n.TLSKeyPEM == "" || len(n.CertSHA256) != 64 || n.Port < s.PortMin || n.Port > s.PortMax {
				t.Fatalf("credentials %+v", n)
			}
			// Public host is an IP: SNI = the server's first Reality server name
			// (plan 250 chain rule), certificate SAN = that name, pin = DER sha256.
			sni := nativeAnyTLSDefaultSNI(srv0)
			if sni == "" {
				t.Fatalf("fixture has no Reality server name: %q", srv0.RealityServerNames)
			}
			block, _ := pem.Decode([]byte(n.TLSCertPEM))
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil || len(cert.DNSNames) != 1 || cert.DNSNames[0] != sni {
				t.Fatalf("certificate SAN %v %v", cert, err)
			}
			if sum := sha256.Sum256(block.Bytes); hex.EncodeToString(sum[:]) != n.CertSHA256 {
				t.Fatal("pin is not the certificate sha256")
			}
			if o := LandingNodeOpts(n); o.SNI != sni {
				t.Fatalf("SNI for an IP host: %+v", o)
			}
			if f := LandingNodeFilterFeatures(n); len(f) != 1 || f[0] != "tls" {
				t.Fatalf("AnyTLS filter features %v", f)
			}
			// Agent filter + withheld: only from landing_anytls 1 on.
			old, cur := LandingAgentProtocols{VLESSEnc: 1}, LandingAgentProtocols{VLESSEnc: 1, AnyTLS: 1}
			if !NativeLandingNotSent(old)("anytls") || NativeLandingNotSent(cur)("anytls") || !NativeLandingNotSent(cur)("snell") || !NativeLandingNotSent(cur)("mieru") || NativeLandingNotSent(cur)("ss2022") {
				t.Fatal("native filter")
			}
			srv := reloadServer(t, r, s.ID)
			if !LandingWithheldAgentFunc(srv, old)(n) || LandingWithheldAgentFunc(srv, cur)(n) || LandingWithheldFunc(srv)(n) || LandingNodeWithheld(srv, n) {
				t.Fatal("native withheld")
			}
			d := NativeAnyTLSDesired(*n)
			if d["password"] != n.Password || d["certificate_pem"] != n.TLSCertPEM || d["key_pem"] != n.TLSKeyPEM || len(d) != 3 {
				t.Fatalf("desired %v", d)
			}
			// Links: anytls:// with insecure=1, Clash pinned + UDP on, Surge pinned.
			links, err := r.LandingNodeLinks(*n)
			if err != nil || len(links) != 1 {
				t.Fatal(links, err)
			}
			u, err := url.Parse(links[0].URL)
			if err != nil || u.Scheme != "anytls" || u.User.Username() != n.Password || u.Host != "203.0.113.1:"+idString(int64(n.Port)) || u.Query().Get("insecure") != "1" || u.Query().Get("sni") != sni || u.Fragment != n.Name {
				t.Fatalf("link %q %v", links[0].URL, err)
			}
			for _, want := range []string{"type: anytls", `server: "203.0.113.1"`, `password: "` + n.Password + `"`, `sni: "` + sni + `"`, `fingerprint: "` + n.CertSHA256 + `"`, "udp: true"} {
				if !strings.Contains(links[0].Clash, want) {
					t.Fatalf("clash lacks %q:\n%s", want, links[0].Clash)
				}
			}
			if !strings.Contains(links[0].Surge, "anytls") || !strings.Contains(links[0].Surge, n.CertSHA256) {
				t.Fatalf("surge line %q", links[0].Surge)
			}
			// Subscription node: AnyTLS extra with the pin and the PEM.
			sn, ok := subLandingNode(*n, srv, "203.0.113.1", n.Port, n.Name)
			if !ok || sn.Extra == nil || sn.Extra.Type != "anytls" || sn.Extra.CertSHA256 != n.CertSHA256 || sn.Extra.CertPEM != n.TLSCertPEM || !sn.Extra.UDP || !strings.HasPrefix(sn.URI, "anytls://") {
				t.Fatalf("sub node %+v %v", sn, ok)
			}
			// A domain public host becomes the SNI and the certificate name.
			z := *reloadServer(t, r, s.ID)
			z.PublicHost = "us.example.com"
			if err := r.SaveLandingServer(&z, []int64{701}); err != nil {
				t.Fatal(err)
			}
			n2, err := r.CreateLandingNode(501, s.ID, "域名 AnyTLS", landingcustom.ProtocolAnyTLS, false)
			if err != nil {
				t.Fatal(err)
			}
			if o := LandingNodeOpts(n2); o.SNI != "us.example.com" {
				t.Fatalf("SNI %+v", o)
			}
			// Agent downgraded: the switch stays (unrelated saves work), new nodes refused.
			if err := r.db.Model(&model.Node{}).Where("id = 601").Update("landing_anytls", 0).Error; err != nil {
				t.Fatal(err)
			}
			w := *reloadServer(t, r, s.ID)
			w.MaxNodesPerUser = 9
			if err := r.SaveLandingServer(&w, []int64{701}); err != nil {
				t.Fatalf("unrelated save after downgrade: %v", err)
			}
			if _, err := r.CreateLandingNode(501, s.ID, "再来一个", landingcustom.ProtocolAnyTLS, false); err == nil || !strings.Contains(err.Error(), AnyTLSAgentReason) {
				t.Fatalf("create on downgraded agent: %v", err)
			}
		})
	}
}

func validAnyTLSPasswordForTest(p string) bool {
	if len(p) != 24 {
		return false
	}
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
