package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

type runtimeObject struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	OwnerID int64  `json:"ownerId,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
	Error   string `json:"error,omitempty"`
}

type runtimeInventory struct {
	Services   []string `json:"services"`
	Chains     []string `json:"chains"`
	Hops       []string `json:"hops"`
	Limiters   []string `json:"limiters"`
	Nftables   []string `json:"nftables"`
	WGPaths    []string `json:"wgPaths"`
	WGForwards []string `json:"wgForwards"`
	// Plan 138 agents: revisions of the panel-tagged objects applied by the
	// agent's current process ("service:<name>" → rev) and that process' id.
	// nil Revs = an older agent.
	Revs map[string]string `json:"revs,omitempty"`
	Boot string            `json:"boot,omitempty"`
}

func (i runtimeInventory) objects() []runtimeObject {
	items := []runtimeObject{}
	for kind, names := range map[string][]string{"service": i.Services, "chain": i.Chains, "hop": i.Hops, "limiter": i.Limiters, "nft": i.Nftables, "wgPath": i.WGPaths, "wgForward": i.WGForwards} {
		for _, name := range names {
			items = append(items, runtimeObject{Kind: kind, Name: name})
		}
	}
	sort.Slice(items, func(a, b int) bool { return objectKey(items[a]) < objectKey(items[b]) })
	return items
}

func objectKey(o runtimeObject) string { return o.Kind + ":" + o.Name }

type orphanNodeReport struct {
	NodeID       int64           `json:"nodeId"`
	Name         string          `json:"name"`
	Source       string          `json:"source,omitempty"`
	ObservedAt   int64           `json:"observedAt,omitempty"`
	Coverage     string          `json:"coverage,omitempty"`
	Skipped      string          `json:"skipped,omitempty"`
	Owned        []runtimeObject `json:"owned"`
	Orphans      []runtimeObject `json:"orphans"`
	Unknown      []runtimeObject `json:"unknown"`
	Missing      []runtimeObject `json:"missing"`
	MissingCount int             `json:"missingCount"`
}

var forwardRuntimePattern = regexp.MustCompile(`^([1-9][0-9]*)_([1-9][0-9]*)_(0|[1-9][0-9]*)(_(tcp|udp))?$`)

func runtimeID(name, prefix, suffix string) int64 {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return 0
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0
	}
	return id
}

func forwardRuntimeID(o runtimeObject) int64 {
	switch o.Kind {
	case "service":
		parts := forwardRuntimePattern.FindStringSubmatch(o.Name)
		if len(parts) == 0 {
			return 0
		}
		id, userID, grantID, ok := parseFlowServiceIDs(o.Name)
		if !ok || buildForwardServiceBase(id, userID, grantID)+parts[4] != o.Name {
			return 0
		}
		return id
	case "nft":
		return runtimeID(o.Name, "", "_nft")
	case "wgForward":
		return runtimeID(o.Name, "forward-", "")
	case "limiter":
		return runtimeID(o.Name, "forward_", "_speed")
	}
	return 0
}

func pathNodes(s *repo.RuntimeOwnership, pathID int64, endpointsOnly bool) map[int64]bool {
	segments := []model.PathSegment{}
	for _, seg := range s.Segments {
		if seg.PathID == pathID {
			segments = append(segments, seg)
		}
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].Sequence < segments[j].Sequence })
	nodes := map[int64]bool{}
	if endpointsOnly && len(segments) > 0 {
		nodes[segments[0].FromNodeID] = true
		nodes[segments[len(segments)-1].ToNodeID] = true
	} else {
		for _, seg := range segments {
			nodes[seg.FromNodeID], nodes[seg.ToNodeID] = true, true
		}
	}
	return nodes
}

func forwardOnNode(s *repo.RuntimeOwnership, f model.Forward, nodeID int64) bool {
	if f.Mode == "wg_path" {
		return pathNodes(s, f.WGPathID, true)[nodeID]
	}
	for _, p := range s.Ports {
		if p.ForwardID == f.ID && p.NodeID == nodeID {
			return true
		}
	}
	return false
}

// Return unknown unless the entire name matches a name emitted by this panel.
// Existing owners on their assigned nodes are retained even when paused or
// carrying a historical user_tunnel suffix; an old name is not proof of orphanhood.
func classifyRuntime(s *repo.RuntimeOwnership, nodeID int64, o runtimeObject) (runtimeObject, string) {
	if id := forwardRuntimeID(o); id > 0 {
		o.OwnerID = id
		for _, f := range s.Forwards {
			if f.ID != id {
				continue
			}
			if forwardOnNode(s, f, nodeID) {
				return o, "owned"
			}
			o.Reason = "forward is not assigned to this node"
			return o, "orphan"
		}
		o.Reason = "forward does not exist"
		return o, "orphan"
	}
	id := int64(0)
	if o.Kind == "service" {
		id = runtimeID(o.Name, "", "_tls")
	}
	if o.Kind == "chain" {
		id = runtimeID(o.Name, "chains_", "")
	}
	if id > 0 {
		o.OwnerID = id
		for _, t := range s.Tunnels {
			if t.ID != id {
				continue
			}
			for _, c := range s.Chains {
				if c.TunnelID == id && c.NodeID == nodeID && t.Type == 2 &&
					((o.Kind == "service" && (c.ChainType == "2" || c.ChainType == "3")) ||
						(o.Kind == "chain" && (c.ChainType == "1" || c.ChainType == "2"))) {
					return o, "owned"
				}
			}
			o.Reason = "tunnel runtime role is not assigned to this node"
			return o, "orphan"
		}
		o.Reason = "tunnel does not exist"
		return o, "orphan"
	}
	if o.Kind == "wgPath" {
		if id := runtimeID(o.Name, "wg-flvx-", ""); id > 0 {
			o.OwnerID = id
			for _, p := range s.Paths {
				if p.ID != id {
					continue
				}
				if pathNodes(s, id, false)[nodeID] {
					return o, "owned"
				}
				o.Reason = "WG path is not assigned to this node"
				return o, "orphan"
			}
			o.Reason = "WG path does not exist"
			return o, "orphan"
		}
	}
	// Tunnel hops are inline in chains, not independently registered. Standalone
	// hop names and shared speed limiters have no exclusive forward ownership.
	o.Reason = "name has no unambiguous FLVX forward/tunnel owner"
	return o, "unknown"
}

func sweepNodeGuard(s *repo.RuntimeOwnership, nodeID int64) string {
	for _, n := range s.Nodes {
		if n.ID != nodeID {
			continue
		}
		if n.IsRemote != 0 || n.RemoteURL.String != "" || n.RemoteToken.String != "" || s.SharedNodes[nodeID] {
			return "federation/shared node (including historical shares)"
		}
		if n.Status != 1 {
			return "offline"
		}
		return ""
	}
	return "node no longer exists"
}

func (h *Handler) readNodeInventory(nodeID int64) (runtimeInventory, string, int64, error) {
	result, err := h.sendNodeCommandWithTimeout(nodeID, "ListServices", map[string]interface{}{}, 5*time.Second, false, false)
	if err == nil {
		var i runtimeInventory
		raw, marshalErr := json.Marshal(result.Data)
		if marshalErr != nil {
			return i, "", 0, marshalErr
		}
		if err := json.Unmarshal(raw, &i); err != nil {
			return i, "", 0, err
		}
		if i.Services == nil {
			return i, "", 0, fmt.Errorf("ListServices response has no services array")
		}
		return i, "ListServices", time.Now().Unix(), nil
	}
	msg := strings.ToLower(err.Error())
	if result.Type != "UnknownCommandResponse" && !strings.Contains(msg, "未知命令") && !strings.Contains(msg, "unknown command") {
		return runtimeInventory{}, "", 0, err
	}
	names, at, online := h.wsServer.ServiceInventorySnapshot(nodeID)
	if !online || names == nil || time.Now().Unix()-at > 30 {
		return runtimeInventory{}, "serviceConnections", at, fmt.Errorf("no fresh serviceConnections report from current connection")
	}
	i := runtimeInventory{Services: []string{}, Nftables: []string{}}
	for _, name := range names {
		if runtimeID(name, "", "_nft") > 0 {
			i.Nftables = append(i.Nftables, name)
		} else {
			i.Services = append(i.Services, name)
		}
	}
	return i, "serviceConnections", at, nil
}

func missingRuntime(s *repo.RuntimeOwnership, nodeID int64, i runtimeInventory, precise bool) []runtimeObject {
	present := map[string]bool{}
	for _, o := range i.objects() {
		present[objectKey(o)] = true
	}
	missing := []runtimeObject{}
	add := func(kind, name string, owner int64) {
		o := runtimeObject{Kind: kind, Name: name, OwnerID: owner}
		if !present[objectKey(o)] {
			missing = append(missing, o)
		}
	}
	for _, f := range s.Forwards {
		if f.Status != 1 || !forwardOnNode(s, f, nodeID) {
			continue
		}
		switch f.Mode {
		case "wg_path":
			if precise {
				add("wgForward", fmt.Sprintf("forward-%d", f.ID), f.ID)
			}
		case "nftables":
			add("nft", fmt.Sprintf("%d_nft", f.ID), f.ID)
		default:
			grants := []int64{}
			for _, g := range s.Grants {
				if g.UserID == f.UserID && g.TunnelID == f.TunnelID {
					grants = append(grants, g.ID)
				}
			}
			preferred := int64(0)
			if len(grants) > 0 {
				preferred = grants[0]
			}
			bases := buildForwardServiceBaseCandidates(f.ID, f.UserID, preferred, grants)
			for _, suffix := range []string{"_tcp", "_udp"} {
				found := false
				for _, base := range bases {
					if present["service:"+base] || present["service:"+base+suffix] {
						found = true
					}
				}
				if !found {
					missing = append(missing, runtimeObject{Kind: "service", Name: bases[0] + suffix, OwnerID: f.ID})
				}
			}
		}
	}
	for _, t := range s.Tunnels {
		if t.Status != 1 || t.Type != 2 {
			continue
		}
		for _, c := range s.Chains {
			if c.TunnelID != t.ID || c.NodeID != nodeID {
				continue
			}
			if c.ChainType == "2" || c.ChainType == "3" {
				add("service", fmt.Sprintf("%d_tls", t.ID), t.ID)
			}
			if precise && (c.ChainType == "1" || c.ChainType == "2") {
				add("chain", fmt.Sprintf("chains_%d", t.ID), t.ID)
			}
		}
	}
	if precise {
		for _, p := range s.Paths {
			if p.Status == "active" && pathNodes(s, p.ID, false)[nodeID] {
				add("wgPath", fmt.Sprintf("wg-flvx-%d", p.ID), p.ID)
			}
		}
	}
	sort.Slice(missing, func(a, b int) bool { return objectKey(missing[a]) < objectKey(missing[b]) })
	return missing
}

func (h *Handler) deleteOrphanRuntime(nodeID int64, o runtimeObject) error {
	var command string
	var payload map[string]interface{}
	switch o.Kind {
	case "service":
		command, payload = "DeleteService", map[string]interface{}{"services": []string{o.Name}}
	case "chain":
		command, payload = "DeleteChains", map[string]interface{}{"chain": o.Name}
	case "limiter":
		command, payload = "DeleteLimiters", map[string]interface{}{"limiter": o.Name}
	case "nft":
		command, payload = "DeleteNftablesRules", map[string]interface{}{"forward_ids": []int64{o.OwnerID}, "terminate": true}
	case "wgForward":
		command, payload = "RemoveWGForwardRule", map[string]interface{}{"forward_id": o.OwnerID}
	case "wgPath":
		command, payload = "RemoveWireGuardPath", map[string]interface{}{"path_id": o.OwnerID, "interface": o.Name}
	default:
		return fmt.Errorf("unsupported runtime kind")
	}
	_, err := h.sendNodeCommand(nodeID, command, payload, false, true)
	return err
}

// POST /api/v1/node/orphan-sweep defaults to read-only. An optional targets
// allowlist lets an operator execute only the names reviewed in a dry run.
func (h *Handler) nodeOrphanSweep(w http.ResponseWriter, r *http.Request) {
	_, roleID, authErr := userRoleFromRequest(r)
	if authErr != nil || roleID != 0 {
		code := http.StatusForbidden
		if authErr != nil {
			code = http.StatusUnauthorized
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(code)
		response.WriteJSON(w, response.Err(code, "权限不足，仅管理员可操作"))
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Execute bool    `json:"execute"`
		NodeIDs []int64 `json:"nodeIds"`
		Targets []struct {
			NodeID int64  `json:"nodeId"`
			Kind   string `json:"kind"`
			Name   string `json:"name"`
		} `json:"targets"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	s, err := h.repo.ReadRuntimeOwnership()
	if err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	reports := []orphanNodeReport{}
	for _, node := range s.Nodes {
		if len(req.NodeIDs) > 0 && !slices.Contains(req.NodeIDs, node.ID) {
			continue
		}
		out := orphanNodeReport{NodeID: node.ID, Name: node.Name, Owned: []runtimeObject{}, Orphans: []runtimeObject{}, Unknown: []runtimeObject{}, Missing: []runtimeObject{}}
		out.Skipped = sweepNodeGuard(s, node.ID)
		if out.Skipped != "" {
			reports = append(reports, out)
			continue
		}
		i, source, at, err := h.readNodeInventory(node.ID)
		out.Source, out.ObservedAt = source, at
		if err != nil {
			out.Skipped = err.Error()
			reports = append(reports, out)
			continue
		}
		out.Coverage = "registry services/chains/hops/limiters, nft rule IDs, FLVX WG interfaces/configs"
		if source != "ListServices" {
			out.Coverage = "partial: CurrentConns services and active nft IDs only; idle nft rules and chains/hops/limiters/WG may be absent; missing is provisional"
		}
		for _, o := range i.objects() {
			if strings.HasPrefix(o.Name, "fed_") {
				out.Skipped = "federation runtime names indicate another panel's ownership"
			}
			o, status := classifyRuntime(s, node.ID, o)
			switch status {
			case "owned":
				out.Owned = append(out.Owned, o)
			case "orphan":
				out.Orphans = append(out.Orphans, o)
			default:
				out.Unknown = append(out.Unknown, o)
			}
		}
		out.Missing = missingRuntime(s, node.ID, i, source == "ListServices")
		out.MissingCount = len(out.Missing)
		if req.Execute && out.Skipped == "" {
			for idx := range out.Orphans {
				o := &out.Orphans[idx]
				if req.Targets != nil {
					selected := false
					for _, target := range req.Targets {
						if target.NodeID == node.ID && target.Kind == o.Kind && target.Name == o.Name {
							selected = true
						}
					}
					if !selected {
						continue
					}
				}
				// Recheck DB ownership/sharing immediately before every destructive
				// command. No DB rows are changed by this endpoint.
				latest, err := h.repo.ReadRuntimeOwnership()
				if err != nil {
					o.Error = err.Error()
					continue
				}
				if reason := sweepNodeGuard(latest, node.ID); reason != "" {
					o.Error = reason
					continue
				}
				if _, status := classifyRuntime(latest, node.ID, *o); status != "orphan" {
					o.Error = "ownership changed; retained"
					continue
				}
				if err := h.deleteOrphanRuntime(node.ID, *o); err != nil {
					o.Error = err.Error()
				} else {
					o.Deleted = true
				}
			}
		}
		reports = append(reports, out)
	}
	// Chain rules (plan 108): DB-side owners of loopback chain nodes. The
	// agent removes any landing inbound missing from desired state, so a chain
	// node without a rule exists only while its row does; clean the row and
	// push the exit.
	chainOrphans, chainErr := h.repo.FindChainOrphans()
	if chainErr != nil {
		chainOrphans = []repo.ChainOrphan{}
	}
	if len(req.NodeIDs) > 0 {
		filtered := chainOrphans[:0]
		for _, o := range chainOrphans {
			if o.Kind == "chain" || slices.Contains(req.NodeIDs, o.NodeID) {
				filtered = append(filtered, o)
			}
		}
		chainOrphans = filtered
	}
	if req.Execute {
		var servers []int64
		for idx := range chainOrphans {
			o := &chainOrphans[idx]
			if req.Targets != nil {
				selected := false
				for _, target := range req.Targets {
					if target.Kind == o.Kind && target.Name == strconv.FormatInt(o.ID, 10) {
						selected = true
					}
				}
				if !selected {
					continue
				}
			}
			ids, err := h.repo.DeleteChainOrphan(*o)
			if err != nil {
				o.Error = err.Error()
				continue
			}
			o.Deleted = true
			servers = append(servers, ids...)
		}
		h.syncChainServers(servers)
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"execute": req.Execute, "nodes": reports, "chainOrphans": chainOrphans}))
}
