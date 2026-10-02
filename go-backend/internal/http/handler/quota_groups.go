package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

const quotaGroupPushInterval = time.Second

type quotaGroupBudget struct {
	Group          string `json:"group"`
	MaxConnections int    `json:"maxConnections"`
	MaxClientIps   int    `json:"maxClientIps"`
}

type quotaNodeUsage struct {
	Connections int
	ClientIps   map[string]struct{}
}

type quotaLiveUsage struct {
	Connections int
	ClientIps   int
}

type quotaBudgetPlan struct {
	Budgets      map[int64][]quotaGroupBudget
	NodeVersions map[int64]string
	LiveUsage    map[int64]quotaLiveUsage
}

type quotaTargetSource interface {
	ListQuotaGroupTargets() ([]repo.QuotaGroupTarget, error)
}

type quotaCommandSender func(nodeID int64, budgets []quotaGroupBudget) error

type quotaCoordinator struct {
	source   quotaTargetSource
	send     quotaCommandSender
	interval time.Duration
	now      func() time.Time

	mu            sync.Mutex
	usageByNode   map[int64]map[int64]quotaNodeUsage
	liveUsage     map[int64]quotaLiveUsage
	lastSent      map[int64]string
	lastPush      map[int64]time.Time
	unsupported   map[int64]bool
	errorLogged   map[int64]bool
	running       bool
	pending       bool
	timer         *time.Timer
	lastReconcile time.Time
}

func newQuotaCoordinator(source quotaTargetSource, send quotaCommandSender) *quotaCoordinator {
	return &quotaCoordinator{
		source:      source,
		send:        send,
		interval:    quotaGroupPushInterval,
		now:         time.Now,
		usageByNode: make(map[int64]map[int64]quotaNodeUsage),
		liveUsage:   make(map[int64]quotaLiveUsage),
		lastSent:    make(map[int64]string),
		lastPush:    make(map[int64]time.Time),
		unsupported: make(map[int64]bool),
		errorLogged: make(map[int64]bool),
	}
}

func quotaGroupForUser(userID int64) string {
	return fmt.Sprintf("user-%d", userID)
}

func quotaGroupUserID(group string) (int64, bool) {
	var userID int64
	if _, err := fmt.Sscanf(strings.TrimSpace(group), "user-%d", &userID); err != nil || userID <= 0 {
		return 0, false
	}
	if quotaGroupForUser(userID) != strings.TrimSpace(group) {
		return 0, false
	}
	return userID, true
}

func (c *quotaCoordinator) Observe(nodeID int64, usages []ws.QuotaGroupUsage) {
	if c == nil || nodeID <= 0 {
		return
	}
	nodeUsage := make(map[int64]quotaNodeUsage, len(usages))
	for _, item := range usages {
		userID, ok := quotaGroupUserID(item.Group)
		if !ok {
			continue
		}
		ips := make(map[string]struct{}, len(item.ClientIps))
		for _, rawIP := range item.ClientIps {
			if ip := strings.TrimSpace(rawIP); ip != "" {
				ips[ip] = struct{}{}
			}
		}
		connections := item.Connections
		if connections < 0 {
			connections = 0
		}
		nodeUsage[userID] = quotaNodeUsage{Connections: connections, ClientIps: ips}
	}

	c.mu.Lock()
	c.usageByNode[nodeID] = nodeUsage
	c.mu.Unlock()
	c.Trigger()
}

func (c *quotaCoordinator) NodeOnline(nodeID int64) {
	if c == nil || nodeID <= 0 {
		return
	}
	c.mu.Lock()
	delete(c.usageByNode, nodeID)
	delete(c.lastSent, nodeID)
	delete(c.lastPush, nodeID)
	delete(c.unsupported, nodeID)
	delete(c.errorLogged, nodeID)
	c.mu.Unlock()
	c.Trigger()
}

func (c *quotaCoordinator) NodeOffline(nodeID int64) {
	if c == nil || nodeID <= 0 {
		return
	}
	c.mu.Lock()
	delete(c.usageByNode, nodeID)
	delete(c.lastSent, nodeID)
	delete(c.lastPush, nodeID)
	c.mu.Unlock()
	c.Trigger()
}

func (c *quotaCoordinator) InvalidateNodes(nodeIDs ...int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	for _, nodeID := range nodeIDs {
		if nodeID > 0 {
			delete(c.lastSent, nodeID)
		}
	}
	c.mu.Unlock()
	c.Trigger()
}

func (c *quotaCoordinator) Trigger() {
	if c == nil || c.source == nil || c.send == nil {
		return
	}
	c.mu.Lock()
	c.pending = true
	if c.running || c.timer != nil {
		c.mu.Unlock()
		return
	}

	now := c.now()
	delay := time.Duration(0)
	if !c.lastReconcile.IsZero() {
		elapsed := now.Sub(c.lastReconcile)
		if elapsed < c.interval {
			delay = c.interval - elapsed
		}
	}
	if delay > 0 {
		c.timer = time.AfterFunc(delay, c.startScheduledReconcile)
		c.mu.Unlock()
		return
	}
	c.running = true
	c.pending = false
	c.lastReconcile = now
	c.mu.Unlock()
	go c.runReconcile()
}

func (c *quotaCoordinator) startScheduledReconcile() {
	c.mu.Lock()
	c.timer = nil
	if c.running || !c.pending {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.pending = false
	c.lastReconcile = c.now()
	c.mu.Unlock()
	c.runReconcile()
}

func (c *quotaCoordinator) runReconcile() {
	c.reconcileOnce()
	c.mu.Lock()
	c.running = false
	pending := c.pending
	c.mu.Unlock()
	if pending {
		c.Trigger()
	}
}

func (c *quotaCoordinator) reconcileOnce() {
	targets, err := c.source.ListQuotaGroupTargets()
	if err != nil {
		log.Printf("quota groups: list targets failed: %v", err)
		return
	}

	c.mu.Lock()
	usageSnapshot := cloneQuotaUsage(c.usageByNode)
	c.mu.Unlock()
	plan := computeQuotaBudgetPlan(targets, usageSnapshot)

	c.mu.Lock()
	c.liveUsage = plan.LiveUsage
	c.mu.Unlock()

	var wg sync.WaitGroup
	for nodeID, budgets := range plan.Budgets {
		encoded, _ := json.Marshal(budgets)
		hash := string(encoded)
		now := c.now()

		c.mu.Lock()
		if c.unsupported[nodeID] || c.lastSent[nodeID] == hash || (!c.lastPush[nodeID].IsZero() && now.Sub(c.lastPush[nodeID]) < c.interval) {
			c.mu.Unlock()
			continue
		}
		c.lastPush[nodeID] = now
		c.mu.Unlock()

		budgetsCopy := append([]quotaGroupBudget(nil), budgets...)
		version := plan.NodeVersions[nodeID]
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.send(nodeID, budgetsCopy)
			c.mu.Lock()
			defer c.mu.Unlock()
			if err == nil {
				c.lastSent[nodeID] = hash
				delete(c.errorLogged, nodeID)
				return
			}
			if quotaAgentSupport(version) == quotaAgentUnknown {
				c.unsupported[nodeID] = true
			}
			if !c.errorLogged[nodeID] {
				log.Printf("quota groups: node %d rejected SetQuotaGroups: %v", nodeID, err)
				c.errorLogged[nodeID] = true
			}
		}()
	}
	wg.Wait()
}

func (c *quotaCoordinator) LiveUsage() map[int64]quotaLiveUsage {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[int64]quotaLiveUsage, len(c.liveUsage))
	for userID, usage := range c.liveUsage {
		out[userID] = usage
	}
	return out
}

func cloneQuotaUsage(source map[int64]map[int64]quotaNodeUsage) map[int64]map[int64]quotaNodeUsage {
	out := make(map[int64]map[int64]quotaNodeUsage, len(source))
	for nodeID, users := range source {
		out[nodeID] = make(map[int64]quotaNodeUsage, len(users))
		for userID, usage := range users {
			ips := make(map[string]struct{}, len(usage.ClientIps))
			for ip := range usage.ClientIps {
				ips[ip] = struct{}{}
			}
			out[nodeID][userID] = quotaNodeUsage{Connections: usage.Connections, ClientIps: ips}
		}
	}
	return out
}

func computeQuotaBudgetPlan(targets []repo.QuotaGroupTarget, usageByNode map[int64]map[int64]quotaNodeUsage) quotaBudgetPlan {
	plan := quotaBudgetPlan{
		Budgets:      make(map[int64][]quotaGroupBudget),
		NodeVersions: make(map[int64]string),
		LiveUsage:    make(map[int64]quotaLiveUsage),
	}
	type userLimits struct {
		connections int
		clientIps   int
		nodes       map[int64]repo.QuotaGroupTarget
	}
	users := make(map[int64]*userLimits)
	for _, target := range targets {
		if target.UserID <= 0 || target.NodeID <= 0 || (target.MaxConnections <= 0 && target.MaxClientIps <= 0) {
			continue
		}
		limits := users[target.UserID]
		if limits == nil {
			limits = &userLimits{nodes: make(map[int64]repo.QuotaGroupTarget)}
			users[target.UserID] = limits
		}
		limits.connections = target.MaxConnections
		limits.clientIps = target.MaxClientIps
		limits.nodes[target.NodeID] = target
		plan.NodeVersions[target.NodeID] = target.NodeVersion
	}

	for userID, limits := range users {
		allIps := make(map[string]struct{})
		totalConnections := 0
		for nodeID := range limits.nodes {
			usage := usageByNode[nodeID][userID]
			totalConnections += usage.Connections
			for ip := range usage.ClientIps {
				allIps[ip] = struct{}{}
			}
		}
		plan.LiveUsage[userID] = quotaLiveUsage{Connections: totalConnections, ClientIps: len(allIps)}

		for nodeID, target := range limits.nodes {
			if target.NodeStatus != 1 || quotaAgentSupport(target.NodeVersion) == quotaAgentUnsupported {
				continue
			}
			connectionBudget := -1
			if limits.connections > 0 {
				otherConnections := totalConnections - usageByNode[nodeID][userID].Connections
				connectionBudget = limits.connections - otherConnections
				if connectionBudget < 0 {
					connectionBudget = 0
				}
			}

			clientIPBudget := -1
			if limits.clientIps > 0 {
				localIps := usageByNode[nodeID][userID].ClientIps
				otherOnlyIps := make(map[string]struct{})
				for otherNodeID := range limits.nodes {
					if otherNodeID == nodeID {
						continue
					}
					for ip := range usageByNode[otherNodeID][userID].ClientIps {
						if _, activeLocally := localIps[ip]; !activeLocally {
							otherOnlyIps[ip] = struct{}{}
						}
					}
				}
				clientIPBudget = limits.clientIps - len(otherOnlyIps)
				if clientIPBudget < 0 {
					clientIPBudget = 0
				}
			}

			plan.Budgets[nodeID] = append(plan.Budgets[nodeID], quotaGroupBudget{
				Group:          quotaGroupForUser(userID),
				MaxConnections: connectionBudget,
				MaxClientIps:   clientIPBudget,
			})
		}
	}

	for nodeID := range plan.Budgets {
		sort.Slice(plan.Budgets[nodeID], func(i, j int) bool {
			return plan.Budgets[nodeID][i].Group < plan.Budgets[nodeID][j].Group
		})
	}
	return plan
}

type quotaAgentSupportLevel int

const (
	quotaAgentUnknown quotaAgentSupportLevel = iota
	quotaAgentUnsupported
	quotaAgentSupported
)

func quotaAgentSupport(version string) quotaAgentSupportLevel {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" || !stableVersionPattern.MatchString(version) {
		return quotaAgentUnknown
	}
	_, fork, revision := splitForkVersion(version)
	if fork && revision >= 13 {
		return quotaAgentSupported
	}
	return quotaAgentUnsupported
}
