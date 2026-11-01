// Package probe builds the public 「探针」 status page data (plan 187).
//
// Everything that leaves this package for anonymous viewers is one of the
// whitelist structs below: they are never internal models, so a new column on
// a node can never leak by accident. No IP, address, port, node id, secret,
// agent version, user / tunnel / rule / landing data, and no internal error
// strings. The package only reads what the caller hands it; it cannot reach
// the agent control plane or write anything.
package probe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"math"
	"strconv"
	"strings"
	"sync"
)

// Snapshot is the whole live payload: one per tick, shared by every viewer.
type Snapshot struct {
	Title     string `json:"title"`
	TS        int64  `json:"ts"`
	LatencyOn bool   `json:"latency_on"`
	Nodes     []Node `json:"nodes"`
	// Plan 189 custom groups: only groups with ≥ 1 shown node, in the admin's
	// order; both fields are omitted when no group is published.
	GroupMode string  `json:"group_mode,omitempty"`
	Groups    []Group `json:"groups,omitempty"`
}

// Group is a custom group as the page sees it: just its name. Nodes point at
// it by index (Node.G), never by any internal id.
type Group struct {
	Name string `json:"name"`
}

// Group modes (admin default; the visitor may switch on the page).
const (
	GroupModeRegion = "region"
	GroupModeGroup  = "group"
)

// Node is one server card. Metrics are zero while the node is offline (the
// page shows 「最后在线 …」 from Seen instead).
type Node struct {
	K          string    `json:"k"`
	Name       string    `json:"name"`
	Region     string    `json:"region"`
	Online     bool      `json:"online"`
	OS         string    `json:"os"`
	Arch       string    `json:"arch"`
	Seen       int64     `json:"seen"`
	CPU        float64   `json:"cpu"`
	Load1      float64   `json:"load1"`
	Mem        float64   `json:"mem"`
	Disk       float64   `json:"disk"`
	NetIn      int64     `json:"net_in"`
	NetOut     int64     `json:"net_out"`
	TrafficIn  int64     `json:"traffic_in"`
	TrafficOut int64     `json:"traffic_out"`
	Uptime     int64     `json:"uptime"`
	TCP        int64     `json:"tcp"`
	Latency    []Latency `json:"latency,omitempty"`
	// G: indexes into Snapshot.Groups (plan 189), omitted when in none.
	G []int `json:"g,omitempty"`
}

// Latency is the newest measured TCP latency from this node to another shown
// node (target = that node's key; the page shows its display name).
type Latency struct {
	To string  `json:"to"`
	MS float64 `json:"ms"`
	OK bool    `json:"ok"`
}

// Series is the detail view history of one node.
type Series struct {
	K     string `json:"k"`
	Range string `json:"range"`
	Step  int64  `json:"step"`
	// Points: [ts ms, cpu %, mem %, net in B/s, net out B/s, tcp].
	Points  [][6]float64    `json:"points"`
	Latency []LatencySeries `json:"latency,omitempty"`
}

// LatencySeries: [ts ms, ms] per round, ms < 0 = that round failed.
type LatencySeries struct {
	To     string       `json:"to"`
	Points [][2]float64 `json:"points"`
}

// Round1 keeps one decimal: smaller frames, no jitter in the last digits.
func Round1(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10) / 10
}

// Keyer maps node ids to opaque public keys. HMAC with a panel secret: stable
// across restarts (same JWT secret), not guessable, not reversible.
type Keyer struct {
	secret []byte
	mu     sync.Mutex
	keys   map[int64]string
	ids    map[int64]int64
}

func NewKeyer(secret string) *Keyer {
	return &Keyer{secret: []byte("flvx-probe:" + secret), keys: map[int64]string{}, ids: map[int64]int64{}}
}

func (k *Keyer) mac(id int64) []byte {
	m := hmac.New(sha256.New, k.secret)
	m.Write([]byte("node:" + strconv.FormatInt(id, 10)))
	return m.Sum(nil)
}

var keyEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// Key is the 10-character public key of a node (50 bits).
func (k *Keyer) Key(id int64) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if v, ok := k.keys[id]; ok {
		return v
	}
	v := keyEncoding.EncodeToString(k.mac(id))[:10]
	if len(k.keys) > 4096 { // node ids only; bound anyway
		k.keys = map[int64]string{}
	}
	k.keys[id] = v
	return v
}

// PublicID is an opaque positive number (< 2^53, JS-safe) standing in for a
// node id on the old public endpoints (/monitor/public/*, /system-info type=2).
func (k *Keyer) PublicID(id int64) int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	if v, ok := k.ids[id]; ok {
		return v
	}
	sum := k.mac(id)
	v := int64(binary.BigEndian.Uint64(sum[8:16])>>12) + 1_000_000
	if len(k.ids) > 4096 {
		k.ids = map[int64]int64{}
	}
	k.ids[id] = v
	return v
}

// ParseOSArch reads 「(distro/arch)」 from the agent version string
// ("3.0.27-fork.103 (debian/amd64)"); only [a-z0-9._-] survive, so the agent
// version itself never reaches the page.
func ParseOSArch(version string) (string, string) {
	open := strings.LastIndexByte(version, '(')
	if open < 0 {
		return "", ""
	}
	inner := version[open+1:]
	if end := strings.IndexByte(inner, ')'); end >= 0 {
		inner = inner[:end]
	} else {
		return "", ""
	}
	parts := strings.SplitN(inner, "/", 2)
	osName := cleanToken(parts[0])
	arch := ""
	if len(parts) == 2 {
		arch = cleanToken(parts[1])
	}
	return osName, arch
}

func cleanToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
		if b.Len() >= 16 {
			break
		}
	}
	return b.String()
}

// CleanRegion keeps a two-letter region code (upper case) or nothing.
func CleanRegion(code string) string {
	c := strings.ToUpper(strings.TrimSpace(code))
	if len(c) != 2 || c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' {
		return ""
	}
	return c
}
