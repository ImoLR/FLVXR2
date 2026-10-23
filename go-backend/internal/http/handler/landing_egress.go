package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

type egressDNSEntry struct {
	addrs []netip.Addr
	at    time.Time
}

// egressLookup resolves hostnames for exemption lists and upstream checks.
// Tests replace it to stay offline.
var egressLookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// egressResolve caches answers for 10 minutes and keeps the last good answer
// when a lookup fails, so node exemptions do not flap on DNS hiccups.
func (h *Handler) egressResolve(host string) []netip.Addr {
	h.egressDNSMu.Lock()
	if h.egressDNS == nil {
		h.egressDNS = map[string]egressDNSEntry{}
	}
	entry, ok := h.egressDNS[host]
	h.egressDNSMu.Unlock()
	if ok && time.Since(entry.at) < 10*time.Minute {
		return entry.addrs
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := egressLookup(ctx, host)
	if err != nil {
		// Keep the last good answer; retry after a minute rather than on every
		// compile or page load (each failed lookup can block for 2 s).
		h.egressDNSMu.Lock()
		h.egressDNS[host] = egressDNSEntry{addrs: entry.addrs, at: time.Now().Add(-9 * time.Minute)}
		h.egressDNSMu.Unlock()
		return entry.addrs
	}
	h.egressDNSMu.Lock()
	h.egressDNS[host] = egressDNSEntry{addrs: addrs, at: time.Now()}
	h.egressDNSMu.Unlock()
	return addrs
}

func (h *Handler) landingExemptSources() ([]string, error) {
	hosts, err := h.repo.LandingExemptHosts()
	if err != nil {
		return nil, errors.New("读取 FLVX 节点地址失败")
	}
	whitelist, err := h.repo.GetLandingSourceWhitelist()
	if err != nil {
		return nil, errors.New("读取来源白名单失败")
	}
	return landingegress.ExemptSources(hosts, whitelist, h.egressResolve), nil
}

// landingEgressSupport reports whether a server can take an egress plan and
// which geo data it has.
func landingEgressSupport(d *repo.LandingEgressData) (bool, bool, bool, string) {
	if d.Server.Source == "shared" {
		e := repo.SharedLandingCaps(&d.Server).Egress
		switch {
		case !e.Supports(landingegress.Version):
			return false, false, false, "该共享服务器暂不支持自定义出站"
		case !e.Allowed:
			return false, false, false, "妙妙屋共享方未允许自定义出站与路由"
		}
		return true, e.GeoIP, e.GeoSite, ""
	}
	if d.NodeEgress < landingegress.Version {
		return false, false, false, "节点程序版本过旧，升级后才能使用出站与路由"
	}
	return true, true, true, ""
}

func parseReserved(s string) []int {
	out := []int{}
	for _, part := range strings.Split(s, ",") {
		if v, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			out = append(out, v)
		}
	}
	if len(out) != 3 {
		return nil
	}
	return out
}

// resolveLandingOutbound turns a template into a concrete outbound for one
// server: WARP uses the server's own account, FLVX landing nodes their creds.
func resolveLandingOutbound(d *repo.LandingEgressData, id int64) (landingegress.PlanOutbound, error) {
	o, ok := d.Outbounds[id]
	if !ok {
		return landingegress.PlanOutbound{}, fmt.Errorf("出站模板 %d 已删除", id)
	}
	po := landingegress.PlanOutbound{IPVersion: o.IPVersion}
	s := o.Settings
	switch o.Type {
	case landingegress.TypeDirect:
		po.Type = "direct"
	case landingegress.TypeProxy:
		po.Type, po.Server, po.Port = s.Protocol, s.Server, s.Port
		po.Username, po.Password, po.Method, po.UUID, po.Flow = s.Username, s.Password, s.Method, s.UUID, s.Flow
		if s.Protocol == "vless" {
			po.Reality = &landingegress.PlanReality{ServerName: s.RealityServerName, PublicKey: s.RealityPublicKey, ShortID: s.RealityShortID, Fingerprint: s.RealityFingerprint}
		}
	case landingegress.TypeWarp:
		po.Type = "wireguard"
		if s.WarpMode == "manual" {
			po.WireGuard = &landingegress.PlanWireGuard{PrivateKey: s.PrivateKey, PeerPublicKey: s.PeerPublicKey, Endpoint: s.Endpoint, AddressV4: s.AddressV4, AddressV6: s.AddressV6, Reserved: s.Reserved, MTU: s.MTU}
			break
		}
		a, ok := d.Warp[id]
		if !ok || a.PrivateKey == "" {
			return po, fmt.Errorf("WARP「%s」尚未在本服务器注册成功，使用它的流量暂时封锁", o.Name)
		}
		po.WireGuard = &landingegress.PlanWireGuard{PrivateKey: a.PrivateKey, PeerPublicKey: a.PeerPublicKey, AddressV4: a.AddressV4, AddressV6: a.AddressV6, EndpointV4: a.EndpointV4, EndpointV6: a.EndpointV6, Reserved: parseReserved(a.Reserved), MTU: 1280}
		if (o.IPVersion == "force_ipv4" && a.AddressV4 == "") || (o.IPVersion == "force_ipv6" && a.AddressV6 == "") {
			return po, fmt.Errorf("WARP「%s」账号缺少强制 IP 版本对应的地址", o.Name)
		}
	case landingegress.TypeLandingNode:
		u, ok := d.Upstreams[id]
		switch {
		case !ok:
			return po, fmt.Errorf("出站「%s」的上游节点不可用", o.Name)
		case u.Err != "":
			return po, fmt.Errorf("出站「%s」：%s", o.Name, u.Err)
		case u.ServerID == d.Server.ID:
			return po, fmt.Errorf("出站「%s」不能使用本服务器上的节点作为上游", o.Name)
		case u.Node.Status != "active" || u.Node.DeletePending:
			return po, fmt.Errorf("出站「%s」的上游节点未启用", o.Name)
		case u.Node.Protocol != "ss2022" && u.Node.Protocol != "vless_reality_vision":
			// Plan 121: AnyTLS / Snell / Mieru nodes are client-facing only.
			return po, fmt.Errorf("出站「%s」的上游节点是 %s，暂只支持 SS2022 / VLESS Reality 节点作为上游", o.Name, repo.LandingProtocolName(u.Node.Protocol))
		}
		po.Server, po.Port = landingegress.NormalizeHost(u.Host), u.Port
		if !landingegress.ValidHost(po.Server) {
			return po, fmt.Errorf("出站「%s」的上游地址无效或是内网地址", o.Name)
		}
		if u.Node.Protocol == "ss2022" {
			po.Type, po.Method, po.Password = "shadowsocks", u.Node.Method, u.Node.ServerKey+":"+u.Node.UserKey
		} else {
			names := jsonStrings(u.Node.ServerNames)
			sni := ""
			if len(names) > 0 {
				sni = names[0]
			}
			po.Type, po.UUID, po.Flow = "vless", u.Node.UUID, u.Node.Flow
			po.Reality = &landingegress.PlanReality{ServerName: sni, PublicKey: u.Node.PublicKey, ShortID: u.Node.ShortID, Fingerprint: "chrome"}
		}
		if a, err := netip.ParseAddr(po.Server); err == nil && ((o.IPVersion == "force_ipv4" && !a.Is4()) || (o.IPVersion == "force_ipv6" && a.Is4())) {
			return po, fmt.Errorf("出站「%s」的上游 IP 与强制 IP 版本不一致", o.Name)
		}
	default:
		return po, fmt.Errorf("出站「%s」类型无效", o.Name)
	}
	return po, nil
}

// landingAbuseReady: the server can take plan rules now. A native Core started
// before plan 107 has no RoutingService; sending it the default rules would
// make the agent pause every node until the admin confirms one restart, so the
// defaults wait for readiness (or that confirmation) and the payload stays as
// it was. The mmwx Helper restarts its own Core when needed.
func landingAbuseReady(d *repo.LandingEgressData) bool {
	return d.Server.Source == "shared" || d.Server.EgressReady || d.Server.EgressRestartOK
}

func landingEgressInput(d *repo.LandingEgressData) landingegress.CompileInput {
	in := landingegress.CompileInput{ServerProfileID: d.Server.RouteProfileID, Profiles: d.Profiles, AllowCoreRestart: d.Server.EgressRestartOK}
	in.Abuse = landingAbuseReady(d)
	// Builtin markers (and chain coverage) only for agents that understand
	// them; the mmwx Custom decodes plans strictly and never gets the field.
	if d.Server.Source == "native" && d.NodeGuard >= 1 {
		in.Builtin, in.ChainTags = true, d.ChainTags
	}
	// Plan 134 layers: global layers follow the abuse defaults' readiness;
	// node rules wait for a Core that hot-applies rules (never a restart).
	global := d.Global
	in.Global, in.Layers, in.NodeRules = &global, in.Abuse, landingNodeRulesReady(d)
	in.Markers = d.Server.Source == "native" && d.NodeGuard >= landingegress.LayersVersion
	in.Outbounds, in.NoIPv4, in.NoIPv6 = d.Outbounds, d.NoIPv4, d.NoIPv6
	in.AbuseOff = d.Abuse
	withheld := repo.LandingWithheldFunc(&d.Server)
	for _, n := range d.Nodes {
		// Withheld nodes (plan 121) are not in the desired state either.
		if n.DeletePending || withheld(&n) {
			continue
		}
		rules, def := repo.NodeRulesFromModel(n)
		in.Nodes = append(in.Nodes, landingegress.NodeRef{ID: n.ID, Tag: n.InboundTag, Override: n.RouteProfileID, Rules: rules, Default: def})
	}
	in.Resolve = func(id int64) (landingegress.PlanOutbound, error) { return resolveLandingOutbound(d, id) }
	return in
}

// landingNodeRulesReady: node rules (plan 134) are sent only where a rule
// change is a hot RoutingService update: the mmwx Helper (its FLVX Core got
// the routing API with the first plan) or a native Core that reported it.
// Elsewhere they stay queued (「下次重启生效」) instead of restarting a Core
// that carries other users' connections.
func landingNodeRulesReady(d *repo.LandingEgressData) bool {
	return d.Server.Source == "shared" || d.Server.EgressReady
}

// coveredLandingTags lists inbound tags of nodes on a non-default profile.
func coveredLandingTags(d *repo.LandingEgressData) []string {
	tags := []string{}
	withheld := repo.LandingWithheldFunc(&d.Server)
	for _, n := range d.Nodes {
		if !n.DeletePending && !withheld(&n) && landingegress.EffectiveProfile(d.Server.RouteProfileID, n.RouteProfileID) != 0 {
			tags = append(tags, n.InboundTag)
		}
	}
	return tags
}

// compileLandingEgress returns the plan to send (nil = built-in default or
// unsupported server, i.e. today's payload), compile warnings, and a compile
// error. On error the returned plan blocks the affected nodes (fail closed).
func (h *Handler) compileLandingEgress(serverID int64) (*landingegress.Plan, []string, error) {
	d, err := h.repo.LandingEgressData(serverID)
	if err != nil {
		return nil, nil, errors.New("读取出站与路由设置失败")
	}
	supported, geoIP, geoSite, _ := landingEgressSupport(d)
	if !supported {
		return nil, nil, nil
	}
	in := landingEgressInput(d)
	in.GeoIP, in.GeoSite = geoIP, geoSite
	in.Exempt = h.landingExemptSources
	plan, warnings, err := landingegress.Compile(in)
	if err != nil {
		return landingegress.BlockPlan(coveredLandingTags(d), d.Server.EgressRestartOK), warnings, err
	}
	if !plan.Active() {
		return nil, warnings, nil
	}
	return plan, warnings, nil
}

func planHash(p *landingegress.Plan) string {
	if p == nil {
		return ""
	}
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// validateLandingEgress compiles every affected server against a candidate
// change before it is saved, so limits, geo capabilities and upstream loops
// are rejected at save time instead of at sync time.
func (h *Handler) validateLandingEgress(serverIDs []int64, mutate func(d *repo.LandingEgressData)) error {
	for _, id := range serverIDs {
		d, err := h.repo.LandingEgressData(id)
		if err != nil || d == nil {
			continue
		}
		if mutate != nil {
			mutate(d)
		}
		supported, geoIP, geoSite, _ := landingEgressSupport(d)
		if !supported {
			continue
		}
		in := landingEgressInput(d)
		in.GeoIP, in.GeoSite = geoIP, geoSite
		in.Exempt = func() ([]string, error) { return nil, nil }
		_, rep, err := landingegress.CompileLayers(in)
		if err != nil {
			return fmt.Errorf("落地服务器「%s」：%w", d.Server.Name, err)
		}
		// A change that would push the global rules out of a server is refused
		// (node rules beyond the budget are dropped and shown on the node).
		for _, note := range rep.GlobalNotes {
			if strings.HasPrefix(note, "全局规则未下发：") {
				return fmt.Errorf("落地服务器「%s」：改动后放不下全局规则（%s）", d.Server.Name, strings.TrimPrefix(note, "全局规则未下发："))
			}
		}
	}
	return nil
}

// checkLandingLoops rejects upstream chains that come back to a server.
func checkLandingLoops(edges map[int64][]int64) error {
	state := map[int64]int{}
	var visit func(int64) bool
	visit = func(n int64) bool {
		if state[n] == 1 {
			return true
		}
		if state[n] == 2 {
			return false
		}
		state[n] = 1
		for _, next := range edges[n] {
			if next == n || visit(next) {
				return true
			}
		}
		state[n] = 2
		return false
	}
	keys := make([]int64, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		if visit(k) {
			return errors.New("FLVX 落地节点出站形成了环路（上游最终又回到本服务器），请调整上游或方案")
		}
	}
	return nil
}

// checkUpstreamDNS resolves a proxy upstream once at save time; any private or
// reserved answer is rejected (the agent re-checks when rendering).
func checkUpstreamDNS(host string) error {
	if host == "" {
		return nil
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := egressLookup(ctx, host)
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if landingegress.IsBlockedAddr(a) {
			return errors.New("上游域名解析到内网、回环或保留地址，不能使用")
		}
	}
	return nil
}

// ensureLandingWarp registers missing WARP devices for the given servers (all
// servers when ids is nil) within a time budget. Failures back off per account.
func (h *Handler) ensureLandingWarp(ids []int64, force bool, budget time.Duration) []string {
	h.landingEgressMu.Lock()
	defer h.landingEgressMu.Unlock()
	if ids == nil {
		servers, err := h.repo.ListLandingServers(0, true)
		if err != nil {
			return nil
		}
		for _, s := range servers {
			ids = append(ids, s.ID)
		}
	}
	deadline := time.Now().Add(budget)
	var problems []string
	for _, serverID := range ids {
		d, err := h.repo.LandingEgressData(serverID)
		if err != nil || d == nil {
			continue
		}
		if ok, _, _, _ := landingEgressSupport(d); !ok {
			continue
		}
		needed := map[int64]bool{}
		for _, a := range landingUsedActions(d) {
			if id, ok := landingegress.ActionOutbound(a); ok {
				if o, ok := d.Outbounds[id]; ok && o.Type == landingegress.TypeWarp && o.Settings.WarpMode != "manual" {
					needed[id] = true
				}
			}
		}
		for id := range needed {
			o := d.Outbounds[id]
			licenseHash := repo.LicenseHash(o.Settings.WarpLicense)
			a, has := d.Warp[id]
			if !force && has && a.PrivateKey != "" && a.LicenseHash == licenseHash {
				continue
			}
			if !force && has && a.NextAttemptAt > time.Now().UnixMilli() {
				continue
			}
			if time.Now().After(deadline) {
				problems = append(problems, "WARP 注册未完成，稍后自动重试")
				return problems
			}
			if err := h.registerLandingWarp(d.Server, o, a, has); err != nil {
				problems = append(problems, fmt.Sprintf("%s：%s", d.Server.Name, err.Error()))
			}
		}
	}
	return problems
}

func (h *Handler) registerLandingWarp(s model.LandingServer, o landingegress.Outbound, old model.LandingWarpAccount, hadOld bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	acct, err, licenseErr := landingegress.RegisterWarp(ctx, nil, o.Settings.WarpLicense)
	if err != nil {
		_ = h.repo.RecordLandingWarpFailure(s.ID, o.ID, err.Error())
		return err
	}
	reserved := ""
	if len(acct.Reserved) == 3 {
		reserved = fmt.Sprintf("%d,%d,%d", acct.Reserved[0], acct.Reserved[1], acct.Reserved[2])
	}
	row := model.LandingWarpAccount{LandingServerID: s.ID, OutboundID: o.ID, DeviceID: acct.DeviceID, Token: acct.Token, PrivateKey: acct.PrivateKey, PublicKey: acct.PublicKey, PeerPublicKey: acct.PeerPublicKey, AddressV4: acct.AddressV4, AddressV6: acct.AddressV6, EndpointV4: acct.EndpointV4, EndpointV6: acct.EndpointV6, Reserved: reserved, AccountType: acct.AccountType, LicenseHash: repo.LicenseHash(o.Settings.WarpLicense), Status: "ok"}
	if hadOld {
		row.ID, row.CreatedTime = old.ID, old.CreatedTime
	}
	if licenseErr != nil {
		row.LastError = licenseErr.Error() + "，当前为免费账号"
	}
	if err := h.repo.SaveLandingWarpAccount(&row); err != nil {
		return errors.New("保存 WARP 账号失败")
	}
	if hadOld && old.DeviceID != "" && old.DeviceID != acct.DeviceID {
		delCtx, delCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = landingegress.DeleteWarp(delCtx, nil, old.DeviceID, old.Token)
		delCancel()
	}
	return licenseErr
}

// maybeEnsureLandingWarp runs from the minute reconcile loop but works at most
// every five minutes, one batched pass over all servers.
func (h *Handler) maybeEnsureLandingWarp() {
	if time.Since(h.landingWarpAt) < 5*time.Minute {
		return
	}
	h.landingWarpAt = time.Now()
	h.ensureLandingWarp(nil, false, 60*time.Second)
}

// Views.

type landingEgressCatalog struct {
	outbounds map[int64]landingegress.Outbound
	profiles  map[int64]landingegress.Profile
	// abuse returns the global switches (plan 140), read at most once and
	// only by views that show them (the node list never does).
	abuse func() landingegress.AbuseSwitches
	// settings / nodeFacts feed the outbound labels (plan 139): the global
	// group and the native nodes' capabilities, each read at most once per
	// request; inputs caches the per-server label input.
	settings  func() (landingegress.Global, landingegress.AbuseSwitches)
	nodeFacts func() map[int64]repo.LandingNativeNodeFacts
	inputs    map[int64]landingegress.CompileInput
}

func (h *Handler) landingEgressCatalog() landingEgressCatalog {
	var settingsOnce, factsOnce sync.Once
	var global landingegress.Global
	var switches landingegress.AbuseSwitches
	settings := func() (landingegress.Global, landingegress.AbuseSwitches) {
		settingsOnce.Do(func() { global, switches, _, _ = h.repo.GetLandingGlobalSettings() })
		return global, switches
	}
	var facts map[int64]repo.LandingNativeNodeFacts
	nodeFacts := func() map[int64]repo.LandingNativeNodeFacts {
		factsOnce.Do(func() { facts, _ = h.repo.LandingNativeNodeFactsAll() })
		return facts
	}
	c := landingEgressCatalog{settings: settings, nodeFacts: nodeFacts, inputs: map[int64]landingegress.CompileInput{}}
	c.abuse = func() landingegress.AbuseSwitches { _, sw := settings(); return sw }
	o, p, err := h.repo.LandingEgressCatalog()
	if err != nil {
		c.outbounds, c.profiles = map[int64]landingegress.Outbound{}, map[int64]landingegress.Profile{}
		return c
	}
	c.outbounds, c.profiles = o, p
	return c
}

// labelInput is the compile input of a server minus its nodes, built by the
// same functions as the real compile (landingEgressSupport/landingEgressInput)
// so labels follow the plan's precedence exactly.
func (c landingEgressCatalog) labelInput(s model.LandingServer) landingegress.CompileInput {
	if in, ok := c.inputs[s.ID]; ok && s.ID != 0 {
		return in
	}
	d := &repo.LandingEgressData{Server: s, Outbounds: c.outbounds, Profiles: c.profiles}
	if c.settings != nil {
		d.Global, d.Abuse = c.settings()
	}
	detected := ""
	if s.Source == "native" && c.nodeFacts != nil {
		f := c.nodeFacts()[s.NodeID]
		d.NodeEgress, d.NodeGuard, detected = f.LandingEgress, f.LandingGuard, f.EgressDetected
	}
	d.NoIPv4, d.NoIPv6 = repo.LandingServerFamilies(&s, detected)
	in := landingLabelInput(d)
	if c.inputs != nil && s.ID != 0 {
		c.inputs[s.ID] = in
	}
	return in
}

// landingLabelInput: the compile input labels are resolved with (nodes are
// passed to EffectiveLabel one at a time).
func landingLabelInput(d *repo.LandingEgressData) landingegress.CompileInput {
	supported, geoIP, geoSite, _ := landingEgressSupport(d)
	if !supported {
		// No plan reaches the server: only the profile summary as before.
		return landingegress.CompileInput{ServerProfileID: d.Server.RouteProfileID, Profiles: d.Profiles, Outbounds: d.Outbounds}
	}
	in := landingEgressInput(d)
	in.GeoIP, in.GeoSite = geoIP, geoSite
	in.Nodes, in.Resolve, in.Exempt = nil, nil, nil
	return in
}

// serverLabel: what a node without own settings on this server gets.
func (c landingEgressCatalog) serverLabel(s model.LandingServer) string {
	return c.labelInput(s).EffectiveLabel(nil)
}

// nodeLabel: the node's own effective outbound (its profile override and
// node rules included).
func (c landingEgressCatalog) nodeLabel(s model.LandingServer, n model.LandingNode) string {
	rules, def := repo.NodeRulesFromModel(n)
	return c.labelInput(s).EffectiveLabel(&landingegress.NodeRef{ID: n.ID, Tag: n.InboundTag, Override: n.RouteProfileID, Rules: rules, Default: def})
}

// defaultProfileName names the built-in default profile (id 0) on a server:
// it follows the 全局默认出站 (plan 134), e.g. 「默认 · 强制 IPv4」.
func (c landingEgressCatalog) defaultProfileName(s *model.LandingServer) string {
	if s == nil {
		return "默认（直连）"
	}
	in := c.labelInput(*s)
	in.ServerProfileID = 0
	action, _, _ := in.EffectiveDefault(nil)
	if label := landingegress.ActionLabel(action, c.outbounds); label != "直连" {
		return "默认 · " + label
	}
	return "默认（直连）"
}

// abuseRuleLabels names the default rules switched on globally (plan 140 /
// 143).
func abuseRuleLabels(s landingegress.AbuseSwitches) []string {
	return landingegress.AbuseRuleNames(s)
}

func (c landingEgressCatalog) label(profileID int64) string {
	return landingegress.Label(profileID, c.profiles, c.outbounds)
}

// profileName names a profile; id 0 is the built-in default of server s.
func (c landingEgressCatalog) profileName(id int64, s *model.LandingServer) string {
	if p, ok := c.profiles[id]; ok {
		return p.Name
	}
	return c.defaultProfileName(s)
}

// landingGuardView: the Reality guard (plan 127) of a server for admins.
// state: shared (FLVX builds no Core there) | off | agent_old | idle (on, no
// guarded Reality listener reported) | on | restart (Core needs one confirmed
// restart) | error (rules failed, nodes unguarded).
func landingGuardView(s model.LandingServer, nodeGuard ...int) map[string]any {
	enabled := !s.RealityGuardOff
	state := "idle"
	switch {
	case s.Source == "shared":
		state = "shared"
	case !enabled:
		state = "off"
	case len(nodeGuard) == 0 || nodeGuard[0] < 1:
		state = "agent_old"
	case s.GuardState == "on":
		state = "on"
	case s.GuardState == "restart_required":
		state = "restart"
	case s.GuardState == "error":
		state = "error"
	}
	return map[string]any{"enabled": enabled, "state": state, "nodes": s.GuardNodes}
}

// landingServerEgressView is the admin block of a server card/settings.
func (h *Handler) landingServerEgressView(s model.LandingServer, nodeEgress int, c landingEgressCatalog, nodeGuard ...int) map[string]any {
	supported, reason := true, ""
	if s.Source == "shared" {
		e := repo.SharedLandingCaps(&s).Egress
		switch {
		case !e.Supports(landingegress.Version):
			supported, reason = false, "该共享服务器暂不支持自定义出站"
		case !e.Allowed:
			supported, reason = false, "妙妙屋共享方未允许自定义出站与路由"
		}
	} else if nodeEgress < landingegress.Version {
		supported, reason = false, "节点程序版本过旧，升级后才能使用出站与路由"
	}
	warnings := []string{}
	_ = json.Unmarshal([]byte(s.EgressWarnings), &warnings)
	// Default anti-abuse rules (plan 127): active | off (server profile switched
	// them off) | global_off (plan 140: both switched off globally) | restart
	// (native Core needs one confirmed restart) | unsupported.
	abuse := "active"
	switch {
	case !supported:
		abuse = "unsupported"
	case !landingegress.AbuseOn(s.RouteProfileID, c.profiles):
		abuse = "off"
	case len(abuseRuleLabels(c.abuse())) == 0:
		abuse = "global_off"
	case s.Source == "native" && !s.EgressReady && !s.EgressRestartOK:
		abuse = "restart"
	}
	return map[string]any{
		"profileId": s.RouteProfileID, "profileName": c.profileName(s.RouteProfileID, &s), "label": c.serverLabel(s),
		"supported": supported, "unsupportedReason": reason, "abuseDefaults": abuse, "abuseRules": abuseRuleLabels(c.abuse()), "realityGuard": landingGuardView(s, nodeGuard...),
		"restartRequired":  supported && s.Source == "native" && !s.EgressReady,
		"restartConfirmed": s.EgressRestartOK, "state": s.EgressState, "error": s.EgressError, "warnings": warnings, "checkedAt": s.EgressCheckedAt,
	}
}

func (h *Handler) landingEgressSave(serverIDs []int64) {
	for _, id := range serverIDs {
		_, _ = h.syncLandingServer(id)
	}
}

// landingEgressSaveAsync pushes affected servers without holding the request.
func (h *Handler) landingEgressSaveAsync(serverIDs []int64) {
	if len(serverIDs) == 0 {
		return
	}
	h.egressSyncWG.Add(1)
	go func() {
		defer h.egressSyncWG.Done()
		h.landingEgressSave(serverIDs)
	}()
}

func decodeOutbound(raw json.RawMessage) (landingegress.Outbound, error) {
	var o landingegress.Outbound
	if err := json.Unmarshal(raw, &o); err != nil {
		return o, errors.New("请求参数错误")
	}
	return o, nil
}

func (h *Handler) registerLandingEgress(mux *http.ServeMux) {
	routes := map[string]http.HandlerFunc{
		"overview": h.landingEgressOverview, "outbound/save": h.landingEgressOutboundSave, "outbound/delete": h.landingEgressOutboundDelete,
		"outbound/parse": h.landingEgressParseLink, "profile/save": h.landingEgressProfileSave, "profile/delete": h.landingEgressProfileDelete,
		"whitelist/save": h.landingEgressWhitelistSave, "warp/register": h.landingEgressWarpRegister,
		// Plan 134 global rule group and node-rule caps.
		"global/get": h.landingEgressGlobalGet, "global/save": h.landingEgressGlobalSave, "user-caps/save": h.landingEgressUserCapsSave,
		// Plan 140: built-in rule switches and the preset library (admin
		// writes; the list for everyone is /api/v1/landing/preset/list).
		"builtin/save": h.landingEgressBuiltinSave, "preset/save": h.landingPresetSave,
		"preset/delete": h.landingPresetDelete, "preset/reorder": h.landingPresetReorder, "preset/restore": h.landingPresetRestore,
	}
	for path, fn := range routes {
		fn := fn
		mux.HandleFunc("/api/v1/landing/egress/"+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			if !h.ensureAdminAccess(w, r) {
				return
			}
			fn(w, r)
		})
	}
}

func outboundView(o landingegress.Outbound, m model.LandingOutbound, usage []string) map[string]any {
	return map[string]any{"id": o.ID, "name": o.Name, "type": o.Type, "ipVersion": o.IPVersion, "settings": o.Settings, "remark": o.Remark, "usedBy": usage, "userAllowed": o.UserAllowed, "updatedTime": m.UpdatedTime}
}

func (h *Handler) landingEgressOverview(w http.ResponseWriter, r *http.Request) {
	outs, err := h.repo.ListLandingOutbounds()
	if err != nil {
		landingError(w, err)
		return
	}
	profiles, err := h.repo.ListLandingRouteProfiles()
	if err != nil {
		landingError(w, err)
		return
	}
	usage, err := h.repo.LandingProfileUsage()
	if err != nil {
		landingError(w, err)
		return
	}
	whitelist, _ := h.repo.GetLandingSourceWhitelist()
	hosts, _ := h.repo.LandingExemptHosts()
	nodeAddrs := landingegress.ExemptSources(hosts, nil, h.egressResolve)
	decoded := map[int64]landingegress.Profile{}
	for _, p := range profiles {
		decoded[p.ID] = repo.ProfileFromModel(p)
	}
	outViews := []map[string]any{}
	for _, m := range outs {
		o := repo.OutboundFromModel(m)
		used := []string{}
		for _, p := range profiles {
			if profileUsesOutboundView(decoded[p.ID], o.ID) {
				used = append(used, p.Name)
			}
		}
		outViews = append(outViews, outboundView(o, m, used))
	}
	// Plan 134: global layers and node rules using each template.
	global, _, _ := h.repo.GetLandingGlobal()
	allNodes, _ := h.repo.ListLandingNodes(0, true)
	for _, v := range outViews {
		ref := fmt.Sprintf("outbound:%v", v["id"])
		inGlobal := global.DefaultAction == ref
		for _, rule := range append(append([]landingegress.Rule{}, global.Forced...), global.Default...) {
			inGlobal = inGlobal || rule.Action == ref
		}
		nodeUses := 0
		for _, n := range allNodes {
			rules, def := repo.NodeRulesFromModel(n)
			for _, a := range append(landingegress.Actions(rules), def) {
				if a == ref {
					nodeUses++
					break
				}
			}
		}
		v["usedByGlobal"], v["usedByNodes"] = inGlobal, nodeUses
	}
	globalView, err := h.landingGlobalView()
	if err != nil {
		landingError(w, err)
		return
	}
	profileViews := []map[string]any{}
	for _, m := range profiles {
		p := decoded[m.ID]
		u := usage[m.ID]
		profileViews = append(profileViews, map[string]any{"id": p.ID, "name": p.Name, "defaultAction": p.DefaultAction, "rules": p.Rules, "abuseDefaults": p.AbuseDefaultsEnabled(), "serverCount": u[0], "nodeCount": u[1], "updatedTime": m.UpdatedTime})
	}
	nodes, _ := h.repo.ListLandingNodes(0, true)
	servers, _ := h.repo.ListLandingServers(0, true)
	serverName := map[int64]string{}
	for _, s := range servers {
		serverName[s.ID] = s.Name
	}
	userIDs := []int64{}
	for _, n := range nodes {
		userIDs = append(userIDs, n.UserID)
	}
	names, _ := h.repo.UserNamesByIDs(userIDs)
	nodeChoices := []map[string]any{}
	for _, n := range nodes {
		// Only SS2022 / VLESS nodes can be an upstream (plan 121).
		if landingcustom.IsExtraProtocol(n.Protocol) {
			continue
		}
		nodeChoices = append(nodeChoices, map[string]any{"id": n.ID, "name": n.Name, "serverId": n.LandingServerID, "serverName": serverName[n.LandingServerID], "protocol": n.Protocol, "userName": names[n.UserID], "status": n.Status})
	}
	forwards, _ := h.repo.LandingForwardChoices()
	warp, _ := h.repo.ListLandingWarpAccounts()
	warpViews := []map[string]any{}
	for _, a := range warp {
		warpViews = append(warpViews, map[string]any{"serverId": a.LandingServerID, "serverName": serverName[a.LandingServerID], "outboundId": a.OutboundID, "status": a.Status, "accountType": a.AccountType, "addressV4": a.AddressV4, "addressV6": a.AddressV6, "lastError": a.LastError, "updatedTime": a.UpdatedTime})
	}
	ipCodes, siteCodes := landingegress.GeoCodes()
	sort.Strings(ipCodes)
	sort.Strings(siteCodes)
	response.WriteJSON(w, response.OK(map[string]any{
		"outbounds": outViews, "profiles": profileViews, "whitelist": whitelist,
		"exemptNodeCount": len(nodeAddrs), "exemptPreview": nodeAddrs[:min(len(nodeAddrs), 12)],
		"landingNodes": nodeChoices, "forwards": forwards, "warpAccounts": warpViews,
		"geo":       map[string]any{"release": landingegress.GeoRelease, "geoip": ipCodes, "geosite": siteCodes},
		"ssMethods": landingegress.SSMethods(), "global": globalView,
	}))
}

func profileUsesOutboundView(p landingegress.Profile, id int64) bool {
	ref := "outbound:" + strconv.FormatInt(id, 10)
	if p.DefaultAction == ref {
		return true
	}
	for _, r := range p.Rules {
		if r.Action == ref {
			return true
		}
	}
	return false
}

func (h *Handler) landingEgressOutboundSave(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := landingDecode(r, &raw); err != nil {
		landingError(w, err)
		return
	}
	o, err := decodeOutbound(raw)
	if err != nil {
		landingError(w, err)
		return
	}
	candidate := o
	if err := landingegress.NormalizeOutbound(&candidate); err != nil {
		landingError(w, err)
		return
	}
	if candidate.Type == landingegress.TypeProxy {
		if err := checkUpstreamDNS(candidate.Settings.Server); err != nil {
			landingError(w, err)
			return
		}
	}
	if candidate.Type == landingegress.TypeWarp && candidate.Settings.WarpMode == "manual" && candidate.Settings.Endpoint != "" {
		host, _, _ := net.SplitHostPort(candidate.Settings.Endpoint)
		if err := checkUpstreamDNS(host); err != nil {
			landingError(w, err)
			return
		}
	}
	var affected []int64
	if o.ID > 0 {
		profiles, err := h.repo.LandingProfilesUsingOutbound(o.ID)
		if err != nil {
			landingError(w, err)
			return
		}
		affected, _ = h.repo.LandingServersUsingProfiles(profiles)
		// Global layers and node rules using it (plan 134).
		layered, _ := h.repo.LandingServersUsingLayers(o.ID)
		affected = mergeIDs(affected, layered)
	}
	if candidate.Type == landingegress.TypeLandingNode {
		edges, err := h.repo.LandingUpstreamEdges(0, nil, nil, nil, &candidate)
		if err != nil {
			landingError(w, err)
			return
		}
		if err := checkLandingLoops(edges); err != nil {
			landingError(w, err)
			return
		}
	}
	saved, err := h.repo.SaveLandingOutbound(&o)
	if err != nil {
		landingError(w, err)
		return
	}
	warnings := h.ensureLandingWarp(affected, false, 20*time.Second)
	h.landingEgressSaveAsync(affected)
	out := outboundView(o, *saved, nil)
	out["warnings"] = warnings
	response.WriteJSON(w, response.OK(out))
}

func (h *Handler) landingEgressOutboundDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	var accounts []model.LandingWarpAccount
	all, _ := h.repo.ListLandingWarpAccounts()
	for _, a := range all {
		if a.OutboundID == req.ID {
			accounts = append(accounts, a)
		}
	}
	if err := h.repo.DeleteLandingOutbound(req.ID); err != nil {
		landingError(w, err)
		return
	}
	// Release the Cloudflare devices of deleted WARP templates (best effort).
	for _, a := range accounts {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = landingegress.DeleteWarp(ctx, nil, a.DeviceID, a.Token)
		cancel()
	}
	response.WriteJSON(w, response.OKEmpty())
}

func (h *Handler) landingEgressParseLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Link string `json:"link"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	settings, name, err := landingegress.ParseLink(req.Link)
	if err != nil {
		landingError(w, err)
		return
	}
	o := landingegress.Outbound{Name: name, Type: landingegress.TypeProxy, IPVersion: "system", Settings: settings}
	if o.Name == "" {
		o.Name = "导入的代理"
	}
	if err := landingegress.NormalizeOutbound(&o); err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(map[string]any{"name": o.Name, "settings": o.Settings}))
}

func (h *Handler) landingEgressProfileSave(w http.ResponseWriter, r *http.Request) {
	var p landingegress.Profile
	if err := landingDecode(r, &p); err != nil {
		landingError(w, err)
		return
	}
	outs, _, err := h.repo.LandingEgressCatalog()
	if err != nil {
		landingError(w, err)
		return
	}
	ids := map[int64]bool{}
	for id := range outs {
		ids[id] = true
	}
	candidate := p
	candidate.Rules = append([]landingegress.Rule(nil), p.Rules...)
	if err := landingegress.NormalizeProfile(&candidate, ids); err != nil {
		landingError(w, err)
		return
	}
	var affected []int64
	if p.ID > 0 {
		affected, _ = h.repo.LandingServersUsingProfiles([]int64{p.ID})
		if err := h.validateLandingEgress(affected, func(d *repo.LandingEgressData) {
			if candidate.AbuseDefaults == nil {
				candidate.AbuseDefaults = d.Profiles[candidate.ID].AbuseDefaults
			}
			d.Profiles[candidate.ID] = candidate
		}); err != nil {
			landingError(w, err)
			return
		}
		edges, err := h.repo.LandingUpstreamEdges(0, nil, nil, &candidate, nil)
		if err != nil {
			landingError(w, err)
			return
		}
		if err := checkLandingLoops(edges); err != nil {
			landingError(w, err)
			return
		}
	}
	saved, notes, err := h.repo.SaveLandingRouteProfile(&p)
	if err != nil {
		landingError(w, err)
		return
	}
	warnings := append(notes, h.ensureLandingWarp(affected, false, 20*time.Second)...)
	h.landingEgressSaveAsync(affected)
	view := repo.ProfileFromModel(*saved)
	response.WriteJSON(w, response.OK(map[string]any{"id": view.ID, "name": view.Name, "defaultAction": view.DefaultAction, "rules": view.Rules, "abuseDefaults": view.AbuseDefaultsEnabled(), "warnings": warnings}))
}

func (h *Handler) landingEgressProfileDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if err := h.repo.DeleteLandingRouteProfile(req.ID); err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}

func (h *Handler) landingEgressWhitelistSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Whitelist []string `json:"whitelist"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	list, err := h.repo.SaveLandingSourceWhitelist(req.Whitelist)
	if err != nil {
		landingError(w, err)
		return
	}
	// Plans pick the new exemption up on the next sync (≤ 1 minute); push now.
	servers, _ := h.repo.ListLandingServers(0, true)
	ids := []int64{}
	for _, s := range servers {
		if s.Source == "native" {
			ids = append(ids, s.ID)
		}
	}
	h.landingEgressSaveAsync(ids)
	response.WriteJSON(w, response.OK(map[string]any{"whitelist": list}))
}

func (h *Handler) landingEgressWarpRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerID   int64 `json:"serverId"`
		OutboundID int64 `json:"outboundId"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	d, err := h.repo.LandingEgressData(req.ServerID)
	if err != nil || d == nil {
		landingError(w, errors.New("落地服务器不存在"))
		return
	}
	o, ok := d.Outbounds[req.OutboundID]
	if !ok || o.Type != landingegress.TypeWarp || o.Settings.WarpMode == "manual" {
		landingError(w, errors.New("请选择自动注册的 WARP 出站"))
		return
	}
	h.landingEgressMu.Lock()
	old, has := d.Warp[req.OutboundID]
	err = h.registerLandingWarp(d.Server, o, old, has)
	h.landingEgressMu.Unlock()
	if err != nil && !strings.Contains(err.Error(), "许可证") {
		landingError(w, err)
		return
	}
	_, _ = h.syncLandingServer(req.ServerID)
	a, _ := h.repo.GetLandingWarpAccount(req.ServerID, req.OutboundID)
	view := map[string]any{"status": "ok"}
	if a != nil {
		view = map[string]any{"status": a.Status, "accountType": a.AccountType, "addressV4": a.AddressV4, "addressV6": a.AddressV6, "lastError": a.LastError}
	}
	response.WriteJSON(w, response.OK(view))
}

// validateServerEgressAssignment checks a server profile change.
func (h *Handler) validateServerEgressAssignment(s model.LandingServer, profileID int64, confirmRestart bool) (bool, error) {
	if profileID == s.RouteProfileID {
		return false, nil
	}
	if profileID < 0 {
		return false, errors.New("路由方案无效")
	}
	if profileID > 0 {
		p, err := h.repo.GetLandingRouteProfile(profileID)
		if err != nil || p == nil {
			return false, errors.New("路由方案不存在")
		}
	}
	d, err := h.repo.LandingEgressData(s.ID)
	if err != nil {
		return false, errors.New("读取落地服务器失败")
	}
	supported, _, _, reason := landingEgressSupport(d)
	if !supported && profileID != 0 {
		return false, errors.New(reason)
	}
	if err := h.validateLandingEgress([]int64{s.ID}, func(d *repo.LandingEgressData) { d.Server.RouteProfileID = profileID }); err != nil {
		return false, err
	}
	edges, err := h.repo.LandingUpstreamEdges(s.ID, &profileID, nil, nil, nil)
	if err != nil {
		return false, err
	}
	if err := checkLandingLoops(edges); err != nil {
		return false, err
	}
	needRestart := profileID != 0 && s.Source == "native" && !s.EgressReady
	if needRestart && !confirmRestart {
		return false, errors.New("该服务器的落地 Core 需要重启一次才能启用出站与路由，请勾选确认后再保存")
	}
	return needRestart, nil
}

// validateNodeEgressOverride checks a node profile override change.
func (h *Handler) validateNodeEgressOverride(n *model.LandingNode, profileID *int64) error {
	if profileID != nil && *profileID < 0 {
		return errors.New("路由方案无效")
	}
	if profileID != nil && *profileID > 0 {
		p, err := h.repo.GetLandingRouteProfile(*profileID)
		if err != nil || p == nil {
			return errors.New("路由方案不存在")
		}
	}
	d, err := h.repo.LandingEgressData(n.LandingServerID)
	if err != nil {
		return errors.New("读取落地服务器失败")
	}
	effective := landingegress.EffectiveProfile(d.Server.RouteProfileID, profileID)
	supported, _, _, reason := landingEgressSupport(d)
	if !supported && effective != 0 {
		return errors.New(reason)
	}
	if effective != 0 && d.Server.Source == "native" && !d.Server.EgressReady && !d.Server.EgressRestartOK {
		return errors.New("请先在落地服务器设置中确认重启落地 Core，再为节点单独指定路由方案")
	}
	if err := h.validateLandingEgress([]int64{n.LandingServerID}, func(d *repo.LandingEgressData) {
		for i := range d.Nodes {
			if d.Nodes[i].ID == n.ID {
				d.Nodes[i].RouteProfileID = profileID
			}
		}
	}); err != nil {
		return err
	}
	edges, err := h.repo.LandingUpstreamEdges(0, nil, map[int64]*int64{n.ID: profileID}, nil, nil)
	if err != nil {
		return err
	}
	return checkLandingLoops(edges)
}

// mergeLandingEgressWarnings records compile warnings for shared servers
// between Custom status reports.
func (h *Handler) mergeLandingEgressWarnings(serverID int64, warnings []string) {
	s, err := h.repo.GetLandingServer(serverID)
	if err != nil || s == nil {
		return
	}
	_ = h.repo.RecordLandingEgressStatus(serverID, s.EgressState, s.EgressError, s.EgressReady, warnings)
}
