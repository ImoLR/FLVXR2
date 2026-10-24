package repo

import (
	"go-backend/internal/store/model"
)

// LandingNodeListData batches the data needed by node lists. Query count is
// constant for both the user view and the admin view, including shared nodes.
type LandingNodeListData struct {
	Servers          map[int64]*model.LandingServer
	Users            map[int64]*model.User
	Allowed          map[int64]bool
	ForwardCounts    map[int64]int64
	IntegrationError string
	// ConnSupport (plan 141): native agents' 仅入站 capability per server.
	ConnSupport LandingConnSupport
}

func (r *Repository) LandingNodeListData(nodes []model.LandingNode) (*LandingNodeListData, error) {
	out := &LandingNodeListData{Servers: map[int64]*model.LandingServer{}, Users: map[int64]*model.User{}, Allowed: map[int64]bool{}, ForwardCounts: map[int64]int64{}}
	if len(nodes) == 0 {
		return out, nil
	}
	ids, serverIDs, userIDs := []int64{}, []int64{}, []int64{}
	seenServers, seenUsers := map[int64]bool{}, map[int64]bool{}
	for _, n := range nodes {
		ids = append(ids, n.ID)
		if !seenServers[n.LandingServerID] {
			serverIDs = append(serverIDs, n.LandingServerID)
			seenServers[n.LandingServerID] = true
		}
		if !seenUsers[n.UserID] {
			userIDs = append(userIDs, n.UserID)
			seenUsers[n.UserID] = true
		}
	}
	// The native agent's landing_conn_mode (plan 141) rides on the server
	// query: the list keeps its constant query count.
	var rows []struct {
		model.LandingServer
		AgentConnMode int
	}
	if err := r.db.Model(&model.LandingServer{}).Select("landing_server.*, COALESCE(node.landing_conn_mode, 0) AS agent_conn_mode").
		Joins("LEFT JOIN node ON node.id = landing_server.node_id AND landing_server.source = ?", "native").
		Where("landing_server.id IN ?", serverIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	servers := make([]model.LandingServer, len(rows))
	out.ConnSupport = LandingConnSupport{}
	shared := false
	for i := range rows {
		servers[i] = rows[i].LandingServer
		out.Servers[servers[i].ID] = &servers[i]
		shared = shared || servers[i].Source == "shared"
		if servers[i].Source == "native" {
			out.ConnSupport[servers[i].ID] = rows[i].AgentConnMode
		}
	}
	var users []model.User
	if err := r.db.Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	for i := range users {
		out.Users[users[i].ID] = &users[i]
	}
	var allowed []int64
	// Use the same direct/group grants as landingPermission; admins own all
	// public servers, but creating on behalf of a user uses that user's grants.
	cond := "u.role_id = 0 OR EXISTS (SELECT 1 FROM landing_server_user g WHERE g.landing_server_id = n.landing_server_id AND g.user_id = n.user_id) OR EXISTS (SELECT 1 FROM landing_server_user_group g JOIN user_group_user gu ON gu.user_group_id = g.user_group_id JOIN user_group ug ON ug.id = g.user_group_id WHERE g.landing_server_id = n.landing_server_id AND gu.user_id = n.user_id AND ug.status = 1)"
	if err := r.db.Table("landing_node n").Joins("JOIN \"user\" u ON u.id = n.user_id").Where("n.id IN ?", ids).Where(cond).Pluck("n.id", &allowed).Error; err != nil {
		return nil, err
	}
	for _, id := range allowed {
		out.Allowed[id] = true
	}
	var counts []struct {
		LandingNodeID int64
		Count         int64
	}
	if err := r.db.Model(&model.Forward{}).Select("landing_node_id, COUNT(*) AS count").Where("landing_node_id IN ?", ids).Group("landing_node_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	for _, row := range counts {
		out.ForwardCounts[row.LandingNodeID] = row.Count
	}
	if shared {
		cfg, err := r.GetLandingIntegration()
		if err != nil {
			return nil, err
		}
		out.IntegrationError = cfg.SyncError
	}
	return out, nil
}
