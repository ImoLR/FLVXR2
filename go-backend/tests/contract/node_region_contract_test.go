package contract_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"go-backend/internal/auth"
	"go-backend/internal/store/model"
)

func TestNodeRegionDetectAdminOnlyContract(t *testing.T) {
	const secret = "node-region-contract"
	router, _ := setupContractRouter(t, secret)
	userToken, err := auth.GenerateToken(2, "normal_user", 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	out := requestContractEnvelope(t, router, userToken, "/api/v1/node/detect-region", map[string]string{"ip": "8.8.8.8"})
	if out.Code != 403 {
		t.Fatalf("non-admin detection = %+v", out)
	}
	out = requestContractEnvelope(t, router, mustAdminToken(t, secret), "/api/v1/node/detect-region", map[string]string{"ip": "10.0.0.1"})
	if out.Code != 0 || out.Data.(map[string]interface{})["region"] != "" {
		t.Fatalf("private detection = %+v", out)
	}
}

func TestNodeRegionRoundTripWithoutPropagationContract(t *testing.T) {
	const secret = "node-region-roundtrip"
	router, repository := setupContractRouter(t, secret)
	admin := mustAdminToken(t, secret)
	node := model.Node{ID: 1001, Name: "entry", ServerIP: "192.0.2.1", Port: "1000-2000", Status: 1, TCPListenAddr: "[::]", UDPListenAddr: "[::]"}
	tunnel := model.Tunnel{ID: 1, Name: "tunnel", Type: 2, Status: 1, InIP: sql.NullString{String: "192.0.2.99", Valid: true}}
	for _, row := range []interface{}{&node, &tunnel,
		&model.ChainTunnel{TunnelID: 1, NodeID: 1001, ChainType: "1"},
		&model.ChainTunnel{TunnelID: 1, NodeID: 1001, ChainType: "3"},
		&model.Forward{ID: 1, TunnelID: 1, Status: 1},
		&model.ForwardPort{ForwardID: 1, NodeID: 1001, Port: 1234, InIP: sql.NullString{String: "192.0.2.99", Valid: true}},
	} {
		if err := repository.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"tunnel", "forward_port"} {
		if err := repository.DB().Exec("CREATE TRIGGER reject_region_rewrite_" + table + " BEFORE UPDATE OF in_ip ON " + table + " BEGIN SELECT RAISE(ABORT, 'unexpected rewrite'); END").Error; err != nil {
			t.Fatal(err)
		}
	}
	body := map[string]interface{}{"id": 1001, "name": "entry", "serverIp": "192.0.2.1", "port": "1000-2000", "tcpListenAddr": "[::]", "udpListenAddr": "[::]", "region": " hk ", "regionCity": " 葵涌 "}
	out := requestContractEnvelope(t, router, admin, "/api/v1/node/update", body)
	if out.Code != 0 {
		t.Fatalf("update = %+v", out)
	}
	result := out.Data.(map[string]interface{})
	for _, key := range []string{"entryAddressesUpdated", "tunnelsUpdated", "rulesUpdated"} {
		if result[key] != float64(0) {
			t.Fatalf("region propagated: %+v", result)
		}
	}
	if len(result["failures"].([]interface{})) != 0 {
		t.Fatalf("region redeployed: %+v", result)
	}
	list := requestContractEnvelope(t, router, admin, "/api/v1/node/list", nil)
	if list.Code != 0 {
		t.Fatalf("node list = %+v", list)
	}
	item := mustContractSlice(t, list.Data, "nodes")[0].(map[string]interface{})
	if item["region"] != "HK" || item["regionCity"] != "葵涌" {
		t.Fatalf("region roundtrip = %+v", item)
	}
	if got := mustQueryString(t, repository, "SELECT in_ip FROM tunnel WHERE id = 1"); got != "192.0.2.99" {
		t.Fatalf("tunnel ingress rewritten: %s", got)
	}
	if got := mustQueryString(t, repository, "SELECT in_ip FROM forward_port WHERE forward_id = 1"); got != "192.0.2.99" {
		t.Fatalf("rule ingress rewritten: %s", got)
	}
	// Older clients that omit the new fields must retain an admin override.
	delete(body, "region")
	delete(body, "regionCity")
	out = requestContractEnvelope(t, router, admin, "/api/v1/node/update", body)
	if out.Code != 0 || mustQueryString(t, repository, "SELECT region FROM node WHERE id = 1001") != "HK" {
		t.Fatalf("override lost: %+v", out)
	}
}

func TestTunnelRegionUserPrivacyContract(t *testing.T) {
	const secret = "tunnel-region-privacy"
	router, repository := setupContractRouter(t, secret)
	for _, row := range []interface{}{
		&model.Node{ID: 1001, Name: "SECRET_ENTRY_NODE", ServerIP: "198.51.100.77", Region: "CN", RegionCity: "深圳", Port: "1000-2000"},
		&model.Node{ID: 1002, Name: "SECRET_EXIT_NODE", ServerIP: "198.51.100.88", Region: "HK", Port: "1000-2000"},
		&model.Tunnel{ID: 1, Name: "visible tunnel", Type: 2, Status: 1},
		&model.ChainTunnel{TunnelID: 1, NodeID: 1001, ChainType: "1"},
		&model.ChainTunnel{TunnelID: 1, NodeID: 1002, ChainType: "3"},
		&model.UserTunnel{UserID: 2, TunnelID: 1},
	} {
		if err := repository.DB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	userToken, err := auth.GenerateToken(2, "normal_user", 1, secret)
	if err != nil {
		t.Fatal(err)
	}
	out := requestContractEnvelope(t, router, userToken, "/api/v1/tunnel/user/tunnel", nil)
	if out.Code != 0 {
		t.Fatalf("user tunnel list = %+v", out)
	}
	data, err := json.Marshal(out.Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"SECRET_ENTRY_NODE", "SECRET_EXIT_NODE", "198.51.100.77", "198.51.100.88", `"nodeId"`, `"entryNodes"`, `"node-1001"`, `"id":1001`, `"id":1002`} {
		if strings.Contains(string(data), private) {
			t.Fatalf("user response leaks %q: %s", private, data)
		}
	}
	item := mustContractSlice(t, out.Data, "tunnels")[0].(map[string]interface{})
	group := item["entryGroups"].([]interface{})[0].(map[string]interface{})
	if group["label"] != "深圳" || item["exitRegions"].([]interface{})[0] != "HK" {
		t.Fatalf("group metadata = %s", data)
	}
	admin := mustAdminToken(t, secret)
	out = requestContractEnvelope(t, router, admin, "/api/v1/tunnel/user/tunnel", nil)
	item = mustContractSlice(t, out.Data, "admin tunnels")[0].(map[string]interface{})
	if item["entryGroups"].([]interface{})[0].(map[string]interface{})["label"] != "SECRET_ENTRY_NODE" {
		t.Fatalf("admin entry label = %+v", item)
	}
	out = requestContractEnvelope(t, router, admin, "/api/v1/tunnel/list", nil)
	item = mustContractSlice(t, out.Data, "admin tunnel list")[0].(map[string]interface{})
	entry := item["entryNodes"].([]interface{})[0].(map[string]interface{})
	if entry["id"] != float64(1001) || entry["regionCity"] != "深圳" {
		t.Fatalf("admin entry metadata = %+v", entry)
	}
}
