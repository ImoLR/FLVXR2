// Package sublink renders plan-185 user subscriptions: the node model shared
// by every output format, the item references a subscription stores, the
// User-Agent → format table and the Clash (mihomo) / sing-box / Base64
// generators. It is pure (no database, no network).
package sublink

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"

	"go-backend/internal/linechain"
)

// Output formats and rule modes.
const (
	FormatAuto    = "auto"
	FormatClash   = "clash"
	FormatSingBox = "singbox"
	FormatBase64  = "base64"

	ModeCNDirect = "cn_direct"
	ModeGlobal   = "global"

	GroupSelect = "节点选择"
	GroupAuto   = "自动选择"
	TestURL     = "https://www.gstatic.com/generate_204"
)

// ValidFormat / ValidMode guard stored values.
func ValidFormat(f string) bool {
	switch f {
	case FormatAuto, FormatClash, FormatSingBox, FormatBase64:
		return true
	}
	return false
}

func ValidMode(m string) bool { return m == ModeCNDirect || m == ModeGlobal }

// Node is one proxy of a rendered subscription. Standard protocols live in
// Up (linechain.Upstream); FLVX landing protocols without a linechain type
// (AnyTLS / Snell / Mieru) use Extra instead.
type Node struct {
	Name string
	Up   linechain.Upstream
	// Insecure: skip certificate verification (airport nodes with
	// self-signed certificates from external subscriptions only).
	Insecure bool
	Extra    *Extra
	// URI: the share link FLVX already gives for this node (landing / rule
	// links); Base64 output uses it with the final name.
	URI string
}

// Extra holds an AnyTLS / Snell / Mieru landing node.
type Extra struct {
	Type       string // anytls | snell | mieru
	Server     string
	Port       int
	Username   string
	Password   string
	SNI        string
	CertSHA256 string // AnyTLS self-signed certificate pin (Clash)
	CertPEM    string // AnyTLS certificate (sing-box trusts it directly)
	Version    int    // Snell
	Obfs       string // Snell: http | tls
	ObfsHost   string
	Transport  string // Mieru: TCP | UDP
	UDP        bool
}

// Type is the node's protocol key.
func (n Node) Type() string {
	if n.Extra != nil {
		return n.Extra.Type
	}
	return n.Up.Type
}

// Server / Port of the node.
func (n Node) Server() string {
	if n.Extra != nil {
		return n.Extra.Server
	}
	return n.Up.Server
}

func (n Node) Port() int {
	if n.Extra != nil {
		return n.Extra.Port
	}
	return n.Up.Port
}

// ProtocolLabel is the short protocol name shown in the picker.
func (n Node) ProtocolLabel() string {
	if n.Extra != nil {
		switch n.Extra.Type {
		case "anytls":
			return "AnyTLS"
		case "snell":
			return "Snell"
		case "mieru":
			return "Mieru"
		}
		return n.Extra.Type
	}
	return n.Up.ProtocolLabel()
}

// Item reference kinds stored in a subscription.
const (
	RefForwardLink   = "forward_link"
	RefLandingDirect = "landing_direct"
	RefExternalNode  = "external_node"
	RefSourceNode    = "source_node"
	RefSourceAll     = "source_all"
)

// Ref points at one node (or a whole source) of the owner.
type Ref struct {
	Kind       string `json:"kind"`
	ForwardID  int64  `json:"forwardId,omitempty"`
	Entry      string `json:"entry,omitempty"` // forward_link: "<entryNodeId>|<host>"
	LandingID  int64  `json:"landingId,omitempty"`
	ExternalID int64  `json:"externalId,omitempty"`
	SourceID   int64  `json:"sourceId,omitempty"`
	Name       string `json:"name,omitempty"` // source_node: node name
}

// Key identifies a reference (the same node twice is one item).
func (r Ref) Key() string {
	switch r.Kind {
	case RefForwardLink:
		return "f:" + strconv.FormatInt(r.ForwardID, 10) + ":" + r.Entry
	case RefLandingDirect:
		return "l:" + strconv.FormatInt(r.LandingID, 10)
	case RefExternalNode:
		return "e:" + strconv.FormatInt(r.ExternalID, 10)
	case RefSourceNode:
		return "s:" + strconv.FormatInt(r.SourceID, 10) + ":" + r.Name
	case RefSourceAll:
		return "a:" + strconv.FormatInt(r.SourceID, 10)
	}
	return ""
}

// EntryKey builds a forward_link entry key.
func EntryKey(nodeID int64, host string) string {
	return strconv.FormatInt(nodeID, 10) + "|" + strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
}

// MaxItems bounds the references of one subscription.
const MaxItems = 300

// CleanRefs validates and dedupes references (order kept). Unknown kinds or
// empty ids are an error: they only come from a broken client.
func CleanRefs(refs []Ref) ([]Ref, error) {
	if len(refs) > MaxItems {
		return nil, errors.New("订阅里的节点太多（最多 " + strconv.Itoa(MaxItems) + " 个）")
	}
	out := make([]Ref, 0, len(refs))
	seen := map[string]bool{}
	for _, r := range refs {
		c := Ref{Kind: r.Kind}
		switch r.Kind {
		case RefForwardLink:
			c.ForwardID, c.Entry = r.ForwardID, strings.TrimSpace(r.Entry)
			if c.ForwardID <= 0 || c.Entry == "" || len(c.Entry) > 300 {
				return nil, errors.New("节点引用无效")
			}
		case RefLandingDirect:
			c.LandingID = r.LandingID
			if c.LandingID <= 0 {
				return nil, errors.New("节点引用无效")
			}
		case RefExternalNode:
			c.ExternalID = r.ExternalID
			if c.ExternalID <= 0 {
				return nil, errors.New("节点引用无效")
			}
		case RefSourceNode:
			c.SourceID, c.Name = r.SourceID, r.Name
			if c.SourceID <= 0 || c.Name == "" || len(c.Name) > 200 {
				return nil, errors.New("节点引用无效")
			}
		case RefSourceAll:
			c.SourceID = r.SourceID
			if c.SourceID <= 0 {
				return nil, errors.New("节点引用无效")
			}
		default:
			return nil, errors.New("节点引用无效")
		}
		if k := c.Key(); !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// ParseRefs reads stored items; broken JSON yields an empty list.
func ParseRefs(s string) []Ref {
	var refs []Ref
	if json.Unmarshal([]byte(s), &refs) != nil {
		return nil
	}
	return refs
}

// RefsJSON is the stored form.
func RefsJSON(refs []Ref) string {
	if refs == nil {
		refs = []Ref{}
	}
	b, _ := json.Marshal(refs)
	return string(b)
}

var circled = []string{"①", "②", "③", "④", "⑤", "⑥", "⑦", "⑧", "⑨", "⑩", "⑪", "⑫", "⑬", "⑭", "⑮", "⑯", "⑰", "⑱", "⑲", "⑳"}

// reservedNames are proxy-group / built-in names a node may not take.
var reservedNames = map[string]bool{GroupSelect: true, GroupAuto: true, "DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "COMPATIBLE": true, "GLOBAL": true, "direct": true}

// FinalizeNames gives every node a non-empty unique name: names that occur
// more than once get ①②… in order. Nodes are changed in place.
func FinalizeNames(nodes []Node) {
	count := map[string]int{}
	for i := range nodes {
		name := strings.TrimSpace(nodes[i].Name)
		if name == "" {
			name = nodes[i].ProtocolLabel() + " " + nodes[i].Server()
		}
		if reservedNames[name] {
			name += " · 节点"
		}
		nodes[i].Name = name
		count[name]++
	}
	seq := map[string]int{}
	used := map[string]bool{}
	for i := range nodes {
		name := nodes[i].Name
		if count[name] > 1 {
			seq[name]++
			n := seq[name]
			suffix := "(" + strconv.Itoa(n) + ")"
			if n <= len(circled) {
				suffix = circled[n-1]
			}
			name = name + " " + suffix
		}
		for used[name] {
			name += "'"
		}
		used[name] = true
		nodes[i].Name = name
	}
}

const tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// TokenLength of a subscription token (base62, ~131 bits).
const TokenLength = 22

// NewToken returns a random subscription token.
func NewToken() (string, error) {
	b := make([]byte, TokenLength)
	max := big.NewInt(int64(len(tokenAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = tokenAlphabet[n.Int64()]
	}
	return string(b), nil
}

// ValidToken checks a token's shape before any lookup.
func ValidToken(t string) bool {
	if len(t) < TokenLength || len(t) > 64 {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// SourceNode is one cached node of an external subscription source.
type SourceNode struct {
	Name     string             `json:"n"`
	Up       linechain.Upstream `json:"u"`
	Insecure bool               `json:"i,omitempty"`
}

// ParseSourceNodes reads a source's cache; broken JSON yields nothing.
func ParseSourceNodes(s string) []SourceNode {
	var out []SourceNode
	if json.Unmarshal([]byte(s), &out) != nil {
		return nil
	}
	return out
}

// Node converts a cached source node.
func (s SourceNode) Node() Node { return Node{Name: s.Name, Up: s.Up, Insecure: s.Insecure} }
