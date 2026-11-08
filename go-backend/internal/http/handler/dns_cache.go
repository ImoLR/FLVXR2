package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/ws"
)

// Plan 240: the node's local DNS cache (flvx-dns-cache on the agent). The node list shows the agent's last
// local check; 「修复 DNS 缓存」 and a node becoming a native landing run `ensure` once on the agent.

const (
	dnsCacheEnsureTimeout = 210 * time.Second // agent bounds one run at 200 s
	dnsCacheCooldown      = 60 * time.Second  // per node, between two runs
	dnsCacheDefaultZone   = "imgamer.top"
)

type dnsCacheGuard struct {
	mu   sync.Mutex
	busy map[int64]bool
	last map[int64]time.Time
}

// begin reserves a run for the node; it fails while a run is active or inside the cooldown.
func (g *dnsCacheGuard) begin(nodeID int64, now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy == nil {
		g.busy, g.last = map[int64]bool{}, map[int64]time.Time{}
	}
	if g.busy[nodeID] {
		return errors.New("这台节点正在配置 DNS 缓存，请稍候")
	}
	if t, ok := g.last[nodeID]; ok && now.Sub(t) < dnsCacheCooldown {
		return fmt.Errorf("刚刚执行过，请 %d 秒后再试", int((dnsCacheCooldown-now.Sub(t)).Seconds())+1)
	}
	g.busy[nodeID], g.last[nodeID] = true, now
	return nil
}

func (g *dnsCacheGuard) end(nodeID int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.busy, nodeID)
}

var dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// registrableZone: host or zone → registrable domain (last two labels, three for co.uk-style names); "" for
// IPs and junk. Same rule as norm_zone in flvx-dns-cache.sh.
func registrableZone(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	if i := strings.IndexAny(h, "/?"); i >= 0 {
		h = h[:i]
	}
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.TrimSuffix(strings.Trim(h, "[]"), ".")
	if h == "" || net.ParseIP(h) != nil || !dnsLabelRe.MatchString(h) {
		return ""
	}
	p := strings.Split(h, ".")
	n := len(p)
	if n >= 3 && len(p[n-1]) == 2 {
		switch p[n-2] {
		case "com", "net", "org", "gov", "edu", "co", "ac", "or", "ne", "go":
			return strings.Join(p[n-3:], ".")
		}
	}
	return strings.Join(p[n-2:], ".")
}

// dnsCacheZones: our own names the node's cache sends to Cloudflare (fresh DDNS IPs, no stale ISP cache):
// panel address, DDNS provider zones + record domains, landing public hosts, probe domain.
func (h *Handler) dnsCacheZones(r *http.Request) []string {
	set := map[string]bool{}
	add := func(v string) {
		if z := registrableZone(v); z != "" {
			set[z] = true
		}
	}
	for _, v := range h.ddnsPanelHosts(r) {
		add(v)
	}
	if v, err := h.repo.GetViteConfigValue(probeDomainKey); err == nil {
		add(v)
	}
	if providers, err := h.repo.ListDNSProviders(); err == nil {
		for _, p := range providers {
			var zones []string
			if json.Unmarshal([]byte(p.Zones), &zones) == nil {
				for _, z := range zones {
					add(z)
				}
			}
		}
	}
	if recs, err := h.repo.ListAllDDNSRecords(); err == nil {
		for _, rec := range recs {
			add(rec.Domain)
		}
	}
	if servers, err := h.repo.ListLandingServers(0, true); err == nil {
		for _, s := range servers {
			add(s.PublicHost)
		}
	}
	if len(set) == 0 {
		set[dnsCacheDefaultZone] = true
	}
	out := make([]string, 0, len(set))
	for z := range set {
		out = append(out, z)
	}
	sort.Strings(out)
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

type dnsCacheEnsureResult struct {
	DNSCache *ws.DNSCacheView `json:"dnsCache"`
	Summary  string           `json:"summary,omitempty"`
	Output   string           `json:"output,omitempty"`
}

// runDNSCacheEnsure sends DNSCacheEnsure to the node and stores the fresh state it returns.
func (h *Handler) runDNSCacheEnsure(nodeID int64, zones []string) (*dnsCacheEnsureResult, error) {
	if err := h.dnsCache.begin(nodeID, time.Now()); err != nil {
		return nil, err
	}
	defer h.dnsCache.end(nodeID)
	res, err := h.sendNodeCommandWithTimeout(nodeID, "DNSCacheEnsure", map[string]interface{}{"zones": zones}, dnsCacheEnsureTimeout, false, false)
	out := &dnsCacheEnsureResult{}
	if res.Data != nil {
		if raw, e := json.Marshal(res.Data["dns_cache"]); e == nil {
			var rep ws.DNSCacheReport
			if json.Unmarshal(raw, &rep) == nil && rep.Mode != "" {
				h.wsServer.SetDNSCache(nodeID, &rep)
			}
		}
		out.Summary, _ = res.Data["summary"].(string)
		out.Output, _ = res.Data["output"].(string)
	}
	out.DNSCache = h.wsServer.DNSCache(nodeID)
	if err != nil {
		return out, err
	}
	return out, nil
}

// nodeDNSCacheEnsure: POST /api/v1/node/dns-cache/ensure {id} — 「修复 DNS 缓存」 (admin only, via the
// /api/v1/node/ middleware). One run per node at a time, at most one per minute.
func (h *Handler) nodeDNSCacheEnsure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := decodeJSON(r.Body, &req); err != nil || req.ID <= 0 {
		response.WriteJSON(w, response.ErrDefault("节点 ID 无效"))
		return
	}
	if node, err := h.getNodeRecord(req.ID); err != nil || node == nil {
		response.WriteJSON(w, response.ErrDefault("节点不存在"))
		return
	} else if node.IsRemote == 1 {
		response.WriteJSON(w, response.ErrDefault("远程节点请在它所在的面板处理"))
		return
	}
	if h.wsServer == nil || !h.wsServer.NodeOnline(req.ID) {
		response.WriteJSON(w, response.ErrDefault("节点不在线"))
		return
	}
	if !h.wsServer.DNSCacheCapable(req.ID) && h.nodeCommandSender == nil {
		response.WriteJSON(w, response.ErrDefault("节点程序版本过旧，请先在「节点」页升级"))
		return
	}
	res, err := h.runDNSCacheEnsure(req.ID, h.dnsCacheZones(r))
	if err != nil {
		if res == nil {
			response.WriteJSON(w, response.ErrDefault(err.Error()))
			return
		}
		response.WriteJSON(w, response.Err(-2, "修复失败："+err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(res))
}

// ensureDNSCacheOnLanding: a node just became an FLVX native landing → run ensure once in the background
// (no-op when the cache is already right). Old agents and remote nodes are skipped; errors are only logged.
func (h *Handler) ensureDNSCacheOnLanding(nodeID int64, r *http.Request) {
	if h.wsServer == nil || !h.wsServer.DNSCacheCapable(nodeID) {
		return
	}
	if node, err := h.getNodeRecord(nodeID); err != nil || node == nil || node.IsRemote == 1 {
		return
	}
	zones := h.dnsCacheZones(r)
	go func() {
		res, err := h.runDNSCacheEnsure(nodeID, zones)
		switch {
		case err != nil:
			log.Printf("[dns-cache] node %d landing ensure: %v", nodeID, err)
		case res != nil && res.DNSCache != nil && !res.DNSCache.OK:
			log.Printf("[dns-cache] node %d landing ensure: mode=%s %s", nodeID, res.DNSCache.Mode, res.DNSCache.Reason)
		}
	}()
}
