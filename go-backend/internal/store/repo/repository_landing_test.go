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
	orphanSeed(t, r, &model.User{ID: 501, User: "landing-owner", RoleID: 1, Status: 1, ExpTime: time.Now().Add(30 * 24 * time.Hour).UnixMilli()}, &model.User{ID: 502, User: "landing-other", RoleID: 1, Status: 1}, &model.Node{ID: 601, Name: "native", ServerIP: "203.0.113.1", ServerIPV6: sql.NullString{String: "2001:db8::1", Valid: true}, ExtraIPs: sql.NullString{String: `["203.0.113.2"]`, Valid: true}, Port: "10000-19999", LandingCapable: true, Status: 1}, &model.UserGroup{ID: 701, Name: "landing-grant", Status: 1}, &model.UserGroupUser{UserGroupID: 701, UserID: 501})
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
			password, hasPassword := u.User.Password()
			if u.User.Username() != ssn.Method || !hasPassword || password != ssn.ServerKey+":"+ssn.UserKey {
				t.Fatal("standard URL parser could not read SIP002 method/password")
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

func TestLandingSS2022SIP002URL(t *testing.T) {
	n := model.LandingNode{Name: "SS 2022", Protocol: "ss2022", Method: "2022-blake3-aes-128-gcm", ServerKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\xff", 16))), UserKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\xfb", 16)))}
	for _, host := range []string{"203.0.113.1", "2001:db8::1"} {
		parsed, err := url.Parse(LandingURL(n, host, 30001))
		if err != nil {
			t.Fatal(err)
		}
		password, present := parsed.User.Password()
		if parsed.User.Username() != n.Method || !present || password != n.ServerKey+":"+n.UserKey {
			t.Fatal("SIP002 username/password did not round-trip with standard net/url parser")
		}
		if parsed.Scheme != "ss" || parsed.Hostname() != host || parsed.Port() != "30001" || parsed.Fragment != n.Name {
			t.Fatal("SIP002 endpoint or name did not round-trip")
		}
	}
}

func TestLandingDeletionPreparationRetainsAccountingIdentity(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			n, err := r.CreateLandingNode(501, s.ID, "delete", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.DeleteLandingNode(n.ID, true); err == nil {
				t.Fatal("confirmed-delete guard allowed an active node")
			}
			orphanSeed(t, r, &model.Forward{ID: 901, UserID: 501, LandingNodeID: n.ID, Name: "referenced"})
			if _, err := r.PrepareLandingNodeDeletion(n.ID); err == nil {
				t.Fatal("prepared a referenced node")
			}
			unchanged, _ := r.GetLandingNode(n.ID)
			if unchanged == nil || unchanged.Status != "active" {
				t.Fatalf("failed preparation changed status: %+v", unchanged)
			}
			if err := r.db.Delete(&model.Forward{}, 901).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.db.Model(n).Updates(map[string]interface{}{"counter_epoch": "100", "last_seen_up": 10, "last_seen_down": 20, "auto_disabled": true}).Error; err != nil {
				t.Fatal(err)
			}
			before, _ := r.GetLandingServer(s.ID)
			prepared, err := r.PrepareLandingNodeDeletion(n.ID)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Status != "disabled" || prepared.AutoDisabled || prepared.Port != n.Port || prepared.ServerKey != n.ServerKey || prepared.UserKey != n.UserKey || prepared.CounterEpoch != "100" || prepared.LastSeenUp != 10 || prepared.LastSeenDown != 20 {
				t.Fatalf("preparation lost identity/cursor: %+v", prepared)
			}
			after, _ := r.GetLandingServer(s.ID)
			if after.Revision != before.Revision+1 {
				t.Fatalf("revision %d -> %d", before.Revision, after.Revision)
			}
			if err := r.DeleteLandingNode(n.ID, true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

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

func TestLandingRenewalRevisionAndCapabilityDowngrade(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			expires := time.Now().Add(24 * time.Hour).UnixMilli()
			s.DefaultExpiryMode = "fixed"
			s.DefaultExpiresAt = &expires
			if err := r.SaveLandingServer(s, []int64{701}); err != nil {
				t.Fatal(err)
			}
			n, err := r.CreateLandingNode(501, s.ID, "renew", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := r.GetLandingServer(s.ID)
			if err := r.SaveLandingUserSetting(501, 5, []model.UserServerQuota{{ServerKind: "node", ServerRefID: 601, LimitBytes: 0, UsedIn: 999, Paused: true}}, 86400000); err != nil {
				t.Fatal(err)
			}
			after, _ := r.GetLandingServer(s.ID)
			renewed, _ := r.GetLandingNode(n.ID)
			if after.Revision <= before.Revision || *renewed.ExpiresAt != expires+86400000 || renewed.ServerKey != n.ServerKey || renewed.Port != n.Port {
				t.Fatal("renewal changed identity or did not increment revision")
			}
			_, _, quotas, err := r.GetLandingUserSetting(501)
			if err != nil || len(quotas) != 1 || quotas[0].UsedIn != 0 || !quotas[0].LimitOverride {
				t.Fatalf("quota save accepted usage: %+v %v", quotas, err)
			}
			if err := r.ValidateNodeLandingPortRange(601, "10000-39999"); err == nil {
				t.Fatal("widened rule range over landing")
			}
			if used, err := r.HasOtherForwardOnNodePort(601, n.Port, 0); err != nil || !used {
				t.Fatal("forward listener can collide with landing")
			}
			if err := r.SetNodeLandingCapability(601, false); err != nil {
				t.Fatal(err)
			}
			if _, err := r.EnableLandingServer(601); err == nil {
				t.Fatal("downgraded agent could enable")
			}
			if _, err := r.CreateLandingNode(501, s.ID, "downgraded", "ss2022", false); err == nil {
				t.Fatal("downgraded agent could create")
			}
			if err := r.SaveLandingUserSetting(501, 0, nil, 0); err != nil {
				t.Fatal(err)
			}
			setting, limit, _, _ := r.GetLandingUserSetting(501)
			if setting.MaxNodes != 0 || !setting.MaxNodesCustom || limit.Effective != 0 || limit.Source != "custom" {
				t.Fatal("explicit zero node limit became default")
			}
		})
	}
}

func TestLandingTunnelSwitchRematchesTarget(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			n, err := r.CreateLandingNode(501, s.ID, "target", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			addr := "203.0.113.1:" + itoaLanding(n.Port)
			orphanSeed(t, r, &model.Tunnel{ID: 801, Name: "old"}, &model.Tunnel{ID: 802, Name: "new"}, &model.Node{ID: 603, Name: "other-exit", ServerIP: "203.0.113.3"}, &model.ChainTunnel{TunnelID: 801, NodeID: 601, ChainType: "3"}, &model.ChainTunnel{TunnelID: 802, NodeID: 603, ChainType: "3"}, &model.Forward{ID: 901, UserID: 501, TunnelID: 801, LandingNodeID: n.ID, RemoteAddr: "127.0.0.1:" + itoaLanding(n.Port)}, &model.Forward{ID: 902, UserID: 501, TunnelID: 801, RemoteAddr: addr}, &model.Forward{ID: 903, UserID: 501, TunnelID: 801, RemoteAddr: addr + ",1.1.1.1:443"})
			if err := r.UpdateForwardTunnel(901, 802, 1); err != nil {
				t.Fatal(err)
			}
			var f model.Forward
			r.db.First(&f, 901)
			if f.LandingNodeID != 0 {
				t.Fatal("loopback exemption survived unrelated exit")
			}
			if err := r.UpdateForwardTunnel(902, 802, 1); err != nil {
				t.Fatal(err)
			}
			f = model.Forward{}
			r.db.First(&f, 902)
			if f.LandingNodeID != n.ID {
				t.Fatal("manual public target not matched on switch")
			}
			if err := r.UpdateForwardTunnel(903, 802, 1); err == nil {
				t.Fatal("mixed targets accepted on tunnel switch")
			}
			f = model.Forward{}
			r.db.First(&f, 903)
			if f.TunnelID != 801 {
				t.Fatal("rejected switch changed tunnel")
			}
		})
	}
}

func TestLandingDisabledEmptyServerNodeDeletion(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			if err := r.DeleteNodeCascade(601); err == nil {
				t.Fatal("deleted active landing server")
			}
			n, err := r.CreateLandingNode(501, s.ID, "reserved", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			s.Status = "disabled"
			if err := r.SaveLandingServer(s, []int64{701}); err != nil {
				t.Fatal(err)
			}
			if err := r.DeleteNodeCascade(601); err == nil {
				t.Fatal("deleted disabled server with landing nodes")
			}
			if err := r.DeleteLandingNode(n.ID); err != nil {
				t.Fatal(err)
			}
			orphanSeed(t, r, &model.UserServerQuota{UserID: 501, ServerKind: "node", ServerRefID: 601, LimitBytes: 100}, &model.ChainTunnel{TunnelID: 801, NodeID: 601, ChainType: "3"},
				&model.LandingWarpAccount{LandingServerID: s.ID, OutboundID: 9, DeviceID: "d", Token: "t", PrivateKey: "k", Status: "ok", CreatedTime: 1, UpdatedTime: 1})
			if err := r.DeleteNodeCascade(601); err == nil {
				t.Fatal("landing cleanup bypassed existing tunnel reference")
			}
			if retained, _ := r.GetLandingServer(s.ID); retained == nil {
				t.Fatal("failed deletion removed landing metadata")
			}
			if err := r.db.Where("node_id = ?", 601).Delete(&model.ChainTunnel{}).Error; err != nil {
				t.Fatal(err)
			}
			if err := r.DeleteNodeCascade(601); err != nil {
				t.Fatal(err)
			}
			orphanCount(t, r, "node", "id = ?", 0, 601)
			orphanCount(t, r, "landing_server", "id = ?", 0, s.ID)
			orphanCount(t, r, "landing_server_user_group", "landing_server_id = ?", 0, s.ID)
			orphanCount(t, r, "landing_warp_account", "landing_server_id = ?", 0, s.ID)
			orphanCount(t, r, "user_server_quota", "server_kind = ? AND server_ref_id = ?", 0, "node", 601)
		})
	}
}

func TestLandingLoopbackDirectTunnel(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			n, err := r.CreateLandingNode(501, s.ID, "loopback", "ss2022", false)
			if err != nil {
				t.Fatal(err)
			}
			orphanSeed(t, r, &model.Tunnel{ID: 801, Name: "single direct", Type: 1}, &model.Tunnel{ID: 802, Name: "multiple direct", Type: 1}, &model.ChainTunnel{TunnelID: 801, NodeID: 601, ChainType: "1"}, &model.ChainTunnel{TunnelID: 802, NodeID: 601, ChainType: "1"}, &model.ChainTunnel{TunnelID: 802, NodeID: 603, ChainType: "1"})
			for _, host := range []string{"127.0.0.1", "[::1]"} {
				target := host + ":" + itoaLanding(n.Port)
				id, err := r.MatchLandingTarget(501, 801, target)
				if err != nil || id != n.ID {
					t.Fatalf("single direct match %d %v", id, err)
				}
				id, err = r.MatchLandingTarget(501, 802, target)
				if err != nil || id != 0 {
					t.Fatalf("ambiguous multi-entry exemption %d %v", id, err)
				}
			}
		})
	}
}

func TestLandingPhaseOneUniqueIndexMigration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r, s := landingRepoFixture(t, dialect)
			// Recreate the phase-one index, then exercise the actual startup migration.
			if err := r.db.Exec("CREATE UNIQUE INDEX idx_landing_server_node_id ON landing_server(node_id)").Error; err != nil {
				t.Fatal(err)
			}
			if err := autoMigrateAll(r.db); err != nil {
				t.Fatal(err)
			}
			for _, id := range []int64{7, 8} {
				row := model.LandingServer{Source: "shared", RemoteServerID: id, Name: "shared", PublicHost: "203.0.113.7"}
				if err := r.db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			duplicate := *s
			duplicate.ID = 0
			if err := r.db.Create(&duplicate).Error; err == nil {
				t.Fatal("native uniqueness lost")
			}
		})
	}
}
