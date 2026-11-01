package handler

import (
	"container/list"
	"context"
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/clientip"
	"go-backend/internal/http/middleware"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"

	"rsc.io/qr"
)

// Render cache: per (token, format) for 60 s, bounded by entries and bytes.
const (
	subCacheEntries = 256
	subCacheBytes   = 16 << 20
	subRenderTTL    = 60 * time.Second
	subPullMarkGap  = 60 * time.Second
)

type subRender struct {
	key    string
	body   []byte
	nodes  int
	subID  int64
	userID int64
	at     time.Time
}

type subRenderCache struct {
	mu    sync.Mutex
	ll    *list.List
	m     map[string]*list.Element
	bytes int
	max   int
	maxB  int
	ttl   time.Duration
	now   func() time.Time
}

func newSubRenderCache(max, maxBytes int, ttl time.Duration) *subRenderCache {
	return &subRenderCache{ll: list.New(), m: map[string]*list.Element{}, max: max, maxB: maxBytes, ttl: ttl, now: time.Now}
}

func (c *subRenderCache) get(key string) *subRender {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[key]
	if !ok {
		return nil
	}
	v := el.Value.(*subRender)
	if c.now().Sub(v.at) > c.ttl {
		c.removeLocked(el)
		return nil
	}
	c.ll.MoveToFront(el)
	return v
}

func (c *subRenderCache) put(v *subRender) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.m[v.key]; ok {
		c.removeLocked(el)
	}
	if len(v.body) > c.maxB {
		return
	}
	c.m[v.key] = c.ll.PushFront(v)
	c.bytes += len(v.body)
	for c.ll.Len() > c.max || c.bytes > c.maxB {
		c.removeLocked(c.ll.Back())
	}
}

func (c *subRenderCache) removeLocked(el *list.Element) {
	v := el.Value.(*subRender)
	c.ll.Remove(el)
	delete(c.m, v.key)
	c.bytes -= len(v.body)
}

func (c *subRenderCache) purge(match func(*subRender) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for el := c.ll.Front(); el != nil; {
		next := el.Next()
		if match(el.Value.(*subRender)) {
			c.removeLocked(el)
		}
		el = next
	}
}

func (c *subRenderCache) purgeSub(id int64) {
	c.purge(func(v *subRender) bool { return v.subID == id })
}

func (c *subRenderCache) purgeUser(id int64) {
	c.purge(func(v *subRender) bool { return v.userID == id })
}

func (c *subRenderCache) stats() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len(), c.bytes
}

// subRenderFor renders (or reuses) a subscription in one format; "count"
// only resolves the nodes (browser page).
func (h *Handler) subRenderFor(ctx context.Context, s *model.UserSubscription, format string, stash bool) (*subRender, error) {
	st := h.subState()
	key := s.Token + "|" + format
	if stash {
		key += "|stash"
	}
	if v := st.cache.get(key); v != nil {
		return v, nil
	}
	refs := sublink.ParseRefs(s.Items)
	d, err := h.subLoad(ctx, s.UserID, refs, false, true)
	if err != nil {
		return nil, err
	}
	nodes, _ := d.resolve(refs)
	if len(nodes) > model.MaxSubscriptionNodes {
		nodes = nodes[:model.MaxSubscriptionNodes]
	}
	sublink.FinalizeNames(nodes)
	v := &subRender{key: key, subID: s.ID, userID: s.UserID, at: time.Now()}
	switch format {
	case sublink.FormatSingBox:
		v.body, v.nodes = sublink.SingBox(nodes, s.RuleMode)
	case sublink.FormatBase64:
		v.body, v.nodes = sublink.Base64(nodes)
	case "count":
		v.nodes = len(nodes)
	default:
		v.body, v.nodes = sublink.Clash(nodes, s.RuleMode, stash)
	}
	st.cache.put(v)
	return v, nil
}

func subPlain(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if code == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	w.WriteHeader(code)
	_, _ = w.Write([]byte(msg))
}

func queryFormat(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "clash", "mihomo", "stash":
		return sublink.FormatClash
	case "singbox", "sing-box":
		return sublink.FormatSingBox
	case "base64", "v2ray", "shadowrocket":
		return sublink.FormatBase64
	}
	return ""
}

// subPublic serves GET /s/<token> without login. Nothing is logged per
// pull (the token is the secret); refusals are summarized once a minute.
func (h *Handler) subPublic(w http.ResponseWriter, r *http.Request) {
	middleware.SuppressRequestLog(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		subPlain(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	st := h.subState()
	if !h.subLinkEnabled() {
		subPlain(w, http.StatusForbidden, "订阅功能已关闭")
		return
	}
	ipKey := clientip.LimitKey(r)
	if !st.missRL.Take(ipKey) {
		st.refusals.Note("ip")
		subPlain(w, http.StatusTooManyRequests, "请求太频繁，请稍后再试")
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/s/")
	if !sublink.ValidToken(token) {
		st.refusals.Note("bad_token")
		http.NotFound(w, r)
		return
	}
	s, err := h.repo.GetSubscriptionByToken(token)
	if err != nil {
		st.missRL.Refund(ipKey)
		subPlain(w, http.StatusServiceUnavailable, "暂时不可用，请稍后再试")
		return
	}
	if s == nil {
		st.refusals.Note("unknown_token")
		http.NotFound(w, r)
		return
	}
	st.missRL.Refund(ipKey)
	if !st.tokenRL.Take(strconv.FormatInt(s.ID, 10)) {
		st.refusals.Note("per_token")
		subPlain(w, http.StatusTooManyRequests, "拉取太频繁（每小时最多 60 次），请稍后再试")
		return
	}
	user, err := h.repo.GetUserByID(s.UserID)
	if err != nil {
		subPlain(w, http.StatusServiceUnavailable, "暂时不可用，请稍后再试")
		return
	}
	now := time.Now()
	if user == nil || user.Status == 0 || (user.ExpTime > 0 && user.ExpTime < now.UnixMilli()) {
		subPlain(w, http.StatusForbidden, "账号已停用或已到期")
		return
	}
	client := sublink.DetectClient(r.Header.Get("User-Agent"))
	format := queryFormat(r.URL.Query().Get("t"))
	if format == "" && client.Browser {
		h.subPage(w, r, s)
		return
	}
	if format == "" {
		format = s.Format
	}
	if format == sublink.FormatAuto || !sublink.ValidFormat(format) {
		format = client.Format
	}
	stash := client.Stash && format == sublink.FormatClash
	v, err := h.subRenderFor(r.Context(), s, format, stash)
	if err != nil {
		subPlain(w, http.StatusServiceUnavailable, "暂时不可用，请稍后再试")
		return
	}
	const giga = int64(1024 * 1024 * 1024)
	info := "upload=0; download=" + strconv.FormatInt(user.InFlow+user.OutFlow, 10) + "; total=" + strconv.FormatInt(user.Flow*giga, 10)
	if user.ExpTime > 0 {
		info += "; expire=" + strconv.FormatInt(user.ExpTime/1000, 10)
	}
	ext, ctype := ".yaml", "text/yaml; charset=utf-8"
	switch format {
	case sublink.FormatSingBox:
		ext, ctype = ".json", "application/json; charset=utf-8"
	case sublink.FormatBase64:
		ext, ctype = ".txt", "text/plain; charset=utf-8"
	}
	hd := w.Header()
	hd.Set("Content-Type", ctype)
	hd.Set("Cache-Control", "no-store")
	hd.Set("subscription-userinfo", info)
	hd.Set("profile-update-interval", "12")
	hd.Set("profile-title", "base64:"+base64.StdEncoding.EncodeToString([]byte(s.Name)))
	hd.Set("content-disposition", "attachment; filename=\"flvx"+ext+"\"; filename*=UTF-8''"+url.PathEscape(s.Name+ext))
	hd.Set("Content-Length", strconv.Itoa(len(v.body)))
	_, _ = w.Write(v.body)
	h.subMarkPulled(s.ID, client.Label, v.nodes, now)
}

// subMarkPulled writes the card's 「最近拉取」 at most once a minute per
// subscription (memory check first, then a conditional update).
func (h *Handler) subMarkPulled(id int64, client string, nodes int, now time.Time) {
	st := h.subState()
	st.mu.Lock()
	if at, ok := st.marked[id]; ok && now.Sub(at) < subPullMarkGap {
		st.mu.Unlock()
		return
	}
	if len(st.marked) >= 4096 {
		for k, at := range st.marked {
			if now.Sub(at) >= subPullMarkGap {
				delete(st.marked, k)
			}
		}
	}
	st.marked[id] = now
	st.mu.Unlock()
	_ = h.repo.MarkSubscriptionPulled(id, now.UnixMilli(), client, nodes, subPullMarkGap)
}

var subPageTmpl = template.Must(template.New("sub").Parse(`<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>{{.Name}} · 订阅</title>
<style>
body{margin:0;background:#eef1f4;color:#1f2937;font:15px/1.6 -apple-system,"PingFang SC","Noto Sans SC","Microsoft YaHei",sans-serif}
.card{max-width:440px;margin:32px auto;background:#fff;border-radius:18px;padding:22px;box-shadow:0 0 0 1px #e6e9ee}
h1{font-size:20px;margin:0 0 4px}.muted{color:#6b7280;font-size:13px}
.pill{display:inline-block;border-radius:999px;padding:0 10px;font-size:12px;font-weight:600;background:rgba(34,197,94,.15);color:#15803d}
.url{margin:14px 0 8px;background:#f1f5f9;border-radius:10px;padding:9px 11px;font:12.5px ui-monospace,Menlo,monospace;word-break:break-all}
button{border:0;border-radius:10px;padding:8px 14px;font-weight:600;background:#16a34a;color:#fff;font-size:14px;cursor:pointer}
.qr{text-align:center;margin:16px 0 6px}.qr img{width:220px;height:220px;image-rendering:pixelated}
ul{padding-left:18px;margin:6px 0 0}li{margin:3px 0}
@media (max-width:480px){.card{margin:14px 12px;padding:18px}}
@media (prefers-color-scheme:dark){body{background:#0f172a;color:#e5e7eb}.card{background:#111827;box-shadow:0 0 0 1px #1f2937}.url{background:#1f2937}.muted{color:#9ca3af}.pill{color:#86efac}}
</style></head><body><div class="card">
<h1>{{.Name}}</h1><div class="muted"><span class="pill">{{.Count}} 个节点</span> · 这是一条代理订阅链接，请在客户端里导入</div>
<div class="url" id="u">{{.URL}}</div>
<button type="button" onclick="navigator.clipboard&&navigator.clipboard.writeText(document.getElementById('u').textContent).then(function(){this.textContent='已复制'}.bind(this))">复制订阅链接</button>
{{if .QR}}<div class="qr"><img alt="订阅二维码" src="{{.QR}}"></div><div class="muted" style="text-align:center">手机客户端扫码导入</div>{{end}}
<p class="muted" style="margin-top:16px">怎么导入（链接会自动识别客户端）：</p>
<ul class="muted">
<li>Clash Verge / mihomo / Stash：订阅 → 新建 → 粘贴链接</li>
<li>sing-box（SFA / SFI / SFM）：配置 → 新建 → 远程 → 粘贴链接</li>
<li>Shadowrocket / v2rayN / v2rayNG：添加订阅 → 粘贴链接或扫码</li>
</ul>
<p class="muted">链接就是你的密码，不要发给别人；泄露了请在面板「订阅」里重置。</p>
</div></body></html>`))

// subPage is the small page a browser gets: name, node count, the link and
// a QR code. No node secrets.
func (h *Handler) subPage(w http.ResponseWriter, r *http.Request, s *model.UserSubscription) {
	v, err := h.subRenderFor(r.Context(), s, "count", false)
	count := 0
	if err == nil {
		count = v.nodes
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); p == "https" || p == "http" {
		scheme = p
	}
	link := scheme + "://" + r.Host + "/s/" + s.Token
	data := map[string]interface{}{"Name": s.Name, "Count": count, "URL": link}
	if code, err := qr.Encode(link, qr.M); err == nil {
		code.Scale = 6
		data["QR"] = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()))
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Cache-Control", "no-store")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("X-Robots-Tag", "noindex")
	hd.Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	_ = subPageTmpl.Execute(w, data)
}
