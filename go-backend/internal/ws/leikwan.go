package ws

import (
	"encoding/json"
	"go-backend/internal/store/model"
	"log"
	"sort"
	"strconv"
	"sync"
	"time"
)

type leiKwanReport struct {
	Status   string                         `json:"status"`
	Forwards []model.LeiKwanDetectedForward `json:"forwards"`
}

func (s *Server) recordLeiKwan(ns *nodeSession, report *leiKwanReport) {
	// nil is an old agent: do not overwrite an existing observation.
	if report == nil || (report.Status != "absent" && report.Status != "present" && report.Status != "running") || len(report.Forwards) > 512 {
		return
	}
	forwards := make([]model.LeiKwanDetectedForward, 0, len(report.Forwards))
	seen := map[int64]bool{}
	if report.Status != "absent" {
		for _, f := range report.Forwards {
			if f.ForwardID <= 0 || f.ListenPort < 1 || f.ListenPort > 65535 || seen[f.ForwardID] {
				continue
			}
			seen[f.ForwardID] = true
			forwards = append(forwards, f)
		}
	}
	sort.Slice(forwards, func(i, j int) bool { return forwards[i].ForwardID < forwards[j].ForwardID })
	raw, _ := json.Marshal(forwards)
	data := string(raw)
	if ns.leikwanStatus == report.Status && ns.leikwanForwards == data {
		return
	}
	if err := s.repo.UpdateNodeLeiKwan(ns.nodeID, report.Status, data, time.Now().UnixMilli()); err != nil {
		log.Printf("node %d LeiKwan detection persistence failed", ns.nodeID)
		return
	}
	ns.leikwanStatus, ns.leikwanForwards = report.Status, data
	s.logLeiKwanPortConflicts(ns.nodeID, forwards)
}

// Plan 265: when the LeiKwan client listens on a port the panel already uses
// on that machine (e.g. LeiKwan added a forward on an FLVX rule's port),
// nothing is moved; the admin is told once per node/port per window.
const leiKwanConflictLogEvery = 6 * time.Hour

var leiKwanConflictLog = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

func (s *Server) logLeiKwanPortConflicts(nodeID int64, forwards []model.LeiKwanDetectedForward) {
	if s == nil || s.repo == nil || len(forwards) == 0 {
		return
	}
	ports := make([]int, 0, len(forwards))
	seen := map[int]bool{}
	for _, f := range forwards {
		if !seen[f.ListenPort] {
			seen[f.ListenPort] = true
			ports = append(ports, f.ListenPort)
		}
	}
	held, err := s.repo.LeiKwanPortHolders(nodeID, ports)
	if err != nil || len(held) == 0 {
		return
	}
	for _, p := range leiKwanConflictsToLog(nodeID, held, time.Now()) {
		log.Printf("node %d: LeiKwan client listens on port %d, which the panel already uses on this node; nothing was moved — change that rule/tunnel port", nodeID, p)
	}
}

// leiKwanConflictsToLog: the held ports not logged for this node within the
// window (the map only keeps entries younger than the window).
func leiKwanConflictsToLog(nodeID int64, held []int, now time.Time) []int {
	leiKwanConflictLog.Lock()
	defer leiKwanConflictLog.Unlock()
	for k, at := range leiKwanConflictLog.at {
		if now.Sub(at) >= leiKwanConflictLogEvery {
			delete(leiKwanConflictLog.at, k)
		}
	}
	var out []int
	for _, p := range held {
		key := strconv.FormatInt(nodeID, 10) + ":" + strconv.Itoa(p)
		if _, ok := leiKwanConflictLog.at[key]; ok {
			continue
		}
		leiKwanConflictLog.at[key] = now
		out = append(out, p)
	}
	return out
}
