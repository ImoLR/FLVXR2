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
	// Aliases help the name-based suggestion (server names like 「Boil HKT
	// 99u」); never used for blocking.
	Aliases []string `json:"aliases,omitempty"`
}

// Limits of the vendor list.
const (
	MaxVendors       = 64
	MaxVendorDomains = 32
)

// SeedVendors is the built-in list (used while the admin has not saved one).
// Domains are only the vendor's own hosts — never shared infrastructure other
// sites use (amazonaws.com, cloudfront.net, aliyuncs.com, alicdn.com,
// bcebos.com, volces.com, cloudflare.com …). Each name covers its subdomains.
func SeedVendors() []Vendor {
	return []Vendor{
		{Key: "dmit", Name: "DMIT", Domains: []string{"dmit.io", "dmithost.net"}, Remark: "官网 / 客户中心 www.dmit.io，活动页 act.dmithost.net"},
		{Key: "bestvm", Name: "Bestvm", Domains: []string{"bestvm.cloud"}, Remark: "官网 / 客户中心 bestvm.cloud"},
		{Key: "boil", Name: "Boil", Domains: []string{"boil.network"}, Remark: "客户中心 cloud.boil.network，换 IP 面板 ippanel.boil.network", Aliases: []string{"boilcloud"}},
		{Key: "fachost", Name: "Fachost", Domains: []string{"fachost.cloud"}, Remark: "官网 / 客户中心 fachost.cloud"},
		{Key: "pqs", Name: "PQS", Domains: []string{"pqs.pw"}, Remark: "PittQiao 官网 / API pqs.pw", Aliases: []string{"pittqiao"}},
		{Key: "tcscloud", Name: "Tcscloud", Domains: []string{"tcscloud.net"}, Remark: "客户中心 billing.tcscloud.net，换 IP 接口 ipapi.tcscloud.net（只影响经代理访问，本机脚本不受影响）"},
		{Key: "nexkr", Name: "NexKR", Domains: []string{"nexkr.sh"}, Remark: "官网 / 下单 nexkr.sh（不含 client.nexkr.cloud：那是各客户机器的地址）"},
		{Key: "leikwan", Name: "Leikwanhost", Domains: []string{"leikwanhost.com"}, Remark: "官网 / 客户中心 buy.leikwanhost.com、v3.leikwanhost.com", Aliases: []string{"leikwan", "利群"}},
		{Key: "vmsilo", Name: "vmsilo", Domains: []string{"vmsilo.com"}, Remark: "客户中心 portal.vmsilo.com"},
		{Key: "lightlayer", Name: "Lightlayer", Domains: []string{"lightlayer.net"}, Remark: "官网 lightlayer.net"},
		{Key: "aws", Name: "AWS", Domains: []string{"aws.amazon.com", "signin.aws"}, Remark: "控制台 / 账单 / 登录（console.aws.amazon.com、signin.aws.amazon.com 等）；不影响 amazon.com 购物和 amazonaws.com 上的网站"},
		{Key: "aliyun", Name: "阿里云", Domains: []string{"aliyun.com", "alibabacloud.com"}, Remark: "官网 / 控制台；不含 aliyuncs.com、alicdn.com（别的网站也在用）"},
		{Key: "baiducloud", Name: "百度智能云", Domains: []string{"cloud.baidu.com", "bce.baidu.com"}, Remark: "官网 / 控制台；不影响百度搜索和 bcebos.com", Aliases: []string{"百度云", "baidu"}},
		{Key: "volcengine", Name: "火山引擎", Domains: []string{"volcengine.com"}, Remark: "官网 / 控制台；不含 volces.com（别的服务也在用）", Aliases: []string{"火山"}},
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
		aliases := []string{}
		for _, a := range v.Aliases {
			if a = strings.TrimSpace(a); a != "" && len(aliases) < 8 && len([]rune(a)) <= 32 {
				aliases = append(aliases, a)
			}
		}
		v.Aliases = aliases
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

// SuggestVendor guesses a server's vendor from its name (and the node name /
// remark when given): the longest key, name or alias that appears as a word
// prefix (case-insensitive). Only a suggestion: the stored choice stays
// explicit.
func SuggestVendor(list []Vendor, texts ...string) string {
	hay := strings.ToLower(strings.Join(texts, " "))
	best, bestLen := "", 0
	for _, v := range list {
		for _, cand := range append([]string{v.Key, v.Name}, v.Aliases...) {
			c := strings.ToLower(strings.TrimSpace(cand))
			if len(c) < 3 || len(c) <= bestLen {
				continue
			}
			if wordPrefix(hay, c) {
				best, bestLen = v.Key, len(c)
			}
		}
	}
	return best
}

// wordPrefix: c occurs at the start of hay or after a non-letter/digit.
func wordPrefix(hay, c string) bool {
	for i := 0; ; {
		j := strings.Index(hay[i:], c)
		if j < 0 {
			return false
		}
		at := i + j
		if at == 0 || !isWordByte(hay[at-1]) {
			return true
		}
		i = at + 1
	}
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
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
