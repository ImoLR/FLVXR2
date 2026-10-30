package socket

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/go-gost/x/landing"
)

// Plan 168: entry speed limits shaped in the kernel (landing package, shared
// with the per-user landing 速率).
//
// Handshake: entryRateTc=1 tells the panel this agent takes SetEntryRates
// and no longer needs gost limiters / nftables policers for those limits;
// entryRateTcErr=<reason> = the kernel cannot (one probe per process), so the
// panel keeps the old path. Node 36 never runs the landing manager and never
// advertises it.

var (
	entryRateProbeOnce   sync.Once
	entryRateProbeReason string
	entryRateProbe       = landing.ProbeEntryRate // tests replace it
)

func entryRateProbeResult() string {
	entryRateProbeOnce.Do(func() {
		// Kill switch per machine (systemd Environment=): the panel then
		// keeps the gost / nftables path for this node.
		if strings.EqualFold(strings.TrimSpace(os.Getenv("FLVX_ENTRY_RATE_TC")), "off") {
			entryRateProbeReason = "已在本机关闭（FLVX_ENTRY_RATE_TC=off）"
			return
		}
		entryRateProbeReason = entryRateProbe()
	})
	return entryRateProbeReason
}

// entryRateQuery is the handshake part (appended to buildWebSocketQuery).
func entryRateQuery(nodeID int64) string {
	if nodeID == 36 {
		return ""
	}
	q := "&entryRateTc=" + strconv.Itoa(landing.EntryRateVersion)
	if reason := entryRateProbeResult(); reason != "" {
		r := []rune(reason)
		if len(r) > 120 {
			reason = string(r[:120])
		}
		q += "&entryRateTcErr=" + url.QueryEscape(reason)
	}
	return q
}

func (w *WebSocketReporter) handleSetEntryRates(data interface{}) (landing.EntryRateStatus, error) {
	if w.nodeID == 36 {
		return landing.EntryRateStatus{}, fmt.Errorf("entry rates are not supported on this node")
	}
	if reason := entryRateProbeResult(); reason != "" {
		return landing.EntryRateStatus{State: "error", Error: reason}, fmt.Errorf("内核不支持 tc 限速：%s", reason)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return landing.EntryRateStatus{}, err
	}
	var r landing.EntryRates
	if err := json.Unmarshal(raw, &r); err != nil {
		return landing.EntryRateStatus{}, fmt.Errorf("invalid entry rates")
	}
	return landing.Default().ApplyEntryRates(r)
}

// entryRateReport returns the entry status for the 1 s metric message only
// when it changed since the last one sent on this connection.
func (w *WebSocketReporter) entryRateReport() *landing.EntryRateStatus {
	if w.nodeID == 36 {
		return nil
	}
	st, seq := landing.Default().EntryRateReport()
	if seq == 0 || seq == w.entryRateSentSeq {
		return nil
	}
	w.entryRateSentSeq = seq
	return &st
}
