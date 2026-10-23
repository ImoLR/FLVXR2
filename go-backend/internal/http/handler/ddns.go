package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"go-backend/internal/ddns"
	"go-backend/internal/http/response"
	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// Node DDNS API (plan 142). All routes live under /api/v1/node/, which the
// auth middleware restricts to admins.

func (h *Handler) registerDDNS(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/node/ddns/provider/list", h.ddnsProviderList)
	mux.HandleFunc("/api/v1/node/ddns/provider/create", h.ddnsProviderCreate)
	mux.HandleFunc("/api/v1/node/ddns/provider/update", h.ddnsProviderUpdate)
	mux.HandleFunc("/api/v1/node/ddns/provider/delete", h.ddnsProviderDelete)
	mux.HandleFunc("/api/v1/node/ddns/provider/test", h.ddnsProviderTest)
	mux.HandleFunc("/api/v1/node/ddns/sync", h.ddnsSyncNow)
}

const ddnsTokenRecheckEvery = 24 * time.Hour

func ddnsProviderView(p model.DNSProvider, usedBy int64) map[string]interface{} {
	zones := []string{}
	_ = json.Unmarshal([]byte(p.Zones), &zones)
	return map[string]interface{}{
		"id": p.ID, "name": p.Name, "providerType": p.ProviderType,
		"tokenSet": dnsProviderToken(&p) != "", "tokenStatus": p.TokenStatus,
		"tokenExpiresOn": p.TokenExpiresOn, "tokenCheckedAt": p.TokenCheckedAt,
		"tokenError": p.TokenError, "zones": zones, "usedBy": usedBy,
	}
}

func (h *Handler) ddnsProviderList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	providers, err := h.repo.ListDNSProviders()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	usage, _ := h.repo.DNSProviderUsage()
	now := h.ddnsState().now()
	out := make([]map[string]interface{}, 0, len(providers))
	for _, p := range providers {
		out = append(out, ddnsProviderView(p, usage[p.ID]))
		// Token validity is re-checked at most once a day, in the background.
		if dnsProviderToken(&p) != "" && now.Sub(time.UnixMilli(p.TokenCheckedAt)) >= ddnsTokenRecheckEvery {
			h.ddnsCheckTokenAsync(p.ID)
		}
	}
	response.WriteJSON(w, response.OK(out))
}

func (h *Handler) ddnsCheckTokenAsync(providerID int64) {
	m := h.ddnsState()
	m.mu.Lock()
	if m.checking[providerID] {
		m.mu.Unlock()
		return
	}
	m.checking[providerID] = true
	m.mu.Unlock()
	m.pending.Add(1)
	go func() {
		defer m.pending.Done()
		defer func() {
			m.mu.Lock()
			delete(m.checking, providerID)
			m.mu.Unlock()
		}()
		p, err := h.repo.GetDNSProvider(providerID)
		if err != nil || p == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = h.ddnsCheckToken(ctx, p, dnsProviderToken(p), true)
	}()
}

type ddnsTestResult struct {
	Status    string                   `json:"status"`
	ExpiresOn int64                    `json:"expiresOn"`
	CheckedAt int64                    `json:"checkedAt"`
	Error     string                   `json:"error"`
	Zones     []map[string]interface{} `json:"zones"`
}

// ddnsCheckToken verifies a token and lists its zones; store=true writes the
// result to the provider row.
func (h *Handler) ddnsCheckToken(ctx context.Context, p *model.DNSProvider, token string, store bool) (ddnsTestResult, error) {
	m := h.ddnsState()
	probe := *p
	probe.Credentials = dnsProviderCredentials(token)
	client := h.ddnsClient(&probe)
	now := m.now()
	result := ddnsTestResult{CheckedAt: now.UnixMilli(), Zones: []map[string]interface{}{}}
	info, err := client.VerifyToken(ctx)
	var zoneNames []string
	if err == nil {
		result.Status = info.Status
		if !info.ExpiresOn.IsZero() {
			result.ExpiresOn = info.ExpiresOn.UnixMilli()
		}
		if info.Status != "active" {
			err = fmt.Errorf("Token 状态为 %s（不是 active）", info.Status)
		} else {
			zones, zerr := client.ListZones(ctx)
			if zerr != nil {
				err = zerr
			} else {
				zoneNames = []string{}
				for _, z := range zones {
					result.Zones = append(result.Zones, map[string]interface{}{"name": z.Name, "canEdit": z.CanEditDNS(), "status": z.Status})
					if z.CanEditDNS() {
						zoneNames = append(zoneNames, z.Name)
					}
				}
				if p.ID > 0 {
					m.mu.Lock()
					m.zones[p.ID] = ddnsZoneCache{zones: zones, at: now}
					m.mu.Unlock()
				}
			}
		}
	}
	if err != nil {
		result.Error = err.Error()
		if result.Status == "" {
			result.Status = "invalid"
		}
		if !ddns.IsAuthError(err) && result.Status == "invalid" {
			result.Status = "error" // network / API trouble, not a verdict on the token
		}
	}
	if store && p.ID > 0 {
		_ = h.repo.SetDNSProviderTokenCheck(p.ID, result.Status, result.ExpiresOn, result.CheckedAt, result.Error, zoneNames)
	}
	return result, err
}

func dnsProviderCredentials(token string) string {
	raw, _ := json.Marshal(map[string]string{"api_token": strings.TrimSpace(token)})
	return string(raw)
}

func validateDDNSToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("API Token 不能为空")
	}
	if len(token) > 200 || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("API Token 格式不正确")
	}
	return nil
}

func (h *Handler) ddnsProviderCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		Name         string `json:"name"`
		ProviderType string `json:"providerType"`
		Token        string `json:"token"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 100 {
		response.WriteJSON(w, response.ErrDefault("名称不能为空，最多 100 个字"))
		return
	}
	if t := strings.TrimSpace(req.ProviderType); t != "" && t != model.DNSProviderCloudflare {
		response.WriteJSON(w, response.ErrDefault("目前只支持 Cloudflare"))
		return
	}
	if err := validateDDNSToken(req.Token); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	p := &model.DNSProvider{Name: name, ProviderType: model.DNSProviderCloudflare, Credentials: dnsProviderCredentials(req.Token)}
	if err := h.repo.CreateDNSProvider(p); err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	_, _ = h.ddnsCheckToken(ctx, p, req.Token, true)
	saved, _ := h.repo.GetDNSProvider(p.ID)
	if saved == nil {
		saved = p
	}
	response.WriteJSON(w, response.OK(ddnsProviderView(*saved, 0)))
}

func (h *Handler) ddnsProviderUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if err := decodeJSON(r.Body, &req); err != nil || req.ID <= 0 {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 100 {
		response.WriteJSON(w, response.ErrDefault("名称不能为空，最多 100 个字"))
		return
	}
	credentials := ""
	if strings.TrimSpace(req.Token) != "" {
		if err := validateDDNSToken(req.Token); err != nil {
			response.WriteJSON(w, response.ErrDefault(err.Error()))
			return
		}
		credentials = dnsProviderCredentials(req.Token)
	}
	if err := h.repo.UpdateDNSProvider(req.ID, name, credentials); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	saved, err := h.repo.GetDNSProvider(req.ID)
	if err != nil || saved == nil {
		response.WriteJSON(w, response.ErrDefault("DNS 服务商不存在"))
		return
	}
	if credentials != "" {
		h.ddnsForgetZones(req.ID)
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		_, _ = h.ddnsCheckToken(ctx, saved, req.Token, true)
		saved, _ = h.repo.GetDNSProvider(req.ID)
		// Records that failed on the old token get a fresh try.
		h.ddnsRetryProviderNodes(req.ID)
	}
	usage, _ := h.repo.DNSProviderUsage()
	response.WriteJSON(w, response.OK(ddnsProviderView(*saved, usage[req.ID])))
}

func (h *Handler) ddnsRetryProviderNodes(providerID int64) {
	nodes, err := h.repo.ListDDNSNodes()
	if err != nil {
		return
	}
	for _, n := range nodes {
		if n.DDNSProviderID == providerID {
			h.ddnsSyncAsync(n.ID, true)
		}
	}
}

func (h *Handler) ddnsProviderDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	id := idFromBody(r, w)
	if id <= 0 {
		return
	}
	if err := h.repo.DeleteDNSProvider(id); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	h.ddnsForgetZones(id)
	response.WriteJSON(w, response.OKEmpty())
}

// ddnsProviderTest checks a saved provider ({id}) or an unsaved token
// ({token}, optionally with the id of the provider being edited).
func (h *Handler) ddnsProviderTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	p := &model.DNSProvider{ProviderType: model.DNSProviderCloudflare}
	token := strings.TrimSpace(req.Token)
	store := false
	if req.ID > 0 {
		saved, err := h.repo.GetDNSProvider(req.ID)
		if err != nil || saved == nil {
			response.WriteJSON(w, response.ErrDefault("DNS 服务商不存在"))
			return
		}
		p = saved
		if token == "" {
			token, store = dnsProviderToken(saved), true
		}
	}
	if err := validateDDNSToken(token); err != nil {
		response.WriteJSON(w, response.ErrDefault(err.Error()))
		return
	}
	if !store {
		p = &model.DNSProvider{ProviderType: model.DNSProviderCloudflare} // unsaved token: no cache, no write
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, _ := h.ddnsCheckToken(ctx, p, token, store)
	response.WriteJSON(w, response.OK(result))
}

// ddnsSyncNow is 「立即同步」: a forced sync of one node, answered with the
// node's DDNS view.
func (h *Handler) ddnsSyncNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	id := idFromBody(r, w)
	if id <= 0 {
		return
	}
	node, err := h.repo.GetNodeByID(id)
	if err != nil || node == nil {
		response.WriteJSON(w, response.ErrDefault("节点不存在"))
		return
	}
	if !node.DDNSEnabled {
		response.WriteJSON(w, response.ErrDefault("该节点未开启 DDNS"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	h.ddnsSyncNode(ctx, id, true)
	views, err := h.ddnsNodeViews()
	if err != nil {
		response.WriteJSON(w, response.Err(-2, err.Error()))
		return
	}
	response.WriteJSON(w, response.OK(views[id]))
}

// ---- node create / update ----

type ddnsRequestRecord struct {
	Domain   string `json:"domain"`
	Type     string `json:"type"`
	Role     string `json:"role"`
	Source   string `json:"source"`
	Prefix   string `json:"prefix"`
	Takeover bool   `json:"takeover"`
}

type ddnsRequest struct {
	Enabled    bool                `json:"enabled"`
	ProviderID int64               `json:"providerId"`
	Records    []ddnsRequestRecord `json:"records"`
}

// parseDDNSRequest reads the optional `ddns` object of a node save; nil
// means the request does not touch the DDNS settings.
func parseDDNSRequest(req map[string]interface{}) (*ddnsRequest, error) {
	raw, ok := req["ddns"]
	if !ok || raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, errors.New("DDNS 参数错误")
	}
	var out ddnsRequest
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, errors.New("DDNS 参数错误")
	}
	return &out, nil
}

// ddnsPlan is a validated DDNS config ready to be saved.
type ddnsPlan struct {
	enabled    bool
	providerID int64
	records    []model.NodeDDNSRecord
}

// mainDomain returns the domain of the main record of a family ("A"/"AAAA").
func (p *ddnsPlan) mainDomain(recordType string) string {
	if p == nil || !p.enabled {
		return ""
	}
	for _, rec := range p.records {
		if rec.Role == model.DDNSRoleMain && rec.RecordType == recordType {
			return rec.Domain
		}
	}
	return ""
}

func ddnsPlanFromDB(node *model.Node, records []model.NodeDDNSRecord) *ddnsPlan {
	return &ddnsPlan{enabled: node.DDNSEnabled, providerID: node.DDNSProviderID, records: records}
}

func (h *Handler) ddnsPanelHosts(r *http.Request) []string {
	var hosts []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		value = strings.TrimPrefix(strings.TrimPrefix(value, "https://"), "http://")
		if i := strings.IndexAny(value, "/?"); i >= 0 {
			value = value[:i]
		}
		if host, _, err := net.SplitHostPort(value); err == nil {
			value = host
		}
		if value = strings.ToLower(strings.Trim(value, "[]")); value != "" {
			hosts = append(hosts, value)
		}
	}
	if v, err := h.repo.GetViteConfigValue("ip"); err == nil {
		add(v)
	}
	if r != nil {
		add(r.Host)
	}
	return hosts
}

// validateDDNSRequest checks a DDNS config before anything is saved. New or
// changed records are checked against Cloudflare (zone, CNAME, existing
// records pointing elsewhere / proxied unless 「接管已有记录」).
func (h *Handler) validateDDNSRequest(ctx context.Context, r *http.Request, nodeID int64, req *ddnsRequest, known []string) (*ddnsPlan, error) {
	plan := &ddnsPlan{enabled: req.Enabled, providerID: req.ProviderID}
	if len(req.Records) > repo.MaxNodeDDNSRecords {
		return nil, fmt.Errorf("每个节点最多 %d 条 DDNS 记录", repo.MaxNodeDDNSRecords)
	}
	if req.Enabled && len(req.Records) == 0 {
		return nil, errors.New("开启 DDNS 时至少要有一条记录")
	}
	var existing []model.NodeDDNSRecord
	if nodeID > 0 {
		var err error
		if existing, err = h.repo.GetNodeDDNSRecords(nodeID); err != nil {
			return nil, err
		}
	}
	old := make(map[string]model.NodeDDNSRecord, len(existing))
	for _, rec := range existing {
		old[rec.Domain+"|"+rec.RecordType] = rec
	}
	seen := map[string]bool{}
	mains := map[string]bool{}
	panelHosts := h.ddnsPanelHosts(r)
	for i, in := range req.Records {
		domain, err := ddns.NormalizeDomain(in.Domain)
		if err != nil {
			return nil, err
		}
		recordType := strings.ToUpper(strings.TrimSpace(in.Type))
		role := strings.TrimSpace(in.Role)
		if role == "" {
			role = model.DDNSRoleMain
		}
		if role != model.DDNSRoleMain && role != model.DDNSRoleExtra {
			return nil, fmt.Errorf("第 %d 条记录的用途无效", i+1)
		}
		source := strings.TrimSpace(in.Source)
		prefix, err := ddns.ValidateSource(recordType, source, in.Prefix)
		if err != nil {
			return nil, fmt.Errorf("%s：%v", domain, err)
		}
		key := domain + "|" + recordType
		if seen[key] {
			return nil, fmt.Errorf("记录 %s（%s）重复", domain, recordType)
		}
		seen[key] = true
		if role == model.DDNSRoleMain {
			if mains[recordType] {
				return nil, fmt.Errorf("%s 记录只能有一条「主地址」，其余请选「附加入口」", recordType)
			}
			mains[recordType] = true
		}
		if ddns.ProtectedDomain(domain, "", panelHosts) {
			return nil, fmt.Errorf("%s 是面板或其他服务使用的域名，FLVX 不会修改它", domain)
		}
		if owner, err := h.repo.FindDDNSRecordOwner(domain, recordType); err != nil {
			return nil, err
		} else if owner > 0 && owner != nodeID {
			return nil, fmt.Errorf("域名 %s 的 %s 记录已被节点 #%d 使用", domain, recordType, owner)
		}
		rec := model.NodeDDNSRecord{Domain: domain, RecordType: recordType, Role: role, Source: source, SourcePrefix: prefix, Takeover: in.Takeover}
		if prev, ok := old[key]; ok {
			rec.Applied = prev.Applied
		}
		plan.records = append(plan.records, rec)
	}
	if !req.Enabled {
		return plan, nil
	}
	provider, err := h.repo.GetDNSProvider(req.ProviderID)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, errors.New("请选择 DNS 服务商")
	}
	client := h.ddnsClient(provider)
	for _, rec := range plan.records {
		prev, ok := old[rec.Domain+"|"+rec.RecordType]
		if ok && prev.RecordID != "" && (prev.Takeover == rec.Takeover || rec.Takeover) {
			continue // already ours: no Cloudflare round trip on unrelated edits
		}
		zone, err := h.ddnsZoneFor(ctx, client, provider, rec.Domain)
		if err != nil {
			return nil, ddnsSaveError(err)
		}
		if ddns.ProtectedDomain(rec.Domain, zone.Name, panelHosts) {
			return nil, fmt.Errorf("%s 不能用作节点 DDNS 域名", rec.Domain)
		}
		records, err := client.ListRecords(ctx, zone.ID, rec.Domain, "")
		if err != nil {
			return nil, ddnsSaveError(err)
		}
		same, err := ddnsSameTypeRecords(records, rec.RecordType)
		if err != nil {
			return nil, fmt.Errorf("%s：%v", rec.Domain, err)
		}
		if len(same) == 1 && !rec.Takeover && same[0].Comment != ddns.RecordComment &&
			(same[0].Proxied || !ddnsKnownAddress(same[0].Content, known)) {
			return nil, fmt.Errorf("%s：%v", rec.Domain, ddnsTakeoverError(same[0]))
		}
	}
	return plan, nil
}

func ddnsSaveError(err error) error {
	var cfg *ddnsConfigError
	if errors.As(err, &cfg) {
		return err
	}
	if ddns.IsAuthError(err) {
		return fmt.Errorf("DNS 服务商的 API Token 无效或没有权限：%v", err)
	}
	return fmt.Errorf("无法连接 DNS 服务商校验域名：%v", err)
}

func ddnsKnownAddress(content string, known []string) bool {
	key := strings.ToLower(strings.Trim(strings.TrimSpace(content), "[]"))
	if ip := net.ParseIP(key); ip != nil {
		key = ip.String()
	}
	for _, k := range known {
		k = strings.ToLower(strings.Trim(strings.TrimSpace(k), "[]"))
		if ip := net.ParseIP(k); ip != nil {
			k = ip.String()
		}
		if k != "" && k == key {
			return true
		}
	}
	return false
}

// ddnsKnownAddresses: the node's own literal addresses (fields + extras),
// its latest agent report and the panel-seen IP.
func (h *Handler) ddnsKnownAddresses(nodeID int64, fields ...string) []string {
	out := append([]string{}, fields...)
	if nodeID > 0 {
		if a, ok := h.ddnsState().addrsOf(nodeID); ok {
			out = append(out, a.V4...)
			out = append(out, a.V6...)
		}
		if _, _, ip := h.ddnsSession(nodeID); ip != "" {
			out = append(out, ip)
		}
	}
	return out
}

// ddnsOwnedFields applies DDNS ownership to the submitted address fields:
// a main record keeps the stored field (it becomes the domain after the
// first sync); applied extra domains stay in the extra list.
func ddnsOwnedFields(plan *ddnsPlan, oldNode *model.Node, v4, v6, extras string) (string, string, string) {
	if plan == nil || !plan.enabled {
		return v4, v6, extras
	}
	// Edit: a stored value stays (an IP until the first sync switches it, the
	// domain afterwards); an empty stored field takes the submitted value.
	// Create: an empty field gets the domain right away.
	owned := func(submitted string, stored sql.NullString, domain string) string {
		if domain == "" {
			return submitted
		}
		if oldNode != nil {
			if strings.TrimSpace(stored.String) != "" {
				return stored.String
			}
			return submitted
		}
		if strings.TrimSpace(submitted) == "" {
			return domain
		}
		return submitted
	}
	var storedV4, storedV6 sql.NullString
	if oldNode != nil {
		storedV4, storedV6 = oldNode.ServerIPV4, oldNode.ServerIPV6
	}
	v4 = owned(v4, storedV4, plan.mainDomain("A"))
	v6 = owned(v6, storedV6, plan.mainDomain("AAAA"))
	list := repo.ParseNodeExtraIPs(extras)
	have := map[string]bool{}
	for _, token := range list {
		have[token] = true
	}
	for _, rec := range plan.records {
		if rec.Role == model.DDNSRoleExtra && rec.Applied && !have[rec.Domain] {
			list = append(list, rec.Domain)
			have[rec.Domain] = true
		}
	}
	if len(list) == 0 {
		return v4, v6, ""
	}
	raw, _ := json.Marshal(list)
	return v4, v6, string(raw)
}

// markAppliedAtCreate: a new node whose main field got the domain directly.
func (p *ddnsPlan) markAppliedAtCreate(v4, v6 string) {
	for i := range p.records {
		rec := &p.records[i]
		if rec.Role != model.DDNSRoleMain {
			continue
		}
		if (rec.RecordType == "A" && strings.EqualFold(v4, rec.Domain)) || (rec.RecordType == "AAAA" && strings.EqualFold(v6, rec.Domain)) {
			rec.Applied = true
		}
	}
}

// ---- node list view ----

type ddnsRecordView struct {
	ID          int64  `json:"id"`
	Domain      string `json:"domain"`
	Type        string `json:"type"`
	Role        string `json:"role"`
	Source      string `json:"source"`
	Prefix      string `json:"prefix"`
	Takeover    bool   `json:"takeover"`
	CurrentIP   string `json:"currentIp"`
	TTL         int    `json:"ttl"`
	Applied     bool   `json:"applied"`
	Status      string `json:"status"`
	LastError   string `json:"lastError"`
	LastSyncAt  int64  `json:"lastSyncAt"`
	LastCheckAt int64  `json:"lastCheckAt"`
}

type ddnsNodeView struct {
	Enabled      bool             `json:"enabled"`
	ProviderID   int64            `json:"providerId"`
	ProviderName string           `json:"providerName"`
	Status       string           `json:"status"`
	Online       bool             `json:"online"`
	AddrReport   bool             `json:"addrReport"`
	AgentAddrs   *ddns.AgentAddrs `json:"agentAddrs"`
	PanelIP      string           `json:"panelIp"`
	Records      []ddnsRecordView `json:"records"`
}

// ddnsNodeViews builds the DDNS part of the node list: 3 queries in total.
func (h *Handler) ddnsNodeViews() (map[int64]*ddnsNodeView, error) {
	records, err := h.repo.ListAllDDNSRecords()
	if err != nil {
		return nil, err
	}
	nodes, err := h.repo.ListDDNSNodes()
	if err != nil {
		return nil, err
	}
	providers, err := h.repo.ListDNSProviders()
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, p := range providers {
		names[p.ID] = p.Name
	}
	out := map[int64]*ddnsNodeView{}
	view := func(nodeID int64) *ddnsNodeView {
		v := out[nodeID]
		if v == nil {
			v = &ddnsNodeView{Records: []ddnsRecordView{}}
			v.Online, v.AddrReport, v.PanelIP = h.ddnsSession(nodeID)
			if a, ok := h.ddnsState().addrsOf(nodeID); ok {
				v.AgentAddrs = &a
			}
			out[nodeID] = v
		}
		return v
	}
	for _, n := range nodes {
		v := view(n.ID)
		v.Enabled, v.ProviderID, v.ProviderName = true, n.DDNSProviderID, names[n.DDNSProviderID]
	}
	for _, rec := range records {
		v := view(rec.NodeID)
		v.Records = append(v.Records, ddnsRecordView{
			ID: rec.ID, Domain: rec.Domain, Type: rec.RecordType, Role: rec.Role, Source: rec.Source,
			Prefix: rec.SourcePrefix, Takeover: rec.Takeover, CurrentIP: rec.CurrentIP, TTL: rec.TTL,
			Applied: rec.Applied, Status: rec.Status, LastError: rec.LastError,
			LastSyncAt: rec.LastSyncAt, LastCheckAt: rec.LastCheckAt,
		})
	}
	for _, v := range out {
		v.Status = ddnsNodeStatus(v)
	}
	return out, nil
}

// ddnsNodeStatus is the node badge: failed > upgrade > synced (all synced
// and switched) > pending; "" when DDNS is off.
func ddnsNodeStatus(v *ddnsNodeView) string {
	if !v.Enabled || len(v.Records) == 0 {
		return ""
	}
	status := model.DDNSStatusSynced
	for _, rec := range v.Records {
		switch rec.Status {
		case model.DDNSStatusFailed, model.DDNSStatusNoAddr:
			return model.DDNSStatusFailed
		case model.DDNSStatusUpgrade:
			status = model.DDNSStatusUpgrade
		case model.DDNSStatusSynced:
			if !rec.Applied && status == model.DDNSStatusSynced {
				status = model.DDNSStatusPending
			}
		default:
			if status == model.DDNSStatusSynced {
				status = model.DDNSStatusPending
			}
		}
	}
	return status
}
