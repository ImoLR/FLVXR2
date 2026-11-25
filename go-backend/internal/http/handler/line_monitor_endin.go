package handler

// Plan 310 r3: the 线路监测 line kind 「利群专线端内」 (model.LineKindLeiKwanEndIn).
// Passive: no TcpPing — the per-minute 利群 pass (leikwan_land.go) gives one
// sample a minute = 端内 (全程 UDP − 入口段) of the line's 专线, read from the
// 利群入口 reports; its parts (全程, 入口段, which 入口机) go to
// line_monitor_part. A minute without a reading is 无数据 (a gap), never 断流.

import (
	"fmt"
	"math"
	"strings"

	"go-backend/internal/linemon"
	"go-backend/internal/store/model"
)

// lineMonitorPassive buffers one 端内 sample (written by the next flush).
func (h *Handler) lineMonitorPassive(lineID, ts int64, c lkEndInCand) {
	rt := h.lineMon()
	endIn := c.endIn()
	ms10 := int(math.Round(endIn * 10))
	s := model.LineMonitorSample{LineID: lineID, TS: ts, Status: model.LineSampleOK, Ms10: ms10,
		Part: &model.LineMonitorPart{Total10: int(math.Round(c.TotalMs * 10)), Seg10: int(math.Round(c.SegMs * 10)), ViaNode: c.NodeID}}
	rt.mu.Lock()
	rt.live[lineID] = lineMonitorLive{TS: ts, Status: linemon.StatusOK, Ms: linemon.Float(float64(ms10) / 10)}
	rt.buffer = append(rt.buffer, s)
	if len(rt.buffer) > lineMonitorBufferCap {
		rt.buffer = rt.buffer[len(rt.buffer)-lineMonitorBufferCap:]
	}
	rt.mu.Unlock()
}

// lineMonitorPassiveGap: no reading this minute (live 无数据 with the reason).
func (h *Handler) lineMonitorPassiveGap(lineID, ts int64, why string) {
	rt := h.lineMon()
	rt.mu.Lock()
	rt.live[lineID] = lineMonitorLive{TS: ts, Status: 0, Ms: linemon.Float(math.NaN()), Reason: why}
	rt.mu.Unlock()
}

// lineMonitorEndInLines: the 专线 (id → line) and the 利群入口 nodes (id →
// name) an 端内 line may name.
func (h *Handler) lineMonitorEndInRefs() (map[int64]model.LeiKwanLine, map[int64]leiKwanEntryNode) {
	lines := map[int64]model.LeiKwanLine{}
	if ls, err := h.repo.ListLeiKwanLines(); err == nil {
		for _, l := range ls {
			lines[l.ID] = l
		}
	}
	entries := map[int64]leiKwanEntryNode{}
	if setting, err := h.repo.GetLeiKwanSetting(); err == nil && setting != nil {
		for _, e := range h.leiKwanEntryNodes(setting.EntryGroupID) {
			entries[e.ID] = e
		}
	}
	return lines, entries
}

// validateLineMonitorEndIn checks one 「利群专线端内」 line of a chart save.
func validateLineMonitorEndIn(n int, l lineMonitorLineInput, lines map[int64]model.LeiKwanLine, entries map[int64]leiKwanEntryNode) (model.LineMonitorLine, string, error) {
	if _, ok := lines[l.LeiKwanLineID]; !ok || l.LeiKwanLineID <= 0 {
		return model.LineMonitorLine{}, "", fmt.Errorf("第 %d 条线路：请选择利群专线", n)
	}
	if l.SourceNodeID > 0 {
		if _, ok := entries[l.SourceNodeID]; !ok {
			return model.LineMonitorLine{}, "", fmt.Errorf("第 %d 条线路：入口机要是利群入口节点（或选自动）", n)
		}
	}
	label := strings.TrimSpace(l.Label)
	if len([]rune(label)) > 50 {
		return model.LineMonitorLine{}, "", fmt.Errorf("第 %d 条线路：备注不能超过 50 个字符", n)
	}
	paused := 0
	if l.Paused {
		paused = 1
	}
	key := fmt.Sprintf("lk|%d|%d", l.LeiKwanLineID, l.SourceNodeID)
	return model.LineMonitorLine{ID: l.ID, Kind: model.LineKindLeiKwanEndIn, LeiKwanLineID: l.LeiKwanLineID, SourceNodeID: l.SourceNodeID,
		Label: label, Paused: paused}, key, nil
}

// lineMonitorChartSlot: the bucket floor of a chart (60 s when it has an 端内
// line, so every bucket can hold one of its samples).
func lineMonitorChartSlot(lines []model.LineMonitorLine) int64 {
	for _, l := range lines {
		if l.Kind == model.LineKindLeiKwanEndIn {
			return model.LineEndInSlotSeconds
		}
	}
	return linemon.SlotSeconds
}

func lineMonitorLineSlot(l model.LineMonitorLine) int64 {
	if l.Kind == model.LineKindLeiKwanEndIn {
		return model.LineEndInSlotSeconds
	}
	return linemon.SlotSeconds
}
