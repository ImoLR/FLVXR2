package handler

import (
	"strings"
	"testing"
	"time"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 316 + 318: 屏蔽本机厂商官网 — the vendor is picked on the NODE (required,
// multi): the node's landing gets the vendor rule first (union of its
// vendors, v2 agent = abuse marker, v3 = vendor marker); a vendor change on
// the node re-syncs the landing; 「其他」 = no rule + ⚠; the assign API is for
// mmwx shares only; the switch removes the rule; a used vendor cannot be
// deleted; users never see vendor fields.
func TestLandingVendorBlock(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	e.exec("UPDATE node SET landing_capable=?, landing_egress=1, landing_guard=2, name='landing', server_ip='203.0.113.2' WHERE id=2", true)
	e.exec("UPDATE landing_server SET egress_ready=? WHERE id=1", true)
	agent := egressAgent(t, e, true)
	e.h.SetLandingLayerApplyDelay(10 * time.Millisecond)
	lastPlan := func() *landingegress.Plan {
		t.Helper()
		plan, _ := agent.payloads[len(agent.payloads)-1]["egress"].(*landingegress.Plan)
		return plan
	}
	names := func(p *landingegress.Plan) string {
		var out []string
		for _, r := range p.Rules {
			out = append(out, r.Name)
		}
		return strings.Join(out, "|")
	}
	vendorView := func() map[string]any {
		t.Helper()
		view, err := e.h.landingGlobalView()
		if err != nil {
			t.Fatal(err)
		}
		return view["builtin"].(map[string]any)["vendor"].(map[string]any)
	}
	issueIDs := func(vv map[string]any) []int64 {
		var ids []int64
		for _, it := range vv["nodeIssues"].([]map[string]any) {
			ids = append(ids, it["id"].(int64))
		}
		return ids
	}
	setNode := func(vendors []string, other string) (int, string) {
		t.Helper()
		code, msg, _ := adminPost(t, e.h.nodeUpdate, map[string]any{"id": 2, "name": "landing", "serverIp": "203.0.113.2", "port": "1000-29999", "vendors": vendors, "vendorOther": other})
		e.h.WaitEgressSync()
		return code, msg
	}
	mirror := func() string {
		s, _ := e.r.GetLandingServer(1)
		return s.Vendor
	}
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	before := names(lastPlan())
	if before != "默认：屏蔽 SMTP 25|默认：屏蔽 BT" {
		t.Fatalf("fresh plan %s", before)
	}
	// No vendor on the node: the admin row lists the node as an issue (no
	// suggestion any more), the node row ✗ unset.
	vv := vendorView()
	if vv["on"] != true || vv["unset"].(int) < 1 || len(vv["vendors"].([]landingegress.Vendor)) != len(landingegress.SeedVendors()) || !containsID(issueIDs(vv), 2) {
		t.Fatalf("admin vendor view %+v", vv)
	}
	for _, s := range vv["servers"].([]map[string]any) {
		if _, ok := s["suggest"]; ok {
			t.Fatalf("suggestion left in %+v", s)
		}
	}
	n, _ := e.r.GetLandingNode(1)
	nv, _ := e.h.landingNodeRulesView(n, false)
	if nb := nv["builtin"].(map[string]any)["vendor"].(map[string]any); nb["on"] != false || nb["offReason"] != "unset" {
		t.Fatalf("node view unset %+v", nb)
	}
	// The per-server assign API is for mmwx shares only.
	if out := egressCall(t, e.h, "/api/v1/landing/egress/vendor/assign", map[string]any{"serverId": 1, "vendor": "dmit"}, 0); out.Code == 0 || !strings.Contains(out.Msg, "节点") {
		t.Fatalf("assign on a native server %+v", out)
	}
	// An old node cannot be saved with an empty pick.
	if code, msg := setNode([]string{}, ""); code == 0 || !strings.Contains(msg, "VPS 厂商") {
		t.Fatalf("empty pick accepted: %d %s", code, msg)
	}
	// Pick DMIT on the node → the landing follows at once (+ mirror).
	if code, msg := setNode([]string{"dmit"}, ""); code != 0 {
		t.Fatalf("set dmit %d %s", code, msg)
	}
	p := lastPlan()
	if names(p) != "内置：屏蔽本机厂商官网（DMIT）|"+before {
		t.Fatalf("plan after pick %s", names(p))
	}
	r0 := p.Rules[0]
	if strings.Join(r0.Domain, ",") != "domain:dmit.com,domain:dmit.io,domain:dmithost.net" || r0.Outbound != "block" || r0.Builtin != landingegress.BuiltinAbuse {
		t.Fatalf("vendor rule %+v", r0)
	}
	if mirror() != "dmit" {
		t.Fatalf("mirror %q", mirror())
	}
	nv, _ = e.h.landingNodeRulesView(n, false)
	if nb := nv["builtin"].(map[string]any)["vendor"].(map[string]any); nb["on"] != true || nb["name"] != "DMIT" || nb["sni"] != false {
		t.Fatalf("node view on %+v", nb)
	}
	// Two vendors (seller + upstream cloud): one rule, union of the names.
	if code, msg := setNode([]string{"leikwan", "volcengine"}, ""); code != 0 {
		t.Fatalf("set two %d %s", code, msg)
	}
	p = lastPlan()
	if names(p) != "内置：屏蔽本机厂商官网（Leikwanhost、火山引擎）|"+before || strings.Join(p.Rules[0].Domain, ",") != "domain:leikwanhost.com,domain:volcengine.cn,domain:volcengine.com" {
		t.Fatalf("two vendors %s %v", names(p), p.Rules[0].Domain)
	}
	if mirror() != "leikwan" || containsID(issueIDs(vendorView()), 2) {
		t.Fatalf("mirror %q / still an issue", mirror())
	}
	s, _ := e.r.GetLandingServer(1)
	sv := e.h.landingServerView(*s)
	if sv["vendorFromNode"] != true || sv["vendorName"] != "Leikwanhost、火山引擎" || strings.Join(sv["vendorKeys"].([]string), ",") != "leikwan,volcengine" || sv["vendorNodeName"] != "landing" {
		t.Fatalf("server view %+v", sv)
	}
	if _, ok := sv["vendorSuggest"]; ok {
		t.Fatal("vendorSuggest still in the server view")
	}
	// 「其他」 only: no rule, ⚠ on node + landing.
	if code, msg := setNode([]string{}, "某小厂"); code != 0 {
		t.Fatalf("set other %d %s", code, msg)
	}
	if names(lastPlan()) != before || mirror() != "" {
		t.Fatalf("other → %s mirror %q", names(lastPlan()), mirror())
	}
	vv = vendorView()
	if !containsID(issueIDs(vv), 2) {
		t.Fatalf("其他 not an issue %+v", vv["nodeIssues"])
	}
	s, _ = e.r.GetLandingServer(1)
	if sv := e.h.landingServerView(*s); sv["vendorOther"] != "某小厂" || sv["vendor"] != "" {
		t.Fatalf("server view other %+v %+v", sv["vendorOther"], sv["vendor"])
	}
	// 「其他」 that is already in the list is refused (pick it instead).
	if code, _ := setNode([]string{}, "DMIT"); code == 0 {
		t.Fatal("其他 = listed vendor accepted")
	}
	// Seller known + 其他: the known vendor still blocks, ⚠ stays.
	if code, msg := setNode([]string{"leikwan"}, "某小厂"); code != 0 {
		t.Fatalf("set mixed %d %s", code, msg)
	}
	if names(lastPlan()) != "内置：屏蔽本机厂商官网（Leikwanhost）|"+before || !containsID(issueIDs(vendorView()), 2) {
		t.Fatalf("mixed %s", names(lastPlan()))
	}
	if code, msg := setNode([]string{"dmit"}, ""); code != 0 {
		t.Fatalf("back to dmit %d %s", code, msg)
	}
	// An update without the vendor fields keeps the pick (older API callers).
	if code, msg, _ := adminPost(t, e.h.nodeUpdate, map[string]any{"id": 2, "name": "landing", "serverIp": "203.0.113.2", "port": "1000-29999"}); code != 0 {
		t.Fatalf("update w/o vendors %d %s", code, msg)
	}
	if nd, _ := e.r.GetNodeByID(2); nd.Vendors != "dmit" || nd.VendorOther != "" {
		t.Fatalf("absent vendors changed the pick %q %q", nd.Vendors, nd.VendorOther)
	}
	// A plan-316 agent gets the vendor marker (SNI / Host match).
	e.exec("UPDATE node SET landing_guard=3 WHERE id=2")
	if _, err := e.h.syncLandingServer(1); err != nil {
		t.Fatal(err)
	}
	if got := lastPlan().Rules[0].Builtin; got != landingegress.BuiltinVendor {
		t.Fatalf("v3 marker %q", got)
	}
	// The vendor in use (by the node) cannot be deleted.
	vs, _ := e.r.GetLandingVendorSettings()
	var rest []landingegress.Vendor
	for _, v := range vs.Vendors {
		if v.Key != "dmit" {
			rest = append(rest, v)
		}
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/vendor/save", map[string]any{"vendors": rest}, 0); out.Code == 0 || !strings.Contains(out.Msg, "节点") {
		t.Fatalf("deleting a used vendor was accepted %+v", out)
	}
	// Edit the vendor's names: the plan follows.
	for i := range vs.Vendors {
		if vs.Vendors[i].Key == "dmit" {
			vs.Vendors[i].Domains = []string{"dmit.io"}
		}
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/vendor/save", map[string]any{"vendors": vs.Vendors}, 0); out.Code != 0 {
		t.Fatalf("save list %+v", out)
	}
	e.h.WaitEgressSync()
	if got := strings.Join(lastPlan().Rules[0].Domain, ","); got != "domain:dmit.io" {
		t.Fatalf("edited names %s", got)
	}
	// Global switch off: the rule is gone, the rest unchanged.
	if out := egressCall(t, e.h, "/api/v1/landing/egress/vendor/save", map[string]any{"on": false}, 0); out.Code != 0 {
		t.Fatalf("switch %+v", out)
	}
	e.h.WaitEgressSync()
	if names(lastPlan()) != before {
		t.Fatalf("plan with switch off %s", names(lastPlan()))
	}
	nv, _ = e.h.landingNodeRulesView(n, false)
	if nb := nv["builtin"].(map[string]any)["vendor"].(map[string]any); nb["on"] != false || nb["offReason"] != "global" {
		t.Fatalf("node view off %+v", nb)
	}
	// Server card fields (admin) / user view.
	s, _ = e.r.GetLandingServer(1)
	sv = e.h.landingServerView(*s)
	if sv["vendor"] != "dmit" || sv["vendorName"] != "DMIT" || sv["vendorOn"] != false {
		t.Fatalf("server view %+v %+v %+v", sv["vendor"], sv["vendorName"], sv["vendorOn"])
	}
	uv := e.h.landingServerUserView(*s, e.h.landingEgressCatalog(), &repo.LandingUserNodeCounts{})
	for _, k := range []string{"vendor", "vendorName", "vendorKeys", "vendorOther", "vendorFromNode", "vendorNodeName", "vendorOptions", "vendorDomains", "vendorSuggest"} {
		if _, ok := uv[k]; ok {
			t.Fatalf("user server view carries %s", k)
		}
	}
	// Unknown key refused on the node.
	if code, _ := setNode([]string{"nope"}, ""); code == 0 {
		t.Fatal("unknown vendor accepted")
	}
}

// Plan 318: mmwx shares keep their own pick (assign API, server 设置) and a
// node's vendor change never touches them; a share's rule is unchanged.
func TestLandingVendorShareUnchanged(t *testing.T) {
	e := newFlowTestEnv(t)
	e.addUser(2)
	seedHandlerLanding(t, e, 2, 2, 1)
	if err := e.r.DB().Create(&model.LandingServer{ID: 7, NodeID: 0, Source: "shared", Name: "share", PublicHost: "203.0.113.7", FlowType: 2, TrafficRatio: 1, Status: "active", Vendor: "boil"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.r.DB().Create(&model.LandingNode{ID: 7, UserID: 2, LandingServerID: 7, Name: "share-node", Protocol: "ss2022", Port: 10018, InboundTag: "flvxl-7", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	d, err := e.r.LandingEgressData(7)
	if err != nil || d.VendorInfo == nil || d.VendorInfo.Name != "Boil" || strings.Join(d.VendorInfo.Domains, ",") != "boil.network" {
		t.Fatalf("share vendor %+v %v", d, err)
	}
	if _, _, err := e.r.SetNodeVendors(2, repo.NodeVendorPick{Keys: []string{"dmit"}}); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.r.GetLandingServer(7); s.Vendor != "boil" {
		t.Fatalf("share vendor touched: %q", s.Vendor)
	}
	if out := egressCall(t, e.h, "/api/v1/landing/egress/vendor/assign", map[string]any{"serverId": 7, "vendor": "pqs"}, 0); out.Code != 0 {
		t.Fatalf("share assign %+v", out)
	}
	if d, _ := e.r.LandingEgressData(7); d.VendorInfo == nil || d.VendorInfo.Key != "pqs" {
		t.Fatalf("share after assign %+v", d.VendorInfo)
	}
	s, _ := e.r.GetLandingServer(7)
	if sv := e.h.landingServerView(*s); sv["vendor"] != "pqs" || sv["vendorFromNode"] != nil {
		t.Fatalf("share view %+v %+v", sv["vendor"], sv["vendorFromNode"])
	}
}

// Plan 318: node/create requires a vendor (UI + admin API) and stores the
// pick; internal paths (federation remote node) create without one → ⚠.
func TestNodeCreateRequiresVendor(t *testing.T) {
	e := newFlowTestEnv(t)
	if code, msg, _ := adminPost(t, e.h.nodeCreate, map[string]any{"name": "novendor", "serverIp": "10.9.0.1"}); code == 0 || !strings.Contains(msg, "VPS 厂商") {
		t.Fatalf("create without vendor: %d %s", code, msg)
	}
	var count int64
	e.r.DB().Model(&model.Node{}).Where("name = ?", "novendor").Count(&count)
	if count != 0 {
		t.Fatal("refused create stored a node")
	}
	if code, msg, _ := adminPost(t, e.h.nodeCreate, map[string]any{"name": "volc", "serverIp": "10.9.0.2", "vendors": []string{"leikwan", "volcengine"}}); code != 0 {
		t.Fatalf("create with vendors: %d %s", code, msg)
	}
	var n model.Node
	if err := e.r.DB().Where("name = ?", "volc").First(&n).Error; err != nil || n.Vendors != "leikwan,volcengine" || n.VendorOther != "" {
		t.Fatalf("stored %+v %v", n.Vendors, err)
	}
	if code, msg, _ := adminPost(t, e.h.nodeCreate, map[string]any{"name": "other", "serverIp": "10.9.0.3", "vendors": []string{}, "vendorOther": "某小厂"}); code != 0 {
		t.Fatalf("create with 其他: %d %s", code, msg)
	}
	if err := e.r.CreateRemoteNode("remote", "remote-secret", "10.9.0.4", "1000-2000", time.Now().UnixMilli(), 1, 99, "https://peer.example", "tok", ""); err != nil {
		t.Fatal(err)
	}
	vv, _ := e.h.landingVendorAdminView()
	got := map[string]string{}
	for _, it := range vv["nodeIssues"].([]map[string]any) {
		got[it["name"].(string)] = it["other"].(string)
	}
	if _, ok := got["remote"]; !ok || got["other"] != "某小厂" {
		t.Fatalf("issues %v", got)
	}
	if _, ok := got["volc"]; ok {
		t.Fatal("a node with vendors listed as an issue")
	}
	// node/list (admin) carries the pick.
	items, _ := e.r.ListNodes(&repo.ListNodesOptions{})
	for _, it := range items {
		if it["name"] == "volc" && strings.Join(it["vendors"].([]string), ",") != "leikwan,volcengine" {
			t.Fatalf("list item %+v", it["vendors"])
		}
	}
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
