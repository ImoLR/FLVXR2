package handler

// Plan 273 利群自己的流量: one background poller reads, for every configured
// 利群 account, the vendor's billing numbers (current cycle usage incl.
// per-forward bytes, the last closed cycle, the device list and the service
// summary with expires_at) and keeps the last good snapshot in memory and in
// leikwan_usage_snapshot (a restart shows numbers at once).
//
// Request budget per account: a round every 2.5 min does at most 2 due reads,
// 15 s apart. usage + devices are due every 5 min, service every 30 min,
// previous once a day (or right after the cycle rolls over) → about 26 reads
// an hour (vendor limit 120 / min, shared with the panel's other calls, which
// use the same per-token limiter in leikwan.Client).
// Back-off: 401/403 → 30 min, 429 → the vendor's Retry-After (≥ 5 min), both
// for every read of the account; other errors → 5 / 10 / 30 min.
//
// Visibility: the 套餐流量 card, devices and account totals are admin only
// (/api/v1/leikwan/usage). Rule rows carry vendorFlow = the forward's own
// bytes; users get it only on their own rules (leikwan-rule/list already
// returns only those), without share / extra / error text.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/leikwan"
	"go-backend/internal/store/model"
)

const (
	lkUsageRoundEvery = 150 * time.Second
	lkUsageGap        = 15 * time.Second
	lkUsageEvery      = 5 * time.Minute
	lkServiceEvery    = 30 * time.Minute
	lkPreviousEvery   = 24 * time.Hour
	lkUsageLongWait   = 30 * time.Minute
	lkUsagePerRound   = 2
	lkUsageSlack      = 10 * time.Second
)

// Kinds in priority order (a round takes the first two due).
var lkUsageKinds = []string{"usage", "devices", "service", "previous"}

func lkUsageInterval(kind string) time.Duration {
	switch kind {
	case "service":
		return lkServiceEvery
	case "previous":
		return lkPreviousEvery
	}
	return lkUsageEvery
}

// lkUsageStaleAfter: a snapshot older than this shows 「数据旧了」.
func lkUsageStaleAfter(kind string) time.Duration {
	switch kind {
	case "service":
		return 2 * time.Hour
	case "previous":
		return 48 * time.Hour
	}
	return 15 * time.Minute
}

type lkUsageState struct {
	mu     sync.Mutex
	loaded bool
	snaps  map[string]*model.LeiKwanUsageSnapshot
	parsed map[int64]*leikwan.ServiceUsage // account → current usage (decoded once)
	pollMu sync.Mutex                      // one round at a time
}

func lkUsageKey(accountID int64, kind string) string { return fmt.Sprintf("%d:%s", accountID, kind) }

// lkUsageEnsure loads the persisted snapshots once (retried on a DB error).
// Caller holds st.mu.
func (h *Handler) lkUsageEnsure() {
	st := &h.lkUsage
	if st.loaded {
		return
	}
	if st.snaps == nil {
		st.snaps = map[string]*model.LeiKwanUsageSnapshot{}
		st.parsed = map[int64]*leikwan.ServiceUsage{}
	}
	rows, err := h.repo.ListLeiKwanUsageSnapshots()
	if err != nil {
		return
	}
	for i := range rows {
		s := rows[i]
		st.snaps[lkUsageKey(s.AccountID, s.Kind)] = &s
	}
	st.loaded = true
}

// lkUsageSnap returns a copy of the snapshot of (account, kind) that still
// belongs to the account's service (a changed service id starts over).
func (h *Handler) lkUsageSnap(a *model.LeiKwanAccount, kind string) (model.LeiKwanUsageSnapshot, bool) {
	st := &h.lkUsage
	st.mu.Lock()
	defer st.mu.Unlock()
	h.lkUsageEnsure()
	s := st.snaps[lkUsageKey(a.ID, kind)]
	if s == nil || kind != "devices" && s.ServiceID != a.ServiceID {
		return model.LeiKwanUsageSnapshot{}, false
	}
	return *s, true
}

// lkCurrentUsage: the decoded current-cycle report of the account (nil until
// the first good read).
func (h *Handler) lkCurrentUsage(a *model.LeiKwanAccount) (*leikwan.ServiceUsage, model.LeiKwanUsageSnapshot) {
	s, ok := h.lkUsageSnap(a, "usage")
	if !ok || s.Data == "" {
		return nil, s
	}
	st := &h.lkUsage
	st.mu.Lock()
	defer st.mu.Unlock()
	if u := st.parsed[a.ID]; u != nil && u.ServiceID == a.ServiceID {
		return u, s
	}
	var u leikwan.ServiceUsage
	if json.Unmarshal([]byte(s.Data), &u) != nil {
		return nil, s
	}
	if u.ServiceID == 0 {
		u.ServiceID = a.ServiceID
	}
	st.parsed[a.ID] = &u
	return &u, s
}

// lkPrevious is the stored form of the previous-cycle read.
type lkPrevious struct {
	None     bool                  `json:"none,omitempty"`
	ForStart string                `json:"forStart"` // current cycle start when read
	Report   *leikwan.ServiceUsage `json:"report,omitempty"`
}

// lkUsageDue lists the account's reads due at now, in priority order.
func (h *Handler) lkUsageDue(a *model.LeiKwanAccount, now time.Time) []string {
	out := []string{}
	cur, _ := h.lkCurrentUsage(a)
	for _, kind := range lkUsageKinds {
		s, ok := h.lkUsageSnap(a, kind)
		if !ok {
			out = append(out, kind)
			continue
		}
		due := time.UnixMilli(s.NextAt).Before(now.Add(lkUsageSlack))
		if kind == "previous" && !due && s.ErrorCount == 0 && cur != nil {
			// The cycle rolled over since the last read: read the closed one now.
			var p lkPrevious
			if json.Unmarshal([]byte(s.Data), &p) == nil && p.ForStart != cur.Period.CycleStartedAt {
				due = true
			}
		}
		if due {
			out = append(out, kind)
		}
	}
	return out
}

// lkUsageFetch performs one read; data = the JSON to store.
func (h *Handler) lkUsageFetch(ctx context.Context, a *model.LeiKwanAccount, kind string) (string, error) {
	var v interface{}
	switch kind {
	case "usage":
		u, err := h.leikwan.ServiceUsage(ctx, a.Token, a.ServiceID, false)
		if err != nil {
			return "", err
		}
		v = u
	case "previous":
		p := lkPrevious{}
		if cur, _ := h.lkCurrentUsage(a); cur != nil {
			p.ForStart = cur.Period.CycleStartedAt
		}
		u, err := h.leikwan.ServiceUsage(ctx, a.Token, a.ServiceID, true)
		var apiErr *leikwan.APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Code == "usage_period_not_found" {
			p.None = true
		} else if err != nil {
			return "", err
		} else {
			u.Forwards, u.Daily = nil, nil // the card shows the totals only
			p.Report = &u
		}
		v = p
	case "devices":
		d, err := h.leikwan.Devices(ctx, a.Token)
		if err != nil {
			return "", err
		}
		v = d
	case "service":
		list, err := h.leikwan.Services(ctx, a.Token)
		if err != nil {
			return "", err
		}
		var found *leikwan.ServiceSummary
		for i := range list {
			if list[i].ID == a.ServiceID {
				found = &list[i]
			}
		}
		if found == nil {
			return "", errors.New("利群账号里没有这个服务")
		}
		v = found
	default:
		return "", errors.New("unknown kind")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// lkUsageErrText: short admin text (never vendor message bodies or tokens).
func lkUsageErrText(err error) string {
	var apiErr *leikwan.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Status == 401:
			return "API Token 无效或账号停用 (401)"
		case apiErr.Status == 403:
			return "利群拒绝读取 (403)"
		case apiErr.Status == 429:
			return "利群限速，稍后再读 (429)"
		case apiErr.Status >= 500:
			return fmt.Sprintf("利群服务出错 (%d)", apiErr.Status)
		}
		return apiErr.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "利群响应超时"
	}
	t := err.Error()
	if len(t) > 190 {
		t = t[:190]
	}
	return t
}

// lkUsageBackoff: when to try again after the n-th error in a row; account
// = true when every read of the account must wait (token / rate limit).
func lkUsageBackoff(err error, n int) (time.Duration, bool) {
	var apiErr *leikwan.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 401, 403:
			return lkUsageLongWait, true
		case 429:
			d := apiErr.RetryAfter
			if d < lkUsageEvery {
				d = lkUsageEvery
			}
			if d > time.Hour {
				d = time.Hour
			}
			return d, true
		}
	}
	if n >= 3 {
		return lkUsageLongWait, false
	}
	return lkUsageEvery << (n - 1), false
}

// lkUsageRecord stores one read's result in memory and in the DB.
func (h *Handler) lkUsageRecord(a *model.LeiKwanAccount, kind string, roundStart, now time.Time, data string, err error) {
	st := &h.lkUsage
	st.mu.Lock()
	h.lkUsageEnsure()
	key := lkUsageKey(a.ID, kind)
	s := st.snaps[key]
	if s == nil {
		s = &model.LeiKwanUsageSnapshot{AccountID: a.ID, Kind: kind}
		st.snaps[key] = s
	}
	if kind != "devices" && s.ServiceID != a.ServiceID {
		s.Data, s.FetchedAt = "", 0
	}
	s.ServiceID = a.ServiceID
	s.CheckedAt = now.UnixMilli()
	bumps := []*model.LeiKwanUsageSnapshot{}
	if err == nil {
		s.Data, s.FetchedAt, s.Error, s.ErrorCount = data, now.UnixMilli(), "", 0
		s.NextAt = roundStart.Add(lkUsageInterval(kind)).UnixMilli()
		if kind == "usage" {
			delete(st.parsed, a.ID)
		}
	} else {
		s.ErrorCount++
		s.Error = lkUsageErrText(err)
		wait, account := lkUsageBackoff(err, s.ErrorCount)
		s.NextAt = roundStart.Add(wait).UnixMilli()
		if account {
			for _, k := range lkUsageKinds {
				o := st.snaps[lkUsageKey(a.ID, k)]
				if o == nil {
					// Never read yet: a placeholder row carries the wait.
					o = &model.LeiKwanUsageSnapshot{AccountID: a.ID, Kind: k, ServiceID: a.ServiceID}
					st.snaps[lkUsageKey(a.ID, k)] = o
				}
				if o != s && o.NextAt < s.NextAt {
					o.NextAt = s.NextAt
					bumps = append(bumps, o)
				}
			}
		}
		if s.ErrorCount == 1 || s.ErrorCount%12 == 0 {
			log.Printf("利群用量：账号 %d 读 %s 失败（第 %d 次）：%s，%s 后再试", a.ID, kind, s.ErrorCount, s.Error, wait.Round(time.Second))
		}
	}
	rows := append([]model.LeiKwanUsageSnapshot{*s}, func() []model.LeiKwanUsageSnapshot {
		out := make([]model.LeiKwanUsageSnapshot, 0, len(bumps))
		for _, b := range bumps {
			out = append(out, *b)
		}
		return out
	}()...)
	st.mu.Unlock()
	for i := range rows {
		row := rows[i]
		if e := h.repo.SaveLeiKwanUsageSnapshot(&row); e != nil {
			log.Printf("利群用量：保存快照失败：%v", e)
			continue
		}
		st.mu.Lock()
		if cur := st.snaps[lkUsageKey(row.AccountID, row.Kind)]; cur != nil && cur.ID == 0 {
			cur.ID = row.ID
		}
		st.mu.Unlock()
	}
}

// LeiKwanUsageRound runs one poll round at now: per account at most two due
// reads, gap apart (the loop uses 15 s; tests pass 0). Returns the number of
// vendor reads made.
func (h *Handler) LeiKwanUsageRound(ctx context.Context, now time.Time, gap time.Duration) int {
	if h == nil || h.repo == nil || h.leikwan == nil {
		return 0
	}
	st := &h.lkUsage
	st.pollMu.Lock()
	defer st.pollMu.Unlock()
	accounts, err := h.repo.ListLeiKwanAccounts()
	if err != nil || len(accounts) == 0 {
		return 0
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	reads := 0
	for i := range accounts {
		a := &accounts[i]
		if a.Token == "" || a.ServiceID <= 0 {
			continue
		}
		due := h.lkUsageDue(a, now)
		if len(due) > lkUsagePerRound {
			due = due[:lkUsagePerRound]
		}
		for _, kind := range due {
			if reads > 0 && gap > 0 {
				t := time.NewTimer(gap)
				select {
				case <-ctx.Done():
					t.Stop()
					return reads
				case <-t.C:
				}
			}
			data, err := h.lkUsageFetch(ctx, a, kind)
			reads++
			h.lkUsageRecord(a, kind, now, time.Now(), data, err)
			if _, account := lkUsageBackoff(err, 1); err != nil && account {
				break // token / rate limit: the account waits, no second read
			}
		}
	}
	return reads
}

func (h *Handler) runLeiKwanUsageLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		timer.Reset(lkUsageRoundEvery)
		h.LeiKwanUsageRound(ctx, time.Now(), lkUsageGap)
	}
}

// lkUsageResetAccount: after the admin saves the account (new token / service)
// every read is due again and the error back-off ends.
func (h *Handler) lkUsageResetAccount(accountID int64) {
	st := &h.lkUsage
	st.mu.Lock()
	h.lkUsageEnsure()
	rows := []model.LeiKwanUsageSnapshot{}
	for _, k := range lkUsageKinds {
		if s := st.snaps[lkUsageKey(accountID, k)]; s != nil && (s.NextAt != 0 || s.ErrorCount != 0) {
			s.NextAt, s.ErrorCount = 0, 0
			rows = append(rows, *s)
		}
	}
	delete(st.parsed, accountID)
	st.mu.Unlock()
	for i := range rows {
		_ = h.repo.SaveLeiKwanUsageSnapshot(&rows[i])
	}
}

// lkUsageDropAccount forgets a deleted account's snapshots (the DB rows go
// with the account).
func (h *Handler) lkUsageDropAccount(accountID int64) {
	st := &h.lkUsage
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, k := range lkUsageKinds {
		delete(st.snaps, lkUsageKey(accountID, k))
	}
	delete(st.parsed, accountID)
}

// ---- views ----

// lkVendorMs: a vendor UTC time string as unix ms (nil when empty / unknown).
func lkVendorMs(s *string) interface{} {
	if s == nil || *s == "" {
		return nil
	}
	t := leikwan.ParseTime(*s)
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func lkStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func lkUsageStatusView(s model.LeiKwanUsageSnapshot, ok bool, kind string, now time.Time) map[string]interface{} {
	if !ok {
		return map[string]interface{}{"fetchedAt": 0, "checkedAt": 0, "nextAt": 0, "stale": true, "pending": true}
	}
	// stale = old data or the last read failed (the numbers shown are the
	// last good ones); error says why (admin only).
	stale := s.FetchedAt == 0 || s.Error != "" || now.Sub(time.UnixMilli(s.FetchedAt)) > lkUsageStaleAfter(kind)
	v := map[string]interface{}{"fetchedAt": s.FetchedAt, "checkedAt": s.CheckedAt, "nextAt": s.NextAt, "stale": stale, "pending": s.FetchedAt == 0 && s.Error == ""}
	if s.Error != "" {
		v["error"], v["errorCount"] = s.Error, s.ErrorCount
	}
	return v
}

// lkRuleVendorFlow: vendorFlow of one rule (nil without a report row).
func lkRuleVendorFlow(u *leikwan.ServiceUsage, s model.LeiKwanUsageSnapshot, forwardID int64, admin bool, now time.Time) map[string]interface{} {
	if u == nil {
		return nil
	}
	for _, f := range u.Forwards {
		if f.ForwardID != forwardID {
			continue
		}
		start, end := u.Period.CycleStartedAt, u.Period.CycleEndsAt
		v := map[string]interface{}{
			"total": f.TotalBytes, "in": f.BytesIn, "out": f.BytesOut,
			"cycleStart": lkVendorMs(&start), "cycleEnd": lkVendorMs(&end),
			"updatedAt": s.FetchedAt, "stale": s.Error != "" || now.Sub(time.UnixMilli(s.FetchedAt)) > lkUsageStaleAfter("usage"),
		}
		if admin {
			v["extra"], v["share"], v["state"] = f.ExtraBilledBytes, f.SharePercent, f.State
			if s.Error != "" {
				v["error"] = s.Error
			}
		}
		return v
	}
	return nil
}

// leiKwanVendorFlows: account id → (current report, snapshot) for the rule
// list (each account decoded once per request).
type lkVendorReport struct {
	usage *leikwan.ServiceUsage
	snap  model.LeiKwanUsageSnapshot
}

func (h *Handler) leiKwanVendorReports(rules []model.LeiKwanRule, settingAccount int64) map[int64]lkVendorReport {
	out := map[int64]lkVendorReport{}
	for _, x := range rules {
		id := x.AccountID
		if id == 0 {
			id = settingAccount
		}
		if _, done := out[id]; done || id == 0 {
			continue
		}
		a, err := h.repo.GetLeiKwanAccount(id)
		if err != nil || a == nil {
			out[id] = lkVendorReport{}
			continue
		}
		u, s := h.lkCurrentUsage(a)
		out[id] = lkVendorReport{usage: u, snap: s}
	}
	return out
}

// leikwanUsage: POST /api/v1/leikwan/usage {accountId?} (admin) — the
// 套餐流量 card, the per-forward table and the 利群在线设备 list from the
// last snapshots (never a vendor call).
func (h *Handler) leikwanUsage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID int64 `json:"accountId"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	var a *model.LeiKwanAccount
	var err error
	if req.AccountID > 0 {
		a, err = h.leiKwanAccount(req.AccountID)
	} else {
		_, a, err = h.leiKwanSettingAccount()
	}
	if a == nil {
		msg := "未配置利群账号"
		if err != nil {
			msg = err.Error()
		}
		response.WriteJSON(w, response.OK(map[string]interface{}{"accountReady": false, "note": msg}))
		return
	}
	response.WriteJSON(w, response.OK(h.leiKwanUsageView(a, time.Now())))
}

func (h *Handler) leiKwanUsageView(a *model.LeiKwanAccount, now time.Time) map[string]interface{} {
	out := map[string]interface{}{"accountReady": true, "accountId": a.ID, "accountName": a.Name, "serviceId": a.ServiceID,
		"pollEverySec": int(lkUsageEvery / time.Second)}

	// Service summary: plan name + 到期.
	if s, ok := h.lkUsageSnap(a, "service"); ok && s.Data != "" {
		var sv leikwan.ServiceSummary
		if json.Unmarshal([]byte(s.Data), &sv) == nil {
			out["service"] = map[string]interface{}{"name": sv.Name, "planName": sv.Plan.Name, "status": sv.Status,
				"expiresAt": lkVendorMs(sv.ExpiresAt), "expiresAtRaw": lkStr(sv.ExpiresAt), "forwardLimit": sv.ForwardLimit, "activeForwardCount": sv.ActiveForwardCount}
		}
		out["serviceStatus"] = lkUsageStatusView(s, true, "service", now)
	} else {
		out["serviceStatus"] = lkUsageStatusView(s, ok, "service", now)
	}

	// Current cycle.
	u, us := h.lkCurrentUsage(a)
	_, usOK := h.lkUsageSnap(a, "usage")
	out["usageStatus"] = lkUsageStatusView(us, usOK, "usage", now)
	if u != nil {
		rules, _ := h.repo.ListLeiKwanRules(0)
		byForward := map[int64]model.LeiKwanRule{}
		users := []int64{}
		for _, x := range rules {
			if x.AccountID == 0 || x.AccountID == a.ID {
				byForward[x.ForwardID] = x
				users = append(users, x.UserID)
			}
		}
		names := h.repo.LeiKwanUserNames(users)
		forwards := make([]map[string]interface{}, 0, len(u.Forwards))
		var other int64
		otherIDs := []int64{}
		for _, f := range u.Forwards {
			item := map[string]interface{}{"forwardId": f.ForwardID, "state": f.State, "listenPort": f.ListenPort, "routeLabel": f.Route.Label,
				"totalBytes": f.TotalBytes, "bytesIn": f.BytesIn, "bytesOut": f.BytesOut, "extraBilledBytes": f.ExtraBilledBytes, "sharePercent": f.SharePercent}
			if x, ok := byForward[f.ForwardID]; ok {
				item["ruleId"], item["ruleName"], item["userName"] = x.ID, x.Name, names[x.UserID]
			} else {
				other += f.TotalBytes
				otherIDs = append(otherIDs, f.ForwardID)
			}
			forwards = append(forwards, item)
		}
		daily := make([]map[string]interface{}, 0, len(u.Daily))
		for _, d := range u.Daily {
			daily = append(daily, map[string]interface{}{"date": d.Date, "totalBytes": d.TotalBytes})
		}
		p := u.Period
		out["usage"] = map[string]interface{}{
			"cycleStart": lkVendorMs(&p.CycleStartedAt), "cycleEnd": lkVendorMs(&p.CycleEndsAt), "countedSince": lkVendorMs(&p.CountedSince),
			"trafficResetAt": lkVendorMs(p.TrafficResetAt), "billingMode": u.Usage.BillingMode, "status": u.Status,
			"usedBytes": u.Usage.UsedBytes, "includedBytes": u.Usage.IncludedBytes, "remainingBytes": u.Usage.RemainingBytes,
			"usedPercent": u.Usage.UsedPercent, "billableBytes": u.Usage.BillableBytes, "lifetimeUsedBytes": u.Usage.LifetimeUsedBytes,
			"attributedBytes": u.AttributedBytes, "unattributedBytes": u.UnattributedBytes,
			"forwards": forwards, "otherBytes": other, "otherForwards": otherIDs, "daily": daily,
		}
	}

	// Last closed cycle.
	if s, ok := h.lkUsageSnap(a, "previous"); ok && s.Data != "" {
		var pv lkPrevious
		if json.Unmarshal([]byte(s.Data), &pv) == nil {
			if pv.None {
				out["previousNone"] = true
			} else if pv.Report != nil {
				p := pv.Report.Period
				out["previous"] = map[string]interface{}{"cycleStart": lkVendorMs(&p.CycleStartedAt), "cycleEnd": lkVendorMs(&p.CycleEndsAt),
					"usedBytes": pv.Report.Usage.UsedBytes, "includedBytes": pv.Report.Usage.IncludedBytes, "usedPercent": pv.Report.Usage.UsedPercent}
			}
		}
	}

	// Devices (read-only).
	ds, dsOK := h.lkUsageSnap(a, "devices")
	out["devicesStatus"] = lkUsageStatusView(ds, dsOK, "devices", now)
	if dsOK && ds.Data != "" {
		var dl leikwan.DeviceList
		if json.Unmarshal([]byte(ds.Data), &dl) == nil {
			out["devices"] = h.leiKwanDevicesView(dl)
		}
	}
	return out
}

func (h *Handler) leiKwanDevicesView(dl leikwan.DeviceList) map[string]interface{} {
	ips := make([]string, 0, len(dl.Devices))
	for _, d := range dl.Devices {
		if ip := lkStr(d.IP); ip != "" {
			ips = append(ips, ip)
		}
	}
	nodes, _ := h.repo.LeiKwanNodesByIPs(ips)
	entry := map[int64]bool{}
	if setting, _ := h.repo.GetLeiKwanSetting(); setting != nil {
		for _, n := range h.leiKwanEntryNodes(setting.EntryGroupID) {
			entry[n.ID] = true
		}
	}
	items := make([]map[string]interface{}, 0, len(dl.Devices))
	for _, d := range dl.Devices {
		item := map[string]interface{}{"deviceId": d.DeviceID, "clientId": d.ClientID, "name": lkStr(d.DeviceName), "platform": lkStr(d.Platform),
			"osVersion": lkStr(d.OSVersion), "clientBuild": lkStr(d.ClientBuild), "ip": lkStr(d.IP), "online": d.Online, "state": d.State,
			"countsTowardLimit": d.CountsTowardLimit, "blockedUntil": lkVendorMs(d.BlockedUntil), "firstSeenAt": lkVendorMs(d.FirstSeenAt),
			"lastSeenAt": lkVendorMs(d.LastSeenAt)}
		if n, ok := nodes[strings.ToLower(lkStr(d.IP))]; ok {
			item["nodeId"], item["nodeName"], item["leikwanEntry"] = n.ID, n.Name, entry[n.ID]
		}
		items = append(items, item)
	}
	return map[string]interface{}{"limit": dl.OnlineDeviceLimit, "count": dl.OnlineDeviceCount, "slots": dl.AvailableDeviceSlots,
		"limitSource": dl.LimitSource, "windowSec": dl.OnlineWindowSeconds, "items": items}
}
