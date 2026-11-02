package contract_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/auth"
	httpserver "go-backend/internal/http"
	"go-backend/internal/http/handler"
	"go-backend/internal/http/response"
	"go-backend/internal/renewal"
	"go-backend/internal/store/model"
	"go-backend/internal/tgbot"
	"go-backend/internal/tgbot/tgtest"
)

const tgContractToken = "7012345678:AAContractTokenNeverReal_0123456789abcdef"

type tgClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *tgClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *tgClock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

type tgLogs struct {
	mu    sync.Mutex
	lines []string
}

func (l *tgLogs) Logf(f string, a ...interface{}) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
	l.mu.Unlock()
}
func (l *tgLogs) all() string { l.mu.Lock(); defer l.mu.Unlock(); return strings.Join(l.lines, "\n") }

func twTime(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, renewal.Zone)
}

func fileSum(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "-"
		}
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// TestTelegramBotContract: plan 196 on SQLite and PostgreSQL with a fake
// Telegram server — machines (FLVX + 救命版, deduped), token never returned
// or logged, generic config API refusal + export strip, bind flow (wrong
// chat ignored, one-use code), daily reminders (texts, dedupe across
// restarts, rolling reset, next listed day, missed-send catch-up), forged
// buttons rejected, 已续费 (ack + FLVX node dismissal, 救命版 untouched),
// 不再提醒这台 (mute list), unbind → no goroutine.
func TestTelegramBotContract(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingContractRepo(t, dialect)
			ensureContractUser(t, r, 2, 1)
			for _, n := range []*model.Node{
				{ID: 861, Name: "Bestvm US", Secret: "s861", ServerIP: "203.0.113.61", Port: "1-2", Inx: 1, Status: 1,
					ExpiryTime: sql.NullInt64{Int64: twTime(2026, 10, 3, 0, 0).UnixMilli(), Valid: true}, RenewalCycle: sqlNullString("month")},
				{ID: 862, Name: "Mkcloud 深港ixp 1", Secret: "s862", ServerIP: "203.0.113.62", Port: "1-2", Inx: 2, Status: 1,
					ExpiryTime: sql.NullInt64{Int64: twTime(2025, 11, 9, 0, 0).UnixMilli(), Valid: true}, RenewalCycle: sqlNullString("year")},
				{ID: 863, Name: "Breadcloud JP", Secret: "s863", ServerIP: "87.76.190.35", Port: "1-2", Inx: 3, Status: 1},
				{ID: 864, Name: "火山广州", Secret: "s864", ServerIP: "203.0.113.64", Port: "1-2", Inx: 4, Status: 1},
			} {
				if err := r.DB().Create(n).Error; err != nil {
					t.Fatal(err)
				}
			}
			oldPath := buildOldPanelDB(t, []oldPanelSeed{
				{id: 13, name: "BREADCLOUD JP", ip: "87.76.190.35", v4: "87.76.190.35", expiry: twTime(2025, 11, 5, 0, 0).UnixMilli(), cycle: "year"},
				{id: 1, name: "PQS TW", ip: "45.150.1.2", v4: "45.150.1.2", expiry: twTime(2025, 11, 2, 15, 0).UnixMilli(), cycle: "year"},
				{id: 35, name: "Vmiss KR", ip: "45.93.28.126", v4: "45.93.28.126"},
			})
			oldSum := fileSum(t, oldPath) + fileSum(t, oldPath+"-wal")
			t.Setenv("FLVX_OLDPANEL_DB", oldPath)

			fake := tgtest.New(tgContractToken)
			defer fake.Close()
			clk := &tgClock{t: twTime(2026, 11, 2, 9, 0)}
			logs := &tgLogs{}
			admin, _ := auth.GenerateToken(1, "admin", 0, probeSecret)
			user, _ := auth.GenerateToken(2, "token_user_2", 1, probeSecret)

			type env struct {
				h      *handler.Handler
				router http.Handler
			}
			start := func() env {
				h := handler.New(r, probeSecret, "test")
				h.SetTelegramBotOptions(func(o *tgbot.Options) {
					o.APIBase, o.Now, o.Logf = fake.URL(), clk.Now, logs.Logf
					o.BackoffMin, o.BackoffMax, o.SendGap = 20*time.Millisecond, 40*time.Millisecond, -1
				})
				h.EnableTelegramBot()
				return env{h: h, router: httpserver.NewRouter(h, probeSecret, r)}
			}
			raw := func(e env, path, token string, body interface{}) []byte {
				t.Helper()
				b, _ := json.Marshal(body)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/"+path, bytes.NewReader(b))
				req.Header.Set("Authorization", token)
				w := httptest.NewRecorder()
				e.router.ServeHTTP(w, req)
				return w.Body.Bytes()
			}
			call := func(e env, path string, body interface{}) response.R {
				t.Helper()
				var out response.R
				b := raw(e, path, admin, body)
				if err := json.Unmarshal(b, &out); err != nil {
					t.Fatalf("%s: %v %s", path, err, b)
				}
				if strings.Contains(string(b), tgContractToken) || strings.Contains(string(b), "AAContractToken") {
					t.Fatalf("%s returned the token: %s", path, b)
				}
				return out
			}
			type machine struct {
				Key      string `json:"key"`
				Name     string `json:"name"`
				Source   string `json:"source"`
				OldMatch string `json:"oldMatch"`
				Cycle    string `json:"cycle"`
				NextDue  int64  `json:"nextDue"`
				Days     int    `json:"days"`
				Muted    bool   `json:"muted"`
				Renewed  bool   `json:"renewed"`
			}
			type view struct {
				TokenSet bool `json:"tokenSet"`
				Profile  struct {
					Username string `json:"username"`
				} `json:"profile"`
				Running  bool              `json:"running"`
				Chats    []model.TGBotChat `json:"chats"`
				Machines []machine         `json:"machines"`
				NoExpiry int               `json:"noExpiry"`
				OldPanel string            `json:"oldPanel"`
				Settings tgbot.Settings    `json:"settings"`
			}
			get := func(e env) view {
				t.Helper()
				out := call(e, "config/tgbot", nil)
				if out.Code != 0 {
					t.Fatalf("get %+v", out)
				}
				b, _ := json.Marshal(out.Data)
				var v view
				_ = json.Unmarshal(b, &v)
				return v
			}
			wait := func(what string, cond func() bool) {
				t.Helper()
				deadline := time.Now().Add(8 * time.Second)
				for !cond() {
					if time.Now().After(deadline) {
						t.Fatalf("timeout: %s", what)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			settle := func() { time.Sleep(120 * time.Millisecond) }
			lastRun := func() string {
				return mustQueryString(t, r, "SELECT COALESCE(MAX(value),'') FROM vite_config WHERE name = ?", tgbot.KeyLastRun)
			}

			e := start()
			// ── unset: machines listed, nothing running ─────────────────
			v := get(e)
			if v.TokenSet || v.Running || e.h.TelegramBotRunning() || v.OldPanel != "ok" {
				t.Fatalf("unset view %+v", v)
			}
			var keys []string
			for _, m := range v.Machines {
				keys = append(keys, fmt.Sprintf("%s %s %s %d", m.Key, m.Name, m.Source, m.Days))
			}
			wantKeys := []string{"o1 PQS TW old 0", "f861 Bestvm US flvx 1", "f863 Breadcloud JP flvx 3", "f862 Mkcloud 深港ixp 1 flvx 7"}
			if strings.Join(keys, "|") != strings.Join(wantKeys, "|") || v.NoExpiry != 2 {
				t.Fatalf("machines %v noExpiry %d", keys, v.NoExpiry)
			}
			if v.Machines[2].OldMatch != "BREADCLOUD JP" {
				t.Fatalf("matched pair %+v", v.Machines[2])
			}
			var denied response.R
			_ = json.Unmarshal(raw(e, "config/tgbot", user, nil), &denied)
			if denied.Code != 403 {
				t.Fatalf("non-admin got %+v", denied)
			}

			// ── token: format, refusal, save; never returned ────────────
			if out := call(e, "config/tgbot/token", map[string]string{"token": "abc"}); out.Code == 0 {
				t.Fatal("bad token format accepted")
			}
			if out := call(e, "config/tgbot/token", map[string]string{"token": "7012345678:AAWrongTokenxxxxxxxxxxxxxxxxxxxxxxxxxx"}); out.Code == 0 || !strings.Contains(out.Msg, "拒绝") {
				t.Fatalf("wrong token %+v", out)
			}
			if out := call(e, "config/tgbot/token", map[string]string{"token": tgContractToken}); out.Code != 0 {
				t.Fatalf("save token %+v", out)
			}
			v = get(e)
			if !v.TokenSet || v.Profile.Username != "flvx_test_bot" || v.Running {
				t.Fatalf("after token %+v", v)
			}
			cfgList := raw(e, "config/list", admin, nil)
			if strings.Contains(string(cfgList), "tg_bot_") || strings.Contains(string(cfgList), "AAContract") {
				t.Fatalf("config/list leaks bot keys: %s", cfgList)
			}
			for _, k := range []string{tgbot.KeyToken, tgbot.KeySecret, "TG_BOT_TOKEN"} {
				if out := call(e, "config/get", map[string]string{"name": k}); out.Code == 0 {
					t.Fatalf("config/get %s answered %+v", k, out)
				}
				if out := call(e, "config/update", map[string]string{k: "x"}); out.Code == 0 {
					t.Fatalf("config/update %s accepted", k)
				}
				if out := call(e, "config/update-single", map[string]string{"name": k, "value": "x"}); out.Code == 0 {
					t.Fatalf("config/update-single %s accepted", k)
				}
			}
			if got := mustQueryString(t, r, "SELECT value FROM vite_config WHERE name = ?", tgbot.KeyToken); got != tgContractToken {
				t.Fatalf("stored token changed: %q", got)
			}
			full, err := r.ExportAll("full")
			if err != nil {
				t.Fatal(err)
			}
			part, err := r.ExportPartial([]string{"configs"})
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range []interface{}{full, part} {
				b, _ := json.Marshal(x)
				if strings.Contains(string(b), "AAContract") || strings.Contains(string(b), "tg_bot_secret") || strings.Contains(string(b), "tg_bot_token") {
					t.Fatal("backup export carries the bot token / key")
				}
			}

			// ── settings validation ─────────────────────────────────────
			if out := call(e, "config/tgbot/settings", map[string]interface{}{"days": "99"}); out.Code == 0 {
				t.Fatal("days 99 accepted")
			}
			if out := call(e, "config/tgbot/settings", map[string]interface{}{"time": "25:00"}); out.Code == 0 {
				t.Fatal("time 25:00 accepted")
			}
			if out := call(e, "config/tgbot/settings", map[string]interface{}{"enabled": true, "days": "7，3,1,0", "time": "10:00"}); out.Code != 0 {
				t.Fatalf("settings %+v", out)
			}

			// ── bind: wrong chat / code ignored, one use ────────────────
			out := call(e, "config/tgbot/bind-code", nil)
			if out.Code != 0 {
				t.Fatalf("bind code %+v", out)
			}
			bc := out.Data.(map[string]interface{})
			code := bc["code"].(string)
			if bc["link"] != "https://t.me/flvx_test_bot?start="+code || bc["command"] != "/start "+code {
				t.Fatalf("bind code view %+v", bc)
			}
			wait("bot running", e.h.TelegramBotRunning)
			adminChat := tgbot.Chat{ID: -1001234567890, Type: "supergroup", Title: "运维群"}
			alice := tgbot.User{ID: 4242, FirstName: "Alice"}
			stranger := tgbot.Chat{ID: 999, Type: "private", FirstName: "Mallory"}
			mallory := tgbot.User{ID: 999, FirstName: "Mallory"}
			fake.PushMessage(stranger, mallory, "/start NOTVALID")
			fake.PushMessage(stranger, mallory, "/help")
			fake.PushMessage(adminChat, alice, "/start@flvx_test_bot "+code)
			fake.PushMessage(stranger, mallory, "/start "+code)
			wait("bound", func() bool { return len(get(e).Chats) == 1 })
			settle()
			for _, c := range fake.Calls("") {
				if c.Int("chat_id") == stranger.ID {
					t.Fatalf("stranger got an answer: %+v", c)
				}
			}
			v = get(e)
			if v.Chats[0].ChatID != adminChat.ID || v.Chats[0].Title != "运维群" || v.Chats[0].Kind != "supergroup" {
				t.Fatalf("chat %+v", v.Chats[0])
			}
			if out := call(e, "config/tgbot/test", nil); out.Code != 0 {
				t.Fatalf("test send %+v", out)
			}

			// ── daily run at 10:00 (nothing at 09:00) ───────────────────
			fake.Reset()
			settle()
			if n := len(fake.Calls("sendMessage")); n != 0 {
				t.Fatalf("sent before 10:00: %d", n)
			}
			clk.Set(twTime(2026, 11, 2, 10, 0))
			wait("4 reminders", func() bool { return mustQueryInt(t, r, "SELECT COUNT(*) FROM tg_bot_sent") == 4 })
			wait("last run", func() bool { return lastRun() == "2026-11-02" })
			sends := fake.Calls("sendMessage")
			var texts []string
			byKey := map[string]tgtest.Call{}
			for _, s := range sends {
				texts = append(texts, s.Str("text"))
				if b := s.Buttons(); len(b) == 1 && len(b[0]) == 2 {
					parts := strings.Split(b[0][0].Data, ".")
					byKey[parts[1]] = s
				}
			}
			wantTexts := []string{
				"⏰ PQS TW 年付 今天到期（11-02） · 救命版",
				"⏰ Bestvm US 月付 明天到期（11-03）",
				"⏰ Breadcloud JP 年付 3 天后到期（11-05）",
				"⏰ Mkcloud 深港ixp 1 年付 7 天后到期（11-09）",
			}
			if strings.Join(texts, "\n") != strings.Join(wantTexts, "\n") {
				t.Fatalf("reminders:\n%s", strings.Join(texts, "\n"))
			}
			msgID := func(c tgtest.Call) int64 {
				return mustQueryInt64(t, r, "SELECT message_id FROM tg_bot_sent WHERE text = ?", c.Str("text"))
			}

			// ── forged buttons: rejected; stranger: silent ───────────────
			b861 := byKey["f861"].Buttons()[0]
			b862 := byKey["f862"].Buttons()[0]
			p861 := strings.Split(b861[0].Data, ".")
			p862 := strings.Split(b862[0].Data, ".")
			forged := p862[0] + "." + p862[1] + "." + p862[2] + "." + p861[3] // f862 with f861's signature
			fake.Reset()
			fake.PushCallback(adminChat, alice, msgID(byKey["f862"]), byKey["f862"].Str("text"), forged)
			fake.PushCallback(stranger, mallory, msgID(byKey["f861"]), byKey["f861"].Str("text"), b861[0].Data)
			fake.WaitCalls("answerCallbackQuery", 1, 3*time.Second)
			settle()
			if a := fake.Calls("answerCallbackQuery"); len(a) != 1 || a[0].Str("text") != "这个按钮已失效" {
				t.Fatalf("answers %+v", a)
			}
			if n := len(fake.Calls("editMessageText")); n != 0 || mustQueryInt(t, r, "SELECT COUNT(*) FROM tg_bot_ack") != 0 {
				t.Fatalf("forged / foreign button acted (edits %d)", n)
			}

			// ── 已续费 on an FLVX node ───────────────────────────────────
			due861 := twTime(2026, 11, 3, 0, 0).UnixMilli()
			fake.Reset()
			fake.PushCallback(adminChat, alice, msgID(byKey["f861"]), byKey["f861"].Str("text"), b861[0].Data)
			fake.WaitCalls("answerCallbackQuery", 1, 3*time.Second)
			settle()
			if a := fake.Calls("answerCallbackQuery"); len(a) != 1 || a[0].Str("text") != "已记下：已续费" {
				t.Fatalf("renewed answer %+v", a)
			}
			edits := fake.Calls("editMessageText")
			if len(edits) != 1 || edits[0].Str("text") != "⏰ Bestvm US 月付 明天到期（11-03）\n✅ 已续费（Alice），下次到期 12-03" || len(edits[0].Buttons()) != 0 {
				t.Fatalf("renewed edit %+v", edits)
			}
			if got := mustQueryInt64(t, r, "SELECT due_ms FROM tg_bot_ack WHERE machine_key = 'f861'"); got != due861 {
				t.Fatalf("ack due %d", got)
			}
			until, dismissed := mustQueryInt64Int(t, r, "SELECT expiry_reminder_dismissed_until, expiry_reminder_dismissed FROM node WHERE id = 861")
			if dismissed != 1 || until != due861/1000 {
				t.Fatalf("node dismissal until=%d dismissed=%d", until, dismissed)
			}
			if n := mustQueryInt(t, r, "SELECT COUNT(*) FROM node WHERE expiry_reminder_dismissed <> 0"); n != 1 {
				t.Fatalf("dismissed nodes %d", n)
			}
			nodeList := raw(e, "node/list", admin, map[string]interface{}{})
			if !strings.Contains(string(nodeList), fmt.Sprintf(`"expiryReminderDismissedUntil":%d`, due861/1000)) {
				t.Fatalf("node/list misses dismissed-until: %.400s", nodeList)
			}
			fake.Reset()
			fake.PushCallback(adminChat, alice, msgID(byKey["f861"]), byKey["f861"].Str("text"), b861[0].Data)
			fake.WaitCalls("answerCallbackQuery", 1, 3*time.Second)
			if a := fake.Calls("answerCallbackQuery"); len(a) != 1 || a[0].Str("text") != "之前已经标记过已续费" {
				t.Fatalf("second press %+v", a)
			}

			// ── 已续费 on a 救命版-only machine: FLVX table only ──────────
			bo1 := byKey["o1"].Buttons()[0]
			fake.Reset()
			fake.PushCallback(adminChat, alice, msgID(byKey["o1"]), byKey["o1"].Str("text"), bo1[0].Data)
			fake.WaitCalls("answerCallbackQuery", 1, 3*time.Second)
			settle()
			if n := mustQueryInt(t, r, "SELECT COUNT(*) FROM tg_bot_ack WHERE machine_key = 'o1'"); n != 1 {
				t.Fatal("救命版 ack missing")
			}
			if n := mustQueryInt(t, r, "SELECT COUNT(*) FROM node WHERE expiry_reminder_dismissed <> 0"); n != 1 {
				t.Fatalf("救命版 renew touched FLVX nodes (%d)", n)
			}
			if e := fake.Calls("editMessageText"); len(e) != 1 || !strings.HasSuffix(e[0].Str("text"), "✅ 已续费（Alice），下次到期 2027-11-02") {
				t.Fatalf("o1 edit %+v", e)
			}

			// ── 不再提醒这台 ─────────────────────────────────────────────
			fake.Reset()
			fake.PushCallback(adminChat, alice, msgID(byKey["f862"]), byKey["f862"].Str("text"), b862[1].Data)
			fake.WaitCalls("answerCallbackQuery", 1, 3*time.Second)
			settle()
			if a := fake.Calls("answerCallbackQuery"); len(a) != 1 || a[0].Str("text") != "以后不再提醒这台" {
				t.Fatalf("mute answer %+v", a)
			}
			if e := fake.Calls("editMessageText"); len(e) != 1 || !strings.Contains(e[0].Str("text"), "🔕 已不再提醒这台（Alice）") {
				t.Fatalf("mute edit %+v", e)
			}
			v = get(e)
			states := map[string]string{}
			for _, m := range v.Machines {
				states[m.Key] = fmt.Sprintf("muted=%v renewed=%v", m.Muted, m.Renewed)
			}
			if states["f862"] != "muted=true renewed=false" || states["f861"] != "muted=false renewed=true" || states["o1"] != "muted=false renewed=true" {
				t.Fatalf("states %v", states)
			}

			// ── restart the same day: nothing re-sent ───────────────────
			e.h.StopTelegramBot()
			if e.h.TelegramBotRunning() {
				t.Fatal("still running after stop")
			}
			fake.Reset()
			clk.Set(twTime(2026, 11, 2, 15, 0))
			e = start()
			wait("running after restart", e.h.TelegramBotRunning)
			settle()
			if n := len(fake.Calls("sendMessage")); n != 0 {
				t.Fatalf("restart re-sent %d", n)
			}
			e.h.StopTelegramBot()
			if err := r.UpsertConfig(tgbot.KeyLastRun, "", time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			e = start()
			wait("rerun marks the day", func() bool { return lastRun() == "2026-11-02" })
			if n := len(fake.Calls("sendMessage")); n != 0 {
				t.Fatalf("sent keys did not dedupe: %d", n)
			}

			// ── 11-03: Bestvm US rolled (30 days), no 「已过期」; others
			// not on a listed day; Mkcloud muted → nothing ───────────────
			clk.Set(twTime(2026, 11, 3, 10, 0))
			wait("11-03 run", func() bool { return lastRun() == "2026-11-03" })
			if n := len(fake.Calls("sendMessage")); n != 0 {
				t.Fatalf("11-03 sent %d: %+v", n, fake.Calls("sendMessage"))
			}
			v = get(e)
			for _, m := range v.Machines {
				if m.Key == "f861" && (m.Days != 30 || m.Renewed || m.NextDue != twTime(2026, 12, 3, 0, 0).UnixMilli()) {
					t.Fatalf("rolled machine %+v", m)
				}
			}
			e.h.StopTelegramBot()

			// ── 11-04: panel down at 10:00, up at 14:00 → caught up once;
			// Breadcloud JP (not clicked) is reminded again ──────────────
			clk.Set(twTime(2026, 11, 4, 14, 0))
			e = start()
			wait("11-04 catch-up", func() bool { return lastRun() == "2026-11-04" })
			sends = fake.Calls("sendMessage")
			if len(sends) != 1 || sends[0].Str("text") != "⏰ Breadcloud JP 年付 明天到期（11-05）" {
				t.Fatalf("11-04 sends %+v", sends)
			}

			// ── unbind → no goroutine ───────────────────────────────────
			if out := call(e, "config/tgbot/unbind", map[string]interface{}{"chatId": fmt.Sprint(adminChat.ID)}); out.Code != 0 {
				t.Fatalf("unbind %+v", out)
			}
			wait("stopped", func() bool { return !e.h.TelegramBotRunning() })
			e.h.StopTelegramBot()

			// ── never: token in logs / requests other than the API path;
			// 救命版 file untouched ───────────────────────────────────────
			if strings.Contains(logs.all(), tgContractToken) || strings.Contains(logs.all(), "AAContract") {
				t.Fatalf("token logged:\n%s", logs.all())
			}
			if got := fileSum(t, oldPath) + fileSum(t, oldPath+"-wal"); got != oldSum {
				t.Fatal("救命版 database file changed")
			}
			var oldDismissed int
			ro, err := sql.Open("sqlite", "file:"+oldPath+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer ro.Close()
			if err := ro.QueryRow("SELECT COUNT(*) FROM node WHERE expiry_reminder_dismissed <> 0").Scan(&oldDismissed); err != nil || oldDismissed != 0 {
				t.Fatalf("救命版 rows changed: %d %v", oldDismissed, err)
			}
			methods := map[string]bool{}
			for _, p := range fake.Paths() {
				methods[p[strings.LastIndex(p, "/")+1:]] = true
			}
			var ms []string
			for m := range methods {
				ms = append(ms, m)
			}
			sort.Strings(ms)
			t.Logf("bot API methods used: %v", ms)
		})
	}
}
