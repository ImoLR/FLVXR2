package repo

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// Plan 121 fixtures: share 10 「Dmit T1」 is also FLVX node 34 (dual-installed).
func extraProtocolShare(caps bool) landingcustom.Share {
	s := landingcustom.Share{ID: 10, Name: "Dmit T1", Hosts: landingcustom.Hosts{IPv4: "154.3.39.138"}, Status: "active", PortMin: 30000, PortMax: 30010,
		ResetDay: 1, MaxConnections: 1000, Core: landingcustom.Core{State: "running"}, Protocols: []string{"ss2022", "vless_reality_vision"}, DualInstall: true,
		ServerMode: "embedded", BusyPorts: []int{30000, 30002, 10015}}
	if caps {
		s.ProtocolCaps = &landingcustom.ProtocolCaps{Versions: []int{1}, Protocols: map[string]landingcustom.ProtocolCap{
			"anytls": {UDP: true}, "snell": {UDP: true, Versions: []int{4, 5}, Obfs: []string{"none", "http", "tls"}}, "mieru": {UDP: true, Transports: []string{"TCP", "UDP"}}}}
	}
	return s
}

func extraProtocolFixture(t *testing.T, dialect string) (*Repository, *model.LandingServer) {
	t.Helper()
	r := orphanTestRepo(t, dialect)
	orphanSeed(t, r, &model.User{ID: 501, User: "landing-owner", Status: 1, ExpTime: time.Now().Add(30 * 24 * time.Hour).UnixMilli()},
		&model.Node{ID: 34, Name: "Dmit T1", ServerIP: "154.3.39.138", Port: "10000-65535", Status: 1},
		&model.ForwardPort{ForwardID: 9001, NodeID: 34, Port: 30001})
	if err := r.SyncSharedLandings([]landingcustom.Share{extraProtocolShare(true)}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var s model.LandingServer
	if err := r.db.Where("source = 'shared' AND remote_server_id = 10").First(&s).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&model.LandingServerUser{LandingServerID: s.ID, UserID: 501, CreatedTime: 1}).Error; err != nil {
		t.Fatal(err)
	}
	return r, &s
}

func reloadServer(t *testing.T, r *Repository, id int64) *model.LandingServer {
	t.Helper()
	s, err := r.GetLandingServer(id)
	if err != nil || s == nil {
		t.Fatal(err)
	}
	return s
}

func desiredByID(d landingcustom.Desired) map[string]landingcustom.Node {
	out := map[string]landingcustom.Node{}
	for _, n := range d.Nodes {
		out[n.ID] = n
	}
	return out
}

func TestSharedExtraProtocolsLifecycle(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := extraProtocolFixture(t, dialect)
			// Fresh share: only the v1 protocols start enabled (opt-in), caps stored sanitized.
			if got := jsonStringsOf(s.Protocols); strings.Join(got, ",") != "ss2022,vless_reality_vision" {
				t.Fatalf("fresh protocols %v", got)
			}
			caps := SharedLandingCaps(s)
			if caps.ServerMode != "embedded" || caps.BusyPorts != nil || caps.ProtocolCaps == nil || fmt.Sprint(SharedBusyPortList(s)) != "[30000 30002]" {
				t.Fatalf("caps not stored: %+v busy %v", caps, SharedBusyPortList(s))
			}
			if _, err := r.CreateLandingNode(501, s.ID, "not enabled", "anytls", false); err == nil {
				t.Fatal("extra protocol created before the admin enabled it")
			}
			// Admin enables all three with Snell v5 + HTTP obfs and Mieru UDP.
			s.Protocols = `["ss2022","vless_reality_vision","anytls","snell","mieru"]`
			s.SnellVersion, s.SnellObfs, s.SnellObfsHost, s.MieruTransport = 5, "http", "www.bing.com", "UDP"
			if err := r.SaveLandingServer(s, nil); err != nil {
				t.Fatal(err)
			}
			s = reloadServer(t, r, s.ID)
			any, err := r.CreateLandingNode(501, s.ID, "我的 AnyTLS", "anytls", false)
			if err != nil {
				t.Fatal(err)
			}
			snell, err := r.CreateLandingNode(501, s.ID, "我的 Snell", "snell", false)
			if err != nil {
				t.Fatal(err)
			}
			mieru, err := r.CreateLandingNode(501, s.ID, "我的 Mieru", "mieru", false)
			if err != nil {
				t.Fatal(err)
			}
			// 30000 / 30002 busy on the machine (Custom), 30001 = FLVX node 34's forward.
			if any.Port != 30003 || snell.Port != 30004 || mieru.Port != 30005 {
				t.Fatalf("ports %d %d %d", any.Port, snell.Port, mieru.Port)
			}
			alnum := regexp.MustCompile(`^[A-Za-z0-9]+$`)
			if len(any.Password) != 24 || !alnum.MatchString(any.Password) || len(snell.Password) != 32 || !alnum.MatchString(snell.Password) ||
				len(mieru.Password) != 24 || !regexp.MustCompile(`^[a-z0-9]{10}$`).MatchString(mieru.Username) {
				t.Fatalf("credentials %q %q %q/%q", any.Password, snell.Password, mieru.Username, mieru.Password)
			}
			// AnyTLS certificate: parseable, key matches, fingerprint = SHA-256 of DER, IP SAN (IP host, no SNI).
			block, _ := pem.Decode([]byte(any.TLSCertPEM))
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tls.X509KeyPair([]byte(any.TLSCertPEM), []byte(any.TLSKeyPEM)); err != nil {
				t.Fatal("key does not match certificate:", err)
			}
			sum := sha256.Sum256(block.Bytes)
			if any.CertSHA256 != hex.EncodeToString(sum[:]) || len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "154.3.39.138" || cert.NotAfter.Before(time.Now().AddDate(9, 0, 0)) {
				t.Fatalf("certificate %s %v %v", any.CertSHA256, cert.IPAddresses, cert.NotAfter)
			}
			if o := LandingNodeOpts(snell); o.Version != 5 || o.Obfs != "http" || o.ObfsHost != "www.bing.com" {
				t.Fatalf("snell opts %+v", o)
			}
			if o := LandingNodeOpts(mieru); o.Transport != "UDP" {
				t.Fatalf("mieru opts %+v", o)
			}
			// Desired carries exactly one contract object per node.
			d, err := r.SharedLandingDesired(s.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			nodes := desiredByID(d)
			a, sn, mi := nodes[idString(any.ID)], nodes[idString(snell.ID)], nodes[idString(mieru.ID)]
			if a.AnyTLS == nil || a.AnyTLS.Password != any.Password || a.AnyTLS.Padding != "default" || a.AnyTLS.TLS.ServerName != "" || a.AnyTLS.TLS.CertificatePEM != any.TLSCertPEM || a.AnyTLS.TLS.KeyPEM != any.TLSKeyPEM || a.SS2022 != nil || a.VLESS != nil {
				t.Fatalf("anytls desired %+v", a)
			}
			if sn.Snell == nil || *sn.Snell != (landingcustom.Snell{PSK: snell.Password, Version: 5, Obfs: "http", ObfsHost: "www.bing.com"}) {
				t.Fatalf("snell desired %+v", sn.Snell)
			}
			if mi.Mieru == nil || *mi.Mieru != (landingcustom.Mieru{Username: mieru.Username, Password: mieru.Password}) || !mi.Enabled {
				t.Fatalf("mieru desired %+v", mi)
			}
			// Client configs.
			links, err := r.LandingNodeLinks(*any)
			if err != nil || len(links) != 1 {
				t.Fatal(links, err)
			}
			if !strings.HasPrefix(links[0].URL, "anytls://"+any.Password+"@154.3.39.138:30003/?insecure=1#") || strings.Contains(links[0].URL, "sni=") ||
				!strings.Contains(links[0].Clash, `fingerprint: "`+any.CertSHA256+`"`) || !strings.Contains(links[0].Clash, "udp: true") || links[0].Surge != "" {
				t.Fatalf("anytls link %+v", links[0])
			}
			links, _ = r.LandingNodeLinks(*snell)
			if links[0].URL != "" || links[0].Surge != "我的 Snell = snell, 154.3.39.138, 30004, psk="+snell.Password+", version=5, obfs=http, obfs-host=www.bing.com" ||
				!strings.Contains(links[0].Clash, "obfs-opts:\n    mode: http\n    host: \"www.bing.com\"") {
				t.Fatalf("snell link %+v", links[0])
			}
			links, _ = r.LandingNodeLinks(*mieru)
			u, err := url.Parse(links[0].URL)
			if err != nil || u.Scheme != "mierus" || u.User.Username() != mieru.Username || u.Query().Get("protocol") != "UDP" || u.Query().Get("port") != "30005" || u.Query().Get("profile") != "我的 Mieru" {
				t.Fatalf("mieru link %q %v", links[0].URL, err)
			}
			// Forward ports on the same machine (node 34) avoid the shared landing ports.
			ports, err := landingPortsOnNode(r.db, 34)
			if err != nil || !containsAll(ports, 30003, 30004, 30005) {
				t.Fatalf("node 34 landing ports %v %v", ports, err)
			}
			if used, err := r.LandingPortInUse(34, 30004); err != nil || !used {
				t.Fatalf("LandingPortInUse %v %v", used, err)
			}
			// The Helper is replaced by one without the capability: nodes withheld, rest keeps syncing.
			if err := r.SyncSharedLandings([]landingcustom.Share{extraProtocolShare(false)}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			s = reloadServer(t, r, s.ID)
			if got := jsonStringsOf(s.Protocols); len(got) != 5 {
				t.Fatalf("enabled extra protocols dropped by a missing capability: %v", got)
			}
			before := d.Revision
			d, err = r.SharedLandingDesired(s.ID, nil)
			if err != nil || len(d.Nodes) != 0 || d.Revision <= before {
				t.Fatalf("withheld desired %+v %v", d, err)
			}
			if !LandingNodeWithheld(s, any) || LandingNodeWithheld(s, &model.LandingNode{Protocol: "ss2022"}) {
				t.Fatal("withheld flag")
			}
			if _, err := r.CreateLandingNode(501, s.ID, "new", "snell", false); err == nil || !strings.Contains(err.Error(), "Helper 需升级") {
				t.Fatalf("create while unsupported: %v", err)
			}
			if err := r.SaveLandingServer(s, nil); err != nil {
				t.Fatal("unrelated save blocked by missing capability:", err)
			}
			// Capability back: same ports and credentials again.
			if err := r.SyncSharedLandings([]landingcustom.Share{extraProtocolShare(true)}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			s = reloadServer(t, r, s.ID)
			d, _ = r.SharedLandingDesired(s.ID, nil)
			nodes = desiredByID(d)
			if len(d.Nodes) != 3 || nodes[idString(any.ID)].Port != 30003 || nodes[idString(any.ID)].AnyTLS.Password != any.Password {
				t.Fatalf("restored desired %+v", d.Nodes)
			}
		})
	}
}

func TestExtraProtocolValidation(t *testing.T) {
	r, s := extraProtocolFixture(t, "sqlite")
	base := *s
	for name, mutate := range map[string]func(*model.LandingServer){
		"snell obfs without host": func(x *model.LandingServer) { x.Protocols = `["snell"]`; x.SnellObfs = "http"; x.SnellObfsHost = "" },
		"snell v6":                func(x *model.LandingServer) { x.Protocols = `["snell"]`; x.SnellVersion = 6 },
		"anytls sni is an ip":     func(x *model.LandingServer) { x.Protocols = `["anytls"]`; x.AnyTLSServerName = "1.2.3.4" },
		"mieru quic":              func(x *model.LandingServer) { x.Protocols = `["mieru"]`; x.MieruTransport = "QUIC" },
		"unknown protocol":        func(x *model.LandingServer) { x.Protocols = `["hysteria2"]` },
	} {
		x := base
		mutate(&x)
		if err := r.SaveLandingServer(&x, nil); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	// A share restricting Snell to v4 refuses a v5 server default.
	share := extraProtocolShare(true)
	share.ProtocolCaps.Protocols["snell"] = landingcustom.ProtocolCap{Versions: []int{4}}
	if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	x := *reloadServer(t, r, s.ID)
	x.Protocols, x.SnellVersion = `["ss2022","snell"]`, 5
	if err := r.SaveLandingServer(&x, nil); err == nil || !strings.Contains(err.Error(), "Snell") {
		t.Fatalf("v5 accepted by a v4-only share: %v", err)
	}
	x.SnellVersion = 4
	if err := r.SaveLandingServer(&x, nil); err != nil {
		t.Fatal(err)
	}
	// Enabling a protocol the share does not advertise is refused (never kept).
	share.ProtocolCaps = nil
	if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	x = *reloadServer(t, r, s.ID)
	x.Protocols = `["ss2022","snell","mieru"]`
	if err := r.SaveLandingServer(&x, nil); err == nil || !strings.Contains(err.Error(), "Mieru") {
		t.Fatalf("unadvertised protocol enabled: %v", err)
	}
}

func TestNativeLandingRefusesExtraProtocols(t *testing.T) {
	r, s := landingRepoFixture(t, "sqlite")
	x := *s
	x.Protocols = `["ss2022","anytls"]`
	if err := r.SaveLandingServer(&x, []int64{701}); err == nil || !strings.Contains(err.Error(), "妙妙屋共享") {
		t.Fatalf("native accepted anytls: %v", err)
	}
	if _, err := r.CreateLandingNode(501, s.ID, "x", "mieru", false); err == nil {
		t.Fatal("native created mieru")
	}
}

func TestSharedAnyTLSUsesShareDomain(t *testing.T) {
	r := orphanTestRepo(t, "sqlite")
	orphanSeed(t, r, &model.User{ID: 501, User: "owner", Status: 1})
	share := extraProtocolShare(true)
	share.Hosts.Domain = "dmit.example.com"
	if err := r.SyncSharedLandings([]landingcustom.Share{share}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var s model.LandingServer
	r.db.Where("remote_server_id = 10").First(&s)
	if s.AnyTLSServerName != "dmit.example.com" || s.PublicHost != "dmit.example.com" {
		t.Fatalf("defaults %q %q", s.AnyTLSServerName, s.PublicHost)
	}
	s.Protocols = `["ss2022","anytls"]`
	if err := r.SaveLandingServer(&s, nil); err != nil {
		t.Fatal(err)
	}
	r.db.Create(&model.LandingServerUser{LandingServerID: s.ID, UserID: 501, CreatedTime: 1})
	n, err := r.CreateLandingNode(501, s.ID, "a", "anytls", false)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(n.TLSCertPEM))
	cert, _ := x509.ParseCertificate(block.Bytes)
	if LandingNodeOpts(n).SNI != "dmit.example.com" || len(cert.DNSNames) != 1 || cert.DNSNames[0] != "dmit.example.com" {
		t.Fatalf("sni %+v san %v", LandingNodeOpts(n), cert.DNSNames)
	}
	links, _ := r.LandingNodeLinks(*n)
	if !strings.Contains(links[0].URL, "sni=dmit.example.com") || !strings.Contains(links[0].Clash, `sni: "dmit.example.com"`) {
		t.Fatalf("links %+v", links[0])
	}
}

func jsonStringsOf(raw string) []string {
	var out []string
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func idString(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func containsAll(list []int, want ...int) bool {
	set := map[int]bool{}
	for _, v := range list {
		set[v] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
