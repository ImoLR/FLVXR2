package tgbot

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"go-backend/internal/renewal"
	"go-backend/internal/store/model"
)

// vite_config keys (all hidden: repo.IsHiddenConfigKey).
const (
	KeyToken    = "tg_bot_token"
	KeySecret   = "tg_bot_secret"
	KeyProfile  = "tg_bot_profile"
	KeySettings = "tg_bot_settings"
	KeyLastRun  = "tg_bot_last_run"
)

// keep sent / ack rows this long after their due date.
const keepRows = 90 * 24 * time.Hour

// Store is what the bot needs from the database (*repo.Repository).
type Store interface {
	GetConfigsByNames(names []string) (map[string]string, error)
	UpsertConfig(name, value string, now int64) error
	TGBotListChats() ([]model.TGBotChat, error)
	TGBotAddChat(c model.TGBotChat, max int) (bool, error)
	TGBotDeleteChat(chatID int64) (bool, error)
	TGBotDeleteAllChats() error
	TGBotSetChatError(chatID int64, msg string, at int64) error
	TGBotListSent(minDue int64) ([]model.TGBotSent, error)
	TGBotSentFor(key string, due int64) ([]model.TGBotSent, error)
	TGBotInsertSent(s model.TGBotSent) error
	TGBotListAcks(minDue int64) ([]model.TGBotAck, error)
	TGBotInsertAck(a model.TGBotAck) (bool, error)
	TGBotPrune(before int64) error
}

// Hooks connect the bot to the panel.
type Hooks struct {
	// Machines: every machine with an expiry (FLVX + 救命版-only, deduped).
	Machines func(now time.Time) ([]Machine, error)
	// Renewed: 「已续费」 on an FLVX node for the given (current) due date —
	// the panel's own reminder is dismissed until then.
	Renewed func(m Machine, due int64) error
	// AppName: the panel name used in bound / test messages.
	AppName func() string
}

// Options tune the runtime (tests shorten the waits).
type Options struct {
	APIBase     string
	HTTPClient  *http.Client
	Now         func() time.Time
	PollTimeout time.Duration // long poll, default 50 s
	BackoffMin  time.Duration // default 30 s
	BackoffMax  time.Duration // default 10 min
	SendGap     time.Duration // between two sends, default 1 s
	CodeTTL     time.Duration // bind code, default 10 min
	LogEvery    time.Duration // error log lines, default 10 min
	CallTimeout time.Duration // send / edit / answer, default 15 s
	Logf        func(format string, args ...interface{})
}

func (o *Options) defaults() {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.PollTimeout <= 0 {
		o.PollTimeout = 50 * time.Second
	}
	if o.BackoffMin <= 0 {
		o.BackoffMin = 30 * time.Second
	}
	if o.BackoffMax <= 0 {
		o.BackoffMax = 10 * time.Minute
	}
	if o.SendGap < 0 {
		o.SendGap = 0
	} else if o.SendGap == 0 {
		o.SendGap = time.Second
	}
	if o.CodeTTL <= 0 {
		o.CodeTTL = 10 * time.Minute
	}
	if o.LogEvery <= 0 {
		o.LogEvery = 10 * time.Minute
	}
	if o.CallTimeout <= 0 {
		o.CallTimeout = 15 * time.Second
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	if strings.TrimSpace(o.APIBase) == "" {
		o.APIBase = DefaultAPIBase
	}
	if o.HTTPClient == nil {
		// One shared pool: the long poll + the odd send / admin call.
		o.HTTPClient = &http.Client{Transport: newTransport()}
	}
}

// Profile is the bot as getMe described it.
type Profile struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Status is the admin card's view of the bot (never the token).
type Status struct {
	TokenSet      bool              `json:"tokenSet"`
	Profile       Profile           `json:"profile"`
	Running       bool              `json:"running"`
	LastError     string            `json:"lastError"`
	LastErrorAt   int64             `json:"lastErrorAt"`
	LastOKAt      int64             `json:"lastOkAt"`
	LastRun       string            `json:"lastRun"`
	NextRunAt     int64             `json:"nextRunAt"`
	BindCode      string            `json:"bindCode,omitempty"`
	BindExpiresAt int64             `json:"bindExpiresAt,omitempty"`
	Chats         []model.TGBotChat `json:"chats"`
	Settings      Settings          `json:"settings"`
}

// Stats: resource numbers for tests / the demo.
type Stats struct {
	Starts        int64
	Polls         int64
	Updates       int64
	Sends         int64
	Edits         int64
	DailyRuns     int64
	LastDailyMs   float64
	LastDailySent int
}

type sentKey struct {
	key  string
	due  int64
	days int
	chat int64
}

// Manager runs the bot: no goroutine unless a token is set and a chat is
// bound (or a bind code is waiting); then exactly one.
type Manager struct {
	store Store
	hooks Hooks
	opt   Options

	mu       sync.Mutex
	enabled  bool
	token    string
	secret   []byte
	profile  Profile
	settings Settings
	lastRun  string
	chats    map[int64]bool
	code     string
	codeExp  time.Time

	running  bool
	gen      int64
	cancel   context.CancelFunc
	runToken string
	wg       sync.WaitGroup

	lastErr      string
	lastErrAt    time.Time
	lastOK       time.Time
	dailyRetryAt time.Time
	dailyBackoff time.Duration
	stats        Stats

	logMu    sync.Mutex
	logAt    time.Time
	logMuted int
}

// New builds an idle manager (Enable starts it when there is work).
func New(store Store, hooks Hooks, opt Options) *Manager {
	opt.defaults()
	return &Manager{store: store, hooks: hooks, opt: opt, settings: DefaultSettings(), chats: map[int64]bool{}}
}

// Enable lets the bot run (StartBackgroundJobs) and starts it if needed.
func (m *Manager) Enable() {
	m.mu.Lock()
	m.enabled = true
	m.mu.Unlock()
	m.Reconcile()
}

// Stop stops the bot goroutine and waits for it.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.enabled = false
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.running = false
	m.gen++
	m.mu.Unlock()
	m.wg.Wait()
}

// Reconcile reloads token, chats and settings from the database and starts,
// restarts (token changed) or stops the goroutine.
func (m *Manager) Reconcile() {
	cfg, err := m.store.GetConfigsByNames([]string{KeyToken, KeySecret, KeyProfile, KeySettings, KeyLastRun})
	if err != nil {
		m.noteErr("load settings", err)
		return
	}
	chats, err := m.store.TGBotListChats()
	if err != nil {
		m.noteErr("load chats", err)
		return
	}
	var prof Profile
	_ = json.Unmarshal([]byte(cfg[KeyProfile]), &prof)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = strings.TrimSpace(cfg[KeyToken])
	m.secret = decodeSecret(cfg[KeySecret])
	m.profile = prof
	m.settings = ParseSettings(cfg[KeySettings])
	m.lastRun = strings.TrimSpace(cfg[KeyLastRun])
	m.chats = make(map[int64]bool, len(chats))
	for _, c := range chats {
		m.chats[c.ChatID] = true
	}
	m.applyLocked()
}

// applyLocked starts / stops the goroutine to match the state.
func (m *Manager) applyLocked() {
	want := m.shouldRunLocked(m.opt.Now())
	if m.running && (!want || m.runToken != m.token) {
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		m.running = false
		m.gen++
		if !want {
			m.opt.Logf("[tgbot] stopped")
		}
	}
	if want && !m.running {
		ctx, cancel := context.WithCancel(context.Background())
		m.gen++
		m.running, m.cancel, m.runToken = true, cancel, m.token
		m.stats.Starts++
		client := NewClient(m.opt.APIBase, m.token, m.opt.HTTPClient)
		m.wg.Add(1)
		go m.loop(ctx, m.gen, client)
		who := m.profile.Username
		if who == "" {
			who = "bot"
		}
		m.opt.Logf("[tgbot] polling as @%s (%d chats)", who, len(m.chats))
	}
}

func (m *Manager) shouldRunLocked(now time.Time) bool {
	if !m.enabled || m.token == "" || len(m.secret) == 0 {
		return false
	}
	return len(m.chats) > 0 || (m.code != "" && now.Before(m.codeExp))
}

func decodeSecret(s string) []byte {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) < 16 {
		return nil
	}
	return b
}

func nextBackoff(cur, min, max time.Duration) time.Duration {
	if cur <= 0 {
		return min
	}
	cur *= 2
	if cur > max {
		return max
	}
	return cur
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// loop is the one bot goroutine: daily reminders when due, then one long
// poll; errors back off 30 s → 10 min.
func (m *Manager) loop(ctx context.Context, gen int64, client *Client) {
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		if m.gen == gen {
			m.running = false
			if m.cancel != nil {
				m.cancel()
				m.cancel = nil
			}
		}
		m.mu.Unlock()
	}()
	var offset int64
	var backoff time.Duration
	for ctx.Err() == nil {
		now := m.opt.Now()
		m.mu.Lock()
		if m.gen != gen || !m.shouldRunLocked(now) {
			if m.gen == gen {
				m.opt.Logf("[tgbot] stopped (nothing to do)")
			}
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()

		m.maybeDaily(ctx, client, now)

		timeout := m.pollTimeout(now)
		pctx, cancel := context.WithTimeout(ctx, timeout+15*time.Second)
		updates, err := client.GetUpdates(pctx, offset, timeout)
		cancel()
		m.mu.Lock()
		m.stats.Polls++
		m.mu.Unlock()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			backoff = nextBackoff(backoff, m.opt.BackoffMin, m.opt.BackoffMax)
			wait := backoff
			if ra := RetryAfter(err); ra > wait {
				wait = ra
			}
			m.noteErr("poll", err)
			if !sleepCtx(ctx, wait) {
				return
			}
			continue
		}
		backoff = 0
		m.noteOK()
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			m.mu.Lock()
			m.stats.Updates++
			m.mu.Unlock()
			m.handleUpdate(ctx, client, u)
		}
	}
}

// dayKey is the Taiwan calendar date.
func dayKey(t time.Time) string { return t.In(renewal.Zone).Format("2006-01-02") }

func minuteOfDay(t time.Time) int {
	l := t.In(renewal.Zone)
	return l.Hour()*60 + l.Minute()
}

// nextDailyLocked: when the next daily run is due (zero = nothing pending).
func (m *Manager) nextDailyLocked(now time.Time) time.Time {
	if !m.settings.Enabled || len(m.chats) == 0 {
		return time.Time{}
	}
	l := now.In(renewal.Zone)
	send := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, renewal.Zone).Add(time.Duration(m.settings.SendMinute()) * time.Minute)
	if m.lastRun == dayKey(now) {
		send = send.AddDate(0, 0, 1)
	}
	if m.dailyRetryAt.After(send) {
		send = m.dailyRetryAt
	}
	return send
}

// pollTimeout: the long-poll length, shortened so the send time is on time.
func (m *Manager) pollTimeout(now time.Time) time.Duration {
	m.mu.Lock()
	next := m.nextDailyLocked(now)
	m.mu.Unlock()
	t := m.opt.PollTimeout
	if !next.IsZero() {
		if until := next.Sub(now); until < t {
			t = until.Truncate(time.Second) + time.Second
		}
	}
	if t < time.Second {
		t = time.Second
	}
	return t
}

// maybeDaily runs today's reminders once at / after the send time (also
// right after a start that missed it, if it is still the same day).
func (m *Manager) maybeDaily(ctx context.Context, client *Client, now time.Time) {
	m.mu.Lock()
	next := m.nextDailyLocked(now)
	st, secret := m.settings, m.secret
	m.mu.Unlock()
	if next.IsZero() || now.Before(next) {
		return
	}
	today := dayKey(now)
	err := m.runDaily(ctx, client, now, st, secret)
	if ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	if err == nil {
		m.lastRun, m.dailyBackoff, m.dailyRetryAt = today, 0, time.Time{}
	} else {
		m.dailyBackoff = nextBackoff(m.dailyBackoff, m.opt.BackoffMin, m.opt.BackoffMax)
		wait := m.dailyBackoff
		if ra := RetryAfter(err); ra > wait {
			wait = ra
		}
		m.dailyRetryAt = now.Add(wait)
	}
	m.mu.Unlock()
	if err != nil {
		m.noteErr("reminders", err)
		return
	}
	if err := m.store.UpsertConfig(KeyLastRun, today, now.UnixMilli()); err != nil {
		m.noteErr("save last run", err)
	}
	m.mu.Lock()
	sent, ms := m.stats.LastDailySent, m.stats.LastDailyMs
	m.mu.Unlock()
	// One line per day.
	m.opt.Logf("[tgbot] daily reminders %s: %d messages sent, %.1f ms", today, sent, ms)
}

// runDaily sends today's reminders that were not sent yet. A transient
// failure stops the run (retried later, sent ones are not repeated); a
// permanent one (bot blocked, chat gone) marks that chat and goes on.
func (m *Manager) runDaily(ctx context.Context, client *Client, now time.Time, st Settings, secret []byte) error {
	started := time.Now()
	sentCount := 0
	defer func() {
		m.mu.Lock()
		m.stats.DailyRuns++
		m.stats.LastDailyMs = float64(time.Since(started).Microseconds()) / 1000
		m.stats.LastDailySent = sentCount
		m.mu.Unlock()
	}()
	if m.hooks.Machines == nil {
		return errors.New("no machine source")
	}
	machines, partial := m.hooks.Machines(now)
	if partial != nil && !IsPartial(partial) {
		return fmt.Errorf("machines: %w", partial)
	}
	minDue := now.Add(-48 * time.Hour).UnixMilli()
	acks, err := m.store.TGBotListAcks(minDue)
	if err != nil {
		return err
	}
	acked := make(map[AckKey]bool, len(acks))
	for _, a := range acks {
		acked[AckKey{Key: a.MachineKey, Due: a.DueMs}] = true
	}
	reminders := Select(machines, st, acked, now)
	if len(reminders) > 0 {
		sent, err := m.store.TGBotListSent(minDue)
		if err != nil {
			return err
		}
		done := make(map[sentKey]bool, len(sent))
		for _, s := range sent {
			done[sentKey{s.MachineKey, s.DueMs, s.Days, s.ChatID}] = true
		}
		chats, err := m.store.TGBotListChats()
		if err != nil {
			return err
		}
		dead := map[int64]bool{}
		first := true
		for _, r := range reminders {
			text := ReminderText(r, now)
			buttons := ReminderButtons(secret, r.Machine.Key, r.Due)
			for i := range chats {
				c := &chats[i]
				k := sentKey{r.Machine.Key, r.Due, r.Days, c.ChatID}
				if done[k] || dead[c.ChatID] {
					continue
				}
				if !first && !sleepCtx(ctx, m.opt.SendGap) {
					return ctx.Err()
				}
				first = false
				sctx, cancel := context.WithTimeout(ctx, m.opt.CallTimeout)
				id, err := client.SendMessage(sctx, c.ChatID, text, buttons)
				cancel()
				if err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if IsPermanent(err) {
						dead[c.ChatID] = true
						_ = m.store.TGBotSetChatError(c.ChatID, m.redact(err.Error()), now.UnixMilli())
						continue
					}
					return err
				}
				sentCount++
				m.mu.Lock()
				m.stats.Sends++
				m.mu.Unlock()
				done[k] = true
				if err := m.store.TGBotInsertSent(model.TGBotSent{MachineKey: r.Machine.Key, DueMs: r.Due, Days: r.Days, ChatID: c.ChatID,
					MessageID: id, Text: text, SentTime: now.UnixMilli()}); err != nil {
					m.noteErr("save sent reminder", err)
				}
				if c.LastError != "" {
					_ = m.store.TGBotSetChatError(c.ChatID, "", 0)
					c.LastError = ""
				}
			}
		}
	}
	if err := m.store.TGBotPrune(now.Add(-keepRows).UnixMilli()); err != nil {
		m.noteErr("prune", err)
	}
	// Incomplete list: what we had is sent (and not repeated); the rest is
	// retried with the back-off.
	return partial
}

func (m *Manager) handleUpdate(ctx context.Context, client *Client, u Update) {
	switch {
	case u.CallbackQuery != nil:
		m.handleCallback(ctx, client, *u.CallbackQuery)
	case u.Message != nil:
		m.handleMessage(ctx, client, *u.Message)
	}
}

// handleMessage only knows `/start <code>` (also /start@bot <code>); every
// other message is ignored without an answer.
func (m *Manager) handleMessage(ctx context.Context, client *Client, msg Message) {
	fields := strings.Fields(msg.Text)
	if len(fields) != 2 || msg.Chat.ID == 0 || msg.Chat.Type == "channel" {
		return
	}
	cmd := fields[0]
	m.mu.Lock()
	username := m.profile.Username
	m.mu.Unlock()
	if cmd != "/start" && !(strings.HasPrefix(cmd, "/start@") && username != "" && strings.EqualFold(cmd[len("/start@"):], username)) {
		return
	}
	now := m.opt.Now()
	if !m.consumeCode(fields[1], now) {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, m.opt.CallTimeout)
	defer cancel()
	chats, err := m.store.TGBotListChats()
	if err != nil {
		m.noteErr("bind", err)
		return
	}
	bound := false
	for _, c := range chats {
		bound = bound || c.ChatID == msg.Chat.ID
	}
	if !bound && len(chats) >= MaxChats {
		_, _ = client.SendMessage(cctx, msg.Chat.ID, LimitText(), nil)
		return
	}
	title := oneLine(msg.Chat.DisplayName(), 120)
	if _, err := m.store.TGBotAddChat(model.TGBotChat{ChatID: msg.Chat.ID, Kind: msg.Chat.Type, Title: title, CreatedTime: now.UnixMilli()}, MaxChats); err != nil {
		m.noteErr("bind", err)
		_, _ = client.SendMessage(cctx, msg.Chat.ID, LimitText(), nil)
		return
	}
	m.mu.Lock()
	m.chats[msg.Chat.ID] = true
	st := m.settings
	m.mu.Unlock()
	m.opt.Logf("[tgbot] chat bound (%s)", msg.Chat.Type)
	app := ""
	if m.hooks.AppName != nil {
		app = m.hooks.AppName()
	}
	if _, err := client.SendMessage(cctx, msg.Chat.ID, BoundText(app, st), nil); err != nil {
		m.noteErr("bind reply", err)
	}
}

// consumeCode: the pending bind code, once, before it expires.
func (m *Manager) consumeCode(code string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.code == "" || !now.Before(m.codeExp) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(code))), []byte(m.code)) != 1 {
		return false
	}
	m.code, m.codeExp = "", time.Time{}
	return true
}

// handleCallback: a reminder button from a bound chat with a valid
// signature; anything else is ignored (unbound chats get no answer at all).
func (m *Manager) handleCallback(ctx context.Context, client *Client, cq CallbackQuery) {
	if cq.Message == nil {
		return
	}
	chatID := cq.Message.Chat.ID
	m.mu.Lock()
	bound := m.chats[chatID]
	secret := m.secret
	m.mu.Unlock()
	if !bound {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 2*m.opt.CallTimeout)
	defer cancel()
	answer := func(text string) {
		_ = client.AnswerCallback(cctx, cq.ID, text)
	}
	action, key, due, ok := VerifyCallback(secret, cq.Data)
	if !ok {
		answer("这个按钮已失效")
		return
	}
	now := m.opt.Now()
	var machine *Machine
	if m.hooks.Machines != nil {
		ms, err := m.hooks.Machines(now)
		if err != nil && !IsPartial(err) {
			answer("暂时读不到机器列表，请稍后再试")
			return
		}
		for i := range ms {
			if ms[i].Key == key {
				machine = &ms[i]
				break
			}
		}
		if machine == nil && err != nil && strings.HasPrefix(key, "o") {
			answer("暂时读不到这台机器（救命版数据读不到），请稍后再试")
			return
		}
	}
	by := cq.From.DisplayName()
	switch action {
	case ActionRenewed:
		if machine == nil {
			m.editCopies(cctx, client, key, due, cq.Message, GoneLine)
			answer("这台机器已不在面板里")
			return
		}
		cur, hasExpiry := machine.Status(now)
		if due < now.UnixMilli() {
			next := int64(0)
			if hasExpiry && cur.Cycle != "" {
				next = cur.NextDue
			}
			m.editCopies(cctx, client, key, due, cq.Message, PassedLine(next, now))
			answer("这一期已经过了")
			return
		}
		inserted, err := m.store.TGBotInsertAck(model.TGBotAck{MachineKey: key, DueMs: due, ChatID: chatID, ByName: by, ActedTime: now.UnixMilli()})
		if err != nil {
			m.noteErr("save renewed", err)
			answer("保存失败，请稍后再试")
			return
		}
		// The 节点 page's own reminder: only for the cycle this button is
		// about (the node's dates may have been edited since).
		if machine.NodeID > 0 && hasExpiry && cur.NextDue == due && m.hooks.Renewed != nil {
			if err := m.hooks.Renewed(*machine, due); err != nil {
				m.noteErr("dismiss node reminder", err)
			}
		}
		m.editCopies(cctx, client, key, due, cq.Message, RenewedLine(NextAfter(due, machine.Cycle), by, now))
		if inserted {
			answer("已记下：已续费")
		} else {
			answer("之前已经标记过已续费")
		}
	case ActionMute:
		m.mu.Lock()
		st := m.settings.WithMuted(key, true)
		m.settings = st
		m.mu.Unlock()
		if err := m.store.UpsertConfig(KeySettings, st.JSON(), now.UnixMilli()); err != nil {
			m.noteErr("save mute", err)
			answer("保存失败，请稍后再试")
			return
		}
		m.editCopies(cctx, client, key, due, cq.Message, MutedLine(by))
		answer("以后不再提醒这台")
	}
}

// editCopies appends line to every sent copy of the reminder (all bound
// chats, every day count) and the pressed message, removing the buttons.
func (m *Manager) editCopies(ctx context.Context, client *Client, key string, due int64, pressed *Message, line string) {
	rows, err := m.store.TGBotSentFor(key, due)
	if err != nil {
		m.noteErr("load sent reminders", err)
	}
	m.mu.Lock()
	bound := make(map[int64]bool, len(m.chats))
	for id := range m.chats {
		bound[id] = true
	}
	m.mu.Unlock()
	type msgRef struct{ chat, id int64 }
	done := map[msgRef]bool{}
	edit := func(chat, id int64, base string) {
		if id == 0 || done[msgRef{chat, id}] {
			return
		}
		done[msgRef{chat, id}] = true
		if err := client.EditMessage(ctx, chat, id, base+"\n"+line, nil); err != nil {
			m.noteErr("edit", err)
			return
		}
		m.mu.Lock()
		m.stats.Edits++
		m.mu.Unlock()
	}
	if pressed != nil {
		edit(pressed.Chat.ID, pressed.MessageID, firstLine(pressed.Text))
	}
	for _, r := range rows {
		if bound[r.ChatID] {
			edit(r.ChatID, r.MessageID, r.Text)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func (m *Manager) redact(s string) string {
	m.mu.Lock()
	token := m.token
	m.mu.Unlock()
	return Redact(s, token)
}

// noteErr keeps the last error for the card and logs at most one line per
// LogEvery (the first one at once, then a counted summary).
func (m *Manager) noteErr(what string, err error) {
	if err == nil {
		return
	}
	msg := what + ": " + m.redact(err.Error())
	m.mu.Lock()
	m.lastErr, m.lastErrAt = oneLine(msg, 200), m.opt.Now()
	m.mu.Unlock()
	m.logLine(msg)
}

func (m *Manager) noteOK() {
	now := m.opt.Now()
	m.mu.Lock()
	hadErr := m.lastErr != ""
	m.lastOK = now
	m.lastErr = ""
	m.mu.Unlock()
	if hadErr {
		m.logLine("telegram reachable again")
	}
}

func (m *Manager) logLine(line string) {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	now := m.opt.Now()
	if !m.logAt.IsZero() && now.Sub(m.logAt) < m.opt.LogEvery {
		m.logMuted++
		return
	}
	if m.logMuted > 0 {
		line = fmt.Sprintf("%s (+%d more since the last line)", line, m.logMuted)
	}
	m.logAt, m.logMuted = now, 0
	m.opt.Logf("[tgbot] %s", m.redact(line))
}

// ─── admin API ───────────────────────────────────────────────────────

var tokenRe = regexp.MustCompile(`^[0-9]{5,16}:[A-Za-z0-9_-]{30,80}$`)

// Errors shown to the admin.
var (
	ErrNoToken   = errors.New("还没有设置 bot token")
	ErrNoChats   = errors.New("还没有绑定的聊天")
	ErrBadToken  = errors.New("token 格式不对：应该是 BotFather 给的「数字:字母串」")
	ErrNotBot    = errors.New("这个 token 不是 bot 的")
	ErrRefused   = errors.New("Telegram 拒绝了这个 token（无效或已被撤销）")
	ErrChatLimit = fmt.Errorf("最多只能绑定 %d 个聊天", MaxChats)
)

// SaveToken checks the token with getMe and stores it. A token of another
// bot unbinds the old chats (they never talked to the new bot).
func (m *Manager) SaveToken(ctx context.Context, token string) (Profile, error) {
	token = strings.TrimSpace(token)
	if !tokenRe.MatchString(token) {
		return Profile{}, ErrBadToken
	}
	cctx, cancel := context.WithTimeout(ctx, m.opt.CallTimeout)
	defer cancel()
	u, err := NewClient(m.opt.APIBase, token, m.opt.HTTPClient).GetMe(cctx)
	if err != nil {
		if Unauthorized(err) {
			return Profile{}, ErrRefused
		}
		return Profile{}, fmt.Errorf("连不上 Telegram：%s", Redact(err.Error(), token))
	}
	if !u.IsBot {
		return Profile{}, ErrNotBot
	}
	prof := Profile{ID: u.ID, Username: u.Username, Name: oneLine(u.FirstName, 64)}
	now := m.opt.Now().UnixMilli()
	cfg, err := m.store.GetConfigsByNames([]string{KeySecret, KeyProfile})
	if err != nil {
		return Profile{}, err
	}
	var old Profile
	_ = json.Unmarshal([]byte(cfg[KeyProfile]), &old)
	if old.ID != 0 && old.ID != prof.ID {
		if err := m.store.TGBotDeleteAllChats(); err != nil {
			return Profile{}, err
		}
	}
	if decodeSecret(cfg[KeySecret]) == nil {
		b := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, b); err != nil {
			return Profile{}, err
		}
		if err := m.store.UpsertConfig(KeySecret, hex.EncodeToString(b), now); err != nil {
			return Profile{}, err
		}
	}
	pj, _ := json.Marshal(prof)
	if err := m.store.UpsertConfig(KeyProfile, string(pj), now); err != nil {
		return Profile{}, err
	}
	if err := m.store.UpsertConfig(KeyToken, token, now); err != nil {
		return Profile{}, err
	}
	m.mu.Lock()
	m.lastErr = ""
	m.mu.Unlock()
	m.Reconcile()
	return prof, nil
}

// ClearToken removes the token and unbinds every chat (「删除」).
func (m *Manager) ClearToken() error {
	now := m.opt.Now().UnixMilli()
	if err := m.store.UpsertConfig(KeyToken, "", now); err != nil {
		return err
	}
	if err := m.store.UpsertConfig(KeyProfile, "", now); err != nil {
		return err
	}
	if err := m.store.TGBotDeleteAllChats(); err != nil {
		return err
	}
	m.mu.Lock()
	m.code, m.codeExp, m.lastErr = "", time.Time{}, ""
	m.mu.Unlock()
	m.Reconcile()
	return nil
}

const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewBindCode makes the one pending bind code (8 chars, CodeTTL, one use)
// and starts polling so the bot can see /start <code>.
func (m *Manager) NewBindCode() (string, time.Time, error) {
	m.Reconcile()
	m.mu.Lock()
	if m.token == "" {
		m.mu.Unlock()
		return "", time.Time{}, ErrNoToken
	}
	if len(m.chats) >= MaxChats {
		m.mu.Unlock()
		return "", time.Time{}, ErrChatLimit
	}
	b := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		m.mu.Unlock()
		return "", time.Time{}, err
	}
	code := make([]byte, 8)
	for i, v := range b {
		code[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	m.code = string(code)
	m.codeExp = m.opt.Now().Add(m.opt.CodeTTL)
	exp := m.codeExp
	m.applyLocked()
	m.mu.Unlock()
	return string(code), exp, nil
}

// Unbind removes one chat.
func (m *Manager) Unbind(chatID int64) error {
	ok, err := m.store.TGBotDeleteChat(chatID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("这个聊天没有绑定")
	}
	m.Reconcile()
	return nil
}

// SendTest sends the test message to every bound chat; sent = successes.
func (m *Manager) SendTest(ctx context.Context) (int, error) {
	m.Reconcile()
	m.mu.Lock()
	token, st := m.token, m.settings
	m.mu.Unlock()
	if token == "" {
		return 0, ErrNoToken
	}
	chats, err := m.store.TGBotListChats()
	if err != nil {
		return 0, err
	}
	if len(chats) == 0 {
		return 0, ErrNoChats
	}
	app := ""
	if m.hooks.AppName != nil {
		app = m.hooks.AppName()
	}
	client := NewClient(m.opt.APIBase, token, m.opt.HTTPClient)
	text := TestText(app, st)
	sent := 0
	var last error
	now := m.opt.Now().UnixMilli()
	for _, c := range chats {
		cctx, cancel := context.WithTimeout(ctx, m.opt.CallTimeout)
		_, err := client.SendMessage(cctx, c.ChatID, text, nil)
		cancel()
		if err != nil {
			last = err
			if IsPermanent(err) {
				_ = m.store.TGBotSetChatError(c.ChatID, Redact(err.Error(), token), now)
			}
			continue
		}
		sent++
		if c.LastError != "" {
			_ = m.store.TGBotSetChatError(c.ChatID, "", 0)
		}
	}
	if sent == 0 && last != nil {
		return 0, fmt.Errorf("发送失败：%s", Redact(last.Error(), token))
	}
	return sent, nil
}

// SaveSettings validates and stores the reminder settings.
func (m *Manager) SaveSettings(st Settings) (Settings, error) {
	st, err := st.Normalize()
	if err != nil {
		return st, err
	}
	if err := m.store.UpsertConfig(KeySettings, st.JSON(), m.opt.Now().UnixMilli()); err != nil {
		return st, err
	}
	m.Reconcile()
	return st, nil
}

// Settings returns the current reminder settings (as stored).
func (m *Manager) Settings() Settings {
	cfg, err := m.store.GetConfigsByNames([]string{KeySettings})
	if err != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.settings
	}
	return ParseSettings(cfg[KeySettings])
}

// Acked: 「已续费」 rows from minDue on.
func (m *Manager) Acked(minDue int64) map[AckKey]bool {
	acks, err := m.store.TGBotListAcks(minDue)
	out := make(map[AckKey]bool, len(acks))
	if err != nil {
		return out
	}
	for _, a := range acks {
		out[AckKey{Key: a.MachineKey, Due: a.DueMs}] = true
	}
	return out
}

// Status is the card's view (reloads from the database).
func (m *Manager) Status() Status {
	m.Reconcile()
	chats, _ := m.store.TGBotListChats()
	if chats == nil {
		chats = []model.TGBotChat{}
	}
	now := m.opt.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Status{TokenSet: m.token != "", Profile: m.profile, Running: m.running, LastError: m.lastErr, LastRun: m.lastRun,
		Chats: chats, Settings: m.settings}
	if !m.lastErrAt.IsZero() && m.lastErr != "" {
		s.LastErrorAt = m.lastErrAt.UnixMilli()
	}
	if !m.lastOK.IsZero() {
		s.LastOKAt = m.lastOK.UnixMilli()
	}
	if s.TokenSet {
		if next := m.nextDailyLocked(now); !next.IsZero() {
			s.NextRunAt = next.UnixMilli()
		}
	}
	if m.code != "" && now.Before(m.codeExp) {
		s.BindCode, s.BindExpiresAt = m.code, m.codeExp.UnixMilli()
	}
	return s
}

// Now is the bot's clock (tests use a fake one).
func (m *Manager) Now() time.Time { return m.opt.Now() }

// Running reports whether the goroutine is up.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// Stats returns the resource counters.
func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}
