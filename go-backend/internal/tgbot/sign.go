package tgbot

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
)

// Button actions.
const (
	ActionRenewed = 'r' // ✅ 已续费
	ActionMute    = 'm' // 🔕 不再提醒这台
)

// Machine keys: f<FLVX node id> or o<救命版 node id>.
var machineKeyRe = regexp.MustCompile(`^[fo][1-9][0-9]{0,11}$`)

// ValidMachineKey reports whether key is a well-formed machine key.
func ValidMachineKey(key string) bool { return machineKeyRe.MatchString(key) }

// sigLen: 12 bytes (96 bits) of HMAC-SHA256, base64url = 16 chars.
const sigLen = 12

func mac(secret []byte, payload string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:sigLen])
}

// SignCallback builds the opaque callback data of a reminder button:
// "<action>.<key>.<due base36>.<sig>" (≤ 44 bytes; Telegram allows 64).
func SignCallback(secret []byte, action byte, key string, dueMs int64) string {
	payload := string(action) + "." + key + "." + strconv.FormatInt(dueMs, 36)
	return payload + "." + mac(secret, payload)
}

// VerifyCallback checks the signature and returns the fields. Anything not
// produced by SignCallback with the same secret is refused.
func VerifyCallback(secret []byte, data string) (action byte, key string, dueMs int64, ok bool) {
	if len(secret) == 0 || len(data) > 64 {
		return 0, "", 0, false
	}
	parts := strings.Split(data, ".")
	if len(parts) != 4 || len(parts[0]) != 1 {
		return 0, "", 0, false
	}
	action = parts[0][0]
	if action != ActionRenewed && action != ActionMute {
		return 0, "", 0, false
	}
	if !ValidMachineKey(parts[1]) {
		return 0, "", 0, false
	}
	due, err := strconv.ParseInt(parts[2], 36, 64)
	if err != nil || due <= 0 {
		return 0, "", 0, false
	}
	want := mac(secret, parts[0]+"."+parts[1]+"."+parts[2])
	if !hmac.Equal([]byte(want), []byte(parts[3])) {
		return 0, "", 0, false
	}
	return action, parts[1], due, true
}
