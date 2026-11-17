package repo

// Plan 274: AnyTLS also runs on native FLVX landings (Snell / Mieru stay on
// mmwx-shared servers). The node is made like a shared AnyTLS node (plan 121):
// a random password, a self-signed ECDSA certificate per node (SAN = the
// server's AnyTLS SNI, else its public host), the SHA-256 of the certificate
// pinned by clients that can pin (Clash fingerprint, Surge server-cert-
// fingerprint-sha256, sing-box certificate) and insecure=1 in the share link.
// The agent renders it with the same Core inbound as a plan-250 chain node
// (TLS 1.2+, ALPN h2 / http/1.1, default padding), listening publicly.
//
// An agent below LandingAnyTLSVersion rejects a public AnyTLS node and with it
// the whole desired state, so its AnyTLS nodes are withheld there (port,
// credentials and counters stay) — the same gate as plan 253 vless_enc.

import (
	"encoding/json"
	"errors"
	"strings"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// LandingAnyTLSVersion: native agents run public AnyTLS nodes from this
// landing_anytls on.
const LandingAnyTLSVersion = 1

// AnyTLSAgentReason: why AnyTLS is unavailable on a native server.
const AnyTLSAgentReason = "该服务器的节点程序版本过低，升级后才能使用 AnyTLS"

// NativeAnyTLSSupported: the native agent applies public AnyTLS nodes.
func NativeAnyTLSSupported(agentVersion int) bool { return agentVersion >= LandingAnyTLSVersion }

// isAnyTLS reports the protocol.
func isAnyTLS(p string) bool { return p == landingcustom.ProtocolAnyTLS }

// sharedOnlyProtocol: protocols only an mmwx share can run (plan 121).
func sharedOnlyProtocol(p string) bool { return landingcustom.IsExtraProtocol(p) && !isAnyTLS(p) }

// LandingAgentProtocols are the protocol capabilities of a native agent that
// decide which of its nodes may be sent (landing_vless_enc / landing_anytls).
type LandingAgentProtocols struct {
	VLESSEnc int
	AnyTLS   int
}

// NodeLandingProtocols reads them from the node row.
func NodeLandingProtocols(n *model.Node) LandingAgentProtocols {
	if n == nil {
		return LandingAgentProtocols{}
	}
	return LandingAgentProtocols{VLESSEnc: n.LandingVLESSEnc, AnyTLS: n.LandingAnyTLS}
}

// allLandingProtocols is "every protocol applied" (views, not the agent push).
var allLandingProtocols = LandingAgentProtocols{VLESSEnc: LandingVLESSEncVersion, AnyTLS: LandingAnyTLSVersion}

// nativeSends reports whether a native agent with caps runs protocol p.
func (c LandingAgentProtocols) nativeSends(p string) bool {
	switch {
	case isVLESSEnc(p):
		return NativeVLESSEncSupported(c.VLESSEnc)
	case isAnyTLS(p):
		return NativeAnyTLSSupported(c.AnyTLS)
	case sharedOnlyProtocol(p):
		return false
	}
	return true
}

// nativeProtocolAgentReason: the refusal for switching on / creating a native
// protocol the agent cannot run yet ("" = fine).
func nativeProtocolAgentReason(p string, caps LandingAgentProtocols) string {
	switch {
	case isVLESSEnc(p) && !NativeVLESSEncSupported(caps.VLESSEnc):
		return VLESSEncAgentReason
	case isAnyTLS(p) && !NativeAnyTLSSupported(caps.AnyTLS):
		return AnyTLSAgentReason
	}
	return ""
}

// checkNativeAnyTLSCreate: a new AnyTLS node on a native server needs an agent
// that runs it.
func checkNativeAnyTLSCreate(s *model.LandingServer, protocol string, caps LandingAgentProtocols) error {
	if s.Source != "native" || !isAnyTLS(protocol) {
		return nil
	}
	if !NativeAnyTLSSupported(caps.AnyTLS) {
		return errors.New(AnyTLSAgentReason)
	}
	return nil
}

// NativeAnyTLSDesired is the native agent's anytls object of a node (the
// same shape as a plan-250 chain node: password + inline certificate/key).
func NativeAnyTLSDesired(n model.LandingNode) map[string]interface{} {
	return map[string]interface{}{"password": n.Password, "certificate_pem": n.TLSCertPEM, "key_pem": n.TLSKeyPEM}
}

// landingProtocolUDP: whether a node's client configs announce UDP. Shared
// servers follow the share's capability (plan 121); native nodes always carry
// UDP (SS2022 natively, VLESS / AnyTLS over the TCP stream).
func landingProtocolUDP(s *model.LandingServer, n model.LandingNode) bool {
	if s != nil && s.Source == "shared" && landingcustom.IsCapProtocol(n.Protocol) {
		cp, _ := SharedProtocolCap(s, n.Protocol)
		return cp.UDP
	}
	return true
}

// nativeAnyTLSDefaultSNI: the first valid Reality server name of a native
// server ("" = none), the SNI a new AnyTLS node gets when the server has no
// AnyTLS SNI and its public host is an IP (same rule as chainSNI).
func nativeAnyTLSDefaultSNI(s *model.LandingServer) string {
	var names []string
	_ = json.Unmarshal([]byte(s.RealityServerNames), &names)
	for _, name := range names {
		if name = strings.ToLower(strings.TrimSpace(name)); validLandingHostname(name) {
			return name
		}
	}
	return ""
}
