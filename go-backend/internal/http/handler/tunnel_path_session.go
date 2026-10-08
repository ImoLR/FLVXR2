package handler

import (
	"errors"
	"log"
	"time"
)

type tunnelPathSegmentState struct {
	latency, loss float64
	failures      int
	settledAt     time.Time
}

// Preparation happens after all tunnels have contributed their legacy keys, so
// sharing never depends on tunnel order. Only path-only keys enter the session.
func (p *tunnelQualityProber) preparePathRound(r *tunnelQualityProbeRound, plans []tunnelQualityTunnelPlan, session uint64) {
	r.preservePublicSamples()
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	if session != p.pathSession || !p.pathDemanded() {
		for key, options := range r.planned {
			if options.pingCount == 2 {
				delete(r.planned, key)
			}
		}
		return
	}
	for key, options := range r.planned {
		if options.pingCount != 2 {
			delete(p.pathSegments, key)
			continue
		}
		state := p.pathSegments[key]
		if !state.settledAt.IsZero() && p.now().Sub(state.settledAt) >= tunnelPathSettledTTL {
			delete(p.pathSegments, key)
			continue
		}
		if state.settledAt.IsZero() {
			continue
		}
		result := &tunnelQualityPingResult{done: make(chan struct{}), latency: state.latency, loss: state.loss}
		if state.failures >= 3 {
			result.err = errors.New("探测超时")
		}
		close(result.done)
		r.results.Store(key, result)
		delete(r.planned, key)
	}
	r.onResult = func(key tunnelQualityPingKey, result *tunnelQualityPingResult) {
		p.pathMu.Lock()
		defer p.pathMu.Unlock()
		if session != p.pathSession || !p.pathDemanded() || p.ctx.Err() != nil {
			return
		}
		if options, ok := r.planned[key]; ok && options.pingCount == 2 {
			state := p.pathSegments[key]
			state.latency, state.loss = result.latency, result.loss
			if result.err == nil && result.loss < 100 && result.latency >= 0 {
				state.failures = 0
				state.settledAt = p.now()
			} else {
				state.failures++
				if state.failures >= 3 {
					state.settledAt = p.now()
				}
			}
			p.pathSegments[key] = state
		}
		for _, plan := range plans {
			if plan.path == nil {
				continue
			}
			for _, layer := range plan.path.segments {
				found := false
				for _, segment := range layer {
					if segment.key == key {
						p.publishPath(plan, r)
						found = true
						break
					}
				}
				if found {
					break
				}
			}
		}
	}
	// Fully cached paths refresh even in a round with zero new path commands.
	for _, plan := range plans {
		p.publishPath(plan, r)
	}
}

// Called under pathMu. Read only finished results: a slow unrelated segment or
// public test cannot hold up this tunnel, and later alternatives can refine it.
func (p *tunnelQualityProber) publishPath(plan tunnelQualityTunnelPlan, r *tunnelQualityProbeRound) {
	if plan.base.PathUpdatedAt == 0 {
		return
	}
	// A path-only first snapshot has no legacy timestamp. GetAll keeps it
	// hidden from monitor until the normal round stores legacy measurements.
	snap := tunnelQualitySnapshot{TunnelID: plan.base.TunnelID}
	if value, ok := p.cache.Load(snap.TunnelID); ok {
		snap = *value.(*tunnelQualitySnapshot)
	}
	pending := false
	latency, loss, status := float64(0), float64(100), "timeout"
	if plan.path != nil {
		latency, loss, status = plan.path.probe(func(id int64, ip string, port int, _ diagnosisExecOptions) (float64, float64, error) {
			key := tunnelQualityPingKey{id, ip, port}
			if value, ok := r.results.Load(key); ok {
				result := value.(*tunnelQualityPingResult)
				select {
				case <-result.done:
					if options, planned := r.planned[key]; planned && options.pingCount == 2 && p.pathSegments[key].settledAt.IsZero() {
						pending = true
					}
					return result.latency, result.loss, result.err
				default:
				}
			}
			pending = true
			return 0, 100, errors.New("探测中")
		}, tunnelQualityProbeOptions())
	}
	if status == "ok" || !pending {
		snap.PathLatency, snap.PathLoss, snap.PathStatus = latency, loss, status
	} else if _, exists := p.cache.Load(snap.TunnelID); !exists {
		snap.PathStatus = ""
	}
	if snap.PathStatus != "" {
		snap.PathUpdatedAt = p.now().UnixMilli()
	} else {
		snap.PathUpdatedAt = 0
	}
	p.cache.Store(snap.TunnelID, &snap)
}

func (p *tunnelQualityProber) finishPathRound(r *tunnelQualityProbeRound, session uint64) {
	p.pathMu.Lock()
	defer p.pathMu.Unlock()
	if session != p.pathSession || !p.pathDemanded() {
		return
	}
	p.lastPathProbe.Store(p.now().UnixNano())
	ok, failing, dead := 0, 0, 0
	for _, state := range p.pathSegments {
		if state.settledAt.IsZero() {
			failing++
		} else if state.failures >= 3 {
			dead++
		} else {
			ok++
		}
	}
	log.Printf("tunnel_quality_prober: path probes: pinged=%d ok=%d failing=%d dead=%d", r.pathPinged.Load(), ok, failing, dead)
}
