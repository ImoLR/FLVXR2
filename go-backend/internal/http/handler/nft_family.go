package handler

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

// Plan 150. nftables mode forwards in the kernel: the entry node DNATs every
// packet to the target. NAT cannot translate between IPv4 and IPv6 — in the
// agent's inet table the kernel simply skips a DNAT whose family differs from
// the packet's — so a client that reaches the entry over IPv6 can only be
// forwarded to an IPv6 target, and IPv4 only to IPv4. gost (userspace) has no
// such limit. Node 48 「沪台」 is reachable only over IPv6, so an nftables rule
// to an IPv4-only target there was silently dead while gost worked.
//
// The panel therefore (1) picks, for a domain target, the address family the
// entry is reached with, and (2) saves a rule as gost, with a note, when no
// family matches at all.

type addrFamilies uint8

const (
	familyIPv4 addrFamilies = 1 << iota
	familyIPv6
)

func ipFamily(ip net.IP) addrFamilies {
	switch {
	case ip == nil:
		return 0
	case ip.To4() != nil:
		return familyIPv4
	default:
		return familyIPv6
	}
}

func (f addrFamilies) label() string {
	switch f {
	case familyIPv4:
		return "IPv4"
	case familyIPv6:
		return "IPv6"
	case familyIPv4 | familyIPv6:
		return "IPv4/IPv6"
	}
	return "未知"
}

// nftLookupIPAddr resolves names for the family checks (replaced in tests).
var nftLookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

const nftLookupTimeout = 3 * time.Second

// lookupHostIPs returns a literal IP or every A/AAAA record of a name, in
// resolver order. ok is false when the name cannot be resolved.
func lookupHostIPs(host string) (ips []net.IP, literal, ok bool) {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return nil, false, false
	}
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, true, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), nftLookupTimeout)
	defer cancel()
	addrs, err := nftLookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return nil, false, false
	}
	ips = make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, false, true
}

// nftTarget is one rule target ("host:port"), resolved once for all entries.
type nftTarget struct {
	raw     string
	port    string
	ips     []net.IP // resolver order; empty when unresolved
	literal bool
}

func resolveNftTargets(remoteAddr string) []nftTarget {
	parts := splitRemoteTargets(remoteAddr)
	out := make([]nftTarget, 0, len(parts))
	for _, raw := range parts {
		t := nftTarget{raw: raw}
		if host, port, err := net.SplitHostPort(raw); err == nil {
			t.port = strings.TrimSpace(port)
			t.ips, t.literal, _ = lookupHostIPs(host)
		}
		out = append(out, t)
	}
	return out
}

func (t nftTarget) isDomain() bool { return !t.literal && t.port != "" }

// pick is the DNAT target for an entry reached with the families in prefer:
// the first address of such a family, else the first address (the pre-150
// behaviour, also used when the entry's families are unknown). Literals and
// unresolved names are returned unchanged, as before.
func (t nftTarget) pick(prefer addrFamilies) string {
	if t.literal || len(t.ips) == 0 {
		return t.raw
	}
	chosen := t.ips[0]
	if prefer != 0 {
		for _, ip := range t.ips {
			if ipFamily(ip)&prefer != 0 {
				chosen = ip
				break
			}
		}
	}
	return net.JoinHostPort(chosen.String(), t.port)
}

// nftEntry is what clients use to reach a rule on one entry node.
type nftEntry struct {
	name     string
	families addrFamilies
	complete bool // every entry address resolved
}

// nftEntryFamilies lists, per entry node, the address families of the entry
// addresses users are given (tunnel entry list, rule listen IP, node address).
func (h *Handler) nftEntryFamilies(tunnelID int64, ports []forwardPortRecord) map[int64]nftEntry {
	if h == nil || h.repo == nil || len(ports) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(ports))
	inIPs := make(map[int64]string)
	for _, fp := range ports {
		ids = append(ids, fp.NodeID)
		if ip := strings.TrimSpace(fp.InIP); ip != "" {
			inIPs[fp.NodeID] = ip
		}
	}
	hosts, names, err := h.repo.EntryHostsForNodes(tunnelID, ids, inIPs)
	if err != nil {
		return nil
	}
	cache := map[string][]net.IP{}
	out := make(map[int64]nftEntry, len(hosts))
	for id, list := range hosts {
		e := nftEntry{name: names[id], complete: len(list) > 0}
		for _, host := range list {
			ips, seen := cache[host]
			if !seen {
				ips, _, _ = lookupHostIPs(host)
				cache[host] = ips
			}
			if len(ips) == 0 {
				e.complete = false
				continue
			}
			for _, ip := range ips {
				e.families |= ipFamily(ip)
			}
		}
		out[id] = e
	}
	return out
}

func entryFamilyMap(entries map[int64]nftEntry) map[int64]addrFamilies {
	out := make(map[int64]addrFamilies, len(entries))
	for id, e := range entries {
		out[id] = e.families
	}
	return out
}

// nftRulePayloads builds the AddNftablesRules payloads with family-aware
// targets; every push and the reconnect comparison go through it.
func (h *Handler) nftRulePayloads(forward *forwardRecord, tunnel *tunnelRecord, ports []forwardPortRecord, chainNodes []chainNodeRecord, userTunnelID int64) ([]NftablesRulePayload, map[int64]nftEntry) {
	entries := h.nftEntryFamilies(forward.TunnelID, ports)
	rules := buildNftablesRulePayloadsFor(forward, tunnel, ports, chainNodes, userTunnelID, resolveNftTargets(forward.RemoteAddr), entryFamilyMap(entries))
	// Plan 159: each rule policed at min(own, the user's 速率 there).
	h.capNftRulesByUserRate(forward.UserID, rules)
	return rules, entries
}

// nftFamilyIssue is one entry node where the installed DNAT target cannot
// carry (all or part of) the entry's traffic.
type nftFamilyIssue struct {
	node    string
	entry   addrFamilies
	target  addrFamilies
	allDead bool
}

// nftFamilyIssues compares each entry node's families with the family of the
// DNAT target that would be installed there (the first rule per node: the
// agent keeps one DNAT per rule and protocol). Unknown families are skipped.
func nftFamilyIssues(rules []NftablesRulePayload, entries map[int64]nftEntry) []nftFamilyIssue {
	var out []nftFamilyIssue
	seen := map[int64]bool{}
	for _, r := range rules {
		if r.Protocol != "tcp" || seen[r.NodeID] {
			continue
		}
		seen[r.NodeID] = true
		e, ok := entries[r.NodeID]
		if !ok || !e.complete || e.families == 0 {
			continue
		}
		host, _, err := net.SplitHostPort(r.Target)
		target := ipFamily(net.ParseIP(host))
		if err != nil || target == 0 {
			continue
		}
		switch {
		case e.families&target == 0:
			out = append(out, nftFamilyIssue{node: e.name, entry: e.families, target: target, allDead: true})
		case e.families&^target != 0:
			out = append(out, nftFamilyIssue{node: e.name, entry: e.families &^ target, target: target})
		}
	}
	return out
}

const nftNoFamilyChange = "nftables（内核转发）不能在 IPv4 和 IPv6 之间转换"

func (i nftFamilyIssue) reason() string {
	return fmt.Sprintf("入口「%s」只有 %s 地址，目标只有 %s 地址", i.node, i.entry.label(), i.target.label())
}

func (i nftFamilyIssue) message() string {
	if i.allDead {
		return i.reason() + "；" + nftNoFamilyChange + "，已自动改用 gost 转发"
	}
	return fmt.Sprintf("入口「%s」的 %s 地址不能用 nftables 转发到 %s 目标，请用它的 %s 地址连接，或改用 gost 转发", i.node, i.entry.label(), i.target.label(), i.target.label())
}

// nftFamilyVerdict is the save-time check of an nftables rule: fallback means
// no entry family can reach the target on some entry node, so the rule is
// saved as gost; notes are shown to the user either way.
func (h *Handler) nftFamilyVerdict(tunnelID int64, ports []forwardPortRecord, remoteAddr string) (fallback bool, notes []string) {
	if h == nil || tunnelID <= 0 || len(ports) == 0 {
		return false, nil
	}
	tunnel, err := h.getTunnelRecord(tunnelID)
	if err != nil || tunnel == nil {
		return false, nil
	}
	chainNodes, _ := h.listChainNodesForTunnel(tunnelID)
	rules, entries := h.nftRulePayloads(&forwardRecord{TunnelID: tunnelID, RemoteAddr: remoteAddr, Mode: "nftables"}, tunnel, ports, chainNodes, 0)
	issues := nftFamilyIssues(rules, entries)
	for _, i := range issues {
		if i.allDead {
			fallback = true
		}
	}
	for _, i := range issues {
		if i.allDead == fallback {
			notes = append(notes, i.message())
		}
	}
	return fallback, notes
}

// nftFamilyBlockReason is the batch-move check: moving an nftables rule onto
// entries that can never carry its target is refused (the rule keeps working
// where it is); editing the single rule saves it as gost instead.
func (h *Handler) nftFamilyBlockReason(tunnelID int64, nodeIDs []int64, remoteAddr string) string {
	if h == nil || tunnelID <= 0 || len(nodeIDs) == 0 {
		return ""
	}
	tunnel, err := h.getTunnelRecord(tunnelID)
	if err != nil || tunnel == nil {
		return ""
	}
	probe := make([]forwardPortRecord, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		probe = append(probe, forwardPortRecord{NodeID: id})
	}
	chainNodes, _ := h.listChainNodesForTunnel(tunnelID)
	rules, entries := h.nftRulePayloads(&forwardRecord{TunnelID: tunnelID, RemoteAddr: remoteAddr, Mode: "nftables"}, tunnel, probe, chainNodes, 0)
	for _, i := range nftFamilyIssues(rules, entries) {
		if i.allDead {
			return i.reason() + "；" + nftNoFamilyChange + "，请单独编辑这条规则（保存时会自动改用 gost）"
		}
	}
	return ""
}

// logNftFamilyIssues reports a deployed rule that is (partly) dead because of
// the family mismatch — e.g. a rule saved before plan 150. One line per push.
func logNftFamilyIssues(forwardID int64, rules []NftablesRulePayload, entries map[int64]nftEntry) {
	for _, i := range nftFamilyIssues(rules, entries) {
		log.Printf("[nftables] forward %d: entry %s reached over %s, DNAT target is %s (kernel NAT cannot cross IPv4/IPv6; %s)", forwardID, i.node, i.entry.label(), i.target.label(), map[bool]string{true: "rule carries no traffic, switch it to gost", false: "that entry family carries no traffic"}[i.allDead])
	}
}
