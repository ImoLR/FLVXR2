// Package machineprotect implements the whole-machine connection count and the
// per-machine connection protection of an FLVX node (plan 118):
//
//   - 整机连接数: every row of /proc/net/{tcp,tcp6,udp,udp6} (all states), the same
//     number the mmwx Helper shows on its server card (readConnections()).
//   - 整机连接上限: while the count is at or above the cap, NEW TCP/UDP connections to
//     FLVX business ports are dropped (own nft table, prerouting −150); existing
//     connections are untouched. Released below 95 % of the cap.
//   - TIME_WAIT 上限: the host's net.ipv4.tcp_max_tw_buckets (original value saved and
//     restored).
//   - CLOSE_WAIT 超时: handed to the native landing Core of this node.
//
// One owner per machine: when the mmwx Helper runs on the machine it owns the
// protection and this agent only reports and hands over its business ports.
package machineprotect

import (
	"fmt"
	"time"
)

// User-approved defaults (2026-10-20): cap 1000 (first approved as 1500), CLOSE_WAIT
// 10 s (first approved as 30 s; not 5 s, a CLOSE_WAIT socket may still be flushing
// the tail of a download to a slow client), TIME_WAIT 500.
const (
	DefaultMaxTotal         int64 = 1000
	DefaultTWBuckets        int64 = 500
	DefaultCloseWaitSeconds int64 = 10

	// Blocking starts at total >= cap and ends when total < cap*ReleasePercent/100.
	ReleasePercent int64 = 95

	// SampleInterval is the counting / reconcile period of the single protection loop.
	SampleInterval = 5 * time.Second
)

// Value ranges accepted from the panel; 0 always means "not set".
const (
	MinMaxTotal         int64 = 100
	MaxMaxTotal         int64 = 1_000_000
	MinTWBuckets        int64 = 100
	MaxTWBuckets        int64 = 2_000_000
	MinCloseWaitSeconds int64 = 1
	MaxCloseWaitSeconds int64 = 3600
)

// Owners of a machine's protection.
const (
	OwnerFLVX = "flvx"
	OwnerMmwx = "mmwx"
)

// MmwxStatus values qualify owner=mmwx in reports.
const (
	MmwxStatusOK      = "ok"      // fresh status file mirrored
	MmwxStatusStale   = "stale"   // status file older than 60 s
	MmwxStatusMissing = "missing" // Helper runs but writes no status file (Helper too old)
)

// Settings is what the panel pushes with SetMachineProtection and what the agent
// persists in config.json under "machine_protection". A zero number disables that
// part: no cap / kernel TIME_WAIT value left alone / Core default CLOSE_WAIT.
type Settings struct {
	Enabled          bool  `json:"enabled"`
	MaxTotal         int64 `json:"max_total"`
	TWBuckets        int64 `json:"tw_buckets"`
	CloseWaitSeconds int64 `json:"close_wait_seconds"`
}

// DefaultSettings are the panel defaults; the agent never applies them on its own
// (protection starts only after the panel pushed settings once).
func DefaultSettings() Settings {
	return Settings{Enabled: true, MaxTotal: DefaultMaxTotal, TWBuckets: DefaultTWBuckets, CloseWaitSeconds: DefaultCloseWaitSeconds}
}

// Validate checks the ranges shared with the panel.
func (s Settings) Validate() error {
	if s.MaxTotal != 0 && (s.MaxTotal < MinMaxTotal || s.MaxTotal > MaxMaxTotal) {
		return fmt.Errorf("整机连接上限须为 0 或 %d–%d", MinMaxTotal, MaxMaxTotal)
	}
	if s.TWBuckets != 0 && (s.TWBuckets < MinTWBuckets || s.TWBuckets > MaxTWBuckets) {
		return fmt.Errorf("TIME_WAIT 上限须为 0 或 %d–%d", MinTWBuckets, MaxTWBuckets)
	}
	if s.CloseWaitSeconds != 0 && (s.CloseWaitSeconds < MinCloseWaitSeconds || s.CloseWaitSeconds > MaxCloseWaitSeconds) {
		return fmt.Errorf("CLOSE_WAIT 超时须为 0 或 %d–%d 秒", MinCloseWaitSeconds, MaxCloseWaitSeconds)
	}
	return nil
}

// Conns is the whole-machine connection count reported as machine_conns.
// Total = TCP + UDP rows of /proc/net (every state); the TCP state breakdown comes
// from the same pass: OtherTCP = TCP − Established − TimeWait − CloseWait.
type Conns struct {
	Total       int64 `json:"total"`
	TCP         int64 `json:"tcp"`
	UDP         int64 `json:"udp"`
	Established int64 `json:"established"`
	TimeWait    int64 `json:"time_wait"`
	CloseWait   int64 `json:"close_wait"`
	OtherTCP    int64 `json:"other_tcp"`
}

// Protect is the protection state reported as machine_protect. Nil numbers mean
// "not set"; an absent machine_protect (old agent) means unknown.
type Protect struct {
	Owner            string `json:"owner"`
	Enabled          bool   `json:"enabled"`
	MaxTotal         *int64 `json:"max_total,omitempty"`
	TWBuckets        *int64 `json:"tw_buckets,omitempty"`
	CloseWaitSeconds *int64 `json:"close_wait_seconds,omitempty"`
	Blocking         bool   `json:"blocking"`
	DroppedTotal     int64  `json:"dropped_total"`
	// Ports is the number of protected business port entries (TCP + UDP).
	Ports      int    `json:"ports,omitempty"`
	Error      string `json:"error,omitempty"`
	MmwxStatus string `json:"mmwx_status,omitempty"`
}

// Snapshot is one 5 s sample of the protection loop.
type Snapshot struct {
	Seq     uint64
	At      time.Time
	Conns   *Conns
	Protect Protect
}

func positive(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}

// ReleaseLevel is the total below which blocking ends (hysteresis).
func ReleaseLevel(maxTotal int64) int64 {
	return maxTotal * ReleasePercent / 100
}

// NextBlocking applies the hysteresis: block at total >= cap, keep blocking until
// total < ReleaseLevel(cap).
func NextBlocking(prev bool, total, maxTotal int64) bool {
	if maxTotal <= 0 {
		return false
	}
	if total >= maxTotal {
		return true
	}
	return prev && total >= ReleaseLevel(maxTotal)
}
