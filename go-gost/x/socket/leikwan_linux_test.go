//go:build linux

package socket

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLeiKwanReadOnlyDetection(t *testing.T) {
	root := t.TempDir()
	put := func(path, data string) {
		t.Helper()
		p := filepath.Join(root, path)
		os.MkdirAll(filepath.Dir(p), 0700)
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := detectLeiKwan(root); got.Status != "absent" || len(got.Forwards) != 0 {
		t.Fatal(got)
	}
	put("etc/leikwan-client-agent/config.json", `not JSON; secret-token-must-not-be-read`)
	if got := detectLeiKwan(root); got.Status != "present" {
		t.Fatal(got)
	}
	put("etc/ultra/forward-263.json", `{"forward_id":263,"listen_port":10018,"token":"do-not-report","target":{"host":"private-target"}}`)
	put("etc/ultra/forward-264.json", `{"forward_id":264,"listen_port":10015}`)
	put("etc/ultra/forward-279.json", `{"forward_id":999,"listen_port":10019}`)
	put("etc/ultra/forward-280.json", `{"forward_id":280,"listen_port":70000}`)
	put("etc/ultra/forward-281.json", `bad JSON`)
	put("sys/fs/cgroup/system.slice/ultra-forward-263.service/cgroup.procs", "42\n")
	put("proc/net/tcp", "0: 00000000:2722 00000000:0000 0A\n1: 00000000:271F 00000000:0000 01\n")
	got := detectLeiKwan(root)
	if got.Status != "running" || len(got.Forwards) != 2 || got.Forwards[0].ForwardID != 263 || !got.Forwards[0].Running || got.Forwards[1].Running {
		t.Fatalf("%+v", got)
	}
	raw, _ := json.Marshal(got)
	for _, secret := range []string{"token", "target", "do-not-report", "private-target"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal(string(raw))
		}
	}
	put("sys/fs/cgroup/system.slice/ultra-forward-263.service/cgroup.procs", "")
	if got := detectLeiKwan(root); got.Status != "present" || got.Forwards[0].Running {
		t.Fatal(got)
	}
	put("sys/fs/cgroup/system.slice/leikwan-client-agent.service/cgroup.procs", "50\n")
	if got := detectLeiKwan(root); got.Status != "running" {
		t.Fatal(got)
	}
	// The detector never changes configuration or its permissions.
	raw, _ = os.ReadFile(filepath.Join(root, "etc/leikwan-client-agent/config.json"))
	if string(raw) != "not JSON; secret-token-must-not-be-read" {
		t.Fatal(string(raw))
	}
}
