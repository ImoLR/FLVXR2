//go:build linux

package socket

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func detectLeiKwan(root string) *LeiKwanReport {
	path := func(name string) string { return filepath.Join(root, name) }
	exists := func(name string) bool { _, err := os.Stat(path(name)); return err == nil }
	active := func(unit string) bool {
		raw, err := os.ReadFile(path("sys/fs/cgroup/system.slice/" + unit + "/cgroup.procs"))
		return err == nil && strings.TrimSpace(string(raw)) != ""
	}
	report := &LeiKwanReport{Status: "absent", Forwards: []LeiKwanForward{}}
	if exists("etc/leikwan-client-agent/config.json") || exists("etc/systemd/system/leikwan-client-agent.service") {
		report.Status = "present"
	}
	if active("leikwan-client-agent.service") {
		report.Status = "running"
	}
	files, _ := filepath.Glob(path("etc/ultra/forward-*.json"))
	if len(files) == 0 {
		return report
	}
	listeners := make(map[int]bool)
	for _, name := range []string{"proc/net/tcp", "proc/net/tcp6"} {
		f, err := os.Open(path(name))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(io.LimitReader(f, 4<<20))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 || fields[3] != "0A" {
				continue
			}
			addr := strings.Split(fields[1], ":")
			if len(addr) != 2 {
				continue
			}
			port, err := strconv.ParseUint(addr[1], 16, 16)
			if err == nil {
				listeners[int(port)] = true
			}
		}
		f.Close()
	}
	for _, name := range files {
		id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(name), "forward-"), ".json"), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		if report.Status == "absent" {
			report.Status = "present"
		}
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		// Only these two public fields are decoded. Tokens, targets and all other
		// configuration fields are discarded, never retained or sent to the panel.
		var cfg struct {
			ID   int64 `json:"forward_id"`
			Port int   `json:"listen_port"`
		}
		err = json.NewDecoder(io.LimitReader(f, 256<<10)).Decode(&cfg)
		f.Close()
		if err != nil || cfg.ID != id || cfg.Port < 1 || cfg.Port > 65535 {
			continue
		}
		running := listeners[cfg.Port] && active("ultra-forward-"+strconv.FormatInt(id, 10)+".service")
		if running {
			report.Status = "running"
		}
		report.Forwards = append(report.Forwards, LeiKwanForward{ForwardID: id, ListenPort: cfg.Port, Running: running})
		if len(report.Forwards) == 512 {
			break
		}
	}
	sort.Slice(report.Forwards, func(i, j int) bool { return report.Forwards[i].ForwardID < report.Forwards[j].ForwardID })
	return report
}
