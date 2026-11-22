package landing

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
)

// Plan 291: landings moved over from mmwx keep their credentials — a
// single-key SS2022 server and a VLESS Reality node whose clients use no flow.
func TestImportedSS2022SingleKey(t *testing.T) {
	n := testNode(t, 7)
	n.SS2022.UserKey = ""
	in, err := n.inbound()
	if err != nil {
		t.Fatal(err)
	}
	s := in["settings"].(map[string]any)
	if s["password"] != n.SS2022.ServerKey || s["email"] != n.InboundTag || s["method"] != SS2022Method || s["network"] != "tcp,udp" {
		t.Fatalf("single-key settings wrong: %v", s)
	}
	if _, ok := s["clients"]; ok {
		t.Fatal("single-key server must not list clients (they would require EIH)")
	}
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{n}}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "bad key", base64.StdEncoding.EncodeToString(make([]byte, 32))} {
		bad := testNode(t, 7)
		bad.SS2022.UserKey = ""
		bad.SS2022.ServerKey = key
		if _, err := bad.inbound(); err == nil {
			t.Fatalf("invalid single server key %q accepted", key)
		}
	}
	// Both keys: rendering unchanged (multi-user, one client).
	m := testNode(t, 8)
	in, err = m.inbound()
	if err != nil {
		t.Fatal(err)
	}
	s = in["settings"].(map[string]any)
	clients := s["clients"].([]any)
	if len(clients) != 1 || clients[0].(map[string]any)["password"] != m.SS2022.UserKey || s["password"] != m.SS2022.ServerKey {
		t.Fatalf("two-key rendering changed: %v", s)
	}
	if _, ok := s["email"]; ok {
		t.Fatal("two-key server got a top-level email")
	}
}

func importedRealityNode(t *testing.T, id int64, flow string) Node {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	n := testNode(t, id)
	n.Protocol, n.SS2022 = "vless_reality_vision", nil
	n.VLESS = &VLESS{UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Flow: flow, Reality: Reality{Dest: "www.lovelive-anime.jp:443", ServerNames: []string{"www.lovelive-anime.jp"}, PrivateKey: base64.RawURLEncoding.EncodeToString(key.Bytes()), ShortIDs: []string{""}}}
	return n
}

func TestImportedVLESSWithoutFlow(t *testing.T) {
	vision := importedRealityNode(t, 9, "xtls-rprx-vision")
	in, err := vision.inbound()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(in["settings"])
	if string(got) != `{"clients":[{"email":"flvxl-9","flow":"xtls-rprx-vision","id":"d77c8b43-40ed-42cd-9150-f3f47d0f534d"}],"decryption":"none"}` {
		t.Fatalf("vision rendering changed: %s", got)
	}
	plain := importedRealityNode(t, 10, "")
	in, err = plain.inbound()
	if err != nil {
		t.Fatal(err)
	}
	got, _ = json.Marshal(in["settings"])
	if string(got) != `{"clients":[{"email":"flvxl-10","id":"d77c8b43-40ed-42cd-9150-f3f47d0f534d"}],"decryption":"none"}` {
		t.Fatalf("no-flow rendering wrong: %s", got)
	}
	plain.GuardPort = 40123
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{plain}}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	bad := importedRealityNode(t, 11, "xtls-rprx-direct")
	if _, err := bad.inbound(); err == nil {
		t.Fatal("unknown flow accepted")
	}
}
