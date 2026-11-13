// Package vlessenc generates and checks Xray "VLESS Encryption" strings
// (mlkem768x25519plus, Xray 25.8+), plan 253. Pure Go, no Core binary: the
// key pair and both strings are byte-compatible with `xray vlessenc` (the
// X25519 authentication block) and with the parser of the pinned landing
// Core (f6f58447, infra/conf/vless.go).
//
//	server "decryption": mlkem768x25519plus.<mode>.<ticket>.[<padding>.]<key>…
//	client "encryption": mlkem768x25519plus.<mode>.<rtt>.[<padding>.]<key>…
//
// mode = native | xorpub | random; ticket = "600s" or "<from>-<to>s"; rtt =
// 0rtt | 1rtt; key = base64url (raw) of an X25519 key (32 bytes; the server
// side the private key, the client side the public key, which xray calls the
// "password") or ML-KEM-768 (server: 64-byte seed, client: 1184-byte
// encapsulation key). Optional padding segments ("a-b-c") come first.
package vlessenc

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

const (
	// Prefix is the only VLESS Encryption suite.
	Prefix = "mlkem768x25519plus"
	// Mode FLVX uses: "random" makes every byte (handshake included) look
	// random, like SS2022 — the best chance through protocol filters.
	Mode = "random"
	// ServerTicket / ClientRTT are the `xray vlessenc` defaults.
	ServerTicket = "600s"
	ClientRTT    = "0rtt"
	// maxKeys bounds relay chains in pasted links (xray allows several).
	maxKeys = 8
)

var modes = map[string]bool{"native": true, "xorpub": true, "random": true}

// Keys is one X25519 authentication key pair, base64url without padding.
type Keys struct {
	PrivateKey string // server side, secret
	PublicKey  string // client side ("password")
}

// NewKeys generates a key pair the way `xray x25519` / `xray vlessenc` do:
// 32 random bytes clamped per RFC 7748 (so the printed key is the real one).
func NewKeys() (Keys, error) {
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return Keys{}, err
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	key, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return Keys{}, err
	}
	return Keys{PrivateKey: base64.RawURLEncoding.EncodeToString(priv), PublicKey: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())}, nil
}

// PublicFromPrivate derives the client key of a stored server key.
func PublicFromPrivate(priv string) (string, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(priv)
	if err != nil || len(b) != 32 {
		return "", errors.New("invalid VLESS Encryption private key")
	}
	key, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

// Decryption is the server string of a key pair (mode "" = Mode).
func Decryption(mode, privateKey string) string {
	if mode == "" {
		mode = Mode
	}
	return strings.Join([]string{Prefix, mode, ServerTicket, privateKey}, ".")
}

// Encryption is the client string of a key pair (mode "" = Mode).
func Encryption(mode, publicKey string) string {
	if mode == "" {
		mode = Mode
	}
	return strings.Join([]string{Prefix, mode, ClientRTT, publicKey}, ".")
}

// ValidMode reports a known appearance mode.
func ValidMode(m string) bool { return modes[m] }

// ValidateEncryption checks a client string exactly as the Core will parse
// it (paddings first, then 1–8 keys of 32 or 1184 bytes that are valid
// X25519 public / ML-KEM-768 encapsulation keys), so a pasted link can never
// make the exit's Core refuse its configuration.
func ValidateEncryption(s string) error {
	parts := strings.Split(s, ".")
	if len(parts) < 4 || parts[0] != Prefix || !modes[parts[1]] || (parts[2] != "0rtt" && parts[2] != "1rtt") {
		return errors.New("encryption 格式无效（应为 mlkem768x25519plus.<native|xorpub|random>.<0rtt|1rtt>.<密钥>）")
	}
	return checkTail(parts[3:], func(b []byte) bool {
		switch len(b) {
		case 32:
			_, err := ecdh.X25519().NewPublicKey(b)
			return err == nil
		case mlkem.EncapsulationKeySize768:
			_, err := mlkem.NewEncapsulationKey768(b)
			return err == nil
		}
		return false
	})
}

// ValidateDecryption checks a server string (X25519 private 32 bytes or
// ML-KEM-768 seed 64 bytes).
func ValidateDecryption(s string) error {
	parts := strings.Split(s, ".")
	if len(parts) < 4 || parts[0] != Prefix || !modes[parts[1]] || !validTicket(parts[2]) {
		return errors.New("decryption 格式无效")
	}
	return checkTail(parts[3:], func(b []byte) bool { return len(b) == 32 || len(b) == mlkem.SeedSize })
}

func validTicket(t string) bool {
	if !strings.HasSuffix(t, "s") {
		return false
	}
	from, to, ranged := strings.Cut(strings.TrimSuffix(t, "s"), "-")
	a, err := strconv.Atoi(from)
	if err != nil || a < 0 || len(from) > 6 {
		return false
	}
	if ranged {
		b, err := strconv.Atoi(to)
		return err == nil && b >= a && len(to) <= 6
	}
	return true
}

// checkTail: padding segments (len < 20, like the Core) first, then keys.
func checkTail(tail []string, validKey func([]byte) bool) error {
	keys := 0
	padLen := 0
	for i, seg := range tail {
		if len(seg) < 20 {
			if keys > 0 {
				return errors.New("encryption 的填充参数必须写在密钥前面")
			}
			if err := checkPadding(seg, i, &padLen); err != nil {
				return err
			}
			continue
		}
		b, err := base64.RawURLEncoding.Strict().DecodeString(seg)
		if err != nil || !validKey(b) {
			return errors.New("encryption 密钥无效")
		}
		keys++
	}
	if keys == 0 || keys > maxKeys {
		return errors.New("encryption 密钥数量无效")
	}
	return nil
}

// checkPadding mirrors the Core's ParsePadding for one "a-b-c" segment.
func checkPadding(seg string, i int, total *int) error {
	x := strings.Split(seg, "-")
	if len(x) != 3 {
		return errors.New("encryption 填充参数无效")
	}
	var y [3]int
	for j := range x {
		v, err := strconv.Atoi(x[j])
		if err != nil || v < 0 {
			return errors.New("encryption 填充参数无效")
		}
		y[j] = v
	}
	if i == 0 && (y[0] < 100 || y[1] < 35 || y[2] < 35) {
		return errors.New("encryption 首个填充长度不能小于 35")
	}
	if i%2 == 0 {
		*total += max(y[1], y[2])
	}
	if *total > 18+65535 {
		return errors.New("encryption 填充总长度过大")
	}
	return nil
}
