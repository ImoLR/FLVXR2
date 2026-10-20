package machineprotect

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Fixed contract with the mmwx Helper (task machine-protect-mmwx): the Helper
// writes this file every loop; FLVX only reads it.
const (
	HelperUnit           = "mmwxc-helper.service"
	MmwxStatusRelPath    = "mmwxc/machine-protection.json" // under /run
	BusinessPortsRelPath = "flvx/business-ports.json"      // under /run

	// A status file is fresh when its updated_at is within this window.
	mmwxStatusFreshFor = 60 * time.Second
	// systemctl runs at most this often.
	helperCheckEvery = 30 * time.Second
	// Ownership is sticky: the machine stays mmwx-owned until no mmwx sign was
	// seen for this long, so a restarting Helper never makes FLVX flap its nft
	// table and sysctl.
	mmwxOwnerHold = 2 * time.Minute
)

// mmwxStatus is /run/mmwxc/machine-protection.json.
type mmwxStatus struct {
	Version          int    `json:"version"`
	Owner            string `json:"owner"`
	UpdatedAt        string `json:"updated_at"`
	Enabled          bool   `json:"enabled"`
	MaxTotal         *int64 `json:"max_total"`
	TWBuckets        *int64 `json:"tw_buckets"`
	CloseWaitSeconds *int64 `json:"close_wait_seconds"`
	Total            int64  `json:"total"`
	TCP              int64  `json:"tcp"`
	UDP              int64  `json:"udp"`
	Blocking         bool   `json:"blocking"`
	DroppedTotal     int64  `json:"dropped_total"`
}

// readMmwxStatus returns the parsed file (nil when absent or malformed) and
// whether it is fresh.
func readMmwxStatus(path string, now time.Time) (*mmwxStatus, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return nil, false
	}
	var s mmwxStatus
	if json.Unmarshal(b, &s) != nil || s.Version != 1 || (s.Owner != "" && s.Owner != OwnerMmwx) {
		return nil, false
	}
	at, err := time.Parse(time.RFC3339Nano, s.UpdatedAt)
	if err != nil {
		return &s, false
	}
	age := now.Sub(at)
	return &s, age < mmwxStatusFreshFor && age > -mmwxStatusFreshFor
}

// helperActive reports whether systemd runs the mmwx Helper.
func helperActive(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", HelperUnit).Run() == nil
}

// ownerDetector decides who owns the machine's protection.
type ownerDetector struct {
	statusPath   string
	helperActive func(context.Context) bool

	lastHelperCheck time.Time
	lastHelper      bool
	mmwxSeenAt      time.Time
}

type ownership struct {
	mmwx   bool
	status *mmwxStatus // last parsed status file (may be stale)
	fresh  bool
}

func newOwnerDetector(runDir string, active func(context.Context) bool) *ownerDetector {
	if active == nil {
		active = helperActive
	}
	return &ownerDetector{statusPath: filepath.Join(runDir, MmwxStatusRelPath), helperActive: active}
}

func (d *ownerDetector) detect(ctx context.Context, now time.Time) ownership {
	status, fresh := readMmwxStatus(d.statusPath, now)
	if d.lastHelperCheck.IsZero() || now.Sub(d.lastHelperCheck) >= helperCheckEvery || now.Before(d.lastHelperCheck) {
		d.lastHelper = d.helperActive(ctx)
		d.lastHelperCheck = now
	}
	if fresh || d.lastHelper {
		d.mmwxSeenAt = now
	}
	return ownership{
		mmwx:   !d.mmwxSeenAt.IsZero() && now.Sub(d.mmwxSeenAt) < mmwxOwnerHold,
		status: status,
		fresh:  fresh,
	}
}

// mirror builds the machine_protect report of an mmwx-owned machine.
func (o ownership) mirror() Protect {
	p := Protect{Owner: OwnerMmwx, MmwxStatus: MmwxStatusMissing}
	if o.status == nil {
		return p
	}
	p.MmwxStatus = MmwxStatusStale
	if o.fresh {
		p.MmwxStatus = MmwxStatusOK
	}
	p.Enabled = o.status.Enabled
	p.MaxTotal = clonePositive(o.status.MaxTotal)
	p.TWBuckets = clonePositive(o.status.TWBuckets)
	p.CloseWaitSeconds = clonePositive(o.status.CloseWaitSeconds)
	p.Blocking = o.status.Blocking
	if o.status.DroppedTotal > 0 {
		p.DroppedTotal = o.status.DroppedTotal
	}
	return p
}

// closeWait is the CLOSE_WAIT timeout the machine owner wants for this node's
// FLVX landing Core: only a fresh, enabled mmwx status sets one.
func (o ownership) closeWait() int64 {
	if !o.fresh || o.status == nil || !o.status.Enabled || o.status.CloseWaitSeconds == nil {
		return 0
	}
	v := *o.status.CloseWaitSeconds
	if v < MinCloseWaitSeconds || v > MaxCloseWaitSeconds {
		return 0
	}
	return v
}

func clonePositive(v *int64) *int64 {
	if v == nil {
		return nil
	}
	return positive(*v)
}
