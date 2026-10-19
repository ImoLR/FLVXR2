package landingegress

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

// GeoRelease is the geo data release the agent installs (go-gost/x/landing).
const GeoRelease = "202610182205"

//go:embed data/geo_codes.txt
var geoCodesRaw string

type geoInfo struct{ bytes, items int }

var geoOnce sync.Once
var geoIP, geoSite map[string]geoInfo

func geoTables() (map[string]geoInfo, map[string]geoInfo) {
	geoOnce.Do(func() {
		geoIP, geoSite = map[string]geoInfo{}, map[string]geoInfo{}
		for _, line := range strings.Split(geoCodesRaw, "\n") {
			f := strings.Fields(line)
			if len(f) != 4 || strings.HasPrefix(line, "#") {
				continue
			}
			b, _ := strconv.Atoi(f[2])
			n, _ := strconv.Atoi(f[3])
			if f[0] == "ip" {
				geoIP[f[1]] = geoInfo{b, n}
			} else {
				geoSite[f[1]] = geoInfo{b, n}
			}
		}
	})
	return geoIP, geoSite
}

// GeoEntry returns (known, protobuf bytes, items) for "geosite:x[@attr]",
// "geoip:x" or "geoip:!x". Non-geo entries report (true, len, 1).
func GeoEntry(entry string) (bool, int, int) {
	ip, site := geoTables()
	if code, ok := strings.CutPrefix(entry, "geosite:"); ok {
		code, _, _ = strings.Cut(code, "@")
		g, found := site[code]
		return found, g.bytes, g.items
	}
	if code, ok := strings.CutPrefix(entry, "geoip:"); ok {
		g, found := ip[strings.TrimPrefix(code, "!")]
		return found, g.bytes, g.items
	}
	return true, len(entry) + 4, 1
}

// GeoCodes lists available category codes (for UI suggestions).
func GeoCodes() (ip []string, site []string) {
	ipT, siteT := geoTables()
	for c := range ipT {
		ip = append(ip, c)
	}
	for c := range siteT {
		site = append(site, c)
	}
	return ip, site
}

func usesGeo(entries ...[]string) (ip, site bool) {
	for _, list := range entries {
		for _, e := range list {
			ip = ip || strings.HasPrefix(e, "geoip:")
			site = site || strings.HasPrefix(e, "geosite:")
		}
	}
	return ip, site
}
