//go:build linux

package socket

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	lkTick           = time.Minute
	lkReportEvery    = 5 * time.Minute
	lkTrafficEvery   = time.Minute
	lkBackupKeep     = 20
	lkTable          = "flvx_leikwan"
	lkLogswitchUnit  = "leikwan-logswitch.service"
	lkLogswitchDrop  = "60-flvx-panel.conf"
	lkPhoneFile      = "etc/leikwan-phone-backup.nft"
	lkMaxFile        = 1 << 20
	lkCommandTimeout = 20 * time.Second
)

// lkPersist survives agent restarts (state dir, 0600).
type lkPersist struct {
	Applied      map[string]int64 `json:"applied"`
	Forced       map[string]int64 `json:"forced"`
	Written      map[string]int64 `json:"written"`
	Epoch        string           `json:"epoch"`
	CountPorts   []int            `json:"countPorts"`
	BlockPorts   []int            `json:"blockPorts"`
	TableCreated int64            `json:"tableCreated"`
}

type lkRunner func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)

func lkExec(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, lkCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return out, fmt.Errorf("%s: %v %s", filepath.Base(name), err, msg)
	}
	return out, nil
}

// lkHostManager is the agent's 利群主机 module (one goroutine).
type lkHostManager struct {
	root       string
	stateFile  string
	backupRoot string
	now        func() time.Time
	run        lkRunner

	mu        sync.Mutex
	want      *LeiKwanHostSync
	persist   lkPersist
	loaded    bool
	report    *LeiKwanHostReport
	lastKey   string
	lastSent  time.Time
	lastTrSnt time.Time
	due       bool
	status    LKManageStatus
	traffic   *LKTraffic
	waiters   []chan struct{}
	wake      chan struct{}
	agentVer  struct {
		stamp string
		ver   string
	}
}

func newLKHostManager(stateDir string) *lkHostManager {
	if stateDir == "" {
		stateDir = "/etc/flvx_agent"
	}
	return &lkHostManager{root: "/", stateFile: filepath.Join(stateDir, "leikwan-host-state.json"),
		backupRoot: "/var/lib/flvx-leikwan/backup", now: time.Now, run: lkExec, wake: make(chan struct{}, 1)}
}

func (m *lkHostManager) p(name string) string {
	return filepath.Join(m.root, strings.TrimPrefix(name, "/"))
}

func (m *lkHostManager) rel(path string) string {
	if m.root == "/" || m.root == "" {
		return path
	}
	r, err := filepath.Rel(m.root, path)
	if err != nil {
		return path
	}
	return "/" + r
}

func (m *lkHostManager) exists(name string) bool { _, err := os.Stat(m.p(name)); return err == nil }

func (m *lkHostManager) active(unit string) bool {
	raw, err := os.ReadFile(m.p("sys/fs/cgroup/system.slice/" + unit + "/cgroup.procs"))
	return err == nil && strings.TrimSpace(string(raw)) != ""
}

func (m *lkHostManager) mainPID(unit string) int {
	raw, err := os.ReadFile(m.p("sys/fs/cgroup/system.slice/" + unit + "/cgroup.procs"))
	if err != nil {
		return 0
	}
	for _, f := range strings.Fields(string(raw)) {
		if n, err := strconv.Atoi(f); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func (m *lkHostManager) readSmall(name string) ([]byte, bool) {
	f, err := os.Open(m.p(name))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, lkMaxFile))
	return data, err == nil
}

// present: is the LeiKwan client installed here at all?
func (m *lkHostManager) present() bool {
	return m.exists("etc/leikwan-client-agent/config.json") || m.exists("etc/systemd/system/leikwan-client-agent.service")
}

// unitTexts: the unit file and its drop-ins (lexical order).
func (m *lkHostManager) unitTexts(unit string) []string {
	var out []string
	if raw, ok := m.readSmall("etc/systemd/system/" + unit); ok {
		out = append(out, string(raw))
	} else {
		return nil
	}
	drops, _ := filepath.Glob(m.p("etc/systemd/system/" + unit + ".d/*.conf"))
	sort.Strings(drops)
	for _, d := range drops {
		if raw, err := os.ReadFile(d); err == nil {
			out = append(out, string(raw))
		}
	}
	return out
}

func (m *lkHostManager) frontUnit() string {
	test, stable := "leikwan-front-relay-test.service", "leikwan-front-relay.service"
	switch {
	case m.active(test):
		return test
	case m.active(stable):
		return stable
	case m.exists("etc/systemd/system/" + test):
		return test
	case m.exists("etc/systemd/system/" + stable):
		return stable
	}
	return ""
}

func (m *lkHostManager) tokens(path string, norm func(map[string]string) map[string]string) *LKTokens {
	v := &LKTokens{Path: path, Tokens: map[string]string{}}
	if raw, ok := m.readSmall(path); ok {
		v.Present = true
		t, ignored := lkTokenize(string(raw))
		v.Tokens, v.Ignored = norm(t), ignored
	}
	return v
}

var lkStatusFile = regexp.MustCompile(`forward-([0-9]+)\.status\.json$`)

// read discovers the layout of this host and parses every managed file.
func (m *lkHostManager) read() (LKFiles, LKHostState) {
	var files LKFiles
	state := LKHostState{Forwards: []LKForward{}}
	if raw, ok := m.readSmall("etc/hostname"); ok {
		state.Host = strings.TrimSpace(string(raw))
	}
	// front
	if unit := m.frontUnit(); unit != "" {
		texts := m.unitTexts(unit)
		env := lkUnitEnv(texts)
		if strings.Contains(unit, "-test") {
			state.FrontChannel = "test"
			if mm := lkBuildRe.FindStringSubmatch(lkUnitDescription(strings.Join(texts, "\n"))); mm != nil {
				state.FrontBuild = strings.TrimRight(mm[1], ":,)")
			}
		} else {
			state.FrontChannel, state.FrontBuild = "stable", "stable"
		}
		if !m.active(unit) {
			state.FrontChannel += "（未运行）"
		}
		if p := env["LEIKWAN_FRONT_WARM_FILE"]; p != "" {
			files.Warm = m.tokens(p, lkNormWarm)
		}
		if p := env["LEIKWAN_FRONT_SSFILTER_FILE"]; p != "" {
			files.SSFilter = m.tokens(p, lkNormSSFilter)
		}
		if p := env["LEIKWAN_FRONT_SSKEYS"]; p != "" {
			k := &LKKeys{Path: p, FP: map[string]string{}}
			if raw, ok := m.readSmall(p); ok {
				k.Present = true
				k.FP, k.Error = lkParseKeys(raw)
				if info, err := os.Stat(m.p(p)); err == nil && info.Mode().Perm()&0o077 != 0 {
					k.Error = "mode_not_0600"
				}
			}
			files.SSKeys = k
		}
		if p := env["LEIKWAN_FRONT_COALESCE_FILE"]; p != "" {
			c := &LKValue{Path: p, Value: "off"}
			if raw, ok := m.readSmall(p); ok {
				c.Present = true
				c.Value = lkNormValue(string(raw))
			}
			files.Coalesce = c
			state.Coalesce = c.Value
		}
	}
	// logswitch
	if texts := m.unitTexts(lkLogswitchUnit); texts != nil {
		env := lkUnitEnv(texts)
		ls := &LKLogswitch{Unit: "/etc/systemd/system/" + lkLogswitchUnit, DropIn: "/etc/systemd/system/" + lkLogswitchUnit + ".d/" + lkLogswitchDrop,
			Present: true, Lines: lkSortLines(lkSplitList(env["LOGSWITCH_LINES"])), RecordOnly: lkSortUnique(lkSplitList(env["LOGSWITCH_RECORD_ONLY"])),
			TriggersFile: env["LOGSWITCH_TRIGGERS_FILE"], Hold: env["LOGSWITCH_HOLD"], Grace: env["LOGSWITCH_GRACE"],
			LeaseSettle: env["LOGSWITCH_LEASE_SETTLE"], ReturnCheck: env["LOGSWITCH_RETURN_CHECK"]}
		if raw, ok := m.readSmall("run/leikwan-logswitch/lines.json"); ok {
			var lj struct {
				Build string `json:"build"`
			}
			if json.Unmarshal(raw, &lj) == nil {
				ls.Build = lj.Build
			}
		}
		if ls.Build == "" {
			if mm := lkLogswitchRe.FindStringSubmatch(lkUnitDescription(texts[0])); mm != nil {
				ls.Build = "logswitch-" + mm[1]
			}
		}
		if pid := m.mainPID(lkLogswitchUnit); pid > 0 {
			if raw, ok := m.readSmall(fmt.Sprintf("proc/%d/environ", pid)); ok {
				run := map[string]string{}
				for _, kv := range strings.Split(string(raw), "\x00") {
					if k, v, ok := strings.Cut(kv, "="); ok {
						run[k] = v
					}
				}
				rl, rr := lkSortLines(lkSplitList(run["LOGSWITCH_LINES"])), lkSortUnique(lkSplitList(run["LOGSWITCH_RECORD_ONLY"]))
				if !lkListsEqual(rl, ls.Lines) || !lkListsEqual(rr, ls.RecordOnly) {
					ls.Running = &LKLogswitchRun{Lines: rl, RecordOnly: rr}
				}
			}
		}
		files.Logswitch = ls
		state.Logswitch = strings.TrimPrefix(ls.Build, "logswitch-")
		if !m.active(lkLogswitchUnit) {
			state.Logswitch += "（未运行）"
		}
		if ls.TriggersFile != "" {
			files.Triggers = m.tokens(ls.TriggersFile, lkNormTriggers)
		}
	}
	// phone backup
	if raw, ok := m.readSmall(lkPhoneFile); ok {
		rules, comments := lkParsePhone(string(raw))
		files.Phone = &LKPhone{Path: "/" + lkPhoneFile, Present: true, Rules: rules, Comments: comments}
		for p := range rules {
			if n, err := strconv.Atoi(p); err == nil {
				state.PhonePorts = append(state.PhonePorts, n)
			}
		}
		sort.Ints(state.PhonePorts)
	}
	// vendor agent + runtimes
	state.AgentRunning = m.active("leikwan-client-agent.service")
	state.UpdateLock = m.exists("etc/leikwan-client-agent/update-lock")
	if raw, ok := m.readSmall("etc/leikwan-client-agent/config.json"); ok {
		var cfg struct {
			DisableAutoUpdate bool   `json:"disable_auto_update"`
			Selection         string `json:"ultra_selection_mode"`
		}
		if json.Unmarshal(raw, &cfg) == nil {
			state.AutoUpdateOff, state.SelectionMode = cfg.DisableAutoUpdate, cfg.Selection
		}
	}
	state.AgentVersion = m.vendorAgentVersion()
	switch {
	case m.active("leikwan-fast-failover-test.service"):
		state.FastFailover = "test"
	case m.active("leikwan-fast-failover.service"):
		state.FastFailover = "stable"
	}
	cfgs, _ := filepath.Glob(m.p("etc/ultra/forward-*.json"))
	native, udp := 0, 0
	for _, name := range cfgs {
		raw, err := os.ReadFile(name)
		if err != nil || len(raw) > lkMaxFile {
			continue
		}
		var cfg struct {
			ID        int64  `json:"forward_id"`
			Port      int    `json:"listen_port"`
			Target    any    `json:"target"`
			Transport string `json:"transport"`
		}
		if json.Unmarshal(raw, &cfg) != nil || cfg.ID <= 0 {
			continue
		}
		fw := LKForward{ForwardID: cfg.ID, ListenPort: cfg.Port, Transport: cfg.Transport, Target: lkTargetString(cfg.Target)}
		fw.Running = m.active(fmt.Sprintf("ultra-forward-%d.service", cfg.ID))
		if raw, ok := m.readSmall(fmt.Sprintf("run/ultra/forward-%d.status.json", cfg.ID)); ok {
			var st struct {
				State  string `json:"state"`
				Policy string `json:"policy_state"`
				UDP    string `json:"udp_transport"`
				Att    struct {
					FP string `json:"fingerprint"`
				} `json:"runtime_attestation"`
			}
			if json.Unmarshal(raw, &st) == nil {
				fw.State, fw.Policy, fw.UDP = st.State, st.Policy, st.UDP
				if state.CoreVersion == "" && st.Att.FP != "" {
					parts := strings.Split(st.Att.FP, ".")
					if len(parts) > 4 {
						parts = parts[:4]
					}
					state.CoreVersion = strings.Join(parts, ".")
				}
			}
		}
		if strings.Contains(fw.Transport, "udp") {
			udp++
			if fw.UDP == "native_udp" {
				native++
			}
		}
		state.Forwards = append(state.Forwards, fw)
		if len(state.Forwards) >= 64 {
			break
		}
	}
	sort.Slice(state.Forwards, func(i, j int) bool { return state.Forwards[i].ForwardID < state.Forwards[j].ForwardID })
	switch {
	case udp == 0:
	case native == udp:
		state.NativeUDP = "all"
	case native == 0:
		state.NativeUDP = "none"
	default:
		state.NativeUDP = "some"
	}
	return files, state
}

// lkTargetString: the forward target is "host:port" or {host, port, …}.
func lkTargetString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		host, _ := t["host"].(string)
		if host == "" {
			host, _ = t["address"].(string)
		}
		switch p := t["port"].(type) {
		case float64:
			return fmt.Sprintf("%s:%d", host, int(p))
		case string:
			return host + ":" + p
		}
		return host
	}
	return ""
}

var lkAgentVerRe = regexp.MustCompile(`(?m)^AGENT_VERSION\s*=\s*"([^"]+)"`)

func (m *lkHostManager) vendorAgentVersion() string {
	path := m.p("usr/local/bin/leikwan-client-agent.py")
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	stamp := fmt.Sprintf("%d/%d", info.ModTime().UnixNano(), info.Size())
	if stamp == m.agentVer.stamp {
		return m.agentVer.ver
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head, _ := io.ReadAll(io.LimitReader(f, 64<<10))
	ver := ""
	if mm := lkAgentVerRe.FindSubmatch(head); mm != nil {
		ver = string(mm[1])
	}
	m.agentVer.stamp, m.agentVer.ver = stamp, ver
	return ver
}

// ------------------------------------------------------------ persistence

func (m *lkHostManager) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	if raw, err := os.ReadFile(m.stateFile); err == nil {
		_ = json.Unmarshal(raw, &m.persist)
	}
	if m.persist.Applied == nil {
		m.persist.Applied = map[string]int64{}
	}
	if m.persist.Forced == nil {
		m.persist.Forced = map[string]int64{}
	}
	if m.persist.Written == nil {
		m.persist.Written = map[string]int64{}
	}
}

func (m *lkHostManager) save() {
	raw, err := json.Marshal(m.persist)
	if err != nil {
		return
	}
	tmp := m.stateFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err == nil {
		_ = os.Rename(tmp, m.stateFile)
	}
}

// ------------------------------------------------------------ writing

// writeFile replaces a host file atomically (same directory temp file +
// rename, old mode and owner kept) after copying the old one into backupDir.
func (m *lkHostManager) writeFile(path string, data []byte, newMode os.FileMode, backupDir string, mkdir bool) error {
	full := m.p(path)
	dir := filepath.Dir(full)
	if _, err := os.Stat(dir); err != nil {
		if !mkdir {
			return fmt.Errorf("目录不存在: %s", filepath.Dir(path))
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	mode, uid, gid := newMode, -1, -1
	if info, err := os.Stat(full); err == nil {
		mode = info.Mode().Perm()
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(st.Uid), int(st.Gid)
		}
		old, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("读旧文件失败: %v", err)
		}
		bdst := filepath.Join(backupDir, strings.TrimPrefix(path, "/"))
		if err := os.MkdirAll(filepath.Dir(bdst), 0o700); err != nil {
			return fmt.Errorf("备份目录失败: %v", err)
		}
		if err := os.WriteFile(bdst, old, 0o600); err != nil {
			return fmt.Errorf("备份失败: %v", err)
		}
	}
	if newMode == 0o600 {
		mode = 0o600
	}
	tmp := filepath.Join(dir, "."+filepath.Base(full)+".flvx-tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	_ = os.Chmod(tmp, mode)
	if uid >= 0 {
		_ = os.Chown(tmp, uid, gid)
	}
	if err := os.Rename(tmp, full); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func (m *lkHostManager) newBackupDir(now time.Time) string {
	dir := filepath.Join(m.backupRoot, now.UTC().Add(8*time.Hour).Format("20060102-150405"))
	if m.root != "/" && m.root != "" {
		dir = m.p(strings.TrimPrefix(dir, "/"))
	}
	return dir
}

func (m *lkHostManager) pruneBackups() {
	root := m.backupRoot
	if m.root != "/" && m.root != "" {
		root = m.p(strings.TrimPrefix(root, "/"))
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	for len(dirs) > lkBackupKeep {
		_ = os.RemoveAll(filepath.Join(root, dirs[0]))
		dirs = dirs[1:]
	}
}

// applyFile writes the panel's content of file f and runs its reload step.
func (m *lkHostManager) applyFile(ctx context.Context, f string, files LKFiles, d *LKDesired, rev int64, backupDir string) error {
	now := m.now()
	switch f {
	case lkFileWarm:
		if files.Warm == nil || files.Warm.Path == "" {
			return errors.New("这台主机的前端不读预热文件")
		}
		return m.writeFile(files.Warm.Path, []byte(lkRenderWarm(d.Warm.Tokens, rev, now)), 0o644, backupDir, false)
	case lkFileSSFilter:
		if files.SSFilter == nil || files.SSFilter.Path == "" {
			return errors.New("这台主机的前端不读首包过滤文件")
		}
		return m.writeFile(files.SSFilter.Path, []byte(lkRenderSSFilter(d.SSFilter.Tokens, rev, now)), 0o644, backupDir, false)
	case lkFileSSKeys:
		if files.SSKeys == nil || files.SSKeys.Path == "" {
			return errors.New("这台主机的前端没有密钥文件")
		}
		old, _ := m.readSmall(files.SSKeys.Path)
		data, err := lkRenderSSKeys(old, d.SSKeys)
		if err != nil {
			return err
		}
		return m.writeFile(files.SSKeys.Path, data, 0o600, backupDir, false)
	case lkFileTriggers:
		if files.Triggers == nil || files.Triggers.Path == "" {
			return errors.New("这台主机的日志快切没有触发条件文件")
		}
		return m.writeFile(files.Triggers.Path, []byte(lkRenderTriggers(d.Triggers.Tokens, rev, now)), 0o644, backupDir, false)
	case lkFileCoalesce:
		if files.Coalesce == nil || files.Coalesce.Path == "" {
			return errors.New("这台主机的前端不读首包合并文件")
		}
		return m.writeFile(files.Coalesce.Path, []byte(lkRenderCoalesce(d.Coalesce.Value)), 0o644, backupDir, false)
	case lkFileLogswitch:
		if files.Logswitch == nil || !files.Logswitch.Present {
			return errors.New("这台主机没有日志快切")
		}
		if err := m.writeFile(files.Logswitch.DropIn, []byte(lkRenderLogswitchDropIn(d.Logswitch, rev, now)), 0o644, backupDir, true); err != nil {
			return err
		}
		if _, err := m.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
			return err
		}
		_, err := m.run(ctx, nil, "/usr/bin/systemctl", "restart", lkLogswitchUnit)
		return err
	case lkFilePhone:
		if files.Phone == nil || !files.Phone.Present {
			return errors.New("这台主机没有手机备线文件")
		}
		if err := m.writeFile(files.Phone.Path, []byte(lkRenderPhone(d.Phone, rev, now)), 0o644, backupDir, false); err != nil {
			return err
		}
		_, err := m.run(ctx, nil, "/usr/sbin/nft", "-f", m.p(lkPhoneFile))
		return err
	}
	return fmt.Errorf("unknown file %s", f)
}

// manage compares and (only with 接管 on) writes. It returns whether
// something was written (the caller re-reads the host).
func (m *lkHostManager) manage(ctx context.Context, files LKFiles, want *LeiKwanHostSync) bool {
	st := LKManageStatus{Applied: map[string]int64{}, Errors: map[string]string{}, Written: map[string]int64{}}
	if want != nil {
		st.Manage, st.Rev, st.Force = want.Manage, want.Rev, want.Force
	}
	wrote := false
	if want != nil && want.Manage && want.Desired != nil {
		d := want.Desired
		backupDir := ""
		now := m.now()
		for _, f := range lkFileOrder {
			if !lkWanted(f, d) {
				continue
			}
			if lkSame(f, &files, d) {
				if m.persist.Applied[f] != want.Rev || m.persist.Forced[f] != want.Force {
					m.persist.Applied[f], m.persist.Forced[f] = want.Rev, want.Force
					m.save()
				}
				continue
			}
			if want.Rev <= m.persist.Applied[f] && want.Force <= m.persist.Forced[f] {
				st.Drift = append(st.Drift, f)
				continue
			}
			if lkWindowed(f) && !lkInWindow(now) {
				st.Pending = append(st.Pending, f)
				continue
			}
			if backupDir == "" {
				backupDir = m.newBackupDir(now)
			}
			if err := m.applyFile(ctx, f, files, d, want.Rev, backupDir); err != nil {
				st.Errors[f] = err.Error()
				fmt.Printf("⚠️ 利群主机接管 %s 写入失败: %v\n", f, err)
				continue
			}
			fmt.Printf("✅ 利群主机接管 %s 已按面板写入 (rev %d)\n", f, want.Rev)
			m.persist.Applied[f], m.persist.Forced[f], m.persist.Written[f] = want.Rev, want.Force, now.UnixMilli()
			m.save()
			wrote = true
		}
		if backupDir != "" {
			m.pruneBackups()
		}
	}
	for f, r := range m.persist.Applied {
		st.Applied[f] = r
	}
	for f, t := range m.persist.Written {
		st.Written[f] = t
	}
	if len(st.Errors) == 0 {
		st.Errors = nil
	}
	root := m.backupRoot
	if m.root != "/" && m.root != "" {
		root = m.p(strings.TrimPrefix(root, "/"))
	}
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		st.Backup = filepath.Join(m.backupRoot, entries[len(entries)-1].Name())
	}
	m.status = st
	return wrote
}

// ------------------------------------------------------------ traffic (nft)

func lkPortsKey(p []int) string {
	c := append([]int(nil), p...)
	sort.Ints(c)
	parts := make([]string, len(c))
	for i, n := range c {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

func lkCleanPorts(in []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, p := range in {
		if p > 0 && p < 65536 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

// lkTrafficScript: counters are named objects, so flushing and re-adding the
// rules keeps their values. Blocked ports are dropped before the front's
// redirect and the logswitch DNAT (priority -150 < dstnat).
func lkTrafficScript(count, block []int) string {
	var b strings.Builder
	b.WriteString("table inet " + lkTable + " {\n")
	for _, p := range count {
		fmt.Fprintf(&b, "\tcounter u%d { }\n\tcounter d%d { }\n", p, p)
	}
	b.WriteString("\tchain pre { type filter hook prerouting priority -150; policy accept; }\n")
	b.WriteString("\tchain post { type filter hook postrouting priority 150; policy accept; }\n}\n")
	b.WriteString("flush chain inet " + lkTable + " pre\nflush chain inet " + lkTable + " post\n")
	b.WriteString("add rule inet " + lkTable + " pre iifname \"lo\" return\n")
	b.WriteString("add rule inet " + lkTable + " post oifname \"lo\" return\n")
	if len(block) > 0 {
		list := make([]string, len(block))
		for i, p := range block {
			list[i] = strconv.Itoa(p)
		}
		set := strings.Join(list, ", ")
		b.WriteString("add rule inet " + lkTable + " pre meta l4proto { tcp, udp } ct original proto-dst { " + set + " } drop\n")
		b.WriteString("add rule inet " + lkTable + " post ct direction reply meta l4proto { tcp, udp } ct original proto-dst { " + set + " } drop\n")
	}
	for _, p := range count {
		fmt.Fprintf(&b, "add rule inet %s pre ct direction original meta l4proto { tcp, udp } ct original proto-dst %d counter name \"u%d\"\n", lkTable, p, p)
		fmt.Fprintf(&b, "add rule inet %s post ct direction reply meta l4proto { tcp, udp } ct original proto-dst %d counter name \"d%d\"\n", lkTable, p, p)
	}
	return b.String()
}

// lkReadCounters parses `nft -j list table inet flvx_leikwan`.
func lkReadCounters(raw []byte) (map[string][2]int64, bool) {
	var doc struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil, false
	}
	out := map[string][2]int64{}
	table := false
	for _, item := range doc.Nftables {
		if _, ok := item["table"]; ok {
			table = true
		}
		c, ok := item["counter"]
		if !ok {
			continue
		}
		var ctr struct {
			Name  string `json:"name"`
			Bytes int64  `json:"bytes"`
		}
		if json.Unmarshal(c, &ctr) != nil || len(ctr.Name) < 2 {
			continue
		}
		port := ctr.Name[1:]
		if _, err := strconv.Atoi(port); err != nil {
			continue
		}
		v := out[port]
		switch ctr.Name[0] {
		case 'u':
			v[0] = ctr.Bytes
		case 'd':
			v[1] = ctr.Bytes
		default:
			continue
		}
		out[port] = v
	}
	return out, table
}

func (m *lkHostManager) trafficTick(ctx context.Context, want *LeiKwanHostSync) *LKTraffic {
	var wantCount, wantBlock []int
	if want != nil {
		wantBlock = lkCleanPorts(want.Block)
		if want.Traffic {
			wantCount = lkCleanPorts(want.Ports)
		}
	}
	now := m.now()
	tr := &LKTraffic{Epoch: m.persist.Epoch, Ports: map[string][2]int64{}}
	raw, err := m.run(ctx, nil, "/usr/sbin/nft", "-j", "list", "table", "inet", lkTable)
	exists := false
	var counters map[string][2]int64
	if err == nil {
		counters, exists = lkReadCounters(raw)
	}
	newCount := m.persist.CountPorts
	if !exists {
		newCount = nil
	}
	if lkInWindow(now) {
		newCount = wantCount
	} else if lkPortsKey(newCount) != lkPortsKey(wantCount) {
		tr.Pending = true
	}
	need := len(newCount) > 0 || len(wantBlock) > 0
	changed := lkPortsKey(newCount) != lkPortsKey(m.persist.CountPorts) || lkPortsKey(wantBlock) != lkPortsKey(m.persist.BlockPorts) || (need && !exists)
	switch {
	case need && changed:
		if _, err := m.run(ctx, []byte(lkTrafficScript(newCount, wantBlock)), "/usr/sbin/nft", "-f", "-"); err != nil {
			tr.Error = err.Error()
			fmt.Printf("⚠️ 利群流量表更新失败: %v\n", err)
		} else {
			if !exists || m.persist.Epoch == "" {
				var b [8]byte
				_, _ = rand.Read(b[:])
				m.persist.Epoch = hex.EncodeToString(b[:])
				m.persist.TableCreated = now.UnixMilli()
			}
			m.persist.CountPorts, m.persist.BlockPorts = newCount, wantBlock
			m.save()
			if raw, err := m.run(ctx, nil, "/usr/sbin/nft", "-j", "list", "table", "inet", lkTable); err == nil {
				counters, exists = lkReadCounters(raw)
			}
		}
	case !need && exists && (len(m.persist.BlockPorts) > 0 || lkInWindow(now)):
		// Nothing to count or block any more: drop the table (unblocking is
		// immediate; removing counters waits for the window).
		if _, err := m.run(ctx, nil, "/usr/sbin/nft", "delete", "table", "inet", lkTable); err != nil {
			tr.Error = err.Error()
		} else {
			m.persist.CountPorts, m.persist.BlockPorts, m.persist.Epoch = nil, nil, ""
			m.save()
			exists = false
		}
	case !need && !exists && (len(m.persist.CountPorts) > 0 || len(m.persist.BlockPorts) > 0):
		m.persist.CountPorts, m.persist.BlockPorts = nil, nil
		m.save()
	}
	tr.Epoch = m.persist.Epoch
	tr.Blocked = m.persist.BlockPorts
	if exists {
		for _, p := range m.persist.CountPorts {
			if v, ok := counters[strconv.Itoa(p)]; ok {
				tr.Ports[strconv.Itoa(p)] = v
			}
		}
	}
	if !exists && len(wantCount) == 0 && len(wantBlock) == 0 && tr.Error == "" {
		return nil
	}
	return tr
}

// ------------------------------------------------------------ loop / reporting

func (m *lkHostManager) runLoop(ctx context.Context) {
	if m == nil {
		return
	}
	t := time.NewTicker(lkTick)
	defer t.Stop()
	m.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.wake:
		}
		m.tick(ctx)
	}
}

func (m *lkHostManager) tick(ctx context.Context) {
	m.mu.Lock()
	want := m.want
	m.load()
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		for _, w := range m.waiters {
			close(w)
		}
		m.waiters = nil
		m.mu.Unlock()
	}()
	if !m.present() {
		m.mu.Lock()
		m.report = nil
		m.mu.Unlock()
		return
	}
	files, state := m.read()
	if m.manage(ctx, files, want) {
		files, state = m.read()
	}
	tr := m.trafficTick(ctx, want)
	r := &LeiKwanHostReport{V: leiKwanHostVersion, At: m.now().UnixMilli(), State: state, Files: files, Manage: m.status, Traffic: tr}
	key := lkReportKey(r)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if key != m.lastKey || now.Sub(m.lastSent) >= lkReportEvery || (tr != nil && now.Sub(m.lastTrSnt) >= lkTrafficEvery) {
		m.due = true
	}
	m.lastKey = key
	m.report = r
	m.traffic = tr
}

// takeReport returns the report when it is due (the reporter sends it with
// the next system info, at most once a minute).
func (m *lkHostManager) takeReport() *LeiKwanHostReport {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.due || m.report == nil {
		return nil
	}
	m.due = false
	m.lastSent = m.now()
	if m.report.Traffic != nil {
		m.lastTrSnt = m.lastSent
	}
	return m.report
}

// handleSync stores the panel's wanted state, runs one check at once (≤ 15 s)
// and answers with the current report.
func (m *lkHostManager) handleSync(ctx context.Context, data interface{}) (*LeiKwanHostReport, error) {
	if m == nil {
		return nil, errors.New("利群主机模块未启用")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var s LeiKwanHostSync
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("同步数据无效")
	}
	if s.V != leiKwanHostVersion {
		return nil, fmt.Errorf("同步版本 %d 不支持", s.V)
	}
	done := make(chan struct{})
	m.mu.Lock()
	m.want = &s
	m.waiters = append(m.waiters, done)
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
	case <-ctx.Done():
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.report == nil {
		return nil, errors.New("这台机器没有利群客户端")
	}
	m.due = false
	m.lastSent = m.now()
	return m.report, nil
}
