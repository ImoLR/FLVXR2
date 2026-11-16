//go:build warmlab

package warmpool

import (
	"encoding/json"
	"net/http"
	"os"
	"time"
)

// Local lab only (plan 249): `go build -tags warmlab` lets
// FLVX_WARM_IDLE_SLEEP (e.g. "60s") shorten the idle sleep so the lab can show
// a pool draining and waking without waiting hours; the panel's idleSleepS
// is then ignored (plan 264). FLVX_WARM_LAB_HTTP (e.g. "127.0.0.1:6061", plan
// 264) serves GET /warm = every pool's idle / dialing / want so the lab can
// time refills. Release builds do not contain this file: there the timer is
// IdleSleepAfter or the panel's value, and there is no endpoint.
func init() {
	if v := os.Getenv("FLVX_WARM_IDLE_SLEEP"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			idleSleepDefault = d
			idleSleepLocked = true
			Default.mu.Lock()
			Default.idleSleep = d
			Default.mu.Unlock()
		}
	}
	if addr := os.Getenv("FLVX_WARM_LAB_HTTP"); addr != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/warm", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(Default.labPools())
		})
		go func() { _ = http.ListenAndServe(addr, mux) }()
	}
}

type labPool struct {
	Owner   string `json:"owner"`
	Addr    string `json:"addr"`
	Idle    int    `json:"idle"`
	Dialing int    `json:"dialing"`
	Want    int    `json:"want"`
	Peak5s  int    `json:"peak5s"`
	Live    int    `json:"live"`
}

func (m *Manager) labPools() []labPool {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []labPool
	for _, o := range m.sortedOwnersLocked() {
		for _, p := range o.pools {
			out = append(out, labPool{Owner: o.key.name, Addr: p.key.addr, Idle: len(p.idle), Dialing: p.dialing, Want: p.want, Peak5s: p.peak5s(), Live: m.live})
		}
	}
	return out
}
