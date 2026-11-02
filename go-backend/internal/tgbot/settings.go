package tgbot

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Reminder settings limits.
const (
	MaxChats     = 5
	MaxDays      = 60 // largest 「提前天数」
	MaxDayValues = 8
	maxMuted     = 2000
)

// Settings is the tg_bot_settings row.
type Settings struct {
	Enabled bool     `json:"enabled"`
	Days    []int    `json:"days"`  // days before the due date, descending
	Time    string   `json:"time"`  // HH:MM, Taiwan time
	Muted   []string `json:"muted"` // machine keys with 「不提醒」
}

// DefaultSettings: on, 7 / 3 / 1 / 0 days before, 10:00.
func DefaultSettings() Settings {
	return Settings{Enabled: true, Days: []int{7, 3, 1, 0}, Time: "10:00"}
}

// ParseSettings reads a stored row; missing or broken fields fall back to the
// defaults one by one.
func ParseSettings(raw string) Settings {
	st := DefaultSettings()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return st
	}
	var in struct {
		Enabled *bool    `json:"enabled"`
		Days    []int    `json:"days"`
		Time    string   `json:"time"`
		Muted   []string `json:"muted"`
	}
	if json.Unmarshal([]byte(raw), &in) != nil {
		return st
	}
	if in.Enabled != nil {
		st.Enabled = *in.Enabled
	}
	if days, err := normalizeDays(in.Days); err == nil && len(days) > 0 {
		st.Days = days
	}
	if _, err := ParseClock(in.Time); err == nil {
		st.Time = normalizeClock(in.Time)
	}
	st.Muted = normalizeMuted(in.Muted)
	return st
}

// Normalize validates settings coming from the admin API.
func (s Settings) Normalize() (Settings, error) {
	days, err := normalizeDays(s.Days)
	if err != nil {
		return s, err
	}
	if len(days) == 0 {
		return s, errors.New("提前天数至少填一个")
	}
	if _, err := ParseClock(s.Time); err != nil {
		return s, err
	}
	if len(s.Muted) > maxMuted {
		return s, errors.New("不提醒的机器太多")
	}
	return Settings{Enabled: s.Enabled, Days: days, Time: normalizeClock(s.Time), Muted: normalizeMuted(s.Muted)}, nil
}

// JSON is the stored form.
func (s Settings) JSON() string {
	if s.Days == nil {
		s.Days = []int{}
	}
	if s.Muted == nil {
		s.Muted = []string{}
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// IsMuted reports whether the machine has 「不提醒」.
func (s Settings) IsMuted(key string) bool {
	for _, k := range s.Muted {
		if k == key {
			return true
		}
	}
	return false
}

// WithMuted returns a copy with key muted (on) or unmuted.
func (s Settings) WithMuted(key string, on bool) Settings {
	out := make([]string, 0, len(s.Muted)+1)
	for _, k := range s.Muted {
		if k != key {
			out = append(out, k)
		}
	}
	if on {
		out = append(out, key)
	}
	s.Muted = normalizeMuted(out)
	return s
}

// HasDay reports whether n is one of the reminder days.
func (s Settings) HasDay(n int) bool {
	for _, d := range s.Days {
		if d == n {
			return true
		}
	}
	return false
}

// SendMinute: minutes after 00:00 Taiwan time.
func (s Settings) SendMinute() int {
	m, err := ParseClock(s.Time)
	if err != nil {
		m, _ = ParseClock(DefaultSettings().Time)
	}
	return m
}

// ParseDays reads "7,3,1,0" (also "7 3 1 0", "7，3"); see normalizeDays.
func ParseDays(text string) ([]int, error) {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ' ' || r == ';' || r == '；'
	})
	vals := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil, fmt.Errorf("提前天数只能填 0–%d 的整数，用逗号隔开", MaxDays)
		}
		vals = append(vals, n)
	}
	return normalizeDays(vals)
}

// normalizeDays: unique, 0..MaxDays, at most MaxDayValues, descending.
func normalizeDays(vals []int) ([]int, error) {
	seen := map[int]bool{}
	out := make([]int, 0, len(vals))
	for _, n := range vals {
		if n < 0 || n > MaxDays {
			return nil, fmt.Errorf("提前天数只能填 0–%d 的整数，用逗号隔开", MaxDays)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > MaxDayValues {
		return nil, fmt.Errorf("提前天数最多 %d 个", MaxDayValues)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out, nil
}

// ParseClock reads "HH:MM" (24 h) into minutes after midnight.
func ParseClock(s string) (int, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "：", ":"))
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, errors.New("发送时间格式是 时:分，例如 10:00")
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 || len(parts[1]) != 2 {
		return 0, errors.New("发送时间格式是 时:分，例如 10:00")
	}
	return h*60 + m, nil
}

func normalizeClock(s string) string {
	m, err := ParseClock(s)
	if err != nil {
		return DefaultSettings().Time
	}
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

func normalizeMuted(keys []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if !ValidMachineKey(k) || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > maxMuted {
		out = out[:maxMuted]
	}
	return out
}
