package tgbot_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/renewal"
	"go-backend/internal/store/model"
	"go-backend/internal/tgbot"
	"go-backend/internal/tgbot/tgtest"
)

const testToken = "7012345678:AAFakeTokenForTestsOnly_abcdefghijklmnop"

var secret = []byte("0123456789abcdef0123456789abcdef")

func tw(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, renewal.Zone)
}

func TestCallbackSigning(t *testing.T) {
	due := tw(2026, 11, 4, 0, 0).UnixMilli()
	data := tgbot.SignCallback(secret, tgbot.ActionRenewed, "f17", due)
	if len(data) > 64 {
		t.Fatalf("callback data %d bytes", len(data))
	}
	a, key, d, ok := tgbot.VerifyCallback(secret, data)
	if !ok || a != tgbot.ActionRenewed || key != "f17" || d != due {
		t.Fatalf("round trip %c %s %d %v", a, key, d, ok)
	}
	parts := strings.Split(data, ".")
	forged := []string{
		"m." + parts[1] + "." + parts[2] + "." + parts[3], // other action, same sig
		parts[0] + ".f18." + parts[2] + "." + parts[3],    // other machine
		parts[0] + ".o17." + parts[2] + "." + parts[3],    // other source
		parts[0] + "." + parts[1] + "." + strings.ToUpper(parts[2]) + "." + parts[3],
		parts[0] + "." + parts[1] + ".zzzzzzz." + parts[3], // other due date
		parts[0] + "." + parts[1] + "." + parts[2] + ".AAAAAAAAAAAAAAAA",
		data + "x", "", "r.f17", "x.f17.1.abc", "r.f0." + parts[2] + "." + parts[3],
	}
	for _, f := range forged {
		if _, _, _, ok := tgbot.VerifyCallback(secret, f); ok {
			t.Fatalf("forged accepted: %q", f)
		}
	}
	if _, _, _, ok := tgbot.VerifyCallback([]byte("another-secret-another-secret!!"), data); ok {
		t.Fatal("other secret accepted")
	}
	if _, _, _, ok := tgbot.VerifyCallback(nil, data); ok {
		t.Fatal("empty secret accepted")
	}
}

func TestSettingsParse(t *testing.T) {
	d := tgbot.ParseSettings("")
	if !d.Enabled || fmt.Sprint(d.Days) != "[7 3 1 0]" || d.Time != "10:00" {
		t.Fatalf("defaults %+v", d)
	}
	if got := tgbot.ParseSettings(`{"enabled":false,"days":[1,30,1],"time":"9:05","muted":["f3","bad","o2","f3"]}`); got.Enabled ||
		fmt.Sprint(got.Days) != "[30 1]" || got.Time != "09:05" || fmt.Sprint(got.Muted) != "[f3 o2]" {
		t.Fatalf("parsed %+v", got)
	}
	if got := tgbot.ParseSettings(`{"days":[-1],"time":"25:00"}`); fmt.Sprint(got.Days) != "[7 3 1 0]" || got.Time != "10:00" {
		t.Fatalf("broken fields keep defaults %+v", got)
	}
	if days, err := tgbot.ParseDays("7，3、1 0"); err != nil || fmt.Sprint(days) != "[7 3 1 0]" {
		t.Fatalf("days %v %v", days, err)
	}
	for _, bad := range []string{"61", "-1", "a", "1,2,3,4,5,6,7,8,9"} {
		if _, err := tgbot.ParseDays(bad); err == nil {
			t.Fatalf("days %q accepted", bad)
		}
	}
	if _, err := (tgbot.Settings{Days: []int{}, Time: "10:00"}).Normalize(); err == nil {
		t.Fatal("empty days accepted")
	}
	for _, bad := range []string{"24:00", "10:5", "10", "aa:bb"} {
		if _, err := tgbot.ParseClock(bad); err == nil {
			t.Fatalf("clock %q accepted", bad)
		}
	}
}

func TestSelectRollingAndTexts(t *testing.T) {
	now := tw(2026, 11, 2, 10, 0)
	day := int64(24 * time.Hour / time.Millisecond)
	midnight := tw(2026, 11, 3, 0, 0).UnixMilli() // due 00:00 TW tomorrow
	ms := []tgbot.Machine{
		{Key: "f17", Name: "Bestvm US", Source: tgbot.SourceFLVX, NodeID: 17, Anchor: tw(2026, 9, 3, 0, 0).UnixMilli(), Cycle: "month"}, // anchor 2 months back → rolls
		{Key: "f2", Name: "Mkcloud 深港ixp 1", Source: tgbot.SourceFLVX, NodeID: 2, Anchor: tw(2026, 11, 9, 0, 0).UnixMilli(), Cycle: "year"},
		{Key: "o1", Name: "PQS TW", Source: tgbot.SourceOld, Anchor: tw(2026, 11, 5, 0, 0).UnixMilli(), Cycle: "year"},
		{Key: "f9", Name: "No cycle overdue", Source: tgbot.SourceFLVX, NodeID: 9, Anchor: midnight - 5*day},
		{Key: "f10", Name: "No cycle today", Source: tgbot.SourceFLVX, NodeID: 10, Anchor: tw(2026, 11, 2, 18, 0).UnixMilli()},
		{Key: "f11", Name: "Muted", Source: tgbot.SourceFLVX, NodeID: 11, Anchor: midnight, Cycle: "month"},
		{Key: "f12", Name: "Acked", Source: tgbot.SourceFLVX, NodeID: 12, Anchor: midnight, Cycle: "month"},
		{Key: "f13", Name: "Two days", Source: tgbot.SourceFLVX, NodeID: 13, Anchor: tw(2026, 11, 4, 0, 0).UnixMilli(), Cycle: "month"},
	}
	st := tgbot.DefaultSettings().WithMuted("f11", true)
	acked := map[tgbot.AckKey]bool{{Key: "f12", Due: midnight}: true}
	got := tgbot.Select(ms, st, acked, now)
	var lines []string
	for _, r := range got {
		lines = append(lines, tgbot.ReminderText(r, now))
	}
	want := []string{
		"⏰ No cycle today 今天到期（11-02）",
		"⏰ Bestvm US 月付 明天到期（11-03）",
		"⏰ PQS TW 年付 3 天后到期（11-05） · 救命版",
		"⏰ Mkcloud 深港ixp 1 年付 7 天后到期（11-09）",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("reminders:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	// Rolling reset: one second after the due date the next cycle starts,
	// 30 days away → no reminder, nothing 「已过期」.
	after := time.UnixMilli(midnight + 1000)
	for _, r := range tgbot.Select(ms[:1], st, nil, after) {
		t.Fatalf("reminder right after the due date: %+v", r)
	}
	s, _ := ms[0].Status(after)
	if s.Days != 30 || s.NextDue <= midnight {
		t.Fatalf("rolled %+v", s)
	}
	next := tgbot.NextAfter(midnight, "month")
	if tgbot.DateLabel(next, now) != "12-03" {
		t.Fatalf("next %s", tgbot.DateLabel(next, now))
	}
	if l := tgbot.RenewedLine(next, "Alice", now); l != "✅ 已续费（Alice），下次到期 12-03" {
		t.Fatalf("renewed line %q", l)
	}
	if l := tgbot.RenewedLine(tgbot.NextAfter(tw(2026, 11, 9, 0, 0).UnixMilli(), "year"), "", now); l != "✅ 已续费，下次到期 2027-11-09" {
		t.Fatalf("renewed year %q", l)
	}
	if l := tgbot.RenewedLine(tgbot.NextAfter(midnight, ""), "", now); l != "✅ 已续费" {
		t.Fatalf("renewed no cycle %q", l)
	}
}

// memStore is an in-memory tgbot.Store.
type memStore struct {
	mu    sync.Mutex
	cfg   map[string]string
	chats []model.TGBotChat
	sent  []model.TGBotSent
	acks  []model.TGBotAck
	fail  error
}

func newMem() *memStore { return &memStore{cfg: map[string]string{}} }

func (s *memStore) GetConfigsByNames(names []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, n := range names {
		if v, ok := s.cfg[n]; ok {
			out[n] = v
		}
	}
	return out, s.fail
}
func (s *memStore) UpsertConfig(name, value string, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg[name] = value
	return nil
}
func (s *memStore) TGBotListChats() ([]model.TGBotChat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.TGBotChat(nil), s.chats...), nil
}
func (s *memStore) TGBotAddChat(c model.TGBotChat, max int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.chats {
		if x.ChatID == c.ChatID {
			return false, nil
		}
	}
	if len(s.chats) >= max {
		return false, errors.New("limit")
	}
	s.chats = append(s.chats, c)
	return true, nil
}
func (s *memStore) TGBotDeleteChat(id int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.chats {
		if x.ChatID == id {
			s.chats = append(s.chats[:i], s.chats[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}
func (s *memStore) TGBotDeleteAllChats() error {
	s.mu.Lock()
	s.chats = nil
	s.mu.Unlock()
	return nil
}
func (s *memStore) TGBotSetChatError(id int64, msg string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.chats {
		if s.chats[i].ChatID == id {
			s.chats[i].LastError, s.chats[i].LastErrorAt = msg, at
		}
	}
	return nil
}
func (s *memStore) TGBotListSent(minDue int64) ([]model.TGBotSent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.TGBotSent
	for _, x := range s.sent {
		if x.DueMs >= minDue {
			out = append(out, x)
		}
	}
	return out, nil
}
func (s *memStore) TGBotSentFor(key string, due int64) ([]model.TGBotSent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.TGBotSent
	for _, x := range s.sent {
		if x.MachineKey == key && x.DueMs == due {
			out = append(out, x)
		}
	}
	return out, nil
}
func (s *memStore) TGBotInsertSent(r model.TGBotSent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.sent {
		if x.MachineKey == r.MachineKey && x.DueMs == r.DueMs && x.Days == r.Days && x.ChatID == r.ChatID {
			return nil
		}
	}
	s.sent = append(s.sent, r)
	return nil
}
func (s *memStore) TGBotListAcks(minDue int64) ([]model.TGBotAck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.TGBotAck
	for _, x := range s.acks {
		if x.DueMs >= minDue {
			out = append(out, x)
		}
	}
	return out, nil
}
func (s *memStore) TGBotInsertAck(a model.TGBotAck) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.acks {
		if x.MachineKey == a.MachineKey && x.DueMs == a.DueMs {
			return false, nil
		}
	}
	s.acks = append(s.acks, a)
	return true, nil
}
func (s *memStore) TGBotPrune(before int64) error { return nil }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) Logf(f string, a ...interface{}) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
	l.mu.Unlock()
}
func (l *logSink) all() string { l.mu.Lock(); defer l.mu.Unlock(); return strings.Join(l.lines, "\n") }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestClientRedactsToken(t *testing.T) {
	// Nothing listens on port 1: the transport error carries the URL.
	c := tgbot.NewClient("http://127.0.0.1:1", testToken, nil)
	_, err := c.GetMe(context.Background())
	if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "AAFakeToken") {
		t.Fatalf("error leaks token or is nil: %v", err)
	}
	if !strings.Contains(err.Error(), "<token>") {
		t.Fatalf("expected redacted url, got %v", err)
	}
	f := tgtest.New(testToken)
	defer f.Close()
	bad := tgbot.NewClient(f.URL(), "7012345678:AAOtherTokenxxxxxxxxxxxxxxxxxxxxxxxxx", nil)
	_, err = bad.GetMe(context.Background())
	if !tgbot.Unauthorized(err) || strings.Contains(err.Error(), "AAOther") {
		t.Fatalf("unauthorized %v", err)
	}
	f.Fail("sendMessage", 403, 403, "Forbidden: bot was blocked by the user", 0, 1)
	f.Fail("sendMessage", 429, 429, "Too Many Requests: retry after 7", 7, 1)
	good := tgbot.NewClient(f.URL(), testToken, nil)
	_, err = good.SendMessage(context.Background(), 1, "x", nil)
	if !tgbot.IsPermanent(err) {
		t.Fatalf("403 not permanent: %v", err)
	}
	_, err = good.SendMessage(context.Background(), 1, "x", nil)
	if tgbot.IsPermanent(err) || tgbot.RetryAfter(err) != 7*time.Second {
		t.Fatalf("429: %v", err)
	}
	f.Fail("editMessageText", 400, 400, "Bad Request: message is not modified", 0, 1)
	if err := good.EditMessage(context.Background(), 1, 2, "x", nil); err != nil {
		t.Fatalf("not modified must be ok: %v", err)
	}
}

func goroutines() int {
	runtime.GC()
	return runtime.NumGoroutine()
}

// TestManagerIdleBindBackoff: no goroutine without token / chat; bind code
// starts exactly one; wrong chats and forged buttons are ignored; poll
// errors back off and log once; unbinding the last chat stops it.
func TestManagerIdleBindBackoff(t *testing.T) {
	f := tgtest.New(testToken)
	defer f.Close()
	st := newMem()
	clk := &clock{t: tw(2026, 11, 2, 9, 0)}
	logs := &logSink{}
	machines := []tgbot.Machine{{Key: "f17", Name: "Bestvm US", Source: tgbot.SourceFLVX, NodeID: 17, Anchor: tw(2026, 11, 3, 0, 0).UnixMilli(), Cycle: "month"}}
	var renewed []int64
	hc := &http.Client{}
	m := tgbot.New(st, tgbot.Hooks{
		Machines: func(time.Time) ([]tgbot.Machine, error) { return machines, nil },
		Renewed:  func(mc tgbot.Machine, due int64) error { renewed = append(renewed, due); return nil },
		AppName:  func() string { return "FLVX" },
	}, tgbot.Options{APIBase: f.URL(), HTTPClient: hc, Now: clk.Now, BackoffMin: 30 * time.Millisecond, BackoffMax: 120 * time.Millisecond, SendGap: -1, Logf: logs.Logf})
	defer m.Stop()

	base := goroutines()
	m.Enable()
	if m.Running() {
		t.Fatal("running without token")
	}
	if _, _, err := m.NewBindCode(); !errors.Is(err, tgbot.ErrNoToken) {
		t.Fatalf("bind code without token: %v", err)
	}
	if _, err := m.SaveToken(context.Background(), "not-a-token"); !errors.Is(err, tgbot.ErrBadToken) {
		t.Fatalf("bad token: %v", err)
	}
	prof, err := m.SaveToken(context.Background(), testToken)
	if err != nil || prof.Username != "flvx_test_bot" {
		t.Fatalf("save token %+v %v", prof, err)
	}
	if m.Running() {
		t.Fatal("running with token but no chat / code")
	}
	hc.CloseIdleConnections()
	if g := goroutines(); g > base {
		t.Fatalf("goroutines idle %d → %d", base, g)
	}
	code, exp, err := m.NewBindCode()
	if err != nil || len(code) != 8 || exp.Sub(clk.Now()) != 10*time.Minute {
		t.Fatalf("code %q %v %v", code, exp, err)
	}
	waitFor(t, "running", m.Running)

	stranger := tgbot.Chat{ID: 999, Type: "private", FirstName: "Mallory"}
	admin := tgbot.Chat{ID: 4242, Type: "private", FirstName: "Alice"}
	f.PushMessage(stranger, tgbot.User{ID: 999, FirstName: "Mallory"}, "/start WRONGCOD")
	f.PushMessage(stranger, tgbot.User{ID: 999, FirstName: "Mallory"}, "hello")
	f.PushMessage(admin, tgbot.User{ID: 4242, FirstName: "Alice"}, "/start "+code)
	f.PushMessage(stranger, tgbot.User{ID: 999, FirstName: "Mallory"}, "/start "+code) // used up
	waitFor(t, "bound", func() bool { c, _ := st.TGBotListChats(); return len(c) == 1 })
	sends := f.WaitCalls("sendMessage", 1, 2*time.Second)
	time.Sleep(60 * time.Millisecond)
	sends = f.Calls("sendMessage")
	if len(sends) != 1 || sends[0].Int("chat_id") != 4242 || !strings.Contains(sends[0].Str("text"), "已绑定") {
		t.Fatalf("sends after bind: %+v", sends)
	}
	chats, _ := st.TGBotListChats()
	if chats[0].Title != "Alice" || chats[0].Kind != "private" {
		t.Fatalf("chat %+v", chats[0])
	}

	// Forged / foreign buttons: ignored (unbound chat: no answer at all).
	cfg, _ := st.GetConfigsByNames([]string{tgbot.KeySecret})
	if len(cfg[tgbot.KeySecret]) != 64 {
		t.Fatalf("secret %q", cfg[tgbot.KeySecret])
	}
	due := tw(2026, 11, 3, 0, 0).UnixMilli()
	good := tgbot.SignCallback(secret, tgbot.ActionRenewed, "f17", due) // signed with a key the panel does not use
	f.Reset()
	f.PushCallback(stranger, tgbot.User{ID: 999}, 1, "x", good)
	f.PushCallback(admin, tgbot.User{ID: 4242, FirstName: "Alice"}, 2, "⏰ Bestvm US 月付 明天到期（11-03）", good)
	answers := f.WaitCalls("answerCallbackQuery", 1, 2*time.Second)
	time.Sleep(60 * time.Millisecond)
	answers = f.Calls("answerCallbackQuery")
	if len(answers) != 1 || answers[0].Str("text") != "这个按钮已失效" || len(f.Calls("editMessageText")) != 0 || len(renewed) != 0 {
		t.Fatalf("forged button acted: answers=%+v edits=%d renewed=%v", answers, len(f.Calls("editMessageText")), renewed)
	}
	if len(st.acks) != 0 {
		t.Fatal("ack stored for forged button")
	}

	// Poll errors: back-off 30 → 60 → 120 → 120 ms, one log line.
	f.Reset()
	f.Fail("getUpdates", 502, 502, "Bad Gateway", 0, 4)
	firstFailed := func() int {
		for i, c := range f.Calls("getUpdates") {
			if c.Failed {
				return i
			}
		}
		return -1
	}
	waitFor(t, "4 failed polls + 1 ok", func() bool { i := firstFailed(); return i >= 0 && len(f.Calls("getUpdates")) >= i+5 })
	polls := f.Calls("getUpdates")[firstFailed():]
	gaps := []time.Duration{}
	for i := 1; i < 5; i++ {
		if !polls[i-1].Failed || (i < 4 && !polls[i].Failed) {
			t.Fatalf("poll %d failed=%v", i, polls[i].Failed)
		}
		gaps = append(gaps, polls[i].At.Sub(polls[i-1].At))
	}
	wantMin := []time.Duration{30, 60, 120, 120}
	for i, g := range gaps {
		if g < wantMin[i]*time.Millisecond-5*time.Millisecond || g > wantMin[i]*time.Millisecond+150*time.Millisecond {
			t.Fatalf("backoff gaps %v", gaps)
		}
	}
	if n := strings.Count(logs.all(), "poll:"); n != 1 {
		t.Fatalf("poll error lines %d:\n%s", n, logs.all())
	}
	if strings.Contains(logs.all(), testToken) {
		t.Fatal("token in logs")
	}

	// Unbind the last chat → the goroutine stops.
	if err := m.Unbind(4242); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stopped", func() bool { return !m.Running() })
}

// TestManagerDailyDedupeRetry: the daily run sends each reminder once per
// chat, a transient failure is retried without repeats, a permanent one
// marks the chat, missed time is caught up the same day.
func TestManagerDailyDedupeRetry(t *testing.T) {
	f := tgtest.New(testToken)
	defer f.Close()
	st := newMem()
	clk := &clock{t: tw(2026, 11, 2, 9, 59)}
	midnight := tw(2026, 11, 3, 0, 0).UnixMilli()
	machines := []tgbot.Machine{
		{Key: "f17", Name: "Bestvm US", Source: tgbot.SourceFLVX, NodeID: 17, Anchor: midnight, Cycle: "month"},
		{Key: "o1", Name: "PQS TW", Source: tgbot.SourceOld, Anchor: tw(2026, 11, 5, 0, 0).UnixMilli(), Cycle: "year"},
		{Key: "f2", Name: "Not today", Source: tgbot.SourceFLVX, NodeID: 2, Anchor: tw(2026, 11, 7, 0, 0).UnixMilli(), Cycle: "year"},
	}
	st.chats = []model.TGBotChat{{ChatID: 1, Kind: "private", Title: "A"}, {ChatID: 2, Kind: "group", Title: "Ops"}}
	newMgr := func() *tgbot.Manager {
		return tgbot.New(st, tgbot.Hooks{Machines: func(time.Time) ([]tgbot.Machine, error) { return machines, nil }},
			tgbot.Options{APIBase: f.URL(), Now: clk.Now, BackoffMin: 20 * time.Millisecond, BackoffMax: 40 * time.Millisecond, SendGap: -1, Logf: func(string, ...interface{}) {}})
	}
	m := newMgr()
	if _, err := m.SaveToken(context.Background(), testToken); err != nil {
		t.Fatal(err)
	}
	m.Enable()
	waitFor(t, "running", m.Running)
	time.Sleep(80 * time.Millisecond)
	if n := len(f.Calls("sendMessage")); n != 0 {
		t.Fatalf("sent before 10:00: %d", n)
	}
	// 10:00: chat 2 fails transiently once, then works.
	f.Fail("sendMessage", 502, 502, "Bad Gateway", 0, 1)
	clk.Set(tw(2026, 11, 2, 10, 0))
	waitFor(t, "failed send", func() bool { return len(f.Calls("sendMessage")) >= 1 })
	time.Sleep(30 * time.Millisecond)
	if n := len(st.sent); n != 0 {
		t.Fatalf("sent after transient failure %d", n)
	}
	clk.Set(tw(2026, 11, 2, 10, 1)) // retry after the back-off
	waitFor(t, "4 reminders", func() bool { st.mu.Lock(); defer st.mu.Unlock(); return len(st.sent) == 4 })
	waitFor(t, "last run", func() bool {
		c, _ := st.GetConfigsByNames([]string{tgbot.KeyLastRun})
		return c[tgbot.KeyLastRun] == "2026-11-02"
	})
	sends := f.Calls("sendMessage")
	if len(sends) != 5 { // 1 failed + 4 delivered, none twice
		t.Fatalf("send calls %d", len(sends))
	}
	seen := map[string]int{}
	for _, s := range sends[1:] {
		seen[fmt.Sprintf("%d %s", s.Int("chat_id"), s.Str("text"))]++
		if b := s.Buttons(); len(b) != 1 || len(b[0]) != 2 || b[0][0].Text != tgbot.LabelRenewed || b[0][1].Text != tgbot.LabelMute {
			t.Fatalf("buttons %+v", b)
		}
	}
	for k, n := range seen {
		if n != 1 {
			t.Fatalf("%s sent %d times", k, n)
		}
	}
	m.Stop()

	// Restart the same day: nothing is sent again (last run + sent keys).
	f.Reset()
	clk.Set(tw(2026, 11, 2, 15, 0))
	m2 := newMgr()
	m2.Enable()
	waitFor(t, "running", m2.Running)
	time.Sleep(80 * time.Millisecond)
	if n := len(f.Calls("sendMessage")); n != 0 {
		t.Fatalf("restart re-sent %d", n)
	}
	m2.Stop()

	// Even without the last-run marker the sent keys dedupe.
	delete(st.cfg, tgbot.KeyLastRun)
	m3 := newMgr()
	m3.Enable()
	waitFor(t, "last run again", func() bool {
		c, _ := st.GetConfigsByNames([]string{tgbot.KeyLastRun})
		return c[tgbot.KeyLastRun] == "2026-11-02"
	})
	if n := len(f.Calls("sendMessage")); n != 0 {
		t.Fatalf("dedupe failed, re-sent %d", n)
	}
	m3.Stop()

	// Next day, panel down at 10:00, back at 14:00 → caught up once; chat 2
	// blocked the bot (403) → marked, chat 1 still gets it.
	f.Reset()
	clk.Set(tw(2026, 11, 3, 14, 0))
	f.Fail("sendMessage", 403, 403, "Forbidden: bot was kicked from the group chat", 0, 1)
	machines = append(machines, tgbot.Machine{Key: "f5", Name: "Due 11-04", Source: tgbot.SourceFLVX, NodeID: 5, Anchor: tw(2026, 11, 4, 0, 0).UnixMilli(), Cycle: "month"})
	m4 := newMgr()
	m4.Enable()
	waitFor(t, "11-03 run", func() bool {
		c, _ := st.GetConfigsByNames([]string{tgbot.KeyLastRun})
		return c[tgbot.KeyLastRun] == "2026-11-03"
	})
	var texts []string
	for _, s := range f.Calls("sendMessage") {
		texts = append(texts, fmt.Sprintf("%d %s", s.Int("chat_id"), s.Str("text")))
	}
	sort.Strings(texts)
	// 11-03 14:00: Bestvm US rolled to 12-03 (30 days, no reminder), PQS TW
	// 2 days (not in the list), Due 11-04 = 1 day → one message per chat.
	if len(texts) != 2 || !strings.Contains(texts[0], "Due 11-04 月付 明天到期（11-04）") {
		t.Fatalf("11-03 sends %v", texts)
	}
	chats, _ := st.TGBotListChats()
	blocked := 0
	for _, c := range chats {
		if c.LastError != "" {
			blocked++
			if !strings.Contains(c.LastError, "kicked") {
				t.Fatalf("chat error %q", c.LastError)
			}
		}
	}
	if blocked != 1 {
		t.Fatalf("blocked chats %d", blocked)
	}
	m4.Stop()
}
