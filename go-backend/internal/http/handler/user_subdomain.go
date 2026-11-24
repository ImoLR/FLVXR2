package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/ddns"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/usersub"
)

// 用户专属域名 (plan 303). Every non-admin user with a forward port (or a
// native landing node) on a node whose toggle is on gets <label>.<zone>, a
// CNAME to the node's DDNS main domain (DNS only, TTL 120, comment
// flvx-user). The reconcile pass reads the panel's facts, lets
// usersub.Compute decide, writes the lifecycle changes, then brings
// Cloudflare in line row by row. It runs after mutations (debounced kick)
// and every 10 minutes; with no toggle on and no record left it does two
// COUNT queries and nothing else. Only records the database knows (by id,
// read back first) are ever deleted.

const (
	userSubFullEvery   = 10 * time.Minute
	userSubDebounce    = 5 * time.Second
	userSubQuotaGuess  = 200 // CF free zones created since 2024-09
	userSubZoneTimeout = 20 * time.Second
)

type userSubZone struct {
	ProviderID  int64  `json:"providerId"`
	ZoneID      string `json:"-"`
	Name        string `json:"name"`
	Used        int    `json:"used"`
	Quota       int    `json:"quota"`
	QuotaGuess  bool   `json:"quotaGuess"`
	Wildcard    bool   `json:"wildcard"`
	UserRecords int    `json:"userRecords"` // flvx-user CNAMEs in the zone
	Foreign     int    `json:"foreign"`     // flvx-user records the database does not know (never touched)
	CheckedAt   int64  `json:"checkedAt"`
	Error       string `json:"error"`
	// byID / byName: the last listing (drift repair).
	byID   map[string]ddns.Record
	byName map[string][]ddns.Record
}

type userSubNodeWarn struct {
	Capped  int
	Waiting int
}

type userSubManager struct {
	mu       sync.Mutex
	run      sync.Mutex
	kick     chan struct{}
	zones    map[string]*userSubZone // provider|zone
	nodeWarn map[int64]userSubNodeWarn
	lastFull time.Time
	lastRun  time.Time
	nextDue  time.Time
	debounce time.Duration
	now      func() time.Time
	pending  sync.WaitGroup
}

var userSubInit sync.Mutex

func (h *Handler) userSubState() *userSubManager {
	userSubInit.Lock()
	defer userSubInit.Unlock()
	if h.userSub == nil {
		h.userSub = &userSubManager{
			kick:     make(chan struct{}, 1),
			zones:    map[string]*userSubZone{},
			nodeWarn: map[int64]userSubNodeWarn{},
			debounce: userSubDebounce,
			now:      time.Now,
		}
	}
	return h.userSub
}

func userSubZoneKey(providerID int64, zoneID string) string {
	return fmt.Sprintf("%d|%s", providerID, zoneID)
}

// userSubKick asks for a reconcile pass (debounced; never blocks).
func (h *Handler) userSubKick() {
	if h == nil {
		return
	}
	m := h.userSubState()
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// UserSubdomainKicker wraps the API mux: after a POST that may change who
// uses which node (users, forwards, tunnels, groups, nodes, landings,
// orders…) it kicks the reconcile. Reads are skipped; the 10-minute pass
// catches anything else.
func (h *Handler) UserSubdomainKicker(next http.Handler) http.Handler {
	prefixes := []string{"/api/v1/user/", "/api/v1/forward/", "/api/v1/tunnel/", "/api/v1/node/", "/api/v1/group/",
		"/api/v1/package/", "/api/v1/order/", "/api/v1/billing/", "/api/v1/landing/", "/api/v1/leikwan", "/api/v1/backup/",
		"/api/v1/tunnel-group/", "/api/v1/tunnel-group-new/"}
	reads := []string{"list", "detail", "get", "info", "stats", "latency", "history", "monitor", "check", "test", "preview", "options", "logs", "search"}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if r.Method != http.MethodPost {
			return
		}
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/v1/node/user-subdomain/") {
			return // runs its own pass
		}
		matched := false
		for _, p := range prefixes {
			if strings.HasPrefix(path, p) {
				matched = true
				break
			}
		}
		if !matched {
			return
		}
		last := strings.ToLower(path[strings.LastIndex(path, "/")+1:])
		for _, word := range reads {
			if strings.Contains(last, word) {
				return
			}
		}
		h.userSubKick()
	})
}

// runUserSubdomainLoop: kicks (debounced) and a full pass every 10 minutes;
// failed rows are retried at their backoff time.
func (h *Handler) runUserSubdomainLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	m := h.userSubState()
	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-m.kick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.debounce):
			}
			select {
			case <-m.kick:
			default:
			}
		}
		m.mu.Lock()
		full := m.now().Sub(m.lastFull) >= userSubFullEvery
		m.mu.Unlock()
		passCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		h.userSubReconcile(passCtx, full, false)
		cancel()
		m.mu.Lock()
		wait := userSubFullEvery - m.now().Sub(m.lastFull)
		if !m.nextDue.IsZero() {
			if d := m.nextDue.Sub(m.now()); d < wait {
				wait = d
			}
		}
		m.mu.Unlock()
		if wait < 30*time.Second {
			wait = 30 * time.Second
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
	}
}

// UserSubdomainReconcileNow runs one pass synchronously (tests, admin).
func (h *Handler) UserSubdomainReconcileNow(full bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h.userSubReconcile(ctx, full, true)
}

type userSubPass struct {
	nodes   map[int64]repo.UserSubdomainNode
	touched map[int64]bool // users whose live name changed (purge their subscriptions)
}

// userSubReconcile is one pass. full: list each involved zone (drift,
// wildcard, quota); force: ignore retry backoff.
func (h *Handler) userSubReconcile(ctx context.Context, full, force bool) {
	if h == nil || h.repo == nil {
		return
	}
	m := h.userSubState()
	m.run.Lock()
	defer m.run.Unlock()
	now := m.now()
	m.mu.Lock()
	m.lastRun = now
	if full {
		m.lastFull = now
	}
	m.nextDue = time.Time{}
	m.mu.Unlock()

	work, err := h.repo.AnyUserSubdomainWork()
	if err != nil {
		log.Printf("[user-subdomain] facts: %v", err)
		return
	}
	if !work {
		m.mu.Lock()
		m.nodeWarn = map[int64]userSubNodeWarn{}
		m.mu.Unlock()
		return
	}
	nodes, err := h.repo.ListUserSubdomainNodes()
	if err != nil {
		log.Printf("[user-subdomain] nodes: %v", err)
		return
	}
	names, err := h.repo.ListUserSubdomainsForPlan()
	if err != nil {
		log.Printf("[user-subdomain] names: %v", err)
		return
	}
	facts := &usersub.Facts{Now: now.UnixMilli(), Nodes: map[int64]usersub.Node{}, Room: map[string]int{}, Names: names}
	effective := []int64{}
	for id, n := range nodes {
		un := usersub.Node{ID: id, Enabled: n.Enabled(), MainDomain: n.Main.Domain, MainSynced: n.Main.Synced}
		if n.Main.ZoneID != "" && n.ProviderID > 0 {
			un.ZoneKey = userSubZoneKey(n.ProviderID, n.Main.ZoneID)
		}
		facts.Nodes[id] = un
		if un.Effective() {
			effective = append(effective, id)
		}
	}
	if facts.Usage, err = h.repo.UserSubdomainUsage(effective); err != nil {
		log.Printf("[user-subdomain] usage: %v", err)
		return
	}
	userIDs := map[int64]bool{}
	for _, users := range facts.Usage {
		for uid := range users {
			userIDs[uid] = true
		}
	}
	for _, n := range names {
		userIDs[n.UserID] = true
	}
	ids := make([]int64, 0, len(userIDs))
	for id := range userIDs {
		ids = append(ids, id)
	}
	if facts.Users, err = h.repo.UserSubdomainUsers(ids); err != nil {
		log.Printf("[user-subdomain] users: %v", err)
		return
	}

	// Zones: the effective nodes' zones (creation needs the quota) and the
	// zones of names still holding a record. Listed on a full pass or when
	// never seen; failures leave creation waiting.
	type zoneRef struct {
		providerID int64
		zoneID     string
	}
	zoneWant := map[string]zoneRef{}
	for _, id := range effective {
		if n := nodes[id]; n.Main.ZoneID != "" && n.ProviderID > 0 {
			zoneWant[userSubZoneKey(n.ProviderID, n.Main.ZoneID)] = zoneRef{n.ProviderID, n.Main.ZoneID}
		}
	}
	for _, n := range names {
		if n.RecordID != "" && n.ZoneID != "" {
			zoneWant[userSubZoneKey(n.ProviderID, n.ZoneID)] = zoneRef{n.ProviderID, n.ZoneID}
		}
	}
	for key, ref := range zoneWant {
		m.mu.Lock()
		z := m.zones[key]
		m.mu.Unlock()
		if z == nil || z.CheckedAt == 0 || full {
			h.userSubCheckZone(ctx, ref.providerID, ref.zoneID)
		}
		m.mu.Lock()
		if z := m.zones[key]; z != nil && z.CheckedAt > 0 && z.Error == "" {
			facts.Room[key] = usersub.Room(z.Used, z.Quota)
		}
		m.mu.Unlock()
	}

	plan := usersub.Compute(facts)
	pass := &userSubPass{nodes: nodes, touched: map[int64]bool{}}
	if err := h.repo.ApplyUserSubdomainChanges(plan.Changes); err != nil {
		log.Printf("[user-subdomain] apply: %v", err)
		return
	}
	byID := make(map[int64]model.UserSubdomain, len(names))
	for _, n := range names {
		byID[n.ID] = n
	}
	for _, c := range plan.Changes {
		if n, ok := byID[c.ID]; ok && n.State != c.State {
			pass.touched[n.UserID] = true
			if c.State == model.UserSubdomainRetired {
				log.Printf("[user-subdomain] %s retired (%s)", n.Domain, c.Reason)
			}
		}
	}
	for _, c := range plan.Creates {
		if ctx.Err() != nil {
			break
		}
		if err := h.userSubCreateName(c.UserID, nodes[c.NodeID]); err != nil {
			log.Printf("[user-subdomain] new name user %d node %d: %v", c.UserID, c.NodeID, err)
		}
	}
	m.mu.Lock()
	warn := map[int64]userSubNodeWarn{}
	for id, n := range plan.Capped {
		w := warn[id]
		w.Capped = n
		warn[id] = w
	}
	for id, n := range plan.Waiting {
		w := warn[id]
		w.Waiting = n
		warn[id] = w
	}
	m.nodeWarn = warn
	m.mu.Unlock()

	// Cloudflare side, row by row.
	rows, err := h.repo.ListUserSubdomainsForPlan()
	if err != nil {
		log.Printf("[user-subdomain] reload: %v", err)
		return
	}
	var next time.Time
	for i := range rows {
		if ctx.Err() != nil {
			break
		}
		row := rows[i]
		if !force && row.Status == model.UserSubdomainSyncFailed && row.NextRetryAt > now.UnixMilli() {
			at := time.UnixMilli(row.NextRetryAt)
			if next.IsZero() || at.Before(next) {
				next = at
			}
			continue
		}
		h.userSubSyncRow(ctx, pass, row, full)
	}
	m.mu.Lock()
	m.nextDue = next
	m.mu.Unlock()
	for uid := range pass.touched {
		h.subState().cache.purgeUser(uid)
	}
}

// userSubCreateName stores a fresh random name for (user, node) in the
// node main domain's zone. The record is created by the CF pass right after.
func (h *Handler) userSubCreateName(userID int64, node repo.UserSubdomainNode) error {
	m := h.userSubState()
	key := userSubZoneKey(node.ProviderID, node.Main.ZoneID)
	m.mu.Lock()
	z := m.zones[key]
	zoneName := ""
	var listed map[string][]ddns.Record
	if z != nil {
		zoneName, listed = z.Name, z.byName
	}
	m.mu.Unlock()
	if zoneName == "" {
		return errors.New("区域未知")
	}
	for attempt := 0; attempt < 8; attempt++ {
		label := usersub.NewLabel()
		domain := label + "." + zoneName
		if taken, err := h.repo.UserSubdomainLabelTaken(label); err != nil {
			return err
		} else if taken || len(listed[domain]) > 0 {
			continue
		}
		row := &model.UserSubdomain{UserID: userID, NodeID: node.ID, OriginNodeID: node.ID, Label: label, Domain: domain,
			ProviderID: node.ProviderID, ZoneID: node.Main.ZoneID}
		if err := h.repo.CreateUserSubdomain(row); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				continue
			}
			return err
		}
		m.mu.Lock()
		if z := m.zones[key]; z != nil {
			z.Used++ // until the next listing
		}
		m.mu.Unlock()
		return nil
	}
	return errors.New("没有找到空闲的名字")
}

// userSubCheckZone lists one zone (records, wildcard, flvx-user count) and
// reads its record quota. Two calls (plus pages beyond 100 records).
func (h *Handler) userSubCheckZone(ctx context.Context, providerID int64, zoneID string) {
	m := h.userSubState()
	key := userSubZoneKey(providerID, zoneID)
	z := &userSubZone{ProviderID: providerID, ZoneID: zoneID, CheckedAt: m.now().UnixMilli()}
	defer func() {
		m.mu.Lock()
		if old := m.zones[key]; old != nil && z.Error != "" {
			// keep the last good numbers, show the error
			keep := *old
			keep.Error, keep.CheckedAt = z.Error, z.CheckedAt
			z = &keep
		}
		m.zones[key] = z
		m.mu.Unlock()
	}()
	provider, err := h.repo.GetDNSProvider(providerID)
	if err != nil || provider == nil {
		z.Error = "DNS 服务商不存在或已删除"
		return
	}
	ctx, cancel := context.WithTimeout(ctx, userSubZoneTimeout)
	defer cancel()
	client := h.ddnsClient(provider)
	zones, err := h.ddnsZones(ctx, client, providerID)
	if err != nil {
		z.Error = userSubErrText(err)
		return
	}
	for _, zz := range zones {
		if zz.ID == zoneID {
			z.Name = strings.ToLower(zz.Name)
		}
	}
	if z.Name == "" {
		z.Error = "该 DNS 服务商的 Token 看不到这个区域"
		return
	}
	records, total, err := client.ZoneRecords(ctx, zoneID)
	if err != nil {
		z.Error = userSubErrText(err)
		return
	}
	z.Used = total
	z.byID = make(map[string]ddns.Record, len(records))
	z.byName = make(map[string][]ddns.Record, len(records))
	known, _ := h.repo.ListUserSubdomains(0, 0, true)
	ours := map[string]bool{}
	for _, n := range known {
		ours[strings.ToLower(n.Domain)] = true
	}
	for _, rec := range records {
		name := strings.ToLower(strings.TrimSuffix(rec.Name, "."))
		z.byID[rec.ID] = rec
		z.byName[name] = append(z.byName[name], rec)
		if name == "*."+z.Name {
			z.Wildcard = true
		}
		if ddns.IsUserRecord(rec) {
			z.UserRecords++
			if !ours[name] {
				z.Foreign++
			}
		}
	}
	if usage, err := client.ZoneUsage(ctx, zoneID); err == nil && usage.Quota > 0 {
		z.Quota = usage.Quota
		if usage.Usage > 0 {
			z.Used = usage.Usage
		}
	} else {
		z.Quota, z.QuotaGuess = userSubQuotaGuess, true
	}
}

func userSubErrText(err error) string {
	if ddns.IsAuthError(err) {
		return "Cloudflare 拒绝了 API Token（可能已被撤销或更换）：" + err.Error()
	}
	return err.Error()
}

// userSubSyncRow brings one name's Cloudflare record in line with its state.
func (h *Handler) userSubSyncRow(ctx context.Context, pass *userSubPass, row model.UserSubdomain, full bool) {
	m := h.userSubState()
	now := m.now()
	desired := ""
	if row.State == model.UserSubdomainActive {
		if n, ok := pass.nodes[row.NodeID]; ok && n.Enabled() && n.Main.Synced {
			desired = n.Main.Domain
		}
	}
	zoneKey := userSubZoneKey(row.ProviderID, row.ZoneID)
	m.mu.Lock()
	z := m.zones[zoneKey]
	var listing *userSubZone
	if full && z != nil && z.Error == "" && z.byID != nil {
		listing = z
	}
	m.mu.Unlock()

	needs := false
	switch {
	case desired != "":
		needs = row.RecordID == "" || row.Target != desired || row.Status != model.UserSubdomainSyncSynced
		if !needs && listing != nil {
			cur, ok := listing.byID[row.RecordID]
			needs = !ok || !strings.EqualFold(strings.TrimSuffix(cur.Content, "."), desired) || cur.Proxied
		}
	default:
		needs = row.RecordID != "" || row.Status != model.UserSubdomainSyncSynced
		if !needs && listing != nil {
			for _, rec := range listing.byName[strings.ToLower(row.Domain)] {
				if rec.Type == "CNAME" && ddns.IsUserRecord(rec) {
					needs = true // our leftover (e.g. a crash between create and save)
				}
			}
		}
	}
	if !needs {
		return
	}
	provider, err := h.repo.GetDNSProvider(row.ProviderID)
	if err != nil || provider == nil {
		h.userSubFail(row, ddnsConfigErr("DNS 服务商不存在或已删除"), now)
		return
	}
	client := h.ddnsClient(provider)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	wasLive := row.Live()
	if desired == "" {
		if row.RecordID != "" {
			if err := client.DeleteUserRecord(ctx, row.ZoneID, row.RecordID, row.Domain); err != nil && !errors.Is(err, ddns.ErrNotUserRecord) {
				h.userSubFail(row, err, now)
				return
			}
		}
		if listing != nil {
			for _, rec := range listing.byName[strings.ToLower(row.Domain)] {
				if rec.Type == "CNAME" && ddns.IsUserRecord(rec) && rec.ID != row.RecordID {
					if err := client.DeleteUserRecord(ctx, row.ZoneID, rec.ID, row.Domain); err != nil && !errors.Is(err, ddns.ErrNotUserRecord) {
						h.userSubFail(row, err, now)
						return
					}
				}
			}
		}
		_ = h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{
			"record_id": "", "target": "", "status": model.UserSubdomainSyncSynced, "last_error": "",
			"fail_count": 0, "next_retry_at": int64(0), "last_sync_at": now.UnixMilli(),
		})
		if row.RecordID != "" {
			m.mu.Lock()
			if z := m.zones[zoneKey]; z != nil && z.Used > 0 {
				z.Used--
			}
			m.mu.Unlock()
			log.Printf("[user-subdomain] %s record deleted (%s %s)", row.Domain, row.State, row.Reason)
		}
		if wasLive {
			pass.touched[row.UserID] = true
		}
		return
	}
	recordID := row.RecordID
	if recordID != "" {
		if _, err := client.UpdateUserCNAME(ctx, row.ZoneID, recordID, row.Domain, desired); err != nil {
			if !ddns.IsNotFound(err) {
				h.userSubFail(row, err, now)
				return
			}
			recordID = "" // removed in the dashboard: create it again
		}
	}
	created := false
	if recordID == "" {
		existing, err := client.ListRecords(ctx, row.ZoneID, row.Domain, "")
		if err != nil {
			h.userSubFail(row, err, now)
			return
		}
		for _, rec := range existing {
			if rec.Type == "CNAME" && ddns.IsUserRecord(rec) {
				recordID = rec.ID // ours (left over): adopt
				continue
			}
			h.userSubFail(row, ddnsConfigErr("Cloudflare 里已有同名 %s 记录（不是面板创建的），请「换新名」", rec.Type), now)
			return
		}
		if recordID != "" {
			if _, err := client.UpdateUserCNAME(ctx, row.ZoneID, recordID, row.Domain, desired); err != nil {
				h.userSubFail(row, err, now)
				return
			}
		} else {
			rec, err := client.CreateUserCNAME(ctx, row.ZoneID, row.Domain, desired)
			if err != nil {
				h.userSubFail(row, err, now)
				return
			}
			recordID, created = rec.ID, true
		}
	}
	_ = h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{
		"record_id": recordID, "target": desired, "status": model.UserSubdomainSyncSynced, "last_error": "",
		"fail_count": 0, "next_retry_at": int64(0), "last_sync_at": now.UnixMilli(),
	})
	if created {
		log.Printf("[user-subdomain] %s CNAME %s created", row.Domain, desired)
	} else if row.Target != desired {
		log.Printf("[user-subdomain] %s CNAME -> %s", row.Domain, desired)
	}
	pass.touched[row.UserID] = true
}

func (h *Handler) userSubFail(row model.UserSubdomain, err error, now time.Time) {
	fails := row.FailCount + 1
	delay := ddns.Backoff(fails)
	var cfg *ddnsConfigError
	if errors.As(err, &cfg) || ddns.IsAuthError(err) {
		delay = time.Hour
	}
	msg := userSubErrText(err)
	if ddns.IsQuotaError(err) {
		msg = "Cloudflare 记录数已到上限：" + err.Error()
	}
	_ = h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{
		"status": model.UserSubdomainSyncFailed, "last_error": msg, "fail_count": fails,
		"next_retry_at": now.Add(delay).UnixMilli(),
	})
	if fails == 1 || fails%10 == 0 {
		log.Printf("[user-subdomain] %s sync failed (%d): %s", row.Domain, fails, msg)
	}
}

// userSubRewriteForwardItems: the forward list's inIp of every rule shows
// its owner's live name for that entry (one query when nobody has one).
func (h *Handler) userSubRewriteForwardItems(items []map[string]interface{}, entryNodes map[int64][]int64) {
	if len(items) == 0 {
		return
	}
	seen := map[int64]bool{}
	users := []int64{}
	for _, item := range items {
		if uid := asInt64(item["userId"], 0); uid > 0 && !seen[uid] {
			seen[uid] = true
			users = append(users, uid)
		}
	}
	names, err := h.repo.LoadUserEntryNames(users)
	if err != nil || names.Empty() {
		return
	}
	for _, item := range items {
		list, ok := item["inIp"].(string)
		if !ok || list == "" {
			continue
		}
		id := asInt64(item["id"], 0)
		item["inIp"] = names.RewriteEntryList(asInt64(item["userId"], 0), entryNodes[id], list)
	}
}

// ---- node save (DDNS section toggle) ----

// validateUserSubdomainToggle: turning the toggle on needs DDNS with a main
// record, and a zone without a wildcard on the names' level.
func (h *Handler) validateUserSubdomainToggle(ctx context.Context, nodeID int64, plan *ddnsPlan) error {
	if plan == nil || plan.userSubdomain == nil || !*plan.userSubdomain {
		return nil
	}
	if !plan.enabled {
		return errors.New("「用户专属域名」需要先开启 DDNS")
	}
	main := ""
	for _, rec := range plan.records {
		if rec.Role == model.DDNSRoleMain && (main == "" || rec.RecordType == "AAAA") {
			main = rec.Domain
		}
	}
	if main == "" {
		return errors.New("「用户专属域名」需要一条「主地址」DDNS 记录（专属域名都指向它）")
	}
	if nodeID > 0 {
		if n, err := h.repo.GetNodeByID(nodeID); err == nil && n != nil && n.UserSubdomainEnabled && n.DDNSProviderID == plan.providerID {
			return nil // already on: no Cloudflare round trip on unrelated edits
		}
	}
	provider, err := h.repo.GetDNSProvider(plan.providerID)
	if err != nil || provider == nil {
		return errors.New("请选择 DNS 服务商")
	}
	client := h.ddnsClient(provider)
	zone, err := h.ddnsZoneFor(ctx, client, provider, main)
	if err != nil {
		return ddnsSaveError(err)
	}
	wild, err := client.ListRecords(ctx, zone.ID, "*."+zone.Name, "")
	if err != nil {
		return ddnsSaveError(err)
	}
	if len(wild) > 0 {
		return fmt.Errorf("区域 %s 有通配记录 *.%s：暂停或删除的专属域名仍会解析到它，不能开启「用户专属域名」", zone.Name, zone.Name)
	}
	return nil
}

// ---- views ----

type userSubNodeView struct {
	Toggle     bool   `json:"toggle"`
	Effective  bool   `json:"effective"`
	MainDomain string `json:"mainDomain"`
	Active     int    `json:"active"`
	Paused     int    `json:"paused"`
	Failed     int    `json:"failed"`
	Pending    int    `json:"pending"`
	Capped     int    `json:"capped"`
	Waiting    int    `json:"waiting"`
	Wildcard   bool   `json:"wildcard"`
	ZoneError  string `json:"zoneError"`
	Attention  bool   `json:"attention"`
}

// userSubNodeViews: the node list pill data (one query + memory).
func (h *Handler) userSubNodeViews(nodes []repo.DDNSNodeState, records []model.NodeDDNSRecord) map[int64]*userSubNodeView {
	counts, err := h.repo.UserSubdomainCounts()
	if err != nil {
		return nil
	}
	toggled, err := h.repo.ListUserSubdomainToggleNodes()
	if err != nil {
		return nil
	}
	if len(counts) == 0 && len(toggled) == 0 {
		return nil
	}
	m := h.userSubState()
	byNode := map[int64][]model.NodeDDNSRecord{}
	for _, rec := range records {
		byNode[rec.NodeID] = append(byNode[rec.NodeID], rec)
	}
	ddnsOn := map[int64]bool{}
	for _, n := range nodes {
		ddnsOn[n.ID] = true
	}
	out := map[int64]*userSubNodeView{}
	m.mu.Lock()
	defer m.mu.Unlock()
	view := func(id int64) *userSubNodeView {
		v := out[id]
		if v == nil {
			v = &userSubNodeView{}
			out[id] = v
		}
		return v
	}
	for id := range toggled {
		v := view(id)
		v.Toggle = true
		main := repo.UserSubdomainMainOf(byNode[id], nil)
		v.MainDomain = main.Domain
		v.Effective = ddnsOn[id] && main.Domain != "" && main.Synced
		if main.ZoneID != "" {
			for _, n := range nodes {
				if n.ID == id {
					if z := m.zones[userSubZoneKey(n.DDNSProviderID, main.ZoneID)]; z != nil {
						v.Wildcard, v.ZoneError = z.Wildcard, z.Error
					}
				}
			}
		}
	}
	for id, c := range counts {
		v := view(id)
		v.Active, v.Paused, v.Failed, v.Pending = c.Active, c.Paused, c.Failed, c.Pending
	}
	for id, w := range m.nodeWarn {
		if v := out[id]; v != nil {
			v.Capped, v.Waiting = w.Capped, w.Waiting
		}
	}
	for _, v := range out {
		v.Attention = v.Failed > 0 || v.Capped > 0 || v.Wildcard || v.ZoneError != ""
	}
	return out
}

// ---- admin API (under /api/v1/node/: admins only) ----

func (h *Handler) registerUserSubdomain(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/node/user-subdomain/list", h.userSubList)
	mux.HandleFunc("/api/v1/node/user-subdomain/repoint", h.userSubRepoint)
	mux.HandleFunc("/api/v1/node/user-subdomain/rename", h.userSubRename)
	mux.HandleFunc("/api/v1/node/user-subdomain/delete", h.userSubDelete)
	mux.HandleFunc("/api/v1/node/user-subdomain/allow", h.userSubAllow)
	mux.HandleFunc("/api/v1/node/user-subdomain/sync", h.userSubSyncNow)
}

type userSubForwardView = repo.UserSubdomainForward

type userSubItemView struct {
	ID             int64                `json:"id"`
	UserID         int64                `json:"userId"`
	UserName       string               `json:"userName"`
	Label          string               `json:"label"`
	Domain         string               `json:"domain"`
	State          string               `json:"state"`
	Reason         string               `json:"reason"`
	Status         string               `json:"status"`
	LastError      string               `json:"lastError"`
	LastSyncAt     int64                `json:"lastSyncAt"`
	NodeID         int64                `json:"nodeId"`
	NodeName       string               `json:"nodeName"`
	OriginNodeID   int64                `json:"originNodeId"`
	OriginNodeName string               `json:"originNodeName"`
	Moved          string               `json:"moved"` // "in" | "out" | ""
	Target         string               `json:"target"`
	Blocked        bool                 `json:"blocked"`
	Forwards       []userSubForwardView `json:"forwards"`
	Landings       int                  `json:"landings"`
	CreatedTime    int64                `json:"createdTime"`
	RetiredAt      int64                `json:"retiredAt"`
	UnusedSince    int64                `json:"unusedSince"`
}

func (h *Handler) userSubList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		NodeID  int64 `json:"nodeId"`
		UserID  int64 `json:"userId"`
		Retired bool  `json:"retired"`
	}
	if err := decodeJSON(r.Body, &req); err != nil || (req.NodeID <= 0 && req.UserID <= 0) {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	data, err := h.userSubListData(req.NodeID, req.UserID, req.Retired)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(data))
}

func (h *Handler) userSubListData(nodeID, userID int64, retired bool) (map[string]interface{}, error) {
	rows, err := h.repo.ListUserSubdomains(nodeID, userID, true)
	if err != nil {
		return nil, err
	}
	nodeIDs, userIDs := map[int64]bool{}, map[int64]bool{}
	if nodeID > 0 {
		nodeIDs[nodeID] = true
	}
	for _, row := range rows {
		nodeIDs[row.NodeID], nodeIDs[row.OriginNodeID], userIDs[row.UserID] = true, true, true
	}
	nodeNames, err := h.repo.NodeNames(userSubKeys(nodeIDs))
	if err != nil {
		return nil, err
	}
	userNames, err := h.repo.UserNames(userSubKeys(userIDs))
	if err != nil {
		return nil, err
	}
	uses, landings, err := h.repo.UserSubdomainForwardUses(userSubKeys(userIDs), userSubKeys(nodeIDs))
	if err != nil {
		return nil, err
	}
	items := []userSubItemView{}
	history := []userSubItemView{}
	counts := map[string]int{}
	for _, row := range rows {
		v := userSubItemView{ID: row.ID, UserID: row.UserID, UserName: userNames[row.UserID], Label: row.Label, Domain: row.Domain,
			State: row.State, Reason: row.Reason, Status: row.Status, LastError: row.LastError, LastSyncAt: row.LastSyncAt,
			NodeID: row.NodeID, NodeName: nodeNames[row.NodeID], OriginNodeID: row.OriginNodeID, OriginNodeName: nodeNames[row.OriginNodeID],
			Target: row.Target, Blocked: row.Blocked, CreatedTime: row.CreatedTime, RetiredAt: row.RetiredAt, UnusedSince: row.UnusedSince,
			Forwards: uses[[2]int64{row.UserID, row.NodeID}], Landings: landings[[2]int64{row.UserID, row.NodeID}]}
		if v.Forwards == nil {
			v.Forwards = []userSubForwardView{}
		}
		if v.UserName == "" {
			v.UserName = fmt.Sprintf("（已删除用户 #%d）", row.UserID)
		}
		if nodeID > 0 && row.OriginNodeID != row.NodeID {
			if row.NodeID == nodeID {
				v.Moved = "in"
			} else {
				v.Moved = "out"
			}
		} else if nodeID == 0 && row.OriginNodeID != row.NodeID {
			v.Moved = "in"
		}
		if row.State == model.UserSubdomainRetired {
			counts["retired"]++
			if retired {
				history = append(history, v)
			}
			continue
		}
		if v.Moved != "out" {
			counts[row.State]++
			if row.Status == model.UserSubdomainSyncFailed {
				counts["failed"]++
			}
		}
		items = append(items, v)
	}
	out := map[string]interface{}{"items": items, "history": history, "counts": counts}
	nodes, err := h.repo.ListUserSubdomainNodes()
	if err != nil {
		return nil, err
	}
	// 改指 targets: nodes with the toggle on and a synced main domain.
	targets := []map[string]interface{}{}
	for _, n := range nodes {
		if n.Enabled() && n.Main.Domain != "" && n.Main.Synced {
			targets = append(targets, map[string]interface{}{"id": n.ID, "name": n.Name, "mainDomain": n.Main.Domain})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i]["id"].(int64) < targets[j]["id"].(int64) })
	out["targets"] = targets
	if nodeID > 0 {
		n, ok := nodes[nodeID]
		if !ok {
			// toggle off and no names: read the node's main domain directly
			if records, err := h.repo.GetNodeDDNSRecords(nodeID); err == nil {
				n.Main = repo.UserSubdomainMainOf(records, nil)
			}
			if node, err := h.repo.GetNodeByID(nodeID); err == nil && node != nil {
				n.ID, n.Name, n.DDNSEnabled, n.ProviderID = node.ID, node.Name, node.DDNSEnabled, node.DDNSProviderID
			}
		}
		m := h.userSubState()
		m.mu.Lock()
		var zone *userSubZone
		if z := m.zones[userSubZoneKey(n.ProviderID, n.Main.ZoneID)]; z != nil {
			cp := *z
			cp.byID, cp.byName = nil, nil
			zone = &cp
		}
		warn := m.nodeWarn[nodeID]
		m.mu.Unlock()
		out["node"] = map[string]interface{}{
			"id": nodeID, "name": nodeNames[nodeID], "toggle": n.Toggle, "ddnsEnabled": n.DDNSEnabled,
			"mainDomain": n.Main.Domain, "mainIps": n.Main.IPs, "mainSynced": n.Main.Synced, "v6Only": n.Main.V6Only,
			"effective": n.Enabled() && n.Main.Domain != "" && n.Main.Synced,
		}
		out["zone"] = zone
		out["capped"], out["waiting"] = warn.Capped, warn.Waiting
	}
	return out, nil
}

func userSubKeys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		if k > 0 {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (h *Handler) userSubAction(w http.ResponseWriter, r *http.Request, act func(row *model.UserSubdomain, req map[string]interface{}) error) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req map[string]interface{}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	id := asInt64(req["id"], 0)
	row, err := h.repo.GetUserSubdomain(id)
	if err != nil || row == nil {
		response.WriteJSON(w, response.ErrDefault("专属域名不存在"))
		return
	}
	if err := act(row, req); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	h.UserSubdomainReconcileNow(false)
	fresh, _ := h.repo.GetUserSubdomain(id)
	response.WriteJSON(w, response.OK(fresh))
}

// 改指: the CNAME → another node's main domain (the user must already use it).
func (h *Handler) userSubRepoint(w http.ResponseWriter, r *http.Request) {
	h.userSubAction(w, r, func(row *model.UserSubdomain, req map[string]interface{}) error {
		if row.State == model.UserSubdomainRetired {
			return errors.New("这个名字已作废")
		}
		target := asInt64(req["nodeId"], 0)
		if target == row.NodeID {
			return errors.New("已经指向这个节点")
		}
		nodes, err := h.repo.ListUserSubdomainNodes()
		if err != nil {
			return err
		}
		n, ok := nodes[target]
		if !ok || !n.Enabled() {
			return errors.New("目标节点没有开启「用户专属域名」")
		}
		if n.Main.Domain == "" || !n.Main.Synced {
			return errors.New("目标节点的 DDNS 主域名还没同步")
		}
		usage, err := h.repo.UserSubdomainUsage([]int64{target})
		if err != nil {
			return err
		}
		if !usage[target][row.UserID] {
			return fmt.Errorf("该用户在「%s」上没有转发：先给他建好同端口的转发再改指，否则客户端连不上", n.Name)
		}
		return h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{
			"node_id": target, "status": model.UserSubdomainSyncPending, "next_retry_at": int64(0), "fail_count": 0, "unused_since": int64(0),
		})
	})
}

// 换新名: the old name is retired (record deleted), a new random one takes
// its place for the same user / node.
func (h *Handler) userSubRename(w http.ResponseWriter, r *http.Request) {
	h.userSubAction(w, r, func(row *model.UserSubdomain, _ map[string]interface{}) error {
		if row.State == model.UserSubdomainRetired {
			return errors.New("这个名字已作废")
		}
		zoneName := strings.TrimPrefix(row.Domain, row.Label+".")
		for attempt := 0; attempt < 8; attempt++ {
			label := usersub.NewLabel()
			if taken, err := h.repo.UserSubdomainLabelTaken(label); err != nil {
				return err
			} else if taken {
				continue
			}
			if err := h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{
				"state": model.UserSubdomainRetired, "reason": model.UserSubdomainReasonRenamed, "retired_at": time.Now().UnixMilli(),
				"status": model.UserSubdomainSyncPending, "next_retry_at": int64(0),
			}); err != nil {
				return err
			}
			return h.repo.CreateUserSubdomain(&model.UserSubdomain{UserID: row.UserID, NodeID: row.NodeID, OriginNodeID: row.OriginNodeID,
				Label: label, Domain: label + "." + zoneName, ProviderID: row.ProviderID, ZoneID: row.ZoneID, State: row.State, Reason: row.Reason})
		}
		return errors.New("没有找到空闲的名字")
	})
}

// 删除: retired for good; no new name for this user on this node until
// 重新分配 (the user's links fall back to the node main domain).
func (h *Handler) userSubDelete(w http.ResponseWriter, r *http.Request) {
	h.userSubAction(w, r, func(row *model.UserSubdomain, _ map[string]interface{}) error {
		if row.State == model.UserSubdomainRetired {
			return errors.New("这个名字已作废")
		}
		return h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{
			"state": model.UserSubdomainRetired, "reason": model.UserSubdomainReasonAdmin, "blocked": true,
			"retired_at": time.Now().UnixMilli(), "status": model.UserSubdomainSyncPending, "next_retry_at": int64(0),
		})
	})
}

// 重新分配: lift an admin delete; the next pass gives the user a new name.
func (h *Handler) userSubAllow(w http.ResponseWriter, r *http.Request) {
	h.userSubAction(w, r, func(row *model.UserSubdomain, _ map[string]interface{}) error {
		if !row.Blocked {
			return errors.New("这个名字没有被管理员删除")
		}
		return h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{"blocked": false})
	})
}

// 立即同步: a full pass now (zone listing + retries without backoff).
func (h *Handler) userSubSyncNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		NodeID int64 `json:"nodeId"`
		UserID int64 `json:"userId"`
	}
	_ = decodeJSON(r.Body, &req)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	h.userSubReconcile(ctx, true, true)
	if req.NodeID <= 0 && req.UserID <= 0 {
		response.WriteJSON(w, response.OKEmpty())
		return
	}
	data, err := h.userSubListData(req.NodeID, req.UserID, false)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(data))
}

// userSubProviderStats: the DNS 服务商 card line (zone use / quota and the
// provider's names). Zones not checked for 10 minutes are checked now
// (2 calls each, admin page only).
func (h *Handler) userSubProviderStats(ctx context.Context, providers []model.DNSProvider) map[int64]map[string]interface{} {
	counts, _ := h.repo.UserSubdomainProviderCounts()
	m := h.userSubState()
	out := map[int64]map[string]interface{}{}
	for _, p := range providers {
		if dnsProviderToken(&p) == "" {
			continue
		}
		client := h.ddnsClient(&p)
		zctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		zones, err := h.ddnsZones(zctx, client, p.ID)
		cancel()
		list := []userSubZone{}
		if err == nil {
			for _, z := range zones {
				key := userSubZoneKey(p.ID, z.ID)
				m.mu.Lock()
				cur := m.zones[key]
				m.mu.Unlock()
				if cur == nil || m.now().Sub(time.UnixMilli(cur.CheckedAt)) >= userSubFullEvery {
					zctx, cancel := context.WithTimeout(ctx, 10*time.Second)
					h.userSubCheckZone(zctx, p.ID, z.ID)
					cancel()
				}
				m.mu.Lock()
				if cur = m.zones[key]; cur != nil {
					cp := *cur
					cp.byID, cp.byName = nil, nil
					list = append(list, cp)
				}
				m.mu.Unlock()
			}
		}
		c := counts[p.ID]
		out[p.ID] = map[string]interface{}{"zones": list, "names": c[0], "withRecord": c[1]}
	}
	return out
}
