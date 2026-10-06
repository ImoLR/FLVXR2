package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	nodeRedeployDebounce          = 10 * time.Second
	tunnelRedeployRetryInterval   = 60 * time.Second
	tunnelRedeployRetryMaxBackoff = 10 * time.Minute
)

type nodeRedeployState struct {
	online, pending, running bool
	readyAt                  time.Time
	timer                    *time.Timer
	generation               uint64
}

// A running node has no timer. Reconnects move readyAt and set one pending bit,
// so completion schedules at most one follow-up after the stable-online window.
type nodeRedeployScheduler struct {
	mu      sync.Mutex
	wg      sync.WaitGroup
	states  map[int64]*nodeRedeployState
	delay   time.Duration
	run     func(int64)
	online  func(int64) bool
	stopped bool
}

func newNodeRedeployScheduler(delay time.Duration, run func(int64), online func(int64) bool) *nodeRedeployScheduler {
	return &nodeRedeployScheduler{states: make(map[int64]*nodeRedeployState), delay: delay, run: run, online: online}
}

func (s *nodeRedeployScheduler) Online(nodeID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	state := s.states[nodeID]
	if state == nil {
		state = &nodeRedeployState{}
		s.states[nodeID] = state
	}
	state.online, state.pending = true, true
	state.readyAt = time.Now().Add(s.delay)
	if !state.running {
		s.scheduleLocked(nodeID, state)
	}
}

func (s *nodeRedeployScheduler) Offline(nodeID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state := s.states[nodeID]; state != nil {
		state.online, state.pending = false, false
		state.generation++
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}
	}
}

func (s *nodeRedeployScheduler) scheduleLocked(nodeID int64, state *nodeRedeployState) {
	if state.timer != nil {
		state.timer.Stop()
	}
	state.generation++
	generation := state.generation
	state.timer = time.AfterFunc(time.Until(state.readyAt), func() {
		s.mu.Lock()
		if s.stopped || generation != state.generation || !state.online || !state.pending || state.running {
			s.mu.Unlock()
			return
		}
		state.timer = nil
		if s.online != nil && !s.online(nodeID) {
			state.online, state.pending = false, false
			s.mu.Unlock()
			return
		}
		state.running, state.pending = true, false
		s.wg.Add(1)
		s.mu.Unlock()
		defer s.wg.Done()
		s.run(nodeID)
		s.mu.Lock()
		state.running = false
		if !s.stopped && state.online && state.pending {
			s.scheduleLocked(nodeID, state)
		}
		s.mu.Unlock()
	})
}

func (s *nodeRedeployScheduler) Stop() {
	s.mu.Lock()
	s.stopped = true
	for _, state := range s.states {
		if state.timer != nil {
			state.timer.Stop()
		}
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (h *Handler) nodeRuntimeOnline(nodeID int64) bool {
	node, err := h.getNodeRecord(nodeID)
	return err == nil && node != nil && node.Status == 1
}

func (h *Handler) scheduleNodeRedeploy(nodeID int64) {
	h.nodeRedeployMu.Lock()
	if h.nodeRedeploy == nil {
		h.nodeRedeploy = newNodeRedeployScheduler(nodeRedeployDebounce, h.redeployNodeRuntime, h.nodeRuntimeOnline)
	}
	scheduler := h.nodeRedeploy
	h.nodeRedeployMu.Unlock()
	scheduler.Online(nodeID)
}

func (h *Handler) onNodeOffline(nodeID int64) {
	// Hooks run asynchronously; an old disconnect must not cancel a new session.
	if h.nodeRuntimeOnline(nodeID) {
		return
	}
	h.nodeRedeployMu.Lock()
	scheduler := h.nodeRedeploy
	h.nodeRedeployMu.Unlock()
	if scheduler != nil {
		scheduler.Offline(nodeID)
	}
	if h.quotaGroups != nil {
		h.quotaGroups.NodeOffline(nodeID)
	}
}

func (h *Handler) stopNodeRedeploys() {
	h.nodeRedeployMu.Lock()
	scheduler := h.nodeRedeploy
	h.nodeRedeployMu.Unlock()
	if scheduler != nil {
		scheduler.Stop()
	}
}

type tunnelNodeKey struct{ tunnelID, nodeID int64 }
type tunnelNodeRetry struct {
	next  time.Time
	delay time.Duration
}

// Repeated reconnect failures retain the existing retry deadline/backoff.
func (h *Handler) recordTunnelRuntimeResult(tunnelID, nodeID int64, err error) {
	h.redeployRetryMu.Lock()
	defer h.redeployRetryMu.Unlock()
	key := tunnelNodeKey{tunnelID, nodeID}
	if err == nil || errors.Is(err, errTunnelRuntimeInactive) {
		delete(h.redeployPending, key)
		return
	}
	if h.redeployPending == nil {
		h.redeployPending = make(map[tunnelNodeKey]*tunnelNodeRetry)
	}
	if h.redeployPending[key] == nil {
		h.redeployPending[key] = &tunnelNodeRetry{next: time.Now().Add(tunnelRedeployRetryInterval), delay: tunnelRedeployRetryInterval}
	}
}

func (h *Handler) runTunnelRedeployRetryLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	ticker := time.NewTicker(tunnelRedeployRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			h.retryPendingTunnelNodes(now)
		}
	}
}

func (h *Handler) retryPendingTunnelNodes(now time.Time) {
	h.redeployRetryMu.Lock()
	keys := make([]tunnelNodeKey, 0, len(h.redeployPending))
	for key, retry := range h.redeployPending {
		if !retry.next.After(now) {
			keys = append(keys, key)
		}
	}
	h.redeployRetryMu.Unlock()
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tunnelID == keys[j].tunnelID {
			return keys[i].nodeID < keys[j].nodeID
		}
		return keys[i].tunnelID < keys[j].tunnelID
	})
	for _, key := range keys {
		h.retryTunnelNode(key, now)
	}
}

func (h *Handler) retryTunnelNode(key tunnelNodeKey, now time.Time) {
	unlock := h.lockTunnelRuntime(key.tunnelID)
	defer unlock()
	h.redeployRetryMu.Lock()
	retry := h.redeployPending[key]
	if retry == nil || retry.next.After(now) {
		h.redeployRetryMu.Unlock()
		return
	}
	h.redeployRetryMu.Unlock()

	// Check membership even for offline nodes: deleted/disabled tunnels must not
	// leave pending work forever, or recreate runtime after a later reconnect.
	tunnel, err := h.getTunnelRecord(key.tunnelID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (tunnel == nil || tunnel.Status != 1)) {
		h.recordTunnelRuntimeResult(key.tunnelID, key.nodeID, nil)
		return
	}
	if err == nil {
		var member bool
		rows, rowsErr := h.listChainNodesForTunnel(key.tunnelID)
		err = rowsErr
		for _, row := range rows {
			if row.NodeID == key.nodeID {
				member = true
				break
			}
		}
		if err == nil && !member {
			h.recordTunnelRuntimeResult(key.tunnelID, key.nodeID, nil)
			return
		}
	}
	if err == nil && !h.nodeRuntimeOnline(key.nodeID) {
		return
	}
	if err == nil && tunnel.Type == 2 {
		err = h.redeployTunnelRuntimeOnNodeLocked(key.tunnelID, key.nodeID)
	}
	if err == nil {
		err = h.syncTunnelForwardsOnNode(key.tunnelID, key.nodeID)
	}
	if err == nil || errors.Is(err, errTunnelRuntimeInactive) {
		h.recordTunnelRuntimeResult(key.tunnelID, key.nodeID, nil)
		log.Printf("redeploy-retry: tunnel %d node %d ok", key.tunnelID, key.nodeID)
		return
	}
	h.redeployRetryMu.Lock()
	if current := h.redeployPending[key]; current != nil {
		current.delay *= 2
		if current.delay > tunnelRedeployRetryMaxBackoff {
			current.delay = tunnelRedeployRetryMaxBackoff
		}
		current.next = now.Add(current.delay)
	}
	h.redeployRetryMu.Unlock()
	log.Printf("redeploy-retry: tunnel %d node %d failed: %v", key.tunnelID, key.nodeID, err)
}

func (h *Handler) syncTunnelForwardsOnNode(tunnelID, nodeID int64) error {
	forwards, err := h.listForwardsByTunnel(tunnelID)
	if err != nil {
		return err
	}
	var failures []string
	for i := range forwards {
		if err := h.syncForwardServicesOnNode(&forwards[i], nodeID); err != nil {
			failures = append(failures, fmt.Sprintf("规则 %s(%d): %v", forwards[i].Name, forwards[i].ID, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "；"))
	}
	return nil
}
