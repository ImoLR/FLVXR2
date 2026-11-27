package repo

// Plan 317: Snell and Mieru also run on native FLVX landings, so every
// protocol a mmwx share offers runs natively too. A node is made exactly like
// a shared one (plan 121, newExtraLandingCredentials: random PSK / username +
// password, options copied from the server's Snell / Mieru defaults), so its
// links, Clash / Surge configs and subscription entries are byte-identical to
// a shared node with the same fields — a node moved over from a share keeps
// every client. The agent renders the same Core inbounds as mmwx Custom
// (contract landing-protocols-v1 §6): Snell = one PSK user (v4 / v5, obfs
// none / http / tls), TCP only; Mieru = one username / password user on TCP
// and UDP of the port (the client picks the underlay; the server's 客户端传输
// only goes into client configs).
//
// An agent below LandingSnellVersion / LandingMieruVersion rejects such a node
// and with it the whole desired state, so its nodes are withheld there (port,
// credentials and counters stay) — the same gate as plan 253 / 274.

import (
	"strconv"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
)

// LandingSnellVersion / LandingMieruVersion: native agents run public Snell /
// Mieru nodes from this landing_snell / landing_mieru on.
const (
	LandingSnellVersion = 1
	LandingMieruVersion = 1
)

// SnellAgentReason / MieruAgentReason: why the protocol is unavailable on a
// native server.
const (
	SnellAgentReason = "该服务器的节点程序版本过低，升级后才能使用 Snell"
	MieruAgentReason = "该服务器的节点程序版本过低，升级后才能使用 Mieru"
)

// NativeSnellSupported / NativeMieruSupported: the native agent applies them.
func NativeSnellSupported(agentVersion int) bool { return agentVersion >= LandingSnellVersion }
func NativeMieruSupported(agentVersion int) bool { return agentVersion >= LandingMieruVersion }

func isSnell(p string) bool { return p == landingcustom.ProtocolSnell }
func isMieru(p string) bool { return p == landingcustom.ProtocolMieru }

// NativeSnellDesired is the native agent's snell object of a node: the same
// values a share gets (fillSharedExtraProtocol): version 0 (older rows) = 4,
// obfs "" = none, the obfs host only with http / tls obfuscation.
func NativeSnellDesired(n model.LandingNode) map[string]interface{} {
	o := LandingNodeOpts(&n)
	version := o.Version
	if version == 0 {
		version = 4
	}
	out := map[string]interface{}{"psk": n.Password, "version": version, "obfs": "none"}
	if o.Obfs == "http" || o.Obfs == "tls" {
		out["obfs"], out["obfs_host"] = o.Obfs, o.ObfsHost
	}
	return out
}

// NativeMieruDesired is the native agent's mieru object of a node.
func NativeMieruDesired(n model.LandingNode) map[string]interface{} {
	return map[string]interface{}{"username": n.Username, "password": n.Password}
}

// NativeExtraSignature: what the native desired state's revision signature
// gains for the AnyTLS / Snell / Mieru nodes an agent gets, so an agent
// upgrade that starts sending them advances the revision. "" without any
// (servers without such nodes keep their signature byte for byte).
func NativeExtraSignature(anytls, snell, mieru int) string {
	sig := ""
	for _, part := range []struct {
		key string
		n   int
	}{{"at", anytls}, {"sn", snell}, {"mi", mieru}} {
		if part.n > 0 {
			sig += ";" + part.key + "=" + strconv.Itoa(part.n)
		}
	}
	return sig
}
