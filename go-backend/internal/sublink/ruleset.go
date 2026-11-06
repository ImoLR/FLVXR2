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
	"sync"
	"time"
)

// Plan 226: the 国内直连 sing-box config downloads geosite-cn / geoip-cn from
// the panel (DIRECT, the address the subscription itself came from) instead
// of GitHub through 节点选择. sing-box refuses to start when a remote rule set
// can neither be loaded from its cache nor downloaded, so on fork.119 a first
// start with a dead node (or a first node that is dead) failed, and with
// 节点选择 = direct the mainland had to reach raw.githubusercontent.com.
//
// The panel serves a copy embedded at build time and refreshes it from the
// SagerNet rule-set branch at most once a day, on demand (no ticker); a
// failed or invalid download keeps the copy it has.

//go:embed ruleset/geosite-cn.srs ruleset/geoip-cn.srs
var ruleSetFiles embed.FS

// RuleSetPath is the public prefix: GET /s/rs/<name>.srs.
const RuleSetPath = "/s/rs/"

const (
	ruleSetRefresh  = 24 * time.Hour
	ruleSetMaxBytes = 4 << 20
	ruleSetTimeout  = 30 * time.Second
)

var ruleSetUpstream = map[string]string{"geosite-cn": geositeCN, "geoip-cn": geoipCN}

// RuleSetURL is where a client downloads rule set name from the panel at origin.
func RuleSetURL(origin, name string) string { return origin + RuleSetPath + name + ".srs" }

// validRuleSet: a binary sing-box rule set ("SRS" magic + version) of sane size.
func validRuleSet(b []byte) bool {
	return len(b) >= 64 && len(b) <= ruleSetMaxBytes && bytes.HasPrefix(b, []byte("SRS"))
}

type ruleSet struct {
	body    []byte
	etag    string
	checked time.Time
	busy    bool
}

// RuleSets holds the served copies. The zero value is ready to use.
type RuleSets struct {
	// Fetch downloads url; nil = plain HTTP GET (tests replace it).
	Fetch func(ctx context.Context, url string) ([]byte, error)

	mu   sync.Mutex
	sets map[string]*ruleSet
}

func ruleSetETag(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:12]) + `"`
}

// Get returns the current copy of rule set name ("geosite-cn" / "geoip-cn")
// and its ETag; a copy older than a day starts one background refresh.
func (s *RuleSets) Get(name string, now time.Time) ([]byte, string, bool) {
	url, ok := ruleSetUpstream[name]
	if !ok {
		return nil, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sets == nil {
		s.sets = map[string]*ruleSet{}
	}
	rs := s.sets[name]
	if rs == nil {
		b, err := ruleSetFiles.ReadFile("ruleset/" + name + ".srs")
		if err != nil {
			return nil, "", false
		}
		rs = &ruleSet{body: b, etag: ruleSetETag(b)}
		s.sets[name] = rs
	}
	if !rs.busy && now.Sub(rs.checked) >= ruleSetRefresh {
		rs.busy, rs.checked = true, now
		go s.refresh(rs, url)
	}
	return rs.body, rs.etag, true
}

func (s *RuleSets) refresh(rs *ruleSet, url string) {
	ctx, cancel := context.WithTimeout(context.Background(), ruleSetTimeout)
	defer cancel()
	fetch := s.Fetch
	if fetch == nil {
		fetch = fetchRuleSet
	}
	b, err := fetch(ctx, url)
	s.mu.Lock()
	defer s.mu.Unlock()
	rs.busy = false
	if err == nil && validRuleSet(b) {
		rs.body, rs.etag = b, ruleSetETag(b)
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
