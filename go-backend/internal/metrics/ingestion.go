package metrics

import (
	"context"
	"log"
	"sync"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

type SystemInfo struct {
	Uptime                 uint64  `json:"uptime"`
	BytesReceived          uint64  `json:"bytes_received"`
	BytesTransmitted       uint64  `json:"bytes_transmitted"`
	PeriodBytesReceived    uint64  `json:"period_bytes_received"`
	PeriodBytesTransmitted uint64  `json:"period_bytes_transmitted"`
	BaselineRecordedAt     int64   `json:"baseline_recorded_at"`
	NextResetAt            int64   `json:"next_reset_at"`
	RenewalCycle           string  `json:"renewal_cycle,omitempty"`
	CPUUsage               float64 `json:"cpu_usage"`
	MemoryUsage            float64 `json:"memory_usage"`
	DiskUsage              float64 `json:"disk_usage"`
	Load1                  float64 `json:"load1"`
	Load5                  float64 `json:"load5"`
	Load15                 float64 `json:"load15"`
	TCPConns               int64   `json:"tcp_conns"`
	UDPConns               int64   `json:"udp_conns"`
	NetInSpeed             int64   `json:"net_in_speed"`
	NetOutSpeed            int64   `json:"net_out_speed"`
}

type IngestionService struct {
	repo          *repo.Repository
	nodeBuffer    []*model.NodeMetric
	nodeBufferMu  sync.Mutex
	flushInterval time.Duration
	retentionDays int

	// node_metric tiering (plan 135), see repository_node_metric_compact.go.
	now           func() time.Time
	pause         time.Duration // between write batches, lets API writes in
	compactedUpTo int64         // ms; rows before it are compact
	catchUpLogged bool
	lastErrLog    time.Time
}

const (
	// The 15 min / 1 h charts read raw 1 s rows; keep the newest hour raw.
	nodeMetricRawKeep = time.Hour
	// Older rows become one row per node per 30 s (6 h / 24 h charts use 43 s /
	// 173 s buckets, so every bucket keeps data).
	nodeMetricBucket = 30 * time.Second
	// One transaction compacts 1 min of data (~1.7k rows for 29 nodes, ~15 ms
	// on the prod-size copy).
	nodeMetricSlice = time.Minute
	// Longest chart window (24 h) plus a margin.
	nodeMetricRetention    = 24*time.Hour + 30*time.Minute
	nodeMetricMaintainTick = 5 * time.Minute
	nodeMetricPruneBatch   = 2000
)

func NewIngestionService(repo *repo.Repository) *IngestionService {
	return &IngestionService{
		repo:          repo,
		nodeBuffer:    make([]*model.NodeMetric, 0, 500),
		flushInterval: 30 * time.Second,
		retentionDays: 1,
		pause:         50 * time.Millisecond,
	}
}

func (s *IngestionService) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *IngestionService) Start(ctx context.Context) {
	flushTicker := time.NewTicker(s.flushInterval)
	defer flushTicker.Stop()

	// node_metric compaction + retention: first pass a minute after start
	// (one-time catch-up after an upgrade), then every 5 minutes, in its own
	// goroutine so flushes never wait for it.
	go func() {
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				s.MaintainNodeMetrics(ctx)
				timer.Reset(nodeMetricMaintainTick)
			}
		}
	}()

	pruneTicker := time.NewTicker(1 * time.Hour)
	defer pruneTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.flushNodeMetrics()
			return
		case <-flushTicker.C:
			s.flushNodeMetrics()
		case <-pruneTicker.C:
			s.pruneMetrics()
		}
	}
}

func (s *IngestionService) RecordNodeMetric(nodeID int64, info SystemInfo) {
	m := &model.NodeMetric{
		NodeID:      nodeID,
		Timestamp:   time.Now().UnixMilli(),
		CPUUsage:    info.CPUUsage,
		MemUsage:    info.MemoryUsage,
		DiskUsage:   info.DiskUsage,
		NetInBytes:  int64(info.BytesReceived),
		NetOutBytes: int64(info.BytesTransmitted),
		NetInSpeed:  info.NetInSpeed,
		NetOutSpeed: info.NetOutSpeed,
		Load1:       info.Load1,
		Load5:       info.Load5,
		Load15:      info.Load15,
		TCPConns:    info.TCPConns,
		UDPConns:    info.UDPConns,
		Uptime:      int64(info.Uptime),
		PeriodRx:    int64(info.PeriodBytesReceived),
		PeriodTx:    int64(info.PeriodBytesTransmitted),
	}

	s.nodeBufferMu.Lock()
	s.nodeBuffer = append(s.nodeBuffer, m)
	shouldFlush := len(s.nodeBuffer) >= 200
	s.nodeBufferMu.Unlock()

	if shouldFlush {
		go s.flushNodeMetrics()
	}
}

func (s *IngestionService) flushNodeMetrics() {
	s.nodeBufferMu.Lock()
	if len(s.nodeBuffer) == 0 {
		s.nodeBufferMu.Unlock()
		return
	}
	buffer := s.nodeBuffer
	s.nodeBuffer = make([]*model.NodeMetric, 0, 500)
	s.nodeBufferMu.Unlock()

	if s.repo == nil {
		return
	}
	if err := s.repo.InsertNodeMetricBatch(buffer); err != nil {
		log.Printf("monitoring write failed op=node_metric.flush count=%d err=%v", len(buffer), err)
	}
}

// pruneMetrics trims the small per-minute tables hourly; node_metric has its
// own batched maintenance (MaintainNodeMetrics).
func (s *IngestionService) pruneMetrics() {
	cutoff := time.Now().Add(-time.Duration(s.retentionDays) * 24 * time.Hour).UnixMilli()
	if s.repo == nil {
		return
	}
	if err := s.repo.PruneTunnelMetrics(cutoff); err != nil {
		log.Printf("monitoring prune failed op=tunnel_metric cutoff=%d err=%v", cutoff, err)
	}
	if err := s.repo.PruneServiceMonitorResults(cutoff); err != nil {
		log.Printf("monitoring prune failed op=service_monitor_result cutoff=%d err=%v", cutoff, err)
	}
}

func (s *IngestionService) GetLatestMetric(nodeID int64) (*model.NodeMetric, error) {
	return s.repo.GetLatestNodeMetric(nodeID)
}

func (s *IngestionService) GetMetrics(nodeID int64, startMs, endMs int64) ([]model.NodeMetric, error) {
	return s.repo.GetNodeMetrics(nodeID, startMs, endMs)
}

func alignDown(ms, step int64) int64 {
	if step <= 0 {
		return ms
	}
	return ms - ((ms%step)+step)%step
}

func (s *IngestionService) sleep(ctx context.Context) bool {
	if s.pause <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(s.pause)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *IngestionService) logMaintainErr(op string, err error) {
	now := s.clock()
	if !s.lastErrLog.IsZero() && now.Sub(s.lastErrLog) < time.Hour {
		return
	}
	s.lastErrLog = now
	log.Printf("monitoring maintenance failed op=%s err=%v (logged at most hourly)", op, err)
}

// MaintainNodeMetrics drops node_metric rows past retention and compacts rows
// older than an hour into 30 s averages. Every write is a short transaction
// (<= 2000 deleted rows or one 1 min slice) followed by a pause, so API
// requests never wait long behind it. Returns rows deleted (pruned+compacted).
func (s *IngestionService) MaintainNodeMetrics(ctx context.Context) int64 {
	if s.repo == nil {
		return 0
	}
	now := s.clock()
	started := time.Now()
	cutoff := now.Add(-nodeMetricRetention).UnixMilli()
	var pruned, compacted int64
	for ctx.Err() == nil {
		n, err := s.repo.PruneNodeMetricsBatch(cutoff, nodeMetricPruneBatch)
		if err != nil {
			s.logMaintainErr("node_metric.prune", err)
			break
		}
		pruned += n
		if n < nodeMetricPruneBatch || !s.sleep(ctx) {
			break
		}
	}

	slice := nodeMetricSlice.Milliseconds()
	end := alignDown(now.Add(-nodeMetricRawKeep).UnixMilli(), slice)
	start := s.compactedUpTo
	if start == 0 {
		oldest, err := s.repo.OldestNodeMetricTimestamp()
		if err != nil {
			s.logMaintainErr("node_metric.oldest", err)
			return pruned
		}
		if oldest == 0 {
			s.compactedUpTo = end
			return pruned
		}
		if oldest < cutoff {
			oldest = cutoff
		}
		start = alignDown(oldest, slice)
	}
	for from := start; from+slice <= end; from += slice {
		if ctx.Err() != nil {
			break
		}
		n, err := s.repo.CompactNodeMetrics(from, from+slice, nodeMetricBucket.Milliseconds())
		if err != nil {
			s.logMaintainErr("node_metric.compact", err)
			break
		}
		s.compactedUpTo = from + slice
		compacted += n
		if n > 0 && !s.sleep(ctx) {
			break
		}
	}
	// One line for the first (catch-up) pass only; routine passes stay quiet.
	if !s.catchUpLogged && (pruned > 0 || compacted > 0) {
		s.catchUpLogged = true
		log.Printf("node_metric maintenance: pruned=%d compacted_raw_rows=%d took=%s (raw 1 s rows kept for 1 h, then 30 s averages, 24.5 h retention)",
			pruned, compacted, time.Since(started).Round(time.Millisecond))
	}
	return pruned + compacted
}
