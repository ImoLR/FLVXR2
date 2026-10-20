package machineprotect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-gost/x/atomicfile"
)

// StateFileName lives in the agent config directory (e.g. /etc/flvxx) so the
// original kernel value survives agent restarts and OTA.
const StateFileName = "machine-protection-state.json"

const twSysctlRel = "proc/sys/net/ipv4/tcp_max_tw_buckets"

type twState struct {
	Original int64 `json:"original"`
	Applied  int64 `json:"applied"`
}

type stateFile struct {
	Version   int      `json:"version"`
	TWBuckets *twState `json:"tcp_max_tw_buckets,omitempty"`
}

// twBuckets manages net.ipv4.tcp_max_tw_buckets of the agent's network namespace
// (the host's for a systemd agent). The original value is saved before the first
// change and written back on restore — but only while the kernel still holds the
// value FLVX applied, so a value set by someone else is never clobbered.
type twBuckets struct {
	sysctlPath string
	statePath  string
	state      *twState
	loaded     bool
}

func newTWBuckets(procRoot, stateDir string) *twBuckets {
	if procRoot == "" {
		procRoot = "/"
	}
	return &twBuckets{sysctlPath: filepath.Join(procRoot, twSysctlRel), statePath: filepath.Join(stateDir, StateFileName)}
}

func (t *twBuckets) read() (int64, error) {
	b, err := os.ReadFile(t.sysctlPath)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse tcp_max_tw_buckets: %w", err)
	}
	return v, nil
}

func (t *twBuckets) write(v int64) error {
	return os.WriteFile(t.sysctlPath, []byte(strconv.FormatInt(v, 10)+"\n"), 0o644)
}

func (t *twBuckets) load() {
	if t.loaded {
		return
	}
	t.loaded = true
	b, err := os.ReadFile(t.statePath)
	if err != nil {
		return
	}
	var s stateFile
	if json.Unmarshal(b, &s) == nil && s.Version == 1 && s.TWBuckets != nil && s.TWBuckets.Original > 0 {
		t.state = s.TWBuckets
	}
}

func (t *twBuckets) save() error {
	if t.state == nil {
		if err := os.Remove(t.statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.MarshalIndent(stateFile{Version: 1, TWBuckets: t.state}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(t.statePath, append(b, '\n'), 0o600)
}

// apply sets the kernel value to target (> 0).
func (t *twBuckets) apply(target int64) error {
	t.load()
	current, err := t.read()
	if err != nil {
		return err
	}
	if t.state == nil {
		if current == target {
			return nil // already the wanted value; nothing to restore later
		}
		// Persist the original before the first change.
		t.state = &twState{Original: current, Applied: target}
		if err := t.save(); err != nil {
			t.state = nil
			return fmt.Errorf("save original tcp_max_tw_buckets: %w", err)
		}
	} else if t.state.Applied != target {
		t.state.Applied = target
		if err := t.save(); err != nil {
			return err
		}
	}
	if current == target {
		return nil
	}
	return t.write(target)
}

// restore writes the saved original back if the kernel still has FLVX's value,
// then forgets the state. No state = FLVX never changed it.
func (t *twBuckets) restore() error {
	t.load()
	if t.state == nil {
		return nil
	}
	current, err := t.read()
	if err == nil && current == t.state.Applied && current != t.state.Original {
		if err := t.write(t.state.Original); err != nil {
			return err
		}
	}
	t.state = nil
	return t.save()
}

// saved reports whether FLVX currently holds a changed kernel value.
func (t *twBuckets) saved() bool {
	t.load()
	return t.state != nil
}
