package socket

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Where this agent is installed, for OTA and rollback.
//
// install.sh lays an agent out as /etc/<name>/<name> + /etc/<name>/config.json +
// /etc/systemd/system/<name>.service (WorkingDirectory=/etc/<name>) and writes
// "service_name": "<name>" into config.json. Hand-made or old installs can lack
// service_name; OTA then fell back to /etc/flvx_agent and the restart script
// stopped a unit that does not exist — a silent no-op (node 20 ran
// /etc/flvxx/flvxx). Hosts can also run another panel's agent under
// /etc/flux_agent (or /etc/flvx_agent): its config and unit must never be
// taken for ours.

const (
	defaultAgentName  = "flvx_agent"
	legacyAgentPrefix = "flux_agent"
)

// Overridden by tests.
var (
	agentEtcDir     = "/etc"
	agentSystemdDir = "/etc/systemd/system"
	agentExecutable = os.Executable
	// startAgentScript runs the restart script in its own transient unit, so
	// `systemctl stop` of the agent's cgroup does not kill the script itself.
	startAgentScript = func(script string) error {
		return exec.Command("systemd-run", "--quiet", "/bin/sh", "-c", script).Start()
	}
)

// Names derived from a path end up in a shell script.
var agentNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type agentInstall struct {
	Name   string // systemd unit, binary and directory name
	Dir    string // /etc/<name>
	Binary string // /etc/<name>/<name>
	Unit   string // /etc/systemd/system/<name>.service
	Source string // "service_name", "executable" or "default"
}

func agentInstallFor(name, source string) agentInstall {
	dir := filepath.Join(agentEtcDir, name)
	return agentInstall{Name: name, Dir: dir, Binary: filepath.Join(dir, name),
		Unit: filepath.Join(agentSystemdDir, name+".service"), Source: source}
}

// resolvedPath cleans p and resolves symlinks when it exists.
func resolvedPath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// runningAgentPath is the running binary with symlinks resolved, "" if unknown.
// os.Executable reads /proc/self/exe (UPX-packed release binaries included) and
// drops a " (deleted)" suffix after the file was replaced.
func runningAgentPath() string {
	exe, err := agentExecutable()
	if err != nil || exe == "" {
		return ""
	}
	return resolvedPath(exe)
}

// executableAgentName returns <name> for a binary at /etc/<name>/<name>. A
// flux_agent* name never qualifies: that legacy layout is migrated only for an
// explicit service_name, and the same path may hold another panel's agent.
func executableAgentName(exe string) string {
	if exe == "" {
		return ""
	}
	name := filepath.Base(exe)
	dir := filepath.Dir(exe)
	if filepath.Base(dir) != name || filepath.Dir(dir) != resolvedPath(agentEtcDir) {
		return ""
	}
	if !agentNameRe.MatchString(name) || strings.HasPrefix(name, legacyAgentPrefix) {
		return ""
	}
	return name
}

func isRegularFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// resolveAgentInstall picks the install OTA and rollback act on: an explicit
// service_name always wins (incl. the flux_agent* migration); without one the
// running binary decides when it sits at /etc/<n>/<n> and <n>.service exists;
// otherwise the old default flvx_agent.
func resolveAgentInstall(serviceName string) agentInstall {
	if serviceName != "" {
		return agentInstallFor(serviceName, "service_name")
	}
	if name := executableAgentName(runningAgentPath()); name != "" {
		if in := agentInstallFor(name, "executable"); isRegularFile(in.Unit) {
			return in
		}
	}
	return agentInstallFor(defaultAgentName, "default")
}

// check says why OTA / rollback cannot restart this install. The restart
// script edits the unit file in place, so a missing one would leave the agent
// stopped (or, for a unit that never existed, do nothing at all).
func (in agentInstall) check() error {
	if !isRegularFile(in.Unit) {
		return fmt.Errorf("找不到 systemd 服务文件 %s（config.json service_name=%q，运行中的程序 %s），无法自动重启，请在 config.json 写入正确的 service_name 或用安装脚本重装",
			in.Unit, serviceNameOf(in), describePath(runningAgentPath()))
	}
	if in.Source == "default" {
		// No service_name and the running binary is not the default install:
		// flvx_agent.service then belongs to some other agent on this host.
		if exe := runningAgentPath(); exe != "" && exe != resolvedPath(in.Binary) {
			return fmt.Errorf("config.json 没有 service_name，运行中的程序 %s 不是 %s，为免误动其他服务，未自动升级/回退，请在 config.json 写入 service_name",
				exe, in.Binary)
		}
	}
	return nil
}

func serviceNameOf(in agentInstall) string {
	if in.Source == "service_name" {
		return in.Name
	}
	return ""
}

func describePath(p string) string {
	if p == "" {
		return "未知"
	}
	return p
}

// agentConfigPaths lists where the agent config is read from: the working
// directory (the unit sets WorkingDirectory=/etc/<name>), then the legacy
// default dirs — but a legacy dir only when the running binary lives there (or
// the binary is unknown), so a co-installed agent of another panel never lends
// us its config or service_name.
func agentConfigPaths() []string {
	paths := []string{"config.json"}
	exe := runningAgentPath()
	for _, name := range []string{defaultAgentName, legacyAgentPrefix} {
		dir := filepath.Join(agentEtcDir, name)
		if exe == "" || filepath.Dir(exe) == resolvedPath(dir) {
			paths = append(paths, filepath.Join(dir, "config.json"))
		}
	}
	return paths
}

// upgradeProgressObserver lets tests see OTA / rollback progress messages.
var upgradeProgressObserver func(stage string, percent int, message string)
