package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
//
// Round 3 (user 11-24 evening): a user is never handed the main domain. His
// name is reserved in the same request that makes him use the node (the
// kicker reserves before the response leaves), shown at once with
// 「正在就绪」, and a fast pass creates + verifies the record within seconds.
// A fresh name that cannot get a record (zone at the cap, creating keeps
// failing) is not handed out at all (usersub.Hidden).

const (
	userSubFullEvery   = 10 * time.Minute
	userSubDebounce    = 5 * time.Second
	userSubFastWait    = 200 * time.Millisecond // a name was just reserved: create its record now
	userSubReserveWait = 4 * time.Second        // in-request reservation budget (zone name lookup)
	userSubQuotaGuess  = 200                    // CF free zones created since 2024-09
	userSubZoneTimeout = 20 * time.Second
	userSubMinWait     = 3 * time.Second
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
	// authIPs (round 3): the zone's authoritative servers (ip:53) — a fresh
	// name is ready once they answer it.
	authIPs []string
}

type userSubNodeWarn struct {
	Waiting int // names not reserved yet: the zone name is not known (Cloudflare unreachable)
}

type userSubManager struct {
	mu       sync.Mutex
	run      sync.Mutex
	reserve  sync.Mutex // one name per (user, node): reservations never overlap
	fast     atomic.Bool
	kick     chan struct{}
	fastKick chan struct{}           // cuts a running debounce short
	zones    map[string]*userSubZone // provider|zone
	nodeWarn map[int64]userSubNodeWarn
	lastFull time.Time
	lastRun  time.Time
	nextDue  time.Time
	debounce time.Duration
	now      func() time.Time
	pending  sync.WaitGroup
	// dnsCheck (tests): replaces the authoritative-server query.
	dnsCheck func(name string) bool
}

// SetUserSubdomainDNSCheck replaces the authoritative DNS check of fresh
// names (contract tests: Cloudflare's edge answering a few seconds late).
func (h *Handler) SetUserSubdomainDNSCheck(fn func(name string) bool) {
	m := h.userSubState()
	m.mu.Lock()
	m.dnsCheck = fn
	m.mu.Unlock()
}

var userSubInit sync.Mutex

func (h *Handler) userSubState() *userSubManager {
	userSubInit.Lock()
	defer userSubInit.Unlock()
	if h.userSub == nil {
		h.userSub = &userSubManager{
			kick:     make(chan struct{}, 1),
			fastKick: make(chan struct{}, 1),
			zones:    map[string]*userSubZone{},
			nodeWarn: map[int64]userSubNodeWarn{},
			debounce: userSubDebounce,
			now:      time.Now,
		}
	}
	return h.userSub
}

func (m *userSubManager) kickDebounce() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.debounce
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

// userSubKickFast: a name was just reserved — run the pass almost at once
// so its record exists within seconds.
func (h *Handler) userSubKickFast() {
	if h == nil {
		return
	}
	m := h.userSubState()
	m.fast.Store(true)
	select {
	case m.fastKick <- struct{}{}:
	default:
	}
	h.userSubKick()
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
	// Round 3: read-only POSTs by exact last segment (套餐页, copy links,
	// payment status polling…) — the kicker now also reserves names.
	exactReads := map[string]bool{"package": true, "my-subscription": true, "links": true, "ips": true, "client-ips": true, "groups": true,
		"status": true, "diagnose": true, "feature-status": true}
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
		if exactReads[last] {
			return
		}
		for _, word := range reads {
			if strings.Contains(last, word) {
				return
			}
		}
		// Round 3: reserve new names now (DB only, before the response
		// leaves), so the very next list already shows the final name.
		ctx, cancel := context.WithTimeout(context.Background(), userSubReserveWait)
		n := h.userSubReserve(ctx)
		cancel()
		if n > 0 {
			h.userSubKickFast()
			return
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
			wait := m.kickDebounce()
			if m.fast.Swap(false) && wait > userSubFastWait {
				wait = userSubFastWait
			}
			// A name reserved during a normal debounce must not wait it out.
			debounce := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				debounce.Stop()
				return
			case <-debounce.C:
			case <-m.fastKick:
				debounce.Stop()
				select {
				case <-ctx.Done():
					return
				case <-time.After(userSubFastWait):
				}
			}
			m.fast.Store(false)
			select {
			case <-m.fastKick:
			default:
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
		if wait < userSubMinWait {
			wait = userSubMinWait
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

// StartUserSubdomainLoop runs only the 专属域名 loop with the given kick
// debounce (contract tests: 续期 / 启用 through the API and the kicker);
// stop cancels it and waits for the running pass.
func (h *Handler) StartUserSubdomainLoop(debounce time.Duration) (stop func()) {
	m := h.userSubState()
	m.mu.Lock()
	m.debounce = debounce
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	h.jobsWG.Add(1)
	go h.runUserSubdomainLoop(ctx)
	return func() { cancel(); h.jobsWG.Wait() }
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
	facts, nodes, effective, err := h.userSubFacts(now, true)
	if err != nil {
		log.Printf("[user-subdomain] %v", err)
		return
	}
	names := facts.Names

	// Zones: the effective nodes' zones (creation needs the quota) and the
	// zones of names holding or wanting a record. Listed on a full pass or
	// when never seen.
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
		if (n.RecordID != "" || n.State == model.UserSubdomainActive) && n.ZoneID != "" {
			zoneWant[userSubZoneKey(n.ProviderID, n.ZoneID)] = zoneRef{n.ProviderID, n.ZoneID}
		}
	}
	for key, ref := range zoneWant {
		m.mu.Lock()
		z := m.zones[key]
		m.mu.Unlock()
		// A zone whose last check failed is re-checked after 30 s (fresh
		// names wait on it); others only on the full pass.
		retry := z != nil && z.Error != "" && now.Sub(time.UnixMilli(z.CheckedAt)) >= 30*time.Second
		if z == nil || z.CheckedAt == 0 || full || retry {
			h.userSubCheckZone(ctx, ref.providerID, ref.zoneID)
		}
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
	waiting := h.userSubReserveCreates(ctx, nodes, plan.Creates, pass.touched)
	m.mu.Lock()
	warn := map[int64]userSubNodeWarn{}
	for id, n := range waiting {
		warn[id] = userSubNodeWarn{Waiting: n}
	}
	m.nodeWarn = warn
	m.mu.Unlock()

	// Cloudflare side, row by row: deletes / updates first (they free
	// room), then the names that need a new record, admitted by the zone's
	// room (round 3: the cap limits records, never names).
	rows, err := h.repo.ListUserSubdomainsForPlan()
	if err != nil {
		log.Printf("[user-subdomain] reload: %v", err)
		return
	}
	var next time.Time
	creates := map[string][]model.UserSubdomain{}
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
		if h.userSubDesired(pass, row) != "" && row.RecordID == "" {
			key := userSubZoneKey(row.ProviderID, row.ZoneID)
			creates[key] = append(creates[key], row)
			continue
		}
		h.userSubSyncRow(ctx, pass, row, full)
	}
	zoneKeys := make([]string, 0, len(creates))
	for k := range creates {
		zoneKeys = append(zoneKeys, k)
	}
	sort.Strings(zoneKeys)
	for _, key := range zoneKeys {
		list := creates[key]
		m.mu.Lock()
		z := m.zones[key]
		room, known, zoneErr := 0, false, "区域还没检查"
		if z != nil && z.CheckedAt > 0 && z.Error == "" && z.Quota > 0 {
			room, known = usersub.Room(z.Used, z.Quota), true
		} else if z != nil && z.Error != "" {
			zoneErr = z.Error
		}
		used, quota := 0, 0
		if z != nil {
			used, quota = z.Used, z.Quota
		}
		m.mu.Unlock()
		admit, capped, unknown := usersub.Admit(list, room, known)
		rowOf := make(map[int64]model.UserSubdomain, len(list))
		for _, row := range list {
			rowOf[row.ID] = row
		}
		for _, id := range admit {
			if ctx.Err() != nil {
				break
			}
			h.userSubSyncRow(ctx, pass, rowOf[id], full)
		}
		for _, id := range capped {
			h.userSubMarkCapped(pass, rowOf[id], fmt.Sprintf("Cloudflare 记录数已到上限（%d / %d，预留 %d 条给管理员 DDNS）：删除不用的记录或提高上限后自动创建", used, quota, usersub.CapReserve))
		}
		for _, id := range unknown {
			h.userSubFail(pass, rowOf[id], fmt.Errorf("Cloudflare 区域检查失败：%s", zoneErr), now)
		}
	}
	h.userSubConfirm(ctx, pass)
	rows, _ = h.repo.ListUserSubdomainsForPlan()
	for _, row := range rows {
		if row.Status == model.UserSubdomainSyncFailed && row.NextRetryAt > now.UnixMilli() {
			at := time.UnixMilli(row.NextRetryAt)
			if next.IsZero() || at.Before(next) {
				next = at
			}
		}
	}
	m.mu.Lock()
	m.nextDue = next
	m.mu.Unlock()
	for uid := range pass.touched {
		h.subState().cache.purgeUser(uid)
	}
}

// userSubFacts loads what the planner reads. withNames=false loads only
// the names (and users) needed to know who still needs one (reservation).
func (h *Handler) userSubFacts(now time.Time, all bool) (*usersub.Facts, map[int64]repo.UserSubdomainNode, []int64, error) {
	nodes, err := h.repo.ListUserSubdomainNodes()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("nodes: %w", err)
	}
	facts := &usersub.Facts{Now: now.UnixMilli(), Nodes: map[int64]usersub.Node{}}
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
	sort.Slice(effective, func(i, j int) bool { return effective[i] < effective[j] })
	if !all && len(effective) == 0 {
		return facts, nodes, effective, nil
	}
	if facts.Usage, err = h.repo.UserSubdomainUsage(effective); err != nil {
		return nil, nil, nil, fmt.Errorf("usage: %w", err)
	}
	if facts.Names, err = h.repo.ListUserSubdomainsForPlan(); err != nil {
		return nil, nil, nil, fmt.Errorf("names: %w", err)
	}
	userIDs := map[int64]bool{}
	for _, users := range facts.Usage {
		for uid := range users {
			userIDs[uid] = true
		}
	}
	if all {
		for _, n := range facts.Names {
			userIDs[n.UserID] = true
		}
	}
	if facts.Users, err = h.repo.UserSubdomainUsers(userSubKeys(userIDs)); err != nil {
		return nil, nil, nil, fmt.Errorf("users: %w", err)
	}
	return facts, nodes, effective, nil
}

// userSubReserve (round 3) gives every non-admin user who uses a node with
// the toggle on, and has no name there yet, his name now — database only
// (no Cloudflare call, except the cached zone list once after a restart).
// Called from the kicker right after a mutation and from every pass.
// Returns how many names were reserved.
func (h *Handler) userSubReserve(ctx context.Context) int {
	if h == nil || h.repo == nil {
		return 0
	}
	if on, err := h.repo.AnyUserSubdomainToggle(); err != nil || !on {
		return 0
	}
	m := h.userSubState()
	facts, nodes, effective, err := h.userSubFacts(m.now(), false)
	if err != nil || len(effective) == 0 {
		return 0
	}
	plan := usersub.Compute(facts)
	if len(plan.Creates) == 0 {
		return 0
	}
	touched := map[int64]bool{}
	h.userSubReserveCreates(ctx, nodes, plan.Creates, touched)
	for uid := range touched {
		h.subState().cache.purgeUser(uid)
	}
	return len(touched)
}

// userSubReserveCreates stores the planned names (one per user / node,
// re-checked under the reservation lock). Returns node id → names still
// waiting because the zone name is not known yet.
func (h *Handler) userSubReserveCreates(ctx context.Context, nodes map[int64]repo.UserSubdomainNode, creates []usersub.Create, touched map[int64]bool) map[int64]int {
	waiting := map[int64]int{}
	if len(creates) == 0 {
		return waiting
	}
	m := h.userSubState()
	m.reserve.Lock()
	defer m.reserve.Unlock()
	for _, c := range creates {
		if ctx.Err() != nil {
			waiting[c.NodeID]++
			continue
		}
		node := nodes[c.NodeID]
		zoneName := h.userSubZoneName(ctx, node)
		if zoneName == "" {
			waiting[c.NodeID]++
			continue
		}
		if taken, err := h.repo.UserSubdomainPairTaken(c.UserID, c.NodeID); err != nil || taken {
			continue
		}
		if err := h.userSubCreateName(c, node, zoneName); err != nil {
			log.Printf("[user-subdomain] new name user %d node %d: %v", c.UserID, c.NodeID, err)
			continue
		}
		touched[c.UserID] = true
	}
	return waiting
}

// userSubZoneName: the name of the zone a node's main record lives in —
// from the last zone check, an existing name in that zone, or the cached
// zone list (one short Cloudflare call when nothing is cached).
func (h *Handler) userSubZoneName(ctx context.Context, node repo.UserSubdomainNode) string {
	if node.Main.ZoneID == "" || node.ProviderID <= 0 {
		return ""
	}
	m := h.userSubState()
	key := userSubZoneKey(node.ProviderID, node.Main.ZoneID)
	m.mu.Lock()
	z := m.zones[key]
	name := ""
	if z != nil {
		name = z.Name
	}
	m.mu.Unlock()
	if name != "" {
		return name
	}
	if zn, err := h.repo.UserSubdomainZoneName(node.ProviderID, node.Main.ZoneID); err == nil && zn != "" {
		return zn
	}
	provider, err := h.repo.GetDNSProvider(node.ProviderID)
	if err != nil || provider == nil {
		return ""
	}
	zctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	zones, err := h.ddnsZones(zctx, h.ddnsClient(provider), node.ProviderID)
	if err != nil {
		return ""
	}
	for _, zz := range zones {
		if zz.ID == node.Main.ZoneID && strings.HasSuffix(node.Main.Domain, "."+strings.ToLower(zz.Name)) {
			return strings.ToLower(zz.Name)
		}
	}
	return ""
}

// userSubCreateName stores a fresh random name for (user, node) in the
// node main domain's zone. The record is created by the CF pass right after.
func (h *Handler) userSubCreateName(c usersub.Create, node repo.UserSubdomainNode, zoneName string) error {
	m := h.userSubState()
	key := userSubZoneKey(node.ProviderID, node.Main.ZoneID)
	m.mu.Lock()
	var listed map[string][]ddns.Record
	if z := m.zones[key]; z != nil {
		listed = z.byName
	}
	m.mu.Unlock()
	for attempt := 0; attempt < 8; attempt++ {
		label := usersub.NewLabel()
		domain := label + "." + zoneName
		if taken, err := h.repo.UserSubdomainLabelTaken(label); err != nil {
			return err
		} else if taken || len(listed[domain]) > 0 {
			continue
		}
		row := &model.UserSubdomain{UserID: c.UserID, NodeID: node.ID, OriginNodeID: node.ID, Label: label, Domain: domain,
			ProviderID: node.ProviderID, ZoneID: node.Main.ZoneID, State: c.State, Reason: c.Reason}
		if err := h.repo.CreateUserSubdomain(row); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				continue
			}
			return err
		}
		log.Printf("[user-subdomain] %s reserved for user %d on node %d (%s)", domain, c.UserID, node.ID, c.State)
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
	var nameServers []string
	for _, zz := range zones {
		if zz.ID == zoneID {
			z.Name = strings.ToLower(zz.Name)
			nameServers = zz.NameServers
		}
	}
	if z.Name == "" {
		z.Error = "该 DNS 服务商的 Token 看不到这个区域"
		return
	}
	z.authIPs = userSubAuthIPs(ctx, nameServers)
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

// userSubAuthIPs resolves the zone's name servers (their own names exist:
// safe to look up) to one IPv4 each.
func userSubAuthIPs(ctx context.Context, servers []string) []string {
	out := []string{}
	for _, ns := range servers {
		lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ips, err := net.DefaultResolver.LookupIPAddr(lctx, ns)
		cancel()
		if err != nil {
			continue
		}
		for _, ip := range ips {
			if ip.IP.To4() != nil {
				out = append(out, net.JoinHostPort(ip.IP.String(), "53"))
				break
			}
		}
	}
	return out
}

func userSubErrText(err error) string {
	if ddns.IsAuthError(err) {
		return "Cloudflare 拒绝了 API Token（可能已被撤销或更换）：" + err.Error()
	}
	return err.Error()
}

// userSubDesired: the CNAME target a name should have now ("" = no record).
func (h *Handler) userSubDesired(pass *userSubPass, row model.UserSubdomain) string {
	if row.State != model.UserSubdomainActive {
		return ""
	}
	if n, ok := pass.nodes[row.NodeID]; ok && n.Enabled() && n.Main.Synced {
		return n.Main.Domain
	}
	return ""
}

// userSubSyncRow brings one name's Cloudflare record in line with its state.
func (h *Handler) userSubSyncRow(ctx context.Context, pass *userSubPass, row model.UserSubdomain, full bool) {
	m := h.userSubState()
	now := m.now()
	desired := h.userSubDesired(pass, row)
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
		h.userSubFail(pass, row, ddnsConfigErr("DNS 服务商不存在或已删除"), now)
		return
	}
	client := h.ddnsClient(provider)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	wasLive := row.Live()
	if desired == "" {
		if row.RecordID != "" {
			if err := client.DeleteUserRecord(ctx, row.ZoneID, row.RecordID, row.Domain); err != nil && !errors.Is(err, ddns.ErrNotUserRecord) {
				h.userSubFail(pass, row, err, now)
				return
			}
		}
		if listing != nil {
			for _, rec := range listing.byName[strings.ToLower(row.Domain)] {
				if rec.Type == "CNAME" && ddns.IsUserRecord(rec) && rec.ID != row.RecordID {
					if err := client.DeleteUserRecord(ctx, row.ZoneID, rec.ID, row.Domain); err != nil && !errors.Is(err, ddns.ErrNotUserRecord) {
						h.userSubFail(pass, row, err, now)
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
		if wasLive || usersub.Hidden(row) {
			pass.touched[row.UserID] = true
		}
		return
	}
	recordID := row.RecordID
	if recordID != "" {
		if _, err := client.UpdateUserCNAME(ctx, row.ZoneID, recordID, row.Domain, desired); err != nil {
			if !ddns.IsNotFound(err) {
				h.userSubFail(pass, row, err, now)
				return
			}
			recordID = "" // removed in the dashboard: create it again
		}
	}
	created := false
	if recordID == "" {
		existing, err := client.ListRecords(ctx, row.ZoneID, row.Domain, "")
		if err != nil {
			h.userSubFail(pass, row, err, now)
			return
		}
		for _, rec := range existing {
			if rec.Type == "CNAME" && ddns.IsUserRecord(rec) {
				recordID = rec.ID // ours (left over): adopt
				continue
			}
			h.userSubFail(pass, row, ddnsConfigErr("Cloudflare 里已有同名 %s 记录（不是面板创建的），请「换新名」", rec.Type), now)
			return
		}
		if recordID != "" {
			if _, err := client.UpdateUserCNAME(ctx, row.ZoneID, recordID, row.Domain, desired); err != nil {
				h.userSubFail(pass, row, err, now)
				return
			}
		} else {
			rec, err := client.CreateUserCNAME(ctx, row.ZoneID, row.Domain, desired)
			if err != nil {
				if ddns.IsQuotaError(err) {
					// The zone is fuller than the last listing said: no more
					// creates in it this pass.
					m.mu.Lock()
					if z := m.zones[zoneKey]; z != nil && z.Quota > 0 {
						z.Used = z.Quota
					}
					m.mu.Unlock()
					h.userSubMarkCapped(pass, row, "Cloudflare 记录数已到上限："+err.Error())
					return
				}
				h.userSubFail(pass, row, err, now)
				return
			}
			recordID, created = rec.ID, true
			m.mu.Lock()
			if z := m.zones[zoneKey]; z != nil {
				z.Used++ // until the next listing
			}
			m.mu.Unlock()
		}
	}
	// Round 3: ready only once Cloudflare returns the record by id with the
	// right type, name and content (never a public resolver: a lookup before
	// the record exists would cache NXDOMAIN for 30 min).
	if err := userSubVerify(ctx, client, row.ZoneID, recordID, row.Domain, desired); err != nil {
		if created {
			_ = h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{"record_id": recordID})
			row.RecordID = recordID
		}
		h.userSubFail(pass, row, err, now)
		return
	}
	// A fresh name stays 「正在就绪」 (ready_at 0) until the zone's
	// authoritative servers answer it (userSubConfirm, end of the pass).
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

// userSubConfirmWait: how long a fresh name waits for the authoritative
// servers before it counts as ready anyway (the API already confirmed it).
const userSubConfirmWait = 15 * time.Second

// userSubConfirm (round 3): fresh names whose record exists (API-verified)
// become ready once the zone's authoritative servers answer them — measured
// ~3 s after the API on imgamer.top (11-24). A client resolving in between
// would get NXDOMAIN cached for 30 min; the tag 「正在就绪」 covers that
// window. Only the zone's own servers are asked (nothing is cached there),
// never a public resolver.
func (h *Handler) userSubConfirm(ctx context.Context, pass *userSubPass) {
	rows, err := h.repo.ListUserSubdomainsForPlan()
	if err != nil {
		return
	}
	left := []model.UserSubdomain{}
	for _, row := range rows {
		if row.State == model.UserSubdomainActive && row.Live() && row.ReadyAt == 0 {
			left = append(left, row)
		}
	}
	if len(left) == 0 {
		return
	}
	m := h.userSubState()
	start := m.now()
	ready := func(row model.UserSubdomain, how string) {
		_ = h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{"ready_at": m.now().UnixMilli()})
		pass.touched[row.UserID] = true
		log.Printf("[user-subdomain] %s ready (%s, %v after the pass)", row.Domain, how, m.now().Sub(start).Round(100*time.Millisecond))
	}
	for len(left) > 0 && ctx.Err() == nil {
		next := left[:0]
		for _, row := range left {
			switch ok, checked := h.userSubAnswered(ctx, row); {
			case ok:
				ready(row, "authoritative DNS answers")
			case !checked:
				ready(row, "API-verified; no authoritative server to ask")
			default:
				next = append(next, row)
			}
		}
		left = next
		if len(left) == 0 {
			return
		}
		if m.now().Sub(start) >= userSubConfirmWait {
			for _, row := range left {
				ready(row, "API-verified; authoritative DNS still silent after 15 s")
			}
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(400 * time.Millisecond):
		}
	}
}

// userSubAnswered: every authoritative server of the name's zone serves it.
// checked=false: no server known (zone not listed, or no name servers).
func (h *Handler) userSubAnswered(ctx context.Context, row model.UserSubdomain) (ok, checked bool) {
	m := h.userSubState()
	m.mu.Lock()
	check := m.dnsCheck
	var servers []string
	if z := m.zones[userSubZoneKey(row.ProviderID, row.ZoneID)]; z != nil {
		servers = append(servers, z.authIPs...)
	}
	m.mu.Unlock()
	if check != nil {
		return check(row.Domain), true
	}
	if len(servers) == 0 {
		return false, false
	}
	for _, server := range servers {
		has, err := ddns.AuthoritativeHas(ctx, server, row.Domain)
		if err != nil || !has {
			return false, true
		}
	}
	return true, true
}

// userSubVerify reads the record back through the API.
func userSubVerify(ctx context.Context, client *ddns.Client, zoneID, recordID, name, target string) error {
	cur, err := client.GetRecord(ctx, zoneID, recordID)
	if err != nil {
		return fmt.Errorf("记录已提交，读回校验失败：%w", err)
	}
	if cur.Type != "CNAME" || !strings.EqualFold(strings.TrimSuffix(cur.Name, "."), name) ||
		!strings.EqualFold(strings.TrimSuffix(cur.Content, "."), target) {
		return fmt.Errorf("读回的记录不对（%s %s → %s）", cur.Type, cur.Name, cur.Content)
	}
	return nil
}

// userSubFreshBackoff: a fresh name (the user sees 「正在就绪」) is retried
// quickly, then like any other record.
func userSubFreshBackoff(fails int) time.Duration {
	switch fails {
	case 1:
		return 5 * time.Second
	case 2:
		return 15 * time.Second
	case 3:
		return 30 * time.Second
	}
	return ddns.Backoff(fails - 2)
}

func (h *Handler) userSubFail(pass *userSubPass, row model.UserSubdomain, err error, now time.Time) {
	fails := row.FailCount + 1
	delay := ddns.Backoff(fails)
	if row.ReadyAt == 0 {
		delay = userSubFreshBackoff(fails)
	}
	var cfg *ddnsConfigError
	if errors.As(err, &cfg) || ddns.IsAuthError(err) {
		delay = time.Hour
		if fails < usersub.FailShow {
			fails = usersub.FailShow // no point in 「正在就绪」 for an error that needs the admin
		}
	}
	msg := userSubErrText(err)
	if ddns.IsQuotaError(err) {
		msg = "Cloudflare 记录数已到上限：" + err.Error()
	}
	_ = h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{
		"status": model.UserSubdomainSyncFailed, "last_error": msg, "fail_count": fails,
		"next_retry_at": now.Add(delay).UnixMilli(),
	})
	after := row
	after.Status, after.FailCount = model.UserSubdomainSyncFailed, fails
	if pass != nil && usersub.Hidden(after) != usersub.Hidden(row) {
		pass.touched[row.UserID] = true
	}
	if fails == 1 || fails == usersub.FailShow || fails%10 == 0 {
		log.Printf("[user-subdomain] %s sync failed (%d): %s", row.Domain, fails, msg)
	}
}

// userSubMarkCapped: the name waits for room in its zone (no CF call).
func (h *Handler) userSubMarkCapped(pass *userSubPass, row model.UserSubdomain, msg string) {
	if row.Status == model.UserSubdomainSyncCapped && row.LastError == msg {
		return
	}
	_ = h.repo.UpdateUserSubdomain(row.ID, map[string]interface{}{
		"status": model.UserSubdomainSyncCapped, "last_error": msg, "fail_count": 0, "next_retry_at": int64(0),
	})
	after := row
	after.Status = model.UserSubdomainSyncCapped
	if pass != nil && usersub.Hidden(after) != usersub.Hidden(row) {
		pass.touched[row.UserID] = true
	}
	if row.Status != model.UserSubdomainSyncCapped {
		log.Printf("[user-subdomain] %s waits for room in the zone: %s", row.Domain, msg)
	}
}

// userSubRewriteForwardItems: the forward list's inIp of every rule shows
// its owner's name for that entry (one query when nobody has one). Round 3:
// entries whose name cannot be handed out yet are dropped from inIp and
// listed in entryNotices (users: why; admins: + the name and the error).
func (h *Handler) userSubRewriteForwardItems(items []map[string]interface{}, entryNodes map[int64][]int64, admin bool) {
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
		list, _ := item["inIp"].(string)
		id := asInt64(item["id"], 0)
		uid := asInt64(item["userId"], 0)
		rewritten, tags, notices := names.RewriteEntryList(uid, entryNodes[id], list)
		if list != "" {
			item["inIp"] = rewritten
		}
		// The target of a forward to a native landing on such a node too.
		if target, ok := item["remoteAddr"].(string); ok && target != "" {
			item["remoteAddr"], _, _ = names.RewriteEntryList(uid, nil, target)
		}
		if tags := userSubVisibleTags(tags, admin); len(tags) > 0 {
			// 「正在就绪」 / 「已过期 · 记录已删除」 etc. next to the address.
			item["entryNameTags"] = tags
		}
		if len(notices) > 0 {
			item["entryNotices"] = userSubVisibleNotices(notices, admin)
		}
	}
}

// userSubVisibleTags: the tags next to a handed-out name (round 3: users
// see 「正在就绪」 too — the address is final, usable in a few seconds).
func userSubVisibleTags(tags map[string]string, admin bool) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := map[string]string{}
	for host, tag := range tags {
		switch tag {
		case repo.UserEntryTagExpired, repo.UserEntryTagDisabled, repo.UserEntryTagDeleted, repo.UserEntryTagPending,
			repo.UserEntryTagCapped, repo.UserEntryTagFailed:
			out[host] = tag
		}
	}
	return out
}

// userSubVisibleNotices: withheld entries; users never get the withheld
// name or the Cloudflare error text.
func userSubVisibleNotices(notices []repo.UserEntryNotice, admin bool) []repo.UserEntryNotice {
	out := make([]repo.UserEntryNotice, 0, len(notices))
	for _, n := range notices {
		if !admin {
			n.Domain, n.Reason = "", ""
		}
		out = append(out, n)
	}
	return out
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
	// Guard (fork.169): names replace the main domain only, so on a node with
	// several DDNS domains (e.g. 电信 + 联通) the other domains would still be
	// handed out to users. Refused until per-record names exist (plan 305).
	domains := map[string]bool{}
	for _, rec := range plan.records {
		if d := strings.ToLower(strings.TrimSpace(rec.Domain)); d != "" {
			domains[d] = true
		}
	}
	if len(domains) > 1 {
		return fmt.Errorf("这台节点有 %d 个 DDNS 域名：「用户专属域名」目前只支持只有一个 DDNS 域名的节点", len(domains))
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
	Hidden     int    `json:"hidden"` // round 3: fresh names not handed out (capped / creating keeps failing)
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
		v.Active, v.Paused, v.Failed, v.Pending, v.Capped, v.Hidden = c.Active, c.Paused, c.Failed, c.Pending, c.Capped, c.Hidden
	}
	for id, w := range m.nodeWarn {
		if v := out[id]; v != nil {
			v.Waiting = w.Waiting
		}
	}
	for _, v := range out {
		v.Attention = v.Failed > 0 || v.Capped > 0 || v.Hidden > 0 || v.Waiting > 0 || v.Wildcard || v.ZoneError != ""
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
	// Round 3: ReadyAt = first verified in Cloudflare (0 = 正在就绪);
	// Hidden = withheld from the user (no address: capped / keeps failing).
	ReadyAt   int64 `json:"readyAt"`
	FailCount int   `json:"failCount"`
	Hidden    bool  `json:"hidden"`
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
			ReadyAt: row.ReadyAt, FailCount: row.FailCount, Hidden: usersub.Hidden(row),
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
			if row.State == model.UserSubdomainPaused && (row.Reason == model.UserSubdomainReasonExpired || row.Reason == model.UserSubdomainReasonDisabled) {
				counts["paused_"+row.Reason]++ // round 2 header: 已过期 N · 已停用 M
			}
			if row.Status == model.UserSubdomainSyncFailed {
				counts["failed"]++
			}
			if row.Status == model.UserSubdomainSyncCapped {
				counts["capped"]++
			}
			if v.Hidden {
				counts["hidden"]++
			}
			if row.State == model.UserSubdomainActive && (!row.Live() || row.ReadyAt == 0) && !v.Hidden {
				counts["readying"]++ // 正在就绪
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
		m.mu.Unlock()
		if zone != nil {
			if byZone, err := h.repo.UserSubdomainZoneRecordCounts(); err == nil {
				zone.UserRecords = byZone[repo.UserSubdomainZone{ProviderID: n.ProviderID, ZoneID: n.Main.ZoneID}]
			}
		}
		m.mu.Lock()
		warn := m.nodeWarn[nodeID]
		m.mu.Unlock()
		capped := counts["capped"]
		out["node"] = map[string]interface{}{
			"id": nodeID, "name": nodeNames[nodeID], "toggle": n.Toggle, "ddnsEnabled": n.DDNSEnabled,
			"mainDomain": n.Main.Domain, "mainIps": n.Main.IPs, "mainSynced": n.Main.Synced, "v6Only": n.Main.V6Only,
			"effective": n.Enabled() && n.Main.Domain != "" && n.Main.Synced,
		}
		out["zone"] = zone
		out["capped"], out["waiting"] = capped, warn.Waiting
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
// 重新分配. Round 2: his links keep the dead name (tag deleted) — they do not
// fall back to the node main domain, so 删除 really drops him.
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
	zoneCounts := map[string]int{}
	if byZone, err := h.repo.UserSubdomainZoneRecordCounts(); err == nil {
		for k, n := range byZone {
			zoneCounts[userSubZoneKey(k.ProviderID, k.ZoneID)] = n
		}
	}
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
					cp.UserRecords = zoneCounts[key] // the panel's own count, always current
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
