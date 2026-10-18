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
