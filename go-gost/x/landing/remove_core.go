package landing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Plan 252: RemoveLandingCore stops and uninstalls the landing Core of a
// chain-only exit (the panel's auto-created landing for 「我自己的节点」) once
// the panel has no chain copy left there. It runs on demand only.

// RemoveCoreRequest is the panel's RemoveLandingCore command.
type RemoveCoreRequest struct {
	// Revision is the panel's current revision of the (empty) landing state.
	// An agent holding a newer desired state refuses: something was pushed
	// after the panel decided to remove the Core.
	Revision int64 `json:"revision"`
}

// RemoveCoreResult reports what the command removed and what it kept.
type RemoveCoreResult struct {
	Removed     bool     `json:"removed"`
	WasRunning  bool     `json:"was_running"`
	UnitRemoved bool     `json:"unit_removed"`
	Files       []string `json:"files"`          // removed, relative to the landing directory
	Kept        []string `json:"kept,omitempty"` // other files left in the landing directory
	DirRemoved  bool     `json:"dir_removed"`
}

// Refusal prefixes the panel recognises: after one of these it pushes the
// empty desired state again and retries once.
const (
	RemoveCoreHasNodes = "landing desired state still has nodes"
	RemoveCoreStale    = "stale landing revision"
	RemoveCoreApplied  = "landing inbounds still applied"
)

// removeCoreLockWait bounds the wait for a running reconcile (a first Core
// download can hold the lock for minutes); the panel retries later instead.
var removeCoreLockWait = 15 * time.Second

// coreOwnFiles are the files the landing Core installation writes into the
// landing directory. Anything else there (entry_rates.json of the entry 速率
// tree, plan 168, or files we do not know) is kept.
var coreOwnFiles = []string{
	"flvx-landing-core", "config.json", "api.json", "desired.json", "applied.json",
	"apply.json", "apply-outbounds.json", "apply-rules.json", "scan.json",
	"geoip.dat", "geosite.dat",
}

func (m *Manager) unitPath() string {
	dir := m.unitDir
	if dir == "" {
		dir = "/etc/systemd/system"
	}
	return filepath.Join(dir, unitName)
}

// unitDropInDir is where `systemctl set-property` (applyResourceLimits)
// persists the unit's MemoryMax / CPUQuota.
func (m *Manager) unitDropInDir() string {
	dir := m.controlDir
	if dir == "" {
		dir = "/etc/systemd/system.control"
	}
	return filepath.Join(dir, unitName+".d")
}

func (m *Manager) runSystemctl(ctx context.Context, args ...string) error {
	if m.systemctl != nil {
		return m.systemctl(ctx, args...)
	}
	child, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return exec.CommandContext(child, "systemctl", args...).Run()
}

// lockWithin takes m.mu, giving up after d (or when ctx ends).
func (m *Manager) lockWithin(ctx context.Context, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if m.mu.TryLock() {
			return true
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// RemoveCore stops and uninstalls this machine's landing Core. It only acts
// when the desired state holds no node at all (every inbound already removed
// by a normal apply, so no counter is left in the Core) and the request is not
// older than that state. Repeating it after a lost answer is a no-op success.
// It never touches the FLVX agent, other services, entry rates or unknown files.
func (m *Manager) RemoveCore(ctx context.Context, req RemoveCoreRequest) (RemoveCoreResult, error) {
	var res RemoveCoreResult
	if req.Revision <= 0 {
		return res, errors.New("invalid landing revision")
	}
	if !m.lockWithin(ctx, removeCoreLockWait) {
		return res, errors.New("landing Core busy, retry later")
	}
	defer m.mu.Unlock()
	if !m.loaded {
		if b, err := os.ReadFile(filepath.Join(m.dir, "desired.json")); err == nil {
			var d Desired
			if json.Unmarshal(b, &d) == nil {
				m.desired = d
			}
		}
	}
	if m.desired.Revision > req.Revision {
		return res, fmt.Errorf("%s: agent has %d, request %d", RemoveCoreStale, m.desired.Revision, req.Revision)
	}
	if n := len(m.desired.Nodes); n > 0 {
		return res, fmt.Errorf("%s (%d)", RemoveCoreHasNodes, n)
	}
	if m.applied.Inbounds == nil {
		if b, err := os.ReadFile(filepath.Join(m.dir, "applied.json")); err == nil {
			var a appliedState
			if json.Unmarshal(b, &a) == nil {
				m.applied = a
			}
		}
	}
	snapshot, snapErr := m.snapshot(ctx)
	res.WasRunning = snapErr == nil
	// The running Core must not serve anything any more (a failed removal is
	// retried by the 10 s reconcile). Entries of an earlier Core epoch (the
	// Core died or restarted) are not in any running Core.
	if n := len(m.applied.Inbounds); n > 0 && res.WasRunning && m.applied.Epoch == epochOf(snapshot) {
		return res, fmt.Errorf("%s (%d)", RemoveCoreApplied, n)
	}
	// The unit must be the one this agent wrote for its own Core.
	// Test (direct) mode never installs a unit.
	unitFile, unitPresent := m.unitPath(), false
	if !m.direct {
		unitText, err := os.ReadFile(unitFile)
		if err != nil && !os.IsNotExist(err) {
			return res, fmt.Errorf("read landing unit: %w", err)
		}
		unitPresent = err == nil
		if unitPresent && !strings.Contains(string(unitText), `ExecStart="`+m.binary()+`"`) {
			return res, errors.New("landing unit belongs to another installation; not removed")
		}
	}
	if err := m.stopCore(ctx, unitPresent, res.WasRunning); err != nil {
		return res, err
	}
	if !m.direct {
		if unitPresent {
			if err := os.Remove(unitFile); err != nil && !os.IsNotExist(err) {
				return res, fmt.Errorf("remove landing unit: %w", err)
			}
			res.UnitRemoved = true
		}
		if err := os.RemoveAll(m.unitDropInDir()); err != nil {
			return res, fmt.Errorf("remove landing unit drop-ins: %w", err)
		}
		if err := m.runSystemctl(ctx, "daemon-reload"); err != nil {
			return res, fmt.Errorf("reload systemd after removing the landing unit: %w", err)
		}
		_ = m.runSystemctl(ctx, "reset-failed", unitName)
	}
	removed, kept, err := m.removeCoreFiles()
	res.Files, res.Kept = removed, kept
	if err != nil {
		return res, err
	}
	if len(kept) == 0 {
		if err := os.Remove(m.dir); err == nil {
			res.DirRemoved = true
		}
	}
	m.resetAfterRemove(ctx)
	res.Removed = true
	return res, nil
}

// stopCore stops the Core and confirms it is gone before any file is removed.
func (m *Manager) stopCore(ctx context.Context, unitPresent, running bool) error {
	if m.direct {
		if m.proc != nil {
			_ = m.proc.Kill()
			m.proc = nil
		} else if running {
			return errors.New("test-mode landing Core is not managed by this agent")
		}
	} else if unitPresent {
		if err := m.runSystemctl(ctx, "disable", "--now", unitName); err != nil {
			return fmt.Errorf("stop landing Core: %w", err)
		}
	} else if running {
		_ = m.runSystemctl(ctx, "stop", unitName)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := m.snapshot(ctx); err != nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return errors.New("landing Core still answers after stop; nothing removed")
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// removeCoreFiles deletes the Core's own files (and their download / atomic
// write temporaries) plus run/; it returns what it removed and what is left.
func (m *Manager) removeCoreFiles() ([]string, []string, error) {
	own := map[string]bool{}
	for _, name := range coreOwnFiles {
		own[name] = true
	}
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read landing directory: %w", err)
	}
	var removed, kept []string
	for _, e := range entries {
		name := e.Name()
		if !coreOwnName(name, own) {
			kept = append(kept, name)
			continue
		}
		if err := os.RemoveAll(filepath.Join(m.dir, name)); err != nil {
			return removed, kept, fmt.Errorf("remove landing file %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	sort.Strings(removed)
	sort.Strings(kept)
	return removed, kept, nil
}

func coreOwnName(name string, own map[string]bool) bool {
	if name == "run" || own[name] {
		return true
	}
	if base, ok := strings.CutSuffix(name, ".download"); ok && own[base] {
		return true
	}
	// atomicfile temporaries: ".<name>.tmp-<random>".
	if strings.HasPrefix(name, ".") {
		if i := strings.Index(name, ".tmp-"); i > 1 && own[name[1:i]] {
			return true
		}
	}
	return false
}

// resetAfterRemove forgets every trace of the removed Core so that the next
// ApplyLandingDesired installs it from scratch, exactly like the first time.
// Entry 速率 (rateMu side) are untouched; the landing part of the kernel
// shaping is dropped with the empty desired state.
func (m *Manager) resetAfterRemove(ctx context.Context) {
	m.desired = Desired{}
	m.loaded = false
	m.apiPort = 0
	m.epoch = ""
	m.applied = appliedState{}
	m.counters = nil
	m.lastError = ""
	m.egress = EgressStatus{}
	m.egressCache = egressCache{}
	m.sources = sourceMemo{}
	m.withheld = nil
	m.guardWant, m.guardByTag, m.guardOn = nil, nil, nil
	m.routingEpoch, m.routingOK = "", false
	m.restartedRevision, m.upgradedRevision = 0, 0
	m.coreUpdate = ""
	m.verified = nil
	m.downloads = nil
	m.limitsApplied = [2]int{}
	m.inboundFails = nil
	m.lastSnapshot = nil
	m.groupLive = nil
	m.groupUsage.Store(&[]GroupUsage{})
	m.scan = scanState{}
	m.scanSeq.Store(0)
	m.ports.Store(m.desired.publicPorts())
	m.inbound.Store(m.desired.inboundPorts())
	m.pins.sync(ctx, map[pinKey]bool{})
	m.applyRates()
}
