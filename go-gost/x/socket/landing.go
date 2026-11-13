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

// handleRemoveLandingCore (plan 252) stops and uninstalls the landing Core of
// a chain-only exit the panel no longer needs; refused while the desired
// state still has nodes. Bounded below the panel's 90 s command timeout.
func (w *WebSocketReporter) handleRemoveLandingCore(data interface{}) (landing.RemoveCoreResult, error) {
	if w.nodeID == 36 {
		return landing.RemoveCoreResult{}, fmt.Errorf("node 36 cannot host landing services")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return landing.RemoveCoreResult{}, err
	}
	var req landing.RemoveCoreRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return landing.RemoveCoreResult{}, fmt.Errorf("invalid remove landing core request")
	}
	ctx, cancel := context.WithTimeout(w.ctx, 60*time.Second)
	defer cancel()
	return landing.Default().RemoveCore(ctx, req)
}

// handleRealityProbe runs a Reality 伪装域名 测速 from this machine (plan 227):
// read-only, ≤ 2 candidates at once (≤ 2 Reality sockets open on the machine, RST close), one TCP connect per IP + one TLS
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
