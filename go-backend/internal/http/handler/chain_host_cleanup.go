package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 252: a chain-only exit landing (auto-created for 「我自己的节点」) whose
// last chain copy is gone — by any path: rule delete, switch to another
// target, exit moved off the tunnel, failed create, tunnel / user delete,
// orphan sweep — is removed with its Core after a grace period. Paused rules
// keep their copies and therefore their server. The sweep is one indexed
// query; node commands only go to idle candidates.

var (
	chainHostFirstSweep    = 3 * time.Minute
	chainHostSweepEvery    = 10 * time.Minute
	chainHostGrace         = 10 * time.Minute // idle this long before removal (no flapping on delete + recreate)
	chainHostRetryAfter    = 30 * time.Minute // after a failed attempt
	chainHostKickDelay     = 30 * time.Second // a chain change: look again this soon
	chainHostRemoveTimeout = 90 * time.Second // agent bounds its own work to 60 s
)

// Agent refusals (go-gost/x/landing RemoveCore*) after which the empty
// desired state is pushed again and the removal retried once.
var chainHostStateRefusals = []string{"landing desired state still has nodes", "stale landing revision", "landing inbounds still applied"}

type chainHostCleaner struct {
	mu        sync.Mutex
	idleSince map[int64]time.Time // server -> first sweep that saw it without copies
	retryAt   map[int64]time.Time // server -> earliest next attempt after a failure
	noted     map[int64]string    // server -> last logged reason (one line per change)
	kickCh    chan struct{}
}

func (c *chainHostCleaner) kicks() chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kickCh == nil {
		c.kickCh = make(chan struct{}, 1)
	}
	return c.kickCh
}

func (c *chainHostCleaner) kick() {
	select {
	case c.kicks() <- struct{}{}:
	default:
	}
}

// note logs one line per server and reason (no periodic repeats).
func (c *chainHostCleaner) note(s model.LandingServer, nodeName, reason string) {
	c.mu.Lock()
	if c.noted == nil {
		c.noted = map[int64]string{}
	}
	if c.noted[s.ID] == reason {
		c.mu.Unlock()
		return
	}
	c.noted[s.ID] = reason
	c.mu.Unlock()
	log.Printf("[chain-host] idle chain-only landing server=%d on %q kept: %s", s.ID, nodeName, reason)
}

func (c *chainHostCleaner) backoff(id int64, until time.Time) {
	c.mu.Lock()
	if c.retryAt == nil {
		c.retryAt = map[int64]time.Time{}
	}
	c.retryAt[id] = until
	c.mu.Unlock()
}

func (c *chainHostCleaner) forget(id int64) {
	c.mu.Lock()
	delete(c.idleSince, id)
	delete(c.retryAt, id)
	delete(c.noted, id)
	c.mu.Unlock()
}

func (h *Handler) runChainHostCleanupLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	kicks := h.chainHosts.kicks()
	timer := time.NewTimer(chainHostFirstSweep)
	defer timer.Stop()
	due := time.Now().Add(chainHostFirstSweep)
	for {
		select {
		case <-ctx.Done():
			return
		case <-kicks:
			if at := time.Now().Add(chainHostKickDelay); at.Before(due) {
				due = at
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(time.Until(due))
			}
		case <-timer.C:
			next := h.sweepChainHosts(ctx, time.Now())
			due = time.Now().Add(next)
			timer.Reset(next)
		}
	}
}

// sweepChainHosts notes idle chain-only servers, removes those idle for the
// grace period and returns the delay until the next sweep.
func (h *Handler) sweepChainHosts(ctx context.Context, now time.Time) time.Duration {
	next := chainHostSweepEvery
	if h == nil || h.repo == nil {
		return next
	}
	servers, err := h.repo.IdleChainOnlyServers()
	if err != nil {
		log.Printf("[chain-host] sweep failed: %s", compactErrorMessage(err.Error()))
		return next
	}
	c := &h.chainHosts
	c.mu.Lock()
	if c.idleSince == nil {
		c.idleSince = map[int64]time.Time{}
	}
	idle := map[int64]bool{}
	for _, s := range servers {
		idle[s.ID] = true
		if _, ok := c.idleSince[s.ID]; !ok {
			c.idleSince[s.ID] = now
		}
	}
	for id := range c.idleSince {
		if !idle[id] { // used again (or gone): start over next time
			delete(c.idleSince, id)
			delete(c.retryAt, id)
			delete(c.noted, id)
		}
	}
	due := []model.LandingServer{}
	for _, s := range servers {
		ready := c.idleSince[s.ID].Add(chainHostGrace)
		if r, ok := c.retryAt[s.ID]; ok && r.After(ready) {
			ready = r
		}
		if !now.Before(ready) {
			due = append(due, s)
		} else if d := ready.Sub(now); d < next {
			next = d
		}
	}
	c.mu.Unlock()
	for _, s := range due {
		if ctx.Err() != nil {
			break
		}
		h.cleanupChainHost(s, now)
	}
	if next < chainHostKickDelay {
		next = chainHostKickDelay
	}
	return next
}

// cleanupChainHost removes one idle chain-only server and its Core, or logs
// once why it stays. Exit agents without RemoveLandingCore (landing_chain < 3)
// get no command at all; the server stays as before until their upgrade.
func (h *Handler) cleanupChainHost(s model.LandingServer, now time.Time) {
	c := &h.chainHosts
	node, nodeErr := h.repo.GetNodeByID(s.NodeID)
	name := fmt.Sprintf("node %d", s.NodeID)
	if node != nil {
		name = node.Name
	}
	reason, err := h.repo.ChainHostKeepReason(s.ID)
	if err != nil {
		return
	}
	switch {
	case reason != "":
	case nodeErr != nil || node == nil:
		reason = "节点不存在"
	case node.IsRemote != 0:
		reason = "远程节点"
	case !node.LandingCapable || node.LandingChain < repo.ChainHostRemoveVersion:
		reason = fmt.Sprintf("节点程序版本过低（landing_chain=%d），升级节点后自动清理", node.LandingChain)
	case node.Status != 1:
		reason = "节点离线，恢复在线后清理"
	}
	if reason != "" {
		c.note(s, name, reason)
		return
	}
	h.landingSyncMu.Lock()
	defer h.landingSyncMu.Unlock()
	// Re-check with every desired push held off.
	cur, err := h.repo.GetLandingServer(s.ID)
	if err != nil {
		return
	}
	if cur == nil {
		c.forget(s.ID)
		return
	}
	if reason, err = h.repo.ChainHostKeepReason(cur.ID); err != nil || reason != "" {
		if reason != "" {
			c.note(*cur, name, reason)
		}
		return
	}
	res, err := h.sendRemoveLandingCore(cur)
	if err != nil && chainHostStateRefusal(err) {
		// The agent still holds nodes or a newer state (it missed the push
		// after the delete): push the empty state now and retry once.
		if _, serr := h.syncNativeLandingServerLocked(cur.ID); serr != nil {
			err = serr
		} else if cur, err = h.repo.GetLandingServer(s.ID); err == nil && cur != nil {
			res, err = h.sendRemoveLandingCore(cur)
		}
	}
	if err != nil || cur == nil {
		msg := "落地服务器已不存在"
		if err != nil {
			msg = compactErrorMessage(err.Error())
		}
		c.backoff(s.ID, now.Add(chainHostRetryAfter))
		c.note(s, name, "Core 清理未完成，30 分钟后重试："+msg)
		return
	}
	removed, keep, err := h.repo.DeleteIdleChainOnlyServer(cur.ID)
	if err != nil {
		c.backoff(s.ID, now.Add(chainHostRetryAfter))
		c.note(s, name, "Core 已清理，删除服务器记录失败："+compactErrorMessage(err.Error()))
		return
	}
	if !removed {
		// Used again between the check and the delete (e.g. a new chain
		// rule): its Core comes back with this push.
		c.note(s, name, "Core 已清理但又有新用途（"+keep+"），重新部署")
		if _, err := h.syncNativeLandingServerLocked(cur.ID); err != nil {
			log.Printf("[chain-host] server=%d redeploy pending: %s", cur.ID, compactErrorMessage(err.Error()))
		}
		return
	}
	c.mu.Lock()
	since := c.idleSince[s.ID]
	c.mu.Unlock()
	c.forget(s.ID)
	idle := ""
	if !since.IsZero() {
		idle = fmt.Sprintf(", idle %s", now.Sub(since).Round(time.Second))
	}
	log.Printf("[chain-host] removed idle chain-only landing server=%d on %q: Core stopped, unit removed=%v, %d files removed, kept %v%s",
		s.ID, name, res.UnitRemoved, len(res.Files), res.Kept, idle)
}

// chainHostRemoveResult mirrors the agent's RemoveCoreResult.
type chainHostRemoveResult struct {
	Removed     bool     `json:"removed"`
	WasRunning  bool     `json:"was_running"`
	UnitRemoved bool     `json:"unit_removed"`
	Files       []string `json:"files"`
	Kept        []string `json:"kept"`
	DirRemoved  bool     `json:"dir_removed"`
}

func (h *Handler) sendRemoveLandingCore(s *model.LandingServer) (chainHostRemoveResult, error) {
	var out chainHostRemoveResult
	result, err := h.sendNodeCommandWithTimeout(s.NodeID, "RemoveLandingCore", map[string]interface{}{"revision": s.Revision}, chainHostRemoveTimeout, false, false)
	if err != nil {
		return out, &chainHostCommandError{result: result, err: err}
	}
	if !result.Success {
		return out, fmt.Errorf("节点未确认 Core 清理")
	}
	raw, _ := json.Marshal(result.Data)
	if err := json.Unmarshal(raw, &out); err != nil || !out.Removed {
		return out, fmt.Errorf("节点 Core 清理结果格式错误")
	}
	return out, nil
}

// chainHostCommandError keeps the agent's answer (Type set = it answered).
type chainHostCommandError struct {
	result ws.CommandResult
	err    error
}

func (e *chainHostCommandError) Error() string { return e.err.Error() }

func chainHostStateRefusal(err error) bool {
	ce, ok := err.(*chainHostCommandError)
	if !ok || ce.result.Type == "" {
		return false // no answer: the command may have run; never push (that would reinstall)
	}
	msg := ce.result.Message + " " + ce.err.Error()
	for _, r := range chainHostStateRefusals {
		if strings.Contains(msg, r) {
			return true
		}
	}
	return false
}
