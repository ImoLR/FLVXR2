package repo

import (
	"errors"
	"net"
	"strings"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/usersub"

	"gorm.io/gorm"
)

// 用户专属域名 (plan 303): data access for per-user CNAMEs. The reconcile
// loop in the handler decides; these methods only read facts and write rows.

// UserSubdomainMain is the DDNS main domain of a node that user names point
// at: the 主地址 record (A and AAAA sharing one name → that name; one family
// → it; two different names → the AAAA one).
type UserSubdomainMain struct {
	Domain   string
	Synced   bool   // the record exists in Cloudflare
	ZoneID   string // of the main record (names live in the same zone)
	IPs      []string
	V6Only   bool   // AAAA record without an A record of the same name
	V6       string // current IPv6 when V6Only
	RecordID string
}

// UserSubdomainMainOf picks the main domain from a node's DDNS records;
// hasA lists lower-case names that have an A record on any node.
func UserSubdomainMainOf(records []model.NodeDDNSRecord, hasA map[string]bool) UserSubdomainMain {
	var a, aaaa *model.NodeDDNSRecord
	for i := range records {
		rec := &records[i]
		if rec.Role != model.DDNSRoleMain {
			continue
		}
		switch strings.ToUpper(rec.RecordType) {
		case "A":
			a = rec
		case "AAAA":
			aaaa = rec
		}
	}
	pick := aaaa
	if pick == nil {
		pick = a
	}
	if pick == nil {
		return UserSubdomainMain{}
	}
	domain := strings.ToLower(strings.TrimSpace(pick.Domain))
	out := UserSubdomainMain{Domain: domain, ZoneID: pick.ZoneID, RecordID: pick.RecordID}
	// Written once (record id + IP known) = exists in Cloudflare: node DDNS
	// never deletes records, a later failure keeps the last good one.
	synced := func(r *model.NodeDDNSRecord) bool {
		return r != nil && r.RecordID != "" && r.CurrentIP != ""
	}
	out.Synced = synced(pick)
	for _, r := range []*model.NodeDDNSRecord{aaaa, a} {
		if r != nil && strings.EqualFold(strings.TrimSpace(r.Domain), domain) && r.CurrentIP != "" {
			out.IPs = append(out.IPs, strings.ToLower(strings.Trim(r.CurrentIP, "[]")))
		}
	}
	if pick == aaaa && !hasA[domain] && !(a != nil && strings.EqualFold(a.Domain, domain)) {
		if ip := net.ParseIP(strings.TrimSpace(aaaa.CurrentIP)); ip != nil && ip.To4() == nil {
			out.V6Only, out.V6 = true, ip.String()
		}
	}
	return out
}

// UserSubdomainNode is one node the reconcile loop looks at.
type UserSubdomainNode struct {
	ID          int64
	Name        string
	Toggle      bool // user_subdomain_enabled
	DDNSEnabled bool
	ProviderID  int64
	Main        UserSubdomainMain
}

// Enabled: toggle and DDNS both on.
func (n UserSubdomainNode) Enabled() bool { return n.Toggle && n.DDNSEnabled }

// ListUserSubdomainNodes returns the nodes with the toggle on plus every node
// a non-retired name points at (absent ids = deleted nodes). 3 queries.
func (r *Repository) ListUserSubdomainNodes() (map[int64]UserSubdomainNode, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("repository not initialized")
	}
	var nodes []model.Node
	err := r.db.Select("id", "name", "ddns_enabled", "ddns_provider_id", "user_subdomain_enabled").
		Where("user_subdomain_enabled = ? OR id IN (?)", true,
			r.db.Model(&model.UserSubdomain{}).Select("node_id").Where("state <> ?", model.UserSubdomainRetired)).
		Find(&nodes).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]UserSubdomainNode, len(nodes))
	if len(nodes) == 0 {
		return out, nil
	}
	recs, hasA, err := r.ddnsRecordsByNode()
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		out[n.ID] = UserSubdomainNode{ID: n.ID, Name: n.Name, Toggle: n.UserSubdomainEnabled, DDNSEnabled: n.DDNSEnabled,
			ProviderID: n.DDNSProviderID, Main: UserSubdomainMainOf(recs[n.ID], hasA)}
	}
	return out, nil
}

func (r *Repository) ddnsRecordsByNode() (map[int64][]model.NodeDDNSRecord, map[string]bool, error) {
	var all []model.NodeDDNSRecord
	if err := r.db.Find(&all).Error; err != nil {
		return nil, nil, err
	}
	byNode := map[int64][]model.NodeDDNSRecord{}
	hasA := map[string]bool{}
	for _, rec := range all {
		byNode[rec.NodeID] = append(byNode[rec.NodeID], rec)
		if strings.EqualFold(rec.RecordType, "A") {
			hasA[strings.ToLower(strings.TrimSpace(rec.Domain))] = true
		}
	}
	return byNode, hasA, nil
}

// UserSubdomainUsage maps node id → user ids that use the node as an entry
// (a forward port on it) or as a native landing (a landing node on its
// server). 2 queries.
func (r *Repository) UserSubdomainUsage(nodeIDs []int64) (map[int64]map[int64]bool, error) {
	out := map[int64]map[int64]bool{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	type row struct {
		NodeID int64
		UserID int64
	}
	add := func(rows []row) {
		for _, x := range rows {
			if out[x.NodeID] == nil {
				out[x.NodeID] = map[int64]bool{}
			}
			out[x.NodeID][x.UserID] = true
		}
	}
	var fwd []row
	if err := r.db.Table("forward_port").Select("DISTINCT forward_port.node_id AS node_id, forward.user_id AS user_id").
		Joins("JOIN forward ON forward.id = forward_port.forward_id").
		Where("forward_port.node_id IN ?", nodeIDs).Scan(&fwd).Error; err != nil {
		return nil, err
	}
	add(fwd)
	var land []row
	if err := r.db.Table("landing_node").Select("DISTINCT landing_server.node_id AS node_id, landing_node.user_id AS user_id").
		Joins("JOIN landing_server ON landing_server.id = landing_node.landing_server_id").
		Where("landing_server.node_id IN ? AND landing_node.delete_pending = ?", nodeIDs, false).Scan(&land).Error; err != nil {
		return nil, err
	}
	add(land)
	return out, nil
}

// UserSubdomainUsers loads the state of the given users (absent = deleted).
func (r *Repository) UserSubdomainUsers(ids []int64) (map[int64]usersub.User, error) {
	out := map[int64]usersub.User{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.User
	if err := r.db.Select("id", "user", "role_id", "status", "exp_time").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, u := range rows {
		out[u.ID] = usersub.User{ID: u.ID, Admin: u.RoleID == 0, Status: u.Status, ExpTime: u.ExpTime}
	}
	return out, nil
}

// UserNames maps user ids to user names (admin views).
func (r *Repository) UserNames(ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.User
	if err := r.db.Select("id", "user").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, u := range rows {
		out[u.ID] = u.User
	}
	return out, nil
}

// ListUserSubdomainsForPlan: every non-retired name plus the admin-deleted
// tombstones (they block a new name for their pair) and retired names whose
// record is not deleted yet.
func (r *Repository) ListUserSubdomainsForPlan() ([]model.UserSubdomain, error) {
	var rows []model.UserSubdomain
	// Retired rows still holding a record (or not yet synced) come along so
	// the Cloudflare pass deletes their record.
	err := r.db.Where("state <> ? OR blocked = ? OR record_id <> '' OR status <> ?", model.UserSubdomainRetired, true, model.UserSubdomainSyncSynced).
		Order("id ASC").Find(&rows).Error
	return rows, err
}

// ListUserSubdomains lists names for the admin views. nodeID > 0: names
// pointing at the node or made for it; userID > 0: that user's names.
// Retired rows only with retired=true.
func (r *Repository) ListUserSubdomains(nodeID, userID int64, retired bool) ([]model.UserSubdomain, error) {
	q := r.db.Model(&model.UserSubdomain{})
	if nodeID > 0 {
		q = q.Where("node_id = ? OR origin_node_id = ?", nodeID, nodeID)
	}
	if userID > 0 {
		q = q.Where("user_id = ?", userID)
	}
	if !retired {
		q = q.Where("state <> ?", model.UserSubdomainRetired)
	}
	var rows []model.UserSubdomain
	err := q.Order("user_id ASC, id ASC").Find(&rows).Error
	return rows, err
}

// UserSubdomainCounts: per node, names currently pointing at it by state and
// sync status (node list pill). One query.
type UserSubdomainCount struct {
	Active, Paused, Failed, Pending int
}

func (r *Repository) UserSubdomainCounts() (map[int64]*UserSubdomainCount, error) {
	type row struct {
		NodeID int64
		State  string
		Status string
		N      int
	}
	var rows []row
	if err := r.db.Model(&model.UserSubdomain{}).Select("node_id, state, status, COUNT(*) AS n").
		Where("state <> ?", model.UserSubdomainRetired).Group("node_id, state, status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[int64]*UserSubdomainCount{}
	for _, x := range rows {
		c := out[x.NodeID]
		if c == nil {
			c = &UserSubdomainCount{}
			out[x.NodeID] = c
		}
		switch x.State {
		case model.UserSubdomainActive:
			c.Active += x.N
			switch x.Status {
			case model.UserSubdomainSyncFailed:
				c.Failed += x.N
			case model.UserSubdomainSyncPending:
				c.Pending += x.N
			}
		case model.UserSubdomainPaused:
			c.Paused += x.N
			if x.Status == model.UserSubdomainSyncFailed {
				c.Failed += x.N
			}
		}
	}
	return out, nil
}

// UserSubdomainProviderCounts: per provider, non-retired names and those
// holding a record (DNS 服务商 card).
func (r *Repository) UserSubdomainProviderCounts() (map[int64][2]int, error) {
	type row struct {
		ProviderID int64
		Total      int
		WithRecord int
	}
	var rows []row
	if err := r.db.Model(&model.UserSubdomain{}).
		Select("provider_id, COUNT(*) AS total, SUM(CASE WHEN record_id <> '' THEN 1 ELSE 0 END) AS with_record").
		Where("state <> ?", model.UserSubdomainRetired).Group("provider_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[int64][2]int{}
	for _, x := range rows {
		out[x.ProviderID] = [2]int{x.Total, x.WithRecord}
	}
	return out, nil
}

// UserSubdomainZone keys a DNS zone of a provider.
type UserSubdomainZone struct {
	ProviderID int64
	ZoneID     string
}

// UserSubdomainZoneRecordCounts: names holding a Cloudflare record, per zone.
func (r *Repository) UserSubdomainZoneRecordCounts() (map[UserSubdomainZone]int, error) {
	type row struct {
		ProviderID int64
		ZoneID     string
		N          int
	}
	var rows []row
	if err := r.db.Model(&model.UserSubdomain{}).Select("provider_id, zone_id, COUNT(*) AS n").
		Where("record_id <> ''").Group("provider_id, zone_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[UserSubdomainZone]int{}
	for _, x := range rows {
		out[UserSubdomainZone{x.ProviderID, x.ZoneID}] = x.N
	}
	return out, nil
}

func (r *Repository) GetUserSubdomain(id int64) (*model.UserSubdomain, error) {
	var row model.UserSubdomain
	if err := r.db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// UserSubdomainLabelTaken: the label was ever used (labels are never reused).
func (r *Repository) UserSubdomainLabelTaken(label string) (bool, error) {
	var n int64
	err := r.db.Model(&model.UserSubdomain{}).Where("label = ?", label).Count(&n).Error
	return n > 0, err
}

// CreateUserSubdomain inserts a new name (the unique label index is the
// final guard against reuse).
func (r *Repository) CreateUserSubdomain(row *model.UserSubdomain) error {
	now := time.Now().UnixMilli()
	if row.CreatedTime == 0 {
		row.CreatedTime = now
	}
	row.UpdatedTime = now
	if row.OriginNodeID == 0 {
		row.OriginNodeID = row.NodeID
	}
	if row.State == "" {
		row.State = model.UserSubdomainActive
	}
	if row.Status == "" {
		row.Status = model.UserSubdomainSyncPending
	}
	return r.db.Create(row).Error
}

// UpdateUserSubdomain writes columns of one name.
func (r *Repository) UpdateUserSubdomain(id int64, updates map[string]interface{}) error {
	if v, ok := updates["last_error"].(string); ok {
		updates["last_error"] = truncateRunes(v, 250)
	}
	updates["updated_time"] = time.Now().UnixMilli()
	return r.db.Model(&model.UserSubdomain{}).Where("id = ?", id).Updates(updates).Error
}

// ApplyUserSubdomainChanges writes lifecycle changes; a changed state
// re-queues the CF side (pending, no backoff).
func (r *Repository) ApplyUserSubdomainChanges(changes []usersub.Change) error {
	if len(changes) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, c := range changes {
			var cur model.UserSubdomain
			if err := tx.Select("id", "state").Where("id = ?", c.ID).First(&cur).Error; err != nil {
				continue
			}
			updates := map[string]interface{}{"state": c.State, "reason": c.Reason, "unused_since": c.UnusedSince, "updated_time": now}
			if c.RetiredAt > 0 {
				updates["retired_at"] = c.RetiredAt
			}
			if cur.State != c.State {
				updates["status"] = model.UserSubdomainSyncPending
				updates["next_retry_at"] = int64(0)
				updates["fail_count"] = 0
				updates["last_error"] = ""
			}
			if err := tx.Model(&model.UserSubdomain{}).Where("id = ?", c.ID).Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListUserSubdomainToggleNodes: ids of nodes with the toggle on.
func (r *Repository) ListUserSubdomainToggleNodes() (map[int64]bool, error) {
	var ids []int64
	if err := r.db.Model(&model.Node{}).Where("user_subdomain_enabled = ?", true).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// NodeNames maps node ids to names.
func (r *Repository) NodeNames(ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []model.Node
	if err := r.db.Select("id", "name").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, n := range rows {
		out[n.ID] = n.Name
	}
	return out, nil
}

// UserSubdomainForward is one forward of a user that enters on a node.
type UserSubdomainForward struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Port int    `json:"port"`
}

// UserSubdomainForwardUses: per (user, node), the user's forwards with a
// port on that node, and the count of native landing nodes there. 2 queries.
func (r *Repository) UserSubdomainForwardUses(userIDs, nodeIDs []int64) (map[[2]int64][]UserSubdomainForward, map[[2]int64]int, error) {
	fwd := map[[2]int64][]UserSubdomainForward{}
	land := map[[2]int64]int{}
	if len(userIDs) == 0 || len(nodeIDs) == 0 {
		return fwd, land, nil
	}
	type row struct {
		UserID int64
		NodeID int64
		ID     int64
		Name   string
		Port   int
	}
	var rows []row
	if err := r.db.Table("forward_port").
		Select("forward.user_id AS user_id, forward_port.node_id AS node_id, forward.id AS id, forward.name AS name, forward_port.port AS port").
		Joins("JOIN forward ON forward.id = forward_port.forward_id").
		Where("forward.user_id IN ? AND forward_port.node_id IN ?", userIDs, nodeIDs).
		Order("forward.inx ASC, forward.id ASC").Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	seen := map[[3]int64]bool{}
	for _, x := range rows {
		k := [2]int64{x.UserID, x.NodeID}
		if seen[[3]int64{x.UserID, x.NodeID, x.ID}] {
			continue
		}
		seen[[3]int64{x.UserID, x.NodeID, x.ID}] = true
		fwd[k] = append(fwd[k], UserSubdomainForward{ID: x.ID, Name: x.Name, Port: x.Port})
	}
	type lrow struct {
		UserID int64
		NodeID int64
		N      int
	}
	var lrows []lrow
	if err := r.db.Table("landing_node").Select("landing_node.user_id AS user_id, landing_server.node_id AS node_id, COUNT(*) AS n").
		Joins("JOIN landing_server ON landing_server.id = landing_node.landing_server_id").
		Where("landing_node.user_id IN ? AND landing_server.node_id IN ? AND landing_node.delete_pending = ?", userIDs, nodeIDs, false).
		Group("landing_node.user_id, landing_server.node_id").Scan(&lrows).Error; err != nil {
		return nil, nil, err
	}
	for _, x := range lrows {
		land[[2]int64{x.UserID, x.NodeID}] = x.N
	}
	return fwd, land, nil
}

// SetNodeUserSubdomain stores the node toggle.
func (r *Repository) SetNodeUserSubdomain(nodeID int64, enabled bool) error {
	return r.db.Model(&model.Node{}).Where("id = ?", nodeID).Update("user_subdomain_enabled", enabled).Error
}

// AnyUserSubdomainWork: some node has the toggle on, or a name still holds
// a record / is not retired (the loop has something to look at).
func (r *Repository) AnyUserSubdomainWork() (bool, error) {
	var n int64
	if err := r.db.Model(&model.Node{}).Where("user_subdomain_enabled = ?", true).Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	if err := r.db.Model(&model.UserSubdomain{}).Where("state <> ? OR record_id <> ''", model.UserSubdomainRetired).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// ---- hand-out: the owner's name instead of the node main domain ----

// UserEntryNames resolves entry hosts to the owner's live name.
type UserEntryNames struct {
	names map[int64]map[int64]string  // user → node → domain
	hosts map[int64]map[string]bool   // node → hosts a name replaces
	main  map[int64]UserSubdomainMain // node → main domain info
}

// Lookup returns the user's name for an entry host on nodeID ("" = keep
// the host). v6 is the current IPv6 when the main domain is IPv6-only
// (plan 302: subscriptions keep that literal for clients that cannot
// resolve AAAA-only names).
func (x *UserEntryNames) Lookup(userID, nodeID int64, host string) (name, v6 string) {
	if x == nil {
		return "", ""
	}
	name = x.names[userID][nodeID]
	if name == "" {
		return "", ""
	}
	h := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if ip := net.ParseIP(h); ip != nil {
		h = ip.String()
	}
	if !x.hosts[nodeID][h] {
		return "", ""
	}
	m := x.main[nodeID]
	if m.V6Only {
		return name, m.V6
	}
	return name, ""
}

// Empty: no user has a live name (callers skip all host work).
func (x *UserEntryNames) Empty() bool { return x == nil || len(x.names) == 0 }

// LoadUserEntryNames loads the live names of the given users (nil = all
// users) on nodes with the toggle on. One query when nobody has a name.
func (r *Repository) LoadUserEntryNames(userIDs []int64) (*UserEntryNames, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	q := r.db.Model(&model.UserSubdomain{}).Select("user_subdomain.user_id", "user_subdomain.node_id", "user_subdomain.domain", "user_subdomain.id").
		Joins("JOIN node ON node.id = user_subdomain.node_id").
		Where("user_subdomain.state = ? AND user_subdomain.status = ? AND user_subdomain.record_id <> '' AND node.user_subdomain_enabled = ? AND node.ddns_enabled = ?",
			model.UserSubdomainActive, model.UserSubdomainSyncSynced, true, true)
	if userIDs != nil {
		if len(userIDs) == 0 {
			return &UserEntryNames{}, nil
		}
		q = q.Where("user_subdomain.user_id IN ?", userIDs)
	}
	var rows []model.UserSubdomain
	if err := q.Order("user_subdomain.id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	x := &UserEntryNames{names: map[int64]map[int64]string{}, hosts: map[int64]map[string]bool{}, main: map[int64]UserSubdomainMain{}}
	if len(rows) == 0 {
		return x, nil
	}
	recs, hasA, err := r.ddnsRecordsByNode()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if _, ok := x.main[row.NodeID]; !ok {
			m := UserSubdomainMainOf(recs[row.NodeID], hasA)
			x.main[row.NodeID] = m
			hosts := map[string]bool{}
			if m.Domain != "" {
				hosts[m.Domain] = true
				for _, ip := range m.IPs {
					if p := net.ParseIP(ip); p != nil {
						hosts[p.String()] = true
					}
				}
			}
			x.hosts[row.NodeID] = hosts
		}
		if x.main[row.NodeID].Domain == "" {
			continue
		}
		if x.names[row.UserID] == nil {
			x.names[row.UserID] = map[int64]string{}
		}
		if x.names[row.UserID][row.NodeID] == "" { // oldest live name wins (a moved-in name before a later own one)
			x.names[row.UserID][row.NodeID] = row.Domain
		}
	}
	return x, nil
}

// RewriteEntryList rewrites a "host:port,host:port" forward address list
// (forward list inIp) with the owner's names. Hosts are matched per node
// in nodeIDs (the forward's entry nodes).
func (x *UserEntryNames) RewriteEntryList(userID int64, nodeIDs []int64, list string) string {
	if x.Empty() || x.names[userID] == nil || strings.TrimSpace(list) == "" {
		return list
	}
	if nodeIDs == nil {
		// Main domains are unique per node: try every node the user has a name on.
		for id := range x.names[userID] {
			nodeIDs = append(nodeIDs, id)
		}
	}
	parts := strings.Split(list, ",")
	changed := false
	for i, part := range parts {
		host, port, err := net.SplitHostPort(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		for _, nodeID := range nodeIDs {
			if name, _ := x.Lookup(userID, nodeID, host); name != "" {
				parts[i] = net.JoinHostPort(name, port)
				changed = true
				break
			}
		}
	}
	if !changed {
		return list
	}
	return strings.Join(parts, ",")
}
