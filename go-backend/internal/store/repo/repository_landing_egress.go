package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OutboundFromModel decodes a stored template.
func OutboundFromModel(m model.LandingOutbound) landingegress.Outbound {
	o := landingegress.Outbound{ID: m.ID, Name: m.Name, Type: m.Type, IPVersion: m.IPVersion, Remark: m.Remark}
	_ = json.Unmarshal([]byte(m.Settings), &o.Settings)
	return o
}

// ProfileFromModel decodes a stored routing profile.
func ProfileFromModel(m model.LandingRouteProfile) landingegress.Profile {
	on := !m.AbuseDefaultsOff
	p := landingegress.Profile{ID: m.ID, Name: m.Name, DefaultAction: m.DefaultAction, Rules: []landingegress.Rule{}, AbuseDefaults: &on}
	_ = json.Unmarshal([]byte(m.Rules), &p.Rules)
	return p
}

func (r *Repository) ListLandingOutbounds() ([]model.LandingOutbound, error) {
	out := []model.LandingOutbound{}
	err := r.db.Order("id ASC").Find(&out).Error
	return out, err
}

func (r *Repository) ListLandingRouteProfiles() ([]model.LandingRouteProfile, error) {
	out := []model.LandingRouteProfile{}
	err := r.db.Order("id ASC").Find(&out).Error
	return out, err
}

// LandingEgressCatalog loads all templates and profiles once (list pages,
// labels and compilation share it; both tables are small).
func (r *Repository) LandingEgressCatalog() (map[int64]landingegress.Outbound, map[int64]landingegress.Profile, error) {
	outs, err := r.ListLandingOutbounds()
	if err != nil {
		return nil, nil, err
	}
	profiles, err := r.ListLandingRouteProfiles()
	if err != nil {
		return nil, nil, err
	}
	om := map[int64]landingegress.Outbound{}
	for _, o := range outs {
		om[o.ID] = OutboundFromModel(o)
	}
	pm := map[int64]landingegress.Profile{}
	for _, p := range profiles {
		pm[p.ID] = ProfileFromModel(p)
	}
	return om, pm, nil
}

func outboundIDs(tx *gorm.DB) (map[int64]bool, error) {
	var ids []int64
	if err := tx.Model(&model.LandingOutbound{}).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// SaveLandingOutbound validates and stores a template. FLVX landing-node
// upstreams must exist and an optional rule must target that node.
func (r *Repository) SaveLandingOutbound(o *landingegress.Outbound) (*model.LandingOutbound, error) {
	if err := landingegress.NormalizeOutbound(o); err != nil {
		return nil, err
	}
	var saved model.LandingOutbound
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if o.Type == landingegress.TypeLandingNode {
			var n model.LandingNode
			if err := tx.First(&n, o.Settings.LandingNodeID).Error; err != nil {
				return errors.New("上游 FLVX 落地节点不存在")
			}
			if o.Settings.ViaForwardID > 0 {
				var f model.Forward
				if err := tx.First(&f, o.Settings.ViaForwardID).Error; err != nil || f.LandingNodeID != n.ID {
					return errors.New("所选规则的目标不是该落地节点")
				}
			}
		}
		var count int64
		if err := tx.Model(&model.LandingOutbound{}).Where("name = ? AND id <> ?", o.Name, o.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("出站名称已存在")
		}
		now := time.Now().UnixMilli()
		raw, _ := json.Marshal(o.Settings)
		if o.ID > 0 {
			if err := tx.First(&saved, o.ID).Error; err != nil {
				return errors.New("出站不存在")
			}
			if saved.Type != o.Type {
				// Rules keep pointing at the template ID; a type change would
				// silently change what those rules do.
				return errors.New("出站类型创建后不能修改，请新建出站")
			}
		} else {
			saved.CreatedTime = now
		}
		saved.Name, saved.Type, saved.IPVersion, saved.Settings, saved.Remark, saved.UpdatedTime = o.Name, o.Type, o.IPVersion, string(raw), o.Remark, now
		if err := tx.Save(&saved).Error; err != nil {
			return err
		}
		o.ID = saved.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func profileUsesOutbound(p landingegress.Profile, id int64) bool {
	ref := "outbound:" + strconv.FormatInt(id, 10)
	if p.DefaultAction == ref {
		return true
	}
	for _, rule := range p.Rules {
		if rule.Action == ref {
			return true
		}
	}
	return false
}

func (r *Repository) DeleteLandingOutbound(id int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var profiles []model.LandingRouteProfile
		if err := tx.Find(&profiles).Error; err != nil {
			return err
		}
		for _, m := range profiles {
			if profileUsesOutbound(ProfileFromModel(m), id) {
				return fmt.Errorf("该出站正被路由方案「%s」使用，不能删除", m.Name)
			}
		}
		res := tx.Delete(&model.LandingOutbound{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errors.New("出站不存在")
		}
		return tx.Where("outbound_id = ?", id).Delete(&model.LandingWarpAccount{}).Error
	})
}

// SaveLandingRouteProfile validates and stores a profile. Landing nodes are
// created and deleted by users (expired nodes are removed automatically), so a
// rule's 适用落地节点 may name deleted nodes: they are dropped, and a rule left
// without any of its nodes is disabled rather than widened to every node. The
// returned notes tell the admin which rules were changed that way.
func (r *Repository) SaveLandingRouteProfile(p *landingegress.Profile) (*model.LandingRouteProfile, []string, error) {
	var saved model.LandingRouteProfile
	var notes []string
	err := r.db.Transaction(func(tx *gorm.DB) error {
		ids, err := outboundIDs(tx)
		if err != nil {
			return err
		}
		if err := landingegress.NormalizeProfile(p, ids); err != nil {
			return err
		}
		nodeIDs := map[int64]bool{}
		for _, rule := range p.Rules {
			for _, id := range rule.LandingNodeIDs {
				nodeIDs[id] = true
			}
		}
		if len(nodeIDs) > 0 {
			list := make([]int64, 0, len(nodeIDs))
			for id := range nodeIDs {
				list = append(list, id)
			}
			var existing []int64
			if err := tx.Model(&model.LandingNode{}).Where("id IN ?", list).Pluck("id", &existing).Error; err != nil {
				return err
			}
			alive := map[int64]bool{}
			for _, id := range existing {
				alive[id] = true
			}
			for i := range p.Rules {
				rule := &p.Rules[i]
				if len(rule.LandingNodeIDs) == 0 {
					continue
				}
				kept := []int64{}
				for _, id := range rule.LandingNodeIDs {
					if alive[id] {
						kept = append(kept, id)
					}
				}
				switch {
				case len(kept) == len(rule.LandingNodeIDs):
					continue
				case len(kept) == 0:
					// An empty scope means "all nodes": never widen a rule.
					rule.LandingNodeIDs, rule.Enabled = []int64{}, false
					notes = append(notes, fmt.Sprintf("规则「%s」的适用落地节点都已删除，已停用该规则", rule.Name))
				default:
					rule.LandingNodeIDs = kept
					notes = append(notes, fmt.Sprintf("规则「%s」中已删除的落地节点已移除", rule.Name))
				}
			}
		}
		var count int64
		if err := tx.Model(&model.LandingRouteProfile{}).Where("name = ? AND id <> ?", p.Name, p.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("方案名称已存在")
		}
		now := time.Now().UnixMilli()
		if p.ID > 0 {
			if err := tx.First(&saved, p.ID).Error; err != nil {
				return errors.New("路由方案不存在")
			}
		} else {
			saved.CreatedTime = now
		}
		raw, _ := json.Marshal(p.Rules)
		saved.Name, saved.DefaultAction, saved.Rules, saved.UpdatedTime = p.Name, p.DefaultAction, string(raw), now
		if p.AbuseDefaults != nil {
			saved.AbuseDefaultsOff = !*p.AbuseDefaults
		}
		on := !saved.AbuseDefaultsOff
		p.AbuseDefaults = &on
		if err := tx.Save(&saved).Error; err != nil {
			return err
		}
		p.ID = saved.ID
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return &saved, notes, nil
}

func (r *Repository) DeleteLandingRouteProfile(id int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.LandingServer{}).Where("route_profile_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("该方案正被 %d 台落地服务器使用，请先改用其他方案", count)
		}
		if err := tx.Model(&model.LandingNode{}).Where("route_profile_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("该方案正被 %d 个节点单独使用，请先改为跟随服务器", count)
		}
		res := tx.Delete(&model.LandingRouteProfile{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errors.New("路由方案不存在")
		}
		return nil
	})
}

// LandingProfileUsage counts servers and node overrides per profile.
func (r *Repository) LandingProfileUsage() (map[int64][2]int, error) {
	out := map[int64][2]int{}
	type row struct {
		ID    int64
		Count int
	}
	var servers, nodes []row
	if err := r.db.Model(&model.LandingServer{}).Select("route_profile_id AS id, COUNT(*) AS count").Where("route_profile_id > 0").Group("route_profile_id").Scan(&servers).Error; err != nil {
		return nil, err
	}
	if err := r.db.Model(&model.LandingNode{}).Select("route_profile_id AS id, COUNT(*) AS count").Where("route_profile_id IS NOT NULL AND route_profile_id > 0").Group("route_profile_id").Scan(&nodes).Error; err != nil {
		return nil, err
	}
	for _, s := range servers {
		v := out[s.ID]
		v[0] = s.Count
		out[s.ID] = v
	}
	for _, n := range nodes {
		v := out[n.ID]
		v[1] = n.Count
		out[n.ID] = v
	}
	return out, nil
}

func (r *Repository) GetLandingSourceWhitelist() ([]string, error) {
	var s model.LandingEgressSetting
	err := r.db.First(&s, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	_ = json.Unmarshal([]byte(s.SourceWhitelist), &out)
	return out, nil
}

func (r *Repository) SaveLandingSourceWhitelist(list []string) ([]string, error) {
	list, err := landingegress.NormalizeWhitelist(list)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(list)
	s := model.LandingEgressSetting{ID: 1, SourceWhitelist: string(raw), UpdatedTime: time.Now().UnixMilli()}
	err = r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"source_whitelist", "updated_time"})}).Create(&s).Error
	return list, err
}

// LandingExemptHosts returns every address field of every FLVX node; the
// handler resolves hostnames (cached) and adds the whitelist.
func (r *Repository) LandingExemptHosts() ([]string, error) {
	var nodes []model.Node
	if err := r.db.Select("server_ip", "server_ip_v4", "server_ip_v6", "intranet_ip", "extra_ips").Find(&nodes).Error; err != nil {
		return nil, err
	}
	out := []string{}
	for _, n := range nodes {
		out = append(out, n.ServerIP, n.ServerIPV4.String, n.ServerIPV6.String, n.IntranetIP.String)
		var extra []string
		if json.Unmarshal([]byte(n.ExtraIPs.String), &extra) == nil {
			out = append(out, extra...)
		} else {
			out = append(out, n.ExtraIPs.String)
		}
	}
	return out, nil
}

// LandingUpstream is a resolved FLVX landing-node upstream for one template.
type LandingUpstream struct {
	Node     model.LandingNode
	ServerID int64
	Host     string
	Port     int
	Err      string
}

// LandingEgressData is everything Compile needs for one server.
type LandingEgressData struct {
	Server     model.LandingServer
	Nodes      []model.LandingNode
	Outbounds  map[int64]landingegress.Outbound
	Profiles   map[int64]landingegress.Profile
	Warp       map[int64]model.LandingWarpAccount
	Upstreams  map[int64]LandingUpstream
	NodeEgress int // native agent capability (0 = unsupported)
	NodeGuard  int // native agent: Reality guard + builtin rules (plan 127)
	// ChainTags: chain copies on this server (plan 108), native only.
	ChainTags []string
}

func (r *Repository) LandingEgressData(serverID int64) (*LandingEgressData, error) {
	d := &LandingEgressData{Warp: map[int64]model.LandingWarpAccount{}, Upstreams: map[int64]LandingUpstream{}}
	if err := r.db.First(&d.Server, serverID).Error; err != nil {
		return nil, err
	}
	if err := r.db.Where("landing_server_id = ?", serverID).Order("id").Find(&d.Nodes).Error; err != nil {
		return nil, err
	}
	var err error
	if d.Outbounds, d.Profiles, err = r.LandingEgressCatalog(); err != nil {
		return nil, err
	}
	var accounts []model.LandingWarpAccount
	if err := r.db.Where("landing_server_id = ?", serverID).Find(&accounts).Error; err != nil {
		return nil, err
	}
	for _, a := range accounts {
		d.Warp[a.OutboundID] = a
	}
	if d.Server.Source == "native" {
		var n model.Node
		if err := r.db.Select("id", "landing_egress", "landing_chain", "landing_guard").First(&n, d.Server.NodeID).Error; err == nil {
			d.NodeEgress, d.NodeGuard = n.LandingEgress, n.LandingGuard
			if n.LandingChain >= 1 {
				// Exactly the copies the desired state carries (syncLandingServer).
				copies, err := r.ChainCopiesForServer(serverID)
				if err != nil {
					return nil, err
				}
				for _, c := range copies {
					d.ChainTags = append(d.ChainTags, fmt.Sprintf("flvxl-%d", model.ChainAgentIDBase+c.Node.ID))
				}
			}
		}
	}
	// Resolve upstreams only for templates this server actually uses.
	used := map[int64]bool{}
	for _, n := range d.Nodes {
		pid := landingegress.EffectiveProfile(d.Server.RouteProfileID, n.RouteProfileID)
		p, ok := d.Profiles[pid]
		if pid == 0 || !ok {
			continue
		}
		for _, a := range append([]string{p.DefaultAction}, ruleActions(p)...) {
			if id, ok := landingegress.ActionOutbound(a); ok {
				used[id] = true
			}
		}
	}
	for id := range used {
		if o, ok := d.Outbounds[id]; ok && o.Type == landingegress.TypeLandingNode {
			d.Upstreams[id] = r.landingUpstream(o.Settings.LandingNodeID, o.Settings.ViaForwardID)
		}
	}
	return d, nil
}

func ruleActions(p landingegress.Profile) []string {
	out := []string{}
	for _, rule := range p.Rules {
		if rule.Enabled {
			out = append(out, rule.Action)
		}
	}
	return out
}

func (r *Repository) landingUpstream(nodeID, forwardID int64) LandingUpstream {
	u := LandingUpstream{}
	if err := r.db.First(&u.Node, nodeID).Error; err != nil {
		u.Err = "上游 FLVX 落地节点已删除"
		return u
	}
	u.ServerID = u.Node.LandingServerID
	var s model.LandingServer
	if err := r.db.First(&s, u.Node.LandingServerID).Error; err != nil {
		u.Err = "上游落地服务器不存在"
		return u
	}
	u.Host, u.Port = strings.Trim(s.PublicHost, "[]"), u.Node.Port
	if forwardID <= 0 {
		return u
	}
	var f model.Forward
	if err := r.db.First(&f, forwardID).Error; err != nil || f.LandingNodeID != nodeID {
		u.Err = "经由的规则已删除或不再指向该落地节点"
		return u
	}
	entries, err := r.forwardEntries(f)
	if err != nil || len(entries) == 0 {
		u.Err = "经由的规则没有可用入口"
		return u
	}
	// The first entry is the rule's primary entry, as in the landing links.
	u.Host, u.Port = strings.Trim(entries[0].Host, "[]"), entries[0].Port
	return u
}

// LandingForwardChoices lists rules that target landing nodes (for the
// "经由 FLVX 规则" picker), with their entry addresses.
func (r *Repository) LandingForwardChoices() ([]map[string]any, error) {
	var forwards []model.Forward
	if err := r.db.Where("landing_node_id > 0").Order("id").Find(&forwards).Error; err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, f := range forwards {
		entries, err := r.forwardEntries(f)
		if err != nil {
			return nil, err
		}
		addrs := []string{}
		for _, e := range entries {
			addrs = append(addrs, net.JoinHostPort(strings.Trim(e.Host, "[]"), strconv.Itoa(e.Port)))
		}
		out = append(out, map[string]any{"id": f.ID, "name": f.Name, "landingNodeId": f.LandingNodeID, "entries": addrs})
	}
	return out, nil
}

// TouchLandingEgress advances the revision when the compiled plan changed, so
// agents never see one revision with two contents.
func (r *Repository) TouchLandingEgress(serverID int64, hash string) (int64, error) {
	var rev int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var s model.LandingServer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, serverID).Error; err != nil {
			return err
		}
		rev = s.Revision
		if s.EgressHash == hash {
			return nil
		}
		rev = s.Revision + 1
		return tx.Model(&s).Updates(map[string]any{"egress_hash": hash, "revision": rev}).Error
	})
	return rev, err
}

// RecordLandingEgressStatus stores the agent/Custom egress report. Syncs repeat
// every minute, so it writes only when the report changed (egress_checked_at is
// the time of the last change). A ready Core consumes a pending one-time restart
// confirmation, so a later loss of readiness asks the admin again.
func (r *Repository) RecordLandingEgressStatus(serverID int64, state, errText string, ready bool, warnings []string) error {
	raw := ""
	if len(warnings) > 0 {
		b, _ := json.Marshal(warnings)
		raw = string(b)
	}
	err := r.db.Model(&model.LandingServer{}).
		Where("id = ? AND (egress_state <> ? OR egress_error <> ? OR egress_ready <> ? OR egress_warnings <> ?)", serverID, state, errText, ready, raw).
		Updates(map[string]any{"egress_state": state, "egress_error": errText, "egress_ready": ready, "egress_warnings": raw, "egress_checked_at": time.Now().UnixMilli()}).Error
	if err != nil || !ready {
		return err
	}
	return r.db.Model(&model.LandingServer{}).Where("id = ? AND egress_restart_ok = ?", serverID, true).Update("egress_restart_ok", false).Error
}
func (r *Repository) SetLandingServerEgress(serverID, profileID int64, restartOK bool) error {
	values := map[string]any{"route_profile_id": profileID, "updated_time": time.Now().UnixMilli()}
	if restartOK {
		values["egress_restart_ok"] = true
	}
	return r.db.Model(&model.LandingServer{}).Where("id = ?", serverID).Updates(values).Error
}

func (r *Repository) SetLandingNodeRouteProfile(nodeID int64, profileID *int64) error {
	return r.db.Model(&model.LandingNode{}).Where("id = ?", nodeID).Updates(map[string]any{"route_profile_id": profileID, "updated_time": time.Now().UnixMilli()}).Error
}

func (r *Repository) GetLandingRouteProfile(id int64) (*model.LandingRouteProfile, error) {
	var p model.LandingRouteProfile
	err := r.db.First(&p, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &p, err
}

// LandingServersUsingProfiles lists servers whose server profile or node
// overrides reference any of the given profiles.
func (r *Repository) LandingServersUsingProfiles(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var a, b []int64
	if err := r.db.Model(&model.LandingServer{}).Where("route_profile_id IN ?", ids).Pluck("id", &a).Error; err != nil {
		return nil, err
	}
	if err := r.db.Model(&model.LandingNode{}).Where("route_profile_id IN ?", ids).Distinct().Pluck("landing_server_id", &b).Error; err != nil {
		return nil, err
	}
	set := map[int64]bool{}
	for _, id := range append(a, b...) {
		set[id] = true
	}
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// LandingProfilesUsingOutbound lists profile IDs referencing a template.
func (r *Repository) LandingProfilesUsingOutbound(id int64) ([]int64, error) {
	_, profiles, err := r.LandingEgressCatalog()
	if err != nil {
		return nil, err
	}
	out := []int64{}
	for pid, p := range profiles {
		if profileUsesOutbound(p, id) {
			out = append(out, pid)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// LandingUpstreamEdges maps server → servers it chains to via FLVX
// landing-node outbounds (for loop detection).
func (r *Repository) LandingUpstreamEdges(overrideServer int64, overrideProfile *int64, nodeOverride map[int64]*int64, profileOverride *landingegress.Profile, outboundOverride *landingegress.Outbound) (map[int64][]int64, error) {
	outs, profiles, err := r.LandingEgressCatalog()
	if err != nil {
		return nil, err
	}
	if profileOverride != nil {
		profiles[profileOverride.ID] = *profileOverride
	}
	if outboundOverride != nil {
		outs[outboundOverride.ID] = *outboundOverride
	}
	var servers []model.LandingServer
	if err := r.db.Select("id", "route_profile_id").Find(&servers).Error; err != nil {
		return nil, err
	}
	var nodes []model.LandingNode
	if err := r.db.Select("id", "landing_server_id", "route_profile_id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	nodeServer := map[int64]int64{}
	used := map[int64]map[int64]bool{}
	mark := func(server, profile int64) {
		if profile == 0 {
			return
		}
		if used[server] == nil {
			used[server] = map[int64]bool{}
		}
		used[server][profile] = true
	}
	serverProfile := map[int64]int64{}
	for _, s := range servers {
		pid := s.RouteProfileID
		if s.ID == overrideServer && overrideProfile != nil {
			pid = *overrideProfile
		}
		serverProfile[s.ID] = pid
	}
	for _, n := range nodes {
		nodeServer[n.ID] = n.LandingServerID
		override := n.RouteProfileID
		if v, ok := nodeOverride[n.ID]; ok {
			override = v
		}
		mark(n.LandingServerID, landingegress.EffectiveProfile(serverProfile[n.LandingServerID], override))
	}
	edges := map[int64][]int64{}
	for server, set := range used {
		for pid := range set {
			p, ok := profiles[pid]
			if !ok {
				continue
			}
			actions := []string{p.DefaultAction}
			for _, rule := range p.Rules {
				if rule.Enabled {
					actions = append(actions, rule.Action)
				}
			}
			for _, a := range actions {
				id, ok := landingegress.ActionOutbound(a)
				if !ok {
					continue
				}
				o, ok := outs[id]
				if !ok || o.Type != landingegress.TypeLandingNode {
					continue
				}
				target := nodeServer[o.Settings.LandingNodeID]
				if target == 0 {
					var n model.LandingNode
					if err := r.db.Select("landing_server_id").First(&n, o.Settings.LandingNodeID).Error; err == nil {
						target = n.LandingServerID
					}
				}
				if target != 0 {
					edges[server] = append(edges[server], target)
				}
			}
		}
	}
	return edges, nil
}

// WARP accounts.

func (r *Repository) GetLandingWarpAccount(serverID, outboundID int64) (*model.LandingWarpAccount, error) {
	var a model.LandingWarpAccount
	err := r.db.Where("landing_server_id = ? AND outbound_id = ?", serverID, outboundID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &a, err
}

func (r *Repository) ListLandingWarpAccounts() ([]model.LandingWarpAccount, error) {
	out := []model.LandingWarpAccount{}
	err := r.db.Order("id").Find(&out).Error
	return out, err
}

func LicenseHash(license string) string {
	if license == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(license))
	return hex.EncodeToString(sum[:8])
}

// SaveLandingWarpAccount upserts the account of (server, outbound).
func (r *Repository) SaveLandingWarpAccount(a *model.LandingWarpAccount) error {
	now := time.Now().UnixMilli()
	a.UpdatedTime = now
	if a.CreatedTime == 0 {
		a.CreatedTime = now
	}
	return r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "landing_server_id"}, {Name: "outbound_id"}}, UpdateAll: true}).Create(a).Error
}

// RecordLandingWarpFailure keeps any working keys and schedules a retry with
// exponential backoff (1 min … 1 h).
func (r *Repository) RecordLandingWarpFailure(serverID, outboundID int64, errText string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var a model.LandingWarpAccount
		err := tx.Where("landing_server_id = ? AND outbound_id = ?", serverID, outboundID).First(&a).Error
		now := time.Now().UnixMilli()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			a = model.LandingWarpAccount{LandingServerID: serverID, OutboundID: outboundID, Status: "error", CreatedTime: now}
		} else if err != nil {
			return err
		}
		a.Failures++
		backoff := time.Minute << min(a.Failures-1, 6)
		if backoff > time.Hour {
			backoff = time.Hour
		}
		a.LastError, a.NextAttemptAt, a.UpdatedTime = errText, now+backoff.Milliseconds(), now
		if a.PrivateKey == "" {
			a.Status = "error"
		}
		return tx.Save(&a).Error
	})
}

func (r *Repository) DeleteLandingWarpAccount(serverID, outboundID int64) error {
	return r.db.Where("landing_server_id = ? AND outbound_id = ?", serverID, outboundID).Delete(&model.LandingWarpAccount{}).Error
}

// SetNodeLandingCapabilities records the landing capability flags with one
// conditional statement per agent report. versions are the chain-node schema
// version (plan 108) and the Reality guard / builtin-rule version (plan 127);
// agents that do not report them are version 0.
func (r *Repository) SetNodeLandingCapabilities(id int64, capable bool, egress int, versions ...int) error {
	chainVersion, guardVersion := 0, 0
	if len(versions) > 0 {
		chainVersion = versions[0]
	}
	if len(versions) > 1 {
		guardVersion = versions[1]
	}
	return r.db.Model(&model.Node{}).Where("id = ? AND (landing_capable <> ? OR landing_egress <> ? OR landing_chain <> ? OR landing_guard <> ?)", id, capable, egress, chainVersion, guardVersion).Updates(map[string]any{"landing_capable": capable, "landing_egress": egress, "landing_chain": chainVersion, "landing_guard": guardVersion}).Error
}

// UserNamesByIDs maps user IDs to login names in one query.
func (r *Repository) UserNamesByIDs(ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var users []model.User
	if err := r.db.Select("id", "user").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = u.User
	}
	return out, nil
}
