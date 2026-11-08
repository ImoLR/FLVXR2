// Package dnscache ships flvx-dns-cache (plan 240): the node's local DNS cache script (embedded, the
// plan-239 recipe) and the agent's cheap local check of it.
//
// The agent never changes system DNS on its own. The script runs only from the node installer
// (`flvx_agent dns-cache ensure`), from the panel's 「修复 DNS 缓存」 button and when a node becomes an
// FLVX native landing (both via the DNSCacheEnsure command). The periodic check reads two small files and
// /proc, at most once per 10 minutes, with no network traffic.
package dnscache

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

//go:embed flvx-dns-cache.sh
var Script []byte

const (
	// ScriptPath is where the agent installs the script (also handy for admins: flvx-dns-cache status).
	ScriptPath = "/usr/local/sbin/flvx-dns-cache"
	// CapVersion is announced in the websocket handshake (dnsCache=N); the panel only sends DNSCacheEnsure
	// to agents that announce it.
	CapVersion = 1
	// EnsureTimeout bounds one ensure run (apt install on a slow mirror included). The panel waits a bit longer.
	EnsureTimeout = 200 * time.Second

	outFile  = "/run/flvx-dns-cache.out"
	unitName = "flvx-dns-cache-ensure"
	maxOut   = 64 << 10
)

// ErrBusy is returned when another ensure run is still active on this machine.
var ErrBusy = errors.New("DNS 缓存正在配置中，请稍候再试")

// InstallScript writes the embedded script to ScriptPath when it is missing or different.
func InstallScript() error { return installScriptAt(ScriptPath) }

func installScriptAt(path string) error {
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, Script) {
		return os.Chmod(path, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, Script, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CLI runs `flvx_agent dns-cache <args>`: installs the script and replaces the process with it, so the
// installer (and an admin on the shell) sees its output and exit code directly.
func CLI(args []string) int {
	if err := InstallScript(); err != nil {
		fmt.Fprintf(os.Stderr, "flvx-dns-cache: cannot install %s: %v\n", ScriptPath, err)
		return 1
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		fmt.Fprintln(os.Stderr, "flvx-dns-cache: bash not found")
		return 1
	}
	argv := append([]string{"bash", ScriptPath}, args...)
	if err := syscall.Exec(bash, argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "flvx-dns-cache: %v\n", err)
		return 1
	}
	return 0
}

var zoneRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// ValidZones keeps well-formed DNS names (lower-cased, deduplicated, at most 12). IP literals and anything
// with other characters are dropped, so nothing odd ever reaches the script's command line.
func ValidZones(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, z := range in {
		z = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(z)), ".")
		if len(z) == 0 || len(z) > 253 || !zoneRe.MatchString(z) || isNumeric(z) || seen[z] {
			continue
		}
		seen[z] = true
		out = append(out, z)
		if len(out) == 12 {
			break
		}
	}
	return out
}

func isNumeric(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

// Ensure runs `flvx-dns-cache ensure [--zones ...]` once and returns the script output (tail). It runs as a
// transient systemd unit (systemd-run --wait), outside the agent's cgroup, so an agent restart or OTA cannot
// cut a package install in half; without systemd-run it runs as a child process. A non-zero exit of the
// script is not an error here: the caller reads the outcome from Check().
func Ensure(ctx context.Context, zones []string) (string, error) {
	if err := InstallScript(); err != nil {
		return "", err
	}
	args := []string{ScriptPath, "ensure"}
	if z := ValidZones(zones); len(z) > 0 {
		args = append(args, "--zones", strings.Join(z, " "))
	}
	if sr, err := exec.LookPath("systemd-run"); err == nil {
		_ = os.Remove(outFile)
		cmdArgs := append([]string{"--quiet", "--wait", "--collect", "--unit=" + unitName,
			"-p", "StandardOutput=file:" + outFile, "-p", "StandardError=inherit", "/bin/bash"}, args...)
		out, runErr := exec.CommandContext(ctx, sr, cmdArgs...).CombinedOutput()
		if bytes.Contains(out, []byte("already exists")) || bytes.Contains(out, []byte("already loaded")) {
			return "", ErrBusy
		}
		res, _ := readTail(outFile)
		if res == "" && runErr != nil && !isExit(runErr) {
			return strings.TrimSpace(string(out)), fmt.Errorf("systemd-run: %v", runErr)
		}
		return res, nil
	}
	out, err := exec.CommandContext(ctx, "/bin/bash", args...).CombinedOutput()
	if err != nil && !isExit(err) {
		return tail(string(out)), err
	}
	return tail(string(out)), nil
}

func isExit(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

func readTail(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return tail(string(b)), nil
}

// tail keeps the last 30 lines (at most maxOut bytes) of the script output.
func tail(s string) string {
	if len(s) > maxOut {
		s = s[len(s)-maxOut:]
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	return strings.Join(lines, "\n")
}

// Summary returns the script's one-line Chinese summary ("DNS 缓存：…") from an ensure output.
func Summary(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "DNS 缓存：") {
			return lines[i]
		}
	}
	return ""
}
