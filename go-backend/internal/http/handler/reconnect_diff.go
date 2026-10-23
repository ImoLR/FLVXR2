package handler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go-backend/internal/ws"
)

// Diff-only reconnect redeploy (plan 138).
//
// Before plan 138 every agent reconnect deleted and re-added every tunnel and
// forward service of the node (each re-add restarts a listener) even when
// nothing had changed. Agents that report runtime revisions (handshake
// `runtimeRevs=1`, ListServices `revs`) now get one ListServices call; per
// tunnel and per forward the panel builds the payloads it would push (the same
// builders as the push itself) and only pushes what is missing or differs.
// Objects of tunnels/forwards removed while the node was away are deleted
// (same ownership rules as the orphan sweep). An agent restart empties its
// revisions, so it is redeployed in full, exactly like older agents.

// maxReconnectOrphans bounds what a reconnect removes on its own; more than
// this looks like a database/ownership problem, not like a few deletions made
// while the node was away, so they are only reported.
const maxReconnectOrphans = 20

// recentPushWindow: an object pushed to the node during its current session
// this recently may belong to an operation that has not committed its database
// change yet (tunnel create and rule updates dispatch first); a reconnect
// leaves it to the next one. Pushes of an earlier session cannot be in flight.
const recentPushWindow = 2 * time.Minute

type recentPushLog struct {
	mu    sync.Mutex
	nodes map[int64]map[string]time.Time // node -> "<kind>:<name>" -> last push
}

// note records the objects of a state-changing command (cheap no-op for the
// read-only and delete commands).
func (l *recentPushLog) note(nodeID int64, commandType string, data interface{}, now time.Time) {
	keys := ws.ExpectedRuntimeRevs(commandType, data)
	if len(keys) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nodes == nil {
		l.nodes = make(map[int64]map[string]time.Time)
	}
	m := l.nodes[nodeID]
	if m == nil {
		m = make(map[string]time.Time)
		l.nodes[nodeID] = m
	}
	for key, t := range m {
		if now.Sub(t) > 2*recentPushWindow {
			delete(m, key)
		}
	}
	for key := range keys {
		m[key] = now
	}
}

// inFlight: the object was pushed during the current session (started at
// sessionStart) less than recentPushWindow ago.
func (l *recentPushLog) inFlight(nodeID int64, key string, sessionStart, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.nodes[nodeID][key]
	return ok && !t.Before(sessionStart) && now.Sub(t) < recentPushWindow
}

// agentRuntimeInventory is a capable agent's runtime state at reconnect.
type agentRuntimeInventory struct {
	inv     runtimeInventory
	present map[string]bool
	revs    map[string]string
	boot    string
}

func newAgentRuntimeInventory(i runtimeInventory) *agentRuntimeInventory {
	a := &agentRuntimeInventory{inv: i, present: map[string]bool{}, revs: i.Revs, boot: i.Boot}
	for kind, names := range map[string][]string{"service": i.Services, "chain": i.Chains, "limiter": i.Limiters, "nft": i.Nftables} {
		for _, name := range names {
			a.present[kind+":"+name] = true
		}
	}
	if a.revs == nil {
		a.revs = map[string]string{}
	}
	return a
}

// matches: every expected object is running with exactly the expected revision.
func (a *agentRuntimeInventory) matches(expected map[string]string) bool {
	if a == nil || len(expected) == 0 {
		return false
	}
	for key, rev := range expected {
		if rev == "" || !a.present[key] || a.revs[key] != rev {
			return false
		}
	}
	return true
}

// reconnectInventory returns the agent's runtime inventory with revisions, or
// nil + a short note when the node has to be redeployed in full.
func (h *Handler) reconnectInventory(nodeID int64) (*agentRuntimeInventory, string) {
	if h == nil {
		return nil, "no handler"
	}
	capable := h.runtimeRevsCapable
	if capable == nil {
		capable = func(id int64) bool { return h.wsServer != nil && h.wsServer.RuntimeRevsCapable(id) }
	}
	if !capable(nodeID) {
		return nil, "agent without runtime revisions"
	}
	node, err := h.getNodeRecord(nodeID)
	if err != nil || node == nil {
		return nil, "node record unavailable"
	}
	if node.IsRemote == 1 {
		return nil, "remote node"
	}
	i, source, _, err := h.readNodeInventory(nodeID)
	if err != nil || source != "ListServices" || i.Revs == nil {
		if err != nil {
			return nil, "ListServices failed: " + err.Error()
		}
		return nil, "inventory without revisions"
	}
	return newAgentRuntimeInventory(i), ""
}

// forwardRuntimeInputs are the resolved inputs of every gost/nftables push of
// a forward (shared by the push and the reconnect comparison).
type forwardRuntimeInputs struct {
	tunnel             *tunnelRecord
	userTunnelID       int64
	limiterID          *int64
	speed              *int
	arenaMode          bool
	dynamicLimiterName string
	serviceBase        string
}

func (h *Handler) resolveForwardRuntimeInputs(forward *forwardRecord) (*forwardRuntimeInputs, error) {
	tunnel, err := h.getTunnelRecord(forward.TunnelID)
	if err != nil {
		return nil, err
	}
	// Resolve user tunnel first so runtime service name can carry the real user_tunnel id.
	userTunnelID, utLimiterID, utSpeed, err := h.resolveUserTunnelAndLimiter(forward.UserID, forward.TunnelID)
	if err != nil {
		return nil, err
	}
	in := &forwardRuntimeInputs{tunnel: tunnel, userTunnelID: userTunnelID}
	// Determine limiter from forward's SpeedID first, fallback to UserTunnel's limiter.
	if forward.SpeedID.Valid && forward.SpeedID.Int64 > 0 {
		limit, err := h.repo.GetSpeedLimit(forward.SpeedID.Int64)
		if err == nil && limit.Speed > 0 {
			id := forward.SpeedID.Int64
			speedVal := limit.Speed
			in.limiterID, in.speed, in.arenaMode = &id, &speedVal, limit.ArenaMode == 1
		}
	}
	if in.limiterID == nil {
		in.limiterID, in.speed = utLimiterID, utSpeed
		if in.limiterID != nil {
			if limit, err := h.repo.GetSpeedLimit(*in.limiterID); err == nil && limit != nil {
				in.arenaMode = limit.ArenaMode == 1
			}
		}
	}
	if forward.SpeedLimitEnabled && forward.SpeedLimit > 0 {
		in.dynamicLimiterName = fmt.Sprintf("forward_%d_speed", forward.ID)
	}
	in.serviceBase = buildForwardServiceBaseWithResolvedUserTunnel(forward.ID, forward.UserID, userTunnelID)
	return in, nil
}

func mergeRevs(dst, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}

// forwardRuntimeUnchangedOnNode reports whether the agent already runs exactly
// what syncForwardServicesOnNode would push for this forward on this node.
// Any doubt (WG path, landing block, lookup error) means "changed".
func (h *Handler) forwardRuntimeUnchangedOnNode(forward *forwardRecord, nodeID int64, inv *agentRuntimeInventory) bool {
	if inv == nil || forward == nil || forward.Status != 1 || strings.EqualFold(forward.Mode, "wg_path") {
		return false
	}
	if err := h.repo.LandingForwardBlockReason(forward.ID); err != nil {
		return false
	}
	if err := h.checkForwardRecordLanding(context.Background(), forward); err != nil {
		return false
	}
	all, err := h.listForwardPorts(forward.ID)
	if err != nil || len(all) == 0 {
		return false
	}
	ports := make([]forwardPortRecord, 0, len(all))
	for _, port := range all {
		if port.NodeID == nodeID {
			ports = append(ports, port)
		}
	}
	if len(ports) == 0 {
		return true // the push does nothing for this node either
	}
	in, err := h.resolveForwardRuntimeInputs(forward)
	if err != nil {
		return false
	}
	expected := map[string]string{}
	if strings.EqualFold(forward.Mode, "nftables") {
		chainNodes, _ := h.listChainNodesForTunnel(forward.TunnelID)
		nodeRules := filterRulesByNodeID(buildNftablesRulePayloads(forward, in.tunnel, ports, chainNodes, in.userTunnelID, in.speed), nodeID)
		if len(nodeRules) == 0 {
			return true
		}
		mergeRevs(expected, ws.ExpectedRuntimeRevs("AddNftablesRules", AddNftablesRulesRequest{Rules: nodeRules}))
		return inv.matches(expected)
	}
	tunnelTLSProtocol, err := h.isTunnelSelectedTLSProtocol(forward.TunnelID)
	if err != nil {
		return false
	}
	var limiterName string
	for _, fp := range ports {
		if in.dynamicLimiterName != "" {
			mergeRevs(expected, ws.ExpectedRuntimeRevs("AddLimiters", buildDynamicLimiterPayload(in.dynamicLimiterName, forward.SpeedLimit)))
		} else if in.limiterID != nil && in.speed != nil {
			limiterName = buildRuntimeLimiterName(*in.limiterID, fp.NodeID, in.arenaMode)
			_, addPayload := buildNamedLimiterAddPayload(strings.TrimSpace(limiterName), *in.speed)
			mergeRevs(expected, ws.ExpectedRuntimeRevs("AddLimiters", addPayload))
		}
		node, err := h.getNodeRecord(fp.NodeID)
		if err != nil || node == nil {
			return false
		}
		services := buildForwardServiceConfigs(in.serviceBase, forward, in.tunnel, node, fp.Port, strings.TrimSpace(fp.InIP), in.limiterID, limiterName, tunnelTLSProtocol)
		mergeRevs(expected, ws.ExpectedRuntimeRevs("UpdateService", services))
	}
	return inv.matches(expected)
}

// removeReconnectOrphans deletes FLVX runtime objects the agent still runs for
// tunnels/forwards that no longer belong to the node (removed while it was
// away). Ownership rules and guards are the orphan sweep's.
func (h *Handler) removeReconnectOrphans(nodeID int64, inv *agentRuntimeInventory) (int, string) {
	if inv == nil {
		return 0, ""
	}
	s, err := h.repo.ReadRuntimeOwnership()
	if err != nil {
		return 0, "ownership read failed: " + err.Error()
	}
	if reason := sweepNodeGuard(s, nodeID); reason != "" {
		return 0, "orphans not checked: " + reason
	}
	var orphans []runtimeObject
	for _, o := range inv.inv.objects() {
		if strings.HasPrefix(o.Name, "fed_") {
			return 0, "orphans not checked: federation runtime names"
		}
		if obj, status := classifyRuntime(s, nodeID, o); status == "orphan" {
			orphans = append(orphans, obj)
		}
	}
	if len(orphans) == 0 {
		return 0, ""
	}
	if len(orphans) > maxReconnectOrphans {
		return 0, fmt.Sprintf("%d orphaned objects found (more than %d): not removed automatically, run the orphan sweep", len(orphans), maxReconnectOrphans)
	}
	removed := 0
	var done, failed, kept []string
	var sessionStart time.Time
	if h.wsServer != nil {
		sessionStart = h.wsServer.SessionStartedAt(nodeID)
	}
	for _, o := range orphans {
		if h.recentPushes.inFlight(nodeID, o.Kind+":"+o.Name, sessionStart, time.Now()) {
			kept = append(kept, o.Kind+" "+o.Name)
			continue
		}
		ok, err := h.removeOrphanChecked(nodeID, o)
		switch {
		case err != nil:
			failed = append(failed, fmt.Sprintf("%s %s: %v", o.Kind, o.Name, err))
		case ok:
			removed++
			done = append(done, o.Kind+" "+o.Name)
		}
	}
	var notes []string
	if len(done) > 0 {
		notes = append(notes, "removed orphans: "+strings.Join(done, ", "))
	}
	if len(kept) > 0 {
		notes = append(notes, "kept recently pushed (next reconnect decides): "+strings.Join(kept, ", "))
	}
	if len(failed) > 0 {
		notes = append(notes, "orphan removal failed: "+strings.Join(failed, "; "))
	}
	return removed, strings.Join(notes, "; ")
}

// removeOrphanChecked re-reads ownership right before deleting. Tunnel objects
// are checked under the tunnel's runtime lock, which every tunnel create /
// update / delete holds while it dispatches and commits.
func (h *Handler) removeOrphanChecked(nodeID int64, o runtimeObject) (bool, error) {
	if o.Kind == "chain" || (o.Kind == "service" && forwardRuntimeID(o) == 0) {
		if o.OwnerID > 0 {
			unlock := h.lockTunnelRuntime(o.OwnerID)
			defer unlock()
		}
	}
	latest, err := h.repo.ReadRuntimeOwnership()
	if err != nil {
		return false, err
	}
	if sweepNodeGuard(latest, nodeID) != "" {
		return false, nil
	}
	if _, status := classifyRuntime(latest, nodeID, o); status != "orphan" {
		return false, nil
	}
	if err := h.deleteOrphanRuntime(nodeID, o); err != nil {
		return false, err
	}
	return true, nil
}

func formatRedeployTook(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Milliseconds())
}
