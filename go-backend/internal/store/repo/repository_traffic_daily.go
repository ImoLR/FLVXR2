package repo

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"time"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 212: per-day billed traffic for the 主页 cards.

const (
	// TrafficKindTotal marks a user's billed total row (ServerRefID 0).
	TrafficKindTotal = "total"
	// TrafficDailyRetentionDays is how long day rows are kept.
	TrafficDailyRetentionDays = 400
	// trafficDailySinceKey (vite_config) = unix ms when the rollup started.
	trafficDailySinceKey = "traffic_daily_since"
)

// TrafficDayZone is the calendar of the day rows (UTC+8, like the panel's users).
var TrafficDayZone = time.FixedZone("UTC+8", 8*3600)

// TrafficDayKey is the UTC+8 date of t as YYYYMMDD.
func TrafficDayKey(t time.Time) int64 {
	y, m, d := t.In(TrafficDayZone).Date()
	return int64(y*10000 + int(m)*100 + d)
}

func trafficDayStart(day int64) time.Time {
	return time.Date(int(day/10000), time.Month(day/100%100), int(day%100), 0, 0, 0, 0, TrafficDayZone)
}

func trafficDayAdd(day int64, days int) int64 {
	return TrafficDayKey(trafficDayStart(day).AddDate(0, 0, days))
}

// addTrafficDailyTx adds one flow upload to today's rows inside the billing
// transaction: users = billed per-user deltas (user.in_flow/out_flow), servers =
// billed per-user per-server deltas (user_server_quota). Rows are written in key
// order with one upsert so concurrent uploads lock them in the same order.
func addTrafficDailyTx(tx *gorm.DB, users map[int64]FlowCounterDelta, servers map[ServerQuotaKey]FlowCounterDelta, now time.Time) error {
	day, ms := TrafficDayKey(now), now.UnixMilli()
	rows := make([]model.TrafficDaily, 0, len(users)+len(servers))
	for id, d := range users {
		in, out := max(d.In, 0), max(d.Out, 0)
		if id <= 0 || in+out == 0 {
			continue
		}
		rows = append(rows, model.TrafficDaily{Day: day, UserID: id, ServerKind: TrafficKindTotal, InBytes: in, OutBytes: out, UpdatedTime: ms})
	}
	for key, d := range servers {
		in, out := max(d.In, 0), max(d.Out, 0)
		if key.UserID <= 0 || key.RefID <= 0 || (key.Kind != "node" && key.Kind != "landing") || in+out == 0 {
			continue
		}
		rows = append(rows, model.TrafficDaily{Day: day, UserID: key.UserID, ServerKind: key.Kind, ServerRefID: key.RefID, InBytes: in, OutBytes: out, UpdatedTime: ms})
	}
	if len(rows) == 0 {
		return nil
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.UserID != b.UserID {
			return a.UserID < b.UserID
		}
		if a.ServerKind != b.ServerKind {
			return a.ServerKind < b.ServerKind
		}
		return a.ServerRefID < b.ServerRefID
	})
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "day"}, {Name: "user_id"}, {Name: "server_kind"}, {Name: "server_ref_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"in_bytes":     gorm.Expr("traffic_daily.in_bytes + excluded.in_bytes"),
			"out_bytes":    gorm.Expr("traffic_daily.out_bytes + excluded.out_bytes"),
			"updated_time": gorm.Expr("excluded.updated_time"),
		}),
	}).CreateInBatches(&rows, 200).Error
}

// ensureTrafficDailySince records when the rollup started (first start of a
// panel with plan 212). Never blocks the start.
func ensureTrafficDailySince(db *gorm.DB) {
	now := time.Now().UnixMilli()
	cfg := model.ViteConfig{Name: trafficDailySinceKey, Value: strconv.FormatInt(now, 10), Time: now}
	if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoNothing: true}).Create(&cfg).Error; err != nil {
		log.Printf("[traffic-daily] since marker: %v", err)
	}
}

// TrafficDailySince returns when the rollup started (unix ms; 0 = unknown).
func (r *Repository) TrafficDailySince() int64 {
	if r == nil || r.db == nil {
		return 0
	}
	var cfg model.ViteConfig
	if err := r.db.Select("value").Where("name = ?", trafficDailySinceKey).First(&cfg).Error; err != nil {
		return 0
	}
	v, _ := strconv.ParseInt(cfg.Value, 10, 64)
	return v
}

// PurgeTrafficDaily deletes day rows older than the retention window.
func (r *Repository) PurgeTrafficDaily(now time.Time) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("repository not initialized")
	}
	cutoff := trafficDayAdd(TrafficDayKey(now), -TrafficDailyRetentionDays)
	res := r.db.Where("day < ?", cutoff).Delete(&model.TrafficDaily{})
	return res.RowsAffected, res.Error
}

// ─── User 主页: 服务器流量 ─────────────────────────────────────────────

// ServerTrafficRow is one granted server of the user with its current-period
// usage and the limit enforcement uses (0 = 不限).
type ServerTrafficRow struct {
	ServerKind  string `json:"serverKind"`
	ServerRefID int64  `json:"serverRefId"`
	ServerName  string `json:"serverName"`
	Entry       bool   `json:"entry"`
	Landing     bool   `json:"landing"`
	UsedIn      int64  `json:"usedIn"`
	UsedOut     int64  `json:"usedOut"`
	LimitBytes  int64  `json:"limitBytes"`
	Exhausted   bool   `json:"exhausted"`
	PoolName    string `json:"poolName,omitempty"`
}

// ServerTrafficView is the 「服务器流量」 card of one user.
type ServerTrafficView struct {
	ResetDay    int64              `json:"resetDay"`
	PeriodStart string             `json:"periodStart"` // YYYY-MM-DD; "" = no reset (one lifetime period)
	NextReset   string             `json:"nextReset"`   // YYYY-MM-DD; ""  = no reset
	Servers     []ServerTrafficRow `json:"servers"`
}

// landingPeriodBounds mirrors LandingPeriodKey: the start of the current period
// and of the next one (zero times when the reset day is off).
func landingPeriodBounds(resetDay int64, now time.Time) (time.Time, time.Time) {
	if resetDay <= 0 {
		return time.Time{}, time.Time{}
	}
	boundary := func(y int, m time.Month) time.Time {
		day := int(resetDay)
		last := time.Date(y, m+1, 0, 0, 0, 0, 0, now.Location()).Day()
		if day > last {
			day = last
		}
		return time.Date(y, m, day, 0, 0, 0, 0, now.Location())
	}
	year, month, _ := now.Date()
	start := boundary(year, month)
	if now.Before(start) {
		start = boundary(year, month-1)
	}
	next := boundary(start.Year(), start.Month()+1)
	return start, next
}

// UserServerTraffic lists the user's granted servers (the same list as the
// admin's per-server picker) with 本期已用 / 限额 computed exactly like
// applyServerQuotasTx: an elapsed period reads 0, a row without its own limit
// follows the server's current default. Admins get no rows.
func (r *Repository) UserServerTraffic(userID int64, now time.Time) (ServerTrafficView, error) {
	view := ServerTrafficView{Servers: []ServerTrafficRow{}}
	if r == nil || r.db == nil || userID <= 0 {
		return view, nil
	}
	var u model.User
	if err := r.db.Select("id", "role_id", "flow_reset_time").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return view, nil
		}
		return view, err
	}
	view.ResetDay = u.FlowResetTime
	if start, next := landingPeriodBounds(u.FlowResetTime, now); !start.IsZero() {
		view.PeriodStart, view.NextReset = start.Format("2006-01-02"), next.Format("2006-01-02")
	}
	if u.RoleID == 0 {
		return view, nil
	}
	related, err := r.UserRelatedServers(userID)
	if err != nil {
		return view, err
	}
	if len(related) == 0 {
		return view, nil
	}
	period := LandingPeriodKey(u.FlowResetTime, now)
	var quotas []model.UserServerQuota
	if err := r.db.Select("server_kind", "server_ref_id", "limit_bytes", "limit_override", "used_in", "used_out", "period_key").
		Where("user_id = ?", userID).Find(&quotas).Error; err != nil {
		return view, err
	}
	quotaByKey := make(map[string]model.UserServerQuota, len(quotas))
	for _, q := range quotas {
		quotaByKey[fmt.Sprintf("%s:%d", q.ServerKind, q.ServerRefID)] = q
	}
	var nodeIDs, landingIDs []int64
	for _, s := range related {
		if s.ServerKind == "node" {
			nodeIDs = append(nodeIDs, s.ServerRefID)
		} else {
			landingIDs = append(landingIDs, s.ServerRefID)
		}
	}
	defaults := map[string]int64{}
	if len(nodeIDs) > 0 {
		var rows []model.Node
		if err := r.db.Select("id", "default_user_quota_bytes").Where("id IN ?", nodeIDs).Find(&rows).Error; err != nil {
			return view, err
		}
		for _, n := range rows {
			defaults[fmt.Sprintf("node:%d", n.ID)] = n.DefaultUserQuotaBytes
		}
	}
	if len(landingIDs) > 0 {
		var rows []model.LandingServer
		if err := r.db.Select("id", "default_user_quota_bytes").Where("id IN ?", landingIDs).Find(&rows).Error; err != nil {
			return view, err
		}
		for _, s := range rows {
			defaults[fmt.Sprintf("landing:%d", s.ID)] = s.DefaultUserQuotaBytes
		}
	}
	pools := map[string]string{}
	var members []struct {
		ServerKind  string
		ServerRefID int64
		Name        string
	}
	if err := r.db.Table("user_traffic_pool_member AS m").Select("m.server_kind, m.server_ref_id, p.name").
		Joins("JOIN user_traffic_pool AS p ON p.id = m.pool_id").Where("m.user_id = ?", userID).Scan(&members).Error; err != nil {
		return view, err
	}
	for _, m := range members {
		pools[fmt.Sprintf("%s:%d", m.ServerKind, m.ServerRefID)] = m.Name
	}
	for _, s := range related {
		key := fmt.Sprintf("%s:%d", s.ServerKind, s.ServerRefID)
		row := ServerTrafficRow{ServerKind: s.ServerKind, ServerRefID: s.ServerRefID, ServerName: s.ServerName, Entry: s.Entry, Landing: s.Landing, LimitBytes: defaults[key], PoolName: pools[key]}
		if q, ok := quotaByKey[key]; ok {
			if q.LimitOverride {
				row.LimitBytes = q.LimitBytes
			}
			if q.PeriodKey == period {
				row.UsedIn, row.UsedOut = q.UsedIn, q.UsedOut
			}
		}
		row.Exhausted = row.LimitBytes > 0 && row.UsedIn+row.UsedOut >= row.LimitBytes
		view.Servers = append(view.Servers, row)
	}
	return view, nil
}

// ─── Admin 主页: 流量排行 ──────────────────────────────────────────────

// TrafficRankItem is one user or one server of a ranking window.
type TrafficRankItem struct {
	UserID      int64    `json:"userId,omitempty"`
	User        string   `json:"user,omitempty"`
	ServerKind  string   `json:"serverKind,omitempty"`
	ServerRefID int64    `json:"serverRefId,omitempty"`
	Roles       []string `json:"roles,omitempty"` // entry | landing
	Name        string   `json:"name"`
	Deleted     bool     `json:"deleted,omitempty"`
	In          int64    `json:"in"`
	Out         int64    `json:"out"`
}

// TrafficWindow is one tab (今日 / 本周 / 本月) of the ranking.
type TrafficWindow struct {
	Key     string            `json:"key"`
	From    string            `json:"from"` // YYYY-MM-DD
	To      string            `json:"to"`
	Partial bool              `json:"partial"` // the rollup started after the window began
	Users   []TrafficRankItem `json:"users"`
	Servers []TrafficRankItem `json:"servers"`
}

// TrafficRankingView is the admin 「流量排行」 card.
type TrafficRankingView struct {
	Since   int64           `json:"since"`
	Windows []TrafficWindow `json:"windows"`
}

type trafficWindowSpec struct {
	key      string
	from, to int64
}

func trafficWindows(now time.Time) []trafficWindowSpec {
	today := TrafficDayKey(now)
	return []trafficWindowSpec{
		{"today", today, today},
		{"week", trafficDayAdd(today, -6), today},
		{"month", today/100*100 + 1, today},
	}
}

// TrafficWindowRange returns the day range of a window key (today|week|month).
func TrafficWindowRange(key string, now time.Time) (int64, int64, bool) {
	for _, w := range trafficWindows(now) {
		if w.key == key {
			return w.from, w.to, true
		}
	}
	return 0, 0, false
}

func trafficDayText(day int64) string {
	return fmt.Sprintf("%04d-%02d-%02d", day/10000, day/100%100, day%100)
}

type trafficDayRow struct {
	Day         int64
	UserID      int64
	ServerKind  string
	ServerRefID int64
	InBytes     int64
	OutBytes    int64
}

// TrafficRanking returns 今日 / 本周(近 7 天) / 本月 rankings of active users and of
// servers in one read of the day rows (≤ 31 days). Rows of deleted users are
// skipped; servers deleted meanwhile stay, marked deleted.
func (r *Repository) TrafficRanking(now time.Time) (TrafficRankingView, error) {
	view := TrafficRankingView{Windows: []TrafficWindow{}}
	if r == nil || r.db == nil {
		return view, errors.New("repository not initialized")
	}
	view.Since = r.TrafficDailySince()
	specs := trafficWindows(now)
	from := specs[0].from
	for _, w := range specs {
		from = min(from, w.from)
	}
	to := specs[0].to
	var rows []trafficDayRow
	if err := r.db.Table("traffic_daily AS d").
		Select("d.day, d.user_id, d.server_kind, d.server_ref_id, d.in_bytes, d.out_bytes").
		Joins(`JOIN "user" AS u ON u.id = d.user_id`).
		Where("d.day >= ? AND d.day <= ?", from, to).Scan(&rows).Error; err != nil {
		return view, err
	}
	users, err := r.trafficUserNames(rows)
	if err != nil {
		return view, err
	}
	servers, err := r.trafficServerInfo(rows)
	if err != nil {
		return view, err
	}
	for _, w := range specs {
		win := TrafficWindow{Key: w.key, From: trafficDayText(w.from), To: trafficDayText(w.to), Users: []TrafficRankItem{}, Servers: []TrafficRankItem{}}
		win.Partial = view.Since > trafficDayStart(w.from).UnixMilli()
		userSum := map[int64]*TrafficRankItem{}
		serverSum := map[string]*TrafficRankItem{}
		for _, row := range rows {
			if row.Day < w.from || row.Day > w.to {
				continue
			}
			if row.ServerKind == TrafficKindTotal {
				u, ok := users[row.UserID]
				if !ok || u.RoleID == 0 {
					continue // admins' own traffic is not ranked (it still counts on the servers)
				}
				it := userSum[row.UserID]
				if it == nil {
					it = &TrafficRankItem{UserID: row.UserID, User: u.User, Name: u.Name}
					userSum[row.UserID] = it
				}
				it.In += row.InBytes
				it.Out += row.OutBytes
				continue
			}
			if !servers.listed(row.ServerKind, row.ServerRefID) {
				continue // only 入口 and 落地 servers are ranked
			}
			key := fmt.Sprintf("%s:%d", row.ServerKind, row.ServerRefID)
			it := serverSum[key]
			if it == nil {
				it = servers.item(row.ServerKind, row.ServerRefID)
				serverSum[key] = it
			}
			it.In += row.InBytes
			it.Out += row.OutBytes
		}
		win.Users = rankItems(userSum)
		win.Servers = rankItems(serverSum)
		view.Windows = append(view.Windows, win)
	}
	return view, nil
}

// UserTrafficSplitView is the per-server split of one user in one window. The
// servers add up to more than Total when a flow crosses several servers.
type UserTrafficSplitView struct {
	UserID  int64             `json:"userId"`
	User    string            `json:"user"`
	Name    string            `json:"name"`
	From    string            `json:"from"`
	To      string            `json:"to"`
	In      int64             `json:"in"`
	Out     int64             `json:"out"`
	Servers []TrafficRankItem `json:"servers"`
}

// UserTrafficSplit returns one user's per-server billed traffic in a window.
func (r *Repository) UserTrafficSplit(userID int64, window string, now time.Time) (*UserTrafficSplitView, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	from, to, ok := TrafficWindowRange(window, now)
	if !ok {
		return nil, errors.New("统计范围无效")
	}
	var u model.User
	if err := r.db.Select("id", "user", "name").First(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("用户不存在")
		}
		return nil, err
	}
	view := &UserTrafficSplitView{UserID: u.ID, User: u.User, Name: u.Name, From: trafficDayText(from), To: trafficDayText(to), Servers: []TrafficRankItem{}}
	var rows []trafficDayRow
	if err := r.db.Model(&model.TrafficDaily{}).
		Select("server_kind, server_ref_id, SUM(in_bytes) AS in_bytes, SUM(out_bytes) AS out_bytes").
		Where("day >= ? AND day <= ? AND user_id = ?", from, to, userID).
		Group("server_kind, server_ref_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	servers, err := r.trafficServerInfo(rows)
	if err != nil {
		return nil, err
	}
	sum := map[string]*TrafficRankItem{}
	for _, row := range rows {
		if row.ServerKind == TrafficKindTotal {
			view.In, view.Out = row.InBytes, row.OutBytes
			continue
		}
		if !servers.listed(row.ServerKind, row.ServerRefID) {
			continue
		}
		it := servers.item(row.ServerKind, row.ServerRefID)
		it.In, it.Out = row.InBytes, row.OutBytes
		sum[fmt.Sprintf("%s:%d", row.ServerKind, row.ServerRefID)] = it
	}
	view.Servers = rankItems(sum)
	return view, nil
}

func rankItems[K comparable](m map[K]*TrafficRankItem) []TrafficRankItem {
	out := make([]TrafficRankItem, 0, len(m))
	for _, it := range m {
		if it.In+it.Out > 0 {
			out = append(out, *it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].In+out[i].Out, out[j].In+out[j].Out
		if a != b {
			return a > b
		}
		if out[i].ServerKind != out[j].ServerKind {
			return out[i].ServerKind > out[j].ServerKind
		}
		if out[i].ServerRefID != out[j].ServerRefID {
			return out[i].ServerRefID < out[j].ServerRefID
		}
		return out[i].UserID < out[j].UserID
	})
	return out
}

func (r *Repository) trafficUserNames(rows []trafficDayRow) (map[int64]model.User, error) {
	ids := map[int64]bool{}
	for _, row := range rows {
		if row.ServerKind == TrafficKindTotal {
			ids[row.UserID] = true
		}
	}
	out := map[int64]model.User{}
	if len(ids) == 0 {
		return out, nil
	}
	list := make([]int64, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	var users []model.User
	if err := r.db.Select("id", "user", "name", "role_id").Where("id IN ?", list).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = u
	}
	return out, nil
}

type trafficServers struct {
	names map[string]string
	roles map[string][]string
}

func (s trafficServers) item(kind string, ref int64) *TrafficRankItem {
	key := fmt.Sprintf("%s:%d", kind, ref)
	it := &TrafficRankItem{ServerKind: kind, ServerRefID: ref, Name: s.names[key], Roles: s.roles[key]}
	if it.Name == "" {
		it.Deleted = true
	}
	return it
}

// listed reports whether a server takes part in the server ranking: only 入口
// and 落地 servers (the scope of the user's 「服务器流量」 card); relay / exit
// only nodes and deleted nodes (role unknown) are left out.
func (s trafficServers) listed(kind string, ref int64) bool {
	return len(s.roles[fmt.Sprintf("%s:%d", kind, ref)]) > 0
}

// trafficServerInfo resolves names and roles (入口 from the tunnels' entry
// chain rows, 落地 from landing servers) of the servers in rows. Relay and exit
// hops give no role: those servers are not ranked.
func (r *Repository) trafficServerInfo(rows []trafficDayRow) (trafficServers, error) {
	out := trafficServers{names: map[string]string{}, roles: map[string][]string{}}
	seen := map[string]bool{}
	keys := []TrafficPoolServer{}
	var nodeIDs []int64
	for _, row := range rows {
		if row.ServerKind == TrafficKindTotal {
			continue
		}
		key := fmt.Sprintf("%s:%d", row.ServerKind, row.ServerRefID)
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, TrafficPoolServer{ServerKind: row.ServerKind, ServerRefID: row.ServerRefID})
		if row.ServerKind == "node" {
			nodeIDs = append(nodeIDs, row.ServerRefID)
		} else {
			out.roles[key] = []string{"landing"}
		}
	}
	if len(keys) == 0 {
		return out, nil
	}
	names, err := serverNames(r.db, keys)
	if err != nil {
		return out, err
	}
	out.names = names
	if len(nodeIDs) > 0 {
		var chains []struct {
			NodeID    int64
			ChainType string
		}
		if err := r.db.Model(&model.ChainTunnel{}).Distinct("node_id", "chain_type").Where("node_id IN ? AND chain_type = ?", nodeIDs, "1").Scan(&chains).Error; err != nil {
			return out, err
		}
		has := map[int64]map[string]bool{}
		for _, c := range chains {
			if has[c.NodeID] == nil {
				has[c.NodeID] = map[string]bool{}
			}
			switch c.ChainType {
			case "1":
				has[c.NodeID]["entry"] = true
			}
		}
		var natives []int64
		if err := r.db.Model(&model.LandingServer{}).Where("source = ? AND node_id IN ?", "native", nodeIDs).Pluck("node_id", &natives).Error; err != nil {
			return out, err
		}
		for _, id := range natives {
			if has[id] == nil {
				has[id] = map[string]bool{}
			}
			has[id]["landing"] = true
		}
		for id, set := range has {
			roles := []string{}
			for _, role := range []string{"entry", "landing"} {
				if set[role] {
					roles = append(roles, role)
				}
			}
			out.roles[fmt.Sprintf("node:%d", id)] = roles
		}
	}
	return out, nil
}
