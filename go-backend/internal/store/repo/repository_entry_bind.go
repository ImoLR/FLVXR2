package repo

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"go-backend/internal/store/model"
)

// Plan 152: a rule may be bound to some of its tunnel's entries
// (forward.entry_node_ids), and tunnel names may carry the {入口} token that
// is rendered per viewer from the entry nodes' 入口显示名 (node.entry_alias).

// TunnelEntryToken is the placeholder in tunnel names.
const TunnelEntryToken = "{入口}"

// HasTunnelEntryToken reports whether a raw tunnel name needs rendering.
func HasTunnelEntryToken(name string) bool { return strings.Contains(name, TunnelEntryToken) }

// ParseEntryNodeIDs reads a binding CSV: positive ids, first occurrence order.
func ParseEntryNodeIDs(csv string) []int64 {
	out := make([]int64, 0, 2)
	seen := make(map[int64]bool)
	for _, part := range strings.Split(csv, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// FormatEntryNodeIDs writes a binding CSV (” for none).
func FormatEntryNodeIDs(ids []int64) string {
	parts := make([]string, 0, len(ids))
	seen := make(map[int64]bool)
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

// EffectiveEntryNodes applies a binding to the owner's allowed entries (kept
// in allowed order). Stale ids drop out; when none is left the binding is
// cleared and every allowed entry is used again. changed reports that the
// stored binding should become kept.
func EffectiveEntryNodes(allowed []int64, binding string) (effective []int64, kept string, changed bool) {
	ids := ParseEntryNodeIDs(binding)
	if len(ids) == 0 {
		return allowed, "", strings.TrimSpace(binding) != ""
	}
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	effective = make([]int64, 0, len(ids))
	for _, id := range allowed {
		if want[id] {
			effective = append(effective, id)
			delete(want, id)
		}
	}
	if len(effective) == 0 {
		return allowed, "", true
	}
	kept = FormatEntryNodeIDs(effective)
	return effective, kept, kept != binding
}

func (r *Repository) SetForwardEntryBinding(forwardID int64, binding string) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Model(&model.Forward{}).Where("id = ?", forwardID).Update("entry_node_ids", FormatEntryNodeIDs(ParseEntryNodeIDs(binding))).Error
}

// ForwardEntryBindings returns the bound rules among ids (all bound rules
// when ids is empty): one query.
func (r *Repository) ForwardEntryBindings(ids []int64) (map[int64]string, error) {
	out := make(map[int64]string)
	if r == nil || r.db == nil {
		return out, errors.New("repository not initialized")
	}
	type row struct {
		ID           int64
		EntryNodeIDs string
	}
	var rows []row
	q := r.db.Model(&model.Forward{}).Select("id, entry_node_ids").Where("entry_node_ids <> ''")
	if len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}
	if err := q.Find(&rows).Error; err != nil {
		return out, err
	}
	for _, rw := range rows {
		out[rw.ID] = rw.EntryNodeIDs
	}
	return out, nil
}

// BoundForwardPairs lists the (user, tunnel) pairs that have bound rules.
func (r *Repository) BoundForwardPairs() (map[[2]int64]bool, error) {
	out := make(map[[2]int64]bool)
	if r == nil || r.db == nil {
		return out, errors.New("repository not initialized")
	}
	var pairs []RevokedUserTunnelPair
	if err := r.db.Model(&model.Forward{}).Distinct("user_id", "tunnel_id").Where("entry_node_ids <> '' AND tunnel_id > 0").Find(&pairs).Error; err != nil {
		return out, err
	}
	for _, p := range pairs {
		out[[2]int64{p.UserID, p.TunnelID}] = true
	}
	return out, nil
}

// entryDisplay is what {入口} shows for one entry: its alias, else the node
// name for admins and the public entry label for everyone else.
func (node tunnelEntryNode) entryDisplay(admin bool) string {
	if alias := strings.TrimSpace(node.Alias); alias != "" {
		return alias
	}
	if admin {
		return strings.TrimSpace(node.Name)
	}
	label, _ := node.entryLabelKey(false)
	return label
}

func entryDisplays(nodes []tunnelEntryNode, admin bool) []string {
	out := make([]string, 0, len(nodes))
	seen := make(map[string]bool)
	for _, node := range nodes {
		if d := node.entryDisplay(admin); d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// RenderTunnelName replaces {入口} with the distinct displays joined by 「/」.
func RenderTunnelName(raw string, displays []string) string {
	if !HasTunnelEntryToken(raw) {
		return raw
	}
	value := strings.Join(displays, "/")
	if value == "" {
		value = "入口"
	}
	return strings.ReplaceAll(raw, TunnelEntryToken, value)
}

func renderTunnelNameFor(raw string, nodes []tunnelEntryNode, admin bool) string {
	if !HasTunnelEntryToken(raw) {
		return raw
	}
	return RenderTunnelName(raw, entryDisplays(nodes, admin))
}

// entryGroupsNamed is entryGroups plus each option's rendered tunnel name
// when the raw name has the token.
func (info tunnelRegionInfo) entryGroupsNamed(admin bool, raw string) []tunnelEntryGroup {
	groups := info.entryGroups(admin)
	if !HasTunnelEntryToken(raw) {
		return groups
	}
	for i := range groups {
		nodes := make([]tunnelEntryNode, 0, 1)
		for _, node := range info.EntryNodes {
			if _, key := node.entryLabelKey(admin); key == groups[i].Key {
				nodes = append(nodes, node)
			}
		}
		groups[i].Name = renderTunnelNameFor(raw, nodes, admin)
	}
	return groups
}

// TunnelNameViewer is who reads a tunnel name. Non-admin viewers with a
// UserID see their permitted entries only; UserID 0 = every entry with public
// labels (peer panels).
type TunnelNameViewer struct {
	Admin  bool
	UserID int64
}

// TunnelNameRenderer renders {入口} for one viewer and one batch of tunnels.
type TunnelNameRenderer struct {
	viewer  TunnelNameViewer
	entries map[int64][]tunnelEntryNode // every entry, chain order
	visible map[int64][]tunnelEntryNode // the viewer's entries
}

// NewTunnelNameRenderer loads the entry nodes of the tunnels whose raw name
// carries the token, plus extra tunnels (bound rules): one query, plus one
// batched grant lookup for non-admin viewers. Nothing templated → no query.
func (r *Repository) NewTunnelNameRenderer(viewer TunnelNameViewer, raw map[int64]string, extra ...int64) (*TunnelNameRenderer, error) {
	tr := &TunnelNameRenderer{viewer: viewer, entries: map[int64][]tunnelEntryNode{}, visible: map[int64][]tunnelEntryNode{}}
	want := make(map[int64]bool)
	for id, name := range raw {
		if id > 0 && HasTunnelEntryToken(name) {
			want[id] = true
		}
	}
	for _, id := range extra {
		if id > 0 {
			want[id] = true
		}
	}
	if len(want) == 0 || r == nil || r.db == nil {
		return tr, nil
	}
	ids := make([]int64, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	regions, err := r.loadTunnelRegions(ids)
	if err != nil {
		return tr, err
	}
	for _, id := range ids {
		tr.entries[id] = regions[id].EntryNodes
		tr.visible[id] = regions[id].EntryNodes
	}
	if !viewer.Admin && viewer.UserID > 0 {
		allowed, err := r.userEntryRestrictions(viewer.UserID, ids, tr.entries)
		if err != nil {
			return tr, err
		}
		for id, nodes := range allowed {
			tr.visible[id] = nodes
		}
	}
	return tr, nil
}

// Name renders a tunnel name without entry context.
func (tr *TunnelNameRenderer) Name(tunnelID int64, raw string) string {
	if tr == nil || !HasTunnelEntryToken(raw) {
		return raw
	}
	return renderTunnelNameFor(raw, tr.visible[tunnelID], tr.viewer.Admin)
}

func (tr *TunnelNameRenderer) nodesOf(tunnelID int64, nodeIDs []int64) []tunnelEntryNode {
	if tr == nil {
		return nil
	}
	want := make(map[int64]bool, len(nodeIDs))
	for _, id := range nodeIDs {
		want[id] = true
	}
	out := make([]tunnelEntryNode, 0, len(nodeIDs))
	for _, node := range tr.entries[tunnelID] {
		if want[node.ID] {
			out = append(out, node)
		}
	}
	return out
}

// NameForNodes renders a tunnel name for specific entries (a bound rule).
func (tr *TunnelNameRenderer) NameForNodes(tunnelID int64, raw string, nodeIDs []int64) string {
	if tr == nil || !HasTunnelEntryToken(raw) {
		return raw
	}
	nodes := tr.nodesOf(tunnelID, nodeIDs)
	if len(nodes) == 0 {
		return tr.Name(tunnelID, raw)
	}
	return renderTunnelNameFor(raw, nodes, tr.viewer.Admin)
}

// EntryKeyLabel returns the viewer's picker key (admin node-<id>, user hashed
// label key) and the display of a bound rule's entries.
func (tr *TunnelNameRenderer) EntryKeyLabel(tunnelID int64, nodeIDs []int64) (string, string) {
	nodes := tr.nodesOf(tunnelID, nodeIDs)
	if len(nodes) == 0 {
		return "", ""
	}
	_, key := nodes[0].entryLabelKey(tr.viewer.Admin)
	return key, strings.Join(entryDisplays(nodes, tr.viewer.Admin), "/")
}

// userEntryRestrictions is TunnelEntryRestriction for many tunnels at once:
// the result holds the restricted tunnels only (missing = every entry).
func (r *Repository) userEntryRestrictions(userID int64, tunnelIDs []int64, entries map[int64][]tunnelEntryNode) (map[int64][]tunnelEntryNode, error) {
	out := make(map[int64][]tunnelEntryNode)
	var user model.User
	if err := r.db.Select("id, role_id").Where("id = ?", userID).Limit(1).Find(&user).Error; err != nil {
		return out, err
	}
	if user.ID == 0 || user.RoleID == 0 {
		return out, nil
	}
	var uts []model.UserTunnel
	if err := r.db.Select("id, tunnel_id").Where("user_id = ? AND tunnel_id IN ?", userID, tunnelIDs).Order("id ASC").Find(&uts).Error; err != nil {
		return out, err
	}
	utByTunnel := make(map[int64]int64)
	utIDs := make([]int64, 0, len(uts))
	for _, ut := range uts {
		if _, ok := utByTunnel[ut.TunnelID]; !ok {
			utByTunnel[ut.TunnelID] = ut.ID
			utIDs = append(utIDs, ut.ID)
		}
	}
	if len(utIDs) == 0 {
		return out, nil
	}
	type grantRow struct {
		UserTunnelID   int64
		TunnelGroupID  int64
		CreatedByGroup int
	}
	var grants []grantRow
	if err := r.db.Model(&model.GroupPermissionGrant{}).Select("user_tunnel_id, tunnel_group_id, created_by_group").Where("user_tunnel_id IN ?", utIDs).Find(&grants).Error; err != nil {
		return out, err
	}
	restricted := make(map[int64]bool)
	groupsByUT := make(map[int64][]int64)
	groupSet := make(map[int64]bool)
	for _, g := range grants {
		if g.CreatedByGroup == 1 {
			restricted[g.UserTunnelID] = true
		}
		groupsByUT[g.UserTunnelID] = append(groupsByUT[g.UserTunnelID], g.TunnelGroupID)
		groupSet[g.TunnelGroupID] = true
	}
	if len(restricted) == 0 {
		return out, nil
	}
	groupIDs := make([]int64, 0, len(groupSet))
	for id := range groupSet {
		groupIDs = append(groupIDs, id)
	}
	var rows []model.TunnelGroupTunnelEntry
	if err := r.db.Select("tunnel_group_id, tunnel_id, node_id").Where("tunnel_group_id IN ? AND tunnel_id IN ?", groupIDs, tunnelIDs).Find(&rows).Error; err != nil {
		return out, err
	}
	entryRows := make(map[[2]int64]map[int64]bool) // (group, tunnel) → nodes
	for _, row := range rows {
		k := [2]int64{row.TunnelGroupID, row.TunnelID}
		if entryRows[k] == nil {
			entryRows[k] = map[int64]bool{}
		}
		entryRows[k][row.NodeID] = true
	}
	for tunnelID, utID := range utByTunnel {
		if !restricted[utID] || len(entries[tunnelID]) == 0 {
			continue
		}
		permitted := make(map[int64]bool)
		for _, gid := range groupsByUT[utID] {
			for id := range entryRows[[2]int64{gid, tunnelID}] {
				permitted[id] = true
			}
		}
		nodes := make([]tunnelEntryNode, 0, len(permitted))
		for _, node := range entries[tunnelID] {
			if permitted[node.ID] {
				nodes = append(nodes, node)
			}
		}
		out[tunnelID] = nodes
	}
	return out, nil
}

// ResolveTunnelEntryKey maps a picker option key to the allowed entry nodes it
// stands for (chain order). Admin callers use node-<id>; everyone may use the
// hashed label key, which can cover several nodes with the same label.
func (r *Repository) ResolveTunnelEntryKey(tunnelID int64, allowed []int64, key string, admin bool) ([]int64, error) {
	key = strings.TrimSpace(key)
	if key == "" || tunnelID <= 0 {
		return nil, nil
	}
	regions, err := r.loadTunnelRegions([]int64{tunnelID})
	if err != nil {
		return nil, err
	}
	permitted := make(map[int64]bool, len(allowed))
	for _, id := range allowed {
		permitted[id] = true
	}
	out := make([]int64, 0, 1)
	for _, node := range regions[tunnelID].EntryNodes {
		if !permitted[node.ID] {
			continue
		}
		_, userKey := node.entryLabelKey(false)
		if key == userKey || admin && key == "node-"+strconv.FormatInt(node.ID, 10) {
			out = append(out, node.ID)
		}
	}
	return out, nil
}

// EntryKeysForNodes gives the viewer's picker key of each node (latency
// entries, plan 152 part C): one query.
func (r *Repository) EntryKeysForNodes(nodeIDs []int64, admin bool) (map[int64]string, error) {
	out := make(map[int64]string, len(nodeIDs))
	if len(nodeIDs) == 0 || r == nil || r.db == nil {
		return out, nil
	}
	if admin {
		for _, id := range nodeIDs {
			out[id] = "node-" + strconv.FormatInt(id, 10)
		}
		return out, nil
	}
	var nodes []model.Node
	if err := r.db.Select("id, name, region, region_city").Where("id IN ?", nodeIDs).Find(&nodes).Error; err != nil {
		return out, err
	}
	for _, n := range nodes {
		_, key := tunnelEntryNode{ID: n.ID, Name: n.Name, Region: n.Region, RegionCity: n.RegionCity}.entryLabelKey(false)
		out[n.ID] = key
	}
	return out, nil
}

// SetNodeEntryAlias stores a node's 入口显示名.
func (r *Repository) SetNodeEntryAlias(nodeID int64, alias string) error {
	if r == nil || r.db == nil {
		return errors.New("repository not initialized")
	}
	return r.db.Model(&model.Node{}).Where("id = ?", nodeID).Update("entry_alias", strings.TrimSpace(alias)).Error
}

// DecorateForwardItems renders the rule list's tunnelName for the viewer and
// adds entryKey/entryLabel (viewer's key format, never node ids or names to
// non-admins) to bound rules: one binding query + one renderer batch.
func (r *Repository) DecorateForwardItems(items []map[string]interface{}, viewer TunnelNameViewer) error {
	if r == nil || r.db == nil || len(items) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(items))
	raw := make(map[int64]string)
	for _, item := range items {
		ids = append(ids, toInt64(item["id"]))
		if tid := toInt64(item["tunnelId"]); tid > 0 {
			raw[tid], _ = item["tunnelName"].(string)
		}
	}
	bindings, err := r.ForwardEntryBindings(ids)
	if err != nil {
		return err
	}
	extra := make([]int64, 0, len(bindings))
	for _, item := range items {
		if _, ok := bindings[toInt64(item["id"])]; ok {
			extra = append(extra, toInt64(item["tunnelId"]))
		}
	}
	tr, err := r.NewTunnelNameRenderer(viewer, raw, extra...)
	if err != nil {
		return err
	}
	for _, item := range items {
		tid := toInt64(item["tunnelId"])
		if tid <= 0 {
			continue
		}
		name, _ := item["tunnelName"].(string)
		binding, bound := bindings[toInt64(item["id"])]
		if !bound {
			item["tunnelName"] = tr.Name(tid, name)
			continue
		}
		nodes := ParseEntryNodeIDs(binding)
		item["tunnelName"] = tr.NameForNodes(tid, name, nodes)
		if key, label := tr.EntryKeyLabel(tid, nodes); key != "" {
			item["entryKey"] = key
			item["entryLabel"] = label
		}
	}
	return nil
}

func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case int32:
		return int64(n)
	}
	return 0
}

// RenderPackageTunnelNames renders the dashboard / user package tunnel names
// in place (bound rules show their own entries).
func (r *Repository) RenderPackageTunnelNames(tunnels []model.UserTunnelDetail, forwards []model.UserForwardDetail, viewer TunnelNameViewer) error {
	raw := make(map[int64]string)
	for _, t := range tunnels {
		raw[t.TunnelID] = t.TunnelName
	}
	fids := make([]int64, 0, len(forwards))
	for _, f := range forwards {
		raw[f.TunnelID] = f.TunnelName
		fids = append(fids, f.ID)
	}
	templated := false
	for _, name := range raw {
		templated = templated || HasTunnelEntryToken(name)
	}
	if !templated {
		return nil // nothing to render: no query at all
	}
	bindings := map[int64]string{}
	if len(fids) > 0 {
		var err error
		if bindings, err = r.ForwardEntryBindings(fids); err != nil {
			return err
		}
	}
	tr, err := r.NewTunnelNameRenderer(viewer, raw)
	if err != nil {
		return err
	}
	for i := range tunnels {
		tunnels[i].TunnelName = tr.Name(tunnels[i].TunnelID, tunnels[i].TunnelName)
	}
	for i := range forwards {
		if b, ok := bindings[forwards[i].ID]; ok {
			forwards[i].TunnelName = tr.NameForNodes(forwards[i].TunnelID, forwards[i].TunnelName, ParseEntryNodeIDs(b))
			continue
		}
		forwards[i].TunnelName = tr.Name(forwards[i].TunnelID, forwards[i].TunnelName)
	}
	return nil
}

// GetTunnelDisplayName is GetTunnelName rendered for admin output (every
// entry). Internal code that parses names keeps using GetTunnelName.
func (r *Repository) GetTunnelDisplayName(tunnelID int64) (string, error) {
	raw, err := r.GetTunnelName(tunnelID)
	if err != nil || !HasTunnelEntryToken(raw) {
		return raw, err
	}
	return r.RenderTunnelNamesAdmin(map[int64]string{tunnelID: raw})[tunnelID], nil
}

// RenderTunnelNamesAdmin renders a batch of raw names for admin output (one
// query when any is templated, none otherwise).
func (r *Repository) RenderTunnelNamesAdmin(raw map[int64]string) map[int64]string {
	out := make(map[int64]string, len(raw))
	tr, err := r.NewTunnelNameRenderer(TunnelNameViewer{Admin: true}, raw)
	for id, name := range raw {
		if err != nil {
			out[id] = name
			continue
		}
		out[id] = tr.Name(id, name)
	}
	return out
}
