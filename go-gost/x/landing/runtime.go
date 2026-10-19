package landing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-gost/x/atomicfile"
)

const unitName = "flvx-landing-core.service"

var defaultOnce sync.Once
var defaultManager *Manager

func Default() *Manager {
	defaultOnce.Do(func() {
		executable, _ := os.Executable()
		dir := filepath.Join(filepath.Dir(executable), "landing")
		direct := os.Getenv("FLVX_LANDING_TEST_DIRECT") == "1"
		if direct && filepath.IsAbs(os.Getenv("FLVX_LANDING_TEST_DIR")) {
			dir = os.Getenv("FLVX_LANDING_TEST_DIR")
		}
		defaultManager = NewManager(dir, direct)
	})
	return defaultManager
}

type appliedState struct {
	Epoch      string            `json:"epoch"`
	Inbounds   map[string]string `json:"inbounds"`
	Egress     string            `json:"egress,omitempty"`
	Outbounds  map[string]string `json:"outbounds,omitempty"`
	EgressTags []string          `json:"egress_tags,omitempty"`
}
type Manager struct {
	mu        sync.Mutex
	dir       string
	direct    bool
	uid, gid  int
	desired   Desired
	loaded    bool
	apiPort   int
	epoch     string
	applied   appliedState
	counters  []Counter
	lastError string
	started   bool

	egress            EgressStatus
	egressCache       egressCache
	withheld          map[string]bool
	routingEpoch      string
	routingOK         bool
	restartedRevision int64
	geoChecked        map[string]geoStamp
	proc              *os.Process // direct (test) mode only
	execPath          string      // test hook, see command()
	// Test hooks; nil uses the real implementations.
	detectLocal func() localAddrs
	geoDownload func(ctx context.Context, direct, path, expected string, limit int64, mode os.FileMode) error
}

// NewManager's direct mode is used only by the explicit local test switch.
func NewManager(dir string, direct bool) *Manager {
	return &Manager{dir: dir, direct: direct, uid: -1, gid: -1}
}
func (m *Manager) binary() string { return filepath.Join(m.dir, "flvx-landing-core") }

// command returns the executable used to run the verified binary; tests point
// it at a wrapper (e.g. one that enters a network namespace).
func (m *Manager) command() string {
	if m.execPath != "" {
		return m.execPath
	}
	return m.binary()
}
func (m *Manager) socket() string { return filepath.Join(m.dir, "run", "control.sock") }

func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		if !m.loaded {
			if b, err := os.ReadFile(filepath.Join(m.dir, "desired.json")); err == nil {
				var d Desired
				if json.Unmarshal(b, &d) == nil && d.validate() == nil {
					m.desired = d
					m.loaded = true
				}
			}
		}
		if m.loaded {
			if err := m.reconcile(ctx); err != nil {
				m.lastError = err.Error()
			} else {
				m.lastError = ""
			}
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) Apply(ctx context.Context, d Desired) (Status, error) {
	if err := d.validate(); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.loaded {
		if b, err := os.ReadFile(filepath.Join(m.dir, "desired.json")); err == nil {
			_ = json.Unmarshal(b, &m.desired)
		}
	}
	if d.Revision < m.desired.Revision {
		return m.statusLocked(), fmt.Errorf("stale landing revision")
	}
	if d.Revision == m.desired.Revision && canonical(d) != canonical(m.desired) {
		return m.statusLocked(), fmt.Errorf("landing revision reused with different content")
	}
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return Status{}, fmt.Errorf("create landing directory: %w", err)
	}
	if err := m.writeJSON("desired.json", d); err != nil {
		return Status{}, err
	}
	m.desired = d
	m.loaded = true
	err := m.reconcile(ctx)
	if err != nil {
		m.lastError = err.Error()
	} else {
		m.lastError = ""
	}
	return m.statusLocked(), err
}
func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.statusLocked() }
func (m *Manager) statusLocked() Status {
	state := "disabled"
	if m.desired.Enabled {
		state = "running"
		if m.epoch == "" {
			state = "starting"
		}
	}
	if m.lastError != "" {
		state = "error"
	}
	egress := m.egress
	if egress.State == "" {
		egress.State = "none"
	}
	egress.Warnings = append([]string(nil), egress.Warnings...)
	egress.Withheld = append([]string(nil), egress.Withheld...)
	return Status{Revision: m.desired.Revision, Epoch: m.epoch, CoreVersion: CoreVersion, State: state, Error: m.lastError, Nodes: append([]Counter{}, m.counters...), Egress: &egress}
}
func (m *Manager) Counters() []Counter {
	// A first install can spend minutes on download mirrors. Cumulative landing
	// samples can wait for the next tick; existing forwarding uploads cannot.
	if !m.mu.TryLock() {
		return nil
	}
	defer m.mu.Unlock()
	if !m.loaded {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	_ = m.collect(ctx)
	return append([]Counter(nil), m.counters...)
}

func (m *Manager) writeJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return m.writeFile(name, b, 0600)
}
func (m *Manager) writeFile(name string, b []byte, mode os.FileMode) error {
	if err := atomicfile.WriteFile(filepath.Join(m.dir, name), b, mode); err != nil {
		return fmt.Errorf("write landing state: %w", err)
	}
	return nil
}

func (m *Manager) install(ctx context.Context) error {
	expected, ok := CoreSHA256[runtime.GOARCH]
	if !ok || runtime.GOOS != "linux" {
		return fmt.Errorf("landing Core requires Linux amd64 or arm64")
	}
	if hash, err := fileHash(m.binary()); err == nil && hash == expected {
		return nil
	}
	direct := "https://github.com/ImoLR/mmwx-custom/releases/download/" + CoreVersion + "/mmwxc-core-linux-" + runtime.GOARCH
	if err := downloadVerified(ctx, direct, m.binary(), expected, 128<<20, 0755); err != nil {
		return fmt.Errorf("pinned landing Core download or SHA256 validation failed")
	}
	return nil
}

func (m *Manager) geoDownloadFn() func(ctx context.Context, direct, path, expected string, limit int64, mode os.FileMode) error {
	if m.geoDownload != nil {
		return m.geoDownload
	}
	return downloadVerified
}
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}

func (m *Manager) setup(ctx context.Context) error {
	if err := m.install(ctx); err != nil {
		return err
	}
	if m.apiPort == 0 {
		var saved struct {
			Port int `json:"port"`
		}
		b, _ := os.ReadFile(filepath.Join(m.dir, "api.json"))
		_ = json.Unmarshal(b, &saved)
		if saved.Port >= 1024 && saved.Port <= 65535 {
			m.apiPort = saved.Port
		} else {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return err
			}
			m.apiPort = listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			if err := m.writeJSON("api.json", map[string]int{"port": m.apiPort}); err != nil {
				return err
			}
		}
	}
	// Rewrite only on change: the running Core reads it at start, and an
	// unchanged file keeps disk writes at zero in the steady state.
	config, err := json.MarshalIndent(baseConfig(m.apiPort), "", "  ")
	if err != nil {
		return err
	}
	if old, readErr := os.ReadFile(filepath.Join(m.dir, "config.json")); readErr != nil || string(old) != string(config) {
		if err := m.writeFile("config.json", config, 0600); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(m.dir, "run"), 0700); err != nil {
		return err
	}
	if m.direct {
		return nil
	}
	account, err := user.Lookup("flvx-landing")
	if err != nil {
		if err := exec.CommandContext(ctx, "useradd", "--system", "--user-group", "--no-create-home", "--home-dir", m.dir, "--shell", "/usr/sbin/nologin", "flvx-landing").Run(); err != nil {
			return fmt.Errorf("create landing service account: %w", err)
		}
		account, err = user.Lookup("flvx-landing")
		if err != nil {
			return err
		}
	}
	m.uid, _ = strconv.Atoi(account.Uid)
	m.gid, _ = strconv.Atoi(account.Gid)
	// Only the socket directory is writable by the service. A writable binary
	// directory would let a compromised unprivileged Core replace the API client
	// subsequently executed by the root agent.
	if err := os.Chown(m.dir, 0, m.gid); err != nil {
		return err
	}
	if err := os.Chmod(m.dir, 0750); err != nil {
		return err
	}
	if err := os.Chown(filepath.Join(m.dir, "run"), m.uid, m.gid); err != nil {
		return err
	}
	if err := os.Chown(filepath.Join(m.dir, "config.json"), 0, m.gid); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(m.dir, "config.json"), 0640); err != nil {
		return err
	}
	unit, err := m.unit()
	if err != nil {
		return err
	}
	path := "/etc/systemd/system/" + unitName
	old, _ := os.ReadFile(path)
	if string(old) != unit {
		if err := atomicfile.WriteFile(path, []byte(unit), 0644); err != nil {
			return err
		}
		if err := exec.CommandContext(ctx, "systemctl", "daemon-reload").Run(); err != nil {
			return fmt.Errorf("reload landing unit: %w", err)
		}
	}
	// set-property changes runtime resource ceilings without restarting existing connections.
	if err := exec.CommandContext(ctx, "systemctl", "set-property", unitName, fmt.Sprintf("MemoryMax=%dM", m.desired.CoreMemoryMB), fmt.Sprintf("CPUQuota=%d%%", m.desired.CoreCPUPercent)).Run(); err != nil {
		return fmt.Errorf("set landing resource limits: %w", err)
	}
	return nil
}
func (m *Manager) unit() (string, error) {
	if strings.ContainsAny(m.dir, "\n\r\x00%\"") {
		return "", fmt.Errorf("unsupported landing installation path")
	}
	return fmt.Sprintf(`[Unit]
Description=FLVX isolated landing Core
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=flvx-landing
Group=flvx-landing
ExecStart="%s" run -config "%s"
Environment="MMWXC_CORE_CONTROL_SOCKET=%s"
UMask=0077
Restart=on-failure
RestartSec=3
MemoryMax=%dM
CPUQuota=%d%%
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
CapabilityBoundingSet=
ReadWritePaths="%s"
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
`, m.binary(), filepath.Join(m.dir, "config.json"), m.socket(), m.desired.CoreMemoryMB, m.desired.CoreCPUPercent, filepath.Join(m.dir, "run")), nil
}
func (m *Manager) start(ctx context.Context) error {
	if m.direct {
		cmd := exec.Command(m.command(), "run", "-config", filepath.Join(m.dir, "config.json"))
		cmd.Env = append(os.Environ(), "MMWXC_CORE_CONTROL_SOCKET="+m.socket())
		// Local test processes are reaped; the core intentionally survives an agent restart.
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start test landing Core: %w", err)
		}
		m.proc = cmd.Process
		go func() { _ = cmd.Wait() }()
	} else {
		if err := exec.CommandContext(ctx, "systemctl", "enable", "--now", unitName).Run(); err != nil {
			return fmt.Errorf("start landing Core: %w", err)
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := m.snapshot(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("landing Core readiness timed out")
}
