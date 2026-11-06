package socket

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-gost/x/landing"
)

func (w *WebSocketReporter) handleApplyLandingDesired(data interface{}) (landing.Status, error) {
	if w.nodeID == 36 {
		return landing.Status{}, fmt.Errorf("node 36 cannot host landing services")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return landing.Status{}, err
	}
	var desired landing.Desired
	if err := json.Unmarshal(raw, &desired); err != nil {
		return landing.Status{}, fmt.Errorf("invalid landing desired state")
	}
	ctx, cancel := context.WithTimeout(w.ctx, 25*time.Minute)
	defer cancel()
	return landing.Default().Apply(ctx, desired)
}

// handleRealityProbe runs a Reality 伪装域名 测速 from this machine (plan 227):
// read-only, ≤ 4 candidates at once, one TCP connect per IP + one TLS
// handshake per candidate.
func (w *WebSocketReporter) handleRealityProbe(data interface{}) (landing.RealityProbeResponse, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return landing.RealityProbeResponse{}, err
	}
	var req landing.RealityProbeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return landing.RealityProbeResponse{}, fmt.Errorf("invalid reality probe request")
	}
	// Below the panel's 90 s command timeout (and Cloudflare's 100 s): a
	// candidate not finished by then is reported as 已取消.
	ctx, cancel := context.WithTimeout(w.ctx, 80*time.Second)
	defer cancel()
	return landing.ProbeReality(ctx, req)
}
