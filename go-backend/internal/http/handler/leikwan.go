package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/leikwan"
	"go-backend/internal/store/model"
)

// SetLeiKwanClient permits embedding the panel with a fake API in integration
// tests. Production always constructs the client with the official fixed URL.
func (h *Handler) SetLeiKwanClient(c *leikwan.Client) { h.leikwan = c }

// Fixed stripes keep per-pipe serialization bounded even across deleted pipes
// or invalid request IDs. Normal tunnel runtime locks remain independent.
func (h *Handler) lockLeiKwanPipe(id int64) func() {
	mu := &h.leikwanPipeLocks[uint64(id)%uint64(len(h.leikwanPipeLocks))]
	mu.Lock()
	return mu.Unlock
}

func (h *Handler) registerLeiKwan(mux *http.ServeMux) {
	for path, fn := range map[string]http.HandlerFunc{
		"account/list": h.leikwanAccountList, "account/save": h.leikwanAccountSave, "account/delete": h.leikwanAccountDelete,
		"overview": h.leikwanOverview, "forward/detail": h.leikwanForwardDetail, "pipe/takeover": h.leikwanTakeover,
		"pipe/create": h.leikwanCreate, "pipe/retarget": h.leikwanRetarget, "pipe/delete": h.leikwanDelete, "pipe/check": h.leikwanPipeCheck,
	} {
		mux.HandleFunc("/api/v1/leikwan/"+path, func(w http.ResponseWriter, r *http.Request) {
			// Preserve the panel envelope and also make the HTTP authorization status explicit.
			if _, role, err := userRoleFromRequest(r); err == nil && role != 0 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
			}
			if !h.ensureAdminAccess(w, r) {
				return
			}
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				response.WriteJSON(w, response.Err(405, "仅支持 POST"))
				return
			}
			fn(w, r)
		})
	}
}
func leiKwanDecode(r *http.Request, v interface{}) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("请求参数错误")
	}
	var extra interface{}
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("请求参数错误")
	}
	return nil
}
func leiKwanError(w http.ResponseWriter, err error) {
	code := -1
	var apiErr *leikwan.APIError
	if errors.As(err, &apiErr) {
		code = apiErr.Status
		if apiErr.Status == 429 {
			w.Header().Set("Retry-After", fmt.Sprint(int(apiErr.RetryAfter.Seconds())+1))
		}
	}
	response.WriteJSON(w, response.Err(code, err.Error()))
}
func leiKwanAccountView(a model.LeiKwanAccount) map[string]interface{} {
	return map[string]interface{}{"id": a.ID, "name": a.Name, "serviceId": a.ServiceID, "tokenConfigured": a.Token != "", "updatedTime": a.UpdatedTime}
}
func (h *Handler) leikwanAccountList(w http.ResponseWriter, r *http.Request) {
	as, err := h.repo.ListLeiKwanAccounts()
	if err != nil {
		leiKwanError(w, errors.New("读取账号失败"))
		return
	}
	out := make([]map[string]interface{}, 0, len(as))
	for _, a := range as {
		out = append(out, leiKwanAccountView(a))
	}
	response.WriteJSON(w, response.OK(out))
}
func (h *Handler) leikwanAccountSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		ServiceID int64  `json:"serviceId"`
		Token     string `json:"token"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.ID < 0 || req.Name == "" || len(req.Name) > 100 || req.ServiceID <= 0 || len(req.Token) > 2048 || strings.ContainsAny(req.Token, " \r\n\t") {
		leiKwanError(w, errors.New("账号名称、服务 ID 或 API token 无效"))
		return
	}
	a := &model.LeiKwanAccount{ID: req.ID, Name: req.Name, ServiceID: req.ServiceID, Token: req.Token}
	if req.ID > 0 {
		old, err := h.repo.GetLeiKwanAccount(req.ID)
		if err != nil || old == nil {
			leiKwanError(w, errors.New("账号不存在"))
			return
		}
		if old.ServiceID != req.ServiceID {
			ps, err := h.repo.ListLeiKwanPipes(req.ID)
			if err != nil || len(ps) > 0 {
				leiKwanError(w, errors.New("账号已有管道，不能更换服务 ID"))
				return
			}
		}
		if a.Token == "" {
			a.Token = old.Token
		}
		h.leikwan.Invalidate(old.Token, old.ServiceID)
	}
	if a.Token == "" {
		leiKwanError(w, errors.New("请填写 API token"))
		return
	}
	if err := h.repo.SaveLeiKwanAccount(a); err != nil {
		leiKwanError(w, errors.New("保存账号失败"))
		return
	}
	response.WriteJSON(w, response.OK(leiKwanAccountView(*a)))
}
func (h *Handler) leikwanAccountDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	if err := h.repo.DeleteLeiKwanAccount(req.ID); err != nil {
		leiKwanError(w, err)
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}
func (h *Handler) leiKwanAccount(id int64) (*model.LeiKwanAccount, error) {
	a, err := h.repo.GetLeiKwanAccount(id)
	if err != nil || a == nil {
		return nil, errors.New("利群账号不存在")
	}
	return a, nil
}
func (h *Handler) leikwanOverview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID int64 `json:"accountId"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	a, err := h.leiKwanAccount(req.AccountID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	view, err := h.leikwan.Overview(r.Context(), a.Token, a.ServiceID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	pipes, err := h.repo.ListLeiKwanPipes(a.ID)
	if err != nil {
		leiKwanError(w, errors.New("读取管道失败"))
		return
	}
	byID := map[int64]model.LeiKwanPipe{}
	for _, p := range pipes {
		byID[p.ForwardID] = p
	}
	type forwardView struct {
		leikwan.Forward
		Pipe       *model.LeiKwanPipe `json:"pipe"`
		TunnelName string             `json:"tunnelName"`
	}
	forwards := make([]forwardView, 0, len(view.Forwards))
	present := make(map[int64]bool, len(view.Forwards))
	for _, f := range view.Forwards {
		present[f.ID] = true
		item := forwardView{Forward: f}
		if p, ok := byID[f.ID]; ok {
			item.Pipe = &p
			if p.TunnelID != nil {
				item.TunnelName = h.repo.LeiKwanTunnelName(*p.TunnelID)
			}
		}
		forwards = append(forwards, item)
	}
	// Pipes FLVX still has but LeiKwan no longer lists (deleted on the LeiKwan
	// side): shown separately so their tunnels are not silently dead. Display
	// only — cleanup stays an explicit admin action (tunnel delete / pipe delete).
	type missingView struct {
		Pipe       model.LeiKwanPipe `json:"pipe"`
		TunnelName string            `json:"tunnelName"`
	}
	missing := make([]missingView, 0)
	for _, p := range pipes {
		if present[p.ForwardID] {
			continue
		}
		item := missingView{Pipe: p}
		if p.TunnelID != nil {
			item.TunnelName = h.repo.LeiKwanTunnelName(*p.TunnelID)
		}
		missing = append(missing, item)
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"account": leiKwanAccountView(*a), "options": view.Options, "forwards": forwards, "missingPipes": missing}))
}
func (h *Handler) leikwanForwardDetail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID int64 `json:"accountId"`
		ForwardID int64 `json:"forwardId"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	a, err := h.leiKwanAccount(req.AccountID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	f, tag, err := h.leikwan.GetForward(r.Context(), a.Token, req.ForwardID)
	if err == nil && f.ServiceID != a.ServiceID {
		err = errors.New("转发不属于此服务")
	}
	if err != nil {
		leiKwanError(w, err)
		return
	}
	response.WriteJSON(w, response.OK(map[string]interface{}{"forward": f, "etag": tag}))
}

var leiKwanKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)
var leiKwanExcluded = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32")}

func leiKwanPublicHost(n *model.NodeRecord) (string, error) {
	if n == nil || n.IsRemote != 0 {
		return "", errors.New("请选择本面板的落地节点")
	}
	for _, raw := range []string{n.ServerIPv4, n.ServerIPv6, n.ServerIP} {
		ip, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil || ip.Zone() != "" {
			continue
		}
		ip = ip.Unmap()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		reserved := false
		for _, prefix := range leiKwanExcluded {
			if prefix.Contains(ip) {
				reserved = true
				break
			}
		}
		if !reserved {
			return ip.String(), nil
		}
	}
	return "", errors.New("落地节点必须配置公网 IP，不能使用内网地址或域名")
}
func (h *Handler) leiKwanTarget(nodeID int64, port int) (*model.NodeRecord, leikwan.Target, error) {
	if port < 1 || port > 65535 {
		return nil, leikwan.Target{}, errors.New("落地端口必须为 1–65535")
	}
	n, err := h.repo.GetNodeRecord(nodeID)
	if err != nil {
		return nil, leikwan.Target{}, errors.New("落地节点不存在")
	}
	host, err := leiKwanPublicHost(n)
	return n, leikwan.Target{Source: "manual", Host: host, Port: port}, err
}
func leiKwanForwardUsable(f leikwan.Forward) error {
	if f.ID <= 0 || f.ListenPort < 1 || f.ListenPort > 65535 || f.State != "active" || f.NativeOutbound || (f.Transport != "tcp" && f.Transport != "tcp+udp") {
		return errors.New("该转发不能作为 FLVX 管道（需要非 Native Outbound 的 TCP 转发）")
	}
	return nil
}
func leiKwanPipeFromForward(p model.LeiKwanPipe, f leikwan.Forward, tag string, nodeID int64, port int) *model.LeiKwanPipe {
	p.ForwardID = f.ID
	p.LandingNodeID = nodeID
	p.LandingPort = port
	p.ListenPort = f.ListenPort
	p.RouteID = f.Route.ID
	p.RouteLabel = f.Route.Label
	p.Transport = f.Transport
	p.TargetHost = f.Target.Host
	p.ETag = tag
	p.UpdatedTime = time.Now().UnixMilli()
	return &p
}
func sameLeiKwanTarget(a, b leikwan.Target) bool { return a.Host == b.Host && a.Port == b.Port }

type leiKwanChangeRequest struct {
	AccountID      int64          `json:"accountId"`
	ForwardID      int64          `json:"forwardId"`
	PipeID         int64          `json:"pipeId"`
	LandingNodeID  int64          `json:"landingNodeId"`
	LandingPort    int            `json:"landingPort"`
	ExpectedTarget leikwan.Target `json:"expectedTarget"`
	ETag           string         `json:"etag"`
	Confirmed      bool           `json:"confirmed"`
}

func (h *Handler) leikwanTakeover(w http.ResponseWriter, r *http.Request) {
	var req leiKwanChangeRequest
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	if !req.Confirmed || req.ETag == "" {
		leiKwanError(w, errors.New("请确认转发 ID、线路及新旧目标；此操作影响所有设备"))
		return
	}
	a, err := h.leiKwanAccount(req.AccountID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	_, target, err := h.leiKwanTarget(req.LandingNodeID, req.LandingPort)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	unlock := h.lockLeiKwanPipe(req.ForwardID)
	defer unlock()
	existing, err := h.repo.GetLeiKwanPipeByForwardID(req.ForwardID)
	if err != nil || existing != nil {
		leiKwanError(w, errors.New("此转发已经被接管"))
		return
	}
	f, tag, err := h.leikwan.GetForward(r.Context(), a.Token, req.ForwardID)
	if err == nil && f.ServiceID != a.ServiceID {
		err = errors.New("转发不属于此服务")
	}
	if err == nil {
		err = leiKwanForwardUsable(f)
	}
	if err == nil && (tag != req.ETag || !sameLeiKwanTarget(f.Target, req.ExpectedTarget)) {
		err = &leikwan.APIError{Status: 412, Code: "forward_version_conflict"}
	}
	if err != nil {
		leiKwanError(w, err)
		return
	}
	if !sameLeiKwanTarget(f.Target, target) {
		f, tag, err = h.leikwan.PatchTarget(r.Context(), a.Token, f.ID, tag, target)
		h.leikwan.Invalidate(a.Token, a.ServiceID)
	}
	if err != nil {
		leiKwanError(w, err)
		return
	}
	p := leiKwanPipeFromForward(model.LeiKwanPipe{AccountID: a.ID}, f, tag, req.LandingNodeID, req.LandingPort)
	if err := h.repo.SaveLeiKwanPipe(p); err != nil {
		leiKwanError(w, errors.New("平台已保存，保存管道失败；请刷新后重新接管"))
		return
	}
	response.WriteJSON(w, response.OK(p))
}

// patchLeiKwanPipe does no repository access so the normal tunnel transaction
// can persist its allocated landing port atomically with the pipe binding.
func (h *Handler) patchLeiKwanPipe(ctx context.Context, a *model.LeiKwanAccount, p *model.LeiKwanPipe, n *model.NodeRecord, port int) (*model.LeiKwanPipe, error) {
	host, err := leiKwanPublicHost(n)
	if err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, errors.New("落地端口无效")
	}
	f, tag, err := h.leikwan.GetForward(ctx, a.Token, p.ForwardID)
	if err != nil {
		return nil, err
	}
	if f.ServiceID != a.ServiceID {
		return nil, errors.New("转发不属于此服务")
	}
	if err := leiKwanForwardUsable(f); err != nil {
		return nil, err
	}
	if f.ListenPort != p.ListenPort || f.Transport != p.Transport || (p.RouteID > 0 && f.Route.ID != p.RouteID) {
		return nil, errors.New("利群转发线路、监听端口或传输已在平台改变，请先检查管道")
	}
	target := leikwan.Target{Source: "manual", Host: host, Port: port}
	if !sameLeiKwanTarget(f.Target, target) {
		if f.Target.Host != p.TargetHost || f.Target.Port != p.LandingPort {
			return nil, &leikwan.APIError{Status: 412, Code: "forward_version_conflict"}
		}
		f, tag, err = h.leikwan.PatchTarget(ctx, a.Token, p.ForwardID, tag, target)
		h.leikwan.Invalidate(a.Token, a.ServiceID)
		if err != nil {
			// The platform may have committed before a timeout/response loss.
			// Return the attempted target so the tunnel transaction can perform
			// a conditional compensation after rollback, even in that case.
			pending := *p
			pending.TargetHost, pending.LandingNodeID, pending.LandingPort = host, n.ID, port
			return &pending, err
		}
	}
	return leiKwanPipeFromForward(*p, f, tag, n.ID, port), nil
}
func (h *Handler) retargetLeiKwanPipe(ctx context.Context, pipeID, nodeID int64, port int) (*model.LeiKwanPipe, error) {
	p, err := h.repo.GetLeiKwanPipe(pipeID)
	if err != nil || p == nil {
		return nil, errors.New("管道不存在")
	}
	a, err := h.leiKwanAccount(p.AccountID)
	if err != nil {
		return nil, err
	}
	n, _, err := h.leiKwanTarget(nodeID, port)
	if err != nil {
		return nil, err
	}
	updated, err := h.patchLeiKwanPipe(ctx, a, p, n, port)
	if err != nil {
		return nil, err
	}
	if err := h.repo.SaveLeiKwanPipe(updated); err != nil {
		return nil, errors.New("平台已保存，保存管道失败，请重试原操作")
	}
	return updated, nil
}
func (h *Handler) leikwanRetarget(w http.ResponseWriter, r *http.Request) {
	var req leiKwanChangeRequest
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	if !req.Confirmed || req.ETag == "" {
		leiKwanError(w, errors.New("请确认转发 ID、线路及新旧目标；此操作影响所有设备"))
		return
	}
	unlock := h.lockLeiKwanPipe(req.PipeID)
	defer unlock()
	p, err := h.repo.GetLeiKwanPipe(req.PipeID)
	if err != nil || p == nil {
		leiKwanError(w, errors.New("管道不存在"))
		return
	}
	if p.TunnelID != nil {
		leiKwanError(w, errors.New("此管道已用于隧道，请编辑隧道的落地节点以同步部署"))
		return
	}

	a, err := h.leiKwanAccount(p.AccountID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	_, target, err := h.leiKwanTarget(req.LandingNodeID, req.LandingPort)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	f, tag, err := h.leikwan.GetForward(r.Context(), a.Token, p.ForwardID)
	if err == nil && f.ServiceID != a.ServiceID {
		err = errors.New("转发不属于此服务")
	}
	if err == nil {
		err = leiKwanForwardUsable(f)
	}
	if err == nil && (req.ETag != tag || !sameLeiKwanTarget(req.ExpectedTarget, f.Target)) {
		err = &leikwan.APIError{Status: 412, Code: "forward_version_conflict"}
	}
	if err != nil {
		leiKwanError(w, err)
		return
	}
	if !sameLeiKwanTarget(f.Target, target) {
		f, tag, err = h.leikwan.PatchTarget(r.Context(), a.Token, p.ForwardID, tag, target)
		h.leikwan.Invalidate(a.Token, a.ServiceID)
	}
	if err != nil {
		leiKwanError(w, err)
		return
	}
	updated := leiKwanPipeFromForward(*p, f, tag, req.LandingNodeID, req.LandingPort)
	if err := h.repo.SaveLeiKwanPipe(updated); err != nil {
		leiKwanError(w, errors.New("平台已保存，保存管道失败，请重试原操作"))
		return
	}

	response.WriteJSON(w, response.OK(updated))
}
func (h *Handler) leikwanCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID     int64  `json:"accountId"`
		RouteID       int64  `json:"routeId"`
		Transport     string `json:"transport"`
		ListenPort    *int   `json:"listenPort"`
		LandingNodeID int64  `json:"landingNodeId"`
		LandingPort   int    `json:"landingPort"`
		RequestID     string `json:"requestId"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	if !leiKwanKeyPattern.MatchString(req.RequestID) || req.RouteID <= 0 || (req.Transport != "tcp" && req.Transport != "tcp+udp") || (req.ListenPort != nil && (*req.ListenPort < 1 || *req.ListenPort > 65535)) {
		leiKwanError(w, errors.New("线路、传输、监听端口或请求标识无效"))
		return
	}
	a, err := h.leiKwanAccount(req.AccountID)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	_, target, err := h.leiKwanTarget(req.LandingNodeID, req.LandingPort)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	body := leikwan.CreateRequest{RouteID: req.RouteID, ListenPort: req.ListenPort, Transport: req.Transport, Target: target}
	raw, _ := json.Marshal(body)
	create := &model.LeiKwanCreate{AccountID: a.ID, RequestID: req.RequestID, Payload: string(raw), LandingNodeID: req.LandingNodeID}
	previous, err := h.repo.GetLeiKwanCreate(a.ID, req.RequestID)
	if err != nil {
		leiKwanError(w, errors.New("读取创建记录失败"))
		return
	}
	if previous == nil {
		view, err := h.leikwan.Overview(r.Context(), a.Token, a.ServiceID)
		if err != nil {
			leiKwanError(w, err)
			return
		}
		available := false
		for _, route := range view.Options.Routes {
			if route.ID == req.RouteID && route.Transports[req.Transport].Available {
				available = true
			}
		}
		if !view.Options.Capabilities.AllowManage || !available {
			leiKwanError(w, errors.New("服务不允许创建转发或所选线路传输不可用"))
			return
		}
		if view.Options.ActiveForwardCount >= view.Options.ForwardLimit {
			leiKwanError(w, errors.New("服务转发配额已用完"))
			return
		}
	}
	if err := h.repo.PrepareLeiKwanCreate(create); err != nil {
		leiKwanError(w, err)
		return
	}
	if create.ForwardID > 0 {
		p, _ := h.repo.GetLeiKwanPipeByForward(a.ID, create.ForwardID)
		if p != nil {
			response.WriteJSON(w, response.OK(p))
			return
		}
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", a.ID, req.RequestID)))
	f, tag, err := h.leikwan.CreateForward(r.Context(), a.Token, a.ServiceID, hex.EncodeToString(sum[:]), body)
	if err != nil {
		leiKwanError(w, err)
		return
	}
	if err := leiKwanForwardUsable(f); err != nil {
		leiKwanError(w, err)
		return
	}
	if f.ServiceID != a.ServiceID || !sameLeiKwanTarget(f.Target, target) || f.Transport != req.Transport || f.Route.ID != req.RouteID {
		leiKwanError(w, errors.New("平台创建结果与请求不一致，请保留请求标识并检查平台"))
		return
	}
	p, err := h.repo.GetLeiKwanPipeByForward(a.ID, f.ID)
	if err != nil {
		leiKwanError(w, errors.New("读取管道失败，请重试原请求"))
		return
	}
	if p == nil {
		p = leiKwanPipeFromForward(model.LeiKwanPipe{AccountID: a.ID}, f, tag, req.LandingNodeID, req.LandingPort)
		if err := h.repo.SaveLeiKwanPipe(p); err != nil {
			leiKwanError(w, errors.New("平台已保存，请重试原请求完成管道记录"))
			return
		}
	}
	if err := h.repo.CompleteLeiKwanCreate(create.ID, f.ID); err != nil {
		leiKwanError(w, errors.New("平台已保存，请重试原请求完成记录"))
		return
	}
	response.WriteJSON(w, response.OK(p))
}
func (h *Handler) deleteLeiKwanPipe(ctx context.Context, pipeID int64) error {
	p, err := h.repo.GetLeiKwanPipe(pipeID)
	if err != nil || p == nil {
		return errors.New("管道不存在")
	}
	a, err := h.leiKwanAccount(p.AccountID)
	if err != nil {
		return err
	}
	f, tag, err := h.leikwan.GetForward(ctx, a.Token, p.ForwardID)
	if err != nil {
		var apiErr *leikwan.APIError
		if errors.As(err, &apiErr) && apiErr.Status == 404 {
			return h.repo.DeleteLeiKwanPipe(p.ID)
		}
		return err
	}
	if tag != p.ETag || f.Target.Host != p.TargetHost || f.Target.Port != p.LandingPort {
		return &leikwan.APIError{Status: 412, Code: "forward_version_conflict"}
	}
	if err := h.leikwan.DeleteForward(ctx, a.Token, p.ForwardID, tag); err != nil {
		return err
	}
	h.leikwan.Invalidate(a.Token, a.ServiceID)
	return h.repo.DeleteLeiKwanPipe(p.ID)
}
func (h *Handler) leikwanDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PipeID    int64  `json:"pipeId"`
		ETag      string `json:"etag"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := leiKwanDecode(r, &req); err != nil {
		leiKwanError(w, err)
		return
	}
	unlock := h.lockLeiKwanPipe(req.PipeID)
	defer unlock()
	p, err := h.repo.GetLeiKwanPipe(req.PipeID)
	if err != nil || p == nil || p.TunnelID != nil {
		leiKwanError(w, errors.New("管道不存在或仍在隧道使用中"))
		return
	}
	if !req.Confirmed || req.ETag != p.ETag {
		leiKwanError(w, errors.New("请重新确认删除；已删除记录仍计入平台 512 条存储上限"))
		return
	}
	if err := h.deleteLeiKwanPipe(r.Context(), p.ID); err != nil {
		leiKwanError(w, err)
		return
	}
	response.WriteJSON(w, response.OKEmpty())
}
