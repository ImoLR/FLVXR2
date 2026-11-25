package repo

import (
	"errors"
	"net"
	"sort"
	"strconv"
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
	// Targets (plan 305): every DDNS domain names point at (main first).
	Targets []UserSubdomainTarget
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
			ProviderID: n.DDNSProviderID, Main: UserSubdomainMainOf(recs[n.ID], hasA), Targets: UserSubdomainTargetsOf(recs[n.ID], hasA)}
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
// server). Plan 305: forwards / landing nodes kept on their old address
// (已有转发保持原地址) do not count — they never get a name. 3 queries.
func (r *Repository) UserSubdomainUsage(nodeIDs []int64) (map[int64]map[int64]bool, error) {
	out := map[int64]map[int64]bool{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	keep, err := r.loadUserSubdomainKeep(nodeIDs, nil)
	if err != nil {
		return nil, err
	}
	add := func(rows []userSubdomainKeepRow, kind string) {
		for _, x := range rows {
			if keep.has(x.NodeID, kind, x.RefID, x.Created) {
				continue
			}
			if out[x.NodeID] == nil {
				out[x.NodeID] = map[int64]bool{}
			}
			out[x.NodeID][x.UserID] = true
		}
	}
	fwd, err := userSubdomainForwardsOn(r.db, nodeIDs)
	if err != nil {
		return nil, err
	}
	add(fwd, model.UserSubdomainKeepForward)
	land, err := userSubdomainLandingsOn(r.db, nodeIDs, false)
	if err != nil {
		return nil, err
	}
	add(land, model.UserSubdomainKeepLanding)
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
	// Round 3: Capped = waiting for room in the zone; Hidden = fresh names
	// not handed out (capped, or creating keeps failing).
	Capped, Hidden int
}

func (r *Repository) UserSubdomainCounts() (map[int64]*UserSubdomainCount, error) {
	var rows []model.UserSubdomain
	if err := r.db.Select("id", "node_id", "state", "status", "fail_count", "ready_at").
		Where("state <> ?", model.UserSubdomainRetired).Find(&rows).Error; err != nil {
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
			c.Active++
			switch x.Status {
			case model.UserSubdomainSyncFailed:
				c.Failed++
			case model.UserSubdomainSyncPending:
				c.Pending++
			case model.UserSubdomainSyncCapped:
				c.Capped++
			}
		case model.UserSubdomainPaused:
			c.Paused++
			if x.Status == model.UserSubdomainSyncFailed {
				c.Failed++
			}
		}
		if usersub.Hidden(x) {
			c.Hidden++
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
	// Kept (plan 305): 保持原地址 — this forward keeps the node address.
	Kept bool `json:"kept,omitempty"`
}

// UserSubdomainForwardUses: per (user, node), the user's forwards with a
// port on that node, and the count of native landing nodes there. 2 queries.
func (r *Repository) UserSubdomainForwardUses(userIDs, nodeIDs []int64) (map[[2]int64][]UserSubdomainForward, map[[2]int64]int, error) {
	fwd := map[[2]int64][]UserSubdomainForward{}
	land := map[[2]int64]int{}
	if len(userIDs) == 0 || len(nodeIDs) == 0 {
		return fwd, land, nil
	}
	keep, err := r.loadUserSubdomainKeep(nodeIDs, userIDs)
	if err != nil {
		return nil, nil, err
	}
	type row struct {
		UserID  int64
		NodeID  int64
		ID      int64
		Name    string
		Port    int
		Created int64
	}
	var rows []row
	if err := r.db.Table("forward_port").
		Select("forward.user_id AS user_id, forward_port.node_id AS node_id, forward.id AS id, forward.name AS name, forward_port.port AS port, forward.created_time AS created").
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
		fwd[k] = append(fwd[k], UserSubdomainForward{ID: x.ID, Name: x.Name, Port: x.Port,
			Kept: keep.has(x.NodeID, model.UserSubdomainKeepForward, x.ID, x.Created)})
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

// AnyUserSubdomainToggle: some node has the toggle on (one COUNT).
func (r *Repository) AnyUserSubdomainToggle() (bool, error) {
	var n int64
	if err := r.db.Model(&model.Node{}).Where("user_subdomain_enabled = ?", true).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// UserSubdomainPairTaken: the user already has a live or paused name
// pointing at the node's DDNS record (plan 305: "" = main), or an
// admin-deleted tombstone for it.
func (r *Repository) UserSubdomainPairTaken(userID, nodeID int64, record string) (bool, error) {
	var n int64
	err := r.db.Model(&model.UserSubdomain{}).
		Where("user_id = ? AND node_id = ? AND record_domain = ? AND (state <> ? OR blocked = ?)", userID, nodeID, record, model.UserSubdomainRetired, true).
		Count(&n).Error
	return n > 0, err
}

// UserSubdomainZoneName: the zone name of an existing name in that zone
// ("" = none yet).
func (r *Repository) UserSubdomainZoneName(providerID int64, zoneID string) (string, error) {
	var row model.UserSubdomain
	err := r.db.Select("label", "domain").Where("provider_id = ? AND zone_id = ? AND domain <> ''", providerID, zoneID).
		Order("id DESC").Limit(1).Find(&row).Error
	if err != nil || row.Domain == "" {
		return "", err
	}
	return strings.ToLower(strings.TrimPrefix(row.Domain, row.Label+".")), nil
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
//
// Round 2 (user 11-24): on a node whose toggle is on (and main domain
// synced) a user never sees the node IP or its main domain once he has a
// name. His name is handed out whatever its Cloudflare state:
//   - active (record live, or about to be created / re-created: tag pending)
//   - paused 到期 / 停用: the record is deleted on purpose, the name stays
//     (tag expired / disabled) so the client is dead until 续期 / 启用 and
//     then works again without a refresh
//   - admin-deleted (删除, blocked tombstone): the dead name (tag deleted) —
//     he must not fall back to the main domain he could keep using.
// Round 3 (user 11-24 evening): the main domain is never handed to a user,
// not even before he has a name. Names are reserved at once, so a user
// normally always has one; it is shown with 「正在就绪」 until its record is
// verified. A fresh name that cannot get a record (zone at the cap, creating
// keeps failing) and the instant before the reservation give NO address at
// all (Hidden: subs omit the entry, lists show why). Nodes without the
// toggle keep their hosts as they are (fork.168).

// Entry name tags.
const (
	UserEntryTagPending  = "pending"  // 正在就绪: the record is being created / verified (usable in seconds)
	UserEntryTagExpired  = "expired"  // 到期: record deleted, name kept
	UserEntryTagDisabled = "disabled" // 停用: record deleted, name kept
	UserEntryTagDeleted  = "deleted"  // 删除 by the admin: dead name
	UserEntryTagCapped   = "capped"   // 入口域名名额已满 (zone at the record cap)
	UserEntryTagFailed   = "failed"   // 入口域名创建失败 (creating the record keeps failing)
)

// UserEntryTagMessage: what a user is told when an entry has no address.
func UserEntryTagMessage(tag string) string {
	switch tag {
	case UserEntryTagCapped:
		return "入口域名名额已满，请联系管理员"
	case UserEntryTagFailed:
		return "入口域名创建失败，请联系管理员"
	default:
		return "入口域名正在就绪，请几秒后再试"
	}
}

// UserEntryHiddenError: every entry of a link was withheld (Hidden).
type UserEntryHiddenError struct{ Tag string }

func (e *UserEntryHiddenError) Error() string { return UserEntryTagMessage(e.Tag) }

// UserEntryHost is what one entry host becomes for one user.
type UserEntryHost struct {
	Host   string // "" = keep the original host (unless Hidden)
	Tag    string // see UserEntryTag*; "" = live name
	V6Only bool   // Host is an AAAA-only name (main domain without A record)
	Name   bool   // Host is the user's own name
	// Hidden (round 3): give no address at all for this entry; Tag says why
	// (capped / failed / pending = no name yet). Domain / Reason: the
	// withheld name and its last error (admin views only).
	Hidden bool
	Domain string
	Reason string
}

// UserEntryNotice: one withheld entry of a forward address list.
type UserEntryNotice struct {
	NodeID int64  `json:"nodeId"`
	Port   int    `json:"port"`
	Tag    string `json:"tag"`
	Domain string `json:"domain,omitempty"` // admins only
	Reason string `json:"reason,omitempty"` // admins only
}

type userEntryName struct {
	domain string
	tag    string
	rank   int // lower wins: live/pending 0, paused 1, deleted 2
	id     int64
	hidden bool
	reason string
}

// UserEntryNames resolves entry hosts to the owner's name.
type UserEntryNames struct {
	names map[int64]map[int64]map[string]userEntryName // user → node → record key → name
	// hosts: node → host (DDNS domain or its current IP) → record key.
	hosts   map[int64]map[string]string
	targets map[int64]map[string]UserSubdomainTarget // node → record key → target
	admins  map[int64]bool                           // users that never get a name
	// Plan 305 (已有转发保持原地址): node → kept forward / landing node ids,
	// and node → users holding any of them.
	keptForward map[int64]map[int64]bool
	keptLanding map[int64]map[int64]bool
	keptUser    map[int64]map[int64]bool
	effective   bool // some node has the toggle on
}

func normEntryHost(host string) string {
	h := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if ip := net.ParseIP(h); ip != nil {
		h = ip.String()
	}
	return h
}

// Resolve: the host this user is given for an entry host on nodeID outside
// any forward / landing node (the landing server list). Plan 305: a user
// whose forwards / landings on the node all keep the old address (and who
// has no name for this record) sees the host as before.
func (x *UserEntryNames) Resolve(userID, nodeID int64, host string) UserEntryHost {
	if x == nil {
		return UserEntryHost{}
	}
	return x.resolve(userID, nodeID, host, x.keptUser[nodeID][userID])
}

// ResolveForward: the host the owner of forward forwardID is given for its
// entry host on nodeID. A kept forward (已有转发保持原地址) keeps it as is.
func (x *UserEntryNames) ResolveForward(userID, nodeID, forwardID int64, host string) UserEntryHost {
	if x == nil {
		return UserEntryHost{}
	}
	if forwardID > 0 && x.keptForward[nodeID][forwardID] {
		return UserEntryHost{}
	}
	if forwardID <= 0 {
		return x.Resolve(userID, nodeID, host)
	}
	return x.resolve(userID, nodeID, host, false)
}

// ResolveLanding: the host the owner of native landing node landingID is
// given for its server's public host on nodeID (直连).
func (x *UserEntryNames) ResolveLanding(userID, nodeID, landingID int64, host string) UserEntryHost {
	if x == nil {
		return UserEntryHost{}
	}
	if landingID > 0 && x.keptLanding[nodeID][landingID] {
		return UserEntryHost{}
	}
	if landingID <= 0 {
		return x.Resolve(userID, nodeID, host)
	}
	return x.resolve(userID, nodeID, host, false)
}

// Kept: forward forwardID keeps its old address on nodeID (admin tag).
func (x *UserEntryNames) Kept(nodeID, forwardID int64) bool {
	return x != nil && x.keptForward[nodeID][forwardID]
}

func (x *UserEntryNames) resolve(userID, nodeID int64, host string, keepIfNameless bool) UserEntryHost {
	key, ok := x.hosts[nodeID][normEntryHost(host)]
	if !ok {
		return UserEntryHost{}
	}
	t := x.targets[nodeID][key]
	if n, ok := x.names[userID][nodeID][key]; ok {
		if n.hidden {
			return UserEntryHost{Hidden: true, Tag: n.tag, Domain: n.domain, Reason: n.reason}
		}
		return UserEntryHost{Host: n.domain, Tag: n.tag, V6Only: t.V6Only, Name: true}
	}
	if x.admins[userID] || userID <= 0 || keepIfNameless {
		return UserEntryHost{}
	}
	// Round 3: no name yet (the instant before the reservation, or the DDNS
	// record is not in Cloudflare yet): nothing — never the node's domain.
	return UserEntryHost{Hidden: true, Tag: UserEntryTagPending}
}

// Lookup returns the user's own name for an entry host on nodeID ("" = none)
// and its tag.
func (x *UserEntryNames) Lookup(userID, nodeID int64, host string) (name, tag string) {
	h := x.Resolve(userID, nodeID, host)
	if !h.Name {
		return "", h.Tag
	}
	return h.Host, h.Tag
}

// Empty: no node has the toggle on (callers skip all host work).
func (x *UserEntryNames) Empty() bool { return x == nil || !x.effective }

// LoadUserEntryNames loads the handed-out names of the given users (nil =
// all users) on nodes with the toggle on. One query when no node has it on.
// Plan 305: every DDNS domain of such a node (and its current IPs) maps to
// the user's name for that domain; the node's keep set comes along.
func (r *Repository) LoadUserEntryNames(userIDs []int64) (*UserEntryNames, error) {
	if r == nil || r.db == nil {
		return nil, nil
	}
	x := &UserEntryNames{names: map[int64]map[int64]map[string]userEntryName{}, hosts: map[int64]map[string]string{},
		targets: map[int64]map[string]UserSubdomainTarget{}, admins: map[int64]bool{},
		keptForward: map[int64]map[int64]bool{}, keptLanding: map[int64]map[int64]bool{}, keptUser: map[int64]map[int64]bool{}}
	if userIDs != nil && len(userIDs) == 0 {
		return x, nil
	}
	var nodeIDs []int64
	if err := r.db.Model(&model.Node{}).Where("user_subdomain_enabled = ? AND ddns_enabled = ?", true, true).
		Order("id ASC").Pluck("id", &nodeIDs).Error; err != nil {
		return nil, err
	}
	if len(nodeIDs) == 0 {
		return x, nil
	}
	recs, hasA, err := r.ddnsRecordsByNode()
	if err != nil {
		return nil, err
	}
	aliases, err := r.userSubdomainAddressAliases(nodeIDs, recs)
	if err != nil {
		return nil, err
	}
	live := []int64{}
	for _, id := range nodeIDs {
		m := UserSubdomainMainOf(recs[id], hasA)
		if m.Domain == "" || !m.Synced {
			continue // not effective: hosts stay as they are
		}
		hosts := map[string]string{}
		targets := map[string]UserSubdomainTarget{}
		for _, t := range UserSubdomainTargetsOf(recs[id], hasA) {
			targets[t.Key] = t
			// Every DDNS domain is replaced, synced or not: a domain whose
			// record is not in Cloudflare yet has no name yet → withheld.
			if _, dup := hosts[t.Domain]; !dup {
				hosts[t.Domain] = t.Key
			}
			for _, ip := range t.IPs {
				if p := net.ParseIP(ip); p != nil {
					if _, dup := hosts[p.String()]; !dup {
						hosts[p.String()] = t.Key
					}
				}
			}
		}
		// Plan 307 「保持节点地址」: the node's own address (a hostname we do
		// not own, e.g. a vendor or an older shared name) stands for the main
		// record of its family.
		for host, domain := range aliases[id] {
			for key, t := range targets {
				if t.Domain == domain {
					if _, dup := hosts[host]; !dup {
						hosts[host] = key
					}
				}
			}
		}
		x.hosts[id], x.targets[id] = hosts, targets
		live = append(live, id)
	}
	if len(live) == 0 {
		return x, nil
	}
	x.effective = true
	q := r.db.Model(&model.UserSubdomain{}).
		Select("id", "user_id", "node_id", "record_domain", "domain", "state", "reason", "status", "record_id", "blocked", "retired_at",
			"ready_at", "fail_count", "last_error").
		Where("node_id IN ? AND (state <> ? OR blocked = ?)", live, model.UserSubdomainRetired, true)
	aq := r.db.Model(&model.User{}).Where("role_id = 0")
	if userIDs != nil {
		q = q.Where("user_id IN ?", userIDs)
		aq = aq.Where("id IN ?", userIDs)
	}
	var rows []model.UserSubdomain
	if err := q.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var admins []int64
	if err := aq.Pluck("id", &admins).Error; err != nil {
		return nil, err
	}
	for _, id := range admins {
		x.admins[id] = true
	}
	kept, _, err := r.liveUserSubdomainKeep(live)
	if err != nil {
		return nil, err
	}
	wanted := map[int64]bool{}
	for _, id := range userIDs {
		wanted[id] = true
	}
	for _, k := range kept {
		if userIDs != nil && !wanted[k.UserID] {
			continue
		}
		set := x.keptForward
		if k.Kind == model.UserSubdomainKeepLanding {
			set = x.keptLanding
		}
		if set[k.NodeID] == nil {
			set[k.NodeID] = map[int64]bool{}
		}
		set[k.NodeID][k.RefID] = true
		if x.keptUser[k.NodeID] == nil {
			x.keptUser[k.NodeID] = map[int64]bool{}
		}
		x.keptUser[k.NodeID][k.UserID] = true
	}
	for _, row := range rows {
		n := userEntryName{domain: row.Domain, id: row.ID}
		switch {
		case row.State == model.UserSubdomainRetired: // blocked tombstone
			n.rank, n.tag = 2, UserEntryTagDeleted
		case row.State == model.UserSubdomainPaused && row.Reason == model.UserSubdomainReasonExpired:
			n.rank, n.tag = 1, UserEntryTagExpired
		case row.State == model.UserSubdomainPaused && row.Reason == model.UserSubdomainReasonDisabled:
			n.rank, n.tag = 1, UserEntryTagDisabled
		case row.Live() && row.ReadyAt > 0:
			// active, record verified and served by the zone's DNS: the plain name
		default: // active not live yet, or paused for a node reason that no longer holds (comes back next pass)
			n.tag = UserEntryTagPending
			capped := row.Status == model.UserSubdomainSyncCapped
			failed := row.Status == model.UserSubdomainSyncFailed && row.FailCount >= usersub.FailShow
			switch {
			case capped:
				n.tag = UserEntryTagCapped
			case failed:
				n.tag = UserEntryTagFailed
			}
			// A fresh name that cannot get its record is withheld; one that
			// was ready before stays (the user already has it), tagged.
			n.hidden = usersub.Hidden(row)
			if capped || failed {
				n.reason = row.LastError
			}
		}
		if row.Domain == "" {
			continue
		}
		byNode := x.names[row.UserID]
		if byNode == nil {
			byNode = map[int64]map[string]userEntryName{}
			x.names[row.UserID] = byNode
		}
		byKey := byNode[row.NodeID]
		if byKey == nil {
			byKey = map[string]userEntryName{}
			byNode[row.NodeID] = byKey
		}
		cur, ok := byKey[row.RecordDomain]
		// Best rank wins; among live names the oldest (a moved-in name before
		// a later own one); among tombstones the newest.
		if !ok || n.rank < cur.rank || n.rank == cur.rank && n.rank == 2 && n.id > cur.id {
			byKey[row.RecordDomain] = n
		}
	}
	return x, nil
}

// userSubdomainAddressAliases (plan 307): for nodes with 「保持节点地址」, the
// node's v4 / v6 address field when it is a hostname (not an IP literal and
// not one of the node's DDNS domains) → the domain of the main record of the
// same family. IP literals map only through a record's own IP, never by
// guess (a node whose record points elsewhere keeps its literal). 1 query.
func (r *Repository) userSubdomainAddressAliases(nodeIDs []int64, recs map[int64][]model.NodeDDNSRecord) (map[int64]map[string]string, error) {
	out := map[int64]map[string]string{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	var nodes []model.Node
	if err := r.db.Select("id", "server_ip", "server_ip_v4", "server_ip_v6", "ddns_keep_address").
		Where("id IN ? AND ddns_keep_address = ?", nodeIDs, true).Find(&nodes).Error; err != nil {
		return nil, err
	}
	for _, n := range nodes {
		own := map[string]bool{}
		mainOf := map[string]string{}
		for _, rec := range recs[n.ID] {
			d := strings.ToLower(strings.TrimSpace(rec.Domain))
			own[d] = true
			if rec.Role == model.DDNSRoleMain {
				mainOf[strings.ToUpper(rec.RecordType)] = d
			}
		}
		add := func(field, family string) {
			host := normEntryHost(field)
			if host == "" || net.ParseIP(host) != nil || own[host] || mainOf[family] == "" {
				return
			}
			if out[n.ID] == nil {
				out[n.ID] = map[string]string{}
			}
			if _, dup := out[n.ID][host]; !dup {
				out[n.ID][host] = mainOf[family]
			}
		}
		add(n.ServerIPV4.String, "A")
		add(n.ServerIPV6.String, "AAAA")
	}
	return out, nil
}

// RewriteEntryList rewrites a "host:port,host:port" forward address list
// (forward list inIp) for its owner. Hosts are matched per node in nodeIDs
// (the forward's entry nodes; nil = every node with the toggle on). tags maps
// a written host → its tag (nil when none). Round 3: withheld entries are
// dropped from the list and returned as notices. Plan 305: forwardID is the
// forward the list belongs to (a kept one keeps its hosts; 0 = not a
// forward's own entries, e.g. the target of a forward to a native landing).
func (x *UserEntryNames) RewriteEntryList(userID, forwardID int64, nodeIDs []int64, list string) (string, map[string]string, []UserEntryNotice) {
	if x.Empty() || strings.TrimSpace(list) == "" {
		return list, nil, nil
	}
	if nodeIDs == nil {
		for id := range x.hosts {
			nodeIDs = append(nodeIDs, id)
		}
		sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	}
	parts := strings.Split(list, ",")
	kept := make([]string, 0, len(parts))
	changed := false
	var tags map[string]string
	var notices []UserEntryNotice
	for _, part := range parts {
		host, port, err := net.SplitHostPort(strings.TrimSpace(part))
		if err != nil {
			kept = append(kept, part)
			continue
		}
		out := part
		for _, nodeID := range nodeIDs {
			h := x.ResolveForward(userID, nodeID, forwardID, host)
			if h.Hidden {
				p, _ := strconv.Atoi(port)
				notices = append(notices, UserEntryNotice{NodeID: nodeID, Port: p, Tag: h.Tag, Domain: h.Domain, Reason: h.Reason})
				out = ""
				changed = true
				break
			}
			if h.Host == "" {
				continue
			}
			out = net.JoinHostPort(h.Host, port)
			changed = true
			if h.Tag != "" {
				if tags == nil {
					tags = map[string]string{}
				}
				tags[h.Host] = h.Tag
			}
			break
		}
		if out != "" {
			kept = append(kept, out)
		}
	}
	if !changed {
		return list, nil, nil
	}
	return strings.Join(kept, ","), tags, notices
}
