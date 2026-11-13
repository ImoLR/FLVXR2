package repo

// Plan 253: 「VLESS Encryption」 landing nodes (protocol vless_enc): VLESS
// with Xray's mlkem768x25519plus encryption over raw TCP, no TLS / Reality.
// Unlike AnyTLS / Snell / Mieru they also run on native FLVX landings:
//   - native: the agent must report landing_vless_enc ≥ LandingVLESSEncVersion
//     (an older agent rejects the unknown protocol and with it the whole
//     desired state, so its nodes are withheld there);
//   - shared: the share must advertise protocol_caps.protocols.vless_enc.
// Keys (X25519 authentication, ML-KEM-768 + X25519 key exchange) are made
// once at node create, never per request or push.

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/vlessenc"
)

// LandingVLESSEncVersion: native agents run vless_enc nodes and VLESS
// Encryption outbounds (chain upstreams) from this landing_vless_enc on.
const LandingVLESSEncVersion = 1

// VLESSEncClientHint is the one-line client note on the node and link card.
const VLESSEncClientHint = "需较新的 Xray / Mihomo 客户端（v2rayN、v2rayNG、Clash Meta 等）；Surge、sing-box 暂不支持"

// VLESSEncAgentReason / VLESSEncShareReason: why the protocol is unavailable.
const (
	VLESSEncAgentReason = "该服务器的节点程序版本过低，升级后才能使用 VLESS Encryption"
	VLESSEncShareReason = "该共享服务器暂不支持 VLESS Encryption（需升级妙妙屋 Helper）"
)

// VLESSEncFlow is the flow of every vless_enc node FLVX creates: Vision pads
// the inner TLS handshake lengths (the Core supports it together with
// encryption; with the random appearance it never splices).
const VLESSEncFlow = "xtls-rprx-vision"

// isVLESSEnc reports the protocol.
func isVLESSEnc(p string) bool { return p == landingcustom.ProtocolVLESSEnc }

// NativeVLESSEncSupported: the native agent applies vless_enc nodes.
func NativeVLESSEncSupported(agentVersion int) bool { return agentVersion >= LandingVLESSEncVersion }

// newVLESSEncCredentials fills a vless_enc node: UUID v4, Vision, a fresh
// X25519 key pair (private key secret, public key = the client "password")
// and the appearance mode in ProtocolOpts.
func newVLESSEncCredentials(n *model.LandingNode) error {
	keys, err := vlessenc.NewKeys()
	if err != nil {
		return err
	}
	id, err := newUUIDv4()
	if err != nil {
		return err
	}
	n.UUID, n.Flow = id, VLESSEncFlow
	n.PrivateKey, n.PublicKey = keys.PrivateKey, keys.PublicKey
	raw, _ := json.Marshal(LandingProtocolOpts{Mode: vlessenc.Mode})
	n.ProtocolOpts = string(raw)
	return nil
}

// VLESSEncDecryption / VLESSEncEncryption: the node's server and client
// strings (derived, never stored twice).
func VLESSEncDecryption(n model.LandingNode) string {
	return vlessenc.Decryption(LandingNodeOpts(&n).Mode, n.PrivateKey)
}
func VLESSEncEncryption(n model.LandingNode) string {
	return vlessenc.Encryption(LandingNodeOpts(&n).Mode, n.PublicKey)
}

// VLESSEncDesired is the contract / native desired object of a node.
func VLESSEncDesired(n model.LandingNode) *landingcustom.VLESSEnc {
	return &landingcustom.VLESSEnc{UUID: n.UUID, Flow: n.Flow, Decryption: VLESSEncDecryption(n)}
}

// vlessEncURL is the standard share link (v2rayN / v2rayNG / Xray / mihomo):
// vless://uuid@host:port?encryption=…&flow=…&security=none&type=tcp#name.
func vlessEncURL(n model.LandingNode, host string, port int) string {
	authority := net.JoinHostPort(strings.Trim(strings.TrimSpace(host), "[]"), strconv.Itoa(port))
	q := url.Values{"encryption": {VLESSEncEncryption(n)}, "security": {"none"}, "type": {"tcp"}}
	if n.Flow != "" {
		q.Set("flow", n.Flow)
	}
	return "vless://" + n.UUID + "@" + authority + "?" + q.Encode() + "#" + url.PathEscape(n.Name)
}

// vlessEncClash is the Clash (mihomo ≥ 1.19.13, lab-tested 1.19.32) entry.
func vlessEncClash(n model.LandingNode, host string, port int, udp bool) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	var b strings.Builder
	line := func(format string, args ...interface{}) { b.WriteString(fmt.Sprintf(format, args...) + "\n") }
	line("- name: %s", yamlString(n.Name))
	line("  type: vless")
	line("  server: %s", yamlString(host))
	line("  port: %d", port)
	line("  uuid: %s", yamlString(n.UUID))
	if n.Flow != "" {
		line("  flow: %s", n.Flow)
	}
	line("  network: tcp")
	line("  tls: false")
	line("  udp: %t", udp)
	line("  encryption: %s", yamlString(VLESSEncEncryption(n)))
	return strings.TrimRight(b.String(), "\n")
}

// checkVLESSEncCreate: a new vless_enc node needs the capability of its
// server (native agent version / share caps).
func checkVLESSEncCreate(s *model.LandingServer, agentVersion int) error {
	if s.Source == "shared" {
		if _, ok := SharedProtocolCap(s, landingcustom.ProtocolVLESSEnc); !ok {
			return errors.New(VLESSEncShareReason)
		}
		return nil
	}
	if !NativeVLESSEncSupported(agentVersion) {
		return errors.New(VLESSEncAgentReason)
	}
	return nil
}

// newUUIDv4 is a random RFC 4122 version 4 UUID.
func newUUIDv4() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// NativeLandingNotSent returns the protocol filter of a native agent: the
// protocols its desired state must leave out (AnyTLS / Snell / Mieru always,
// VLESS Encryption below LandingVLESSEncVersion). Scan guard targets, user
// groups and nodes all use it, so they always describe the same node set.
func NativeLandingNotSent(agentVLESSEnc int) func(protocol string) bool {
	vlessEnc := NativeVLESSEncSupported(agentVLESSEnc)
	return func(p string) bool {
		return landingcustom.IsExtraProtocol(p) || (isVLESSEnc(p) && !vlessEnc)
	}
}
