package socket

import (
	"sync"
	"time"
)

type LeiKwanForward struct {
	ForwardID  int64 `json:"forwardId"`
	ListenPort int   `json:"listenPort"`
	Running    bool  `json:"running"`
}
type LeiKwanReport struct {
	Status   string           `json:"status"`
	Forwards []LeiKwanForward `json:"forwards"`
}

// Called by the existing system report, with at most one read-only scan a minute.
// No subprocess, independent timer or access to the client account config.
type leiKwanDetector struct {
	mu      sync.Mutex
	checked time.Time
	report  *LeiKwanReport
}

func (d *leiKwanDetector) result() *LeiKwanReport {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.checked.IsZero() || time.Since(d.checked) >= time.Minute {
		d.report = detectLeiKwan("/")
		d.checked = time.Now()
	}
	return d.report
}
