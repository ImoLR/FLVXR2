package ws

import (
	"encoding/json"
	"go-backend/internal/store/model"
	"log"
	"sort"
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
}
