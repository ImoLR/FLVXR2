package handler

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/ddns"
	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

const (
	ctOld = "240e:b8f:a30:1904:ca:8aff:fec0:2813"
	ctNew = "240e:b8f:72d:5c04:ca:8aff:fec0:2813"
	cuIP  = "2408:820c:750b:dc64:ca:8aff:fec0:2813"
	cuNew = "2408:820c:9999:dc64:ca:8aff:fec0:2813"
	ctDom = "leikwan-hutai-ct.imgamer.top"
	cuDom = "leikwan-hutai-cu.imgamer.top"
)

type ddnsLab struct {
	*flowTestEnv
	cf       *fakeCF
	clock    time.Time
	online   bool
	report   bool
	clientIP string
	mu       sync.Mutex
	commands int
}

// newDDNSLab: node 23 like node 48 (CT main IPv6 + CU extra IP), entry of
// tunnel 25 (both picked), exit of tunnel 26; rule 42 inherits, rule 43
// binds the CU address explicitly.
func newDDNSLab(t *testing.T) *ddnsLab {
	t.Helper()
	l := &ddnsLab{flowTestEnv: newNodeSyncEnv(t), cf: newFakeCF(t), clock: time.Unix(1792800000, 0), online: true, report: true, clientIP: "123.204.3.9"}
	l.exec(`UPDATE node SET name = 'Leikwanhost 沪台IPLC', server_ip = ?, server_ip_v4 = NULL, server_ip_v6 = ?, extra_ips = ? WHERE id = 23`, ctOld, ctOld, `["`+cuIP+`"]`)
	l.exec(`UPDATE tunnel SET in_ip = ? WHERE id = 25`, ctOld+","+cuIP)
	l.addTunnel(26, 1, 1)
	l.exec(`INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, protocol) VALUES(26, '3', 23, 1, 'tls')`)
	l.addForward(43, 7, 25)
	l.exec(`INSERT INTO forward_port(forward_id, node_id, port, in_ip) VALUES(43, 23, 1235, ?)`, cuIP)
	l.exec(`CREATE TABLE ddns_test_rewrites(n INTEGER)`)
	l.exec(`INSERT INTO ddns_test_rewrites VALUES(0)`)
	l.exec(`CREATE TRIGGER ddns_test_count AFTER UPDATE OF in_ip ON tunnel BEGIN UPDATE ddns_test_rewrites SET n = n + 1; END`)
	l.h.wsServer = nil
	l.h.nodeCommandSender = func(int64, string, interface{}, time.Duration) (ws.CommandResult, error) {
		l.mu.Lock()
		l.commands++
		l.mu.Unlock()
		return ws.CommandResult{}, nil
	}
	m := l.h.ddnsState()
	m.newClient = l.cf.client
	m.now = func() time.Time { l.mu.Lock(); defer l.mu.Unlock(); return l.clock }
	m.session = func(int64) (bool, bool, string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.online, l.report, l.clientIP
	}
	if err := l.r.CreateDNSProvider(&model.DNSProvider{Name: "Cloudflare imgamer.top", ProviderType: "cloudflare", Credentials: dnsProviderCredentials("test-token")}); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *ddnsLab) advance(d time.Duration) {
	l.mu.Lock()
	l.clock = l.clock.Add(d)
	l.mu.Unlock()
}

func (l *ddnsLab) commandCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.commands
}

func (l *ddnsLab) rewrites() int {
	var n int
	l.r.DB().Raw(`SELECT n FROM ddns_test_rewrites`).Scan(&n)
	return n
}

func (l *ddnsLab) wait() { l.h.ddnsState().pending.Wait() }

func node48DDNS(extra map[string]interface{}) map[string]interface{} {
	ct := map[string]interface{}{"domain": ctDom, "type": "AAAA", "role": "main", "source": "ct"}
	cu := map[string]interface{}{"domain": cuDom, "type": "AAAA", "role": "extra", "source": "cu"}
	for k, v := range extra {
		ct[k] = v
		cu[k] = v
	}
	return map[string]interface{}{"enabled": true, "providerId": 1, "records": []interface{}{ct, cu}}
}

// node48Body is the full field set the node form sends (stored values).
func (l *ddnsLab) node48Body(ddnsCfg interface{}) map[string]interface{} {
	n, _ := l.r.GetNodeByID(23)
	body := map[string]interface{}{"id": 23, "name": n.Name, "serverIpV4": n.ServerIPV4.String, "serverIpV6": n.ServerIPV6.String,
		"intranetIp": "", "extraIPs": strings.Join(decodeExtras(n.ExtraIPs.String), "\n"), "interfaceName": "", "port": "1000-2000",
		"tcpListenAddr": "[::]", "udpListenAddr": "[::]"}
	if ddnsCfg != nil {
		body["ddns"] = ddnsCfg
	}
	return body
}

func decodeExtras(raw string) []string {
	var out []string
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func (l *ddnsLab) node() *model.Node {
	n, err := l.r.GetNodeByID(23)
	if err != nil || n == nil {
		l.t.Fatalf("node: %v", err)
	}
	return n
}

func (l *ddnsLab) records() map[string]model.NodeDDNSRecord {
	recs, err := l.r.GetNodeDDNSRecords(23)
	if err != nil {
		l.t.Fatal(err)
	}
	out := map[string]model.NodeDDNSRecord{}
	for _, r := range recs {
		out[r.Domain] = r
	}
	return out
}

func (l *ddnsLab) tunnelInIP(id int64) string {
	var v string
	l.r.DB().Raw(`SELECT in_ip FROM tunnel WHERE id = ?`, id).Scan(&v)
	return v
}

func (l *ddnsLab) bindIP() string {
	var v string
	l.r.DB().Raw(`SELECT in_ip FROM forward_port WHERE forward_id = 43`).Scan(&v)
	return v
}

func (l *ddnsLab) agentReports(v6 ...string) {
	l.h.onNodeAddrs(23, ws.NodeAddrs{V4: []string{}, V6: v6})
	l.wait()
}

func TestNodeDDNSNode48EndToEnd(t *testing.T) {
	l := newDDNSLab(t)

	// Save with the agent's addresses unknown yet: pending, nothing switched.
	code, msg, _ := adminPost(t, l.h.nodeUpdate, l.node48Body(node48DDNS(nil)))
	if code != 0 {
		t.Fatalf("save: %d %s", code, msg)
	}
	l.wait()
	if l.cf.count("POST /zones/zone1/dns_records") != 0 || l.node().ServerIPV6.String != ctOld {
		t.Fatal("nothing may be written before the agent reports addresses")
	}
	for _, rec := range l.records() {
		if rec.Status != model.DDNSStatusPending || rec.Applied {
			t.Fatalf("record %s = %+v", rec.Domain, rec)
		}
	}

	// First report (CT prefix already rotated): both records created, node
	// fields switched to the domains once, listen IP kept literal.
	l.agentReports(ctNew, cuIP)
	if got := l.cf.find(ctDom, "AAAA"); len(got) != 1 || got[0].Content != ctNew || got[0].Proxied || got[0].TTL != 120 || got[0].Comment != ddns.RecordComment {
		t.Fatalf("CT record = %+v", got)
	}
	if got := l.cf.find(cuDom, "AAAA"); len(got) != 1 || got[0].Content != cuIP {
		t.Fatalf("CU record = %+v", got)
	}
	n := l.node()
	if n.ServerIPV6.String != ctDom || n.ServerIP != ctDom {
		t.Fatalf("main address = %q / %q", n.ServerIP, n.ServerIPV6.String)
	}
	if extras := decodeExtras(n.ExtraIPs.String); len(extras) != 1 || extras[0] != cuDom {
		t.Fatalf("extra IPs = %q", n.ExtraIPs.String)
	}
	if got := l.tunnelInIP(25); got != ctDom+","+cuDom {
		t.Fatalf("tunnel 25 in_ip = %q", got)
	}
	if got := l.bindIP(); got != cuIP {
		t.Fatalf("listen IP must stay literal, got %q", got)
	}
	// Users see and copy the domains (rule 42 inherits the tunnel entries).
	var forwards struct {
		Items []struct {
			ID   int64  `json:"id"`
			InIP string `json:"inIp"`
		} `json:"items"`
	}
	l.h.wsServer = ws.NewServer(l.r, "test") // the rule list reads live metrics
	nodeSyncRequest(t, l.h.forwardList, map[string]interface{}{}, &forwards)
	l.h.wsServer = nil
	if len(forwards.Items) == 0 {
		t.Fatal("rule list empty")
	}
	for _, f := range forwards.Items {
		if f.ID == 42 && f.InIP != ctDom+":1234,"+cuDom+":1234" {
			t.Fatalf("rule 42 entries = %q", f.InIP)
		}
	}
	for _, rec := range l.records() {
		if rec.Status != model.DDNSStatusSynced || !rec.Applied || rec.TTL != 120 || rec.RecordID == "" {
			t.Fatalf("record %s = %+v", rec.Domain, rec)
		}
	}
	rewritesAfterSwitch, commandsAfterSwitch := l.rewrites(), l.commandCount()
	callsAfterSwitch := l.cf.totalCalls()

	// Same set again (reconnect): no Cloudflare call.
	l.agentReports(ctNew, cuIP)
	if l.cf.totalCalls() != callsAfterSwitch {
		t.Fatalf("unchanged report made %d Cloudflare calls", l.cf.totalCalls()-callsAfterSwitch)
	}

	// Prefix rotation of CT and CU: exactly one PUT per record, node fields
	// and tunnels untouched, nothing redeployed except the bound rule.
	puts := l.cf.count("PUT /zones/zone1/dns_records/:id")
	l.agentReports(ctOld, cuNew)
	if got := l.cf.count("PUT /zones/zone1/dns_records/:id") - puts; got != 2 {
		t.Fatalf("PUTs after rotation = %d, want 2", got)
	}
	if got := l.cf.find(ctDom, "AAAA"); got[0].Content != ctOld {
		t.Fatalf("CT record after rotation = %+v", got)
	}
	if n := l.node(); n.ServerIPV6.String != ctDom || decodeExtras(n.ExtraIPs.String)[0] != cuDom {
		t.Fatal("node address fields changed on an IP rotation")
	}
	if l.rewrites() != rewritesAfterSwitch {
		t.Fatalf("tunnel in_ip rewritten again (%d -> %d)", rewritesAfterSwitch, l.rewrites())
	}
	if got := l.bindIP(); got != cuNew {
		t.Fatalf("listen IP = %q, want the new CU address", got)
	}
	// Only the rule bound to the CU address was redeployed (UpdateService …).
	if extra := l.commandCount() - commandsAfterSwitch; extra == 0 || extra > 3 {
		t.Fatalf("commands after rotation = %d (want only the bound rule)", extra)
	}

	// Node list view: badge synced, token never exposed.
	views, err := l.h.ddnsNodeViews()
	if err != nil || views[23] == nil || views[23].Status != model.DDNSStatusSynced || len(views[23].Records) != 2 {
		t.Fatalf("view = %+v %v", views[23], err)
	}
	raw, _ := json.Marshal(views)
	if strings.Contains(string(raw), "test-token") {
		t.Fatal("token leaked into the node view")
	}

	// A full-field save without `ddns` (old UI / API script sending a raw IP)
	// keeps DDNS on and the domain in the owned field.
	body := l.node48Body(nil)
	body["serverIpV6"] = ctNew
	if code, msg, _ := adminPost(t, l.h.nodeUpdate, body); code != 0 {
		t.Fatalf("save without ddns: %d %s", code, msg)
	}
	if n := l.node(); !n.DDNSEnabled || n.ServerIPV6.String != ctDom || n.ServerIP != ctDom {
		t.Fatalf("owned field overwritten: %+v", n)
	}
	// Deleting extras in the form does not drop the applied CU domain.
	body = l.node48Body(nil)
	body["extraIPs"] = ""
	if code, msg, _ := adminPost(t, l.h.nodeUpdate, body); code != 0 {
		t.Fatalf("save: %d %s", code, msg)
	}
	if extras := decodeExtras(l.node().ExtraIPs.String); len(extras) != 1 || extras[0] != cuDom {
		t.Fatalf("applied extra domain dropped: %v", extras)
	}

	// Turning DDNS off keeps the records and the domains.
	off := node48DDNS(nil)
	off["enabled"] = false
	if code, msg, _ := adminPost(t, l.h.nodeUpdate, l.node48Body(off)); code != 0 {
		t.Fatalf("disable: %d %s", code, msg)
	}
	l.wait()
	if n := l.node(); n.DDNSEnabled || n.ServerIPV6.String != ctDom || len(l.cf.find(ctDom, "AAAA")) != 1 {
		t.Fatal("disable must keep addresses and DNS records")
	}
}

func TestNodeDDNSIdleHourCallsAndBackoff(t *testing.T) {
	l := newDDNSLab(t)
	if code, msg, _ := adminPost(t, l.h.nodeUpdate, l.node48Body(node48DDNS(nil))); code != 0 {
		t.Fatalf("save: %d %s", code, msg)
	}
	l.wait()
	l.agentReports(ctNew, cuIP)
	start := l.cf.totalCalls()
	ctx := context.Background()
	// One simulated hour, timer passes every 5 minutes.
	for i := 0; i < 12; i++ {
		l.advance(5 * time.Minute)
		l.h.ddnsDuePass(ctx)
	}
	idle := l.cf.totalCalls() - start
	t.Logf("Cloudflare calls during 1 h idle: %d for 2 records", idle)
	if idle > 4 {
		t.Fatalf("idle hour made %d calls (target ≤ 2 per record)", idle)
	}
	if l.cf.count("PUT /zones/zone1/dns_records/:id")+l.cf.count("POST /zones/zone1/dns_records") != 2 {
		t.Fatal("drift check must not write when nothing changed")
	}

	// Cloudflare errors: failed + backoff 30 s, 60 s …; no call while backing off.
	l.cf.mu.Lock()
	l.cf.fail = 2
	l.cf.mu.Unlock()
	l.agentReports(ctOld, cuIP)
	rec := l.records()[ctDom]
	if rec.Status != model.DDNSStatusFailed || rec.FailCount != 1 || rec.NextRetryAt != l.clock.Add(30*time.Second).UnixMilli() {
		t.Fatalf("after first failure: %+v", rec)
	}
	before := l.cf.totalCalls()
	l.advance(10 * time.Second)
	l.h.ddnsDuePass(ctx)
	if l.cf.totalCalls() != before {
		t.Fatal("call during backoff")
	}
	l.advance(25 * time.Second)
	l.h.ddnsDuePass(ctx) // retry: second failure answer
	if rec = l.records()[ctDom]; rec.FailCount != 2 || rec.NextRetryAt != l.clock.Add(time.Minute).UnixMilli() {
		t.Fatalf("after second failure: %+v", rec)
	}
	l.advance(61 * time.Second)
	l.h.ddnsDuePass(ctx)
	if rec = l.records()[ctDom]; rec.Status != model.DDNSStatusSynced || rec.CurrentIP != ctOld || rec.FailCount != 0 {
		t.Fatalf("after recovery: %+v", rec)
	}
	if got := l.cf.find(ctDom, "AAAA"); got[0].Content != ctOld {
		t.Fatalf("record after recovery = %+v", got)
	}
}

func TestNodeDDNSSaveRefusesForeignRecordsUnlessTakeover(t *testing.T) {
	l := newDDNSLab(t)
	l.h.onNodeAddrs(23, ws.NodeAddrs{V6: []string{ctNew, cuIP}})
	l.cf.add(ddns.Record{Type: "AAAA", Name: ctDom, Content: "2400:cb00::1", TTL: 1})
	code, msg, _ := adminPost(t, l.h.nodeUpdate, l.node48Body(node48DDNS(nil)))
	if code == 0 || !strings.Contains(msg, "接管已有记录") || !strings.Contains(msg, "2400:cb00::1") {
		t.Fatalf("foreign record accepted: %d %s", code, msg)
	}
	if l.node().DDNSEnabled {
		t.Fatal("refused save changed the node")
	}
	// Ticking 接管 takes it over (one PUT, no second record).
	if code, msg, _ := adminPost(t, l.h.nodeUpdate, l.node48Body(node48DDNS(map[string]interface{}{"takeover": true}))); code != 0 {
		t.Fatalf("takeover save: %d %s", code, msg)
	}
	l.wait()
	if got := l.cf.find(ctDom, "AAAA"); len(got) != 1 || got[0].Content != ctNew || got[0].TTL != 120 {
		t.Fatalf("taken over record = %+v", got)
	}

	for name, rec := range map[string]ddns.Record{
		"proxied": {Type: "AAAA", Name: "p.imgamer.top", Content: ctNew, Proxied: true},
		"cname":   {Type: "CNAME", Name: "c.imgamer.top", Content: "x.example.com"},
	} {
		l.cf.add(rec)
		cfg := map[string]interface{}{"enabled": true, "providerId": 1, "records": []interface{}{
			map[string]interface{}{"domain": rec.Name, "type": "AAAA", "role": "main", "source": "ct"}}}
		code, msg, _ := adminPost(t, l.h.nodeUpdate, l.node48Body(cfg))
		if code == 0 {
			t.Fatalf("%s record accepted", name)
		}
		t.Logf("%s refused: %s", name, msg)
	}
	for _, domain := range []string{"x.example.com", "flvxx.imgamer.top", "mmwx.imgamer.top", "imgamer.top"} {
		cfg := map[string]interface{}{"enabled": true, "providerId": 1, "records": []interface{}{
			map[string]interface{}{"domain": domain, "type": "AAAA", "role": "main", "source": "ct"}}}
		if code, msg, _ := adminPost(t, l.h.nodeUpdate, l.node48Body(cfg)); code == 0 {
			t.Fatalf("%s accepted", domain)
		} else {
			t.Logf("%s refused: %s", domain, msg)
		}
	}
	if l.cf.count("DELETE") != 0 {
		t.Fatal("delete called")
	}
}

func TestNodeDDNSOldAgentAndPanelSource(t *testing.T) {
	l := newDDNSLab(t)
	l.report = false // old agent: no interface report
	cfg := map[string]interface{}{"enabled": true, "providerId": 1, "records": []interface{}{
		map[string]interface{}{"domain": ctDom, "type": "AAAA", "role": "main", "source": "ct"},
		map[string]interface{}{"domain": "leikwan-v4.imgamer.top", "type": "A", "role": "main", "source": "panel"},
	}}
	if code, msg, _ := adminPost(t, l.h.nodeUpdate, l.node48Body(cfg)); code != 0 {
		t.Fatalf("save: %d %s", code, msg)
	}
	l.wait()
	recs := l.records()
	if recs[ctDom].Status != model.DDNSStatusUpgrade {
		t.Fatalf("AAAA on old agent = %+v", recs[ctDom])
	}
	if a := recs["leikwan-v4.imgamer.top"]; a.Status != model.DDNSStatusSynced || a.CurrentIP != "123.204.3.9" {
		t.Fatalf("A from panel-seen IPv4 = %+v", a)
	}
	if n := l.node(); n.ServerIPV4.String != "leikwan-v4.imgamer.top" {
		t.Fatalf("v4 field = %q", n.ServerIPV4.String)
	}
	views, _ := l.h.ddnsNodeViews()
	if views[23].Status != model.DDNSStatusUpgrade {
		t.Fatalf("badge = %s", views[23].Status)
	}
}

func TestNodeDDNSCreateWithEmptyAddressUsesDomain(t *testing.T) {
	l := newDDNSLab(t)
	body := map[string]interface{}{"name": "new-hutai", "serverIpV4": "", "serverIpV6": "", "port": "1000-2000", "extraIPs": "",
		"ddns": node48DDNS(nil)}
	code, msg, _ := adminPost(t, l.h.nodeCreate, body)
	if code != 0 {
		t.Fatalf("create: %d %s", code, msg)
	}
	var n model.Node
	if err := l.r.DB().Where("name = ?", "new-hutai").First(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n.ServerIPV6.String != ctDom || n.ServerIP != ctDom || !n.DDNSEnabled {
		t.Fatalf("created node = %+v", n)
	}
	recs, _ := l.r.GetNodeDDNSRecords(n.ID)
	if len(recs) != 2 || !recs[0].Applied || recs[1].Applied {
		t.Fatalf("records = %+v", recs)
	}
	// The same domain cannot belong to two nodes.
	l.wait()
	code, msg, _ = adminPost(t, l.h.nodeUpdate, l.node48Body(node48DDNS(nil)))
	if code == 0 || !strings.Contains(msg, "已被节点") {
		t.Fatalf("duplicate domain accepted: %d %s", code, msg)
	}
	// Deleting the node removes its DDNS rows, never the DNS records.
	if err := l.h.deleteNodeByID(n.ID); err != nil {
		t.Fatal(err)
	}
	if recs, _ := l.r.GetNodeDDNSRecords(n.ID); len(recs) != 0 {
		t.Fatal("ddns rows left after node delete")
	}
}

func TestDNSProviderAPIMasksTokenAndRefusesDeleteInUse(t *testing.T) {
	l := newDDNSLab(t)
	code, msg, data := adminPost(t, l.h.ddnsProviderList, map[string]interface{}{})
	if code != 0 || strings.Contains(string(data), "test-token") || !strings.Contains(string(data), `"tokenSet":true`) {
		t.Fatalf("list: %d %s %s", code, msg, data)
	}
	code, _, data = adminPost(t, l.h.ddnsProviderTest, map[string]interface{}{"id": 1})
	if code != 0 || !strings.Contains(string(data), `"status":"active"`) || !strings.Contains(string(data), "imgamer.top") {
		t.Fatalf("test: %s", data)
	}
	if p, _ := l.r.GetDNSProvider(1); p.TokenStatus != "active" || p.TokenExpiresOn != 0 {
		t.Fatalf("stored check = %+v", p)
	}
	// Bad token: reported, not stored for an unsaved test.
	code, _, data = adminPost(t, l.h.ddnsProviderTest, map[string]interface{}{"token": "wrong"})
	if code != 0 || !strings.Contains(string(data), `"status":"invalid"`) {
		t.Fatalf("bad token test: %s", data)
	}
	// Rename keeps the token.
	if code, msg, _ := adminPost(t, l.h.ddnsProviderUpdate, map[string]interface{}{"id": 1, "name": "CF", "token": ""}); code != 0 {
		t.Fatalf("rename: %s", msg)
	}
	if p, _ := l.r.GetDNSProvider(1); dnsProviderToken(p) != "test-token" || p.Name != "CF" {
		t.Fatalf("after rename: %+v", p)
	}
	if code, msg, _ := adminPost(t, l.h.nodeUpdate, l.node48Body(node48DDNS(nil))); code != 0 {
		t.Fatalf("save: %s", msg)
	}
	l.wait()
	if code, msg, _ := adminPost(t, l.h.ddnsProviderDelete, map[string]interface{}{"id": 1}); code == 0 || !strings.Contains(msg, "节点使用") {
		t.Fatalf("delete in use: %d %s", code, msg)
	}
}
