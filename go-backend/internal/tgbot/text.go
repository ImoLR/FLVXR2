package tgbot

import (
	"strconv"
	"strings"
	"time"

	"go-backend/internal/renewal"
)

// Button labels.
const (
	LabelRenewed = "✅ 已续费"
	LabelMute    = "🔕 不再提醒这台"
)

// CycleLabel: 月付 / 季付 / 半年付 / 年付 ("" without a known cycle).
func CycleLabel(cycle string) string {
	switch renewal.NormalizeCycle(cycle) {
	case renewal.CycleMonth:
		return "月付"
	case renewal.CycleQuarter:
		return "季付"
	case renewal.CycleHalfYear:
		return "半年付"
	case renewal.CycleYear:
		return "年付"
	}
	return ""
}

// DateLabel: "11-03" in Taiwan time, with the year when it is not this year.
func DateLabel(ms int64, now time.Time) string {
	t := time.UnixMilli(ms).In(renewal.Zone)
	if t.Year() != now.In(renewal.Zone).Year() {
		return t.Format("2006-01-02")
	}
	return t.Format("01-02")
}

// DaysLabel: 今天到期 / 明天到期 / N 天后到期.
func DaysLabel(days int) string {
	switch {
	case days <= 0:
		return "今天到期"
	case days == 1:
		return "明天到期"
	}
	return strconv.Itoa(days) + " 天后到期"
}

// ReminderText: 「⏰ Bestvm US 月付 明天到期（11-03）」, 救命版-only machines
// get 「· 救命版」 so the admin knows where to renew / look.
func ReminderText(r Reminder, now time.Time) string {
	var b strings.Builder
	b.WriteString("⏰ ")
	b.WriteString(oneLine(r.Machine.Name, 60))
	if c := CycleLabel(r.Cycle); c != "" {
		b.WriteString(" ")
		b.WriteString(c)
	}
	b.WriteString(" ")
	b.WriteString(DaysLabel(r.Days))
	b.WriteString("（")
	b.WriteString(DateLabel(r.Due, now))
	b.WriteString("）")
	if r.Machine.Source == SourceOld {
		b.WriteString(" · 救命版")
	}
	return b.String()
}

// RenewedLine: 「✅ 已续费（Alice），下次到期 12-03」.
func RenewedLine(next int64, by string, now time.Time) string {
	s := "✅ 已续费"
	if by = oneLine(by, 32); by != "" {
		s += "（" + by + "）"
	}
	if next > 0 {
		s += "，下次到期 " + DateLabel(next, now)
	}
	return s
}

// PassedLine: the button was pressed after the due date had passed.
func PassedLine(next int64, now time.Time) string {
	s := "⌛ 这一期已经过了"
	if next > 0 {
		s += "，下次到期 " + DateLabel(next, now)
	}
	return s
}

// MutedLine: 「🔕 不再提醒这台」 pressed.
func MutedLine(by string) string {
	s := "🔕 已不再提醒这台"
	if by = oneLine(by, 32); by != "" {
		s += "（" + by + "）"
	}
	return s + "，可在 设置 → Telegram 通知 里恢复"
}

// GoneLine: the machine is gone or has no expiry any more.
const GoneLine = "ℹ️ 这台机器已不在面板里，或已没有到期日"

// BoundText answers a successful /start <code>.
func BoundText(app string, st Settings) string {
	s := "✅ 已绑定「" + appName(app) + "」面板的 Telegram 通知。"
	if st.Enabled {
		s += "\n机器快到期时会在这里提醒你（每天 " + st.Time + "，台湾时间）。"
	} else {
		s += "\n到期提醒目前是关闭的，可以在面板 设置 → Telegram 通知 里打开。"
	}
	return s
}

// LimitText answers /start <code> when MaxChats chats are bound already.
func LimitText() string {
	return "⚠️ 最多只能绑定 " + strconv.Itoa(MaxChats) + " 个聊天，请先在面板 设置 → Telegram 通知 里解绑一个。"
}

// TestText is the 「测试发送」 message.
func TestText(app string, st Settings) string {
	s := "🔔 测试消息：「" + appName(app) + "」面板的 Telegram 通知正常。"
	if st.Enabled {
		days := make([]string, 0, len(st.Days))
		for _, d := range st.Days {
			days = append(days, strconv.Itoa(d))
		}
		s += "\n到期提醒每天 " + st.Time + "（台湾时间）发到这里，提前 " + strings.Join(days, "、") + " 天。"
	} else {
		s += "\n到期提醒目前是关闭的。"
	}
	return s
}

// ReminderButtons: 「✅ 已续费」 「🔕 不再提醒这台」 for one machine and due date.
func ReminderButtons(secret []byte, key string, due int64) [][]Button {
	return [][]Button{{
		{Text: LabelRenewed, Data: SignCallback(secret, ActionRenewed, key, due)},
		{Text: LabelMute, Data: SignCallback(secret, ActionMute, key, due)},
	}}
}

func appName(app string) string {
	if app = oneLine(app, 40); app == "" {
		return "FLVX"
	}
	return app
}

// oneLine: trimmed, newlines folded, at most max runes.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
