package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-backend/internal/ddns"
	"go-backend/internal/store/model"
	"go-backend/internal/ws"
)

// Node DDNS updater (plan 142). Event driven: an agent address report, a
// node coming online, a save and 「立即同步」 sync one node; a single timer
// goroutine runs failed retries (backoff) and the slow drift check. The CF
// API is only called when an address changed, on a retry, or once per record
// per DriftCheckEvery (GET only). DNS records are never deleted.

type ddnsManager struct {
	mu        sync.Mutex
	addrs     map[int64]ddns.AgentAddrs
	nodeLocks map[int64]*sync.Mutex
	zones     map[int64]ddnsZoneCache
	logAt     map[int64]time.Time
	checking  map[int64]bool
	wake      chan struct{}
	calls     atomic.Int64
	// test hooks
	newClient func(token string) *ddns.Client
	session   func(nodeID int64) (online, addrReport bool, clientIP string)
	now       func() time.Time
	pending   sync.WaitGroup
}

type ddnsZoneCache struct {
	zones []ddns.Zone
	at    time.Time
}

const ddnsZoneCacheTTL = 10 * time.Minute

// Errors that only the admin can fix retry slowly.
type ddnsConfigError struct{ msg string }

func (e *ddnsConfigError) Error() string { return e.msg }

func ddnsConfigErr(format string, args ...interface{}) error {
	return &ddnsConfigError{msg: fmt.Sprintf(format, args...)}
}

var ddnsManagerInit sync.Mutex

func (h *Handler) ddnsState() *ddnsManager {
	ddnsManagerInit.Lock()
	defer ddnsManagerInit.Unlock()
	if h.ddns == nil {
		h.ddns = &ddnsManager{
			addrs:     map[int64]ddns.AgentAddrs{},
			nodeLocks: map[int64]*sync.Mutex{},
			zones:     map[int64]ddnsZoneCache{},
			logAt:     map[int64]time.Time{},
			checking:  map[int64]bool{},
			wake:      make(chan struct{}, 1),
			now:       time.Now,
		}
	}
	return h.ddns
}

func (m *ddnsManager) nodeLock(nodeID int64) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.nodeLocks[nodeID]
	if l == nil {
		l = &sync.Mutex{}
		m.nodeLocks[nodeID] = l
	}
	return l
}

func (m *ddnsManager) wakeUp() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *ddnsManager) addrsOf(nodeID int64) (ddns.AgentAddrs, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.addrs[nodeID]
	return a, ok
}

func (h *Handler) ddnsClient(p *model.DNSProvider) *ddns.Client {
	m := h.ddnsState()
	token := dnsProviderToken(p)
	var c *ddns.Client
	if m.newClient != nil {
		c = m.newClient(token)
	} else {
		c = ddns.NewCloudflare(token)
	}
	prev := c.Calls
	c.Calls = func(method, path string) {
		m.calls.Add(1)
		if prev != nil {
			prev(method, path)
		}
	}
	return c
}

func (h *Handler) ddnsSession(nodeID int64) (bool, bool, string) {
	m := h.ddnsState()
	if m.session != nil {
		return m.session(nodeID)
	}
	if h.wsServer == nil {
		return false, false, ""
	}
	return h.wsServer.NodeDDNSSession(nodeID)
}

// onNodeAddrs is the WS hook for a new agent address report (non-blocking).
func (h *Handler) onNodeAddrs(nodeID int64, report ws.NodeAddrs) {
	m := h.ddnsState()
	addrs := ddns.NormalizeReport(report.V4, report.V6)
	m.mu.Lock()
	old, had := m.addrs[nodeID]
	m.addrs[nodeID] = addrs
	m.mu.Unlock()
	if had && old.Equal(addrs) {
		return
	}
	h.ddnsSyncAsync(nodeID, false)
}

func (h *Handler) ddnsNodeOnline(nodeID int64) { h.ddnsSyncAsync(nodeID, false) }

func (h *Handler) ddnsNodeOffline(nodeID int64) {
	if h.nodeRuntimeOnline(nodeID) {
		return
	}
	m := h.ddnsState()
	m.mu.Lock()
	delete(m.addrs, nodeID)
	m.mu.Unlock()
}

func (h *Handler) ddnsSyncAsync(nodeID int64, force bool) {
	m := h.ddnsState()
	m.pending.Add(1)
	go func() {
		defer m.pending.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		h.ddnsSyncNode(ctx, nodeID, force)
	}()
}

// ddnsSyncNode brings the records of one node in line with its current
// addresses. force skips backoff and runs a full check of every record.
func (h *Handler) ddnsSyncNode(ctx context.Context, nodeID int64, force bool) {
	if h == nil || h.repo == nil || nodeID <= 0 {
		return
	}
	m := h.ddnsState()
	lock := m.nodeLock(nodeID)
	lock.Lock()
	defer lock.Unlock()
	node, err := h.repo.GetNodeByID(nodeID)
	if err != nil || node == nil || !node.DDNSEnabled {
		return
	}
	records, err := h.repo.GetNodeDDNSRecords(nodeID)
	if err != nil || len(records) == 0 {
		return
	}
	now := m.now()
	provider, err := h.repo.GetDNSProvider(node.DDNSProviderID)
	if err != nil {
		return
	}
	if provider == nil {
		for _, rec := range records {
			h.ddnsFail(rec, ddnsConfigErr("DNS 服务商不存在或已删除"), now)
		}
		return
	}
	client := h.ddnsClient(provider)
	online, addrReport, clientIP := h.ddnsSession(nodeID)
	addrs, haveAddrs := m.addrsOf(nodeID)
	for _, rec := range records {
		desired, ok := ddns.SelectAddress(rec.RecordType, rec.Source, rec.SourcePrefix, addrs, clientIP)
		if !ok {
			if !online {
				// Keep the last known state while the node is away; move a
				// due retry / check forward so the timer does not spin.
				switch {
				case rec.Status == model.DDNSStatusFailed && rec.NextRetryAt <= now.UnixMilli():
					_ = h.repo.UpdateDDNSRecordState(rec.ID, map[string]interface{}{"next_retry_at": now.Add(ddns.Backoff(rec.FailCount)).UnixMilli()})
				case rec.Status == model.DDNSStatusSynced && now.Sub(time.UnixMilli(rec.LastCheckAt)) >= ddns.DriftCheckEvery:
					_ = h.repo.UpdateDDNSRecordState(rec.ID, map[string]interface{}{"last_check_at": now.UnixMilli()})
				}
				continue
			}
			needsAgent := ddns.NeedsAgentReport(rec.RecordType, rec.Source)
			status, msg := "", ""
			switch {
			case needsAgent && !addrReport:
				status, msg = model.DDNSStatusUpgrade, "需要升级节点后生效（当前节点版本不上报网卡地址）"
			case needsAgent && haveAddrs, !needsAgent:
				status, msg = model.DDNSStatusNoAddr, ddnsNoAddrText(rec)
			}
			if status != "" && (rec.Status != status || rec.LastError != msg) {
				_ = h.repo.UpdateDDNSRecordState(rec.ID, map[string]interface{}{"status": status, "last_error": msg})
			}
			continue
		}
		synced := rec.Status == model.DDNSStatusSynced && rec.RecordID != ""
		changed := desired != rec.CurrentIP || !synced
		driftDue := synced && now.Sub(time.UnixMilli(rec.LastCheckAt)) >= ddns.DriftCheckEvery
		if !force && !changed && !driftDue {
			if !rec.Applied {
				h.ddnsApply(rec, desired)
			}
			continue
		}
		if !force && rec.Status == model.DDNSStatusFailed && rec.NextRetryAt > now.UnixMilli() {
			continue // backoff (a save or 「立即同步」 retries at once)
		}
		written, err := h.ddnsUpsert(ctx, client, provider, &rec, desired)
		if err != nil {
			h.ddnsFail(rec, err, now)
			continue
		}
		oldIP := rec.CurrentIP
		updates := map[string]interface{}{
			"zone_id": rec.ZoneID, "record_id": rec.RecordID, "current_ip": desired, "ttl": ddns.RecordTTL,
			"status": model.DDNSStatusSynced, "last_error": "", "last_sync_at": now.UnixMilli(),
			"last_check_at": now.UnixMilli(), "fail_count": 0, "next_retry_at": int64(0),
		}
		if err := h.repo.UpdateDDNSRecordState(rec.ID, updates); err != nil {
			log.Printf("[ddns] node %d %s: save state: %v", nodeID, rec.Domain, err)
			continue
		}
		if written {
			log.Printf("[ddns] node %d %s %s -> %s", nodeID, rec.RecordType, rec.Domain, desired)
		}
		rec.CurrentIP = desired
		if !rec.Applied {
			h.ddnsApply(rec, desired)
		} else if oldIP != "" && oldIP != desired {
			h.ddnsRewriteBind(nodeID, oldIP, desired)
		}
	}
	m.wakeUp()
}

func ddnsNoAddrText(rec model.NodeDDNSRecord) string {
	switch rec.Source {
	case ddns.SourcePanel:
		return "面板看到的连接不是公网 IPv4（可能经 IPv6 连接面板）"
	case ddns.SourceIface:
		return "节点网卡上没有公网 IPv4（NAT 机器请选「面板看到的出口 IPv4」）"
	case ddns.SourceCT:
		return "节点没有电信 240e 开头的 IPv6"
	case ddns.SourceCU:
		return "节点没有联通 2408 开头的 IPv6"
	case ddns.SourceCM:
		return "节点没有移动 2409 开头的 IPv6"
	case ddns.SourcePrefix:
		return "节点没有 " + rec.SourcePrefix + " 开头的 IPv6"
	}
	return "节点没有公网 IPv6"
}

func (h *Handler) ddnsFail(rec model.NodeDDNSRecord, err error, now time.Time) {
	m := h.ddnsState()
	fails := rec.FailCount + 1
	delay := ddns.Backoff(fails)
	var cfg *ddnsConfigError
	if errors.As(err, &cfg) || ddns.IsAuthError(err) {
		delay = time.Hour // only the admin can fix it; a save retries at once
	}
	msg := err.Error()
	if ddns.IsAuthError(err) {
		msg = "Cloudflare 拒绝了 API Token（可能已被撤销或更换）：" + msg
	}
	_ = h.repo.UpdateDDNSRecordState(rec.ID, map[string]interface{}{
		"status": model.DDNSStatusFailed, "last_error": msg, "fail_count": fails,
		"next_retry_at": now.Add(delay).UnixMilli(), "last_check_at": now.UnixMilli(),
	})
	m.mu.Lock()
	last := m.logAt[rec.ID]
	due := now.Sub(last) >= 10*time.Minute
	if due {
		m.logAt[rec.ID] = now
	}
	m.mu.Unlock()
	if due {
		log.Printf("[ddns] node %d %s %s failed (%d): %s", rec.NodeID, rec.RecordType, rec.Domain, fails, msg)
	}
}

func isCFNotFound(err error) bool {
	var apiErr *ddns.APIError
	return errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound || apiErr.Code == 81044)
}

// ddnsUpsert makes the record point at desired. It sets rec.ZoneID and
// rec.RecordID and reports whether anything was written.
func (h *Handler) ddnsUpsert(ctx context.Context, client *ddns.Client, provider *model.DNSProvider, rec *model.NodeDDNSRecord, desired string) (bool, error) {
	if rec.ZoneID == "" {
		zone, err := h.ddnsZoneFor(ctx, client, provider, rec.Domain)
		if err != nil {
			return false, err
		}
		rec.ZoneID = zone.ID
	}
	if rec.RecordID != "" {
		if desired != rec.CurrentIP {
			if _, err := client.UpdateRecord(ctx, rec.ZoneID, rec.RecordID, rec.RecordType, rec.Domain, desired); err == nil {
				return true, nil
			} else if !isCFNotFound(err) {
				return false, err
			}
			rec.RecordID = "" // removed in the dashboard: create it again
		} else {
			cur, err := client.GetRecord(ctx, rec.ZoneID, rec.RecordID)
			switch {
			case err == nil && cur.Type == rec.RecordType && strings.EqualFold(strings.TrimSuffix(cur.Name, "."), rec.Domain):
				if cur.Content == desired && !cur.Proxied && cur.TTL == ddns.RecordTTL {
					return false, nil
				}
				if _, err := client.UpdateRecord(ctx, rec.ZoneID, rec.RecordID, rec.RecordType, rec.Domain, desired); err != nil {
					return false, err
				}
				return true, nil
			case err == nil, isCFNotFound(err):
				rec.RecordID = ""
			default:
				return false, err
			}
		}
	}
	existing, err := client.ListRecords(ctx, rec.ZoneID, rec.Domain, "")
	if err != nil {
		return false, err
	}
	same, err := ddnsSameTypeRecords(existing, rec.RecordType)
	if err != nil {
		return false, err
	}
	if len(same) == 1 {
		cur := same[0]
		if (cur.Proxied || cur.Content != desired) && !rec.Takeover && cur.Comment != ddns.RecordComment {
			return false, ddnsTakeoverError(cur)
		}
		rec.RecordID = cur.ID
		if cur.Content == desired && !cur.Proxied && cur.TTL == ddns.RecordTTL {
			return false, nil
		}
		if _, err := client.UpdateRecord(ctx, rec.ZoneID, cur.ID, rec.RecordType, rec.Domain, desired); err != nil {
			return false, err
		}
		return true, nil
	}
	created, err := client.CreateRecord(ctx, rec.ZoneID, rec.RecordType, rec.Domain, desired)
	if err != nil {
		return false, err
	}
	rec.RecordID = created.ID
	return true, nil
}

func ddnsSameTypeRecords(existing []ddns.Record, recordType string) ([]ddns.Record, error) {
	var same []ddns.Record
	for _, r := range existing {
		if r.Type == "CNAME" {
			return nil, ddnsConfigErr("该域名已有 CNAME 记录，不能再添加 A/AAAA 记录")
		}
		if r.Type == recordType {
			same = append(same, r)
		}
	}
	if len(same) > 1 {
		return nil, ddnsConfigErr("Cloudflare 里有 %d 条同名 %s 记录，请先删除多余的记录", len(same), recordType)
	}
	return same, nil
}

func ddnsTakeoverError(cur ddns.Record) error {
	detail := "指向 " + cur.Content
	if cur.Proxied {
		detail += "，已开启代理（橙色云朵）"
	}
	return ddnsConfigErr("Cloudflare 已有这条 %s 记录（%s），如确认交给 FLVX 管理，请勾选「接管已有记录」", cur.Type, detail)
}

// ddnsZones lists the provider's zones (cached 10 minutes).
func (h *Handler) ddnsZones(ctx context.Context, client *ddns.Client, providerID int64) ([]ddns.Zone, error) {
	m := h.ddnsState()
	m.mu.Lock()
	cached, ok := m.zones[providerID]
	m.mu.Unlock()
	if ok && m.now().Sub(cached.at) < ddnsZoneCacheTTL {
		return cached.zones, nil
	}
	zones, err := client.ListZones(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.zones[providerID] = ddnsZoneCache{zones: zones, at: m.now()}
	m.mu.Unlock()
	return zones, nil
}

func (h *Handler) ddnsForgetZones(providerID int64) {
	m := h.ddnsState()
	m.mu.Lock()
	delete(m.zones, providerID)
	m.mu.Unlock()
}

func (h *Handler) ddnsZoneFor(ctx context.Context, client *ddns.Client, provider *model.DNSProvider, domain string) (ddns.Zone, error) {
	zones, err := h.ddnsZones(ctx, client, provider.ID)
	if err != nil {
		return ddns.Zone{}, err
	}
	for _, z := range zones {
		if strings.EqualFold(z.Name, domain) {
			return ddns.Zone{}, ddnsConfigErr("%s 是根域名（区域本身），FLVX 不会修改它，请用子域名", domain)
		}
	}
	zone, ok := ddns.ZoneFor(domain, zones)
	if !ok {
		names := make([]string, 0, len(zones))
		for _, z := range zones {
			names = append(names, z.Name)
		}
		if len(names) == 0 {
			return ddns.Zone{}, ddnsConfigErr("该 DNS 服务商的 Token 看不到任何域名区域")
		}
		return ddns.Zone{}, ddnsConfigErr("域名 %s 不在该 DNS 服务商可管理的区域（%s）里", domain, strings.Join(names, "、"))
	}
	if !zone.CanEditDNS() {
		return ddns.Zone{}, ddnsConfigErr("该 Token 对区域 %s 没有 DNS 编辑权限", zone.Name)
	}
	return zone, nil
}

// ddnsApply switches the node address field owned by rec to its domain
// (once) and runs the usual node-update sync for that change.
func (h *Handler) ddnsApply(rec model.NodeDDNSRecord, desired string) {
	if rec.CurrentIP == "" {
		rec.CurrentIP = desired
	}
	oldNode, newNode, err := h.repo.ApplyDDNSDomain(rec.ID)
	if err != nil {
		log.Printf("[ddns] node %d %s: switch address to domain: %v", rec.NodeID, rec.Domain, err)
		return
	}
	if oldNode == nil || newNode == nil {
		return
	}
	result := h.syncNodeUpdate(oldNode, newNode)
	log.Printf("[ddns] node %d address -> %s: %d tunnels, %d rules, %d failures", rec.NodeID, rec.Domain, result.TunnelsUpdated, result.RulesUpdated, len(result.Failures))
	// A listen IP still on the replaced literal (e.g. a stale prefix) follows
	// the record's current address.
	for _, old := range ddnsReplacedLiterals(oldNode, newNode) {
		if old != desired {
			h.ddnsRewriteBind(rec.NodeID, old, desired)
		}
	}
}

func ddnsReplacedLiterals(oldNode, newNode *model.Node) []string {
	var out []string
	if oldNode.ServerIPV4.String != newNode.ServerIPV4.String && isLiteralIP(oldNode.ServerIPV4.String) {
		out = append(out, strings.TrimSpace(oldNode.ServerIPV4.String))
	}
	if oldNode.ServerIPV6.String != newNode.ServerIPV6.String && isLiteralIP(oldNode.ServerIPV6.String) {
		out = append(out, strings.Trim(strings.TrimSpace(oldNode.ServerIPV6.String), "[]"))
	}
	return out
}

func isLiteralIP(s string) bool {
	s = strings.Trim(strings.TrimSpace(s), "[]")
	return s != "" && strings.Trim(s, "0123456789abcdefABCDEF:.") == "" && (strings.Count(s, ".") == 3 || strings.Contains(s, ":"))
}

// ddnsRewriteBind moves explicit listen IPs from oldIP to newIP and
// redeploys those rules.
func (h *Handler) ddnsRewriteBind(nodeID int64, oldIP, newIP string) {
	ids, err := h.repo.RewriteNodeBindIP(nodeID, oldIP, newIP)
	if err != nil {
		log.Printf("[ddns] node %d listen IP %s -> %s: %v", nodeID, oldIP, newIP, err)
		return
	}
	for _, id := range ids {
		forward, err := h.getForwardRecord(id)
		if err != nil || forward == nil {
			continue
		}
		if err := h.syncNodeForwardServices(forward); err != nil {
			log.Printf("[ddns] node %d rule %d redeploy after listen IP change: %v", nodeID, id, err)
		}
	}
}

// runDDNSLoop runs due retries and drift checks; it sleeps until the next
// due time (an hour when nothing is due) and wakes early after syncs/saves.
func (h *Handler) runDDNSLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	m := h.ddnsState()
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-m.wake:
			// Coalesce: the pass below reads the fresh state.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		next := h.ddnsDuePass(ctx)
		wait := time.Hour
		if !next.IsZero() {
			wait = next.Sub(m.now())
			if wait < 5*time.Second {
				wait = 5 * time.Second
			}
			if wait > time.Hour {
				wait = time.Hour
			}
		}
		timer.Reset(wait)
	}
}

// ddnsDuePass syncs nodes with a due retry / drift check and returns the
// next due time (zero = nothing scheduled).
func (h *Handler) ddnsDuePass(ctx context.Context) time.Time {
	m := h.ddnsState()
	nodes, err := h.repo.ListDDNSNodes()
	if err != nil || len(nodes) == 0 {
		return time.Time{}
	}
	enabled := make(map[int64]bool, len(nodes))
	for _, n := range nodes {
		enabled[n.ID] = true
	}
	records, err := h.repo.ListAllDDNSRecords()
	if err != nil {
		return time.Time{}
	}
	now := m.now()
	var next time.Time
	due := map[int64]bool{}
	for _, rec := range records {
		if !enabled[rec.NodeID] {
			continue
		}
		var at time.Time
		switch rec.Status {
		case model.DDNSStatusFailed:
			at = time.UnixMilli(rec.NextRetryAt)
		case model.DDNSStatusSynced:
			at = time.UnixMilli(rec.LastCheckAt).Add(ddns.DriftCheckEvery)
		default:
			continue // pending / upgrade / noaddr wait for an event
		}
		if !at.After(now) {
			due[rec.NodeID] = true
			continue
		}
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	for nodeID := range due {
		if ctx.Err() != nil {
			break
		}
		h.ddnsSyncNode(ctx, nodeID, false)
	}
	if len(due) > 0 {
		// Synced records moved their check time forward; recompute.
		return now.Add(5 * time.Second)
	}
	return next
}

func dnsProviderToken(p *model.DNSProvider) string {
	if p == nil {
		return ""
	}
	var creds struct {
		APIToken string `json:"api_token"`
	}
	_ = json.Unmarshal([]byte(p.Credentials), &creds)
	return strings.TrimSpace(creds.APIToken)
}

// SetDDNSAPIBase points the Cloudflare client at another base URL (contract
// tests run a fake API).
func (h *Handler) SetDDNSAPIBase(base string) {
	m := h.ddnsState()
	m.newClient = func(token string) *ddns.Client {
		c := ddns.NewCloudflare(token)
		c.BaseURL = base
		return c
	}
}

// WaitDDNS waits for background DDNS syncs (tests).
func (h *Handler) WaitDDNS() { h.ddnsState().pending.Wait() }
