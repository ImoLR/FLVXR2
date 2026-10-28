package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 158: per user per server 入站连接数 and user totals that include
// landing inbound. The coordinator (quota_groups.go) splits the budgets; this
// file holds the glue around it.

// forwardQuotaGroup says whether the forward's services on entry node nodeID
// join the user's quota group, and the group's initial budgets (-1 =
// unlimited) used until the coordinator's first push: the user's 总连接数 /
// 总 IP 数 and, since plan 158, the user's 入站连接数 on that entry server.
func forwardQuotaGroup(forward *forwardRecord, nodeID int64) (bool, int, int) {
	if forward == nil || forward.UserID <= 0 {
		return false, -1, -1
	}
	nodeCap := 0
	if nodeID > 0 {
		nodeCap = forward.UserServerConnCaps[nodeID]
	}
	if forward.UserMaxConnections <= 0 && forward.UserMaxClientIps <= 0 && nodeCap <= 0 {
		return false, -1, -1
	}
	connections := forward.UserMaxConnections
	if connections <= 0 || (nodeCap > 0 && nodeCap < connections) {
		connections = nodeCap
	}
	if connections <= 0 {
		connections = -1
	}
	clientIPs := forward.UserMaxClientIps
	if clientIPs <= 0 {
		clientIPs = -1
	}
	return true, connections, clientIPs
}

// joinedEntryNodes lists the entry nodes where the user's forwards join the
// quota group under the given caps (nil = none). A node whose state differs
// between two cap sets needs its forwards re-pushed (the group is attached
// when a service is created).
func joinedEntryNodes(limits repo.QuotaUserLimits, caps map[repo.ServerQuotaKey]int) map[int64]bool {
	out := map[int64]bool{}
	if limits.MaxConnections > 0 || limits.MaxClientIps > 0 {
		return nil // every entry joins
	}
	for key, value := range caps {
		if key.Kind == "node" && value > 0 {
			out[key.RefID] = true
		}
	}
	return out
}

// resyncForwardsForCapToggle re-pushes the user's active forwards entering at
// nodes whose quota-group attachment changed between before and after; the
// budgets themselves always follow through the coordinator without a re-push.
func (h *Handler) resyncForwardsForCapToggle(userID int64, limits repo.QuotaUserLimits, before, after map[repo.ServerQuotaKey]int) []string {
	old, cur := joinedEntryNodes(limits, before), joinedEntryNodes(limits, after)
	if old == nil || cur == nil {
		return nil
	}
	changed := map[int64]bool{}
	for id := range old {
		if !cur[id] {
			changed[id] = true
		}
	}
	for id := range cur {
		if !old[id] {
			changed[id] = true
		}
	}
	if len(changed) == 0 {
		return nil
	}
	forwards, err := h.repo.ListActiveForwardsByUser(userID)
	if err != nil {
		return []string{fmt.Sprintf("入站连接数转发同步失败: %v", err)}
	}
	warnings := []string{}
	for _, item := range forwards {
		forward, getErr := h.getForwardRecord(item.ID)
		if getErr != nil || forward == nil {
			continue
		}
		ports, portErr := h.repo.ListForwardPorts(forward.ID)
		if portErr != nil {
			continue
		}
		touches := false
		for _, fp := range ports {
			if changed[fp.NodeID] {
				touches = true
				break
			}
		}
		if !touches {
			continue
		}
		syncWarnings, syncErr := h.syncForwardServicesWithWarnings(forward, "UpdateService", true)
		warnings = append(warnings, syncWarnings...)
		if syncErr != nil {
			warnings = append(warnings, fmt.Sprintf("转发 %s 入站连接数下发失败: %v", forward.Name, syncErr))
			log.Printf("plan158: forward=%d resync after cap toggle failed: %v", forward.ID, syncErr)
		}
	}
	return warnings
}

// syncUserLandingServersAsync re-pushes the landing servers the user has
// nodes on (their per-user landing group follows the user's caps). Runs in
// the background so a user save never waits for agents.
func (h *Handler) syncUserLandingServersAsync(userID int64) {
	if h == nil || h.repo == nil || userID <= 0 {
		return
	}
	ids, err := h.repo.LandingNodeOwnerServers(userID)
	if err != nil || len(ids) == 0 {
		return
	}
	go func() {
		for _, id := range ids {
			h.syncLandingServer(id)
		}
	}()
}

// Relay sources (plan 158): the IP addresses our own FLVX nodes may connect
// from. A landing inbound from one of them is a forward rule's relay, already
// counted at the rule's entry, so landing Cores exclude it from the user's
// total (connections and IPs). Names are resolved at most every 10 minutes
// (failures keep the last addresses); agent-reported addresses (DDNS state)
// cover nodes with changing IPv6 prefixes. Loopback is always included.
const relaySourcesRefresh = 10 * time.Minute

// maxRelaySources bounds the list sent to agents / 妙妙屋 (deduplicated).
const maxRelaySources = 4096

type relaySourceCache struct {
	mu         sync.Mutex
	hostsHash  string
	at         time.Time
	resolved   map[string][]string
	names      []string // resolved addresses of host names (background)
	refreshing bool
}

func (h *Handler) relaySourceState() *relaySourceCache {
	h.relaySourcesOnce.Do(func() { h.relaySources = &relaySourceCache{resolved: map[string][]string{}} })
	return h.relaySources
}

// landingRelaySources returns the sorted IP literals of every FLVX node. It
// never waits for DNS: literal addresses count at once, host names (DDNS
// nodes) are resolved in the background at most every 10 minutes (or when
// the host list changes) and join the next sync.
func (h *Handler) landingRelaySources(ctx context.Context) []string {
	set := map[string]bool{"127.0.0.1": true, "::1": true}
	add := func(host string) bool {
		ip, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(host), "[]"))
		if err == nil {
			set[ip.Unmap().String()] = true
		}
		return err == nil
	}
	if h == nil || h.repo == nil {
		return []string{"127.0.0.1", "::1"}
	}
	hosts, err := h.repo.LandingHostInventory()
	st := h.relaySourceState()
	st.mu.Lock()
	if err == nil {
		names := []string{}
		for _, host := range hosts.Nodes {
			if !add(host) {
				names = append(names, host)
			}
		}
		sort.Strings(names)
		raw, _ := json.Marshal(names)
		if !st.refreshing && len(names) > 0 && (string(raw) != st.hostsHash || time.Since(st.at) >= relaySourcesRefresh) {
			st.refreshing = true
			go h.refreshRelayNames(names, string(raw))
		}
	}
	for _, ip := range st.names {
		add(ip)
	}
	st.mu.Unlock()
	if h.ddns != nil {
		m := h.ddnsState()
		m.mu.Lock()
		for _, a := range m.addrs {
			for _, ip := range append(append([]string{}, a.V4...), a.V6...) {
				add(ip)
			}
		}
		m.mu.Unlock()
	}
	out := make([]string, 0, len(set))
	for ip := range set {
		out = append(out, ip)
	}
	sort.Strings(out)
	if len(out) > maxRelaySources {
		out = out[:maxRelaySources]
	}
	return out
}

// refreshRelayNames resolves node host names (2 s each, failures keep the
// last addresses) outside every lock.
func (h *Handler) refreshRelayNames(names []string, hash string) {
	st := h.relaySourceState()
	st.mu.Lock()
	cache := make(map[string][]string, len(st.resolved))
	for k, v := range st.resolved {
		cache[k] = v
	}
	st.mu.Unlock()
	resolved, _ := resolveSharedHosts(context.Background(), landingcustom.LandingHosts{Nodes: names, Native: []string{}}, cache)
	st.mu.Lock()
	st.resolved, st.names, st.hostsHash, st.at, st.refreshing = cache, resolved.Nodes, hash, time.Now(), false
	st.mu.Unlock()
}

// landingUserLimitsForServer builds the plan-158 part of a native landing
// payload: which owners are limited on this server (their nodes join the
// per-user Core group) and each group's static upper bounds, used by the
// agent until the coordinator's first dynamic budget.
type landingUserGroup struct {
	Group                string `json:"group"`
	MaxConnections       int    `json:"max_connections"`
	MaxClientIPs         int    `json:"max_client_ips"`
	ServerMaxConnections int    `json:"server_max_connections"`
}

func (h *Handler) landingUserGroups(s *model.LandingServer, owners []int64) (map[int64]string, []landingUserGroup, error) {
	groupOf := map[int64]string{}
	groups := []landingUserGroup{}
	if len(owners) == 0 {
		return groupOf, groups, nil
	}
	limits, err := h.repo.QuotaUserLimitsFor(owners)
	if err != nil {
		return nil, nil, err
	}
	caps, err := h.repo.ServerConnCapsFor(owners, repo.LandingServerQuotaKey(0, s))
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i] < owners[j] })
	for _, userID := range owners {
		l := limits[userID]
		cap := caps[userID]
		if l.MaxConnections <= 0 && l.MaxClientIps <= 0 && cap <= 0 {
			continue
		}
		g := landingUserGroup{Group: landingQuotaGroupForUser(userID), MaxConnections: -1, MaxClientIPs: -1, ServerMaxConnections: -1}
		if l.MaxConnections > 0 {
			g.MaxConnections = l.MaxConnections
		}
		if l.MaxClientIps > 0 {
			g.MaxClientIPs = l.MaxClientIps
		}
		if cap > 0 {
			g.ServerMaxConnections = cap
		}
		groupOf[userID] = g.Group
		groups = append(groups, g)
	}
	return groupOf, groups, nil
}

// Shared (妙妙屋) servers, contract landing-user-limits-v1: budgets go out on
// the shared loop (every 5 s) when they changed and every
// sharedUserLimitsRefresh otherwise (the answer carries the usage), only for
// shares that advertise the capability and hold nodes of limited users.
const (
	sharedUserLimitsRefresh = 10 * time.Second
)

// exchangeSharedUserLimits runs with sharedLandingMu held (shared loop, 5 s).
func (h *Handler) exchangeSharedUserLimits(ctx context.Context, client *landingcustom.Client, s model.LandingServer, force bool) error {
	if h.quotaGroups == nil {
		return nil
	}
	budgets, ok := h.quotaGroups.ShareBudgets(s.ID)
	st := h.sharedState()
	capable := repo.SharedUserLimitsSupported(&s) && repo.SharedLandingCaps(&s).Status == "active"
	if !capable {
		h.quotaGroups.ForgetShare(s.ID)
		delete(st.userLimitsHash, s.ID)
		delete(st.userLimitsAt, s.ID)
		return nil
	}
	if !ok {
		// No slot: no limited user has nodes here (or the coordinator has not
		// run since the capability appeared — nothing else drives it on a
		// shared-only panel). A share that had budgets gets one empty,
		// authoritative list so 妙妙屋 drops them now, not after its expiry.
		h.quotaGroups.Trigger()
		h.quotaGroups.ForgetShare(s.ID)
		if _, sent := st.userLimitsHash[s.ID]; !sent {
			return nil
		}
		budgets = []quotaShareBudget{}
	}
	req := landingcustom.UserLimitsRequest{Version: landingcustom.UserLimitsVersion, Users: make([]landingcustom.UserLimitBudget, 0, len(budgets))}
	for _, b := range budgets {
		u := landingcustom.UserLimitBudget{UserID: fmt.Sprint(b.UserID), RelayIPReserve: b.RelayIps}
		if b.MaxConnections >= 0 {
			v := int64(b.MaxConnections)
			u.MaxInboundConnections = &v
		}
		if b.ServerMaxConnections != nil {
			v := int64(*b.ServerMaxConnections)
			u.ServerMaxInboundConnections = &v
		}
		if b.MaxClientIps >= 0 {
			v := b.MaxClientIps
			u.MaxInboundOnlineIPs = &v
		}
		req.Users = append(req.Users, u)
	}
	raw, _ := json.Marshal(req.Users)
	now := time.Now()
	if !force && st.userLimitsHash[s.ID] == string(raw) && now.Sub(st.userLimitsAt[s.ID]) < sharedUserLimitsRefresh {
		return nil
	}
	req.Sequence = max(st.userLimitsSeq[s.ID]+1, now.UnixMilli())
	var report landingcustom.UserLimitsReport
	err := h.sharedAttempt(fmt.Sprintf("user-limits:%d", s.ID), force, false, func() error {
		var e error
		report, e = client.PutUserLimits(ctx, s.RemoteServerID, req)
		return e
	})
	if err != nil {
		return err
	}
	st.userLimitsSeq[s.ID] = req.Sequence
	if !ok {
		delete(st.userLimitsHash, s.ID)
		delete(st.userLimitsAt, s.ID)
		return nil
	}
	st.userLimitsHash[s.ID], st.userLimitsAt[s.ID] = string(raw), now
	usages := make([]quotaShareUsage, 0, len(report.Users))
	for _, u := range report.Users {
		var userID int64
		if _, e := fmt.Sscan(u.UserID, &userID); e != nil || userID <= 0 {
			continue
		}
		usages = append(usages, quotaShareUsage{UserID: userID, Connections: u.InboundConnections, Relay: u.RelayConnections, ClientIps: u.OnlineIPs})
	}
	h.quotaGroups.ObserveShare(s.ID, usages)
	return nil
}
