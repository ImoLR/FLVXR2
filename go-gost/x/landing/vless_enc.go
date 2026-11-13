package landing

// Plan 253: VLESS Encryption (Xray mlkem768x25519plus) landing nodes and
// encrypted VLESS outbounds (「我自己的节点」 chain upstreams). The panel only
// sends either to agents reporting landing_vless_enc ≥ VLESSEncVersion: an
// older agent rejects the unknown protocol (and with it the whole desired
// state) and would drop "encryption" from an outbound.

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// VLESSEncVersion is reported as landing_vless_enc.
const VLESSEncVersion = 1

// ProtocolVLESSEnc is the landing protocol key.
const ProtocolVLESSEnc = "vless_enc"

const vlessEncPrefix = "mlkem768x25519plus"

// VLESSEnc is a VLESS Encryption inbound: one client UUID, flow
// xtls-rprx-vision (or empty) and the full server "decryption" string. Raw
// TCP, no TLS; UDP rides the stream (Vision's XUDP).
type VLESSEnc struct {
	UUID       string `json:"uuid"`
	Flow       string `json:"flow"`
	Decryption string `json:"decryption"`
}

var vlessEncModes = map[string]bool{"native": true, "xorpub": true, "random": true}

// validVLESSEncString mirrors the pinned Core's parser (infra/conf/vless.go):
// prefix, mode, the third field (server: "<s>s" / "<from>-<to>s" ticket;
// client: 0rtt | 1rtt), then padding segments (< 20 chars) first and 1–8
// keys whose decoded length is one of keyLens. The panel checks the keys
// themselves; this keeps a bad string from ever reaching the Core.
func validVLESSEncString(s string, server bool) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 4 || len(s) > 16384 || parts[0] != vlessEncPrefix || !vlessEncModes[parts[1]] {
		return false
	}
	if server {
		t := strings.TrimSuffix(parts[2], "s")
		if t == parts[2] {
			return false
		}
		from, to, ranged := strings.Cut(t, "-")
		a, err := strconv.Atoi(from)
		if err != nil || a < 0 || len(from) > 6 {
			return false
		}
		if ranged {
			if b, err := strconv.Atoi(to); err != nil || b < a || len(to) > 6 {
				return false
			}
		}
	} else if parts[2] != "0rtt" && parts[2] != "1rtt" {
		return false
	}
	keys := 0
	for _, seg := range parts[3:] {
		if len(seg) < 20 {
			x := strings.Split(seg, "-")
			if keys > 0 || len(x) != 3 {
				return false
			}
			for _, v := range x {
				if n, err := strconv.Atoi(v); err != nil || n < 0 {
					return false
				}
			}
			continue
		}
		b, err := base64.RawURLEncoding.Strict().DecodeString(seg)
		if err != nil {
			return false
		}
		switch {
		case len(b) == 32:
		case server && len(b) == 64:
		case !server && len(b) == 1184:
		default:
			return false
		}
		keys++
	}
	return keys > 0 && keys <= 8
}

func (v *VLESSEnc) validate() error {
	if v == nil {
		return fmt.Errorf("missing VLESS Encryption credentials")
	}
	if _, err := uuid.Parse(v.UUID); err != nil || (v.Flow != "" && v.Flow != "xtls-rprx-vision") {
		return fmt.Errorf("invalid VLESS Encryption credentials")
	}
	if !validVLESSEncString(v.Decryption, true) {
		return fmt.Errorf("invalid VLESS Encryption decryption")
	}
	return nil
}

// vlessEncInbound fills a VLESS inbound with decryption: no fallbacks (the
// Core refuses them together), raw TCP without security.
func (n Node) vlessEncInbound(inbound map[string]any) error {
	v := n.VLESSEnc
	if err := v.validate(); err != nil {
		return err
	}
	client := map[string]any{"id": v.UUID, "email": n.InboundTag}
	if v.Flow != "" {
		client["flow"] = v.Flow
	}
	inbound["protocol"] = "vless"
	inbound["settings"] = map[string]any{"decryption": v.Decryption, "clients": []any{client}}
	inbound["streamSettings"] = map[string]any{"network": "tcp"}
	return nil
}
