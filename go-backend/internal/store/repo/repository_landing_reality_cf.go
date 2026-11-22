package repo

import (
	"sort"
	"strings"
	"time"

	"go-backend/internal/cfnet"
	"go-backend/internal/store/model"
)

// Plan 290: Reality 伪装域名 hosted on Cloudflare. Reality relays every
// unauthenticated connection to dest; Cloudflare routes by SNI, so a dest on
// Cloudflare lets anyone reach any Cloudflare site through the landing (DMIT
// notice 11-22: such landings get abused as botnet C2 relays). Such candidates
// are blocked automatically (cf_hosted, next to the admin's own blocked) and
// never probed or picked; existing nodes are never changed (admin ⚠ instead).

// RealityCFReason is the reason shown with a Cloudflare-hosted domain.
const RealityCFReason = "Cloudflare 托管：未认证流量可被借道到任意 CF 网站（DMIT 11-22 公告）"

// RealityCFEvidenceMaxAge: a stored 测速 table older than this no longer
// counts as evidence (manual servers are not re-probed). Pins are the
// landing's current state (stored on change) and count while the server is
// active.
const RealityCFEvidenceMaxAge = 48 * time.Hour

// RealityCFVerdict is one host's Cloudflare check.
type RealityCFVerdict struct {
	Hosted   bool
	Evidence string // the matching IP and where it was seen ("" when not hosted)
	// Known: the check has an answer (a lookup that returned addresses, or
	// landing evidence). Unknown verdicts never change a row.
	Known bool
}

// RealityCFChange is one row whose Cloudflare state changed.
type RealityCFChange struct {
	Domain   string
	Hosted   bool
	Evidence string
}

// RealityResultCFIP is the first Cloudflare address a 测速 row reports for
// its candidate ("" = none).
func RealityResultCFIP(res RealityProbeResult) string {
	ips := make([]string, 0, len(res.IPs)+1)
	if res.BestIP != "" {
		ips = append(ips, res.BestIP)
	}
	for _, ip := range res.IPs {
		ips = append(ips, ip.IP)
	}
	return cfnet.First(ips)
}

// RealityPinCFIP is the first Cloudflare address of a pin ("" = none).
func RealityPinCFIP(p RealityPin) string {
	return cfnet.First([]string{p.PinnedIP, p.BestIP})
}

func realityCFEvidenceText(ip, where string) string {
	return ip + "（" + where + "）"
}

// RealityServerCFHosts are the hosts this server's machine resolved to a
// Cloudflare address: its pins (any age) and, when fresh, its 测速 table.
func RealityServerCFHosts(s *model.LandingServer, now time.Time) map[string]string {
	out := map[string]string{}
	if s == nil {
		return out
	}
	for _, p := range LandingRealityPins(s) {
		host := realityHost(p.Host)
		if ip := RealityPinCFIP(p); ip != "" && host != "" && out[host] == "" {
			out[host] = realityCFEvidenceText(ip, "落地 "+s.Name+" 固定的目标 IP")
		}
	}
	if s.RealityProbedAt > 0 && now.Sub(time.UnixMilli(s.RealityProbedAt)) <= RealityCFEvidenceMaxAge {
		for _, res := range LandingRealityProbeResults(s) {
			host := realityHost(res.Target)
			if ip := RealityResultCFIP(res); ip != "" && host != "" && out[host] == "" {
				out[host] = realityCFEvidenceText(ip, "落地 "+s.Name+" 测速解析")
			}
		}
	}
	return out
}

// RealityLandingCFEvidence merges RealityServerCFHosts of all active servers
// (host → evidence of the lowest server id).
func (r *Repository) RealityLandingCFEvidence(now time.Time) (map[string]string, error) {
	var rows []model.LandingServer
	if err := r.db.Select("id", "name", "status", "reality_probe", "reality_probed_at", "reality_pins").Where("status = ?", "active").Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i := range rows {
		for host, ev := range RealityServerCFHosts(&rows[i], now) {
			if out[host] == "" {
				out[host] = ev
			}
		}
	}
	return out, nil
}

// ApplyRealityCFVerdicts stores the verdicts of the candidate rows they name:
// a hosted verdict marks the row (evidence refreshed), a known non-hosted one
// lifts the mark; unknown verdicts and the admin's own block are left alone.
func (r *Repository) ApplyRealityCFVerdicts(verdicts map[string]RealityCFVerdict) ([]RealityCFChange, error) {
	if len(verdicts) == 0 {
		return nil, nil
	}
	var rows []model.LandingRealityDomain
	if err := r.db.Select("id", "domain", "cf_hosted", "cf_evidence").Find(&rows).Error; err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	var changes []RealityCFChange
	for _, row := range rows {
		v, ok := verdicts[row.Domain]
		if !ok || !v.Known {
			continue
		}
		evidence := ""
		if v.Hosted {
			evidence = v.Evidence
		}
		if row.CFHosted == v.Hosted && row.CFEvidence == evidence {
			continue
		}
		if err := r.db.Model(&model.LandingRealityDomain{}).Where("id = ?", row.ID).Updates(map[string]any{"cf_hosted": v.Hosted, "cf_evidence": evidence, "updated_time": now}).Error; err != nil {
			return changes, err
		}
		if row.CFHosted != v.Hosted {
			changes = append(changes, RealityCFChange{Domain: row.Domain, Hosted: v.Hosted, Evidence: evidence})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Domain < changes[j].Domain })
	return changes, nil
}

// RealityListeners are a server's live VLESS Reality listeners: landing nodes
// and chain exit copies (plan 250) — count and dest host → count.
type RealityListeners struct {
	Count int
	Dests map[string]int
}

// RealityListenersOnServer are one server's live Reality listeners.
func (r *Repository) RealityListenersOnServer(serverID int64) (RealityListeners, error) {
	m, err := r.realityListeners(serverID)
	if err != nil || m[serverID] == nil {
		return RealityListeners{Dests: map[string]int{}}, err
	}
	return *m[serverID], nil
}

// RealityListenersByServer aggregates the live Reality listeners of every
// server in two queries (landing nodes not being deleted, active chain copies
// of Reality chains).
func (r *Repository) RealityListenersByServer() (map[int64]*RealityListeners, error) {
	return r.realityListeners(0)
}

// realityListeners: serverID 0 = all servers.
func (r *Repository) realityListeners(serverID int64) (map[int64]*RealityListeners, error) {
	out := map[int64]*RealityListeners{}
	add := func(serverID int64, dest string) {
		l := out[serverID]
		if l == nil {
			l = &RealityListeners{Dests: map[string]int{}}
			out[serverID] = l
		}
		l.Count++
		if host := realityHost(dest); host != "" {
			l.Dests[host]++
		}
	}
	var nodes []struct {
		LandingServerID int64
		Dest            string
	}
	nq := r.db.Model(&model.LandingNode{}).Select("landing_server_id", "dest").
		Where("protocol = ? AND status = ? AND delete_pending = ?", "vless_reality_vision", "active", false)
	if serverID > 0 {
		nq = nq.Where("landing_server_id = ?", serverID)
	}
	if err := nq.Scan(&nodes).Error; err != nil {
		return nil, err
	}
	for _, n := range nodes {
		add(n.LandingServerID, n.Dest)
	}
	var chains []struct {
		LandingServerID int64
		Dest            string
	}
	cq := r.db.Table("forward_chain_node AS fcn").Select("fcn.landing_server_id AS landing_server_id, fc.dest AS dest").
		Joins("JOIN forward_chain fc ON fc.id = fcn.chain_id").
		Where("fcn.state = ? AND fc.inbound_protocol <> ?", model.ChainNodeActive, model.ChainInboundAnyTLS)
	if serverID > 0 {
		cq = cq.Where("fcn.landing_server_id = ?", serverID)
	}
	if err := cq.Scan(&chains).Error; err != nil {
		return nil, err
	}
	for _, c := range chains {
		add(c.LandingServerID, c.Dest)
	}
	return out, nil
}

// RealityCFHostedDomains are the candidates marked as hosted on Cloudflare
// (domain → evidence).
func (r *Repository) RealityCFHostedDomains() (map[string]string, error) {
	var rows []model.LandingRealityDomain
	if err := r.db.Select("domain", "cf_evidence").Where("cf_hosted = ?", true).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, d := range rows {
		out[d.Domain] = d.CFEvidence
	}
	return out, nil
}

// RealityCFCheckHosts are the hosts the periodic check looks at: every
// candidate, every server's default dest and every live Reality listener's
// dest (sorted, unique).
func (r *Repository) RealityCFCheckHosts() ([]string, error) {
	seen := map[string]bool{}
	var domains []string
	if err := r.db.Model(&model.LandingRealityDomain{}).Pluck("domain", &domains).Error; err != nil {
		return nil, err
	}
	for _, d := range domains {
		seen[strings.ToLower(d)] = true
	}
	var dests []string
	if err := r.db.Model(&model.LandingServer{}).Where("status = ?", "active").Pluck("reality_dest", &dests).Error; err != nil {
		return nil, err
	}
	for _, d := range dests {
		if h := realityHost(d); h != "" {
			seen[h] = true
		}
	}
	listeners, err := r.RealityListenersByServer()
	if err != nil {
		return nil, err
	}
	for _, l := range listeners {
		for h := range l.Dests {
			seen[h] = true
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// realityProbeCFHosts are the candidates of a 测速 table that the machine
// resolved to a Cloudflare address (host → evidence).
func realityProbeCFHosts(results []RealityProbeResult, serverName string) map[string]string {
	out := map[string]string{}
	for _, res := range results {
		host := realityHost(res.Target)
		if ip := RealityResultCFIP(res); ip != "" && host != "" && out[host] == "" {
			out[host] = realityCFEvidenceText(ip, "落地 "+serverName+" 测速解析")
		}
	}
	return out
}

// withRealityCF adds the Cloudflare hosts of a 测速 table to the blocked set
// of a pick (copy; info.Blocked itself is not changed).
func withRealityCF(blocked map[string]bool, cf map[string]string) map[string]bool {
	if len(cf) == 0 {
		return blocked
	}
	out := make(map[string]bool, len(blocked)+len(cf))
	for h, b := range blocked {
		out[h] = b
	}
	for h := range cf {
		out[h] = true
	}
	return out
}
