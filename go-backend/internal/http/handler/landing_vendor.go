package handler

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"go-backend/internal/http/response"
	"go-backend/internal/landingegress"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Plan 316 「屏蔽本机厂商官网」: every landing server blocks its own VPS
// vendor's sites (内置 row, first plan rule). Admin: the vendor list, the
// global switch and each server's vendor.
// Plan 318: no guessing; the vendor belongs to the node (required, multi) and
// a native landing follows its node; mmwx shares keep their own pick.

// landingVendorAdminView is the 内置 row of the admin 全局规则 card: switch,
// vendor list, every active server with its vendor (native: its node's
// vendors, read-only) and the nodes that still need a vendor (未设置 / 其他).
func (h *Handler) landingVendorAdminView() (map[string]any, error) {
	vs, err := h.repo.GetLandingVendorSettings()
	if err != nil {
		return nil, err
	}
	refs, err := h.repo.LandingServerVendorRefs()
	if err != nil {
		return nil, err
	}
	nodes, err := h.repo.NodeVendorRefs()
	if err != nil {
		return nil, err
	}
	facts, _ := h.repo.LandingNativeNodeFactsAll()
	names := map[string]string{}
	for _, v := range vs.Vendors {
		names[v.Key] = v.Name
	}
	servers := []map[string]any{}
	unset := 0
	for _, s := range refs {
		item := map[string]any{"id": s.ID, "name": s.Name, "source": s.Source, "nodes": s.Nodes, "vendor": "", "vendorName": ""}
		if s.Source == "native" {
			item["nodeId"], item["nodeName"] = s.NodeID, s.NodeName
			item["vendorKeys"], item["vendorOther"] = s.Pick.Keys, s.Pick.Other
			if c, _, ok := landingegress.CombineVendors(vs.Vendors, s.Pick.Keys); ok {
				item["vendor"], item["vendorName"] = c.Key, c.Name
			}
		} else if names[s.Vendor] != "" {
			item["vendor"], item["vendorName"] = s.Vendor, names[s.Vendor]
		} else {
			unset++ // a share without a vendor (native ones count as their node below)
		}
		// SNI / Host match needs the plan-316 agent; mmwx shares and older
		// agents match the requested domain only.
		item["sni"] = s.Source == "native" && facts[s.NodeID].LandingGuard >= landingegress.VendorVersion
		servers = append(servers, item)
	}
	issues := []map[string]any{}
	for _, n := range nodes {
		if _, _, ok := landingegress.CombineVendors(vs.Vendors, n.Pick.Keys); ok && n.Pick.Other == "" {
			continue
		}
		issues = append(issues, map[string]any{"id": n.ID, "name": n.Name, "other": n.Pick.Other})
	}
	unset += len(issues)
	return map[string]any{"on": vs.On, "vendors": vs.Vendors, "custom": vs.Custom, "servers": servers, "nodeIssues": issues, "unset": unset}, nil
}

// landingServerVendorView adds the admin fields of a server card / 设置
// dialog: the global switch, whether its agent also matches SNI / Host, the
// vendor options (shares pick one) and the vendor in force ("" = none).
// Native servers (plan 318) show their node's pick read-only: vendorFromNode,
// vendorNodeName, vendorKeys, vendorOther.
func landingServerVendorView(s model.LandingServer, v map[string]any, catalog landingEgressCatalog) {
	if catalog.vendors == nil {
		return
	}
	vs := catalog.vendors()
	v["vendorOn"] = vs.On
	var facts repo.LandingNativeNodeFacts
	if s.Source == "native" && catalog.nodeFacts != nil {
		facts = catalog.nodeFacts()[s.NodeID]
	}
	v["vendorSni"] = s.Source == "native" && facts.LandingGuard >= landingegress.VendorVersion
	opts := make([]map[string]any, 0, len(vs.Vendors))
	for _, x := range vs.Vendors {
		opts = append(opts, map[string]any{"key": x.Key, "name": x.Name, "domains": x.Domains})
	}
	v["vendorOptions"] = opts
	var vendor landingegress.Vendor
	var ok bool
	if s.Source == "native" {
		v["vendorFromNode"] = true
		v["vendorNodeName"] = facts.Name
		v["vendorKeys"] = facts.VendorPick.Keys
		v["vendorOther"] = facts.VendorPick.Other
		if facts.VendorPick.Keys == nil {
			v["vendorKeys"] = []string{}
		}
		vendor, _, ok = landingegress.CombineVendors(vs.Vendors, facts.VendorPick.Keys)
	} else {
		vendor, ok = landingegress.FindVendor(vs.Vendors, s.Vendor)
	}
	if !ok {
		v["vendor"] = ""
		return
	}
	v["vendor"], v["vendorName"], v["vendorDomains"] = vendor.Key, vendor.Name, vendor.Domains
}

// landingVendorNodeView is the read-only row of one node: its server's vendor,
// the names it blocks and why it is not in force ("global" switched off,
// "unset" no vendor on the server, "pending" the server cannot take rules yet).
func landingVendorNodeView(d *repo.LandingEgressData, ready bool) map[string]any {
	v := map[string]any{"on": false, "sni": d.Server.Source == "native" && d.NodeGuard >= landingegress.VendorVersion}
	if d.VendorInfo != nil {
		v["name"], v["domains"] = d.VendorInfo.Name, d.VendorInfo.Domains
	}
	switch {
	case !d.VendorOn:
		v["offReason"] = "global"
	case d.VendorInfo == nil:
		v["offReason"] = "unset"
	case !ready:
		v["offReason"] = "pending"
	default:
		v["on"] = true
	}
	return v
}

// landingVendorSave stores the vendor list and/or the global switch (absent =
// keep) and re-syncs every landing server once (debounced, like the 防滥用
// switches; unchanged plans are not re-sent).
func (h *Handler) landingVendorSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On      *bool                   `json:"on"`
		Vendors *[]landingegress.Vendor `json:"vendors"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	if req.On == nil && req.Vendors == nil {
		landingError(w, errors.New("请求参数错误"))
		return
	}
	before, err := h.repo.GetLandingVendorSettings()
	if err != nil {
		landingError(w, err)
		return
	}
	changed := false
	if req.Vendors != nil {
		list := *req.Vendors
		// A new row from the editor has no key yet: derive one from its name.
		taken := map[string]bool{}
		for _, v := range list {
			taken[strings.ToLower(strings.TrimSpace(v.Key))] = true
		}
		for i := range list {
			if strings.TrimSpace(list[i].Key) == "" {
				list[i].Key = vendorKeyFor(list[i].Name, taken)
				taken[list[i].Key] = true
			}
		}
		saved, err := h.repo.SaveLandingVendors(list)
		if err != nil {
			landingError(w, err)
			return
		}
		changed = changed || !vendorListsEqual(before.Vendors, saved) || !before.Custom
	}
	if req.On != nil && *req.On != before.On {
		if err := h.repo.SaveLandingVendorSwitch(*req.On); err != nil {
			landingError(w, err)
			return
		}
		changed = true
	}
	if changed {
		h.resyncAllLandingServers("vendor")
	}
	view, err := h.landingGlobalView()
	if err != nil {
		landingError(w, err)
		return
	}
	view["warnings"] = []string{}
	response.WriteJSON(w, response.OK(view))
}

// landingVendorAssign sets one shared server's vendor ("" = 未设置) and syncs
// it. A native server follows its node (plan 318): refused with a hint.
func (h *Handler) landingVendorAssign(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerID int64  `json:"serverId"`
		Vendor   string `json:"vendor"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	old, err := h.repo.GetLandingServer(req.ServerID)
	if err != nil || old == nil {
		landingError(w, errors.New("落地服务器不存在"))
		return
	}
	if err := h.setLandingServerVendor(old.ID, old.Vendor, req.Vendor); err != nil {
		landingError(w, err)
		return
	}
	view, err := h.landingGlobalView()
	if err != nil {
		landingError(w, err)
		return
	}
	view["warnings"] = []string{}
	response.WriteJSON(w, response.OK(view))
}

// setLandingServerVendor stores a changed (shared) vendor and pushes the
// server's plan.
func (h *Handler) setLandingServerVendor(id int64, old, key string) error {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == old {
		return nil
	}
	if err := h.repo.SetLandingServerVendor(id, key); err != nil {
		return err
	}
	log.Printf("[landing-vendor] server=%d vendor %q -> %q", id, old, key)
	h.scheduleLandingLayerApply([]int64{id})
	return nil
}

func (h *Handler) resyncAllLandingServers(why string) {
	servers, err := h.repo.ListLandingServers(0, true)
	if err != nil {
		log.Printf("[landing-%s] resync skipped: %v", why, err)
		return
	}
	all := make([]int64, 0, len(servers))
	for _, s := range servers {
		all = append(all, s.ID)
	}
	log.Printf("[landing-%s] re-syncing %d landing servers", why, len(all))
	h.scheduleLandingLayerApply(all)
}

// vendorKeyFor makes a key from a name: its ASCII letters/digits, else
// "vendor-N".
func vendorKeyFor(name string, taken map[string]bool) string {
	var b strings.Builder
	for _, c := range strings.ToLower(name) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case (c == '-' || c == '_' || c == ' ') && b.Len() > 0:
			b.WriteByte('-')
		}
		if b.Len() >= 24 {
			break
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "vendor"
	}
	key := base
	for i := 2; taken[key]; i++ {
		key = base + "-" + strconv.Itoa(i)
	}
	return key
}

func vendorListsEqual(a, b []landingegress.Vendor) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Name != b[i].Name || strings.Join(a[i].Domains, ",") != strings.Join(b[i].Domains, ",") {
			return false
		}
	}
	return true
}
