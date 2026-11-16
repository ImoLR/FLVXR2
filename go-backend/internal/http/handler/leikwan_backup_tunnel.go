package handler

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/probe"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 268: one 备线隧道 per 利群专线, picked by the admin from any tunnel.
// The backup always goes via the 前置 (the user's 主入口 machine): its 日志快切
// redirects to the backup tunnel's entry, so the user's address never changes.
// The picker shows that path in one line and, when the panel's own path
// rounds already measured it, the 前置 → backup-entry latency. No new probes.

// leiKwanFrontFarMs: above this the picker warns (amber) that the backup's
// entry is far from the 前置 (that hop is added to every switched connection).
const leiKwanFrontFarMs = 30

// leiKwanFrontLatencyMaxAge: path rounds run every 60 s; older data is stale.
const leiKwanFrontLatencyMaxAge = 5 * time.Minute

// leiKwanLabEdges is set only by the leikwanlab build tag (nil in release).
var leiKwanLabEdges func() []probe.Edge

type leiKwanFront struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Grants int    `json:"grants"`
}

type leiKwanFrontLatency struct {
	FrontID   int64   `json:"frontId"`
	EntryID   int64   `json:"entryId"`
	EntryName string  `json:"entryName"`
	MS        float64 `json:"ms"`
	OK        bool    `json:"ok"`
	Same      bool    `json:"same,omitempty"`
	Far       bool    `json:"far,omitempty"`
}

type leiKwanBackupTunnelView struct {
	repo.LeiKwanTunnelBrief
	FrontLatency []leiKwanFrontLatency `json:"frontLatency"`
}

// leiKwanFronts: the 利群入口 machines that are some user's 主入口 (the 前置
// a backup really starts from); none granted yet → every node of the group.
func (h *Handler) leiKwanFronts(entries []leiKwanEntryNode) []leiKwanFront {
	used, _ := h.repo.CountLeiKwanGrantsByEntry()
	out := []leiKwanFront{}
	for _, e := range entries {
		if used[e.ID] > 0 {
			out = append(out, leiKwanFront{ID: e.ID, Name: e.Name, Grants: used[e.ID]})
		}
	}
	if len(out) == 0 {
		for _, e := range entries {
			out = append(out, leiKwanFront{ID: e.ID, Name: e.Name})
		}
	}
	return out
}

// leiKwanBackupTunnelViews adds the 前置 → entry latency the panel already
// has (probe latency book: node→node edges of the 60 s path rounds, either
// direction) to each tunnel.
func (h *Handler) leiKwanBackupTunnelViews(tunnels []repo.LeiKwanTunnelBrief, fronts []leiKwanFront) []leiKwanBackupTunnelView {
	if leiKwanLabEdges != nil {
		// Lab builds only (build tag leikwanlab): the lab has no agents.
		h.probeInit().latency.Record(leiKwanLabEdges(), time.Now())
	}
	edges := h.probeInit().latency.Latest(time.Now(), leiKwanFrontLatencyMaxAge)
	lookup := func(from, to int64) (float64, bool, bool) {
		for _, e := range edges[from] {
			if e.To == to {
				return e.MS, e.OK, true
			}
		}
		return 0, false, false
	}
	out := make([]leiKwanBackupTunnelView, 0, len(tunnels))
	for _, t := range tunnels {
		v := leiKwanBackupTunnelView{LeiKwanTunnelBrief: t, FrontLatency: []leiKwanFrontLatency{}}
		names := splitNames(t.EntryNames)
		for _, f := range fronts {
			for i, entryID := range t.EntryNodeIDs {
				name := ""
				if i < len(names) {
					name = names[i]
				}
				if entryID == f.ID {
					v.FrontLatency = append(v.FrontLatency, leiKwanFrontLatency{FrontID: f.ID, EntryID: entryID, EntryName: name, OK: true, Same: true})
					continue
				}
				ms, ok, found := lookup(f.ID, entryID)
				if !found {
					ms, ok, found = lookup(entryID, f.ID)
				}
				if !found {
					continue
				}
				v.FrontLatency = append(v.FrontLatency, leiKwanFrontLatency{FrontID: f.ID, EntryID: entryID, EntryName: name, MS: ms, OK: ok, Far: !ok || ms > leiKwanFrontFarMs})
			}
		}
		out = append(out, v)
	}
	return out
}

func splitNames(joined string) []string {
	out := []string{}
	cur := []rune{}
	for _, r := range joined {
		if r == '、' {
			out = append(out, string(cur))
			cur = cur[:0]
			continue
		}
		cur = append(cur, r)
	}
	if joined != "" {
		out = append(out, string(cur))
	}
	return out
}

// leiKwanBackupPlan is what 「按新备线重建」 would do for one rule.
type leiKwanBackupPlan int

const (
	leiKwanBackupKeep   leiKwanBackupPlan = iota // auto backup already on the line's tunnel (or nothing wanted)
	leiKwanBackupLinked                          // imported backup: never moved
	leiKwanBackupMove                            // auto backup on another tunnel → rebuild on the line's
	leiKwanBackupCreate                          // backup wanted but missing → create
	leiKwanBackupRemove                          // line has no backup tunnel any more → drop the auto one
)

func leiKwanPlanBackup(x model.LeiKwanRule, line model.LeiKwanLine, grantOn bool, backups map[int64]repo.LeiKwanBackupForward) leiKwanBackupPlan {
	if x.BackupMode == "linked" {
		return leiKwanBackupLinked
	}
	want := grantOn && line.BackupTunnelID > 0
	if x.BackupForwardID <= 0 {
		if want {
			return leiKwanBackupCreate
		}
		return leiKwanBackupKeep
	}
	if x.BackupMode != "auto" {
		return leiKwanBackupKeep
	}
	if !want {
		if line.BackupTunnelID <= 0 {
			return leiKwanBackupRemove
		}
		// The grant has 备线 off: the grant switch owns that, not this action.
		return leiKwanBackupKeep
	}
	if b, ok := backups[x.BackupForwardID]; ok && b.TunnelID == line.BackupTunnelID {
		return leiKwanBackupKeep
	}
	return leiKwanBackupMove
}

// leiKwanLineBackupStats counts per line what the rebuild would change.
func (h *Handler) leiKwanLineBackupStats(lines []model.LeiKwanLine, rules []model.LeiKwanRule) map[int64]map[string]int {
	grants, _ := h.repo.ListLeiKwanGrants()
	grantOn := map[int64]bool{}
	for _, g := range grants {
		grantOn[g.UserID] = g.BackupLine == 1
	}
	ids := []int64{}
	for _, x := range rules {
		if x.BackupMode == "auto" && x.BackupForwardID > 0 {
			ids = append(ids, x.BackupForwardID)
		}
	}
	backups, _ := h.repo.LeiKwanBackupForwards(ids)
	byID := map[int64]model.LeiKwanLine{}
	out := map[int64]map[string]int{}
	for _, l := range lines {
		byID[l.ID] = l
		out[l.ID] = map[string]int{"auto": 0, "linked": 0, "stale": 0}
	}
	for _, x := range rules {
		l, ok := byID[x.LineID]
		if !ok {
			continue
		}
		st := out[l.ID]
		switch x.BackupMode {
		case "auto":
			st["auto"]++
		case "linked":
			st["linked"]++
		}
		switch leiKwanPlanBackup(x, l, grantOn[x.UserID], backups) {
		case leiKwanBackupMove, leiKwanBackupCreate, leiKwanBackupRemove:
			st["stale"]++
		}
	}
	return out
}

// leikwanLineBackupRebuild: 「按新备线重建」 for one line (admin, confirmed in
// the UI). Auto backups that are not on the line's current 备线隧道 are
// rebuilt there (new one first, then the old one is deleted, so a failure
// keeps the old backup); missing auto backups are created; linked (imported)
// backups are never touched.
func (h *Handler) leikwanLineBackupRebuild(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LineID int64 `json:"lineId"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	leiKwanRuleMu.Lock()
	defer leiKwanRuleMu.Unlock()
	line, err := h.repo.GetLeiKwanLine(req.LineID)
	if err != nil || line == nil {
		leiKwanError(w, errors.New("利群专线不存在"))
		return
	}
	all, err := h.repo.ListLeiKwanRules(0)
	if err != nil {
		leiKwanError(w, errors.New("读取利群规则失败"))
		return
	}
	rules := []model.LeiKwanRule{}
	ids := []int64{}
	for _, x := range all {
		if x.LineID == line.ID {
			rules = append(rules, x)
			if x.BackupMode == "auto" && x.BackupForwardID > 0 {
				ids = append(ids, x.BackupForwardID)
			}
		}
	}
	backups, err := h.repo.LeiKwanBackupForwards(ids)
	if err != nil {
		leiKwanError(w, errors.New("读取备线规则失败"))
		return
	}
	grants, _ := h.repo.ListLeiKwanGrants()
	grantOn := map[int64]bool{}
	for _, g := range grants {
		grantOn[g.UserID] = g.BackupLine == 1
	}
	res := map[string]int{"rebuilt": 0, "created": 0, "removed": 0, "unchanged": 0, "linked": 0}
	warnings := []string{}
	for i := range rules {
		x := rules[i]
		switch leiKwanPlanBackup(x, *line, grantOn[x.UserID], backups) {
		case leiKwanBackupLinked:
			res["linked"]++
		case leiKwanBackupKeep:
			res["unchanged"]++
		case leiKwanBackupCreate:
			if err := h.leiKwanCreateBackup(&x); err != nil {
				x.BackupError = err.Error()
				warnings = append(warnings, fmt.Sprintf("%s：备线未建好（%s）", x.Name, err.Error()))
			} else {
				res["created"]++
			}
			_ = h.repo.SaveLeiKwanRule(&x)
		case leiKwanBackupRemove:
			if err := h.leiKwanDropBackup(&x); err != nil {
				warnings = append(warnings, fmt.Sprintf("%s：%s", x.Name, err.Error()))
				continue
			}
			res["removed"]++
			_ = h.repo.SaveLeiKwanRule(&x)
		case leiKwanBackupMove:
			old := x
			fresh := x
			fresh.BackupForwardID, fresh.BackupMode, fresh.BackupError = 0, "", ""
			if err := h.leiKwanCreateBackup(&fresh); err != nil {
				// Keep the old backup working; report only.
				warnings = append(warnings, fmt.Sprintf("%s：新备线未建好，旧备线保留（%s）", x.Name, err.Error()))
				continue
			}
			if err := h.leiKwanDropBackup(&old); err != nil {
				warnings = append(warnings, fmt.Sprintf("%s：新备线已建好，旧备线 #%d 未删掉（%s）", x.Name, old.BackupForwardID, err.Error()))
			}
			_ = h.repo.SaveLeiKwanRule(&fresh)
			res["rebuilt"]++
		}
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"result": res, "warnings": warnings}))
}

// leiKwanUserRuleCount (plan 268): the user's 利群 rules, which count toward
// 总规则数 — the dashboard adds them to 已用规则.
func (h *Handler) leiKwanUserRuleCount(userID int64) int64 {
	n, err := h.repo.CountLeiKwanRules(userID)
	if err != nil {
		return 0
	}
	return n
}
