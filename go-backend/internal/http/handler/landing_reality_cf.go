package handler

import (
	"context"
	"errors"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/cfnet"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 290: Reality 伪装域名 on Cloudflare. Control plane only: DNS lookups by
// the panel (cached) plus the IPs landings already report with their 测速 /
// pins; nothing on the forwarding path, no extra probes on the machines.

const (
	// realityCFEvery: the full check runs with the reality loop (10-min tick)
	// once per this period, first on the loop's first tick after start.
	realityCFEvery = 6 * time.Hour
	// Lookup answers are reused for this long (failures for realityCFFailTTL).
	realityCFCacheTTL = 6 * time.Hour
	realityCFFailTTL  = 30 * time.Minute
	// One lookup at a time, each bounded; a short pause between uncached ones.
	realityCFLookupTimeout = 5 * time.Second
)

// realityCFLookup resolves a host's A/AAAA addresses (tests replace it).
var realityCFLookup = func(ctx context.Context, host string) ([]string, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP.String())
	}
	return out, nil
}

// realityCFLookupGap is the pause after each uncached lookup (tests shorten it).
var realityCFLookupGap = 50 * time.Millisecond

type realityCFCacheEntry struct {
	ips []string
	err error
	at  time.Time
}

var (
	realityCFCacheMu sync.Mutex
	realityCFCache   = map[string]realityCFCacheEntry{}
	// realityCFVerdicts: the last verdict per checked host (candidates and
	// live dests that are not candidates) for the server ⚠.
	realityCFVerdicts sync.Map // host -> repo.RealityCFVerdict
	realityCFRunMu    sync.Mutex
	realityCFStateMu  sync.Mutex
	realityCFLastRun  time.Time
	realityCFDue      bool
)

// realityCFResolve returns the cached lookup of host (fresh = ignore a
// positive cache entry; failures are retried after realityCFFailTTL anyway).
func realityCFResolve(ctx context.Context, host string, fresh bool) ([]string, error, bool) {
	realityCFCacheMu.Lock()
	e, ok := realityCFCache[host]
	realityCFCacheMu.Unlock()
	if ok {
		age := time.Since(e.at)
		if e.err != nil && age < realityCFFailTTL {
			return e.ips, e.err, true
		}
		if e.err == nil && !fresh && age < realityCFCacheTTL {
			return e.ips, nil, true
		}
	}
	lctx, cancel := context.WithTimeout(ctx, realityCFLookupTimeout)
	ips, err := realityCFLookup(lctx, host)
	cancel()
	if err == nil && len(ips) == 0 {
		err = errors.New("no address")
	}
	if ctx.Err() == nil { // a cancelled caller says nothing about the host
		realityCFCacheMu.Lock()
		realityCFCache[host] = realityCFCacheEntry{ips: ips, err: err, at: time.Now()}
		if len(realityCFCache) > 2000 { // bounded: drop the oldest half
			realityCFTrimLocked()
		}
		realityCFCacheMu.Unlock()
	}
	return ips, err, false
}

func realityCFTrimLocked() {
	type kv struct {
		host string
		at   time.Time
	}
	all := make([]kv, 0, len(realityCFCache))
	for h, e := range realityCFCache {
		all = append(all, kv{h, e.at})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	for _, x := range all[:len(all)/2] {
		delete(realityCFCache, x.host)
	}
}

// realityCFVerdictOf combines the panel lookup with the landings' evidence.
func realityCFVerdictOf(ips []string, lookupErr error, landingEvidence string) repo.RealityCFVerdict {
	if lookupErr == nil {
		if ip := cfnet.First(ips); ip != "" {
			return repo.RealityCFVerdict{Hosted: true, Known: true, Evidence: ip + "（面板解析）"}
		}
	}
	if landingEvidence != "" {
		return repo.RealityCFVerdict{Hosted: true, Known: true, Evidence: landingEvidence}
	}
	if lookupErr == nil && len(ips) > 0 {
		return repo.RealityCFVerdict{Known: true}
	}
	return repo.RealityCFVerdict{}
}

// realityCFCheck checks hosts one at a time and stores the verdicts of
// candidate rows (others are kept in memory for the server ⚠).
func (h *Handler) realityCFCheck(ctx context.Context, hosts []string, fresh bool) ([]repo.RealityCFChange, bool) {
	evidence, err := h.repo.RealityLandingCFEvidence(time.Now())
	if err != nil {
		log.Printf("landing reality cf check: evidence: %v", err)
		evidence = map[string]string{}
	}
	verdicts := make(map[string]repo.RealityCFVerdict, len(hosts))
	complete := true
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		if ctx.Err() != nil {
			complete = false
			break
		}
		ips, lerr, cached := realityCFResolve(ctx, host, fresh)
		v := realityCFVerdictOf(ips, lerr, evidence[host])
		if v.Known {
			verdicts[host] = v
			realityCFVerdicts.Store(host, v)
		}
		if !cached && realityCFLookupGap > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(realityCFLookupGap):
			}
		}
	}
	changes, err := h.repo.ApplyRealityCFVerdicts(verdicts)
	if err != nil {
		log.Printf("landing reality cf check: store: %v", err)
	}
	for _, c := range changes {
		if c.Hosted {
			log.Printf("landing reality cf: %s blocked (Cloudflare %s)", c.Domain, c.Evidence)
		} else {
			log.Printf("landing reality cf: %s unblocked (no longer on Cloudflare)", c.Domain)
		}
	}
	return changes, complete && err == nil
}

// realityCFCheckAll runs the full check (every candidate, every server
// default dest, every live Reality dest) unless one is already running.
func (h *Handler) realityCFCheckAll(ctx context.Context, fresh bool) bool {
	if !realityCFRunMu.TryLock() {
		return false
	}
	defer realityCFRunMu.Unlock()
	hosts, err := h.repo.RealityCFCheckHosts()
	if err != nil {
		log.Printf("landing reality cf check: hosts: %v", err)
		return false
	}
	_, ok := h.realityCFCheck(ctx, hosts, fresh)
	realityCFStateMu.Lock()
	if ok {
		realityCFDue = false
		if fresh { // the 6 h period counts from the last run with fresh lookups
			realityCFLastRun = time.Now()
		}
	}
	realityCFStateMu.Unlock()
	return ok
}

// realityCFCheckIfDue: the reality loop's hook (every 6 h, or right after a
// pool import added domains).
func (h *Handler) realityCFCheckIfDue(ctx context.Context) {
	realityCFStateMu.Lock()
	due := realityCFDue || realityCFLastRun.IsZero() || time.Since(realityCFLastRun) >= realityCFEvery
	periodic := realityCFLastRun.IsZero() || time.Since(realityCFLastRun) >= realityCFEvery
	realityCFStateMu.Unlock()
	if due {
		h.realityCFCheckAll(ctx, periodic)
	}
}

func markRealityCFDue() {
	realityCFStateMu.Lock()
	realityCFDue = true
	realityCFStateMu.Unlock()
}

// realityCFCheckOne checks one host now (admin add / manual pick); the
// verdict is stored when the host is a candidate.
func (h *Handler) realityCFCheckOne(ctx context.Context, host string) repo.RealityCFVerdict {
	host = strings.ToLower(strings.TrimSpace(host))
	h.realityCFCheck(ctx, []string{host}, false)
	if v, ok := realityCFVerdicts.Load(host); ok {
		return v.(repo.RealityCFVerdict)
	}
	return repo.RealityCFVerdict{}
}

// realityCFHostedNow: is host (a manual 伪装域名) on Cloudflare? Candidate
// rows answer first, then a (cached) lookup with this server's evidence.
func (h *Handler) realityCFHostedNow(ctx context.Context, s *model.LandingServer, host string) (bool, string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if ok, ev := h.repo.RealityDomainCFHosted(host); ok {
		return true, ev
	}
	ips, err, _ := realityCFResolve(ctx, host, false)
	own := ""
	if s != nil {
		own = repo.RealityServerCFHosts(s, time.Now())[host]
	}
	v := realityCFVerdictOf(ips, err, own)
	if v.Known {
		realityCFVerdicts.Store(host, v)
	}
	return v.Hosted, v.Evidence
}

// landingRealityRiskView (admin): what of this server's Reality setup is
// exposed — dests on Cloudflare (server default and live listeners) and
// Reality listeners without 防盗 in effect. Read from stored state only.
func (h *Handler) landingRealityRiskView(s model.LandingServer, nodeGuard ...int) map[string]any {
	listeners, _ := h.repo.RealityListenersOnServer(s.ID)
	if listeners.Dests == nil {
		listeners.Dests = map[string]int{}
	}
	guard := landingGuardView(s, nodeGuard...)
	state, _ := guard["state"].(string)
	exposed := listeners.Count > 0 && state != "on" && state != "pending"

	marked, _ := h.repo.RealityCFHostedDomains()
	own := repo.RealityServerCFHosts(&s, time.Now())
	evidenceOf := func(host string) string {
		if ev, ok := marked[host]; ok {
			if ev == "" {
				ev = "已标记"
			}
			return ev
		}
		if ev := own[host]; ev != "" {
			return ev
		}
		if v, ok := realityCFVerdicts.Load(host); ok {
			if vv := v.(repo.RealityCFVerdict); vv.Hosted {
				return vv.Evidence
			}
		}
		return ""
	}
	type cfDest struct {
		Host     string `json:"host"`
		Nodes    int    `json:"nodes"`
		Default  bool   `json:"default"`
		Evidence string `json:"evidence"`
	}
	var dests []cfDest
	defHost := realityHostOf(s.RealityDest)
	seen := map[string]bool{}
	hosts := make([]string, 0, len(listeners.Dests)+1)
	for host := range listeners.Dests {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	if defHost != "" {
		hosts = append([]string{strings.ToLower(defHost)}, hosts...)
	}
	for _, host := range hosts {
		if seen[host] {
			continue
		}
		seen[host] = true
		ev := evidenceOf(host)
		if ev == "" {
			continue
		}
		dests = append(dests, cfDest{Host: host, Nodes: listeners.Dests[host], Default: strings.EqualFold(host, defHost), Evidence: ev})
	}
	if dests == nil {
		dests = []cfDest{}
	}
	return map[string]any{
		"listeners":    listeners.Count,
		"guardState":   state,
		"guardExposed": exposed,
		"cfDests":      dests,
	}
}
