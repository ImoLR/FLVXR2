package handler

import (
	"fmt"
	"sort"
	"strings"
)

// Plan 236: paused rules must stay paused across agent restarts.
//
// Up to fork.124 an agent restart (OTA, crash, reboot) brought every paused
// rule back up: the agent's loader served every service of gost.json and
// ignored `metadata.paused` (fixed in the plan-236 agent), and the panel's
// reconnect redeploy only looks at running rules (status 1), so nothing paused
// them again. Older agents keep doing that, so on every (re)connect the panel
// re-pauses whatever of the node's non-running rules (status <> 1: user/admin
// pause, user expiry, flow quota, rule expiry, CN block, landing pause) the
// agent still runs: one ListServices, then at most one batched PauseService
// with all of them. Plan-236 agents report their paused services (they never
// listen), so for them nothing is sent. Nodes without non-running rules cost
// one database query and no command.

type repauseResult struct {
	inactive int      // non-running rules with a port on the node
	services []string // gost services paused again (one batched command)
	nft, wg  int      // nftables / WG rules removed again
	commands int64
	agentNew bool // agent reports its paused services (plan 236)
	err      string
}

func (r repauseResult) acted() bool {
	return len(r.services) > 0 || r.nft > 0 || r.wg > 0 || r.err != ""
}

// repauseInactiveForwardsOnReconnect runs the safety net and logs one line
// when it had to do something (or failed); quiet otherwise.
func (h *Handler) repauseInactiveForwardsOnReconnect(nodeID int64) {
	r := h.repauseInactiveForwards(nodeID)
	if !r.acted() {
		return
	}
	line := fmt.Sprintf("repause: node %d: %d paused rules, re-paused services=%d nft=%d wg=%d, commands=%d",
		nodeID, r.inactive, len(r.services), r.nft, r.wg, r.commands)
	if r.err != "" {
		line += "; " + r.err
	}
	fmt.Println(line)
}

func (h *Handler) repauseInactiveForwards(nodeID int64) (res repauseResult) {
	if h == nil || h.repo == nil || nodeID <= 0 {
		return
	}
	ids, err := h.repo.ListInactiveForwardIDsByNode(nodeID)
	if err != nil {
		res.err = "list paused rules failed: " + err.Error()
		return
	}
	if len(ids) == 0 {
		return
	}
	res.inactive = len(ids)
	node, err := h.getNodeRecord(nodeID)
	if err != nil || node == nil || node.IsRemote == 1 {
		return
	}
	var before int64
	if h.wsServer != nil {
		before = h.wsServer.CommandsSent(nodeID)
		defer func() { res.commands = h.wsServer.CommandsSent(nodeID) - before }()
	}
	inv, source, _, err := h.readNodeInventory(nodeID)
	if err != nil {
		res.err = "inventory failed: " + err.Error()
		h.repauseWithoutInventory(nodeID, ids, &res)
		return
	}
	res.agentNew = source == "ListServices" && inv.Paused != nil
	inactive := map[int64]bool{}
	for _, id := range ids {
		inactive[id] = true
	}
	pausedOnAgent := map[string]bool{}
	for _, name := range inv.Paused {
		pausedOnAgent[name] = true
	}
	// Running objects of non-running rules, grouped by kind.
	var services []string
	nftIDs := map[int64]bool{}
	wgIDs := map[int64]bool{}
	for _, o := range inv.objects() {
		id := forwardRuntimeID(o)
		if id <= 0 || !inactive[id] {
			continue
		}
		switch o.Kind {
		case "service":
			if !pausedOnAgent[o.Name] {
				services = append(services, o.Name)
			}
		case "nft":
			nftIDs[id] = true
		case "wgForward":
			wgIDs[id] = true
		}
	}
	if len(services) == 0 && len(nftIDs) == 0 && len(wgIDs) == 0 {
		return
	}
	// Re-read right before acting: a rule resumed meanwhile is left alone.
	still, err := h.repo.ListInactiveForwardIDsByNode(nodeID)
	if err != nil {
		res.err = "re-read paused rules failed: " + err.Error()
		return
	}
	stillInactive := map[int64]bool{}
	for _, id := range still {
		stillInactive[id] = true
	}
	var batch []string
	for _, name := range services {
		if stillInactive[forwardRuntimeID(runtimeObject{Kind: "service", Name: name})] {
			batch = append(batch, name)
		}
	}
	sort.Strings(batch)
	var errs []string
	if len(batch) > 0 {
		// One command for all services; pauseServices on the agent checks every
		// name first, and they all come from its own inventory.
		if _, err := h.sendNodeCommand(nodeID, "PauseService", map[string]interface{}{"services": batch}, false, false); err != nil {
			errs = append(errs, "PauseService failed: "+err.Error())
		} else {
			res.services = batch
		}
	}
	for id := range nftIDs {
		if !stillInactive[id] {
			continue
		}
		forward, ports, ok := h.repauseForwardOnNode(id, nodeID)
		if !ok || !strings.EqualFold(forward.Mode, "nftables") {
			continue
		}
		if err := h.deleteNftablesRules(forward, ports); err != nil {
			errs = append(errs, fmt.Sprintf("nft rule %d: %v", id, err))
			continue
		}
		res.nft++
	}
	for id := range wgIDs {
		if !stillInactive[id] {
			continue
		}
		forward, ports, ok := h.repauseForwardOnNode(id, nodeID)
		if !ok || !strings.EqualFold(forward.Mode, "wg_path") {
			continue
		}
		if err := h.syncWGForwardRuleOnNode(forward, ports, "PauseService", nodeID); err != nil {
			errs = append(errs, fmt.Sprintf("wg rule %d: %v", id, err))
			continue
		}
		res.wg++
	}
	if len(errs) > 0 {
		res.err = strings.Join(errs, "; ")
	}
	return
}

// repauseForwardOnNode returns the (still non-running) rule and its ports on
// the node.
func (h *Handler) repauseForwardOnNode(forwardID, nodeID int64) (*forwardRecord, []forwardPortRecord, bool) {
	forward, err := h.getForwardRecord(forwardID)
	if err != nil || forward == nil || forward.Status == 1 {
		return nil, nil, false
	}
	all, err := h.listForwardPorts(forwardID)
	if err != nil {
		return nil, nil, false
	}
	ports := make([]forwardPortRecord, 0, len(all))
	for _, p := range all {
		if p.NodeID == nodeID {
			ports = append(ports, p)
		}
	}
	return forward, ports, len(ports) > 0
}

// repauseWithoutInventory: no inventory (agent too old or busy) → pause the
// gost services rule by rule as the old pause path does (names unknown, so
// not batched); nothing else is touched.
func (h *Handler) repauseWithoutInventory(nodeID int64, ids []int64, res *repauseResult) {
	for _, id := range ids {
		forward, err := h.getForwardRecord(id)
		if err != nil || forward == nil || forward.Status == 1 {
			continue
		}
		if strings.EqualFold(forward.Mode, "nftables") || strings.EqualFold(forward.Mode, "wg_path") {
			continue
		}
		bases, err := h.forwardServiceBaseCandidates(forward)
		if err != nil {
			continue
		}
		if handled, _, err := h.controlForwardServicesOnNode(nodeID, bases, "PauseService"); err == nil && handled {
			res.services = append(res.services, bases[0])
		}
	}
}
