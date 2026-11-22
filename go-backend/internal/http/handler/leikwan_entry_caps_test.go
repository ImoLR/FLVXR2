package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 295 fixture: the 4 prod 利群入口 profiles (11-22) — 火山上海 stable
// (coalesce + logswitch only, offline), 火山广州 old test build (all files,
// phone 19047 → 263's 备线, offline), 阿里广州 test (no phone), 百度云广州
// test (all files, phone ports for 263 / 264). imolr's 主入口 = 百度云广州.
func lkCapsFixture(t *testing.T) (*Handler, *repo.Repository) {
	t.Helper()
	r, err := repo.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{repo: r}
	db := r.DB()
	now := time.Now().UnixMilli()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	grp := sql.NullInt64{Int64: 7, Valid: true}
	for _, n := range []model.Node{
		{ID: 52, Name: "火山广州", ServerIP: "101.96.213.164", Status: 0, LeiKwanStatus: "running"},
		{ID: 58, Name: "百度云广州", ServerIP: "106.13.17.23", Status: 1, LeiKwanStatus: "running"},
		{ID: 60, Name: "阿里广州", ServerIP: "8.134.172.154", Status: 1, LeiKwanStatus: "running"},
		{ID: 61, Name: "火山上海", ServerIP: "14.103.34.108", Status: 0, LeiKwanStatus: "running"},
		{ID: 2, Name: "ixp1", ServerIP: "163.53.52.189", Status: 1},
	} {
		n.Secret, n.Port, n.CreatedTime = "s", "1-65535", now
		n.UpdatedTime = sql.NullInt64{Int64: now - 600_000, Valid: true}
		if n.ID != 2 {
			n.GroupID = grp
		}
		must(db.Create(&n).Error)
	}
	must(db.Create(&model.LeiKwanSetting{ID: 1, AccountID: 1, EntryGroupID: 7, Global: `{}`}).Error)
	for _, f := range []struct {
		id   int64
		port int
	}{{148, 35046}, {595, 40304}, {619, 59349}} {
		must(db.Create(&model.Forward{ID: f.id, UserID: 1, Name: "備線", TunnelID: 90, RemoteAddr: "x:1", CreatedTime: now, UpdatedTime: now, Status: 1}).Error)
		must(db.Create(&model.ForwardPort{ForwardID: f.id, NodeID: 2, Port: f.port}).Error)
	}
	must(db.Create(&model.LeiKwanLine{ID: 1, AccountID: 1, RouteID: 21, Name: "利群·广港", Accel: `{"warm":{"main":10,"backup":4},"ssFilter":"enforce","logswitch":true,"returnCheck":true}`, Status: 1}).Error)
	must(db.Create(&model.LeiKwanLine{ID: 2, AccountID: 1, RouteID: 24, Name: "利群·沪日", Accel: `{"warm":{"main":4,"backup":4},"ssFilter":"enforce","logswitch":true,"returnCheck":true}`, Status: 1}).Error)
	must(db.Create(&model.LeiKwanRule{ID: 1, UserID: 1, LineID: 1, ForwardID: 263, Name: "263 Boil HKT 99u", ListenPort: 10018, BackupForwardID: 148, BackupMode: "linked", Status: 1}).Error)
	must(db.Create(&model.LeiKwanRule{ID: 2, UserID: 1, LineID: 2, ForwardID: 264, Name: "264 Bestvm JP B", ListenPort: 10015, BackupForwardID: 595, BackupMode: "linked", Status: 1}).Error)
	must(db.Create(&model.LeiKwanRule{ID: 7, UserID: 1, LineID: 1, ForwardID: 540, Name: "540 测试", ListenPort: 10021, BackupForwardID: 619, BackupMode: "linked", Accel: `{"warmMain":0}`, Status: 1}).Error)
	must(db.Create(&model.LeiKwanGrant{UserID: 1, MainEntryNodeID: 58, BackupLine: 1, Status: 1}).Error)
	full := `"files":{"coalesce":true,"logswitch":true,"phone":true,"ssfilter":true,"sskeys":true,"triggers":true,"warm":true}`
	for node, prof := range map[int64]string{
		52: `{` + full + `,"phone":{"19047":{"forwardId":148}},"rules":{"263":{"warmMain":10},"264":{"warmMain":4},"540":{"ssFilter":"off","logswitch":false}}}`,
		58: `{` + full + `,"phone":{"19263":{"forwardId":148,"comment":"263 广港"},"19264":{"forwardId":595,"comment":"264 沪日"}},"rules":{"540":{"warmMain":10}}}`,
		60: `{"files":{"coalesce":true,"logswitch":true,"ssfilter":true,"sskeys":true,"triggers":true,"warm":true},"rules":{"263":{"warmMain":10},"264":{"warmMain":4},"540":{"ssFilter":"off","logswitch":false}}}`,
		61: `{"files":{"coalesce":true,"logswitch":true},"rules":{"540":{"logswitch":false}}}`,
	} {
		host, err := r.EnsureLeiKwanHost(node)
		must(err)
		host.Profile, host.Manage = prof, 1
		must(r.SaveLeiKwanHostConfig(host))
	}
	for node, front := range map[int64][2]string{52: {"test", "test-ssfilter-coalesce-warmflush-paced"}, 58: {"test", "test-ssfilter-coalesce-warmflush-paced-headroom-bytes4"}, 60: {"test", "test-ssfilter-coalesce-warmflush-paced-headroom-bytes4"}, 61: {"stable", "stable"}} {
		rep, _ := json.Marshal(lkHostReport{V: 1, At: now, State: lkStateView{FrontChannel: front[0], FrontBuild: front[1], AgentRunning: true}})
		must(r.SaveLeiKwanHostReport(node, string(rep), now))
	}
	// 火山上海 had a push attempt right before it dropped (the duplicate ⚠).
	must(r.SaveLeiKwanHostSync(61, now-60_000, "节点连接已断开"))
	return h, r
}

func TestLeiKwanEntryOfflineAttention(t *testing.T) {
	h, r := lkCapsFixture(t)
	g, err := h.newLKGen()
	if err != nil {
		t.Fatal(err)
	}
	// Both offline hosts get the same single reason; the sync error is not repeated.
	for _, id := range []int64{61, 52} {
		v := h.leiKwanHostManageView(id, g)
		att, _ := v["attention"].([]string)
		if len(att) != 1 || att[0] != lkAttnOffline || v["attentionLevel"] != "error" || v["offline"] != true {
			t.Fatalf("node %d attention %v level %v offline %v", id, att, v["attentionLevel"], v["offline"])
		}
		if since, _ := v["offlineSince"].(int64); since <= 0 {
			t.Fatalf("node %d offlineSince %v", id, v["offlineSince"])
		}
	}
	// Online again: no 离线, the recent sync error shows (as before plan 295).
	if err := r.UpdateNodeStatus(61, 1); err != nil {
		t.Fatal(err)
	}
	v := h.leiKwanHostManageView(61, g)
	att, _ := v["attention"].([]string)
	if len(att) != 1 || !strings.HasPrefix(att[0], "同步失败") || v["attentionLevel"] != "error" || v["offline"] != nil {
		t.Fatalf("online 61: %v %v %v", att, v["attentionLevel"], v["offline"])
	}
	// Online, nothing wrong: no attention, no level.
	v = h.leiKwanHostManageView(60, g)
	if att, _ := v["attention"].([]string); len(att) != 0 || v["attentionLevel"] != nil {
		t.Fatalf("60: %v %v", att, v["attentionLevel"])
	}
	// The host list counts the offline host (card ⚠ / 「N 台需要处理」 / H5 tab).
	_ = r.UpdateNodeStatus(61, 0)
	req := httptest.NewRequest("POST", "/api/v1/leikwan/host/list", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	h.leikwanHostList(w, req)
	var out struct {
		Data struct {
			Attention int `json:"attention"`
			Hosts     []struct {
				Node   leiKwanEntryNode       `json:"node"`
				Caps   lkEntryCaps            `json:"caps"`
				Manage map[string]interface{} `json:"manage"`
			} `json:"hosts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Attention != 2 || len(out.Data.Hosts) != 4 {
		t.Fatalf("host list attention %d hosts %d: %s", out.Data.Attention, len(out.Data.Hosts), w.Body.String())
	}
	for _, hh := range out.Data.Hosts {
		if hh.Node.ID == 61 && (hh.Node.Online || hh.Node.OfflineSince <= 0 || hh.Caps.Client != "稳定版" || hh.Caps.Warm || !hh.Caps.Logswitch) {
			t.Fatalf("61 in list: %+v", hh)
		}
	}
}

func TestLeiKwanEntryCaps(t *testing.T) {
	h, _ := lkCapsFixture(t)
	entries := h.leiKwanEntryNodes(7)
	infos := h.leiKwanEntryInfos(entries)
	sh := infos[61].caps
	if !sh.Known || sh.Client != "稳定版" || sh.Warm || sh.SSFilter || sh.Triggers || sh.Phone || !sh.Logswitch {
		t.Fatalf("火山上海 caps %+v", sh)
	}
	if strings.Join(sh.Missing, ",") != "预热池,首包过滤,快切触发,手机备线" {
		t.Fatalf("火山上海 missing %v", sh.Missing)
	}
	if w := sh.why(lkFileWarm); w != "入口 火山上海 的利群客户端是稳定版，没有预热池" {
		t.Fatalf("why %q", w)
	}
	if ali := infos[60].caps; !ali.Warm || ali.Phone || strings.Join(ali.Missing, ",") != "手机备线" {
		t.Fatalf("阿里广州 caps %+v", ali)
	}
	if bd := infos[58].caps; len(bd.Missing) != 0 || !bd.Online {
		t.Fatalf("百度云广州 caps %+v", bd)
	}
}

func lkFindRule(t *testing.T, rules []lkSwitchRule, fid int64) lkSwitchRule {
	t.Helper()
	for _, x := range rules {
		if x.ForwardID == fid {
			return x
		}
	}
	t.Fatalf("rule %d not in preview", fid)
	return lkSwitchRule{}
}

func lkChange(r lkSwitchRule, key string) *lkSwitchChange {
	for i := range r.Changes {
		if r.Changes[i].Key == key {
			return &r.Changes[i]
		}
	}
	return nil
}

func TestLeiKwanGrantPreview(t *testing.T) {
	h, _ := lkCapsFixture(t)
	// 百度云广州 → 火山上海 (stable): 预热 / 首包过滤 / 快切触发 / 手机备线 lost.
	out, err := h.leiKwanGrantPreview(1, 61)
	if err != nil {
		t.Fatal(err)
	}
	rules := out["rules"].([]lkSwitchRule)
	if len(rules) != 3 || out["lostRules"].(int) != 3 {
		t.Fatalf("preview → 火山上海: %d rules, lost %v", len(rules), out["lostRules"])
	}
	r263 := lkFindRule(t, rules, 263)
	warm := lkChange(r263, lkFileWarm)
	if warm == nil || !warm.Lost || warm.From != "预热 10" || warm.To != "不预热" || warm.Why != "入口 火山上海 的利群客户端是稳定版，没有预热池" {
		t.Fatalf("263 warm %+v", warm)
	}
	for _, key := range []string{lkFileSSFilter, lkFileTriggers, lkFilePhone} {
		if c := lkChange(r263, key); c == nil || !c.Lost {
			t.Fatalf("263 %s %+v (changes %+v)", key, c, r263.Changes)
		}
	}
	if !strings.Contains(strings.Join(r263.Kept, ","), "日志快切 开") {
		t.Fatalf("263 kept %v", r263.Kept)
	}
	// 540: 百度云广州 runs a host-pinned 预热 10; 火山上海 has no logswitch for it (pinned off).
	r540 := lkFindRule(t, rules, 540)
	if c := lkChange(r540, lkFileWarm); c == nil || c.From != "预热 10" || !c.Lost {
		t.Fatalf("540 warm %+v", c)
	}
	if c := lkChange(r540, lkFileLogswitch); c == nil || !c.Lost || !strings.Contains(c.Why, "单独登记") {
		t.Fatalf("540 logswitch %+v", c)
	}
	// The offline target is flagged.
	if notes := out["notes"].([]string); len(notes) == 0 || !strings.Contains(notes[0], "离线") {
		t.Fatalf("notes %v", notes)
	}

	// 百度云广州 → 阿里广州: only 手机备线 lost on 263 / 264 (warm pinned on 阿里广州).
	out, err = h.leiKwanGrantPreview(1, 60)
	if err != nil {
		t.Fatal(err)
	}
	rules = out["rules"].([]lkSwitchRule)
	for _, fid := range []int64{263, 264} {
		x := lkFindRule(t, rules, fid)
		if len(x.Changes) != 1 || x.Changes[0].Key != lkFilePhone || !x.Changes[0].Lost || x.Changes[0].Why != "入口 阿里广州 上没有这条转发的手机备线端口" {
			t.Fatalf("%d → 阿里广州: %+v", fid, x.Changes)
		}
	}
	if notes := out["notes"].([]string); len(notes) != 0 {
		t.Fatalf("online target notes %v", notes)
	}
	// Same entry: nothing to compare.
	out, _ = h.leiKwanGrantPreview(1, 58)
	if out["same"] != true || out["lostRules"].(int) != 0 {
		t.Fatalf("same entry %+v", out)
	}
}

func TestLeiKwanRuleEntryLimits(t *testing.T) {
	h, r := lkCapsFixture(t)
	rules, _ := r.ListLeiKwanRules(0)
	c := h.leiKwanRuleContext(rules, true)
	if len(c.limits) != 0 {
		t.Fatalf("百度云广州 runs everything: %+v", c.limits)
	}
	// Move imolr to 火山上海 (data only, as after a 换主入口): 预热 greys out
	// on 263 / 264 but not on 540 (configured 不预热).
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	gr, _ := r.GetLeiKwanGrant(1)
	gr.MainEntryNodeID = 61
	must(r.SaveLeiKwanGrant(gr))
	c = h.leiKwanRuleContext(rules, true)
	keys := func(id int64) string {
		out := []string{}
		for _, l := range c.limits[id] {
			out = append(out, l.Key)
		}
		return strings.Join(out, ",")
	}
	if got := keys(1); got != "warm,ssfilter,triggers,phone" {
		t.Fatalf("263 limits %q %+v", got, c.limits[1])
	}
	if got := keys(7); got != "ssfilter,triggers" {
		t.Fatalf("540 limits %q", got)
	}
	v := h.leiKwanRuleView(rules[0], c, true)
	if l, _ := v["entryLimits"].([]lkCapLimit); len(l) != 4 || l[0].Why != "主入口 火山上海 的利群客户端是稳定版，没有预热池" {
		t.Fatalf("view limits %+v", v["entryLimits"])
	}
	if e := v["entry"].(map[string]interface{}); e["online"] != false || e["offlineSince"] == nil {
		t.Fatalf("entry %+v", e)
	}
	// Users never get the limits.
	if v := h.leiKwanRuleView(rules[0], h.leiKwanRuleContext(rules, false), false); v["entryLimits"] != nil {
		t.Fatal("user view has entryLimits")
	}
}

func TestLeiKwanGrantSaveRefusesOfflineEntry(t *testing.T) {
	h, r := lkCapsFixture(t)
	call := func(body string) response.R {
		req := httptest.NewRequest("POST", "/api/v1/leikwan/grant/save", bytes.NewReader([]byte(body)))
		w := httptest.NewRecorder()
		h.leikwanGrantSave(w, req)
		var out response.R
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	if out := call(`{"userId":1,"mainEntryNodeId":61,"backupLine":true}`); out.Code == 0 || !strings.Contains(out.Msg, "离线") {
		t.Fatalf("move to offline 火山上海: %+v", out)
	}
	gr, _ := r.GetLeiKwanGrant(1)
	if gr.MainEntryNodeID != 58 {
		t.Fatalf("grant moved: %+v", gr)
	}
	// Keeping an offline current entry (e.g. 备线 switch) still saves.
	gr.MainEntryNodeID = 61
	_ = r.SaveLeiKwanGrant(gr)
	if out := call(`{"userId":1,"mainEntryNodeId":61,"backupLine":true}`); out.Code != 0 {
		t.Fatalf("keep offline current: %+v", out)
	}
}
