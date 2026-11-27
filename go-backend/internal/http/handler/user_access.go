package handler

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/usersub"
)

// Plan 321: grant removal used to leave the user's per-server row and his
// 专属域名 on that node behind. The cleanup itself runs after every mutating
// POST (UserSubdomainKicker → userAccessCleanup) and on the 10-minute pass;
// this file holds the admin views around it.

// accessCleanupAuto: the automatic cleanup (after every mutating POST and on
// the 10-minute pass) is on unless FLVX_ACCESS_CLEANUP=dry — then nothing
// retires or goes by itself; the admin dry run lists and its apply does it
// (release rehearsals on a prod-DB copy, or a cautious first deploy).
func accessCleanupAuto() bool {
	return strings.ToLower(strings.TrimSpace(os.Getenv("FLVX_ACCESS_CLEANUP"))) != "dry"
}

// userServerGrants: per node row of the user ("node:<id>") the grants whose
// tunnel uses that node (entry, relay or exit). Admins: none (they use all).
func (h *Handler) userServerGrants(userID int64, quotas []model.UserServerQuota, related []repo.RelatedServer) map[string][]repo.UserNodeGrant {
	if userID <= 0 {
		return nil
	}
	if u, err := h.repo.GetUserByID(userID); err != nil || u == nil || u.RoleID == 0 {
		return nil
	}
	want := map[int64]bool{}
	for _, q := range quotas {
		if q.ServerKind == "node" {
			want[q.ServerRefID] = true
		}
	}
	for _, s := range related {
		if s.ServerKind == "node" {
			want[s.ServerRefID] = true
		}
	}
	grants, err := h.repo.UserNodeGrants([]int64{userID}, userSubKeys(want), false)
	if err != nil {
		return nil
	}
	out := map[string][]repo.UserNodeGrant{}
	for k, list := range grants {
		out[fmt.Sprintf("node:%d", k[1])] = list
	}
	return out
}

// userAccessGoneNames: the names the current facts retire for lack of access
// (the plan-321 rule), with their rows.
func (h *Handler) userAccessGoneNames() ([]model.UserSubdomain, error) {
	work, err := h.repo.AnyUserSubdomainWork()
	if err != nil || !work {
		return nil, err
	}
	facts, _, _, err := h.userSubFactsWith(h.userSubState().now(), true, true)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]model.UserSubdomain, len(facts.Names))
	for _, n := range facts.Names {
		byID[n.ID] = n
	}
	var out []model.UserSubdomain
	for _, c := range usersub.Compute(facts).Changes {
		if c.State == model.UserSubdomainRetired && c.Reason == model.UserSubdomainReasonNoAccess {
			out = append(out, byID[c.ID])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

type userAccessNameView struct {
	ID       int64  `json:"id"`
	UserID   int64  `json:"userId"`
	UserName string `json:"userName"`
	NodeID   int64  `json:"nodeId"`
	NodeName string `json:"nodeName"`
	Domain   string `json:"domain"`
	Target   string `json:"target"`
	State    string `json:"state"`
	RecordID string `json:"recordId"`
}

// userAccessOrphans: POST /api/v1/node/user-access/orphans {apply}. Without
// apply a dry run: the names that would retire (no_access) and the per-server
// rows that would go. With apply: does it now (the CF records follow on the
// kicked pass) and returns what it did. Admins only (under /api/v1/node/).
func (h *Handler) userAccessOrphans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		Apply bool `json:"apply"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	names, err := h.userAccessGoneNames()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	rows, err := h.repo.UserServerQuotaOrphans(nil)
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	nodeIDs, userIDs := map[int64]bool{}, map[int64]bool{}
	for _, n := range names {
		nodeIDs[n.NodeID], userIDs[n.UserID] = true, true
	}
	nodeNames, _ := h.repo.NodeNames(userSubKeys(nodeIDs))
	userNames, _ := h.repo.UserNames(userSubKeys(userIDs))
	views := make([]userAccessNameView, 0, len(names))
	for _, n := range names {
		views = append(views, userAccessNameView{ID: n.ID, UserID: n.UserID, UserName: userNames[n.UserID], NodeID: n.NodeID, NodeName: nodeNames[n.NodeID],
			Domain: n.Domain, Target: n.Target, State: n.State, RecordID: n.RecordID})
	}
	out := map[string]interface{}{"apply": req.Apply, "names": views, "serverRows": rows, "auto": accessCleanupAuto()}
	if req.Apply {
		retired := h.userSubRetireGone()
		pruned := h.userServerQuotaPrune()
		out["retired"], out["pruned"] = retired, pruned
		if retired > 0 {
			h.userSubKick()
		}
	}
	response.WriteJSON(w, response.OK(out))
}
