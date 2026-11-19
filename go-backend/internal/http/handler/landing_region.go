package handler

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"go-backend/internal/geoip"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 286: the admin 落地服务器 tab groups servers by region. A server's
// region is, in this order: its 「显示地区」 override, the node region of a
// native server, the GeoIP country of its public host (embedded DB-IP data;
// a domain is resolved with DNS, no external geolocation service), else ''
// (未设置地区). Display only — nothing else reads it.

const (
	// landingRegionTTL: a resolved host is re-resolved in the background after
	// this long (the stale value is served meanwhile).
	landingRegionTTL = 6 * time.Hour
	// landingRegionRetry: a host that resolved to nothing is retried after this.
	landingRegionRetry = 10 * time.Minute
	// landingRegionWait bounds how long one list waits for hosts it has never
	// resolved (one shared deadline for the whole list, not per server).
	landingRegionWait     = 300 * time.Millisecond
	landingRegionMaxHosts = 512
)

// landingRegionCache maps a public host to its GeoIP region. Lookups never
// block for longer than the caller's wait: resolving runs in goroutines
// (one per host at a time) and stale entries are served while refreshing.
type landingRegionCache struct {
	mu      sync.Mutex
	entries map[string]*landingRegionEntry
	resolve func(host string) string
	now     func() time.Time
}

type landingRegionEntry struct {
	region   string
	at       time.Time     // last finished lookup (zero = never finished)
	used     time.Time     // last asked for (pruning)
	inflight chan struct{} // closed when the running lookup finishes; nil = idle
}

func newLandingRegionCache(resolve func(string) string) *landingRegionCache {
	if resolve == nil {
		resolve = landingRegionResolve
	}
	return &landingRegionCache{entries: map[string]*landingRegionEntry{}, resolve: resolve, now: time.Now}
}

// landingRegionResolve is the production lookup: an IP literal goes straight
// to the embedded table; a domain is resolved (A first, then AAAA, 2 s each).
func landingRegionResolve(host string) string {
	return geoip.DetectNodeAddresses("", host, "").Region
}

func (h *Handler) landingRegions() *landingRegionCache {
	h.landingRegionOnce.Do(func() {
		if h.landingRegionC == nil {
			h.landingRegionC = newLandingRegionCache(nil)
		}
	})
	return h.landingRegionC
}

// landingRegionHost normalizes a public host for lookup: no brackets, no
// port, lower-case, no trailing dot. ” = nothing to look up.
func landingRegionHost(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	return strings.ToLower(host)
}

// get returns the cached region of host and, when the host has never been
// resolved yet, a channel closed once its first lookup finishes (nil = the
// value is final for now). Starts a lookup when none ran yet or it is stale.
func (c *landingRegionCache) get(host string) (string, <-chan struct{}) {
	host = landingRegionHost(host)
	if host == "" {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	e := c.entries[host]
	if e == nil {
		c.pruneLocked(now)
		e = &landingRegionEntry{}
		c.entries[host] = e
	}
	e.used = now
	ttl := landingRegionTTL
	if e.region == "" {
		ttl = landingRegionRetry
	}
	if e.inflight == nil && (e.at.IsZero() || now.Sub(e.at) >= ttl) {
		done := make(chan struct{})
		e.inflight = done
		go c.run(host, e, done)
	}
	if e.at.IsZero() {
		return e.region, e.inflight
	}
	return e.region, nil
}

func (c *landingRegionCache) run(host string, e *landingRegionEntry, done chan struct{}) {
	region := ""
	func() {
		defer func() { _ = recover() }()
		region = strings.ToUpper(strings.TrimSpace(c.resolve(host)))
	}()
	c.mu.Lock()
	e.region, e.at, e.inflight = region, c.now(), nil
	c.mu.Unlock()
	close(done)
}

// pruneLocked drops idle entries nobody asked for in a day once the map is
// large (public hosts change rarely; this only bounds a pathological churn).
func (c *landingRegionCache) pruneLocked(now time.Time) {
	if len(c.entries) < landingRegionMaxHosts {
		return
	}
	for host, e := range c.entries {
		if e.inflight == nil && now.Sub(e.used) > 24*time.Hour {
			delete(c.entries, host)
		}
	}
}

// lookup returns the region of every host, waiting at most `wait` in total
// for hosts never resolved before; those still unknown come back as ”
// (their lookup keeps running and fills the cache for the next list).
func (c *landingRegionCache) lookup(hosts []string, wait time.Duration) map[string]string {
	out := make(map[string]string, len(hosts))
	var pending []<-chan struct{}
	for _, host := range hosts {
		region, done := c.get(host)
		out[host] = region
		if done != nil {
			pending = append(pending, done)
		}
	}
	if len(pending) == 0 || wait <= 0 {
		return out
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for _, done := range pending {
		select {
		case <-done:
		case <-timer.C:
			return c.peekAll(hosts, out)
		}
	}
	return c.peekAll(hosts, out)
}

func (c *landingRegionCache) peekAll(hosts []string, out map[string]string) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, host := range hosts {
		if e := c.entries[landingRegionHost(host)]; e != nil {
			out[host] = e.region
		}
	}
	return out
}

// warm starts (or refreshes) the lookup of host without waiting, e.g. after
// a public host change, so the next list has it.
func (c *landingRegionCache) warm(host string) { _, _ = c.get(host) }

// landingServerRegionView is the admin view's region trio.
type landingServerRegionView struct {
	Region, Auto, Override string
}

// landingServerRegionsFor resolves the regions of a list of servers with
// one node query and at most one bounded wait for never-seen hosts.
func (h *Handler) landingServerRegionsFor(ss []model.LandingServer, wait time.Duration) map[int64]landingServerRegionView {
	out := make(map[int64]landingServerRegionView, len(ss))
	var nodeIDs []int64
	for _, s := range ss {
		if s.Source != "shared" && s.NodeID > 0 {
			nodeIDs = append(nodeIDs, s.NodeID)
		}
	}
	nodeRegions, _ := h.repo.NodeRegions(nodeIDs)
	var hosts []string
	for _, s := range ss {
		if s.Source == "shared" || nodeRegions[s.NodeID] == "" {
			hosts = append(hosts, s.PublicHost)
		}
	}
	geo := h.landingRegions().lookup(hosts, wait)
	for _, s := range ss {
		auto := ""
		if s.Source != "shared" {
			auto = nodeRegions[s.NodeID]
		}
		if auto == "" {
			auto = geo[s.PublicHost]
		}
		override := strings.ToUpper(strings.TrimSpace(s.RegionOverride))
		region := auto
		if override != "" {
			region = override
		}
		out[s.ID] = landingServerRegionView{Region: region, Auto: auto, Override: override}
	}
	return out
}

func (rv landingServerRegionView) apply(v map[string]interface{}) {
	v["region"], v["regionAuto"], v["regionOverride"] = rv.Region, rv.Auto, rv.Override
}

func (h *Handler) registerLandingRegion(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/landing/server/region", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			response.WriteJSON(w, response.Err(405, "仅支持 POST"))
			return
		}
		h.landingServerRegionUpdate(w, r)
	})
}

// landingServerRegionUpdate — POST /api/v1/landing/server/region
// {id, regionOverride} (admin): the 「显示地区」 of one server (” = 自动).
// One column write: no revision, no agent / 妙妙屋 push, nothing else changes.
func (h *Handler) landingServerRegionUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		ID             int64   `json:"id"`
		RegionOverride *string `json:"regionOverride"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if req.ID <= 0 {
		landingError(w, errors.New("落地服务器ID不能为空"))
		return
	}
	if req.RegionOverride == nil {
		landingError(w, errors.New("缺少显示地区（自动请传空字符串）"))
		return
	}
	code, err := repo.NormalizeTunnelRegionOverride(*req.RegionOverride)
	if err != nil {
		landingError(w, err)
		return
	}
	s, err := h.repo.GetLandingServer(req.ID)
	if err != nil || s == nil {
		landingError(w, errors.New("落地服务器不存在"))
		return
	}
	if _, err := h.repo.SetLandingServerRegionOverride(s.ID, code); err != nil {
		landingError(w, err)
		return
	}
	s.RegionOverride = code
	rv := h.landingServerRegionsFor([]model.LandingServer{*s}, landingRegionWait)[s.ID]
	response.WriteJSON(w, response.OK(map[string]interface{}{"id": s.ID, "region": rv.Region, "regionAuto": rv.Auto, "regionOverride": rv.Override}))
}
