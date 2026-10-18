package repo

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-backend/internal/store/model"
)

func landingRepoFixture(t *testing.T, dialect string) (*Repository, *model.LandingServer) {
	t.Helper()
	r := orphanTestRepo(t, dialect)
	orphanSeed(t, r, &model.User{ID: 501, User: "landing-owner", Status: 1, ExpTime: time.Now().Add(30 * 24 * time.Hour).UnixMilli()}, &model.User{ID: 502, User: "landing-other", Status: 1}, &model.Node{ID: 601, Name: "native", ServerIP: "203.0.113.1", ServerIPV6: sql.NullString{String: "2001:db8::1", Valid: true}, ExtraIPs: sql.NullString{String: `["203.0.113.2"]`, Valid: true}, Port: "10000-19999", LandingCapable: true, Status: 1}, &model.UserGroup{ID: 701, Name: "landing-grant", Status: 1}, &model.UserGroupUser{UserGroupID: 701, UserID: 501})
	s, err := r.EnableLandingServer(601)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SaveLandingServer(s, []int64{701}); err != nil {
		t.Fatal(err)
	}
	return r, s
}
func TestLandingModelsPermissionsPortsAndCredentials(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			if _, err := r.EnableLandingServer(36); err == nil {
				t.Fatal("node 36 enabled")
			}
			orphanSeed(t, r, &model.Node{ID: 602, Name: "old-agent", ServerIP: "203.0.113.9", Port: "10000-19999"})
			if _, err := r.EnableLandingServer(602); err == nil {
				t.Fatal("old agent enabled")
			}
			for _, table := range []interface{}{&model.LandingServer{}, &model.LandingServerUserGroup{}, &model.LandingNode{}, &model.LandingUserSetting{}, &model.UserServerQuota{}} {
				if !r.db.Migrator().HasTable(table) {
					t.Fatalf("missing table %T", table)
				}
			}
			ss, err := r.ListLandingServers(502, false)
			if err != nil || len(ss) != 0 {
				t.Fatalf("ungranted server visible: %+v %v", ss, err)
			}
			if _, err := r.CreateLandingNode(502, s.ID, "forbidden", "ss2022", false); err == nil {
				t.Fatal("ungranted user created node")
			}
			orphanSeed(t, r, &model.ChainTunnel{TunnelID: 901, NodeID: 601, ChainType: "3", Port: sql.NullInt64{Int64: 30000, Valid: true}}, &model.ForwardPort{ForwardID: 902, NodeID: 601, Port: 30001})
			ssn, err := r.CreateLandingNode(501, s.ID, "SS2022", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			if ssn.Port != 30002 {
				t.Fatalf("port collision: %d", ssn.Port)
			}
			for _, key := range []string{ssn.ServerKey, ssn.UserKey} {
				b, err := base64.StdEncoding.DecodeString(key)
				if err != nil || len(b) != 16 {
					t.Fatalf("SS2022 key invalid length=%d error=%v", len(b), err)
				}
			}
			v, err := r.CreateLandingNode(501, s.ID, "Reality", "vless_reality_vision", false)
			if err != nil {
				t.Fatal(err)
			}
			priv, _ := base64.RawURLEncoding.DecodeString(v.PrivateKey)
			pub, _ := base64.RawURLEncoding.DecodeString(v.PublicKey)
			if len(priv) != 32 || len(pub) != 32 || len(v.UUID) != 36 {
				t.Fatal("invalid Reality credentials")
			}
			raw, _ := json.Marshal(ssn)
			if strings.Contains(string(raw), ssn.ServerKey) || strings.Contains(string(raw), ssn.UserKey) {
				t.Fatal("credentials exposed in node JSON")
			}
			link := LandingURL(*ssn, "2001:db8::1", ssn.Port)
			u, err := url.Parse(link)
			if err != nil {
				t.Fatal(err)
			}
			if u.Hostname() != "2001:db8::1" {
				t.Fatal(link)
			}
			userinfo, _ := url.PathUnescape(strings.Split(strings.TrimPrefix(link, "ss://"), "@")[0])
			if userinfo != ssn.Method+":"+ssn.ServerKey+":"+ssn.UserKey {
				t.Fatalf("invalid SIP002 userinfo: %s", userinfo)
			}
			if _, err := r.CreateLandingNode(501, s.ID, "third", "ss2022", false); err != nil {
				t.Fatal(err)
			}
			if _, err := r.CreateLandingNode(501, s.ID, "fourth", "ss2022", false); err == nil {
				t.Fatal("per-server node limit bypassed")
			}
			if err := r.SaveLandingUserSetting(501, 1, nil, 0); err != nil {
				t.Fatal(err)
			}
			s.MaxNodesPerUser = 10
			if err := r.SaveLandingServer(s, []int64{701}); err != nil {
				t.Fatal(err)
			}
			if _, err := r.CreateLandingNode(501, s.ID, "over total", "ss2022", false); err == nil {
				t.Fatal("total limit bypassed")
			}
		})
	}
}
func TestLandingManualTargetsAndLinks(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			n, err := r.CreateLandingNode(501, s.ID, "node", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			port := n.Port
			orphanSeed(t, r, &model.Tunnel{ID: 801, Name: "local", InIP: sql.NullString{String: "entry.example", Valid: true}}, &model.ChainTunnel{TunnelID: 801, NodeID: 601, ChainType: "3"})
			for _, host := range []string{"203.0.113.1", "203.0.113.2", "[2001:db8::1]", "127.0.0.1", "[::1]"} {
				target := host + ":" + itoaLanding(port)
				id, err := r.MatchLandingTarget(501, 801, target)
				if err != nil || id != n.ID {
					t.Fatalf("manual %s => %d %v", target, id, err)
				}
				if _, err := r.MatchLandingTarget(501, 801, target+",8.8.8.8:443"); err == nil {
					t.Fatal("mixed target allowed")
				}
				id, err = r.MatchLandingTarget(502, 801, target)
				if err != nil || id != 0 {
					t.Fatal("other user's target exempt")
				}
			}
			if id, err := r.MatchLandingTarget(501, 999, "127.0.0.1:"+itoaLanding(port)); err != nil || id != 0 {
				t.Fatal("loopback matched a different exit")
			}
			orphanSeed(t, r, &model.Forward{ID: 901, UserID: 501, TunnelID: 801, LandingNodeID: n.ID, Name: "via", RemoteAddr: "203.0.113.1:" + itoaLanding(port)}, &model.ForwardPort{ForwardID: 901, NodeID: 601, Port: 12000})
			links, err := r.LandingNodeLinks(*n)
			if err != nil || len(links) != 2 {
				t.Fatalf("links %+v %v", links, err)
			}
			u, err := url.Parse(links[1].URL)
			if err != nil || u.Host != "entry.example:12000" {
				t.Fatal("forward endpoint incorrect")
			}
			if err := r.DeleteLandingNode(n.ID); err == nil {
				t.Fatal("released referenced landing port")
			}
		})
	}
}
func itoaLanding(v int) string { return strconv.Itoa(v) }

func TestLandingGeneratedConfigRealCore(t *testing.T) {
	binary := os.Getenv("FLVX_LANDING_CORE_BIN")
	if binary == "" {
		t.Skip("set FLVX_LANDING_CORE_BIN to validate generated credentials with pinned Core")
	}
	for _, protocol := range []string{"ss2022", "vless_reality_vision"} {
		t.Run(protocol, func(t *testing.T) {
			n := model.LandingNode{ID: 1, InboundTag: "flvxl-1", Protocol: protocol, Port: 35001}
			if err := newLandingCredentials(&n); err != nil {
				t.Fatal(err)
			}
			inbound := map[string]interface{}{"tag": n.InboundTag, "listen": "127.0.0.1", "port": n.Port, "protocol": "shadowsocks", "settings": map[string]interface{}{"method": n.Method, "password": n.ServerKey, "network": "tcp,udp", "clients": []interface{}{map[string]interface{}{"password": n.UserKey, "email": n.InboundTag}}}, "sniffing": map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls"}, "routeOnly": true}}
			if protocol == "vless_reality_vision" {
				inbound["protocol"] = "vless"
				inbound["settings"] = map[string]interface{}{"decryption": "none", "clients": []interface{}{map[string]interface{}{"id": n.UUID, "flow": n.Flow, "email": n.InboundTag}}}
				inbound["streamSettings"] = map[string]interface{}{"network": "tcp", "security": "reality", "realitySettings": map[string]interface{}{"dest": "www.lovelive-anime.jp:443", "serverNames": []string{"www.lovelive-anime.jp"}, "privateKey": n.PrivateKey, "shortIds": []string{""}}}
			}
			config := map[string]interface{}{"inbounds": []interface{}{inbound}, "outbounds": []interface{}{map[string]interface{}{"protocol": "freedom", "tag": "direct"}}}
			raw, _ := json.Marshal(config)
			path := filepath.Join(t.TempDir(), "landing.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "run", "-test", "-config", path)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("real Core rejected generated %s config: %v %s", protocol, err, out)
			}
		})
	}
}
