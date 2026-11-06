package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
)

// Tunnel relay auth (plan 225).
//
// Every type-2 tunnel's middle/exit relay service ({tid}_tls) used to accept
// anyone: an open proxy that also reached the exit's loopback services (the
// landing Core API) and bypassed entry billing. Each tunnel now has its own
// relay credential, derived from the panel's JWT secret (never stored, never
// returned by any API):
//
//   - connectors (the entry/middle chains_{tid}) always send it. A relay
//     handler without an authenticator ignores the user-auth feature, so this
//     is safe against handlers that do not require it yet;
//   - handlers (middle + exit {tid}_tls) require it once the tunnel's
//     relay_auth flag is 1 and the tunnel is enforceable (see
//     relayAuthEnforceable). Existing tunnels are switched by the ordered
//     rollout (relay_auth_api.go): connectors first, then handlers;
//   - enforced exits also carry the agent-side target guard metadata
//     (fork.120+ agents refuse loopback/private/own-IP targets that are not a
//     landing inbound; older agents ignore the key).

// relayAuthPipeMinFork: agents from this fork accept an auth-protected
// landing in PipePing (LeiKwan pipe probe). Older entry agents expect the
// unauthenticated relay error, so a pipe tunnel only enforces auth once every
// entry runs at least this fork.
const relayAuthPipeMinFork = 120

// relayTargetGuardKey is the exit relay handler metadata key of the target
// guard (agent fork.120+).
const relayTargetGuardKey = "flvx.targetGuard"

type relayAuthSpec struct {
	user    string
	pass    string
	enforce bool // handlers require the credential (and exits guard targets)
}

func tunnelRelayCredential(secret string, tunnelID int64) (string, string) {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("flvx-tunnel-relay-v1:" + strconv.FormatInt(tunnelID, 10)))
	return fmt.Sprintf("flvx-t%d", tunnelID), hex.EncodeToString(mac.Sum(nil))[:32]
}

// relayAuthFor is the relay auth of a tunnel runtime state.
func (h *Handler) relayAuthFor(state *tunnelCreateState) *relayAuthSpec {
	if h == nil || state == nil || state.TunnelID <= 0 {
		return nil
	}
	user, pass := tunnelRelayCredential(h.jwtSecret, state.TunnelID)
	enforce := false
	if state.RelayAuth {
		ok, _ := h.relayAuthEnforceable(state)
		enforce = ok
	}
	return &relayAuthSpec{user: user, pass: pass, enforce: enforce}
}

// relayAuthEnforceable: a tunnel can require relay auth when every node that
// dials one of its relays is a local FLVX agent this panel configures (no
// federation node) and, for a LeiKwan pipe tunnel, every entry agent's pipe
// probe understands an auth-protected landing.
func (h *Handler) relayAuthEnforceable(state *tunnelCreateState) (bool, string) {
	if state == nil {
		return false, "隧道不存在"
	}
	for _, id := range state.NodeIDList {
		if n := state.Nodes[id]; n != nil && n.IsRemote == 1 {
			return false, "含远程（联邦）节点"
		}
	}
	if state.LeiKwanPipe != nil {
		ids := make([]int64, 0, len(state.InNodes))
		for _, in := range state.InNodes {
			ids = append(ids, in.NodeID)
		}
		versions := state.nodeVersions
		if versions == nil && h != nil && h.repo != nil {
			versions, _ = h.repo.NodeVersions(ids)
			state.nodeVersions = versions
		}
		for _, id := range ids {
			if agentForkRevision(versions[id]) < relayAuthPipeMinFork {
				return false, fmt.Sprintf("利群管道入口 %s 的节点程序低于 fork.%d", nodeDisplayName(state.Nodes[id]), relayAuthPipeMinFork)
			}
		}
	}
	return true, ""
}

var agentForkPattern = regexp.MustCompile(`-fork\.(\d+)`)

// agentForkRevision: "3.0.27-fork.103 (debian/amd64)" → 103; 0 = unknown.
func agentForkRevision(version string) int {
	m := agentForkPattern.FindStringSubmatch(version)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// relayConnectorAuth is the connector auth block (always sent).
func relayConnectorAuth(auth *relayAuthSpec) map[string]interface{} {
	if auth == nil || auth.user == "" {
		return nil
	}
	return map[string]interface{}{"username": auth.user, "password": auth.pass}
}
