package ws

import (
	"log"
	"sync"
	"time"
)

// Plan 248: the stored node.status flag (在线/离线) follows the node's current
// agent session.
//
// 11-12 01:59 TW the panel stalled; when it resumed, the old session's 离线
// write queued on the single SQLite connection behind the new session's 在线
// write (database/sql hands a freed connection to a random waiter) and landed
// last, so two connected nodes showed 离线 for 16 h. Every status write of a
// node now runs under that node's status lock and re-checks the live session
// map first: an old session writes 离线 only while no newer session exists,
// and a newer session's 在线 write waits for the old write and lands after it.
// Writes reflect the map at write time, so the last write is always right.
//
// Self-heal for anything that still slips through (a failed write, another
// writer): a live session's metric report checks the stored flag at most every
// nodeStatusHealEvery, and the 2-minute cleanup clears 在线 of local nodes
// without a session.

const (
	nodeStatusHealEvery = 30 * time.Second
	// A session that just ended writes its own 离线; the cleanup leaves the
	// node alone for this long.
	nodeStaleOnlineGrace = 60 * time.Second
)

func (s *Server) nodeStatusLock(nodeID int64) *sync.Mutex {
	s.statusLocksMu.Lock()
	defer s.statusLocksMu.Unlock()
	if s.statusLocks == nil {
		s.statusLocks = make(map[int64]*sync.Mutex)
	}
	lk := s.statusLocks[nodeID]
	if lk == nil {
		lk = &sync.Mutex{}
		s.statusLocks[nodeID] = lk
	}
	return lk
}

func (s *Server) isCurrentSession(ns *nodeSession) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ns != nil && s.nodes[ns.nodeID] == ns
}

// markNodeOnline stores 在线 for ns (write) and tells the browsers, unless a
// newer session replaced ns meanwhile (that one writes for itself).
func (s *Server) markNodeOnline(ns *nodeSession, write func() error) bool {
	lk := s.nodeStatusLock(ns.nodeID)
	lk.Lock()
	defer lk.Unlock()
	if !s.isCurrentSession(ns) {
		return false
	}
	if s.repo != nil && write != nil {
		if err := write(); err != nil {
			log.Printf("[ws] node %d: store 在线 failed: %v (self-heal retries)", ns.nodeID, err)
		}
	}
	s.broadcastStatus(ns.nodeID, 1)
	return true
}

// markNodeOffline stores 离线 after the node's current session ended. It does
// nothing (false) when a newer session of the node registered meanwhile.
func (s *Server) markNodeOffline(nodeID int64) bool {
	lk := s.nodeStatusLock(nodeID)
	lk.Lock()
	defer lk.Unlock()
	if s.NodeOnline(nodeID) {
		return false
	}
	if s.repo != nil {
		if err := s.repo.UpdateNodeStatus(nodeID, 0); err != nil {
			log.Printf("[ws] node %d: store 离线 failed: %v (self-heal retries)", nodeID, err)
		}
	}
	s.broadcastStatus(nodeID, 0)
	return true
}

// maybeHealNodeOnline runs from the session's read loop on metric reports.
func (s *Server) maybeHealNodeOnline(ns *nodeSession, now time.Time) {
	if s == nil || s.repo == nil || ns == nil {
		return
	}
	last := ns.statusCheckedAt.Load()
	if last != 0 && now.UnixNano()-last < int64(nodeStatusHealEvery) {
		return
	}
	if !ns.statusCheckedAt.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	// One single-row read per session per 30 s, off the read loop.
	go s.healNodeOnline(ns)
}

func (s *Server) healNodeOnline(ns *nodeSession) bool {
	status, _, _, _, _, err := s.repo.GetNodeStatusFields(ns.nodeID)
	if err != nil || status == 1 {
		return false
	}
	lk := s.nodeStatusLock(ns.nodeID)
	lk.Lock()
	defer lk.Unlock()
	if !s.isCurrentSession(ns) {
		return false
	}
	if err := s.repo.UpdateNodeStatus(ns.nodeID, 1); err != nil {
		return false
	}
	s.broadcastStatus(ns.nodeID, 1)
	log.Printf("[ws] node %d status self-heal: stored 离线 while its agent session is live → 在线", ns.nodeID)
	return true
}

// healStaleOnline clears a stored 在线 of local nodes without an agent session
// (the flag is only ever set by an agent connecting). Federation nodes keep
// their synced status.
func (s *Server) healStaleOnline(now time.Time) int {
	if s == nil || s.repo == nil {
		return 0
	}
	ids, err := s.repo.ListLocalOnlineNodeIDs()
	if err != nil {
		return 0
	}
	healed := 0
	for _, id := range ids {
		if s.staleOnlineCandidate(id, now) {
			lk := s.nodeStatusLock(id)
			lk.Lock()
			if s.staleOnlineCandidate(id, now) && s.repo.UpdateNodeStatus(id, 0) == nil {
				s.broadcastStatus(id, 0)
				healed++
				log.Printf("[ws] node %d status self-heal: stored 在线 without an agent session → 离线", id)
			}
			lk.Unlock()
		}
	}
	return healed
}

func (s *Server) staleOnlineCandidate(nodeID int64, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.nodes[nodeID] != nil {
		return false
	}
	if at, ok := s.nodeOfflineTime[nodeID]; ok && now.Unix()-at < int64(nodeStaleOnlineGrace/time.Second) {
		return false
	}
	return true
}
