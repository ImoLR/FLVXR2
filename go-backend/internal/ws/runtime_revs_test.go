package ws

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-backend/internal/security"
)

func serviceList() []map[string]interface{} {
	return []map[string]interface{}{
		{"name": "100_7_70_tcp", "addr": "0.0.0.0:1501", "handler": map[string]interface{}{"type": "tcp", "chain": "chains_10"},
			"forwarder": map[string]interface{}{"selector": map[string]interface{}{"failTimeout": "600s", "maxFails": 1}}},
		{"name": "100_7_70_udp", "addr": "0.0.0.0:1501", "handler": map[string]interface{}{"type": "udp"}},
	}
}

func TestRuntimeRevTaggingMatchesExpected(t *testing.T) {
	data := serviceList()
	expected := ExpectedRuntimeRevs("UpdateService", data)
	if len(expected) != 2 || expected["service:100_7_70_tcp"] == "" || expected["service:100_7_70_tcp"] == expected["service:100_7_70_udp"] {
		t.Fatalf("expected revisions: %v", expected)
	}
	sent, ok := annotateRuntimeRevs("UpdateService", data).([]interface{})
	if !ok || len(sent) != 2 {
		t.Fatalf("annotated payload: %#v", sent)
	}
	for _, raw := range sent {
		m := raw.(map[string]interface{})
		if m[RuntimeRevKey] != expected["service:"+m["name"].(string)] {
			t.Fatalf("sent revision %v != expected %v", m[RuntimeRevKey], expected)
		}
	}
	if _, tagged := data[0][RuntimeRevKey]; tagged {
		t.Fatal("the caller's payload must not be modified")
	}
	// AddService of the same objects records the same revisions.
	if add := ExpectedRuntimeRevs("AddService", data); add["service:100_7_70_tcp"] != expected["service:100_7_70_tcp"] {
		t.Fatalf("AddService %v vs UpdateService %v", add, expected)
	}
	// Stable over re-encoding, changed by any payload change.
	again := ExpectedRuntimeRevs("UpdateService", serviceList())
	if again["service:100_7_70_tcp"] != expected["service:100_7_70_tcp"] {
		t.Fatal("revision not stable")
	}
	changed := serviceList()
	changed[0]["addr"] = "0.0.0.0:1502"
	if ExpectedRuntimeRevs("UpdateService", changed)["service:100_7_70_tcp"] == expected["service:100_7_70_tcp"] {
		t.Fatal("changed payload kept its revision")
	}
}

func TestRuntimeRevChainsLimitersAndNft(t *testing.T) {
	chain := map[string]interface{}{"name": "chains_10", "hops": []map[string]interface{}{{"name": "hop_10", "nodes": []map[string]interface{}{{"name": "node_1", "addr": "192.0.2.2:1202"}}}}}
	add := ExpectedRuntimeRevs("AddChains", chain)
	upd := ExpectedRuntimeRevs("UpdateChains", map[string]interface{}{"chain": "chains_10", "data": chain})
	if add["chain:chains_10"] == "" || add["chain:chains_10"] != upd["chain:chains_10"] {
		t.Fatalf("chain add %v vs update %v", add, upd)
	}
	limiter := map[string]interface{}{"name": "forward_100_speed", "limits": []string{"$ 10.0MB 10.0MB"}}
	ladd := ExpectedRuntimeRevs("AddLimiters", limiter)
	lupd := ExpectedRuntimeRevs("UpdateLimiters", map[string]interface{}{"limiter": "forward_100_speed", "data": limiter})
	if ladd["limiter:forward_100_speed"] == "" || ladd["limiter:forward_100_speed"] != lupd["limiter:forward_100_speed"] {
		t.Fatalf("limiter add %v vs update %v", ladd, lupd)
	}
	one := map[string]interface{}{"rules": []map[string]interface{}{{"forward_id": 101, "protocol": "tcp", "port": 1601}, {"forward_id": 101, "protocol": "udp", "port": 1601}}}
	if revs := ExpectedRuntimeRevs("AddNftablesRules", one); len(revs) != 1 || revs["nft:101_nft"] == "" {
		t.Fatalf("single forward nft revision: %v", revs)
	}
	two := map[string]interface{}{"rules": []map[string]interface{}{{"forward_id": 101}, {"forward_id": 102}}}
	if revs := ExpectedRuntimeRevs("AddNftablesRules", two); len(revs) != 0 {
		t.Fatalf("multi-forward nft request must carry no revision: %v", revs)
	}
	if g, ok := annotateRuntimeRevs("AddNftablesRules", two).(map[string]interface{}); !ok || g[RuntimeRevKey] != nil {
		t.Fatalf("multi-forward nft request tagged: %v", g)
	}
	del := map[string]interface{}{"services": []string{"100_7_70_tcp"}}
	if out := annotateRuntimeRevs("DeleteService", del); out.(map[string]interface{})["services"] == nil {
		t.Fatal("non-revision commands are passed through")
	}
}

// The command an agent receives carries the revision inside each object.
func TestSendCommandCarriesRuntimeRevisions(t *testing.T) {
	s, _, url := panelFamilyServer(t, "")
	c, _, err := websocket.DefaultDialer.Dial(url+"&runtimeRevs=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	waitFor(t, func() bool { return s.RuntimeRevsCapable(1) })
	go func() { _, _ = s.SendCommand(1, "AddService", serviceList(), 2*time.Second) }()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var payload []byte
	for {
		mt, p, err := c.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if mt == websocket.TextMessage {
			payload = p
			break
		}
	}
	var wrap encryptedMessage
	if err := json.Unmarshal(payload, &wrap); err != nil || !wrap.Encrypted {
		t.Fatalf("command not encrypted: %s", payload)
	}
	aes, _ := security.NewAESCrypto("test-secret")
	plain, err := aes.Decrypt(wrap.Data)
	if err != nil {
		t.Fatal(err)
	}
	var cmd struct {
		Type string                   `json:"type"`
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(plain, &cmd); err != nil || cmd.Type != "AddService" || len(cmd.Data) != 2 {
		t.Fatalf("command: %s", plain)
	}
	want := ExpectedRuntimeRevs("AddService", serviceList())
	for _, item := range cmd.Data {
		if rev, _ := item[RuntimeRevKey].(string); rev == "" || rev != want["service:"+item["name"].(string)] {
			t.Fatalf("object %v carries %v, want %v", item["name"], item[RuntimeRevKey], want)
		}
	}
	if s.CommandsSent(1) != 1 {
		t.Fatalf("commands sent = %d", s.CommandsSent(1))
	}
	if !strings.Contains(string(plain), `"failTimeout":"600s"`) {
		t.Fatalf("payload content changed: %s", plain)
	}
}
