package handler

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// nodeAdminNoticeMaxChars matches node.admin_notice varchar(512) (PostgreSQL
// counts characters, so the limit is in runes, not bytes).
const nodeAdminNoticeMaxChars = 512

// normalizeNodeAdminNotice checks a node's 节点提示 (plan 154): line breaks are
// kept (CRLF → LF), surrounding blanks trimmed, at most 512 characters.
// Requests send it as a string; anything else is rejected rather than stored.
func normalizeNodeAdminNotice(value interface{}) (string, error) {
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", errors.New("节点提示格式错误")
	}
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	if utf8.RuneCountInString(text) > nodeAdminNoticeMaxChars {
		return "", errors.New("节点提示不能超过512个字符")
	}
	return text, nil
}
