package sublink

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Plan 226: the 国内直连 sing-box config downloads geosite-cn / geoip-cn from
// the panel (DIRECT, the address the subscription itself came from) instead
// of GitHub through 节点选择. sing-box refuses to start when a remote rule set
// can neither be loaded from its cache nor downloaded, so on fork.119 a first
// start with a dead node (or a first node that is dead) failed, and with
// 节点选择 = direct the mainland had to reach raw.githubusercontent.com.
//
// Plan 256: the same mechanism serves every 「分流」 catalog rule set
// (MetaCubeX meta-rules-dat) in the format each client needs:
//
//	<id>.mrs   mihomo (Clash Verge / FlClash / Clash Meta)   meta branch .mrs
//	<id>.srs   sing-box / Hiddify                            sing branch .srs
//	<id>.txt   Stash (format: text; Clash text list)         meta branch .list
//	<id>.list  Surge / Surfboard RULE-SET (classical lines)  derived from .txt
//
// The panel serves copies embedded at build time and refreshes each file
// from upstream at most once a day, on demand (no ticker), one download at a
// time; a failed or invalid download keeps the copy it has. A served name is
// never missing (embedded fallback), so a client never gets a 404 for a set
// its config references.

//go:embed ruleset
var ruleSetFiles embed.FS

// RuleSetPath is the public prefix: GET /s/rs/<name>.<ext>.
const RuleSetPath = "/s/rs/"

const (
	ruleSetRefresh  = 24 * time.Hour
	ruleSetMaxBytes = 4 << 20
	ruleSetTimeout  = 30 * time.Second

	// RuleSetInterval: how often clients re-download a rule set (seconds).
	RuleSetInterval = 86400

	metaRulesBase = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/"
)

// Served extensions.
const (
	ExtSRS  = "srs"
	ExtMRS  = "mrs"
	ExtText = "txt"
	ExtList = "list"
)

// RuleSetURL is where a client downloads rule set name (sing-box .srs) from
// the panel at origin.
func RuleSetURL(origin, name string) string { return RuleSetFileURL(origin, name, ExtSRS) }

// RuleSetFileURL is the panel URL of one served file.
func RuleSetFileURL(origin, name, ext string) string {
	return origin + RuleSetPath + name + "." + ext
}

// upstreamURL: where a served file is refreshed from ("" = not served).
func upstreamURL(name, ext string) string {
	if ext == ExtSRS {
		switch name {
		case "geosite-cn":
			return geositeCN
		case "geoip-cn":
			return geoipCN
		}
	}
	s, ok := catalogSets[name]
	if !ok {
		return ""
	}
	kind := "geosite"
	if s.IP {
		kind = "geoip"
	}
	switch ext {
	case ExtSRS:
		return metaRulesBase + "sing/geo/" + kind + "/" + s.Upstream + ".srs"
	case ExtMRS:
		return metaRulesBase + "meta/geo/" + kind + "/" + s.Upstream + ".mrs"
	case ExtText:
		return metaRulesBase + "meta/geo/" + kind + "/" + s.Upstream + ".list"
	}
	return ""
}

// validRuleSet: a binary sing-box rule set ("SRS" magic + version) of sane size.
func validRuleSet(b []byte) bool {
	return len(b) >= 64 && len(b) <= ruleSetMaxBytes && bytes.HasPrefix(b, []byte("SRS"))
}

// zstdMagic: .mrs files are a zstd stream.
var zstdMagic = []byte{0x28, 0xB5, 0x2F, 0xFD}

// validMRS: a mihomo binary rule set of sane size.
func validMRS(b []byte) bool {
	return len(b) >= 32 && len(b) <= ruleSetMaxBytes && bytes.HasPrefix(b, zstdMagic)
}

// cleanTextList checks a Clash text list (one domain pattern or CIDR per
// line) and returns it normalized: comments and blank lines dropped, CRLF →
// LF, invalid lines dropped. ok=false when it is not a list at all (HTML,
// binary, empty) or more than 2 % of the lines are invalid.
func cleanTextList(b []byte, ipSet bool) ([]byte, bool) {
	if len(b) == 0 || len(b) > ruleSetMaxBytes || !utf8.Valid(b) {
		return nil, false
	}
	var out bytes.Buffer
	out.Grow(len(b))
	good, bad := 0, 0
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		ok := false
		if ipSet {
			p, err := netip.ParsePrefix(line)
			ok = err == nil
			if ok {
				line = p.Masked().String()
			}
		} else {
			ok = validListDomain(line)
		}
		if !ok {
			bad++
			continue
		}
		good++
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if good == 0 || bad*50 > good+bad {
		return nil, false
	}
	return out.Bytes(), true
}

// validListDomain: +.example.com, .example.com, example.com or a wildcard
// pattern of lower-case letters, digits, '-', '_', '.', '*'.
func validListDomain(s string) bool {
	s = strings.TrimPrefix(s, "+.")
	if s == "" || len(s) > 253 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '*') {
			return false
		}
	}
	return true
}

// surgeList turns a cleaned Clash text list into Surge RULE-SET lines.
// Wildcard patterns other than a leading "+." / "." are skipped.
func surgeList(text []byte, ipSet bool) []byte {
	var out bytes.Buffer
	out.Grow(len(text) + len(text)/2)
	for _, line := range strings.Split(string(text), "\n") {
		if line == "" {
			continue
		}
		switch {
		case ipSet:
			if strings.Contains(line, ":") {
				out.WriteString("IP-CIDR6," + line + "\n")
			} else {
				out.WriteString("IP-CIDR," + line + "\n")
			}
		case strings.HasPrefix(line, "+."):
			if d := line[2:]; !strings.Contains(d, "*") {
				out.WriteString("DOMAIN-SUFFIX," + d + "\n")
			}
		case strings.HasPrefix(line, "."):
			if d := line[1:]; !strings.Contains(d, "*") {
				out.WriteString("DOMAIN-SUFFIX," + d + "\n")
			}
		case !strings.Contains(line, "*"):
			out.WriteString("DOMAIN," + line + "\n")
		}
	}
	return out.Bytes()
}

type ruleSet struct {
	name, ext string
	body      []byte
	etag      string
	checked   time.Time
	busy      bool
	// derived Surge list (ext txt only), rebuilt when the body changes.
	list     []byte
	listETag string
}

// RuleSets holds the served copies. The zero value is ready to use.
type RuleSets struct {
	// Fetch downloads url; nil = plain HTTP GET (tests replace it).
	Fetch func(ctx context.Context, url string) ([]byte, error)

	mu      sync.Mutex
	sets    map[string]*ruleSet
	queue   []*ruleSet
	running bool
}

func ruleSetETag(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:12]) + `"`
}

// embeddedRuleSet is the build-time copy of one served file.
func embeddedRuleSet(name, ext string) ([]byte, bool) {
	b, err := ruleSetFiles.ReadFile("ruleset/" + name + "." + ext)
	if err != nil {
		return nil, false
	}
	if ext == ExtText {
		s, ok := catalogSets[name]
		if !ok {
			return nil, false
		}
		return cleanTextList(b, s.IP)
	}
	return b, true
}

// Known reports whether the panel serves name.ext.
func (s *RuleSets) Known(name, ext string) bool {
	if ext == ExtList {
		ext = ExtText
	}
	return upstreamURL(name, ext) != ""
}

// Get returns the current copy of rule set name in format ext and its ETag;
// a copy older than a day queues one background refresh.
func (s *RuleSets) Get(name, ext string, now time.Time) ([]byte, string, bool) {
	src := ext
	if ext == ExtList {
		src = ExtText
	}
	url := upstreamURL(name, src)
	if url == "" {
		return nil, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sets == nil {
		s.sets = map[string]*ruleSet{}
	}
	key := name + "." + src
	rs := s.sets[key]
	if rs == nil {
		b, ok := embeddedRuleSet(name, src)
		if !ok {
			return nil, "", false
		}
		rs = &ruleSet{name: name, ext: src, body: b, etag: ruleSetETag(b)}
		s.sets[key] = rs
	}
	if !rs.busy && now.Sub(rs.checked) >= ruleSetRefresh {
		rs.busy, rs.checked = true, now
		s.queue = append(s.queue, rs)
		if !s.running {
			s.running = true
			go s.worker()
		}
	}
	if ext == ExtList {
		if rs.list == nil {
			rs.list = surgeList(rs.body, catalogSets[name].IP)
			rs.listETag = ruleSetETag(rs.list)
		}
		return rs.list, rs.listETag, true
	}
	return rs.body, rs.etag, true
}

// worker downloads queued refreshes one at a time and exits when the queue
// is empty (started on demand by Get).
func (s *RuleSets) worker() {
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.running = false
			s.mu.Unlock()
			return
		}
		rs := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		s.refresh(rs)
	}
}

func (s *RuleSets) refresh(rs *ruleSet) {
	ctx, cancel := context.WithTimeout(context.Background(), ruleSetTimeout)
	defer cancel()
	fetch := s.Fetch
	if fetch == nil {
		fetch = fetchRuleSet
	}
	b, err := fetch(ctx, upstreamURL(rs.name, rs.ext))
	ok := false
	if err == nil {
		switch rs.ext {
		case ExtSRS:
			ok = validRuleSet(b)
		case ExtMRS:
			ok = validMRS(b)
		case ExtText:
			b, ok = cleanTextList(b, catalogSets[rs.name].IP)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rs.busy = false
	if ok {
		rs.body, rs.etag = b, ruleSetETag(b)
		rs.list, rs.listETag = nil, ""
	}
}

func fetchRuleSet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, ruleSetMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > ruleSetMaxBytes {
		return nil, errors.New("rule set too large")
	}
	return b, nil
}
