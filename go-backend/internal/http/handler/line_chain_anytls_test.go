package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/auth"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/sublink"
)

// Plan 250: 「我自己的节点」 with an AnyTLS inbound on the exits (Surge users).

func anytlsChainEnv(t *testing.T) (*flowTestEnv, *chainAgent) {
	t.Helper()
	e, a := newChainEnv(t)
	e.exec(`UPDATE node SET landing_chain = 2 WHERE id IN (3, 4, 6)`)
	return e, a
}

func chainLinks(t *testing.T, e *flowTestEnv, forwardID int64) []map[string]interface{} {
	t.Helper()
	raw, _ := json.Marshal(map[string]interface{}{"forwardId": forwardID})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	req = req.WithContext(context.WithValue(req.Context(), middleware.ClaimsContextKey, auth.Claims{Sub: "7", RoleID: 1}))
	rec := httptest.NewRecorder()
	e.h.forwardChainLinks(rec, req)
	var out struct {
		Code int                      `json:"code"`
		Data []map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Code != 0 {
		t.Fatalf("links: %s", rec.Body.String())
	}
	return out.Data
}

func createAnyTLSChain(t *testing.T, e *flowTestEnv, tunnelID int64, name, link, key string) *model.ForwardChain {
	t.Helper()
	body := chainCreateBody(tunnelID, name, link, key)
	body["chainInbound"] = "anytls"
	code, msg, data := callAs(t, e.h.forwardCreate, 7, 1, body)
	if code != 0 {
		t.Fatalf("anytls chain create: %d %s", code, msg)
	}
	e.h.chains.wg.Wait()
	c, err := e.r.GetForwardChain(asInt64(data["chainId"], 0))
	if err != nil || c == nil || c.State != model.ChainStateActive || c.LastError != "" {
		t.Fatalf("anytls chain: %+v %v", c, err)
	}
	return c
}

func TestChainAnyTLSCreateLinksAndSubscription(t *testing.T) {
	e, a := anytlsChainEnv(t)
	c := createAnyTLSChain(t, e, 10, "surge-node", testSSLink, "key-anytls-01")
	if c.InboundProtocol != model.ChainInboundAnyTLS || len(c.Password) != 24 || len(c.CertSHA256) != 64 || c.TLSServerName == "" || c.TLSKeyPEM == "" || c.PrevInbound != "" {
		t.Fatalf("anytls chain fields: %+v", c)
	}
	// The certificate: self-signed for the SNI, pinned by its DER SHA-256.
	block, _ := pem.Decode([]byte(c.TLSCertPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	sum := sha256.Sum256(block.Bytes)
	if err != nil || hex.EncodeToString(sum[:]) != c.CertSHA256 || cert.VerifyHostname(c.TLSServerName) != nil {
		t.Fatalf("certificate: %v pin %s sni %s", err, c.CertSHA256, c.TLSServerName)
	}
	f, _ := e.h.getForwardRecord(c.ForwardID)
	if f.RemoteAddr != chainTarget(c.Port) || f.LandingNodeID != 0 {
		t.Fatalf("rule target %+v", f)
	}
	// Both exits run the identical loopback AnyTLS copy, no Reality / guard.
	var first string
	for _, nodeID := range []int64{3, 4} {
		nodes := a.chainNodes(nodeID)
		if len(nodes) != 1 {
			t.Fatalf("exit %d copies %v", nodeID, nodes)
		}
		n := nodes[0]
		any, _ := n["anytls"].(map[string]interface{})
		if n["protocol"] != "anytls" || n["listen"] != "127.0.0.1" || asInt(n["port"], 0) != c.Port || n["vless"] != nil || n["guard_port"] != nil ||
			any["password"] != c.Password || any["certificate_pem"] != c.TLSCertPEM || any["key_pem"] != c.TLSKeyPEM {
			t.Fatalf("exit %d copy %v", nodeID, n)
		}
		raw, _ := json.Marshal(any)
		if first == "" {
			first = string(raw)
		} else if first != string(raw) {
			t.Fatal("exit copies differ")
		}
	}
	// 复制链接: anytls:// + pinned Clash + Surge line.
	port := mustQueryInt(t, e.r, "SELECT port FROM forward_port WHERE forward_id = ?", c.ForwardID)
	links := chainLinks(t, e, c.ForwardID)
	if len(links) != 1 {
		t.Fatalf("links %v", links)
	}
	l := links[0]
	wantSurge := fmt.Sprintf(`surge-node = anytls, 192.0.2.1, %d, password="%s", sni=%s, server-cert-fingerprint-sha256=%s`, port, c.Password, c.TLSServerName, c.CertSHA256)
	if !strings.HasPrefix(asString(l["url"]), fmt.Sprintf("anytls://%s@192.0.2.1:%d/?", c.Password, port)) || !strings.Contains(asString(l["url"]), "sni="+c.TLSServerName) ||
		!strings.Contains(asString(l["clash"]), `fingerprint: "`+c.CertSHA256+`"`) || !strings.Contains(asString(l["clash"]), "type: anytls") ||
		asString(l["surge"]) != wantSurge || l["inboundProtocol"] != "anytls" {
		t.Fatalf("anytls link %v\nwant surge %s", l, wantSurge)
	}
	// Status + rule list carry the protocol.
	if _, _, data := callAs(t, e.h.forwardChainStatus, 7, 1, map[string]interface{}{"forwardId": c.ForwardID}); data["inboundProtocol"] != "anytls" {
		t.Fatalf("status %v", data)
	}
	items := []map[string]interface{}{{"id": c.ForwardID, "tunnelId": int64(10)}}
	if bad := e.h.decorateForwardChains(items, false); bad != 0 || items[0]["chain"].(map[string]interface{})["inboundProtocol"] != "anytls" {
		t.Fatalf("decorated %v (%d non-compliant)", items, bad)
	}
	// Subscription: every format emits the pinned AnyTLS node (Surge too).
	data, err := e.r.SubPanelLinks(7, nil, nil, false)
	if err != nil || len(data.Links) != 1 || !data.Links[0].Chain || data.Links[0].Node.Extra == nil {
		t.Fatalf("sub links %+v %v", data, err)
	}
	ex := data.Links[0].Node.Extra
	if ex.Type != "anytls" || ex.Password != c.Password || ex.CertSHA256 != c.CertSHA256 || ex.CertPEM != c.TLSCertPEM || ex.SNI != c.TLSServerName || !ex.UDP || ex.Server != "192.0.2.1" || ex.Port != port {
		t.Fatalf("sub extra %+v", ex)
	}
	nodes := []sublink.Node{data.Links[0].Node}
	sublink.FinalizeNames(nodes)
	render := func(b []byte, n int) string {
		if n != 1 {
			t.Fatalf("rendered %d nodes:\n%s", n, b)
		}
		return string(b)
	}
	checks := map[string][2]string{
		"clash":   {render(sublink.Clash(nodes, sublink.ModeCNDirect, false)), "fingerprint: " + c.CertSHA256},
		"singbox": {render(sublink.SingBox(nodes, sublink.ModeCNDirect, "https://p.example.com")), `"certificate":`},
		"surge":   {render(sublink.Surge(nodes, sublink.ModeCNDirect, false)), fmt.Sprintf("anytls, 192.0.2.1, %d, password=\"%s\", sni=%s, server-cert-fingerprint-sha256=%s", port, c.Password, c.TLSServerName, c.CertSHA256)},
		"surfb":   {render(sublink.Surge(nodes, sublink.ModeCNDirect, true)), "server-cert-fingerprint-sha256=" + c.CertSHA256},
		"loon":    {render(sublink.Loon(nodes)), c.CertSHA256},
		"qx":      {render(sublink.QX(nodes)), c.CertSHA256},
	}
	for name, v := range checks {
		if !strings.Contains(v[0], v[1]) || !strings.Contains(strings.ToLower(v[0]), "anytls") {
			t.Errorf("%s output lacks the pinned AnyTLS node (%q):\n%s", name, v[1], v[0])
		}
	}
	b64, n := sublink.Base64(nodes)
	plain, _ := base64.StdEncoding.DecodeString(string(b64))
	if n != 1 || !strings.HasPrefix(string(plain), "anytls://"+c.Password+"@192.0.2.1:") {
		t.Fatalf("base64 %q", plain)
	}
	// A Reality chain to the same node may coexist; a 2nd AnyTLS one may not.
	reality := createChain(t, e, 10, "android", testSSLink, "key-anytls-02")
	if reality.ChainInbound() != model.ChainInboundReality || reality.InboundProtocol != model.ChainInboundReality || reality.Password != "" {
		t.Fatalf("reality chain %+v", reality)
	}
	body := chainCreateBody(10, "dup", testSSLink, "key-anytls-03")
	body["chainInbound"] = "anytls"
	if code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, body); code == 0 || !strings.Contains(msg, "surge-node") {
		t.Fatalf("duplicate anytls chain: %d %s", code, msg)
	}
	for _, bad := range []string{"snell", "ss2022", "mieru", "hysteria2"} {
		body := chainCreateBody(10, "bad", "socks5://9.9.9.9:1080", "key-anytls-bad-"+bad)
		body["chainInbound"] = bad
		if code, msg, _ := callAs(t, e.h.forwardCreate, 1, 0, body); code == 0 || msg != repo.ChainInboundMsg {
			t.Fatalf("chain inbound %s: %d %s", bad, code, msg)
		}
	}
}

// Exits on agents before landing_chain 2 cannot run AnyTLS: the option is
// shown disabled with the reason, a create is refused, and an AnyTLS copy is
// never sent to such an agent (it would reject the whole desired state).
func TestChainAnyTLSOldAgentGated(t *testing.T) {
	e, a := newChainEnv(t) // exits report landing_chain 1
	items := []map[string]interface{}{{"id": int64(10)}, {"id": int64(30)}}
	e.h.decorateTunnelLineInfo(items)
	if items[0]["chainAvailable"] != true || items[0]["chainAnyTLSAvailable"] != false || !strings.Contains(asString(items[0]["chainAnyTLSUnavailableReason"]), "版本过低") {
		t.Fatalf("tunnel 10 %v", items[0])
	}
	if items[1]["chainAvailable"] != false || items[1]["chainAnyTLSAvailable"] != nil {
		t.Fatalf("normal tunnel %v", items[1])
	}
	body := chainCreateBody(10, "old", testSSLink, "key-anytls-old1")
	body["chainInbound"] = "anytls"
	if code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, body); code == 0 || !strings.Contains(msg, "升级后才能使用 AnyTLS") {
		t.Fatalf("anytls on old exits: %d %s", code, msg)
	}
	c := createChain(t, e, 10, "reality", testSSLink, "key-anytls-old2")
	// An AnyTLS chain row on an old exit (e.g. agent rolled back): withheld.
	up := mustUpstream(t, "socks5://9.9.9.9:1080")
	x, _, err := e.r.CreateForwardChain(repo.ChainCreateInput{UserID: 7, TunnelID: 10, Upstream: up, ExitNodeIDs: []int64{3, 4}, InFlightAfter: time.Now().UnixMilli(), InboundProtocol: "anytls"})
	if err != nil {
		t.Fatal(err)
	}
	servers, _ := e.r.ChainServerIDs([]int64{x.ID})
	for _, sid := range servers {
		if _, err := e.h.syncLandingServer(sid); err != nil {
			t.Fatal(err)
		}
		d, err := e.r.LandingEgressData(sid)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.ChainTags) != 1 {
			t.Fatalf("egress chain tags %v", d.ChainTags)
		}
	}
	for _, nodeID := range []int64{3, 4} {
		nodes := a.chainNodes(nodeID)
		if len(nodes) != 1 || nodes[0]["protocol"] != "vless_reality_vision" || asInt(nodes[0]["port"], 0) != c.Port {
			t.Fatalf("old exit %d got %v", nodeID, nodes)
		}
	}
	// After the OTA (landing_chain 2) the copy is sent.
	e.exec(`UPDATE node SET landing_chain = 2 WHERE id IN (3, 4)`)
	for _, sid := range servers {
		if _, err := e.h.syncLandingServer(sid); err != nil {
			t.Fatal(err)
		}
	}
	if nodes := a.chainNodes(3); len(nodes) != 2 {
		t.Fatalf("upgraded exit copies %v", nodes)
	}
	// A tunnel blocking TLS refuses AnyTLS like Reality (plan 224).
	e.exec("UPDATE tunnel SET tls = 1 WHERE id = 20")
	e.exec(`UPDATE node SET landing_chain = 2 WHERE id = 6`)
	body = chainCreateBody(20, "tls", testSSLink, "key-anytls-tls1")
	body["chainInbound"] = "anytls"
	if code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, body); code == 0 || msg != repo.ChainTLSBlockedMsg {
		t.Fatalf("anytls on TLS-blocking tunnel: %d %s", code, msg)
	}
}

// Switching an existing chain rule's protocol keeps the rule, its port and
// target; a failure rolls back to the old inbound; the reconciler reverts
// an interrupted switch.
func TestChainAnyTLSProtocolSwitchAndRevert(t *testing.T) {
	e, a := anytlsChainEnv(t)
	c := createChain(t, e, 10, "switch", testSSLink, "key-switch-0001")
	update := func(extra map[string]interface{}) (int, string, map[string]interface{}) {
		body := map[string]interface{}{"id": c.ForwardID, "name": "switch", "tunnelId": 10, "targetMode": "chain"}
		for k, v := range extra {
			body[k] = v
		}
		return callAs(t, e.h.forwardUpdate, 7, 1, body)
	}
	// Same protocol = plain edit (no operation).
	if code, msg, data := update(map[string]interface{}{"chainInbound": "vless_reality_vision"}); code != 0 || data["op"] != nil {
		t.Fatalf("same protocol: %d %s %v", code, msg, data)
	}
	code, msg, data := update(map[string]interface{}{"chainInbound": "anytls"})
	if code != 0 || data["op"] != "relink" {
		t.Fatalf("switch start: %d %s %v", code, msg, data)
	}
	e.h.chains.wg.Wait()
	after, _ := e.r.GetForwardChain(c.ID)
	f, _ := e.h.getForwardRecord(c.ForwardID)
	if after.ChainInbound() != model.ChainInboundAnyTLS || after.PrevInbound != "" || after.LastError != "" || after.Port != c.Port || after.UUID != c.UUID || f.RemoteAddr != chainTarget(c.Port) || after.UpstreamHash != c.UpstreamHash {
		t.Fatalf("switch result %+v forward %+v", after, f)
	}
	for _, nodeID := range []int64{3, 4} {
		if n := a.chainNodes(nodeID); len(n) != 1 || n[0]["protocol"] != "anytls" {
			t.Fatalf("exit %d after switch %v", nodeID, n)
		}
	}
	if !strings.HasPrefix(asString(chainLinks(t, e, c.ForwardID)[0]["url"]), "anytls://") {
		t.Fatal("link did not switch")
	}
	// A failing switch back reverts to AnyTLS with the same credentials.
	a.setApplyErr(func(nodeID int64) error {
		if nodeID == 4 {
			return errors.New("injected")
		}
		return nil
	})
	if code, msg, _ := update(map[string]interface{}{"chainInbound": "vless_reality_vision"}); code != 0 {
		t.Fatalf("switch 2 start: %s", msg)
	}
	waitDriverWithDeadline(t, e, c.ID)
	a.setApplyErr(nil)
	reverted, _ := e.r.GetForwardChain(c.ID)
	if reverted.ChainInbound() != model.ChainInboundAnyTLS || reverted.Password != after.Password || reverted.CertSHA256 != after.CertSHA256 || reverted.PrevInbound != "" || reverted.LastError == "" || reverted.OpStartedAt != 0 {
		t.Fatalf("failed switch not reverted: %+v", reverted)
	}
	// Switch + new link together, then back to Reality: the original Reality
	// credentials (UUID / key) are unchanged.
	if code, msg, _ := update(map[string]interface{}{"chainInbound": "vless_reality_vision", "chainLink": "socks5://9.9.9.9:1080"}); code != 0 {
		t.Fatalf("switch 3: %s", msg)
	}
	e.h.chains.wg.Wait()
	back, _ := e.r.GetForwardChain(c.ID)
	if back.ChainInbound() != model.ChainInboundReality || back.Password != "" || back.TLSKeyPEM != "" || back.UUID != c.UUID || back.UpstreamProtocol != "SOCKS5" || back.PrevUpstream != "" || back.PrevInbound != "" {
		t.Fatalf("back to reality %+v", back)
	}
	if n := a.chainNodes(3); len(n) != 1 || n[0]["protocol"] != "vless_reality_vision" || n[0]["vless"] == nil {
		t.Fatalf("exit after switch back %v", n)
	}
	// Interrupted switch (panel restart): the reconciler restores Reality.
	if _, err := e.r.StartChainOp(c.ID, "relink"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.r.BeginChainInboundChange(c.ID, model.ChainInboundAnyTLS); err != nil {
		t.Fatal(err)
	}
	e.exec("UPDATE forward_chain SET op_started_at = ? WHERE id = ?", time.Now().Add(-time.Hour).UnixMilli(), c.ID)
	e.h.reconcileChains(time.Now())
	e.h.chains.wg.Wait()
	rec, _ := e.r.GetForwardChain(c.ID)
	if rec.ChainInbound() != model.ChainInboundReality || rec.PrevInbound != "" || rec.OpStartedAt != 0 || rec.Password != "" {
		t.Fatalf("reconciler did not revert the switch: %+v", rec)
	}
	// Convert a normal 线路机 rule straight into an AnyTLS chain (attach).
	e.addForward(810, 7, 10)
	e.exec("INSERT INTO forward_port(forward_id, node_id, port) VALUES(810, 1, 1810)")
	code, msg, data = callAs(t, e.h.forwardUpdate, 7, 1, map[string]interface{}{"id": 810, "name": "attach", "tunnelId": 10, "targetMode": "chain", "chainLink": "socks5://9.9.9.10:1080", "chainInbound": "anytls"})
	if code != 0 || data["op"] != "attach" {
		t.Fatalf("attach anytls: %d %s %v", code, msg, data)
	}
	e.h.chains.wg.Wait()
	att, _ := e.r.GetForwardChainByForward(810)
	if att == nil || att.State != model.ChainStateActive || att.ChainInbound() != model.ChainInboundAnyTLS {
		t.Fatalf("attached %+v", att)
	}
}

// 线路机 compliance: AnyTLS landing targets are compliant, Snell / SS2022 not.
func TestLineMachineAnyTLSCompliance(t *testing.T) {
	e, _ := newChainEnv(t)
	e.exec(`INSERT INTO landing_server(id, source, node_id, name, public_host, status, revision, created_time, updated_time) VALUES (50, 'shared', 2, 'land', '192.0.2.2', 'active', 1, 1, 1)`)
	e.exec(`INSERT INTO landing_node(id, user_id, landing_server_id, name, protocol, port, inbound_tag, status, created_time, updated_time) VALUES
		(90, 7, 50, 'r', 'vless_reality_vision', 31000, 'flvxl-90', 'active', 1, 1), (91, 7, 50, 'a', 'anytls', 31001, 'flvxl-91', 'active', 1, 1),
		(92, 7, 50, 's', 'snell', 31002, 'flvxl-92', 'active', 1, 1), (93, 7, 50, 'ss', 'ss2022', 31003, 'flvxl-93', 'active', 1, 1)`)
	items := []map[string]interface{}{}
	for i, landing := range []int64{90, 91, 92, 93} {
		items = append(items, map[string]interface{}{"id": int64(900 + i), "tunnelId": int64(10), "landingNodeId": landing})
	}
	if bad := e.h.decorateForwardChains(items, true); bad != 2 {
		t.Fatalf("non-compliant = %d", bad)
	}
	for i, want := range []bool{false, false, true, true} {
		if (items[i]["lineNonCompliant"] == true) != want {
			t.Fatalf("item %d %v", i, items[i])
		}
	}
	// An admin cannot confirm a landing-node target (its protocol is known).
	e.addForward(905, 7, 10)
	e.exec("UPDATE forward SET landing_node_id = 92 WHERE id = 905")
	if err := e.h.setLineComplianceAck(905, true); err == nil || !strings.Contains(err.Error(), "落地节点") {
		t.Fatalf("ack on snell landing: %v", err)
	}
}
