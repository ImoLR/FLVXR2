package machineprotect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/go-gost/x/atomicfile"
)

// PortSet holds sorted, de-duplicated business ports per transport.
type PortSet struct {
	TCP []int
	UDP []int
}

// Len is the number of port entries (a port on both transports counts twice).
func (p PortSet) Len() int { return len(p.TCP) + len(p.UDP) }

// Equal compares two normalized sets.
func (p PortSet) Equal(o PortSet) bool {
	return equalInts(p.TCP, o.TCP) && equalInts(p.UDP, o.UDP)
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// PortCollector gathers business ports from all sources of one sample.
type PortCollector struct {
	tcp map[int]struct{}
	udp map[int]struct{}
}

func NewPortCollector() *PortCollector {
	return &PortCollector{tcp: map[int]struct{}{}, udp: map[int]struct{}{}}
}

func (c *PortCollector) AddTCP(port int) {
	if port > 0 && port <= 65535 {
		c.tcp[port] = struct{}{}
	}
}

func (c *PortCollector) AddUDP(port int) {
	if port > 0 && port <= 65535 {
		c.udp[port] = struct{}{}
	}
}

// Set returns the normalized result.
func (c *PortCollector) Set() PortSet {
	return PortSet{TCP: sortedKeys(c.tcp), UDP: sortedKeys(c.udp)}
}

func sortedKeys(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// Ports seen again within this window are kept, so a WebSocket reconnect (the
// panel redeploys every rule) or a service restart does not shrink and regrow
// the nft sets and the Helper's port file.
const portRemovalGrace = 30 * time.Second

// portMemory applies portRemovalGrace to successive samples.
type portMemory struct {
	tcp map[int]time.Time
	udp map[int]time.Time
}

func (m *portMemory) merge(now time.Time, live PortSet) PortSet {
	if m.tcp == nil {
		m.tcp, m.udp = map[int]time.Time{}, map[int]time.Time{}
	}
	keep := func(seen map[int]time.Time, ports []int) []int {
		for _, p := range ports {
			seen[p] = now
		}
		out := make([]int, 0, len(seen))
		for p, at := range seen {
			if now.Sub(at) < portRemovalGrace && !now.Before(at) {
				out = append(out, p)
			} else {
				delete(seen, p)
			}
		}
		sort.Ints(out)
		return out
	}
	return PortSet{TCP: keep(m.tcp, live.TCP), UDP: keep(m.udp, live.UDP)}
}

// The Helper ignores the file when it is older than 10 min, so an unchanged set
// is rewritten well before that.
const portsFileRefresh = 4 * time.Minute

type businessPortsFile struct {
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at"`
	TCP       []int  `json:"tcp"`
	UDP       []int  `json:"udp"`
}

// portsFileWriter writes /run/flvx/business-ports.json on change, on refresh and
// when the file vanished. Directory 0755, file 0644, atomic rename.
type portsFileWriter struct {
	path    string
	written PortSet
	at      time.Time
	ok      bool
}

func newPortsFileWriter(runDir string) *portsFileWriter {
	return &portsFileWriter{path: filepath.Join(runDir, BusinessPortsRelPath)}
}

func (w *portsFileWriter) sync(set PortSet, now time.Time) error {
	if w.ok && set.Equal(w.written) && now.Sub(w.at) < portsFileRefresh && !now.Before(w.at) {
		if _, err := os.Stat(w.path); err == nil {
			return nil
		}
	}
	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	doc := businessPortsFile{Version: 1, UpdatedAt: now.UTC().Format(time.RFC3339), TCP: set.TCP, UDP: set.UDP}
	if doc.TCP == nil {
		doc.TCP = []int{}
	}
	if doc.UDP == nil {
		doc.UDP = []int{}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(w.path, append(b, '\n'), 0o644); err != nil {
		w.ok = false
		return err
	}
	// atomicfile keeps the mode of an existing file; the contract says 0644.
	if err := os.Chmod(w.path, 0o644); err != nil {
		return err
	}
	w.written, w.at, w.ok = set, now, true
	return nil
}
