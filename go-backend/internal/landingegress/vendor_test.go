package landingegress

import (
	"reflect"
	"strings"
	"testing"
)

func vendorByKey(t *testing.T, key string) *Vendor {
	t.Helper()
	v, ok := FindVendor(SeedVendors(), key)
	if !ok {
		t.Fatalf("seed vendor %s missing", key)
	}
	return &v
}

func TestSeedVendorsValid(t *testing.T) {
	seed := SeedVendors()
	norm, err := NormalizeVendors(seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(norm) != len(seed) {
		t.Fatalf("seed changed size: %d", len(norm))
	}
	// Shared infrastructure other sites use must never be in the list.
	shared := []string{"amazonaws.com", "cloudfront.net", "aliyuncs.com", "alicdn.com", "bcebos.com", "volces.com", "cloudflare.com", "amazon.com", "baidu.com", "nexkr.cloud"}
	for _, v := range norm {
		for _, d := range v.Domains {
			for _, s := range shared {
				if d == s {
					t.Fatalf("%s lists shared infra %s", v.Key, d)
				}
			}
		}
	}
}

func TestNormalizeVendors(t *testing.T) {
	got, err := NormalizeVendors([]Vendor{{Key: " DMIT ", Name: " DMIT ", Domains: []string{"https://www.DMIT.io/cart?x=1", "dmit.io", "*.dmithost.net", "  "}}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Key != "dmit" || !reflect.DeepEqual(got[0].Domains, []string{"dmit.io", "dmithost.net"}) {
		t.Fatalf("normalize: %+v", got[0])
	}
	bad := [][]Vendor{
		{{Key: "a", Name: "A", Domains: []string{"localhost"}}},
		{{Key: "a", Name: "A", Domains: nil}},
		{{Key: "a", Name: "A", Domains: []string{"a.com"}}, {Key: "a", Name: "B", Domains: []string{"b.com"}}},
		{{Key: "A B", Name: "A", Domains: []string{"a.com"}}},
		{{Key: "a", Name: "", Domains: []string{"a.com"}}},
	}
	for i, list := range bad {
		if _, err := NormalizeVendors(list); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
}

func TestSuggestVendor(t *testing.T) {
	seed := SeedVendors()
	cases := map[string]string{
		"Bestvm US": "bestvm", "Bestvm JP B": "bestvm", "Boil Hinet 马年": "boil", "Boil HKT 99u": "boil",
		"Fachost seednet 黑五": "fachost", "PQS TW": "pqs", "Tcscloud Seednet": "tcscloud", "NexKR": "nexkr",
		"Dmit T1": "dmit", "Leikwanhost 沪台IPLC": "leikwan", "AWS HK": "aws", "Datawave HK 2": "", "百度云广州": "baiducloud",
		"阿里广州": "", "SomeDmitlike": "",
	}
	for name, want := range cases {
		if got := SuggestVendor(seed, name); got != want {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
}

// The vendor rule is the very first rule, lists only that vendor's names,
// covers every node (and chain copies with builtin markers) and carries the
// marker the agent understands.
func TestVendorRuleCompile(t *testing.T) {
	profiles := map[int64]Profile{}
	in := abuseInput(profiles, 0, NodeRef{ID: 1, Tag: "flvxl-1"}, NodeRef{ID: 2, Tag: "flvxl-2"})
	in.Global = &Global{Forced: []Rule{{ID: "google-allow", Name: "放行谷歌/苹果", Enabled: true, Domains: []string{"geosite:google@cn"}, Action: ActionDirect}}, DefaultAction: ActionDirect}
	in.Layers = true
	base, _, err := Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Vendor = vendorByKey(t, "dmit")
	plan, _, err := Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Rules) != len(base.Rules)+1 {
		t.Fatalf("rules %d vs %d", len(plan.Rules), len(base.Rules))
	}
	first := plan.Rules[0]
	want := PlanRule{Name: "内置：屏蔽本机厂商官网（DMIT）", InboundTags: []string{"flvxl-1", "flvxl-2"}, Domain: []string{"domain:dmit.io", "domain:dmithost.net"}, Outbound: ActionBlock}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("vendor rule: %+v", first)
	}
	if !reflect.DeepEqual(plan.Rules[1:], base.Rules) {
		t.Fatal("other rules changed")
	}
	// Before the 放行谷歌/苹果 allow rule and the 防滥用 defaults.
	for i, r := range plan.Rules[1:] {
		if strings.Contains(strings.Join(r.Domain, ","), "dmit") {
			t.Fatalf("vendor names repeated at %d", i+1)
		}
	}
	// Markers: v1/v2 agents → abuse (+ chain copies), v3 → vendor.
	in.Builtin, in.ChainTags = true, []string{"flvxl-900001"}
	plan, _, _ = Compile(in)
	if plan.Rules[0].Builtin != BuiltinAbuse || !reflect.DeepEqual(plan.Rules[0].InboundTags, []string{"flvxl-1", "flvxl-2", "flvxl-900001"}) {
		t.Fatalf("v2 marker: %+v", plan.Rules[0])
	}
	in.VendorMarker = true
	plan, _, _ = Compile(in)
	if plan.Rules[0].Builtin != BuiltinVendor {
		t.Fatalf("v3 marker: %+v", plan.Rules[0])
	}
	// Not ready (Abuse false) → nothing, exactly as without a vendor.
	in2 := abuseInput(profiles, 0, NodeRef{ID: 1, Tag: "flvxl-1"})
	in2.Abuse = false
	in2.Vendor = vendorByKey(t, "dmit")
	plan, _, _ = Compile(in2)
	if plan.Active() {
		t.Fatalf("not-ready server got rules: %+v", plan.Rules)
	}
	// No nodes → no rule.
	in3 := abuseInput(profiles, 0)
	in3.Vendor = vendorByKey(t, "dmit")
	plan, _, _ = Compile(in3)
	if plan.Active() {
		t.Fatalf("empty server got rules: %+v", plan.Rules)
	}
}

// Per node, own vendor only: two servers with different vendors never share
// names.
func TestVendorRulePerServer(t *testing.T) {
	for _, key := range []string{"bestvm", "boil", "fachost", "pqs", "nexkr", "tcscloud", "dmit"} {
		in := abuseInput(map[int64]Profile{}, 0, NodeRef{ID: 1, Tag: "flvxl-1"})
		v := vendorByKey(t, key)
		in.Vendor = v
		plan, _, err := Compile(in)
		if err != nil {
			t.Fatal(err)
		}
		got := plan.Rules[0].Domain
		if len(got) != len(v.Domains) {
			t.Fatalf("%s: %v", key, got)
		}
		for _, other := range SeedVendors() {
			if other.Key == key {
				continue
			}
			for _, d := range other.Domains {
				for _, g := range got {
					if g == "domain:"+d {
						t.Fatalf("%s carries %s's %s", key, other.Key, d)
					}
				}
			}
		}
	}
}

// A user's own node rule (我的规则) that allows the vendor's domain comes
// after the vendor rule, so it cannot let the traffic through.
func TestVendorRuleBeforeNodeRules(t *testing.T) {
	in := abuseInput(map[int64]Profile{}, 0, NodeRef{ID: 1, Tag: "flvxl-1", Rules: []Rule{{ID: "u1", Name: "直连 dmit", Enabled: true, Domains: []string{"domain:dmit.io"}, Action: ActionDirect}}})
	in.Global = &Global{DefaultAction: ActionDirect}
	in.Layers, in.NodeRules = true, true
	in.Vendor = vendorByKey(t, "dmit")
	plan, _, err := Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Rules[0].Outbound != ActionBlock || plan.Rules[0].Domain[0] != "domain:dmit.io" {
		t.Fatalf("first: %+v", plan.Rules[0])
	}
	found := false
	for _, r := range plan.Rules[1:] {
		if r.Outbound == ActionDirect && len(r.Domain) > 0 && r.Domain[0] == "domain:dmit.io" {
			found = true
		}
	}
	if !found {
		t.Fatalf("user rule missing: %+v", plan.Rules)
	}
}

func TestIsVendorRule(t *testing.T) {
	r := VendorPlanRule(*vendorByKey(t, "boil"))
	if !IsVendorRule(r) {
		t.Fatal("vendor rule not recognised")
	}
	r.Port = "443"
	if IsVendorRule(r) {
		t.Fatal("port rule recognised")
	}
}
