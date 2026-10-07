package handler

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"go-backend/internal/store/model"
)

const (
	tunnelQualityProbeInterval = 10 * time.Second
	tunnelQualityProbeTimeout  = 8 * time.Second
	tunnelQualityPingTimeoutMs = 5000
	tunnelQualityRetention     = 24 * time.Hour // keep 24h of history
	tunnelQualityPruneInterval = 10 * time.Minute
)

// tunnelQualitySnapshot is the in-memory latest probe result for a tunnel.
type tunnelQualitySnapshot struct {
	TunnelID           int64   `json:"tunnelId"`
	EntryToExitLatency float64 `json:"entryToExitLatency"`
	ExitToBingLatency  float64 `json:"exitToBingLatency"`
	EntryToExitLoss    float64 `json:"entryToExitLoss"`
	ExitToBingLoss     float64 `json:"exitToBingLoss"`
	Success            bool    `json:"success"`
	ErrorMessage       string  `json:"errorMessage,omitempty"`
	Timestamp          int64   `json:"timestamp"`
	PathLatency        float64 `json:"-"`
	PathLoss           float64 `json:"-"`
	PathStatus         string  `json:"-"`
	PathUpdatedAt      int64   `json:"-"`
}

// tunnelQualityProber runs periodic TCP ping probes against all enabled tunnels.
// Design mirrors health.Checker: background goroutine with worker pool + scheduled cleanup.
type tunnelQualityProber struct {
	handler   *Handler
	cache     sync.Map // tunnelID (int64) → *tunnelQualitySnapshot
	ctx       context.Context
	cancel    context.CancelFunc
	interval  time.Duration
	lastPrune int64
}

// newTunnelQualityProber creates a new prober (not yet running).
func newTunnelQualityProber(h *Handler) *tunnelQualityProber {
	ctx, cancel := context.WithCancel(context.Background())
	return &tunnelQualityProber{
		handler:  h,
		ctx:      ctx,
		cancel:   cancel,
		interval: tunnelQualityProbeInterval,
	}
}

// Start launches the background probe loop (call from jobs.go).
func (p *tunnelQualityProber) Start(ctx context.Context) {
	// Use the provided context so we stop with other background jobs.
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.loop()
}

// Stop halts the background probe loop.
func (p *tunnelQualityProber) Stop() {
	p.cancel()
}

// GetAll returns all cached quality snapshots (latest per tunnel).
func (p *tunnelQualityProber) GetAll() []tunnelQualitySnapshot {
	var items []tunnelQualitySnapshot
	p.cache.Range(func(_, value interface{}) bool {
		if snap, ok := value.(*tunnelQualitySnapshot); ok {
			items = append(items, *snap)
		}
		return true
	})
	return items
}

func (p *tunnelQualityProber) loop() {
	// Initial delay to let the system boot up
	select {
	case <-time.After(5 * time.Second):
	case <-p.ctx.Done():
		return
	}

	// Run once immediately
	p.probeAll()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.probeAll()
			p.maybePrune()
		}
	}
}

// maybePrune deletes old quality rows periodically (mirrors PruneServiceMonitorResults).
func (p *tunnelQualityProber) maybePrune() {
	now := time.Now().UnixMilli()
	if p.lastPrune > 0 && now-p.lastPrune < int64(tunnelQualityPruneInterval/time.Millisecond) {
		return
	}
	p.lastPrune = now

	h := p.handler
	if h == nil || h.repo == nil {
		return
	}

	cutoff := now - int64(tunnelQualityRetention/time.Millisecond)
	if err := h.repo.PruneTunnelQualityResults(cutoff); err != nil {
		log.Printf("tunnel_quality_prober: prune err=%v", err)
	}
}

func (p *tunnelQualityProber) probeAll() {
	h := p.handler
	if h == nil || h.repo == nil {
		return
	}

	tunnelIDs, err := h.repo.ListEnabledTunnelIDs()
	if err != nil {
		log.Printf("tunnel_quality_prober: list enabled tunnels err=%v", err)
		return
	}
	if len(tunnelIDs) == 0 {
		return
	}
	started := time.Now()
	defer func() {
		if elapsed := time.Since(started); elapsed > p.interval {
			log.Printf("tunnel_quality_prober: round took %s (interval %s, tunnels %d)", elapsed.Round(time.Millisecond), p.interval, len(tunnelIDs))
		}
	}()
	round := newTunnelQualityProbeRound(p.tcpPingNode)
	round.connected = h.wsServer.IsNodeConnected
	lookup := p.roundNodeLookup()
	plans := make([]tunnelQualityTunnelPlan, 0, len(tunnelIDs))
	for _, tunnelID := range tunnelIDs {
		if p.ctx.Err() != nil {
			return
		}
		plans = append(plans, p.planTunnel(tunnelID, round, lookup))
	}

	round.run(p.ctx)
	if p.ctx.Err() != nil {
		return
	}
	for _, plan := range plans {
		p.storeResult(plan.snapshot(round))
	}
}

// Cached records are immutable for this round. Local nodes must also have an
// active websocket; remote nodes use the status supplied by federation.
func (p *tunnelQualityProber) roundNodeLookup() func(int64) (*nodeRecord, error) {
	type result struct {
		node *nodeRecord
		err  error
	}
	cache := make(map[int64]result)
	return func(id int64) (*nodeRecord, error) {
		if cached, ok := cache[id]; ok {
			return cached.node, cached.err
		}
		node, err := p.handler.getNodeRecord(id)
		if node != nil {
			copy := *node
			if !p.nodeOnline(node) {
				copy.Status = 0
			}
			node = &copy
		}
		cache[id] = result{node, err}
		return node, err
	}
}

func (p *tunnelQualityProber) nodeOnline(node *nodeRecord) bool {
	return node != nil && node.Status == 1 && (node.IsRemote == 1 || p.handler.wsServer.IsNodeConnected(node.ID))
}

type tunnelQualityTunnelPlan struct {
	base         tunnelQualitySnapshot
	tunnelType   int
	direct       *tunnelQualityPingKey
	directErr    error
	publicNodeID int64
	hasPublic    bool
	publicErr    error
	path         *tunnelPathPlan
}

func tunnelQualityProbeOptions() diagnosisExecOptions {
	return diagnosisExecOptions{
		commandTimeout: tunnelQualityProbeTimeout,
		pingTimeoutMS:  tunnelQualityPingTimeoutMs,
		timeoutMessage: "探测超时",
	}
}

// Keep legacy entry/exit measurements on the first configured nodes. The path
// graph is separate: its minimum must never replace monitor/history values.
func (p *tunnelQualityProber) planTunnel(tunnelID int64, round *tunnelQualityProbeRound, lookup func(int64) (*nodeRecord, error)) tunnelQualityTunnelPlan {
	plan := tunnelQualityTunnelPlan{base: tunnelQualitySnapshot{TunnelID: tunnelID, Timestamp: time.Now().UnixMilli()}}
	h := p.handler
	tunnel, err := h.getTunnelRecord(tunnelID)
	if err != nil {
		plan.base.ErrorMessage = "隧道不存在"
		return plan
	}
	plan.tunnelType = tunnel.Type
	if tunnel.Type == 2 {
		plan.base.PathStatus = "timeout"
		plan.base.PathLoss = 100
		plan.base.PathUpdatedAt = plan.base.Timestamp
	}
	rows, err := h.listChainNodesForTunnel(tunnelID)
	if err != nil || len(rows) == 0 {
		plan.base.ErrorMessage = "隧道配置不完整"
		return plan
	}
	ipPreference := h.repo.GetTunnelIPPreference(tunnelID)
	entries, _, exits := splitChainNodeGroups(rows)
	options := tunnelQualityProbeOptions()
	if tunnel.Type == 2 {
		plan.base.Success = true
		if len(entries) > 0 && len(exits) > 0 {
			target, targetErr := lookup(exits[0].NodeID)
			if targetErr != nil || target == nil {
				plan.base.ErrorMessage = "出口节点不可用"
				plan.base.Success = false
			} else {
				source, sourceErr := lookup(entries[0].NodeID)
				ip, port, resolveErr := resolveChainProbeTarget(source, target, exits[0].Port, ipPreference, exits[0].ConnectIPType)
				if resolveErr != nil {
					plan.base.ErrorMessage = resolveErr.Error()
					plan.base.Success = false
				} else {
					plan.direct = &tunnelQualityPingKey{entries[0].NodeID, ip, port}
					if sourceErr != nil {
						plan.directErr = sourceErr
					} else if source == nil || source.Status != 1 || target.Status != 1 {
						plan.directErr = errors.New("节点不在线")
					} else {
						localTargetID := target.ID
						if target.IsRemote == 1 {
							localTargetID = 0
						}
						round.planNodePing(*plan.direct, localTargetID, options)
					}
				}
			}
		}
		path := planTunnelPath(rows, ipPreference, lookup)
		plan.path = &path
		for _, segments := range path.segments {
			for _, segment := range segments {
				round.planNodePing(segment.key, segment.localTargetID, options)
			}
		}
		if len(exits) > 0 {
			plan.publicNodeID, plan.hasPublic = exits[0].NodeID, true
		}
	} else if len(entries) > 0 {
		plan.publicNodeID, plan.hasPublic = entries[0].NodeID, true
	}
	if plan.hasPublic {
		node, err := lookup(plan.publicNodeID)
		if err != nil {
			plan.publicErr = err
		} else if node == nil || node.Status != 1 {
			plan.publicErr = errors.New("节点不在线")
		} else {
			round.planExitTest(plan.publicNodeID, options)
		}
	}
	return plan
}

func (plan tunnelQualityTunnelPlan) snapshot(round *tunnelQualityProbeRound) *tunnelQualitySnapshot {
	snap := plan.base
	options := tunnelQualityProbeOptions()
	if plan.direct != nil {
		latency, loss, err := float64(0), float64(100), plan.directErr
		if err == nil {
			latency, loss, err = round.result(plan.direct.nodeID, plan.direct.ip, plan.direct.port, options)
		}
		if err == nil {
			snap.EntryToExitLatency, snap.EntryToExitLoss = latency, loss
		} else {
			snap.EntryToExitLatency, snap.EntryToExitLoss = -1, 100
			snap.Success = false
		}
	}
	if plan.path != nil {
		snap.PathLatency, snap.PathLoss, snap.PathStatus = plan.path.probe(round.result, options)
		snap.PathUpdatedAt = time.Now().UnixMilli()
	}
	if plan.hasPublic {
		latency, loss, err := float64(0), float64(100), plan.publicErr
		if err == nil {
			latency, loss, err = round.exitTestResult(plan.publicNodeID)
		}
		if err == nil {
			snap.ExitToBingLatency, snap.ExitToBingLoss = latency, loss
			if plan.tunnelType != 2 {
				snap.Success = true
			}
		} else {
			if snap.ErrorMessage == "" {
				snap.ErrorMessage = err.Error()
			}
			snap.Success = false
		}
	}
	return &snap
}

func (p *tunnelQualityProber) tcpPingNode(nodeID int64, ip string, port int, options diagnosisExecOptions) (latency float64, loss float64, err error) {
	h := p.handler
	if h == nil {
		return 0, 100, nil
	}

	node, nodeErr := h.getNodeRecord(nodeID)
	if nodeErr != nil {
		return 0, 100, nodeErr
	}
	if !p.nodeOnline(node) {
		return 0, 100, errors.New("节点不在线")
	}

	var pingData map[string]interface{}
	var pingErr error
	if node != nil && node.IsRemote == 1 {
		pingData, pingErr = h.tcpPingViaRemoteNode(node, ip, port, options)
	} else {
		pingData, pingErr = h.tcpPingViaNode(nodeID, ip, port, options)
	}
	if pingErr != nil {
		return 0, 100, pingErr
	}

	avgTime := asFloat(pingData["averageTime"], 0)
	packetLoss := asFloat(pingData["packetLoss"], 100)

	return avgTime, packetLoss, nil
}

type tunnelQualityExitResult struct {
	tunnelQualityPingResult
	targetHost string
}

// The complete ordered fallback is sampled once per exit node per round.
func (r *tunnelQualityProbeRound) tcpPingExitTest(nodeID int64, options diagnosisExecOptions) (latency float64, loss float64, targetHost string, err error) {
	result := &tunnelQualityExitResult{tunnelQualityPingResult: tunnelQualityPingResult{done: make(chan struct{}), loss: 100}}
	value, loaded := r.publicResults.LoadOrStore(nodeID, result)
	if loaded {
		result = value.(*tunnelQualityExitResult)
		select {
		case <-result.done:
		case <-r.ctx.Done():
			return 0, 100, "", r.ctx.Err()
		}
	} else {
		for _, target := range exitTestTargets {
			result.latency, result.loss, result.err = r.ping(nodeID, target.host, target.port, options)
			if result.err == nil {
				result.targetHost = target.name
				break
			}
			if r.ctx.Err() != nil {
				break
			}
		}
		if result.err != nil {
			result.latency, result.loss = 0, 100
		}
		close(result.done)
	}
	return result.latency, result.loss, result.targetHost, result.err
}

func (r *tunnelQualityProbeRound) exitTestResult(nodeID int64) (float64, float64, error) {
	value, ok := r.publicResults.Load(nodeID)
	if !ok {
		return 0, 100, errors.New("探测结果不存在")
	}
	result := value.(*tunnelQualityExitResult)
	<-result.done
	return result.latency, result.loss, result.err
}

func (p *tunnelQualityProber) storeResult(snap *tunnelQualitySnapshot) {
	if snap == nil {
		return
	}

	// Update in-memory cache (latest per tunnel)
	p.cache.Store(snap.TunnelID, snap)

	// Persist to database (history)
	h := p.handler
	if h == nil || h.repo == nil {
		return
	}

	successInt := 0
	if snap.Success {
		successInt = 1
	}

	q := &model.TunnelQuality{
		TunnelID:           snap.TunnelID,
		EntryToExitLatency: snap.EntryToExitLatency,
		ExitToBingLatency:  snap.ExitToBingLatency,
		EntryToExitLoss:    snap.EntryToExitLoss,
		ExitToBingLoss:     snap.ExitToBingLoss,
		Success:            successInt,
		ErrorMessage:       snap.ErrorMessage,
		Timestamp:          snap.Timestamp,
	}
	if err := h.repo.InsertTunnelQuality(q); err != nil {
		log.Printf("tunnel_quality_prober: insert db err=%v tunnel_id=%d", err, snap.TunnelID)
	}
}
