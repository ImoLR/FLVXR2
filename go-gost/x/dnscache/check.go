package dnscache

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Status is what the agent reports as dns_cache (plan 240).
type Status struct {
	Mode      string `json:"mode"` // dnsmasq | resolved | minimal | none
	OK        bool   `json:"ok"`
	Reason    string `json:"reason,omitempty"`
	CheckedAt int64  `json:"checked_at,omitempty"` // unix ms of the local check
}

type checker struct {
	resolvConf string
	state      string
	conf       string
	proc       string
}

var defaultChecker = checker{
	resolvConf: "/etc/resolv.conf",
	state:      "/etc/flvx-dnscache/state",
	conf:       "/etc/flvx-dnscache/dnsmasq.conf",
	proc:       "/proc",
}

// Check is the agent's local check: resolv.conf points at a local cache and that cache runs. It reads
// /etc/resolv.conf, the script's small state file and /proc (no exec, no network). Keep the rules in sync
// with do_check in flvx-dns-cache.sh.
func Check() Status {
	s := defaultChecker.check()
	s.CheckedAt = time.Now().UnixMilli()
	return s
}

func (c checker) check() Status {
	raw, err := os.ReadFile(c.resolvConf)
	if err != nil {
		return Status{Mode: "none", Reason: "没有 /etc/resolv.conf"}
	}
	ns, opts := parseResolv(raw)
	stMode, stReason := readState(c.state)
	if len(ns) == 0 {
		return Status{Mode: "none", Reason: "resolv.conf 里没有 nameserver"}
	}
	switch ns[0] {
	case "127.0.0.1", "::1":
		if c.running("dnsmasq", c.conf) {
			return Status{Mode: "dnsmasq", OK: true}
		}
		if stMode == "minimal" {
			return Status{Mode: "minimal", OK: true, Reason: stReason}
		}
		return Status{Mode: "dnsmasq", Reason: "本机缓存 flvx-dnscache 未运行"}
	case "127.0.0.53":
		if c.running("systemd-resolve", "") || c.running("systemd-resolved", "") {
			return Status{Mode: "resolved", OK: true}
		}
		return Status{Mode: "resolved", Reason: "systemd-resolved 未运行"}
	}
	if len(ns) >= 2 && shortTimeout(opts) {
		if stReason == "" {
			stReason = "未装本机缓存（只有备用解析器 + 1 秒超时）"
		}
		return Status{Mode: "minimal", OK: true, Reason: stReason}
	}
	if len(ns) < 2 {
		return Status{Mode: "none", Reason: "未启用本机 DNS 缓存（只有 1 个解析器）"}
	}
	return Status{Mode: "none", Reason: "未启用本机 DNS 缓存（解析超时仍是 5 秒）"}
}

func parseResolv(raw []byte) (ns, opts []string) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || strings.HasPrefix(f[0], "#") || strings.HasPrefix(f[0], ";") {
			continue
		}
		switch f[0] {
		case "nameserver":
			ns = append(ns, f[1])
		case "options":
			opts = append(opts, f[1:]...)
		}
	}
	return ns, opts
}

func shortTimeout(opts []string) bool {
	for _, o := range opts {
		if o == "timeout:1" || o == "timeout:2" {
			return true
		}
	}
	return false
}

func readState(path string) (mode, reason string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "mode":
			mode = strings.TrimSpace(v)
		case "reason":
			reason = strings.TrimSpace(v)
		}
	}
	if len(reason) > 300 {
		reason = reason[:300]
	}
	return mode, reason
}

// running reports whether a process with this comm runs (and, if argContains is set, has it in its
// command line). One pass over /proc/*/comm; cmdline is read only for comm matches.
func (c checker) running(comm, argContains string) bool {
	entries, err := os.ReadDir(c.proc)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if name == "" || name[0] < '0' || name[0] > '9' {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.proc, name, "comm"))
		if err != nil || strings.TrimSpace(string(b)) != comm {
			continue
		}
		if argContains == "" {
			return true
		}
		cl, err := os.ReadFile(filepath.Join(c.proc, name, "cmdline"))
		if err == nil && bytes.Contains(cl, []byte(argContains)) {
			return true
		}
	}
	return false
}
