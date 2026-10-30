package handler

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 168: every entry speed limit in the agent's kernel (tc HTB + fq_codel).
//
// For agents that advertise entryRateTc=1 (and whose kernel passed the probe)
// the panel sends ONE tree per node (SetEntryRates): a group per user with a
// 速率 on the node (plan 159 row / user default), a leaf per rule with an own
// limit (dynamic speed or non-arena 限速规则), a leaf per arena, and the listen
// port of every limited rule. Those limits are then NOT sent as gost limiters /
// metadata.userLimiter / nftables policers (no double limiting). Older agents,
// kernels without tc and remote nodes keep the fork.97 payloads byte for byte.
//
// An arena whose rules belong to two or more users of which at least one has
// a 速率 on the node cannot sit in one HTB tree with the users' classes (a rule
// has one parent); its rules keep the arena on the agent path (gost shared
// limiter / nft per-rule policer as today), their users' 速率 goes to tc.

const (
	entryRateDebounce = 1500 * time.Millisecond
	entryRateLoopTick = time.Minute
)

type entryRatePayload struct {
	Version int                     `json:"version"`
	Classes []entryRateClassPayload `json:"classes"`
	Ports   []entryRatePortPayload  `json:"ports"`
}

type entryRateClassPayload struct {
	ID       string `json:"id"`
	Parent   string `json:"parent,omitempty"`
	Group    bool   `json:"group,omitempty"`
	RateMbps int    `json:"rate_mbps"`
}

type entryRatePortPayload struct {
	Port  int    `json:"port"`
	TCP   bool   `json:"tcp,omitempty"`
	UDP   bool   `json:"udp,omitempty"`
	Class string `json:"class"`
}

// entryRatePlan is what a tc node gets: the tree and the rules whose arena
// stays on the agent path. nil plan = the node uses the old path.
type entryRatePlan struct {
	payload    entryRatePayload
	agentArena map[int64]bool
	rev        string // runtime revision of the payload ("" = empty tree)
}

// tcCovers: the rule's own limit is in the tree (no gost limiter / policer).
func (p *entryRatePlan) tcCovers(forwardID int64) bool {
	return p != nil && !p.agentArena[forwardID]
}

// buildEntryRatePlan computes the tree of one node from its active forwards
// and the users' 速率 there.
func buildEntryRatePlan(rules []repo.EntryRateRuleRow, userRates map[int64]int) *entryRatePlan {
	type rule struct {
		repo.EntryRateRuleRow
		own   int
		arena int64
	}
	seenPort := map[int]bool{}
	var list []rule
	arenaUsers := map[int64]map[int64]bool{}
	arenaSpeed := map[int64]int{}
	for _, r := range rules {
		if strings.EqualFold(r.Mode, "wg_path") || r.Port < 1 || r.Port > 65535 || seenPort[r.Port] {
			continue
		}
		seenPort[r.Port] = true
		x := rule{EntryRateRuleRow: r}
		switch {
		case r.SpeedLimitEnabled && r.SpeedLimit > 0:
			x.own = r.SpeedLimit
		case r.LimitID > 0 && r.LimitSpeed > 0 && r.LimitArena:
			x.arena = r.LimitID
			arenaSpeed[r.LimitID] = r.LimitSpeed
			if arenaUsers[r.LimitID] == nil {
				arenaUsers[r.LimitID] = map[int64]bool{}
			}
			arenaUsers[r.LimitID][r.UserID] = true
		case r.LimitID > 0 && r.LimitSpeed > 0:
			x.own = r.LimitSpeed
		}
		list = append(list, x)
	}
	conflict := map[int64]bool{}
	arenaParent := map[int64]string{}
	for id, users := range arenaUsers {
		withRow := 0
		var only int64
		for u := range users {
			only = u
			if userRates[u] > 0 {
				withRow++
			}
		}
		switch {
		case len(users) >= 2 && withRow > 0:
			conflict[id] = true
		case len(users) == 1 && withRow == 1:
			arenaParent[id] = userClassID(only)
		}
	}
	plan := &entryRatePlan{payload: entryRatePayload{Version: 1, Classes: []entryRateClassPayload{}, Ports: []entryRatePortPayload{}}, agentArena: map[int64]bool{}}
	classes := map[string]entryRateClassPayload{}
	for _, r := range list {
		row := userRates[r.UserID]
		parent := ""
		if row > 0 {
			parent = userClassID(r.UserID)
			classes[parent] = entryRateClassPayload{ID: parent, Group: true, RateMbps: row}
		}
		class := parent
		switch {
		case r.own > 0:
			class = "r" + strconv.FormatInt(r.ForwardID, 10)
			classes[class] = entryRateClassPayload{ID: class, Parent: parent, RateMbps: r.own}
		case r.arena > 0 && conflict[r.arena]:
			plan.agentArena[r.ForwardID] = true
		case r.arena > 0:
			class = "a" + strconv.FormatInt(r.arena, 10)
			classes[class] = entryRateClassPayload{ID: class, Parent: arenaParent[r.arena], RateMbps: arenaSpeed[r.arena]}
		}
		if class != "" {
			plan.payload.Ports = append(plan.payload.Ports, entryRatePortPayload{Port: r.Port, TCP: true, UDP: true, Class: class})
		}
	}
	for _, c := range classes {
		plan.payload.Classes = append(plan.payload.Classes, c)
	}
	sort.Slice(plan.payload.Classes, func(i, j int) bool { return plan.payload.Classes[i].ID < plan.payload.Classes[j].ID })
	sort.Slice(plan.payload.Ports, func(i, j int) bool { return plan.payload.Ports[i].Port < plan.payload.Ports[j].Port })
	plan.rev = ws.ExpectedRuntimeRevs("SetEntryRates", plan.payload)["entryrate:entry"]
	return plan
}

func userClassID(userID int64) string { return "u" + strconv.FormatInt(userID, 10) }

// entryRateTc: the node's agent shapes entry limits in the kernel.
func (h *Handler) entryRateTc(nodeID int64) bool {
	if h == nil || nodeID <= 0 {
		return false
	}
	if h.entryRateTcFn != nil {
		return h.entryRateTcFn(nodeID)
	}
	return h.wsServer != nil && h.wsServer.EntryRateTcCapable(nodeID)
}

// entryRatePlanFor returns the node's current tree, or nil when the node uses
// the old path (agent / kernel without tc, remote node) or the inputs could
// not be read (then the old path is the safe choice for this push).
func (h *Handler) entryRatePlanFor(nodeID int64) *entryRatePlan {
	if !h.entryRateTc(nodeID) {
		return nil
	}
	node, err := h.getNodeRecord(nodeID)
	if err != nil || node == nil || node.IsRemote == 1 {
		return nil
	}
	rules, err := h.repo.EntryRateRules(nodeID)
	if err != nil {
		return nil
	}
	rates, err := h.repo.NodeUserRates(nodeID)
	if err != nil {
		return nil
	}
	return buildEntryRatePlan(rules, rates)
}

// applyRateRefs sets how one rule's gost services on a node reference speed
// limiters: tc node → no limiter for what the tree covers, no userLimiter;
// otherwise the plan-159 reference (fork.97 payload).
func (h *Handler) applyRateRefs(services []map[string]interface{}, forward *forwardRecord, node *nodeRecord, rate int, plan *entryRatePlan) {
	if plan != nil {
		if plan.tcCovers(forward.ID) {
			for _, s := range services {
				delete(s, "limiter")
			}
		}
		return
	}
	applyUserRateRef(services, h.userRateRefFor(forward.UserID, node, rate))
}

// entryRatePushLog remembers per node session what tree was pushed (memory
// only, one entry per tc node).
type entryRatePushLog struct {
	mu    sync.Mutex
	nodes map[int64]entryRatePushed
}

type entryRatePushed struct {
	session    time.Time
	rev        string
	agentArena map[int64]bool
}

func (l *entryRatePushLog) get(nodeID int64, session time.Time) (entryRatePushed, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.nodes[nodeID]
	if !ok || !p.session.Equal(session) {
		return entryRatePushed{}, false
	}
	return p, true
}

func (l *entryRatePushLog) set(nodeID int64, p entryRatePushed) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nodes == nil {
		l.nodes = map[int64]entryRatePushed{}
	}
	l.nodes[nodeID] = p
}

func (l *entryRatePushLog) forget(nodeID int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.nodes, nodeID)
}

// syncEntryRates pushes the node's tree when it differs from what this agent
// session last got; rules whose arena moved between tc and the agent path are
// re-pushed (their service / nft payload changed).
func (h *Handler) syncEntryRates(nodeID int64) error {
	before, after, err := h.syncEntryRatesLocked(nodeID)
	if before != nil {
		h.resyncAgentArenaChanges(nodeID, before, after)
	}
	return err
}

// syncEntryRatesLocked returns the agent-arena sets before / after when a
// push in this session changed the tree (nil before = nothing to re-push).
func (h *Handler) syncEntryRatesLocked(nodeID int64) (map[int64]bool, map[int64]bool, error) {
	h.entryRateMu.Lock()
	defer h.entryRateMu.Unlock()
	plan := h.entryRatePlanFor(nodeID)
	if plan == nil {
		return nil, nil, nil
	}
	session := h.nodeSessionStart(nodeID)
	last, known := h.entryRatePushed.get(nodeID, session)
	if known && last.rev == plan.rev {
		return nil, nil, nil
	}
	if !known && plan.rev == "" {
		// Nothing pushed in this session and nothing to limit: an empty tree
		// is only sent to clear one the agent reported (reconnect) or one
		// pushed earlier in this session.
		h.entryRatePushed.set(nodeID, entryRatePushed{session: session, rev: "", agentArena: plan.agentArena})
		return nil, nil, nil
	}
	if err := h.pushEntryRates(nodeID, plan); err != nil {
		return nil, nil, err
	}
	h.entryRatePushed.set(nodeID, entryRatePushed{session: session, rev: plan.rev, agentArena: plan.agentArena})
	if known {
		return last.agentArena, plan.agentArena, nil
	}
	return nil, nil, nil
}

func (h *Handler) pushEntryRates(nodeID int64, plan *entryRatePlan) error {
	if _, err := h.sendNodeCommand(nodeID, "SetEntryRates", plan.payload, false, false); err != nil {
		return fmt.Errorf("入口限速下发失败：%w", err)
	}
	return nil
}

func (h *Handler) resyncAgentArenaChanges(nodeID int64, before, after map[int64]bool) {
	changed := map[int64]bool{}
	for id := range before {
		if !after[id] {
			changed[id] = true
		}
	}
	for id := range after {
		if !before[id] {
			changed[id] = true
		}
	}
	for id := range changed {
		forward, err := h.getForwardRecord(id)
		if err != nil || forward == nil || forward.Status != 1 {
			continue
		}
		if err := h.syncForwardServicesOnNode(forward, nodeID); err != nil {
			fmt.Printf("entry rate: forward %d re-push on node %d failed: %v\n", id, nodeID, err)
		}
	}
}

// entryRateReconnect makes a reconnected tc agent's tree match before its
// forwards are compared (inv nil = unknown: push). The plan is kept in inv for
// the forward comparison.
func (h *Handler) entryRateReconnect(nodeID int64, inv *agentRuntimeInventory) string {
	h.entryRatePushed.forget(nodeID)
	if !h.entryRateTc(nodeID) {
		return ""
	}
	h.entryRateMu.Lock()
	defer h.entryRateMu.Unlock()
	plan := h.entryRatePlanFor(nodeID)
	if plan == nil {
		return "entry rates: inputs not read"
	}
	if inv != nil {
		inv.entryPlan, inv.entryPlanLoaded = plan, true
	}
	session := h.nodeSessionStart(nodeID)
	if inv != nil && inv.revs["entryrate:entry"] == plan.rev {
		h.entryRatePushed.set(nodeID, entryRatePushed{session: session, rev: plan.rev, agentArena: plan.agentArena})
		return ""
	}
	if err := h.pushEntryRates(nodeID, plan); err != nil {
		return "entry rates: " + err.Error()
	}
	h.entryRatePushed.set(nodeID, entryRatePushed{session: session, rev: plan.rev, agentArena: plan.agentArena})
	return fmt.Sprintf("entry rates pushed (%d classes, %d ports)", len(plan.payload.Classes), len(plan.payload.Ports))
}

// planFor: the plan of the inventory's node, loaded once per reconnect.
func (a *agentRuntimeInventory) planFor(h *Handler, nodeID int64) *entryRatePlan {
	if a != nil && a.entryPlanLoaded {
		return a.entryPlan
	}
	return h.entryRatePlanFor(nodeID)
}

// entryRateDirty batches "something on this node changed" (forward / limiter
// commands, rate edits) into one recomputation per node after a short delay.
type entryRateDirty struct {
	mu    sync.Mutex
	nodes map[int64]bool
	timer *time.Timer
}

func (h *Handler) markEntryRatesDirty(nodeIDs ...int64) {
	if h == nil {
		return
	}
	d := &h.entryRateDirty
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, id := range nodeIDs {
		if id <= 0 || !h.entryRateTc(id) {
			continue
		}
		if d.nodes == nil {
			d.nodes = map[int64]bool{}
		}
		d.nodes[id] = true
	}
	if len(d.nodes) > 0 && d.timer == nil {
		d.timer = time.AfterFunc(entryRateDebounce, h.flushEntryRates)
	}
}

func (h *Handler) flushEntryRates() {
	d := &h.entryRateDirty
	d.mu.Lock()
	nodes := d.nodes
	d.nodes, d.timer = nil, nil
	d.mu.Unlock()
	for id := range nodes {
		if err := h.syncEntryRates(id); err != nil {
			fmt.Printf("entry rate: node %d: %v\n", id, err)
		}
	}
}

// noteEntryRateCommand marks the node when a command changed what runs there.
func (h *Handler) noteEntryRateCommand(nodeID int64, commandType string) {
	switch commandType {
	case "AddService", "UpdateService", "DeleteService", "PauseService", "ResumeService",
		"AddNftablesRules", "UpdateNftablesRules", "DeleteNftablesRules",
		"AddLimiters", "UpdateLimiters", "DeleteLimiters":
		h.markEntryRatesDirty(nodeID)
	}
}

// markAllEntryRatesDirty: a 限速规则 changed (speed / arena), every tc node may
// use it.
func (h *Handler) markAllEntryRatesDirty() {
	ids, err := h.repo.ListNodeIDsByEntryRateMode(ws.EntryRateModeTc)
	if err != nil {
		return
	}
	h.markEntryRatesDirty(ids...)
}

// runEntryRateLoop is the safety pass: once a minute every tc node's tree is
// recomputed (a few queries per node) and pushed only if it changed — catches
// changes no command marked (e.g. a rule deleted right after its commands).
func (h *Handler) runEntryRateLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	ticker := time.NewTicker(entryRateLoopTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		ids, err := h.repo.ListNodeIDsByEntryRateMode(ws.EntryRateModeTc)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if !h.entryRateTc(id) {
				continue
			}
			if err := h.syncEntryRates(id); err != nil {
				fmt.Printf("entry rate: node %d: %v\n", id, err)
			}
		}
	}
}

// decorateForwardRateFallback marks (admin list) rules with an own speed whose
// entry node still limits it the old way (gost / nftables) or reports a tc
// error: rateFallback = the nodes and why (yellow hint in the rule dialog).
func (h *Handler) decorateForwardRateFallback(items []map[string]interface{}, entryNodes map[int64][]int64) {
	var ids []int64
	seen := map[int64]bool{}
	for i := range items {
		if !forwardItemHasOwnSpeed(items[i]) {
			continue
		}
		for _, id := range entryNodes[asInt64(items[i]["id"], 0)] {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	modes, err := h.repo.NodeEntryRateModes(ids)
	if err != nil {
		return
	}
	for i := range items {
		if !forwardItemHasOwnSpeed(items[i]) {
			continue
		}
		var notes []string
		for _, id := range entryNodes[asInt64(items[i]["id"], 0)] {
			n, ok := modes[id]
			if !ok || n.IsRemote == 1 {
				continue
			}
			if note := entryRateNodeNote(n.Name, n.EntryRateTc, n.EntryRateError); note != "" {
				notes = append(notes, note)
			}
		}
		if len(notes) > 0 {
			items[i]["rateFallback"] = strings.Join(notes, "；")
		}
	}
}

func forwardItemHasOwnSpeed(item map[string]interface{}) bool {
	if enabled, _ := item["speedLimitEnabled"].(bool); enabled && asInt64(item["speedLimit"], 0) > 0 {
		return true
	}
	return asInt64(item["speedId"], 0) > 0
}

// entryRateNodeNote: why a node does not shape with tc ("" = it does).
func entryRateNodeNote(name string, mode int, errText string) string {
	switch mode {
	case ws.EntryRateModeTc:
		if errText != "" {
			return fmt.Sprintf("入口 %s 的 tc 限速未完全生效：%s", name, errText)
		}
		return ""
	case ws.EntryRateModeKernel:
		return fmt.Sprintf("入口 %s 没有用 tc 限速（%s），仍按旧方式限速", name, errText)
	}
	return fmt.Sprintf("入口 %s 的 agent 较旧（待升级），仍按旧方式限速", name)
}

// agentRunsServices: the node's agent already runs exactly these service
// payloads (runtime revisions; one ListServices). Only asked for user edits
// on tc nodes (syncForwardServices without a node filter).
func (h *Handler) agentRunsServices(nodeID int64, services []map[string]interface{}) bool {
	inv, _ := h.reconnectInventory(nodeID)
	return inv != nil && inv.matches(ws.ExpectedRuntimeRevs("UpdateService", services))
}
