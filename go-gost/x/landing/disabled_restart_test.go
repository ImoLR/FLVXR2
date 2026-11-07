package landing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Plan 236 check: a landing node the panel disabled (expired user, quota,
// paused) stays closed after an agent or Core restart. The agent restores
// desired.json (enabled flag per node) and the static Core config has no
// inbounds — only enabled nodes are added through the API afterwards.
func TestDisabledLandingNodeStaysClosedAfterRestart(t *testing.T) {
	dir := t.TempDir()
	on, off := testNode(t, 1), testNode(t, 2)
	off.Enabled = false
	m := NewManager(dir, true)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.writeJSON("desired.json", Desired{Revision: 3, Enabled: true, Nodes: []Node{on, off}}); err != nil {
		t.Fatal(err)
	}
	// Restart: what the manager loop does with desired.json on boot.
	raw, err := os.ReadFile(filepath.Join(dir, "desired.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d Desired
	if err := json.Unmarshal(raw, &d); err != nil || d.validate() != nil {
		t.Fatalf("restored desired: %v / %v", err, d.validate())
	}
	pub := d.publicPorts()
	has := func(list []int, p int) bool {
		for _, v := range list {
			if v == p {
				return true
			}
		}
		return false
	}
	if !has(pub.TCP, on.Port) || has(pub.TCP, off.Port) || has(pub.UDP, off.Port) {
		t.Fatalf("public ports after restart %+v (disabled %d)", pub, off.Port)
	}
	inb := *d.inboundPorts()
	if _, ok := inb[off.Port]; ok {
		t.Fatalf("disabled node inbound after restart: %v", inb)
	}
	if _, ok := inb[on.Port]; !ok {
		t.Fatalf("enabled node missing after restart: %v", inb)
	}
	if n := len(baseConfig(1234)["inbounds"].([]any)); n != 0 {
		t.Fatalf("static Core config has %d inbounds", n)
	}
}
