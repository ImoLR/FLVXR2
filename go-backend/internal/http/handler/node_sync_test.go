package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

func nodeSyncRequest(t *testing.T, fn http.HandlerFunc, body interface{}, data interface{}) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	r = r.WithContext(context.WithValue(r.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "1", RoleID: 0}))
	w := httptest.NewRecorder()
	fn(w, r)
	var payload struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || payload.Code != 0 {
		t.Fatalf("request failed: %s (%v)", w.Body.String(), err)
	}
	if err := json.Unmarshal(payload.Data, data); err != nil {
		t.Fatalf("decode data: %v (%s)", err, w.Body.String())
	}
}

func nodeSyncBody(name, ip string) map[string]interface{} {
	return map[string]interface{}{"id": 23, "name": name, "serverIp": ip, "serverIpV4": ip,
		"serverIpV6": "", "intranetIp": "", "extraIPs": "", "interfaceName": "", "port": "1000-2000",
		"tcpListenAddr": "[::]", "udpListenAddr": "[::]"}
}

func newNodeSyncEnv(t *testing.T) *flowTestEnv {
	t.Helper()
	e := newFlowTestEnv(t)
	e.h.wsServer = ws.NewServer(e.r, "test")
	e.addNode(23, "entry")
	e.exec(`UPDATE node SET server_ip = '192.0.2.1', server_ip_v4 = '192.0.2.1' WHERE id = 23`)
	e.addTunnel(25, 1, 1)
	e.exec(`UPDATE tunnel SET name = 'entry tunnel', in_ip = '192.0.2.1' WHERE id = 25`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, protocol) VALUES(25, '1', 23, 1, 'tls')`)
	e.addUser(7)
	e.addForward(42, 7, 25)
	e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(42, 23, 1234)`)
	return e
}

func TestNodeUpdateRewritesTunnelAndRuleList(t *testing.T) {
	e := newNodeSyncEnv(t)
	e.addTunnel(26, 1, 1)
	e.exec(`UPDATE tunnel SET in_ip = 'custom.example.test' WHERE id = 26`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, protocol) VALUES(26, '1', 23, 1, 'tls')`)
	var result nodeSyncResult
	nodeSyncRequest(t, e.h.nodeUpdate, nodeSyncBody("entry", "192.0.2.2"), &result)
	if result.TunnelsUpdated != 1 || result.RulesUpdated != 1 || result.EntryAddressesUpdated != 1 || len(result.Failures) != 0 {
		t.Fatalf("sync result = %+v", result)
	}
	var tunnels []struct {
		ID   int64  `json:"id"`
		InIP string `json:"inIp"`
	}
	nodeSyncRequest(t, e.h.tunnelList, map[string]interface{}{}, &tunnels)
	for _, tunnel := range tunnels {
		want := "192.0.2.2"
		if tunnel.ID == 26 {
			want = "custom.example.test"
		}
		if tunnel.InIP != want {
			t.Fatalf("tunnel %d ingress = %q, want %q", tunnel.ID, tunnel.InIP, want)
		}
	}
	var forwards struct {
		Items []struct {
			InIP string `json:"inIp"`
		} `json:"items"`
	}
	nodeSyncRequest(t, e.h.forwardList, map[string]interface{}{}, &forwards)
	if len(forwards.Items) != 1 || forwards.Items[0].InIP != "192.0.2.2:1234" {
		t.Fatalf("rule list = %+v", forwards)
	}
}

func TestNodeUpdateNameOnlyAndUnchangedDoNotPropagate(t *testing.T) {
	e := newNodeSyncEnv(t)
	// The stale value also proves an unchanged save is not an implicit data fix.
	e.exec(`UPDATE tunnel SET in_ip = '192.0.2.99', type = 2 WHERE id = 25`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, protocol) VALUES(25, '3', 23, 1, 'tls')`)
	e.exec(`CREATE TRIGGER reject_ingress_rewrite BEFORE UPDATE OF in_ip ON tunnel BEGIN SELECT RAISE(ABORT, 'unexpected rewrite'); END`)
	// A runtime call would dereference the missing server; the edited node is online.
	e.h.wsServer = nil
	for _, name := range []string{"n", "renamed"} {
		var result nodeSyncResult
		nodeSyncRequest(t, e.h.nodeUpdate, nodeSyncBody(name, "192.0.2.1"), &result)
		if result.TunnelsUpdated != 0 || result.RulesUpdated != 0 || result.EntryAddressesUpdated != 0 || len(result.Failures) != 0 {
			t.Fatalf("name/unchanged edit propagated: %+v", result)
		}
	}
	var inIP string
	if err := e.r.DB().Raw(`SELECT in_ip FROM tunnel WHERE id = 25`).Scan(&inIP).Error; err != nil || inIP != "192.0.2.99" {
		t.Fatalf("stale unchanged ingress = %q, error %v", inIP, err)
	}
}

func TestNodeUpdateOfflineAndRemoteOnlyRewriteAddresses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		isRemote int
	}{{"offline", 0, 0}, {"remote", 1, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newNodeSyncEnv(t)
			e.exec(`UPDATE node SET status = ?, is_remote = ? WHERE id = 23`, tc.status, tc.isRemote)
			e.h.wsServer = nil
			body := nodeSyncBody("entry", "192.0.2.2")
			body["tcpListenAddr"] = "0.0.0.0"
			var result nodeSyncResult
			nodeSyncRequest(t, e.h.nodeUpdate, body, &result)
			if result.EntryAddressesUpdated != 1 || len(result.Failures) != 0 {
				t.Fatalf("sync result = %+v", result)
			}
		})
	}
}

func TestNodeUpdateListenFailureIsReportedOnceAndSaveSucceeds(t *testing.T) {
	e := newNodeSyncEnv(t)
	body := nodeSyncBody("entry", "192.0.2.1")
	body["tcpListenAddr"] = "0.0.0.0"
	var result nodeSyncResult
	nodeSyncRequest(t, e.h.nodeUpdate, body, &result)
	if len(result.Failures) != 1 || result.Failures[0].Type != "rule" || result.Failures[0].ID != 42 || result.Failures[0].Name != "f" || result.Failures[0].Reason == "" {
		t.Fatalf("expected one rule failure without duplicate redeploy: %+v", result)
	}
	if result.EntryAddressesUpdated != 0 || result.TunnelsUpdated != 1 || result.RulesUpdated != 0 {
		t.Fatalf("sync counts = %+v", result)
	}
	node, err := e.r.GetNodeByID(23)
	if err != nil || node.TCPListenAddr != "0.0.0.0" {
		t.Fatalf("saved node = %+v, error %v", node, err)
	}
}

func TestNodeSyncAddressAndListenClassification(t *testing.T) {
	base := model.Node{ServerIP: "192.0.2.1", TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	for _, tc := range []struct {
		name    string
		change  func(*model.Node)
		address bool
		listen  bool
	}{
		{"server", func(n *model.Node) { n.ServerIP = "192.0.2.2" }, true, false},
		{"v4", func(n *model.Node) { n.ServerIPV4 = sql.NullString{String: "192.0.2.2", Valid: true} }, true, false},
		{"v6", func(n *model.Node) { n.ServerIPV6 = sql.NullString{String: "2001:db8::1", Valid: true} }, true, false},
		{"lan", func(n *model.Node) { n.IntranetIP = sql.NullString{String: "10.0.0.1", Valid: true} }, true, false},
		{"extra", func(n *model.Node) { n.ExtraIPs = sql.NullString{String: "10.0.0.2", Valid: true} }, true, false},
		{"tcp", func(n *model.Node) { n.TCPListenAddr = "0.0.0.0" }, false, true},
		{"udp", func(n *model.Node) { n.UDPListenAddr = "0.0.0.0" }, false, true},
		{"interface", func(n *model.Node) { n.InterfaceName = sql.NullString{String: "eth0", Valid: true} }, false, true},
		{"metadata", func(n *model.Node) {
			n.Name = "new"
			n.Port = "5000-6000"
			n.Inx = 9
			n.Remark.String = "remark"
			n.ExpiryTime.Int64 = 9
			n.RenewalCycle.String = "month"
			n.GroupID.Int64 = 2
		}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated := base
			tc.change(&updated)
			if nodeAddressChanged(&base, &updated) != tc.address || nodeListenChanged(&base, &updated) != tc.listen {
				t.Fatalf("wrong propagation classification for %s", tc.name)
			}
		})
	}
}

func TestNodeSyncDialFamilyRemovalKeepsPreference(t *testing.T) {
	from := &nodeRecord{Name: "entry", ServerIPv4: "192.0.2.1", ServerIPv6: "2001:db8::1"}
	to := &nodeRecord{Name: "exit", ServerIPv4: "192.0.2.2"}
	host, family, err := selectTunnelDialHost(from, to, "auto", "v6")
	if err != nil || host != "192.0.2.2" || family != "v4" {
		t.Fatalf("existing v6 preference fallback changed: %s %s %v", host, family, err)
	}
	from.ServerIPv4 = ""
	_, _, err = selectTunnelDialHost(from, to, "auto", "v6")
	if err == nil || !strings.Contains(err.Error(), "节点链路不兼容") || !strings.Contains(err.Error(), "exit") {
		t.Fatalf("missing clear incompatible-family failure: %v", err)
	}
}

func TestNodeUpdateIncompatibleDialTargetReportsTunnelFailure(t *testing.T) {
	e := newNodeSyncEnv(t)
	e.exec(`UPDATE node SET server_ip = '2001:db8::1', server_ip_v4 = NULL, server_ip_v6 = '2001:db8::1' WHERE id = 23`)
	e.addNode(24, "exit")
	e.exec(`UPDATE node SET server_ip = '2001:db8::2', server_ip_v6 = '2001:db8::2' WHERE id = 24`)
	e.exec(`UPDATE tunnel SET type = 2 WHERE id = 25`)
	e.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, protocol, port, connect_ip_type) VALUES(25, '3', 24, 1, 'tls', 1500, 'v6')`)
	body := nodeSyncBody("exit", "192.0.2.2")
	body["id"] = 24
	var result nodeSyncResult
	nodeSyncRequest(t, e.h.nodeUpdate, body, &result)
	if len(result.Failures) != 1 || result.Failures[0].Type != "tunnel" || result.Failures[0].Name != "entry tunnel" || !strings.Contains(result.Failures[0].Reason, "节点链路不兼容") {
		t.Fatalf("dial failure not reported clearly: %+v", result)
	}
	var connectType string
	if err := e.r.DB().Raw(`SELECT connect_ip_type FROM chain_tunnel WHERE tunnel_id = 25 AND chain_type = '3'`).Scan(&connectType).Error; err != nil || connectType != "v6" {
		t.Fatalf("stored connect type changed: %q (%v)", connectType, err)
	}
}

func TestNodeUpdateReappliesActiveWGPathWithNewPeerEndpoint(t *testing.T) {
	e := newNodeSyncEnv(t)
	e.addNode(24, "peer")
	for _, id := range []int64{23, 24} {
		if err := e.r.SaveWGNodeIdentity(&model.WGNodeIdentity{NodeID: id, PrivateKeyEncrypted: "private", PublicKey: "public"}); err != nil {
			t.Fatal(err)
		}
	}
	path := model.PathTunnel{Name: "wg path", Status: "active"}
	segments := []model.PathSegment{{Sequence: 1, FromNodeID: 23, ToNodeID: 24, TunnelIPFrom: "10.88.1.1", TunnelIPTo: "10.88.1.2", ListenPort: 51820}}
	if err := e.r.CreatePathTunnel(&path, segments, nil); err != nil {
		t.Fatal(err)
	}
	pending := model.PathTunnel{Name: "pending path", Status: "pending"}
	segments[0].ID = 0
	if err := e.r.CreatePathTunnel(&pending, segments, nil); err != nil {
		t.Fatal(err)
	}
	var result nodeSyncResult
	nodeSyncRequest(t, e.h.nodeUpdate, nodeSyncBody("entry", "192.0.2.2"), &result)
	if len(result.Failures) != 1 || result.Failures[0].Type != "path" || result.Failures[0].ID != path.ID {
		t.Fatalf("WG path apply attempt missing: %+v", result)
	}
	detail, err := e.r.GetPathTunnelDetail(path.ID)
	if err != nil || detail.Runtime == nil || detail.Path.Status != "failed" {
		t.Fatalf("WG path was not applied: %+v (%v)", detail, err)
	}
	plans, _, err := e.h.buildWGPathPlans(detail)
	if err != nil || plans[24].Peers[0].Endpoint != "192.0.2.2:51820" {
		t.Fatalf("new WG peer endpoint missing: %+v (%v)", plans, err)
	}
	detail, err = e.r.GetPathTunnelDetail(pending.ID)
	if err != nil || detail.Runtime != nil || detail.Path.Status != "pending" {
		t.Fatalf("pending WG path unexpectedly activated: %+v (%v)", detail, err)
	}
}

func TestNodeSyncJobsBoundedConcurrency(t *testing.T) {
	var active, peak, completed atomic.Int32
	jobs := make([]func(), 20)
	for i := range jobs {
		jobs[i] = func() {
			n := active.Add(1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			completed.Add(1)
		}
	}
	runNodeSyncJobs(jobs)
	if peak.Load() > 4 || completed.Load() != 20 {
		t.Fatalf("peak = %d, completed = %d", peak.Load(), completed.Load())
	}
}
