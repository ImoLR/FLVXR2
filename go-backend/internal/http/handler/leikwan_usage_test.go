package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"go-backend/internal/leikwan"
	"go-backend/internal/leikwan/testapi"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

func srvURL(t *testing.T, f *testapi.Fake) string {
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	return s.URL
}

func lkUsageTestEnv(t *testing.T) (*Handler, *repo.Repository, *testapi.Fake) {
	t.Helper()
	r, err := repo.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	f := testapi.New()
	f.SeedReal8()
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	h := &Handler{repo: r, leikwan: leikwan.NewClient(s.URL, nil)}
	if err := r.DB().Create(&model.LeiKwanAccount{ID: 1, Name: "利群汇聚 · 服务 39", ServiceID: 39, Token: f.Token}).Error; err != nil {
		t.Fatal(err)
	}
	return h, r, f
}

func lkSnapNext(t *testing.T, h *Handler, kind string) time.Time {
	t.Helper()
	a, _ := h.repo.GetLeiKwanAccount(1)
	s, ok := h.lkUsageSnap(a, kind)
	if !ok {
		t.Fatalf("no %s snapshot", kind)
	}
	return time.UnixMilli(s.NextAt)
}

// Plan 273: the request budget — ≤ 2 reads per round; over one hour of 2.5 min
// rounds: usage + devices every 5 min, service every 30 min, previous once.
func TestLeiKwanUsagePollBudget(t *testing.T) {
	h, r, f := lkUsageTestEnv(t)
	ctx := context.Background()
	t0 := time.Now()
	total := 0
	for i := 0; i < 24; i++ {
		n := h.LeiKwanUsageRound(ctx, t0.Add(time.Duration(i)*lkUsageRoundEvery), 0)
		if n > lkUsagePerRound {
			t.Fatalf("round %d made %d reads", i, n)
		}
		total += n
	}
	if f.UsageReads != 12 || f.DeviceReads != 12 || f.ServiceReads != 2 || f.PrevReads != 1 || total != 27 {
		t.Fatalf("one hour: usage %d devices %d service %d previous %d (total %d)", f.UsageReads, f.DeviceReads, f.ServiceReads, f.PrevReads, total)
	}
	if _, w := f.Counts(); w != 0 {
		t.Fatalf("writes to the vendor: %d", w)
	}

	// A restart shows the numbers at once (persisted), without a vendor read.
	before, _ := f.Counts()
	h2 := &Handler{repo: r, leikwan: h.leikwan}
	a, _ := r.GetLeiKwanAccount(1)
	v := h2.leiKwanUsageView(a, time.Now())
	u, _ := v["usage"].(map[string]interface{})
	if u == nil || u["usedBytes"].(int64) <= 0 || u["includedBytes"].(int64) != 1000<<30 || v["service"] == nil || v["devices"] == nil || v["previous"] == nil {
		t.Fatalf("after restart: %v", v)
	}
	if after, _ := f.Counts(); after != before {
		t.Fatalf("view made vendor reads: %d -> %d", before, after)
	}
	// The restarted poller keeps the schedule: nothing is due right away.
	if n := h2.LeiKwanUsageRound(ctx, t0.Add(23*lkUsageRoundEvery+time.Second), 0); n != 0 {
		t.Fatalf("restart round made %d reads", n)
	}
}

// Plan 273: 429 honours Retry-After for every read of the account; 5xx backs
// off 5 / 10 / 30 min; 401 waits 30 min until the admin saves the account.
func TestLeiKwanUsageBackoff(t *testing.T) {
	h, _, f := lkUsageTestEnv(t)
	ctx := context.Background()
	t0 := time.Now()
	if n := h.LeiKwanUsageRound(ctx, t0, 0); n != 2 {
		t.Fatalf("first round %d", n)
	}
	good, _ := h.lkUsageSnap(&model.LeiKwanAccount{ID: 1, ServiceID: 39}, "usage")

	// 429 with Retry-After 900 s: one read, then the account waits 15 min.
	f.Mu.Lock()
	f.FailReads, f.RetryAfter = 429, "900"
	f.Mu.Unlock()
	t1 := t0.Add(5 * time.Minute)
	if n := h.LeiKwanUsageRound(ctx, t1, 0); n != 1 {
		t.Fatalf("429 round made %d reads", n)
	}
	for _, k := range []string{"usage", "devices", "service", "previous"} {
		if next := lkSnapNext(t, h, k); next.Before(t1.Add(14 * time.Minute)) {
			t.Fatalf("%s not pushed out by Retry-After: %v", k, next.Sub(t1))
		}
	}
	// The last good snapshot stays, with the error beside it.
	s, _ := h.lkUsageSnap(&model.LeiKwanAccount{ID: 1, ServiceID: 39}, "usage")
	if s.Data != good.Data || s.FetchedAt != good.FetchedAt || s.Error == "" {
		t.Fatalf("snapshot after 429: fetched %d err %q", s.FetchedAt, s.Error)
	}
	if n := h.LeiKwanUsageRound(ctx, t1.Add(10*time.Minute), 0); n != 0 {
		t.Fatalf("read during Retry-After: %d", n)
	}

	// The shared client itself blocks the token for Retry-After as well (the
	// panel's other 利群 calls wait too); the rest of the test uses a fresh one.
	h.leikwan = leikwan.NewClient(srvURL(t, f), nil)

	// 503 three times in a row: 5, 10, then 30 min.
	f.Mu.Lock()
	f.FailReads = 503
	f.Mu.Unlock()
	at := t1.Add(16 * time.Minute)
	for i, want := range []time.Duration{5 * time.Minute, 10 * time.Minute, 30 * time.Minute} {
		h.lkUsageRecord(&model.LeiKwanAccount{ID: 1, ServiceID: 39}, "devices", at, time.Now(), "", &leikwan.APIError{Status: 503, Code: "operation_failed"})
		if got := lkSnapNext(t, h, "devices").Sub(at).Round(time.Second); got != want {
			t.Fatalf("503 #%d: wait %v, want %v", i+1, got, want)
		}
	}

	// 401: everything waits 30 min; saving the account ends it.
	f.Mu.Lock()
	f.FailReads = 401
	f.Mu.Unlock()
	t2 := t1.Add(2 * time.Hour)
	if n := h.LeiKwanUsageRound(ctx, t2, 0); n != 1 {
		t.Fatalf("401 round made %d reads", n)
	}
	if n := h.LeiKwanUsageRound(ctx, t2.Add(20*time.Minute), 0); n != 0 {
		t.Fatalf("read during the 401 wait: %d", n)
	}
	f.Mu.Lock()
	f.FailReads = 0
	f.Mu.Unlock()
	h.lkUsageResetAccount(1)
	if n := h.LeiKwanUsageRound(ctx, t2.Add(21*time.Minute), 0); n != 2 {
		t.Fatalf("after account save: %d reads", n)
	}
	s, _ = h.lkUsageSnap(&model.LeiKwanAccount{ID: 1, ServiceID: 39}, "usage")
	if s.Error != "" || s.ErrorCount != 0 {
		t.Fatalf("usage still in error after recovery: %+v", s)
	}
}

// Plan 273: no closed cycle = 「暂无上周期」 (not an error); when the current
// cycle rolls over, the previous one is read in the next free round.
func TestLeiKwanUsagePreviousCycle(t *testing.T) {
	h, _, f := lkUsageTestEnv(t)
	ctx := context.Background()
	f.NoPrevious = true
	t0 := time.Now()
	h.LeiKwanUsageRound(ctx, t0, 0)
	h.LeiKwanUsageRound(ctx, t0.Add(lkUsageRoundEvery), 0)
	a := &model.LeiKwanAccount{ID: 1, Name: "x", ServiceID: 39}
	s, _ := h.lkUsageSnap(a, "previous")
	if s.Error != "" || s.FetchedAt == 0 {
		t.Fatalf("404 usage_period_not_found treated as an error: %+v", s)
	}
	if v := h.leiKwanUsageView(a, time.Now()); v["previousNone"] != true || v["previous"] != nil {
		t.Fatalf("view: %v", v)
	}
	// The vendor rolls the cycle: next usage read sees a new start.
	f.Mu.Lock()
	f.NoPrevious = false
	raw, _ := json.Marshal(map[string]interface{}{"ok": true, "service_id": 39, "status": "active",
		"period": map[string]interface{}{"name": "current", "cycle_started_at": "2026-12-04 12:08:03", "cycle_ends_at": "2027-01-04 12:08:03", "counted_since": "2026-12-04 12:08:03"},
		"usage":  map[string]interface{}{"billing_mode": "monthly_bundle", "used_bytes": 0, "included_bytes": 1000 << 30, "remaining_bytes": 1000 << 30, "used_percent": 0}, "forwards": []interface{}{}})
	f.UsageReport = raw
	f.Mu.Unlock()
	h.LeiKwanUsageRound(ctx, t0.Add(2*lkUsageRoundEvery), 0) // usage + devices
	before := f.PrevReads
	h.LeiKwanUsageRound(ctx, t0.Add(3*lkUsageRoundEvery), 0) // previous (rolled)
	if f.PrevReads != before+1 {
		t.Fatalf("previous not re-read after the roll-over: %d -> %d", before, f.PrevReads)
	}
	if v := h.leiKwanUsageView(a, time.Now()); v["previous"] == nil || v["previousNone"] != nil {
		t.Fatalf("view after roll-over: %v", v)
	}
}

// Plan 273: devices map their IP to our node names; deleted / unregistered
// forwards add up in otherBytes.
func TestLeiKwanUsageViewDevicesAndOther(t *testing.T) {
	h, r, f := lkUsageTestEnv(t)
	db := r.DB()
	now := time.Now().UnixMilli()
	if err := db.Create(&model.NodeGroup{ID: 5, Name: "利群入口", CreatedTime: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Node{ID: 60, Name: "阿里广州", Secret: "s", ServerIP: "8.134.172.154", Port: "1-65535", CreatedTime: now, GroupID: sql.NullInt64{Int64: 5, Valid: true}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.LeiKwanSetting{ID: 1, AccountID: 1, EntryGroupID: 5}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.LeiKwanRule{UserID: 1, LineID: 1, AccountID: 1, ForwardID: 263, Name: "263", ListenPort: 10018, Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	h.LeiKwanUsageRound(context.Background(), time.Now(), 0)
	a, _ := r.GetLeiKwanAccount(1)
	v := h.leiKwanUsageView(a, time.Now())
	d := v["devices"].(map[string]interface{})
	items := d["items"].([]map[string]interface{})
	if d["limit"] != 5 || len(items) != 2 || items[0]["nodeName"] != "阿里广州" || items[0]["leikwanEntry"] != true || items[1]["nodeName"] != nil {
		t.Fatalf("devices view: %v", d)
	}
	u := v["usage"].(map[string]interface{})
	var other int64
	for id := range f.Forwards {
		if id != 263 {
			other += id<<20 + id<<18
		}
	}
	if u["otherBytes"].(int64) != other || len(u["otherForwards"].([]int64)) != 7 {
		t.Fatalf("other bytes %v want %d (%v)", u["otherBytes"], other, u["otherForwards"])
	}
	if u["cycleStart"].(int64) != time.Date(2026, 11, 4, 12, 8, 3, 0, time.UTC).UnixMilli() {
		t.Fatalf("cycle start ms %v", u["cycleStart"])
	}
}
