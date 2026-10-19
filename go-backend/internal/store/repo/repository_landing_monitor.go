package repo

import (
	"errors"
	"time"

	"go-backend/internal/store/model"
	"gorm.io/gorm"
)

// LandingMonitorQuota is one user's per-server quota for the current period.
// Used is zero when the stored row belongs to an older period (it resets on the
// next counter); Limit falls back to the server default unless overridden.
type LandingMonitorQuota struct {
	UsedBytes  int64
	LimitBytes int64
	Paused     bool
}

// LandingMonitorData is everything the 监控 「落地」 tab needs, read with a fixed
// number of indexed queries regardless of the number of servers and nodes.
type LandingMonitorData struct {
	Servers []model.LandingServer
	// Nodes are ordered by server, then id. Admin: all nodes. User: own nodes on
	// visible servers only.
	Nodes []model.LandingNode
	// OwnerNames is filled for admins only (user id → login name).
	OwnerNames map[int64]string
	// NativeOnline maps the agent node id of each native server to its status.
	NativeOnline map[int64]bool
	// IntegrationSyncError is the Custom integration error that overrides the
	// per-server shared sync error, as on the 落地 page.
	IntegrationSyncError string
	// Quotas is filled for normal users only, keyed by landing server id.
	Quotas map[int64]LandingMonitorQuota
}

// ListLandingNodeIDs returns every landing node id. The monitor's in-memory rate
// map is swept against it once a minute; it reads the primary key only.
func (r *Repository) ListLandingNodeIDs() ([]int64, error) {
	out := []int64{}
	err := r.db.Model(&model.LandingNode{}).Order("id ASC").Pluck("id", &out).Error
	return out, err
}

// LandingMonitorSnapshot applies the same visibility as the 落地 page: admins
// see all servers and nodes; a user sees only active servers granted to them
// (directly or through an enabled group) and only their own nodes there.
func (r *Repository) LandingMonitorSnapshot(userID int64, admin bool, now time.Time) (*LandingMonitorData, error) {
	out := &LandingMonitorData{OwnerNames: map[int64]string{}, NativeOnline: map[int64]bool{}, Quotas: map[int64]LandingMonitorQuota{}}
	servers, err := r.ListLandingServers(userID, admin)
	if err != nil {
		return nil, err
	}
	out.Servers = servers
	if len(servers) == 0 {
		out.Nodes = []model.LandingNode{}
		return out, nil
	}
	serverIDs := make([]int64, 0, len(servers))
	nativeNodeIDs := []int64{}
	hasShared := false
	for _, s := range servers {
		serverIDs = append(serverIDs, s.ID)
		if s.Source == "shared" {
			hasShared = true
		} else if s.NodeID > 0 {
			nativeNodeIDs = append(nativeNodeIDs, s.NodeID)
		}
	}

	nodes := []model.LandingNode{}
	q := r.db.Model(&model.LandingNode{})
	if !admin {
		q = q.Where("user_id = ? AND landing_server_id IN ?", userID, serverIDs)
	}
	if err = q.Order("landing_server_id ASC, id ASC").Find(&nodes).Error; err != nil {
		return nil, err
	}
	out.Nodes = nodes

	agentNodes := []model.Node{}
	if len(nativeNodeIDs) > 0 {
		if err = r.db.Where("id IN ?", nativeNodeIDs).Find(&agentNodes).Error; err != nil {
			return nil, err
		}
	}
	nodeQuotaDefault := map[int64]int64{}
	for _, n := range agentNodes {
		out.NativeOnline[n.ID] = n.Status == 1
		nodeQuotaDefault[n.ID] = n.DefaultUserQuotaBytes
	}

	if hasShared {
		cfg, e := r.GetLandingIntegration()
		if e != nil {
			return nil, e
		}
		out.IntegrationSyncError = cfg.SyncError
	}

	if admin {
		ownerIDs := []int64{}
		seen := map[int64]bool{}
		for _, n := range nodes {
			if !seen[n.UserID] {
				seen[n.UserID] = true
				ownerIDs = append(ownerIDs, n.UserID)
			}
		}
		if len(ownerIDs) > 0 {
			users := []model.User{}
			if err = r.db.Where("id IN ?", ownerIDs).Find(&users).Error; err != nil {
				return nil, err
			}
			for _, u := range users {
				out.OwnerNames[u.ID] = u.User
			}
		}
		return out, nil
	}

	var user model.User
	if err = r.db.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, nil
		}
		return nil, err
	}
	period := LandingPeriodKey(user.FlowResetTime, now)
	rows := []model.UserServerQuota{}
	if err = r.db.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	stored := map[ServerQuotaKey]model.UserServerQuota{}
	for _, row := range rows {
		stored[ServerQuotaKey{UserID: userID, Kind: row.ServerKind, RefID: row.ServerRefID}] = row
	}
	for i := range servers {
		s := &servers[i]
		key := landingQuotaKey(userID, s)
		limit := s.DefaultUserQuotaBytes
		if key.Kind == "node" {
			limit = nodeQuotaDefault[s.NodeID]
		}
		quota := LandingMonitorQuota{LimitBytes: limit}
		if row, ok := stored[key]; ok {
			if row.LimitOverride {
				quota.LimitBytes = row.LimitBytes
			}
			if row.PeriodKey == period {
				quota.UsedBytes = row.UsedIn + row.UsedOut
				quota.Paused = row.Paused
			}
		}
		out.Quotas[s.ID] = quota
	}
	return out, nil
}
