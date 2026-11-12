package warmpool

import "time"

// ReportEvery: counters ride the metric message at most this often.
const ReportEvery = 60 * time.Second

// Report is sent as `warm_pool` on the metric message once a minute while
// any pool is configured (or a removed one still has counters). Counters are
// deltas since the previous report.
type Report struct {
	Cap    int         `json:"cap"`
	Live   int         `json:"live"`
	CapHit bool        `json:"capHit,omitempty"`
	Keys   []KeyReport `json:"keys"`
}

// KeyReport: one pool key = (owner, target addr).
type KeyReport struct {
	Kind   string `json:"kind"`  // "c" chain (隧道转发) / "s" rule service (端口转发)
	Owner  string `json:"owner"` // chain name (chains_<tunnel>) or service name
	Addr   string `json:"addr"`  // next hop / rule target as configured
	Target bool   `json:"target,omitempty"`
	Idle   int    `json:"idle,omitempty"`
	// cold = full dials: background refills + streams that found no conn
	ColdN  int64   `json:"coldN,omitempty"`
	ColdMs float64 `json:"coldMs,omitempty"`
	MissN  int64   `json:"missN,omitempty"`
	MissMs float64 `json:"missMs,omitempty"`
	// warm = streams that took a pooled conn; WarmMs = their extra delay
	// (≈0, or failed attempt + cold redial when the conn was dead)
	WarmN   int64   `json:"warmN,omitempty"`
	WarmMs  float64 `json:"warmMs,omitempty"`
	DeadN   int64   `json:"deadN,omitempty"`  // pooled conns dead when used
	PeerN   int64   `json:"peerN,omitempty"`  // idle conns closed by the peer
	YoungN  int64   `json:"youngN,omitempty"` // … of those before 40 s
	FailN   int64   `json:"failN,omitempty"`  // refill dials that failed
	State   string  `json:"state,omitempty"`  // rest | unsupported | capped
	AgeCapS int     `json:"ageCapS,omitempty"`
	// Sleep (plan 249): no stream for 30 min — 0 conns, not refilled until
	// the next stream. Absent on older agents (= awake).
	Sleep bool `json:"sleep,omitempty"`
}

// Report returns the counters since the previous call (and resets them), or
// nil when nothing is configured and nothing is left to report.
func (m *Manager) Report() *Report {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.owners) == 0 && len(m.retired) == 0 {
		return nil
	}
	now := m.now()
	r := &Report{Cap: m.cap, Live: m.live, CapHit: m.capHit, Keys: []KeyReport{}}
	m.capHit = false
	add := func(p *pool) {
		k := KeyReport{
			Kind:   p.key.owner.kind.String(),
			Owner:  p.key.owner.name,
			Addr:   p.key.addr,
			Target: p.want > 0 && !p.removed,
			Idle:   len(p.idle),
			ColdN:  p.stats.coldN + p.stats.missN,
			ColdMs: round1(p.stats.coldMs + p.stats.missMs),
			MissN:  p.stats.missN,
			MissMs: round1(p.stats.missMs),
			WarmN:  p.stats.warmN,
			WarmMs: round1(p.stats.warmMs),
			DeadN:  p.stats.deadN,
			PeerN:  p.stats.peerN,
			YoungN: p.stats.youngN,
			FailN:  p.stats.failN,
			Sleep:  p.sleeping && p.want > 0 && !p.removed,
		}
		switch {
		case p.unsupported:
			k.State = "unsupported"
		case now.Before(p.restUntil):
			k.State = "rest"
		case p.capped:
			k.State = "capped"
		}
		if p.ageCap > 0 {
			k.AgeCapS = int(p.ageCap / time.Second)
		}
		p.stats = counters{}
		if !k.Target && k.Idle == 0 && k.ColdN == 0 && k.WarmN == 0 && k.PeerN == 0 && k.FailN == 0 && k.State == "" {
			return
		}
		r.Keys = append(r.Keys, k)
	}
	for _, o := range m.sortedOwnersLocked() {
		for _, p := range o.pools {
			add(p)
		}
	}
	for _, p := range m.retired {
		add(p)
	}
	m.retired = nil
	return r
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
