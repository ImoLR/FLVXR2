// Package tgbot is the panel's Telegram notification bot (plan 196): renewal
// reminders to bound admin chats with 「已续费」 / 「不再提醒这台」 buttons.
//
// Outbound only: long polling getUpdates, no webhook, no public route. The
// token is never logged — every error that may carry the request URL goes
// through Redact.
package tgbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultAPIBase is the public Bot API endpoint.
const DefaultAPIBase = "https://api.telegram.org"

// maxBody caps a Bot API answer (100 updates fit easily).
const maxBody = 4 << 20

// User is a Telegram user or bot.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// DisplayName: "First Last", else @username.
func (u User) DisplayName() string {
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
	if name == "" && u.Username != "" {
		name = "@" + u.Username
	}
	return name
}

// Chat is the chat of a message.
type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// DisplayName: group title, else the person's name, else @username.
func (c Chat) DisplayName() string {
	if t := strings.TrimSpace(c.Title); t != "" {
		return t
	}
	if n := strings.TrimSpace(strings.TrimSpace(c.FirstName) + " " + strings.TrimSpace(c.LastName)); n != "" {
		return n
	}
	if c.Username != "" {
		return "@" + c.Username
	}
	return strconv.FormatInt(c.ID, 10)
}

// Message is the part of a message the bot reads.
type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

// CallbackQuery is a button press.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// Update is one getUpdates item (only the kinds the bot asks for).
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Button is an inline keyboard button with callback data.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

// APIError is a Bot API refusal (ok=false) or an unexpected HTTP status.
type APIError struct {
	Method     string
	Status     int
	Code       int
	Desc       string
	RetryAfter int
}

func (e *APIError) Error() string {
	code := e.Code
	if code == 0 {
		code = e.Status
	}
	if e.Desc == "" {
		return fmt.Sprintf("telegram %s: HTTP %d", e.Method, code)
	}
	return fmt.Sprintf("telegram %s: %d %s", e.Method, code, e.Desc)
}

// Permanent: retrying the same request cannot help (bad request, the bot was
// blocked or removed, chat gone). Unauthorized (bad token) and conflicts are
// not permanent for the poller: they back off.
func (e *APIError) Permanent() bool {
	code := e.Code
	if code == 0 {
		code = e.Status
	}
	return code == http.StatusBadRequest || code == http.StatusForbidden
}

// IsPermanent reports whether err is a permanent Bot API refusal.
func IsPermanent(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Permanent()
}

// RetryAfter: the flood-control wait Telegram asked for (0 = none).
func RetryAfter(err error) time.Duration {
	var ae *APIError
	if errors.As(err, &ae) && ae.RetryAfter > 0 {
		return time.Duration(ae.RetryAfter) * time.Second
	}
	return 0
}

// Unauthorized: the token was refused.
func Unauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Code == http.StatusUnauthorized || ae.Status == http.StatusUnauthorized ||
		ae.Code == http.StatusNotFound || ae.Status == http.StatusNotFound)
}

// Client talks to one bot.
type Client struct {
	base  string
	token string
	hc    *http.Client
}

// NewClient: base "" = DefaultAPIBase; hc nil = a client without overall
// timeout (every call carries its own context deadline).
func NewClient(base, token string, hc *http.Client) *Client {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = DefaultAPIBase
	}
	if hc == nil {
		hc = &http.Client{Transport: newTransport()}
	}
	return &Client{base: base, token: token, hc: hc}
}

func newTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	// One bot = one long poll + the odd send: keep at most one idle
	// connection, close it after 2 minutes idle.
	t.MaxIdleConns = 2
	t.MaxIdleConnsPerHost = 1
	t.MaxConnsPerHost = 4
	t.IdleConnTimeout = 2 * time.Minute
	t.ResponseHeaderTimeout = 0
	return t
}

// Redact removes the token (and the bot<token> path segment) from s.
func Redact(s, token string) string {
	token = strings.TrimSpace(token)
	if token == "" || s == "" {
		return s
	}
	s = strings.ReplaceAll(s, token, "<token>")
	// A URL-escaped copy (':' → %3A) must not survive either.
	if esc := strings.ReplaceAll(token, ":", "%3A"); esc != token {
		s = strings.ReplaceAll(s, esc, "<token>")
	}
	return s
}

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

func (c *Client) redact(err error) error {
	if err == nil {
		return nil
	}
	var ae *APIError
	if errors.As(err, &ae) {
		ae.Desc = Redact(ae.Desc, c.token)
		return ae
	}
	msg := err.Error()
	if clean := Redact(msg, c.token); clean != msg {
		// Keep context.Canceled / DeadlineExceeded detectable.
		var inner error = errors.New(clean)
		if errors.Is(err, context.Canceled) {
			inner = context.Canceled
		} else if errors.Is(err, context.DeadlineExceeded) {
			inner = context.DeadlineExceeded
		}
		return &redactedError{msg: clean, err: inner}
	}
	return err
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call posts params as JSON to /bot<token>/<method> and decodes the result.
func (c *Client) call(ctx context.Context, method string, params interface{}, out interface{}) error {
	if c.token == "" {
		return errors.New("telegram: no token")
	}
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return c.redact(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return c.redact(err)
	}
	var ar apiResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return &APIError{Method: method, Status: resp.StatusCode, Desc: "unexpected answer"}
	}
	if !ar.OK {
		return c.redact(&APIError{Method: method, Status: resp.StatusCode, Code: ar.ErrorCode, Desc: ar.Description, RetryAfter: ar.Parameters.RetryAfter})
	}
	if out != nil && len(ar.Result) > 0 {
		if err := json.Unmarshal(ar.Result, out); err != nil {
			return fmt.Errorf("telegram %s: bad result", method)
		}
	}
	return nil
}

// GetMe checks the token and returns the bot's profile.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, "getMe", struct{}{}, &u)
	return u, err
}

// GetUpdates long-polls for messages and button presses.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	secs := int(timeout / time.Second)
	if secs < 0 {
		secs = 0
	}
	params := map[string]interface{}{
		"timeout":         secs,
		"limit":           100,
		"allowed_updates": []string{"message", "callback_query"},
	}
	if offset > 0 {
		params["offset"] = offset
	}
	var out []Update
	err := c.call(ctx, "getUpdates", params, &out)
	return out, err
}

func keyboard(rows [][]Button) interface{} {
	if rows == nil {
		rows = [][]Button{}
	}
	return map[string]interface{}{"inline_keyboard": rows}
}

// SendMessage sends plain text (no parse mode: names stay literal) and
// returns the message id.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, buttons [][]Button) (int64, error) {
	params := map[string]interface{}{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}
	if len(buttons) > 0 {
		params["reply_markup"] = keyboard(buttons)
	}
	var m Message
	if err := c.call(ctx, "sendMessage", params, &m); err != nil {
		return 0, err
	}
	return m.MessageID, nil
}

// EditMessage replaces a message's text and buttons (nil = remove them). A
// "message is not modified" refusal counts as success.
func (c *Client) EditMessage(ctx context.Context, chatID, messageID int64, text string, buttons [][]Button) error {
	params := map[string]interface{}{
		"chat_id":                  chatID,
		"message_id":               messageID,
		"text":                     text,
		"disable_web_page_preview": true,
		"reply_markup":             keyboard(buttons),
	}
	err := c.call(ctx, "editMessageText", params, nil)
	var ae *APIError
	if errors.As(err, &ae) && strings.Contains(strings.ToLower(ae.Desc), "message is not modified") {
		return nil
	}
	return err
}

// AnswerCallback stops the button's spinner, optionally with a short toast.
func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	params := map[string]interface{}{"callback_query_id": id}
	if text != "" {
		params["text"] = text
	}
	return c.call(ctx, "answerCallbackQuery", params, nil)
}
