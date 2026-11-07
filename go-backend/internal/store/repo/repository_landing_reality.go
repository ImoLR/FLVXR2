package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Plan 227: Reality 伪装域名 candidates (built-in / custom / 妙妙屋域名池),
// 屏蔽, per-server 测速 results and the automatic fastest pick for NEW nodes.

const (
	RealityModeAuto   = "auto"
	RealityModeManual = "manual"

	// RealityProbeMaxCandidates is what one 测速 sends (agent / Helper cap 64).
	RealityProbeMaxCandidates = 64
	// RealityCustomMax caps admin custom domains.
	RealityCustomMax = 40
	// RealityServerNamesMax: host + up to 4 certificate names of the same site.
	RealityServerNamesMax = 5

	realitySeedMarker = "landing_reality_domains_seed_v1"
)

type realitySeed struct {
	domain  string
	blocked bool
	note    string
}

// Built-in candidates: 10 usable (TLS 1.3 + X25519 + h2 checked 11-06 from the
// dev box and, SNI-wise, from Shanghai) + the default-blocked set.
var realityBuiltinSeeds = []realitySeed{
	{"www.lovelive-anime.jp", false, "原来的默认目标（妙妙屋同款，CloudFront）"},
	{"www.tesla.com", false, "CDN：Akamai"},
	{"www.nvidia.com", false, "CDN：Akamai"},
	{"www.amd.com", false, "CDN：Akamai"},
	{"www.intel.com", false, "CDN：Akamai"},
	{"www.sony.com", false, "CDN：Akamai"},
	{"www.samsung.com", false, "CDN：Akamai"},
	{"www.dell.com", false, "CDN：Akamai"},
	{"www.logitech.com", false, "CDN：CloudFront"},
	{"addons.mozilla.org", false, "CDN：Fastly"},
	{"www.microsoft.com", true, "教程和各家面板最常用的目标，用的人太多；微软在大陆有本地节点，海外 IP 冒充它容易露馅"},
	{"www.bing.com", true, "微软，同上"},
	{"www.apple.com", true, "苹果：热门目标；大陆走本地 CDN，海外落地冒充它特征明显"},
	{"www.icloud.com", true, "iCloud 大陆由云上贵州运营，海外 IP 冒充它特征明显"},
	{"www.amazon.com", true, "热门默认目标；亚马逊自有 IP 段（ASN）特征明显"},
	{"aws.amazon.com", true, "AWS 官网，同上"},
	{"www.cloudflare.com", true, "3x-ui 第一个默认目标，用的人极多；Cloudflare 自有 ASN 13335"},
	{"dl.google.com", true, "大陆连谷歌 IP 不通（上海实测超时）；谷歌域名在大陆解析到北京服务器，冒充明显"},
}

// realityRiskRule blocks pool domains of the same kind as the built-in
// blocked set (and mainland sites) when they first appear.
type realityRiskRule struct {
	suffixes []string
	note     string
}

var realityRiskRules = []realityRiskRule{
	{[]string{"microsoft.com", "bing.com", "bing.net", "live.com", "office.com", "windows.com", "azure.com"}, "微软系：热门目标、大陆有本地节点，海外 IP 冒充容易露馅"},
	{[]string{"apple.com", "icloud.com", "cdn-apple.com", "apple.com.cn", "mzstatic.com"}, "苹果系：热门目标、大陆走本地 CDN，海外落地冒充特征明显"},
	{[]string{"amazon.com", "amazonaws.com", "cloudfront.net"}, "亚马逊 / AWS：热门目标，自有 ASN 特征明显"},
	{[]string{"cloudflare.com"}, "Cloudflare：用的人极多，自有 ASN 13335"},
	{[]string{"google.com", "gstatic.com", "googleapis.com", "youtube.com"}, "谷歌系：大陆连谷歌 IP 不通，大陆解析到北京服务器"},
	{[]string{"cn", "bilibili.com", "ixigua.com", "kwai.net", "douyin.com", "qq.com", "baidu.com", "alicdn.com", "taobao.com"}, "国内网站：服务器在海外却冒充国内网站，IP 归属一眼不符"},
}

func realityRiskNote(domain string) string {
	for _, r := range realityRiskRules {
		for _, s := range r.suffixes {
			if domain == s || strings.HasSuffix(domain, "."+s) {
				return r.note
			}
		}
	}
	return ""
}

func seedLandingRealityDomains(db *gorm.DB) error {
	var marker int64
	if err := db.Model(&model.ViteConfig{}).Where("name = ?", realitySeedMarker).Count(&marker).Error; err != nil {
		return err
	}
	if marker > 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UnixMilli()
		for i, s := range realityBuiltinSeeds {
			var existing model.LandingRealityDomain
			err := tx.Where("domain = ?", s.domain).First(&existing).Error
			if err == nil {
				if err := tx.Model(&existing).Updates(map[string]any{"source": "builtin", "note": s.note, "sort_order": i + 1, "updated_time": now}).Error; err != nil {
					return err
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			row := model.LandingRealityDomain{Domain: s.domain, Source: "builtin", Blocked: s.blocked, Note: s.note, SortOrder: i + 1, CreatedTime: now, UpdatedTime: now}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.ViteConfig{Name: realitySeedMarker, Value: "1", Time: now}).Error
	})
}

// NormalizeRealityDomain validates an admin-entered domain (host or
// host:443; https:// prefix and path tolerated).
func NormalizeRealityDomain(raw string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(raw))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	if h, p, err := net.SplitHostPort(d); err == nil {
		if p != "443" {
			return "", errors.New("伪装域名只支持 443 端口")
		}
		d = h
	}
	d = strings.TrimSuffix(d, ".")
	if d == "" || len(d) > 253 || net.ParseIP(d) != nil || !strings.Contains(d, ".") {
		return "", errors.New("请输入域名，例如 www.example.com")
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", errors.New("域名格式不对")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", errors.New("域名格式不对")
			}
		}
	}
	return d, nil
}

// ListLandingRealityDomains returns all candidates: built-in first, then
// custom, then pool, each in its own order.
func (r *Repository) ListLandingRealityDomains() ([]model.LandingRealityDomain, error) {
	var rows []model.LandingRealityDomain
	if err := r.db.Find(&rows).Error; err != nil {
		return nil, err
	}
	sortRealityDomains(rows)
	return rows, nil
}

func realitySourceRank(s string) int {
	switch s {
	case "builtin":
		return 0
	case "custom":
		return 1
	default:
		return 2
	}
}

func sortRealityDomains(rows []model.LandingRealityDomain) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if ra, rb := realitySourceRank(a.Source), realitySourceRank(b.Source); ra != rb {
			return ra < rb
		}
		if a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		if a.Source == "custom" && a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Domain < b.Domain
	})
}

// RealityProbeTargets are the non-blocked candidates in list order (≤ 64),
// as host:443.
func (r *Repository) RealityProbeTargets() ([]string, error) {
	rows, err := r.ListLandingRealityDomains()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range rows {
		if d.Blocked {
			continue
		}
		out = append(out, d.Domain+":443")
		if len(out) == RealityProbeMaxCandidates {
			break
		}
	}
	return out, nil
}

func (r *Repository) AddLandingRealityDomain(raw string) (*model.LandingRealityDomain, error) {
	d, err := NormalizeRealityDomain(raw)
	if err != nil {
		return nil, err
	}
	var row model.LandingRealityDomain
	err = r.db.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&model.LandingRealityDomain{}).Where("domain = ?", d).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return errors.New("这个域名已经在列表里")
		}
		if err := tx.Model(&model.LandingRealityDomain{}).Where("source = ?", "custom").Count(&n).Error; err != nil {
			return err
		}
		if n >= RealityCustomMax {
			return fmt.Errorf("自定义域名最多 %d 个", RealityCustomMax)
		}
		now := time.Now().UnixMilli()
		row = model.LandingRealityDomain{Domain: d, Source: "custom", CreatedTime: now, UpdatedTime: now}
		return tx.Create(&row).Error
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// DeleteLandingRealityDomain removes a custom domain. Built-in and pool
// domains cannot be deleted, only blocked; a custom domain that is also in the
// pool turns into a pool entry.
func (r *Repository) DeleteLandingRealityDomain(id int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var row model.LandingRealityDomain
		if err := tx.First(&row, id).Error; err != nil {
			return errors.New("域名不存在")
		}
		if row.Source != "custom" {
			return errors.New("内置和妙妙屋域名池里的域名不能删除，可以屏蔽")
		}
		if row.InPool {
			return tx.Model(&row).Updates(map[string]any{"source": "pool", "updated_time": time.Now().UnixMilli()}).Error
		}
		return tx.Delete(&row).Error
	})
}

func (r *Repository) SetLandingRealityDomainBlocked(id int64, blocked bool) (*model.LandingRealityDomain, error) {
	var row model.LandingRealityDomain
	if err := r.db.First(&row, id).Error; err != nil {
		return nil, errors.New("域名不存在")
	}
	if err := r.db.Model(&row).Updates(map[string]any{"blocked": blocked, "updated_time": time.Now().UnixMilli()}).Error; err != nil {
		return nil, err
	}
	row.Blocked = blocked
	return &row, nil
}

// RealityDomainBlocked reports whether host is a blocked candidate.
func (r *Repository) RealityDomainBlocked(host string) bool {
	var n int64
	_ = r.db.Model(&model.LandingRealityDomain{}).Where("domain = ? AND blocked = ?", strings.ToLower(host), true).Count(&n).Error
	return n > 0
}

// ReplaceLandingRealityPool applies the current 妙妙屋 pool: new domains are
// added (blocked when they match the risky kinds), built-in / custom rows
// get in_pool, pool rows that left the pool are removed unless the admin
// blocked them (the block is kept if the domain comes back).
func (r *Repository) ReplaceLandingRealityPool(domains []string) (added int, err error) {
	want := map[string]bool{}
	var order []string
	for _, raw := range domains {
		d, e := NormalizeRealityDomain(raw)
		if e != nil || want[d] {
			continue
		}
		want[d] = true
		order = append(order, d)
		if len(order) == 200 {
			break
		}
	}
	err = r.db.Transaction(func(tx *gorm.DB) error {
		var rows []model.LandingRealityDomain
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&rows).Error; err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		have := map[string]model.LandingRealityDomain{}
		for _, row := range rows {
			have[row.Domain] = row
			switch {
			case want[row.Domain] && !row.InPool:
				if err := tx.Model(&model.LandingRealityDomain{}).Where("id = ?", row.ID).Updates(map[string]any{"in_pool": true, "updated_time": now}).Error; err != nil {
					return err
				}
			case !want[row.Domain] && row.InPool && row.Source == "pool" && !row.Blocked:
				if err := tx.Delete(&model.LandingRealityDomain{}, row.ID).Error; err != nil {
					return err
				}
			case !want[row.Domain] && row.InPool:
				if err := tx.Model(&model.LandingRealityDomain{}).Where("id = ?", row.ID).Updates(map[string]any{"in_pool": false, "updated_time": now}).Error; err != nil {
					return err
				}
			}
		}
		for i, d := range order {
			if _, ok := have[d]; ok {
				continue
			}
			note := realityRiskNote(d)
			row := model.LandingRealityDomain{Domain: d, Source: "pool", InPool: true, Blocked: note != "", Note: note, SortOrder: i + 1, CreatedTime: now, UpdatedTime: now}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			added++
		}
		return nil
	})
	return added, err
}

// RealityProbeResult is one candidate of a 测速 (agent RealityProbe /
// Helper probe, contract reality-pick-v1 §1).
type RealityProbeResult struct {
	Target         string           `json:"target"`
	Feasible       bool             `json:"feasible"`
	Reason         string           `json:"reason,omitempty"`
	LatencyMs      int              `json:"latency_ms"`
	BestIP         string           `json:"best_ip,omitempty"`
	IPs            []RealityProbeIP `json:"ips"`
	TLS13          bool             `json:"tls13"`
	X25519         bool             `json:"x25519"`
	H2             bool             `json:"h2"`
	CertValid      bool             `json:"cert_valid"`
	ServerNames    []string         `json:"server_names"`
	CertChainBytes int              `json:"cert_chain_bytes"`
	// Plan 229 (panel side, stored with the table): this 测速's samples (ms,
	// -1 = not usable), worst / median of the usable ones, and the 试用
	// verdict of 妙妙屋池 small sites.
	Samples      []int  `json:"samples,omitempty"`
	WorstMs      int    `json:"worst_ms,omitempty"`
	MedianMs     int    `json:"median_ms,omitempty"`
	Trial        bool   `json:"trial,omitempty"`
	TrialOK      bool   `json:"trial_ok,omitempty"`
	TrialPending bool   `json:"trial_pending,omitempty"` // only too few / too close samples so far
	TrialReason  string `json:"trial_reason,omitempty"`
}

type RealityProbeIP struct {
	IP    string `json:"ip"`
	TCPMs int    `json:"tcp_ms"`
}

// RealityPin is one pinned dest host (agent Status.reality_pins / Helper).
type RealityPin struct {
	Host      string `json:"host"`
	Port      int    `json:"port,omitempty"`
	PinnedIP  string `json:"pinned_ip,omitempty"`
	PinnedMs  int    `json:"pinned_ms,omitempty"`
	BestIP    string `json:"best_ip,omitempty"`
	BestMs    int    `json:"best_ms,omitempty"`
	IPs       int    `json:"ips,omitempty"`
	CheckedAt int64  `json:"checked_at"`
	Error     string `json:"error,omitempty"`
	// MoveReason (plan 234, agents ≥ landing_reality 3): why the last check
	// moved the pin — "faster" (≥ the IP margin) or "failover" (the pinned IP
	// failed or left the DNS answer). Absent = no move / older agent / Helper.
	MoveReason string `json:"move_reason,omitempty"`
}

// Pin move reasons (agent Status.reality_pins move_reason).
const (
	RealityPinMoveFaster   = "faster"
	RealityPinMoveFailover = "failover"
)

func realityHost(target string) string {
	host := strings.ToLower(strings.TrimSpace(target))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host
}

func realityTargetPort(target string) int {
	if _, p, err := net.SplitHostPort(strings.TrimSpace(target)); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	return 443
}

// registrableDomain is a short heuristic for "the same site": the last two
// labels, or three under a two-letter ccTLD with a short second level
// (lovelive-anime.jp, amazon.co.jp, example.com.cn).
func registrableDomain(host string) string {
	labels := strings.Split(host, ".")
	n := len(labels)
	if n <= 2 {
		return host
	}
	if len(labels[n-1]) == 2 && len(labels[n-2]) <= 3 {
		return strings.Join(labels[n-3:], ".")
	}
	return strings.Join(labels[n-2:], ".")
}

// RealityServerNamesFor picks serverNames for a probed candidate: the host
// first, then up to 4 certificate names of the same site.
func RealityServerNamesFor(host string, sans []string) []string {
	host = strings.ToLower(host)
	out := []string{host}
	base := registrableDomain(host)
	for _, n := range sans {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || n == host || strings.HasPrefix(n, "*.") || strings.ContainsAny(n, " /:\r\n") {
			continue
		}
		if n != base && !strings.HasSuffix(n, "."+base) {
			continue
		}
		dup := false
		for _, o := range out {
			dup = dup || o == n
		}
		if dup {
			continue
		}
		out = append(out, n)
		if len(out) == RealityServerNamesMax {
			break
		}
	}
	return out
}

// RealityProbeRecord is what RecordLandingRealityProbe changed.
type RealityProbeRecord struct {
	Picked      string   // host:port now the server default ("" = unchanged)
	ServerNames []string // with Picked
	Previous    string
	// Switched (plan 234): the auto pick replaced a previous default with
	// another domain (not the first pick, not a server-names refresh);
	// Forced = it was a failover (previous default not pickable any more).
	Switched bool
	Forced   bool
}

// RecordLandingRealityProbe stores a 测速 table (rows as merged by
// MergeRealitySamples), adds its samples to the server's history, marks the
// 试用 verdicts and, in auto mode, makes the best usable candidate (see
// PickRealityResult; switch margin: DecideRealityPick) the server default for
// NEW nodes. Existing nodes keep
// their dest; no revision bump (no agent push needed).
func (r *Repository) RecordLandingRealityProbe(serverID int64, results []RealityProbeResult, probeErr string, stats *RealityProbeStats) (RealityProbeRecord, error) {
	var rec RealityProbeRecord
	info, err := r.realityDomainSets()
	if err != nil {
		return rec, err
	}
	margin := r.SwitchMargins().RealityDomainMs
	err = r.db.Transaction(func(tx *gorm.DB) error {
		var s model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, serverID).Error; err != nil {
			return err
		}
		now := info.Now.UnixMilli()
		updates := map[string]any{"reality_probe_error": probeErr}
		rec.Previous = s.RealityDest
		if probeErr == "" {
			info.History = LandingRealityHistory(&s)
			for _, res := range results {
				if res.Reason == "已取消" {
					continue
				}
				info.History.add(realityHost(res.Target), info.Now.Unix(), rowSamples(res))
			}
			info.History.prune(info.Now)
			info.History.Stats = stats
			annotateTrial(results, info)
			raw, _ := json.Marshal(results)
			hist, _ := json.Marshal(info.History)
			updates["reality_probe"] = string(raw)
			updates["reality_probed_at"] = now
			updates["reality_samples"] = string(hist)
		}
		if probeErr == "" && NormalizeRealityMode(s.RealityMode) == RealityModeAuto {
			// Plan 233: the current default stays unless a challenger is ≥
			// the margin (plan 234 setting) faster or it is not pickable any more.
			decision := DecideRealityPick(results, func(h string) bool { return info.Blocked[h] }, s.RealityDest, margin)
			applyRealityPick(&s, decision, updates, &rec)
		}
		return tx.Model(&model.LandingServer{}).Where("id = ?", serverID).Updates(updates).Error
	})
	return rec, err
}

// applyRealityPick turns a pick decision into the server's reality_dest /
// reality_server_names updates (the default for NEW nodes) and fills rec.
func applyRealityPick(s *model.LandingServer, decision RealityPickDecision, updates map[string]any, rec *RealityProbeRecord) {
	best := decision.Pick
	if best == nil {
		return
	}
	host := realityHost(best.Target)
	dest := net.JoinHostPort(host, strconv.Itoa(realityTargetPort(best.Target)))
	names := RealityServerNamesFor(host, best.ServerNames)
	nb, _ := json.Marshal(names)
	if dest != s.RealityDest || string(nb) != s.RealityServerNames {
		updates["reality_dest"] = dest
		updates["reality_server_names"] = string(nb)
		rec.Picked, rec.ServerNames = dest, names
	}
	if prev := realityDestKey(s.RealityDest); prev != "" && prev != realityDestKey(dest) {
		rec.Switched, rec.Forced = true, decision.Forced
	}
}

// RealityAutoPickServerIDs (plan 237) are the active 自动 servers with a
// stored 测速 table, i.e. the ones a changed 伪装域名 margin can affect.
func (r *Repository) RealityAutoPickServerIDs() ([]int64, error) {
	var ids []int64
	err := r.db.Model(&model.LandingServer{}).Where("status = ? AND reality_mode <> ? AND reality_probe <> ?", "active", RealityModeManual, "").Order("id asc").Pluck("id", &ids).Error
	return ids, err
}

// ReapplyRealityDomainMargin (plan 237) re-runs the 伪装域名 pick of one
// 自动 server ONCE on its stored 测速 table with a changed margin — no probe,
// no revision bump (the pick is the default for NEW nodes, as after a 测速).
// Only what the margin decides is applied: the current default is still
// pickable and a challenger now beats it by ≥ marginMs. A failover (current
// not pickable any more, e.g. blocked since) stays with the next 测速, as
// before; a raised margin never switches.
func (r *Repository) ReapplyRealityDomainMargin(serverID int64, marginMs int) (RealityProbeRecord, error) {
	var rec RealityProbeRecord
	info, err := r.realityDomainSets()
	if err != nil {
		return rec, err
	}
	err = r.db.Transaction(func(tx *gorm.DB) error {
		var s model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, serverID).Error; err != nil {
			return err
		}
		if s.Status != "active" || NormalizeRealityMode(s.RealityMode) != RealityModeAuto {
			return nil
		}
		d := DecideRealityPick(LandingRealityProbeResults(&s), func(h string) bool { return info.Blocked[h] }, s.RealityDest, marginMs)
		if d.Pick == nil || d.Forced || d.Current == nil || d.Pick == d.Current {
			return nil
		}
		next := RealityProbeRecord{Previous: s.RealityDest}
		updates := map[string]any{}
		applyRealityPick(&s, d, updates, &next)
		if !next.Switched || len(updates) == 0 {
			return nil
		}
		if err := tx.Model(&model.LandingServer{}).Where("id = ?", serverID).Updates(updates).Error; err != nil {
			return err
		}
		rec = next
		return nil
	})
	return rec, err
}

// RecordLandingRealityPins stores the reported pins when they changed and
// returns the pin moves against the stored ones (plan 234 switch counts;
// counted once: the write only succeeds against the pins it compared with).
func (r *Repository) RecordLandingRealityPins(serverID int64, pins []RealityPin) ([]AutoSwitchEvent, error) {
	if pins == nil {
		pins = []RealityPin{}
	}
	for i := range pins {
		if r := pins[i].MoveReason; r != RealityPinMoveFaster && r != RealityPinMoveFailover {
			pins[i].MoveReason = ""
		}
	}
	sort.SliceStable(pins, func(i, j int) bool {
		if pins[i].Host != pins[j].Host {
			return pins[i].Host < pins[j].Host
		}
		if pins[i].Port != pins[j].Port {
			return pins[i].Port < pins[j].Port
		}
		return pins[i].PinnedIP < pins[j].PinnedIP
	})
	raw, _ := json.Marshal(pins)
	var rows []model.LandingServer
	if err := r.db.Select("id", "reality_pins").Where("id = ?", serverID).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 || rows[0].RealityPins == string(raw) {
		return nil, nil
	}
	res := r.db.Model(&model.LandingServer{}).Where("id = ? AND reality_pins = ?", serverID, rows[0].RealityPins).Updates(map[string]any{"reality_pins": string(raw), "reality_pins_at": time.Now().UnixMilli()})
	if res.Error != nil || res.RowsAffected == 0 {
		return nil, res.Error
	}
	return RealityPinMoves(serverID, LandingRealityPins(&rows[0]), pins, time.Now()), nil
}

// RealityPinMoves are the automatic IP switches between two reports of one
// server: per dest (host, port) every pinned IP that is new while the dest
// was already pinned to another one. A first pin, a fallback to the host and
// a dest that went away are not switches. A Helper may report several IPs per
// dest (unguarded nodes with live connections move later): nodes catching up
// with an IP already reported count nothing.
func RealityPinMoves(serverID int64, old, cur []RealityPin, at time.Time) []AutoSwitchEvent {
	type key struct {
		host string
		port int
	}
	k := func(p RealityPin) key { return key{strings.ToLower(strings.TrimSpace(p.Host)), p.Port} }
	before := map[key]map[string]bool{}
	for _, p := range old {
		if p.PinnedIP == "" {
			continue
		}
		if before[k(p)] == nil {
			before[k(p)] = map[string]bool{}
		}
		before[k(p)][p.PinnedIP] = true
	}
	var out []AutoSwitchEvent
	seen := map[key]map[string]bool{}
	for _, p := range cur {
		prev := before[k(p)]
		if p.PinnedIP == "" || len(prev) == 0 || prev[p.PinnedIP] || seen[k(p)][p.PinnedIP] {
			continue
		}
		if seen[k(p)] == nil {
			seen[k(p)] = map[string]bool{}
		}
		seen[k(p)][p.PinnedIP] = true
		out = append(out, AutoSwitchEvent{Kind: AutoSwitchRealityIP, Unit: serverID, Failover: p.MoveReason == RealityPinMoveFailover, At: at})
	}
	return out
}

func NormalizeRealityMode(m string) string {
	if m == RealityModeManual {
		return RealityModeManual
	}
	return RealityModeAuto
}

// LandingRealityProbeResults decodes a server's stored 测速 table.
func LandingRealityProbeResults(s *model.LandingServer) []RealityProbeResult {
	var out []RealityProbeResult
	if s == nil || s.RealityProbe == "" || json.Unmarshal([]byte(s.RealityProbe), &out) != nil {
		return []RealityProbeResult{}
	}
	return out
}

// LandingRealityPins decodes a server's stored pins.
func LandingRealityPins(s *model.LandingServer) []RealityPin {
	var out []RealityPin
	if s == nil || s.RealityPins == "" || json.Unmarshal([]byte(s.RealityPins), &out) != nil {
		return []RealityPin{}
	}
	return out
}

// RealityAutoDueServers are active servers in auto mode whose last 测速 is
// older than maxAge (oldest first).
func (r *Repository) RealityAutoDueServers(maxAge time.Duration) ([]model.LandingServer, error) {
	var rows []model.LandingServer
	cutoff := time.Now().Add(-maxAge).UnixMilli()
	err := r.db.Where("status = ? AND reality_mode <> ? AND reality_probed_at < ?", "active", RealityModeManual, cutoff).Order("reality_probed_at asc, id asc").Find(&rows).Error
	return rows, err
}
