package contract_test

import (
	"database/sql"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 252 on SQLite and PostgreSQL through the real router, prod-shaped:
// Bestvm US (chain-only, sunflorian's active 「ATTVPS」) and Bestvm JP B
// (chain-only, one rule). Deleting the JP rule through the API leaves its
// server idle (a cleanup candidate); pausing ATTVPS keeps Bestvm US in use;
// the guarded delete removes only the idle one.
func TestChainOnlyCleanupContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { runChainOnlyCleanup(t, dialect) })
	}
}

func runChainOnlyCleanup(t *testing.T, dialect string) {
	const secret = "chain-only-cleanup-jwt"
	r := landingContractRepo(t, dialect)
	now := time.Now().UnixMilli()
	entry := model.Node{Name: "Dmit Echo", Secret: "coc-entry-secret", ServerIP: "10.63.0.1", Port: "20000-20999", CreatedTime: now, Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]", LineMachine: true}
	if err := r.DB().Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	exits := map[string]*model.Node{}
	for i, name := range []string{"Bestvm US", "Bestvm JP B"} {
		n := model.Node{Name: name, Secret: fmt.Sprintf("coc-exit-%d", i), ServerIP: fmt.Sprintf("10.63.1.%d", i+1), Port: "10000-65535", CreatedTime: now, Status: 0, TCPListenAddr: "[::]", UDPListenAddr: "[::]", LandingCapable: true, LandingChain: repo.ChainHostRemoveVersion}
		if err := r.DB().Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		exits[name] = &n
	}
	const uid = 74
	type rule struct {
		exit, name string
		port       int
	}
	rules := []rule{{"Bestvm US", "ATTVPS", 1024}, {"Bestvm JP B", "jp-own", 1025}}
	rows := []interface{}{&model.User{ID: uid, User: "sunflorian", Pwd: "x", RoleID: 1, ExpTime: now + 86400000*30, Flow: 99999, FlowResetTime: 1, Num: 99, CreatedTime: now, Status: 1}}
	servers, forwards := map[string]int64{}, map[string]int64{}
	for i, ru := range rules {
		tu := model.Tunnel{Name: "Dmit Echo -> " + ru.exit, TrafficRatio: 1, Type: 2, Protocol: "tls", Flow: 99999, CreatedTime: now, UpdatedTime: now, Status: 1}
		if err := r.DB().Create(&tu).Error; err != nil {
			t.Fatal(err)
		}
		for _, ct := range []model.ChainTunnel{
			{TunnelID: tu.ID, ChainType: "1", NodeID: entry.ID, Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}},
			{TunnelID: tu.ID, ChainType: "3", NodeID: exits[ru.exit].ID, Port: sql.NullInt64{Int64: int64(21000 + i), Valid: true}, Strategy: sql.NullString{String: "round", Valid: true}, Inx: sql.NullInt64{Int64: 1, Valid: true}, Protocol: sql.NullString{String: "tls", Valid: true}},
		} {
			ct := ct
			if err := r.DB().Create(&ct).Error; err != nil {
				t.Fatal(err)
			}
		}
		s := model.LandingServer{Source: "native", NodeID: exits[ru.exit].ID, Name: ru.exit, PublicHost: exits[ru.exit].ServerIP, PortMin: 30000, PortMax: 39999, FlowType: 2, TrafficRatio: 1, Status: "active", Revision: 69, ChainOnly: true, CoreCPUPercent: repo.ChainOnlyCoreCPUPercent, CoreMemoryMB: 256, CreatedTime: now, UpdatedTime: now}
		if err := r.DB().Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		f := model.Forward{UserID: uid, UserName: "sunflorian", Name: ru.name, TunnelID: tu.ID, RemoteAddr: fmt.Sprintf("127.0.0.1:%d", ru.port), Strategy: "fifo", CreatedTime: now, UpdatedTime: now, Status: 1, Mode: "gost"}
		if err := r.DB().Create(&f).Error; err != nil {
			t.Fatal(err)
		}
		c := model.ForwardChain{UserID: uid, TunnelID: tu.ID, ForwardID: f.ID, State: model.ChainStateActive, Port: ru.port, Upstream: `{"type":"socks","server":"198.51.100.9","port":1080}`, CreatedTime: now, UpdatedTime: now}
		if err := r.DB().Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		rows = append(rows,
			&model.UserTunnel{UserID: uid, TunnelID: tu.ID, Num: 99, Flow: 99999, FlowResetTime: 1, ExpTime: now + 86400000*30, Status: 1},
			&model.ForwardPort{ForwardID: f.ID, NodeID: entry.ID, Port: 20100 + i},
			&model.ForwardChainNode{ChainID: c.ID, LandingServerID: s.ID, NodeID: exits[ru.exit].ID, Port: ru.port, State: model.ChainNodeActive, CreatedTime: now},
		)
		servers[ru.exit], forwards[ru.name] = s.ID, f.ID
	}
	for _, row := range rows {
		if err := r.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	h := handler.New(r, secret, "test")
	router := httpserver.NewRouter(h, secret, r)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	stop := startMockNodeSession(t, server.URL, entry.Secret)
	t.Cleanup(stop)
	user, _ := auth.GenerateToken(uid, "sunflorian", 1, secret)
	idle := func() []int64 {
		t.Helper()
		list, err := r.IdleChainOnlyServers()
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, s := range list {
			out = append(out, s.ID)
		}
		return out
	}
	if got := idle(); len(got) != 0 {
		t.Fatalf("idle before any delete = %v", got)
	}
	// Pausing ATTVPS keeps Bestvm US in use (the rule comes back).
	if out := requestContractEnvelope(t, router, user, "/api/v1/forward/pause", map[string]interface{}{"id": forwards["ATTVPS"]}); out.Code != 0 {
		t.Fatalf("pause: %+v", out)
	}
	// Deleting the JP rule (exit offline) leaves Bestvm JP B idle.
	if out := requestContractEnvelope(t, router, user, "/api/v1/forward/delete", map[string]interface{}{"id": forwards["jp-own"]}); out.Code != 0 {
		t.Fatalf("delete: %+v", out)
	}
	h.WaitChainSyncs()
	if got := idle(); fmt.Sprint(got) != fmt.Sprint([]int64{servers["Bestvm JP B"]}) {
		t.Fatalf("idle after delete = %v, want [%d]", got, servers["Bestvm JP B"])
	}
	if why, err := r.ChainHostKeepReason(servers["Bestvm US"]); err != nil || why != "仍有「我自己的节点」规则" {
		t.Fatalf("Bestvm US keep reason = %q %v", why, err)
	}
	if removed, why, err := r.DeleteIdleChainOnlyServer(servers["Bestvm US"]); err != nil || removed || why == "" {
		t.Fatalf("used server deleted: %v %q %v", removed, why, err)
	}
	if removed, _, err := r.DeleteIdleChainOnlyServer(servers["Bestvm JP B"]); err != nil || !removed {
		t.Fatalf("idle server not deleted: %v %v", removed, err)
	}
	var left []model.LandingServer
	r.DB().Find(&left)
	us, _ := r.GetLandingServer(servers["Bestvm US"])
	var copies int64
	r.DB().Model(&model.ForwardChainNode{}).Where("landing_server_id = ?", servers["Bestvm US"]).Count(&copies)
	if len(left) != 1 || us == nil || us.Revision < 69 || copies != 1 {
		t.Fatalf("left = %+v, Bestvm US = %+v, copies = %d", left, us, copies)
	}
}
