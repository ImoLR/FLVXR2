package handler

// Plan 196 「Telegram 通知」: the admin card's API and the glue between the bot
// (internal/tgbot) and the panel: the machine list (FLVX nodes + 救命版-only
// machines, deduplicated like the probe, plan 195) and 「已续费」 on the 节点
// page's own reminder. The 救命版 is only read (internal/oldpanel), never
// written.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/tgbot"
)

// tgAPIBaseEnv overrides the Bot API endpoint (tests, the demo, or a relay
// for panels that cannot reach api.telegram.org). http / https only.
const tgAPIBaseEnv = "FLVX_TELEGRAM_API_BASE"

// errTGBotConfigKey: the generic config API refuses the bot's keys.
var errTGBotConfigKey = errors.New("这个设置只能在 设置 → Telegram 通知 里修改")

type tgBotState struct {
	mu   sync.Mutex
	mgr  *tgbot.Manager
	hook func(*tgbot.Options)
}

func tgAPIBase() string {
	v := strings.TrimSpace(os.Getenv(tgAPIBaseEnv))
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		log.Printf("[tgbot] %s ignored (not an http(s) URL)", tgAPIBaseEnv)
		return ""
	}
	return strings.TrimRight(v, "/")
}

// tgBot returns the bot manager (created on first use, idle until enabled).
func (h *Handler) tgBot() *tgbot.Manager {
	h.tg.mu.Lock()
	defer h.tg.mu.Unlock()
	if h.tg.mgr == nil {
		opt := tgbot.Options{APIBase: tgAPIBase()}
		if h.tg.hook != nil {
			h.tg.hook(&opt)
		}
		h.tg.mgr = tgbot.New(h.repo, tgbot.Hooks{Machines: h.tgMachines, Renewed: h.tgRenewed, AppName: h.tgAppName}, opt)
	}
	return h.tg.mgr
}

// SetTelegramBotOptions adjusts the bot options before its first use (tests
// and the offline demo: fake API server, clock, short back-off).
func (h *Handler) SetTelegramBotOptions(fn func(*tgbot.Options)) {
	h.tg.mu.Lock()
	h.tg.hook = fn
	h.tg.mu.Unlock()
}

// EnableTelegramBot lets the bot run (StartBackgroundJobs; tests call it
// directly). Without a token and a bound chat nothing starts.
func (h *Handler) EnableTelegramBot() { h.tgBot().Enable() }

// StopTelegramBot stops the bot goroutine, if any.
func (h *Handler) StopTelegramBot() {
	h.tg.mu.Lock()
	mgr := h.tg.mgr
	h.tg.mu.Unlock()
	if mgr != nil {
		mgr.Stop()
	}
}

// TelegramBotStats / TelegramBotRunning: resource numbers for tests / demo.
func (h *Handler) TelegramBotStats() tgbot.Stats { return h.tgBot().Stats() }
func (h *Handler) TelegramBotRunning() bool      { return h.tgBot().Running() }

// tgCollect resolves every machine with an expiry. FLVX nodes come first; a
// node that also runs in the 救命版 stays one machine (its key f<id>) and
// borrows the 救命版 expiry when it has none, like the probe. noExpiry
// counts the machines without a date. A configured but unreadable 救命版
// (or a host name still resolving) gives a usable list + PartialError.
func (h *Handler) tgCollect() ([]tgbot.Machine, int, error) {
	rows, err := h.repo.ListRenewalNodes()
	if err != nil {
		return nil, 0, err
	}
	m, _ := h.oldPanelAdminMerge(rows)
	out := make([]tgbot.Machine, 0, len(rows))
	noExpiry := 0
	for _, n := range rows {
		exp, cycle := n.ExpiryTime, n.RenewalCycle
		if exp <= 0 {
			if e, c, ok := oldPanelExpiry(m, n.ID); ok {
				exp, cycle = e, c
			}
		}
		if exp <= 0 {
			noExpiry++
			continue
		}
		mc := tgbot.Machine{Key: "f" + strconv.FormatInt(n.ID, 10), Name: n.Name, Source: tgbot.SourceFLVX, NodeID: n.ID, Anchor: exp, Cycle: cycle}
		if ids := m.byFLVX[n.ID]; len(ids) > 0 {
			for _, on := range m.view.nodes {
				if on.ID == ids[0] {
					mc.OldMatch = on.Name
				}
			}
		}
		out = append(out, mc)
	}
	var partial error
	o := &h.probeInit().old
	if o.reader != nil && !m.view.ok {
		partial = &tgbot.PartialError{Err: errors.New("救命版数据暂时读不到")}
	}
	if m.view.on && m.view.ok {
		for _, n := range m.view.nodes {
			if _, ok := m.matched[n.ID]; ok {
				continue
			}
			if m.pending[n.ID] {
				partial = &tgbot.PartialError{Err: errors.New("救命版机器的域名还在解析")}
				continue
			}
			if n.ExpiryTime <= 0 {
				noExpiry++
				continue
			}
			out = append(out, tgbot.Machine{Key: "o" + strconv.FormatInt(n.ID, 10), Name: n.Name, Source: tgbot.SourceOld,
				Anchor: n.ExpiryTime, Cycle: n.RenewalCycle})
		}
	}
	return out, noExpiry, partial
}

func (h *Handler) tgMachines(time.Time) ([]tgbot.Machine, error) {
	ms, _, err := h.tgCollect()
	return ms, err
}

// tgRenewed: 「已续费」 also clears the 节点 page's reminder for that cycle
// (expiry_reminder_dismissed until the renewed due date).
func (h *Handler) tgRenewed(m tgbot.Machine, due int64) error {
	if m.NodeID <= 0 {
		return nil
	}
	return h.repo.MarkNodeRenewalDismissed(m.NodeID, due/1000)
}

func (h *Handler) tgAppName() string {
	cfg, err := h.repo.GetConfigsByNames([]string{"app_name"})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cfg["app_name"])
}

// ─── admin API ───────────────────────────────────────────────────────

type tgMachineView struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	OldMatch string `json:"oldMatch,omitempty"`
	Cycle    string `json:"cycle"`
	NextDue  int64  `json:"nextDue"`
	Days     int    `json:"days"`
	Muted    bool   `json:"muted"`
	Renewed  bool   `json:"renewed"`
}

type tgAdminView struct {
	tgbot.Status
	Machines []tgMachineView `json:"machines"`
	NoExpiry int             `json:"noExpiry"`
	OldPanel string          `json:"oldPanel"`
	Partial  string          `json:"partial,omitempty"`
	// Limits for the form.
	MaxChats int `json:"maxChats"`
	MaxDays  int `json:"maxDays"`
}

func (h *Handler) tgAdminView() tgAdminView {
	mgr := h.tgBot()
	st := mgr.Status()
	now := mgr.Now()
	v := tgAdminView{Status: st, Machines: []tgMachineView{}, MaxChats: tgbot.MaxChats, MaxDays: tgbot.MaxDays}
	ms, noExpiry, err := h.tgCollect()
	v.OldPanel = h.oldPanelStatus() // after the read above
	if err != nil && !tgbot.IsPartial(err) {
		v.Partial = "读取节点失败"
		return v
	}
	if err != nil {
		v.Partial = strings.TrimPrefix(err.Error(), "partial machine list: ")
	}
	v.NoExpiry = noExpiry
	acked := mgr.Acked(now.Add(-48 * time.Hour).UnixMilli())
	for _, m := range ms {
		s, ok := m.Status(now)
		if !ok {
			continue
		}
		v.Machines = append(v.Machines, tgMachineView{Key: m.Key, Name: m.Name, Source: m.Source, OldMatch: m.OldMatch, Cycle: s.Cycle,
			NextDue: s.NextDue, Days: s.Days, Muted: st.Settings.IsMuted(m.Key), Renewed: acked[tgbot.AckKey{Key: m.Key, Due: s.NextDue}]})
	}
	sort.SliceStable(v.Machines, func(i, j int) bool {
		if v.Machines[i].NextDue != v.Machines[j].NextDue {
			return v.Machines[i].NextDue < v.Machines[j].NextDue
		}
		return v.Machines[i].Name < v.Machines[j].Name
	})
	return v
}

func (h *Handler) tgAdminGet(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	response.WriteJSON(w, response.OK(h.tgAdminView()))
}

// tgAdminStatus: the cheap view (no machine list) the card polls while a
// bind code is waiting.
func (h *Handler) tgAdminStatus(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	response.WriteJSON(w, response.OK(h.tgBot().Status()))
}

func (h *Handler) tgAdminToken(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	var req struct {
		Token string `json:"token"`
		Clear bool   `json:"clear"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	mgr := h.tgBot()
	if req.Clear {
		if err := mgr.ClearToken(); err != nil {
			response.WriteJSON(w, response.Err(-2, "删除失败"))
			return
		}
		response.WriteJSON(w, response.OK(h.tgAdminView()))
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		response.WriteJSON(w, response.ErrDefault("请填写 bot token"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := mgr.SaveToken(ctx, req.Token); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(h.tgAdminView()))
}

func (h *Handler) tgAdminTest(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	sent, err := h.tgBot().SendTest(ctx)
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(map[string]int{"sent": sent}))
}

func (h *Handler) tgAdminBindCode(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	code, exp, err := h.tgBot().NewBindCode()
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	st := h.tgBot().Status()
	out := map[string]interface{}{"code": code, "expiresAt": exp.UnixMilli(), "command": "/start " + code}
	if st.Profile.Username != "" {
		out["link"] = "https://t.me/" + st.Profile.Username + "?start=" + code
	}
	response.WriteJSON(w, response.OK(out))
}

func (h *Handler) tgAdminUnbind(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	var req struct {
		ChatID json.Number `json:"chatId"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(req.ChatID.String()), 10, 64)
	if err != nil || id == 0 {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	if err := h.tgBot().Unbind(id); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(h.tgBot().Status()))
}

func (h *Handler) tgAdminSettings(w http.ResponseWriter, r *http.Request) {
	if !requireAdminRequest(w, r) {
		return
	}
	var req struct {
		Enabled *bool           `json:"enabled"`
		Days    json.RawMessage `json:"days"`
		Time    *string         `json:"time"`
		Muted   *[]string       `json:"muted"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	mgr := h.tgBot()
	st := mgr.Settings()
	if req.Enabled != nil {
		st.Enabled = *req.Enabled
	}
	if len(req.Days) > 0 && string(req.Days) != "null" {
		var text string
		var list []int
		switch {
		case json.Unmarshal(req.Days, &text) == nil:
			days, err := tgbot.ParseDays(text)
			if err != nil {
				response.WriteJSON(w, response.ErrDefault(err.Error()))
				return
			}
			st.Days = days
		case json.Unmarshal(req.Days, &list) == nil:
			st.Days = list
		default:
			response.WriteJSON(w, response.ErrDefault("提前天数格式不对"))
			return
		}
	}
	if req.Time != nil {
		st.Time = *req.Time
	}
	if req.Muted != nil {
		st.Muted = *req.Muted
	}
	if _, err := mgr.SaveSettings(st); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(h.tgAdminView()))
}

func (h *Handler) registerTelegramBot(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/config/tgbot", h.tgAdminGet)
	mux.HandleFunc("/api/v1/config/tgbot/status", h.tgAdminStatus)
	mux.HandleFunc("/api/v1/config/tgbot/token", h.tgAdminToken)
	mux.HandleFunc("/api/v1/config/tgbot/test", h.tgAdminTest)
	mux.HandleFunc("/api/v1/config/tgbot/bind-code", h.tgAdminBindCode)
	mux.HandleFunc("/api/v1/config/tgbot/unbind", h.tgAdminUnbind)
	mux.HandleFunc("/api/v1/config/tgbot/settings", h.tgAdminSettings)
}
