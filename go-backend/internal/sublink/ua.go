package sublink

import "strings"

// Client is what a User-Agent says about the puller.
type Client struct {
	// Format the client gets in auto mode.
	Format string
	// Label: short client family shown on the card (never the raw UA).
	Label string
	// Stash: Clash YAML without the protocols Stash cannot load.
	Stash bool
	// Surfboard: Surge profile limited to what Surfboard documents.
	Surfboard bool
	// Browser: a web browser (gets the small HTML page).
	Browser bool
}

type uaRule struct {
	keywords []string
	format   string
	label    string
}

// uaRules: the ORDER is the priority (ported from mmwx client_ua.go):
// Stash's UA contains "clash" and must come first; the Clash family is last.
// Plan 191: Surge (iOS "Surge iOS/…", Mac "Surge Mac/…") and Surfboard get a
// managed Surge profile, Loon and Quantumult X their native node lists.
var uaRules = []uaRule{
	{[]string{"stash"}, FormatClash, "Stash"},
	{[]string{"shadowrocket"}, FormatBase64, "Shadowrocket"},
	{[]string{"surge"}, FormatSurge, "Surge"},
	{[]string{"loon"}, FormatLoon, "Loon"},
	{[]string{"quantumult%20x", "quantumult x", "quantumultx"}, FormatQX, "Quantumult X"},
	{[]string{"egern"}, FormatClash, "Egern"},
	{[]string{"surfboard"}, FormatSurge, "Surfboard"},
	{[]string{"hiddify"}, FormatSingBox, "Hiddify"},
	{[]string{"sing-box", "sfi/", "sfa/", "sfm/", "sft/"}, FormatSingBox, "sing-box"},
	{[]string{"v2rayng"}, FormatBase64, "v2rayNG"},
	{[]string{"v2rayn"}, FormatBase64, "v2rayN"},
	{[]string{"v2box"}, FormatBase64, "V2Box"},
	{[]string{"clash-verge", "clash_verge", "clashverge"}, FormatClash, "Clash Verge"},
	{[]string{"flclash"}, FormatClash, "FlClash"},
	{[]string{"nyanpasu"}, FormatClash, "Clash Nyanpasu"},
	{[]string{"clashforandroid", "clash for android"}, FormatClash, "Clash for Android"},
	{[]string{"clashx"}, FormatClash, "ClashX"},
	{[]string{"nekobox", "nekoray"}, FormatClash, "NekoBox"},
	{[]string{"clash.meta", "clashmeta", "clash-meta"}, FormatClash, "Clash Meta"},
	{[]string{"mihomo"}, FormatClash, "mihomo"},
	{[]string{"clash"}, FormatClash, "Clash"},
}

// DetectClient maps a User-Agent to a client. Unknown non-browser clients
// get Clash YAML (like mmwx); a browser is Mozilla without any client marker.
func DetectClient(ua string) Client {
	l := strings.ToLower(strings.TrimSpace(ua))
	for _, r := range uaRules {
		for _, kw := range r.keywords {
			if strings.Contains(l, kw) {
				return Client{Format: r.format, Label: r.label, Stash: r.label == "Stash", Surfboard: r.label == "Surfboard"}
			}
		}
	}
	if strings.Contains(l, "mozilla") {
		return Client{Format: FormatClash, Label: "浏览器", Browser: true}
	}
	return Client{Format: FormatClash, Label: "其它客户端"}
}

// FormatLabel is the Chinese name of a stored format.
func FormatLabel(f string) string {
	switch f {
	case FormatClash:
		return "Clash"
	case FormatSingBox:
		return "sing-box"
	case FormatBase64:
		return "通用"
	}
	return "自动识别客户端"
}
