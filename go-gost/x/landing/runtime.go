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
	"sync/atomic"
	"time"

	"github.com/go-gost/x/atomicfile"
	"github.com/go-gost/x/logsafe"
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
	sources           sourceMemo // expanded source lists reused across renders
	withheld          map[string]bool
	guardWant         map[int]*guardGroup // Reality guards of the desired state (plan 127)
	guardByTag        map[string]int      // node inbound tag -> guard port
	guardOn           map[int]bool        // guards whose rules are applied in this pass
	routingEpoch      string
	routingOK         bool
	restartedRevision int64
	coreUpdate        string                     // plan 129: "restart_required" = running Core older than the binary
	upgradedRevision  int64                      // revision whose confirmed Core upgrade restart was attempted
	verified          map[string]fileStamp       // path -> stamp of the last matching SHA256
	downloads         map[string]*downloadRetry  // asset -> backoff after a failed download
	limitsApplied     [2]int                     // MemoryMax MiB / CPUQuota % last set on the unit
	inboundFails      map[string]*inboundFailure // plan 147: inbound tag -> failed add (backoff + report)
	logs              *logsafe.RoutineLogger
	proc              *os.Process // direct (test) mode only
	execPath          string      // test hook, see command()
	// Test hooks; nil uses the real implementations.
	detectLocal   func() localAddrs
	geoDownload   func(ctx context.Context, direct, path, expected string, limit int64, mode os.FileMode) error
	coreDownload  func(ctx context.Context, direct, path, expected string, limit int64, mode os.FileMode) error
	hashFile      func(path string) (string, error)
	systemctl     func(ctx context.Context, args ...string) error
	resourceCheck func(dir string) error
	now           func() time.Time

	// Plan 158: per-user Core groups. budgets has its own lock (SetQuotaGroups
	// must not wait for a reconcile); groupLive / lastSnapshot are guarded by
	// m.mu; groupUsage is read lock-free by the telemetry report.
	budgetsOnce  sync.Once
	budgets      *budgetStore
	groupLive    map[string]groupLive
	groupUsage   atomic.Pointer[[]GroupUsage]
	lastSnapshot *coreSnapshot

	// Machine protection (plan 118), read without m.mu: reconciles can hold m.mu
	// for minutes during a first download.
	closeWait atomic.Int64               // default_close_wait_timeout_seconds; 0 = Core default
	ports     atomic.Pointer[PortsInUse] // public inbound ports of the desired state
}

// PortsInUse are the public inbound ports of the enabled landing nodes.
type PortsInUse struct {
	TCP []int
	UDP []int
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
	go m.runBudgetApplier(ctx)
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
					m.ports.Store(d.publicPorts())
				}
			}
		}
		if m.loaded {
			m.noteResult(m.reconcile(ctx))
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
	m.ports.Store(d.publicPorts())
	err := m.reconcile(ctx)
	m.noteResult(err)
	return m.statusLocked(), err
}

// noteResult records the reconcile outcome for status reports and the journal.
// The journal gets the first failure, a count every 10 min and one recovery
// line per kind (logsafe.RoutineLogger), never one line per 10 s tick. Waiting
// for a confirmed Core restart is a state, not a failure, and is not logged.
func (m *Manager) noteResult(err error) {
	m.lastError = ""
	if err != nil {
		m.lastError = err.Error()
	}
	m.logger().Record("landing reconcile", "core", m.lastError)
	egressErr := ""
	if m.egress.State == "error" {
		egressErr = m.egress.Error
	}
	m.logger().Record("landing egress", "plan", egressErr)
}

// SetCloseWaitTimeout sets the Core's default CLOSE_WAIT timeout (seconds, 0 =
// Core default); the next reconcile (≤ 10 s) sends it with the connection limits.
func (m *Manager) SetCloseWaitTimeout(seconds int64) {
	if seconds < 0 {
		seconds = 0
	}
	m.closeWait.Store(seconds)
}

// PublicPorts returns the public inbound ports of the enabled landing nodes
// without waiting for a running reconcile.
func (m *Manager) PublicPorts() (tcp, udp []int) {
	if p := m.ports.Load(); p != nil {
		return p.TCP, p.UDP
	}
	return nil, nil
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
	return Status{Revision: m.desired.Revision, Epoch: m.epoch, CoreVersion: CoreVersion, State: state, Error: m.lastError, Nodes: append([]Counter{}, m.counters...), Egress: &egress, CoreUpdate: m.coreUpdate, NodeErrors: m.nodeErrors()}
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

// coreExpected is the pinned SHA256 for this machine; tests override it.
var coreExpected = func() (string, bool) {
	expected, ok := CoreSHA256[runtime.GOARCH]
	return expected, ok && runtime.GOOS == "linux"
}

// corePrevious is the previous pin's SHA256 (fallback only); tests override it.
var corePrevious = func() (string, bool) {
	previous, ok := CorePreviousSHA256[runtime.GOARCH]
	return previous, ok && runtime.GOOS == "linux"
}

// previousCoreOK: the binary on disk is the previous pin, verified.
func (m *Manager) previousCoreOK() bool {
	previous, ok := corePrevious()
	return ok && m.verifiedFile(m.binary(), previous, -1)
}

func (m *Manager) install(ctx context.Context) error {
	expected, ok := coreExpected()
	if !ok {
		return fmt.Errorf("landing Core requires Linux amd64 or arm64")
	}
	// One stat per tick; the 30 MiB binary is hashed only after it changed.
	if m.verifiedFile(m.binary(), expected, -1) {
		return nil
	}
	direct := "https://github.com/ImoLR/mmwx-custom/releases/download/" + CoreVersion + "/mmwxc-core-linux-" + runtime.GOARCH
	download := downloadVerified
	if m.coreDownload != nil {
		download = m.coreDownload
	}
	// Never start a first install on a box that is nearly out of disk or
	// memory: the node also carries live forwarding traffic.
	check := checkInstallResources
	if m.resourceCheck != nil {
		check = m.resourceCheck
	}
	// Plan 129: an upgrade (previous pin on disk) never blocks the reconcile;
	// the running Core keeps serving and the download is retried later.
	fallback := m.previousCoreOK()
	if err := check(m.dir); err != nil {
		if fallback {
			m.logger().Record("landing download", "core", "upgrade waits: "+err.Error())
			return nil
		}
		return err
	}
	err := m.retryDownload(ctx, "core", func(ctx context.Context) error {
		if err := download(ctx, direct, m.binary(), expected, 128<<20, 0755); err != nil {
			return fmt.Errorf("pinned landing Core download or SHA256 validation failed")
		}
		return nil
	})
	if err != nil && fallback {
		return nil
	}
	return err
}

// coreUpdateState reports "restart_required" when the running Core started
// before the current binary was written (setup has just verified that the
// binary on disk is the pinned one): a newer pin was downloaded under a Core
// that keeps running the old executable. One stat per tick; the 1 s margin
// absorbs coarse file timestamps.
func (m *Manager) coreUpdateState(snapshot coreSnapshot) string {
	if snapshot.StartedAt.IsZero() {
		return ""
	}
	info, err := os.Stat(m.binary())
	if err != nil || !info.ModTime().After(snapshot.StartedAt.Add(time.Second)) {
		return ""
	}
	return "restart_required"
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
	return m.applyResourceLimits(ctx)
}

// applyResourceLimits changes the running unit's ceilings without restarting
// existing connections. set-property persists drop-ins, so it runs once per
// agent start and on change instead of forking systemctl and rewriting the
// drop-ins on every reconcile tick.
func (m *Manager) applyResourceLimits(ctx context.Context) error {
	limits := [2]int{m.desired.CoreMemoryMB, m.desired.CoreCPUPercent}
	if limits == m.limitsApplied {
		return nil
	}
	args := []string{"set-property", unitName, fmt.Sprintf("MemoryMax=%dM", limits[0]), fmt.Sprintf("CPUQuota=%d%%", limits[1])}
	run := func(ctx context.Context, args ...string) error {
		return exec.CommandContext(ctx, "systemctl", args...).Run()
	}
	if m.systemctl != nil {
		run = m.systemctl
	}
	if err := run(ctx, args...); err != nil {
		return fmt.Errorf("set landing resource limits: %w", err)
	}
	m.limitsApplied = limits
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
