// Package tgtest is a fake Telegram Bot API server for tests and the offline
// demo (plan 196). It never talks to Telegram.
package tgtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"go-backend/internal/tgbot"
)

// Call is one recorded API call.
type Call struct {
	Method string
	Params map[string]interface{}
	At     time.Time
	Failed bool // answered with a queued failure
}

// Int returns a numeric parameter.
func (c Call) Int(name string) int64 {
	switch v := c.Params[name].(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

// Str returns a string parameter.
func (c Call) Str(name string) string {
	s, _ := c.Params[name].(string)
	return s
}

// Buttons returns the inline keyboard of a send / edit call.
func (c Call) Buttons() [][]tgbot.Button {
	raw, ok := c.Params["reply_markup"]
	if !ok {
		return nil
	}
	b, _ := json.Marshal(raw)
	var kb struct {
		Rows [][]tgbot.Button `json:"inline_keyboard"`
	}
	_ = json.Unmarshal(b, &kb)
	return kb.Rows
}

type failure struct {
	status, code, retryAfter int
	desc                     string
	drop                     bool // close the connection instead of answering
}

// Fake is the server.
type Fake struct {
	Server *httptest.Server
	Token  string
	Bot    tgbot.User
	// PollDelay: how long an empty getUpdates waits (default 20 ms).
	PollDelay time.Duration

	mu      sync.Mutex
	calls   []Call
	updates []tgbot.Update
	nextUpd int64
	nextMsg int64
	fails   map[string][]failure
	wake    chan struct{}
	paths   []string
}

// New starts a fake for one bot token.
func New(token string) *Fake {
	f := &Fake{Token: token, Bot: tgbot.User{ID: 7012345678, IsBot: true, FirstName: "FLVX 通知", Username: "flvx_test_bot"},
		PollDelay: 20 * time.Millisecond, nextUpd: 100, nextMsg: 500, fails: map[string][]failure{}, wake: make(chan struct{}, 1)}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// URL is the API base for tgbot.Options.APIBase.
func (f *Fake) URL() string { return f.Server.URL }

// Close stops the server.
func (f *Fake) Close() { f.Server.Close() }

func (f *Fake) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// PushMessage queues a text message from a chat; returns the update id.
func (f *Fake) PushMessage(chat tgbot.Chat, from tgbot.User, text string) int64 {
	f.mu.Lock()
	f.nextUpd++
	f.nextMsg++
	id := f.nextUpd
	u := from
	f.updates = append(f.updates, tgbot.Update{UpdateID: id, Message: &tgbot.Message{MessageID: f.nextMsg, From: &u, Chat: chat, Text: text}})
	f.mu.Unlock()
	f.poke()
	return id
}

// PushCallback queues a button press on message msgID (text = its text).
func (f *Fake) PushCallback(chat tgbot.Chat, from tgbot.User, msgID int64, text, data string) string {
	f.mu.Lock()
	f.nextUpd++
	id := f.nextUpd
	cqID := "cq" + itoa(id)
	f.updates = append(f.updates, tgbot.Update{UpdateID: id, CallbackQuery: &tgbot.CallbackQuery{ID: cqID, From: from,
		Message: &tgbot.Message{MessageID: msgID, Chat: chat, Text: text}, Data: data}})
	f.mu.Unlock()
	f.poke()
	return cqID
}

// Fail makes the next `times` calls of method fail with the given answer
// (status 0 = drop the connection).
func (f *Fake) Fail(method string, status, code int, desc string, retryAfter, times int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i < times; i++ {
		f.fails[method] = append(f.fails[method], failure{status: status, code: code, desc: desc, retryAfter: retryAfter, drop: status == 0})
	}
}

// Calls returns the recorded calls of one method ("" = all).
func (f *Fake) Calls(method string) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, 0, len(f.calls))
	for _, c := range f.calls {
		if method == "" || c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// Paths returns every request path seen (to check where the token went).
func (f *Fake) Paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

// Reset forgets the recorded calls.
func (f *Fake) Reset() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

// WaitCalls waits until method was called at least n times.
func (f *Fake) WaitCalls(method string, n int, timeout time.Duration) []Call {
	deadline := time.Now().Add(timeout)
	for {
		calls := f.Calls(method)
		if len(calls) >= n || time.Now().After(deadline) {
			return calls
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Pending: queued updates not fetched yet.
func (f *Fake) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updates)
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.mu.Unlock()
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "bot") {
		writeJSON(w, 404, map[string]interface{}{"ok": false, "error_code": 404, "description": "Not Found"})
		return
	}
	method := parts[1]
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	params := map[string]interface{}{}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	_ = dec.Decode(&params)
	f.mu.Lock()
	var fail *failure
	if q := f.fails[method]; len(q) > 0 {
		fl := q[0]
		f.fails[method] = q[1:]
		fail = &fl
	}
	f.calls = append(f.calls, Call{Method: method, Params: params, At: time.Now(), Failed: fail != nil})
	f.mu.Unlock()
	if strings.TrimPrefix(parts[0], "bot") != f.Token {
		writeJSON(w, 401, map[string]interface{}{"ok": false, "error_code": 401, "description": "Unauthorized"})
		return
	}
	if fail != nil {
		if fail.drop {
			if hj, ok := w.(http.Hijacker); ok {
				if c, _, err := hj.Hijack(); err == nil {
					_ = c.Close()
					return
				}
			}
			writeJSON(w, 502, map[string]interface{}{"ok": false})
			return
		}
		body := map[string]interface{}{"ok": false, "error_code": fail.code, "description": fail.desc}
		if fail.retryAfter > 0 {
			body["parameters"] = map[string]int{"retry_after": fail.retryAfter}
		}
		writeJSON(w, fail.status, body)
		return
	}
	switch method {
	case "getMe":
		writeJSON(w, 200, map[string]interface{}{"ok": true, "result": f.Bot})
	case "getUpdates":
		var offset int64
		if v, ok := params["offset"].(json.Number); ok {
			offset, _ = v.Int64()
		}
		deadline := time.After(f.PollDelay)
		for {
			f.mu.Lock()
			kept := f.updates[:0]
			for _, u := range f.updates {
				if u.UpdateID >= offset {
					kept = append(kept, u)
				}
			}
			f.updates = kept
			if len(f.updates) > 0 {
				out := append([]tgbot.Update(nil), f.updates...)
				f.mu.Unlock()
				writeJSON(w, 200, map[string]interface{}{"ok": true, "result": out})
				return
			}
			f.mu.Unlock()
			select {
			case <-f.wake:
				continue
			case <-deadline:
				writeJSON(w, 200, map[string]interface{}{"ok": true, "result": []tgbot.Update{}})
				return
			case <-r.Context().Done():
				return
			}
		}
	case "sendMessage":
		f.mu.Lock()
		f.nextMsg++
		id := f.nextMsg
		f.mu.Unlock()
		chat := Call{Params: params}.Int("chat_id")
		writeJSON(w, 200, map[string]interface{}{"ok": true, "result": map[string]interface{}{"message_id": id, "chat": map[string]int64{"id": chat}, "text": params["text"]}})
	case "editMessageText":
		writeJSON(w, 200, map[string]interface{}{"ok": true, "result": map[string]interface{}{"message_id": Call{Params: params}.Int("message_id")}})
	case "answerCallbackQuery":
		writeJSON(w, 200, map[string]interface{}{"ok": true, "result": true})
	default:
		writeJSON(w, 404, map[string]interface{}{"ok": false, "error_code": 404, "description": "Not Found: method not found"})
	}
}
