package probe

import (
	"sort"
	"sync"
	"time"
)

// Edge is one measured node→node TCP latency of a path-latency round.
type Edge struct {
	From, To int64
	MS       float64
	OK       bool
	At       int64 // ms
}

// LatencyRounds is how many 60 s rounds a pair keeps (1 h) for the detail
// sparkline. Memory only: pairs × 60 × 16 B.
const LatencyRounds = 60

// latencyPairTTL: a pair the rounds stopped measuring (tunnel deleted) is
// forgotten after this.
const latencyPairTTL = 2 * time.Hour

type latencyPoint struct {
	at int64
	ms float32
	ok bool
}

type latencyRing struct {
	pts  [LatencyRounds]latencyPoint
	n    int
	head int // next write position
}

func (r *latencyRing) push(p latencyPoint) {
	r.pts[r.head] = p
	r.head = (r.head + 1) % LatencyRounds
	if r.n < LatencyRounds {
		r.n++
	}
}

func (r *latencyRing) last() latencyPoint {
	return r.pts[(r.head-1+LatencyRounds)%LatencyRounds]
}

func (r *latencyRing) ordered() []latencyPoint {
	out := make([]latencyPoint, 0, r.n)
	start := (r.head - r.n + LatencyRounds) % LatencyRounds
	for i := 0; i < r.n; i++ {
		out = append(out, r.pts[(start+i)%LatencyRounds])
	}
	return out
}

type pairKey struct{ from, to int64 }

// LatencyBook keeps the per-pair rings. Bounded by the tunnel topology (pairs
// come from the panel's own path rounds, never from client input).
type LatencyBook struct {
	mu    sync.RWMutex
	pairs map[pairKey]*latencyRing
}

// Record stores one round. Several measurements of the same directed pair in a
// round (different tunnels / ports) collapse into one: the lowest OK value,
// or a failure if none succeeded.
func (b *LatencyBook) Record(edges []Edge, now time.Time) {
	nowMs := now.UnixMilli()
	round := make(map[pairKey]latencyPoint, len(edges))
	for _, e := range edges {
		if e.From <= 0 || e.To <= 0 || e.From == e.To {
			continue
		}
		k := pairKey{e.From, e.To}
		p := latencyPoint{at: nowMs, ok: e.OK && e.MS >= 0}
		if p.ok {
			p.ms = float32(e.MS)
		}
		prev, seen := round[k]
		switch {
		case !seen:
			round[k] = p
		case p.ok && (!prev.ok || p.ms < prev.ms):
			round[k] = p
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pairs == nil {
		b.pairs = make(map[pairKey]*latencyRing)
	}
	for k, p := range round {
		r := b.pairs[k]
		if r == nil {
			r = &latencyRing{}
			b.pairs[k] = r
		}
		r.push(p)
	}
	cutoff := nowMs - latencyPairTTL.Milliseconds()
	for k, r := range b.pairs {
		if r.last().at < cutoff {
			delete(b.pairs, k)
		}
	}
}

// Latest returns, per source node, the newest measurement of each pair not
// older than maxAge.
func (b *LatencyBook) Latest(now time.Time, maxAge time.Duration) map[int64][]Edge {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[int64][]Edge)
	cutoff := now.UnixMilli() - maxAge.Milliseconds()
	for k, r := range b.pairs {
		p := r.last()
		if r.n == 0 || p.at < cutoff {
			continue
		}
		out[k.from] = append(out[k.from], Edge{From: k.from, To: k.to, MS: float64(p.ms), OK: p.ok, At: p.at})
	}
	for from := range out {
		edges := out[from]
		sort.Slice(edges, func(i, j int) bool { return edges[i].To < edges[j].To })
	}
	return out
}

// History returns the ring of every pair starting at from, keyed by target.
// Failed rounds are ms = -1.
func (b *LatencyBook) History(from int64) map[int64][][2]float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[int64][][2]float64)
	for k, r := range b.pairs {
		if k.from != from {
			continue
		}
		pts := r.ordered()
		series := make([][2]float64, 0, len(pts))
		for _, p := range pts {
			ms := -1.0
			if p.ok {
				ms = Round1(float64(p.ms))
			}
			series = append(series, [2]float64{float64(p.at), ms})
		}
		out[k.to] = series
	}
	return out
}

// Pairs is the number of pairs kept (resource reporting / tests).
func (b *LatencyBook) Pairs() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.pairs)
}
