package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/landingcustom"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

type sharedRetry struct {
	failures int
	next     time.Time
}
type sharedLandingState struct {
	retries             map[string]sharedRetry
	offline             sharedRetry
	lastList, lastHosts time.Time
	lastPushRevision    map[int64]int64
	hostsHash           string
}

func (h *Handler) sharedState() *sharedLandingState {
	if h.sharedLanding == nil {
		h.sharedLanding = &sharedLandingState{retries: map[string]sharedRetry{}, lastPushRevision: map[int64]int64{}}
	}
	return h.sharedLanding
}
func sharedBackoff(failures int) time.Duration {
	return min(15*time.Second*time.Duration(1<<min(max(failures-1, 0), 6)), 10*time.Minute)
}

// Called with sharedLandingMu held. Per-operation backoff plus a shared transport
// breaker avoids one failed request per server when Custom is unreachable.
func (h *Handler) sharedAttempt(key string, force bool, fn func() error) error {
	state := h.sharedState()
	now := time.Now()
	r := state.retries[key]
	if !force && (now.Before(r.next) || now.Before(state.offline.next)) {
		return errors.New("同步异常，正在退避重试")
	}
	err := fn()
	if err == nil {
		delete(state.retries, key)
		state.offline = sharedRetry{}
		return nil
	}
	r.failures++
	r.next = now.Add(sharedBackoff(r.failures))
	state.retries[key] = r
	var api *landingcustom.APIError
	if !errors.As(err, &api) || (api.Status != 409 && api.Status != 422) {
		state.offline.failures++
		state.offline.next = now.Add(sharedBackoff(state.offline.failures))
	}
	_ = h.repo.SetLandingIntegrationResult(err.Error(), 0)
	return err
}
func (h *Handler) landingIntegrationView() map[string]any {
	cfg, _ := h.repo.GetLandingIntegration()
	v := landingMap(cfg)
	v["hasToken"] = cfg.Token != ""
	return v
}
func (h *Handler) landingIntegrationGet(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	response.WriteJSON(w, response.OK(h.landingIntegrationView()))
}
func (h *Handler) landingIntegrationSave(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		BaseURL string `json:"baseUrl"`
		Token   string `json:"token"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	h.sharedLandingMu.Lock()
	err := h.repo.SaveLandingIntegration(req.BaseURL, strings.TrimSpace(req.Token))
	if err == nil {
		h.sharedLanding = nil
	}
	h.sharedLandingMu.Unlock()
	if err != nil {
		landingError(w, err)
		return
	}
	h.syncSharedIntegration(r.Context(), true)
	response.WriteJSON(w, response.OK(h.landingIntegrationView()))
}
func (h *Handler) landingIntegrationTest(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	var req struct {
		BaseURL string `json:"baseUrl"`
		Token   string `json:"token"`
	}
	if err := landingDecode(r, &req); err != nil {
		landingError(w, err)
		return
	}
	cfg, err := h.repo.GetLandingIntegration()
	if err != nil {
		landingError(w, errors.New("接入设置读取失败"))
		return
	}
	if req.BaseURL == "" {
		req.BaseURL = cfg.BaseURL
	}
	if req.Token == "" {
		req.Token = cfg.Token
	}
	client, err := landingcustom.New(req.BaseURL, req.Token)
	if err != nil {
		landingError(w, err)
		return
	}
	shares, err := client.Shares(r.Context())
	if err != nil {
		landingError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(map[string]any{"shareCount": len(shares)}))
}
func (h *Handler) landingIntegrationSync(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAdminAccess(w, r) {
		return
	}
	h.syncSharedIntegration(r.Context(), true)
	response.WriteJSON(w, response.OK(h.landingIntegrationView()))
}
func (h *Handler) runSharedLandingLoop(ctx context.Context) {
	defer h.jobsWG.Done()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			h.syncSharedIntegration(ctx, false)
			timer.Reset(5 * time.Second)
		}
	}
}

// Network work is serialized separately from the native agent and policy locks.
// Enforcement always runs after unlocking, because it can push a new desired state.
func (h *Handler) syncSharedIntegration(ctx context.Context, force bool) {
	enforce := []func(){}
	configured := false
	policyDue := force
	h.sharedLandingMu.Lock()
	func() {
		cfg, err := h.repo.GetLandingIntegration()
		if err != nil || cfg.Token == "" {
			return
		}
		client, err := landingcustom.New(cfg.BaseURL, cfg.Token)
		if err != nil {
			return
		}
		configured = true
		st := h.sharedState()
		now := time.Now()
		if force || now.Sub(st.lastList) >= time.Minute {
			err = h.sharedAttempt("shares", force, func() error {
				shares, e := client.Shares(ctx)
				if e != nil {
					return e
				}
				if e = h.repo.SyncSharedLandings(shares, now.UnixMilli()); e != nil {
					return e
				}
				st.lastList = now
				policyDue = true
				return nil
			})
			if err != nil {
				return
			} // retain everything from the last complete good response
		}
		hosts, e := h.repo.LandingHostInventory()
		if e != nil {
			err = errors.New("FLVX 主机地址读取失败")
		} else {
			sort.Strings(hosts.Native)
			sort.Strings(hosts.Nodes)
			raw, _ := json.Marshal(hosts)
			if force || string(raw) != st.hostsHash || now.Sub(st.lastHosts) >= 10*time.Minute {
				e = h.sharedAttempt("hosts", force, func() error {
					resolved, resolveErr := resolveSharedHosts(ctx, hosts)
					if resolveErr != nil {
						return resolveErr
					}
					if resolveErr = client.PutHosts(ctx, resolved); resolveErr == nil {
						st.hostsHash = string(raw)
						st.lastHosts = now
					}
					return resolveErr
				})
				if e != nil {
					err = e
				}
			}
		}
		servers, e := h.repo.ListLandingServers(0, true)
		if e != nil {
			return
		}
		for _, s := range servers {
			if s.Source != "shared" {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			if force || now.UnixMilli()-s.SharedLastSyncAt >= time.Minute.Milliseconds() {
				var apply func()
				e = h.sharedAttempt(fmt.Sprintf("status:%d", s.ID), force, func() error {
					var pollErr error
					apply, pollErr = h.pollSharedLanding(ctx, client, s)
					return pollErr
				})
				if apply != nil {
					enforce = append(enforce, apply)
				}
				if e == nil {
					e = h.repo.FinalizeSharedLandingDeletions(s.ID)
				}
				if e != nil {
					err = e
					_ = h.repo.SetSharedSyncError(s.ID, e.Error())
				}
			}
			// Re-evaluate content even if revision was not bumped by an owner rename or renewal.
			if e = h.pushSharedLanding(ctx, client, s, force); e != nil {
				err = e
			}
		}
		if err == nil {
			_ = h.repo.SetLandingIntegrationResult("", st.lastList.UnixMilli())
		}
	}()
	h.sharedLandingMu.Unlock()
	for _, apply := range enforce {
		apply()
	}
	if configured && policyDue {
		h.reconcileLandingPolicy(time.Now())
	}
}
func resolveSharedHosts(ctx context.Context, hosts landingcustom.LandingHosts) (landingcustom.LandingHosts, error) {
	resolve := func(input []string) ([]string, error) {
		set := map[string]bool{}
		for _, host := range input {
			host = strings.Trim(strings.TrimSpace(host), "[]")
			if host == "" {
				continue
			}
			if ip := net.ParseIP(host); ip != nil {
				set[ip.String()] = true
				continue
			}
			child, cancel := context.WithTimeout(ctx, 2*time.Second)
			ips, err := net.DefaultResolver.LookupHost(child, host)
			cancel()
			if err != nil || len(ips) == 0 {
				return nil, errors.New("FLVX 主机地址解析失败，保留上次主机报告")
			}
			set[strings.ToLower(host)] = true
			for _, ip := range ips {
				set[ip] = true
			}
		}
		out := make([]string, 0, len(set))
		for host := range set {
			out = append(out, host)
		}
		sort.Strings(out)
		return out, nil
	}
	var err error
	hosts.Nodes, err = resolve(hosts.Nodes)
	if err != nil {
		return hosts, err
	}
	hosts.Native, err = resolve(hosts.Native)
	return hosts, err
}

func (h *Handler) pushSharedLanding(ctx context.Context, client *landingcustom.Client, s model.LandingServer, force bool) error {
	caps := repo.SharedLandingCaps(&s)
	// Custom explicitly refuses PUT for paused/revoked shares; it stops them itself.
	if caps.Status != "active" {
		return nil
	}
	desired, err := h.repo.SharedLandingDesired(s.ID)
	if err != nil {
		return err
	}
	if !force && desired.Revision == s.SharedPushedRevision && time.Now().UnixMilli()-s.SharedLastPushAt < 10*time.Minute.Milliseconds() {
		return nil
	}
	st := h.sharedState()
	key := fmt.Sprintf("push:%d", s.ID)
	if st.lastPushRevision[s.ID] != desired.Revision {
		delete(st.retries, key)
		st.lastPushRevision[s.ID] = desired.Revision
	}
	err = h.sharedAttempt(key, force, func() error { return client.PutDesired(ctx, s.RemoteServerID, desired) })
	// Transport failure only records sync health, never mutates the nodes.
	_ = h.repo.SetSharedPushResult(s.ID, desired.Revision, time.Now().UnixMilli(), err)
	return err
}
func (h *Handler) pollSharedLanding(ctx context.Context, client *landingcustom.Client, s model.LandingServer) (func(), error) {
	status, err := client.Status(ctx, s.RemoteServerID)
	if err != nil {
		return nil, err
	}
	counters := []repo.LandingCounter{}
	for _, node := range status.Nodes {
		id, e := strconv.ParseInt(node.ID, 10, 64)
		if e != nil || id <= 0 {
			return nil, errors.New("妙妙屋节点标识无效")
		}
		counters = append(counters, repo.LandingCounter{ID: id, ServerID: s.ID, InboundTag: fmt.Sprintf("flvxl-%d", id), Epoch: "1", CollectedAt: status.CollectedAt, UpBytes: node.UpBytes, DownBytes: node.DownBytes, Connections: node.Connections})
	}
	apply, err := h.commitFlowWithLanding(0, nil, counters)
	if err != nil {
		return nil, errors.New("妙妙屋落地流量入账失败，保留原计数重试")
	}
	if err = h.repo.RecordSharedStatus(s.ID, status, time.Now().UnixMilli()); err != nil {
		return apply, errors.New("妙妙屋节点状态保存失败")
	}
	return apply, nil
}
func (h *Handler) syncSharedLandingServer(id int64) error {
	h.sharedLandingMu.Lock()
	defer h.sharedLandingMu.Unlock()
	s, err := h.repo.GetLandingServer(id)
	if err != nil || s == nil {
		return errors.New("共享服务器不存在")
	}
	cfg, err := h.repo.GetLandingIntegration()
	if err != nil {
		return errors.New("接入设置读取失败")
	}
	client, err := landingcustom.New(cfg.BaseURL, cfg.Token)
	if err != nil {
		return err
	}
	return h.pushSharedLanding(context.Background(), client, *s, false)
}

// Refuse new native enablement if Custom cannot currently verify the inventory.
// A failure has no effect on already running native or shared nodes.
func (h *Handler) checkSharedNativeConflict(ctx context.Context, nodeID int64) error {
	cfg, err := h.repo.GetLandingIntegration()
	if err != nil {
		return errors.New("接入设置读取失败")
	}
	if cfg.Token == "" {
		return nil
	}
	h.sharedLandingMu.Lock()
	defer h.sharedLandingMu.Unlock()
	client, err := landingcustom.New(cfg.BaseURL, cfg.Token)
	if err != nil {
		return err
	}
	shares, err := client.Shares(ctx)
	if err != nil {
		return errors.New("无法核验妙妙屋共享，请在连接恢复后启用自有落地")
	}
	n, err := h.repo.GetNodeByID(nodeID)
	if err != nil || n == nil {
		return errors.New("节点不存在")
	}
	own := []string{n.ServerIP, n.ServerIPV4.String, n.ServerIPV6.String, n.IntranetIP.String}
	var extra []string
	if json.Unmarshal([]byte(n.ExtraIPs.String), &extra) != nil {
		extra = strings.FieldsFunc(n.ExtraIPs.String, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' })
	}
	own = append(own, extra...)
	resolved, err := resolveSharedHosts(ctx, landingcustom.LandingHosts{Nodes: own, Native: []string{}})
	if err != nil {
		return err
	}
	set := map[string]bool{}
	for _, host := range resolved.Nodes {
		set[host] = true
	}
	for _, s := range shares {
		if s.Status == "revoked" {
			continue
		}
		remote, e := resolveSharedHosts(ctx, landingcustom.LandingHosts{Nodes: []string{s.Hosts.IPv4, s.Hosts.IPv6, s.Hosts.Domain}, Native: []string{}})
		if e != nil {
			return errors.New("妙妙屋共享地址解析失败，暂不能启用自有落地")
		}
		for _, host := range remote.Nodes {
			if set[host] {
				return errors.New("该机器已由妙妙屋共享给 FLVX，不能同时启用自有落地")
			}
		}
	}
	return nil
}

func (h *Handler) deleteSharedLandingNode(w http.ResponseWriter, r *http.Request, n *model.LandingNode) {
	h.sharedLandingMu.Lock()
	n, err := h.repo.PrepareSharedLandingDeletion(n.ID)
	if err != nil {
		h.sharedLandingMu.Unlock()
		landingError(w, err)
		return
	}
	var enforce func()
	pending := true
	err = func() error {
		s, e := h.repo.GetLandingServer(n.LandingServerID)
		if e != nil || s == nil {
			return errors.New("共享服务器不存在")
		}
		cfg, e := h.repo.GetLandingIntegration()
		if e != nil {
			return errors.New("接入设置读取失败")
		}
		client, e := landingcustom.New(cfg.BaseURL, cfg.Token)
		if e != nil {
			return e
		}
		// Omission requests removal, while keeping the local accounting identity.
		if e = h.pushSharedLanding(r.Context(), client, *s, false); e != nil {
			return e
		}
		enforce, e = h.pollSharedLanding(r.Context(), client, *s)
		if e != nil {
			return e
		}
		if e = h.repo.FinalizeSharedLandingDeletions(s.ID); e != nil {
			return e
		}
		current, e := h.repo.GetLandingNode(n.ID)
		if e != nil {
			return e
		}
		pending = current != nil
		return nil
	}()
	h.sharedLandingMu.Unlock()
	if enforce != nil {
		enforce()
	}
	if err != nil {
		_ = h.repo.SetSharedSyncError(n.LandingServerID, err.Error())
	}
	out := response.OK(map[string]bool{"pending": pending})
	if pending {
		out.Msg = "已提交删除，等待妙妙屋确认最终流量后释放端口"
	}
	response.WriteJSON(w, out)
}
