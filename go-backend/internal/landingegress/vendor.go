package landingegress

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Plan 316 「屏蔽本机厂商官网」: every landing server blocks the websites of the
// VPS vendor that hosts it (website, client area, billing, tickets, panels),
// so a rider cannot attack the vendor's own systems from the vendor's IP (the
// 11-26 DMIT case: the whole account was closed). Per server, own vendor
// only; the rule is the first plan rule, so no other layer can let it through.

// BuiltinVendor marks the vendor rule for agents with landing_guard >=
// VendorVersion: block-only, rendered right after the safety rule, never
// withholds a node, and its names may override the routing domain from the
// sniffed TLS SNI / HTTP Host (so a client that dials the vendor by IP with
// the vendor's SNI is caught too). Older agents get BuiltinAbuse (same
// placement and fail-open, requested-domain match only); the mmwx Custom gets
// no marker.
const BuiltinVendor = "vendor"

// VendorVersion is the landing_guard version that understands BuiltinVendor.
const VendorVersion = 3

// Vendor is one entry of the admin-editable vendor list.
type Vendor struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Domains []string `json:"domains"`
	Remark  string   `json:"remark"`
}

// Limits of the vendor list.
const (
	MaxVendors       = 64
	MaxVendorDomains = 32
	// Plan 318: a node (VPS) has 1–MaxNodeVendors vendors (seller + upstream
	// cloud, e.g. Leikwanhost + 火山引擎); their union is one rule of at most
	// MaxRuleVendorDomains names (the agent's maxVendorHosts).
	MaxNodeVendors       = 4
	MaxRuleVendorDomains = 64
	MaxVendorOtherRunes  = 32
)

// SeedVendors is the built-in list (used while the admin has not saved one).
// Domains are only the vendor's own hosts — never shared infrastructure other
// sites use (amazonaws.com, cloudfront.net, aliyuncs.com, alicdn.com,
// bcebos.com, volces.com, cloudflare.com …). Each name covers its subdomains.
// Plan 318: researched list 11-27 (17 vendors); the 14 plan-316 keys and
// names are unchanged, so their rule names stay the same.
func SeedVendors() []Vendor {
	return []Vendor{
		{Key: "dmit", Name: "DMIT", Domains: []string{"dmit.io", "dmithost.net", "dmit.com"}, Remark: "官网 / 客户中心 www.dmit.io，活动页 act.dmithost.net，登录 / 通知 dmit.com"},
		{Key: "bestvm", Name: "Bestvm", Domains: []string{"bestvm.cloud", "vmsilo.com", "xwan.world"}, Remark: "官网 / 客户中心 bestvm.cloud；vmsilo.com、xwan.world 是同一家的姐妹品牌，一起屏蔽"},
		{Key: "boil", Name: "Boil", Domains: []string{"boil.network"}, Remark: "客户中心 cloud.boil.network，换 IP 面板 ippanel.boil.network，存储面板 storagepanel.boil.network"},
		{Key: "fachost", Name: "Fachost", Domains: []string{"fachost.cloud"}, Remark: "官网 / 客户中心 / 控制台 / 状态页（server.、console.、status.、api.fachost.cloud）"},
		{Key: "pqs", Name: "PQS", Domains: []string{"pqs.pw", "pqs.cloud", "pni.com.tw"}, Remark: "PittQiao 官网 / API pqs.pw，pqs.cloud，母公司 pni.com.tw（AS18178）"},
		{Key: "tcscloud", Name: "Tcscloud", Domains: []string{"tcscloud.net"}, Remark: "客户中心 billing.tcscloud.net，换 IP 接口 ipapi.tcscloud.net（只影响经代理访问，本机脚本不受影响）；不含 tcscloud.com（停放域名）"},
		{Key: "nexkr", Name: "NexKR", Domains: []string{"nexkr.sh"}, Remark: "官网 / 下单 nexkr.sh；不含 nexkr.cloud（那是各客户机器的地址，也包括我们自己的 NexKR）"},
		{Key: "leikwan", Name: "Leikwanhost", Domains: []string{"leikwanhost.com"}, Remark: "官网 / 客户中心 buy.leikwanhost.com、v3.leikwanhost.com；整个域名都屏蔽（也包括厂商给客户的入口名，只在经利群落地访问时）"},
		{Key: "vmsilo", Name: "vmsilo", Domains: []string{"vmsilo.com", "xwan.world", "bestvm.cloud"}, Remark: "客户中心 portal.vmsilo.com，旧门户 xwan.world；bestvm.cloud 是姐妹品牌，一起屏蔽"},
		{Key: "lightlayer", Name: "Lightlayer", Domains: []string{"lightlayer.net", "megalayer.net"}, Remark: "官网 lightlayer.net，母品牌 megalayer.net"},
		{Key: "aws", Name: "AWS", Domains: []string{"aws.amazon.com", "signin.aws", "aws.com"}, Remark: "只屏蔽控制台 / 账单 / 登录（console.aws.amazon.com、signin.aws、aws.com）；不影响 amazon.com 购物，不含 amazonaws.com、awsapps.com（别的网站也在用）"},
		{Key: "aliyun", Name: "阿里云", Domains: []string{"aliyun.com", "alibabacloud.com"}, Remark: "官网 / 控制台（aliyun.com 也包括阿里云邮箱）；不含 aliyuncs.com、alicdn.com（别的网站也在用）"},
		{Key: "baiducloud", Name: "百度智能云", Domains: []string{"cloud.baidu.com", "bce.baidu.com"}, Remark: "官网 / 控制台；不影响百度搜索和 passport.baidu.com，不含 baidubce.com、bcebos.com"},
		{Key: "volcengine", Name: "火山引擎", Domains: []string{"volcengine.com", "volcengine.cn"}, Remark: "官网 / 控制台（volcengine.cn 跳到 volcengine.com）；不含 volces.com、字节 CDN、byteplus.com"},
		{Key: "mkcloud", Name: "Mkcloud", Domains: []string{"mkcloud.net"}, Remark: "官网 / 客户中心 www.mkcloud.net（深港 / 沪港 / 沪日 / 沪美 IXP）"},
		{Key: "datawave", Name: "Datawave", Domains: []string{"datawave.sh"}, Remark: "官网 / 控制台 datawave.sh（AS142433 DATAWAVE GLOBAL）"},
		{Key: "nobrand", Name: "Nobrand", Domains: []string{"nnb.ltd", "nbdnet.com", "cornseed.gitbook.io"}, Remark: "NewNB Cloud 新站 nnb.ltd，旧站 cloud.nbdnet.com，文档 cornseed.gitbook.io（只这一个子域名，不含 gitbook.io）"},
	}
}

var (
	vendorKeyRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	vendorHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?)+$`)
)

// NormalizeVendorDomain lowercases a domain and drops a scheme, path, port,
// "domain:" prefix, leading "*." / "." and a trailing dot; "" = invalid.
func NormalizeVendorDomain(raw string) string {
	d := strings.ToLower(strings.TrimSpace(raw))
	d = strings.TrimPrefix(d, "domain:")
	for _, p := range []string{"https://", "http://"} {
		d = strings.TrimPrefix(d, p)
	}
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	if i := strings.LastIndex(d, ":"); i >= 0 {
		d = d[:i]
	}
	d = strings.TrimPrefix(strings.TrimPrefix(d, "*."), ".")
	d = strings.TrimSuffix(d, ".")
	if len(d) > 253 || !vendorHostRe.MatchString(d) {
		return ""
	}
	return d
}

// MinimalDomains sorts and dedups names and drops one already covered by
// another entry (x.dmit.io under dmit.io).
func MinimalDomains(domains []string) []string {
	set := map[string]bool{}
	for _, d := range domains {
		set[d] = true
	}
	out := []string{}
	for d := range set {
		covered := false
		for p := d; ; {
			i := strings.IndexByte(p, '.')
			if i < 0 {
				break
			}
			p = p[i+1:]
			if set[p] {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// NormalizeVendors validates an admin list: unique keys and names, 1–32 valid
// domains each. Keys are stable ids stored on servers.
func NormalizeVendors(list []Vendor) ([]Vendor, error) {
	if len(list) > MaxVendors {
		return nil, fmt.Errorf("厂商最多 %d 个", MaxVendors)
	}
	keys, names := map[string]bool{}, map[string]bool{}
	out := make([]Vendor, 0, len(list))
	for i, v := range list {
		v.Key = strings.ToLower(strings.TrimSpace(v.Key))
		v.Name = strings.TrimSpace(v.Name)
		v.Remark = strings.TrimSpace(v.Remark)
		label := fmt.Sprintf("第 %d 个厂商", i+1)
		if v.Name != "" {
			label = "厂商「" + v.Name + "」"
		}
		switch {
		case !vendorKeyRe.MatchString(v.Key):
			return nil, errors.New(label + "：标识只能用小写字母、数字、- 和 _（最多 32 个字符）")
		case v.Name == "" || len([]rune(v.Name)) > 32:
			return nil, errors.New(label + "：名称不能为空，最多 32 个字")
		case len([]rune(v.Remark)) > 200:
			return nil, errors.New(label + "：备注最多 200 个字")
		case keys[v.Key]:
			return nil, errors.New(label + "：标识重复")
		case names[strings.ToLower(v.Name)]:
			return nil, errors.New(label + "：名称重复")
		}
		keys[v.Key], names[strings.ToLower(v.Name)] = true, true
		var domains []string
		for _, raw := range v.Domains {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			d := NormalizeVendorDomain(raw)
			if d == "" {
				return nil, fmt.Errorf("%s：域名「%s」无效", label, raw)
			}
			domains = append(domains, d)
		}
		v.Domains = MinimalDomains(domains)
		if len(v.Domains) == 0 {
			return nil, errors.New(label + "：至少填写一个域名")
		}
		if len(v.Domains) > MaxVendorDomains {
			return nil, fmt.Errorf("%s：域名最多 %d 个", label, MaxVendorDomains)
		}
		out = append(out, v)
	}
	return out, nil
}

// FindVendor returns the vendor with that key.
func FindVendor(list []Vendor, key string) (Vendor, bool) {
	if key == "" {
		return Vendor{}, false
	}
	for _, v := range list {
		if v.Key == key {
			return v, true
		}
	}
	return Vendor{}, false
}

// VendorRuleName is the plan rule name (shown in Core logs and to mmwx).
func VendorRuleName(v Vendor) string { return "内置：屏蔽本机厂商官网（" + v.Name + "）" }

// VendorPlanRule is the block rule of a vendor (no scope yet).
func VendorPlanRule(v Vendor) PlanRule {
	domains := make([]string, 0, len(v.Domains))
	for _, d := range MinimalDomains(v.Domains) {
		domains = append(domains, "domain:"+d)
	}
	return PlanRule{Name: VendorRuleName(v), Domain: domains, Outbound: ActionBlock}
}

// IsVendorRule reports a compiled vendor rule (by name and shape).
func IsVendorRule(r PlanRule) bool {
	return strings.HasPrefix(r.Name, "内置：屏蔽本机厂商官网（") && r.Outbound == ActionBlock && len(r.Domain) > 0 && len(r.IP)+len(r.SourceIP)+len(r.Protocol) == 0 && r.Port == "" && r.SourcePort == ""
}

// Plan 318: the vendors of a node (the VPS). Stored as a comma list of keys
// in pick order ('' = none) plus an optional 「其他」 name for a vendor that is
// not in the list yet (no rule, admin ⚠ until the admin adds the vendor in
// 管理厂商 and picks it on the node — never linked automatically).

// ParseNodeVendors splits the stored comma list (empty parts dropped).
func ParseNodeVendors(raw string) []string {
	out := []string{}
	for _, k := range strings.Split(raw, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// FormatNodeVendors is the stored form of a key list.
func FormatNodeVendors(keys []string) string { return strings.Join(keys, ",") }

// ErrNodeVendorRequired is returned when neither a vendor nor 「其他」 is given.
var ErrNodeVendorRequired = errors.New("请选择 VPS 厂商（必填；列表里没有就选「其他（列表里没有）」并填写厂商名称）")

// NormalizeNodeVendors validates an admin pick against the vendor list: keys
// lowercased and deduplicated (pick order kept), every key known, at most
// MaxNodeVendors, their union at most MaxRuleVendorDomains names; other is
// the trimmed 「其他」 name. At least one of keys / other is required. A
// 「其他」 name that is already in the list is refused (pick it instead).
func NormalizeNodeVendors(list []Vendor, keys []string, other string) ([]string, string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, k := range keys {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" || seen[k] {
			continue
		}
		if _, ok := FindVendor(list, k); !ok {
			return nil, "", fmt.Errorf("厂商「%s」不在管理厂商列表，请刷新后重新选择", k)
		}
		seen[k] = true
		out = append(out, k)
	}
	other = strings.TrimSpace(other)
	if strings.ContainsAny(other, "\r\n\t") {
		return nil, "", errors.New("「其他」厂商名称不能换行")
	}
	if len([]rune(other)) > MaxVendorOtherRunes {
		return nil, "", fmt.Errorf("「其他」厂商名称最多 %d 个字", MaxVendorOtherRunes)
	}
	if other != "" {
		for _, v := range list {
			if strings.EqualFold(v.Name, other) || strings.EqualFold(v.Key, other) {
				return nil, "", fmt.Errorf("厂商「%s」已在管理厂商列表里，请直接选择它", v.Name)
			}
		}
	}
	if len(out) == 0 && other == "" {
		return nil, "", ErrNodeVendorRequired
	}
	if len(out) > MaxNodeVendors {
		return nil, "", fmt.Errorf("VPS 厂商最多选 %d 个", MaxNodeVendors)
	}
	if v, _, ok := CombineVendors(list, out); ok && len(v.Domains) > MaxRuleVendorDomains {
		return nil, "", fmt.Errorf("所选厂商的域名合计 %d 个，超过上限 %d 个", len(v.Domains), MaxRuleVendorDomains)
	}
	return out, other, nil
}

// CombineVendors merges a node's vendors (pick order) into the one vendor
// the rule is compiled from: name 「A、B」, domains = MinimalDomains(union).
// missing = keys not in the list (skipped); ok = at least one known vendor.
// A single vendor comes back unchanged (same name, so the same rule name as
// plan 316).
func CombineVendors(list []Vendor, keys []string) (v Vendor, missing []string, ok bool) {
	var names, found, domains []string
	for _, k := range keys {
		x, hit := FindVendor(list, k)
		if !hit {
			missing = append(missing, k)
			continue
		}
		found = append(found, x.Key)
		names = append(names, x.Name)
		domains = append(domains, x.Domains...)
	}
	if len(found) == 0 {
		return Vendor{}, missing, false
	}
	return Vendor{Key: strings.Join(found, ","), Name: strings.Join(names, "、"), Domains: MinimalDomains(domains)}, missing, true
}
