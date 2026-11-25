package repo

import (
	"net"
	"sort"
	"strings"
	"time"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// Plan 305: 用户专属域名 on nodes with several DDNS domains (one name per
// user per domain) and 已有转发保持原地址 (forwards / native landings that
// existed when the toggle went on keep their address for good).

// UserSubdomainTarget is one DDNS domain of a node that user names point
// at: the main domain (Key "") and every other DDNS domain of the node
// (Key = that domain), e.g. Leikwanhost 沪台IPLC 电信 -ct (main) and 联通 -cu
// (extra). The A and AAAA records of one domain are one target.
type UserSubdomainTarget struct {
	Key    string // "" = the main domain
	Domain string
	Synced bool // a record of the domain exists in Cloudflare
	ZoneID string
	IPs    []string
	V6Only bool   // AAAA without an A record of the same name
	Source string // record source (ct / cu / cm / auto / prefix / panel / iface)
	Role   string
}

// UserSubdomainTargetLabel: how admin views name a target (电信 / 联通 / 移动
// from the record source, else 主地址 / 附加地址).
func UserSubdomainTargetLabel(t UserSubdomainTarget) string {
	switch t.Source {
	case "ct":
		return "电信"
	case "cu":
		return "联通"
	case "cm":
		return "移动"
	}
	if t.Key == "" {
		return "主地址"
	}
	return "附加地址"
}

// UserSubdomainTargetsOf lists a node's name targets: the main domain first
// (UserSubdomainMainOf), then the node's other DDNS domains by record order.
// nil when the node has no main record.
func UserSubdomainTargetsOf(records []model.NodeDDNSRecord, hasA map[string]bool) []UserSubdomainTarget {
	main := UserSubdomainMainOf(records, hasA)
	if main.Domain == "" {
		return nil
	}
	sorted := append([]model.NodeDDNSRecord(nil), records...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Inx != sorted[j].Inx {
			return sorted[i].Inx < sorted[j].Inx
		}
		return sorted[i].ID < sorted[j].ID
	})
	byDomain := map[string][]model.NodeDDNSRecord{}
	order := []string{}
	for _, rec := range sorted {
		d := strings.ToLower(strings.TrimSpace(rec.Domain))
		if d == "" {
			continue
		}
		if _, ok := byDomain[d]; !ok {
			order = append(order, d)
		}
		byDomain[d] = append(byDomain[d], rec)
	}
	// The AAAA record names the source of a dual-stack domain.
	source := func(recs []model.NodeDDNSRecord) (src, role string) {
		for _, typ := range []string{"AAAA", "A"} {
			for _, rec := range recs {
				if strings.EqualFold(rec.RecordType, typ) {
					return rec.Source, rec.Role
				}
			}
		}
		return "", ""
	}
	mainSrc, _ := source(byDomain[main.Domain])
	out := []UserSubdomainTarget{{Key: "", Domain: main.Domain, Synced: main.Synced, ZoneID: main.ZoneID, IPs: main.IPs, V6Only: main.V6Only,
		Source: mainSrc, Role: model.DDNSRoleMain}}
	for _, d := range order {
		if d == main.Domain {
			continue
		}
		recs := byDomain[d]
		t := UserSubdomainTarget{Key: d, Domain: d}
		t.Source, t.Role = source(recs)
		ownA, v6 := false, ""
		for _, rec := range recs {
			if rec.RecordID != "" && rec.CurrentIP != "" {
				t.Synced = true
			}
			if t.ZoneID == "" {
				t.ZoneID = rec.ZoneID
			}
			if rec.CurrentIP != "" {
				t.IPs = append(t.IPs, strings.ToLower(strings.Trim(rec.CurrentIP, "[]")))
			}
			switch strings.ToUpper(rec.RecordType) {
			case "A":
				ownA = true
			case "AAAA":
				if ip := net.ParseIP(strings.TrimSpace(rec.CurrentIP)); ip != nil && ip.To4() == nil {
					v6 = ip.String()
				}
			}
		}
		t.V6Only = v6 != "" && !ownA && !hasA[d]
		out = append(out, t)
	}
	return out
}

// Target: the target of a name's record key (ok=false: the node has no
// such DDNS domain any more).
func (n UserSubdomainNode) Target(key string) (UserSubdomainTarget, bool) {
	for _, t := range n.Targets {
		if t.Key == key {
			return t, true
		}
	}
	return UserSubdomainTarget{}, false
}

// ---- 已有转发保持原地址 ----

// userSubdomainKeepSet: node → kind → ref id → ref created_time.
type userSubdomainKeepSet map[int64]map[string]map[int64]int64

func (k userSubdomainKeepSet) has(nodeID int64, kind string, refID, created int64) bool {
	c, ok := k[nodeID][kind][refID]
	return ok && c == created
}

func (k userSubdomainKeepSet) add(nodeID int64, kind string, refID, created int64) {
	if k[nodeID] == nil {
		k[nodeID] = map[string]map[int64]int64{}
	}
	if k[nodeID][kind] == nil {
		k[nodeID][kind] = map[int64]int64{}
	}
	k[nodeID][kind][refID] = created
}

// loadUserSubdomainKeep reads the keep rows of the nodes (userIDs != nil:
// only those users'). One query.
func (r *Repository) loadUserSubdomainKeep(nodeIDs, userIDs []int64) (userSubdomainKeepSet, error) {
	out := userSubdomainKeepSet{}
	if len(nodeIDs) == 0 || (userIDs != nil && len(userIDs) == 0) {
		return out, nil
	}
	q := r.db.Model(&model.UserSubdomainKeep{}).Where("node_id IN ?", nodeIDs)
	if userIDs != nil {
		q = q.Where("user_id IN ?", userIDs)
	}
	var rows []model.UserSubdomainKeep
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, x := range rows {
		out.add(x.NodeID, x.Kind, x.RefID, x.RefCreated)
	}
	return out, nil
}

// userSubdomainKeepRow is one forward / landing node on a node (with its
// created_time: a recreated one is new).
type userSubdomainKeepRow struct {
	NodeID  int64
	UserID  int64
	RefID   int64
	Created int64
}

// userSubdomainForwardsOn / userSubdomainLandingsOn: forwards with a port
// on the nodes, native landing nodes on their landing servers.
func userSubdomainForwardsOn(db *gorm.DB, nodeIDs []int64) ([]userSubdomainKeepRow, error) {
	var rows []userSubdomainKeepRow
	err := db.Table("forward_port").
		Select("DISTINCT forward_port.node_id AS node_id, forward.user_id AS user_id, forward.id AS ref_id, forward.created_time AS created").
		Joins("JOIN forward ON forward.id = forward_port.forward_id").
		Where("forward_port.node_id IN ?", nodeIDs).Scan(&rows).Error
	return rows, err
}

func userSubdomainLandingsOn(db *gorm.DB, nodeIDs []int64, withPending bool) ([]userSubdomainKeepRow, error) {
	var rows []userSubdomainKeepRow
	q := db.Table("landing_node").
		Select("DISTINCT landing_server.node_id AS node_id, landing_node.user_id AS user_id, landing_node.id AS ref_id, landing_node.created_time AS created").
		Joins("JOIN landing_server ON landing_server.id = landing_node.landing_server_id").
		Where("landing_server.node_id IN ?", nodeIDs)
	if !withPending {
		q = q.Where("landing_node.delete_pending = ?", false)
	}
	err := q.Scan(&rows).Error
	return rows, err
}

// EnableNodeUserSubdomain turns a node's toggle on in one transaction with
// its keep set: keep=true lists every forward / native landing on the node
// now (已有转发保持原地址: they keep their address), keep=false clears the set
// (full migration). Returns the kept forwards and landings.
func (r *Repository) EnableNodeUserSubdomain(nodeID int64, keep bool) (forwards, landings int, err error) {
	now := time.Now().UnixMilli()
	err = r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("node_id = ?", nodeID).Delete(&model.UserSubdomainKeep{}).Error; err != nil {
			return err
		}
		if keep {
			for _, kind := range []string{model.UserSubdomainKeepForward, model.UserSubdomainKeepLanding} {
				var rows []userSubdomainKeepRow
				var err error
				if kind == model.UserSubdomainKeepForward {
					rows, err = userSubdomainForwardsOn(tx, []int64{nodeID})
				} else {
					rows, err = userSubdomainLandingsOn(tx, []int64{nodeID}, true)
				}
				if err != nil {
					return err
				}
				for _, x := range rows {
					row := model.UserSubdomainKeep{NodeID: nodeID, Kind: kind, RefID: x.RefID, RefCreated: x.Created, UserID: x.UserID, CreatedTime: now}
					if err := tx.Create(&row).Error; err != nil {
						return err
					}
					if kind == model.UserSubdomainKeepForward {
						forwards++
					} else {
						landings++
					}
				}
			}
		}
		return tx.Model(&model.Node{}).Where("id = ?", nodeID).Update("user_subdomain_enabled", true).Error
	})
	return forwards, landings, err
}

// liveUserSubdomainKeep: the keep rows whose forward / landing node still
// exists with the same created_time (all rows when nodeIDs is nil).
func (r *Repository) liveUserSubdomainKeep(nodeIDs []int64) ([]model.UserSubdomainKeep, []int64, error) {
	q := r.db.Model(&model.UserSubdomainKeep{})
	if nodeIDs != nil {
		if len(nodeIDs) == 0 {
			return nil, nil, nil
		}
		q = q.Where("node_id IN ?", nodeIDs)
	}
	var rows []model.UserSubdomainKeep
	if err := q.Order("node_id ASC, id ASC").Find(&rows).Error; err != nil || len(rows) == 0 {
		return nil, nil, err
	}
	ids := map[string][]int64{}
	for _, x := range rows {
		ids[x.Kind] = append(ids[x.Kind], x.RefID)
	}
	created := map[string]map[int64]int64{model.UserSubdomainKeepForward: {}, model.UserSubdomainKeepLanding: {}}
	if list := ids[model.UserSubdomainKeepForward]; len(list) > 0 {
		var fs []model.Forward
		if err := r.db.Select("id", "created_time").Where("id IN ?", list).Find(&fs).Error; err != nil {
			return nil, nil, err
		}
		for _, f := range fs {
			created[model.UserSubdomainKeepForward][f.ID] = f.CreatedTime
		}
	}
	if list := ids[model.UserSubdomainKeepLanding]; len(list) > 0 {
		var ls []model.LandingNode
		if err := r.db.Select("id", "created_time").Where("id IN ?", list).Find(&ls).Error; err != nil {
			return nil, nil, err
		}
		for _, l := range ls {
			created[model.UserSubdomainKeepLanding][l.ID] = l.CreatedTime
		}
	}
	live := []model.UserSubdomainKeep{}
	gone := []int64{}
	for _, x := range rows {
		if c, ok := created[x.Kind][x.RefID]; ok && c == x.RefCreated {
			live = append(live, x)
		} else {
			gone = append(gone, x.ID)
		}
	}
	return live, gone, nil
}

// PruneUserSubdomainKeep deletes keep rows whose forward / landing node is
// gone or was recreated (full pass; a handful of rows).
func (r *Repository) PruneUserSubdomainKeep() error {
	_, gone, err := r.liveUserSubdomainKeep(nil)
	if err != nil || len(gone) == 0 {
		return err
	}
	return r.db.Where("id IN ?", gone).Delete(&model.UserSubdomainKeep{}).Error
}

// UserSubdomainKeptCounts: per node, kept forwards / landings that still
// exist (node list pill, dialog).
func (r *Repository) UserSubdomainKeptCounts() (map[int64]int, error) {
	live, _, err := r.liveUserSubdomainKeep(nil)
	if err != nil {
		return nil, err
	}
	out := map[int64]int{}
	for _, x := range live {
		out[x.NodeID]++
	}
	return out, nil
}

// UserSubdomainKeptForward is one forward kept on its old address (admin).
type UserSubdomainKeptForward struct {
	ForwardID int64  `json:"forwardId"`
	Name      string `json:"name"`
	UserID    int64  `json:"userId"`
	UserName  string `json:"userName"`
	Port      int    `json:"port"`
}

// UserSubdomainKeptOn: the node's forwards kept on their old address that
// still exist, and the count of kept native landing nodes.
func (r *Repository) UserSubdomainKeptOn(nodeID int64) ([]UserSubdomainKeptForward, int, error) {
	live, _, err := r.liveUserSubdomainKeep([]int64{nodeID})
	if err != nil {
		return nil, 0, err
	}
	out := []UserSubdomainKeptForward{}
	ids, landings := []int64{}, 0
	for _, x := range live {
		if x.Kind == model.UserSubdomainKeepForward {
			ids = append(ids, x.RefID)
		} else {
			landings++
		}
	}
	if len(ids) == 0 {
		return out, landings, nil
	}
	type row struct {
		ID       int64
		Name     string
		UserID   int64
		UserName string
		Port     int
	}
	var rows []row
	if err := r.db.Table("forward").
		Select("forward.id AS id, forward.name AS name, forward.user_id AS user_id, u.user AS user_name, forward_port.port AS port").
		Joins("JOIN forward_port ON forward_port.forward_id = forward.id AND forward_port.node_id = ?", nodeID).
		Joins(`LEFT JOIN "user" AS u ON u.id = forward.user_id`).
		Where("forward.id IN ?", ids).Order("forward.user_id ASC, forward.id ASC").Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	seen := map[int64]bool{}
	for _, x := range rows {
		if seen[x.ID] {
			continue
		}
		seen[x.ID] = true
		out = append(out, UserSubdomainKeptForward{ForwardID: x.ID, Name: x.Name, UserID: x.UserID, UserName: x.UserName, Port: x.Port})
	}
	return out, landings, nil
}
