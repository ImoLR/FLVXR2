// Package geoip looks up node countries using embedded DB-IP Country Lite data.
// Attribution and refresh instructions are in DATASET.md.
package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed country.db.gz
var countryData []byte

var (
	loadOnce sync.Once
	v4Table  []byte
	v6Table  []byte
)

func load() {
	reader, err := gzip.NewReader(bytes.NewReader(countryData))
	if err != nil {
		log.Printf("geoip: read embedded data: %v", err)
		return
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 64<<20))
	if err == nil {
		v4Table, v6Table, err = decode(data)
	}
	if err != nil {
		log.Printf("geoip: decode embedded data: %v", err)
	}
}

func decode(data []byte) ([]byte, []byte, error) {
	if len(data) < 16 || string(data[:8]) != "FLVXGEO1" {
		return nil, nil, fmt.Errorf("invalid country data header")
	}
	n4, n6 := uint64(binary.BigEndian.Uint32(data[8:12])), uint64(binary.BigEndian.Uint32(data[12:16]))
	if uint64(len(data)) != 16+n4*10+n6*34 {
		return nil, nil, fmt.Errorf("invalid country data length")
	}
	boundary := 16 + int(n4)*10
	return data[16:boundary], data[boundary:], nil
}

// Non-public/special-use addresses must never acquire the location of a wider range.
var excluded = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func public(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.Zone() != "" {
		return false
	}
	for _, prefix := range excluded {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// Lookup returns a country code for a public IP, or empty when unknown.
func Lookup(addr netip.Addr) string {
	addr = addr.Unmap()
	if !public(addr) {
		return ""
	}
	loadOnce.Do(load)
	var key []byte
	table, width := v6Table, 16
	if addr.Is4() {
		ip := addr.As4()
		key, table, width = ip[:], v4Table, 4
	} else {
		ip := addr.As16()
		key = ip[:]
	}
	stride := width*2 + 2
	i := sort.Search(len(table)/stride, func(i int) bool {
		return bytes.Compare(table[i*stride:i*stride+width], key) > 0
	}) - 1
	if i < 0 {
		return ""
	}
	record := table[i*stride : (i+1)*stride]
	if bytes.Compare(key, record[width:width*2]) > 0 {
		return ""
	}
	code := string(record[width*2:])
	// DB-IP also uses ZZ (unknown) and XK (not an ISO 3166-1 code).
	// Neither is selectable in the fixed ISO region list.
	if code == "ZZ" || code == "XK" {
		return ""
	}
	return code
}

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// DetectionResult describes the public address selected for country lookup.
type DetectionResult struct {
	Region string `json:"region"`
	IP     string `json:"ip"`
	Family string `json:"family"`
	Source string `json:"source"`
	Reason string `json:"reason"`
}

// DetectNodeAddresses selects public IPv4 before IPv6. Each DNS family has a
// shared two-second deadline; no external geolocation service is contacted.
func DetectNodeAddresses(v4, general, v6 string) DetectionResult {
	return detectNodeAddresses(v4, general, v6, net.DefaultResolver)
}

func detectNodeAddresses(v4, general, v6 string, dns resolver) DetectionResult {
	v4, general, v6 = strings.TrimSpace(v4), strings.TrimSpace(general), strings.TrimSpace(v6)
	privateV4, privateV6, dnsFailed := false, false, false
	resultFor := func(ip netip.Addr, source, reason string) DetectionResult {
		ip = ip.Unmap()
		family := "v6"
		if ip.Is4() {
			family = "v4"
		}
		region := Lookup(ip)
		if region == "" {
			reason = "未查到该 IP 的地区"
		}
		return DetectionResult{Region: region, IP: ip.String(), Family: family, Source: source, Reason: reason}
	}
	literal := func(addr string, wantV4 bool) netip.Addr {
		ip, err := netip.ParseAddr(addr)
		if err != nil || ip.Unmap().Is4() != wantV4 {
			return netip.Addr{}
		}
		if public(ip) {
			return ip.Unmap()
		}
		if wantV4 {
			privateV4 = true
		} else {
			privateV6 = true
		}
		return netip.Addr{}
	}
	var domains []string
	for _, addr := range []string{general, v4} {
		if addr == "" || (len(domains) > 0 && domains[0] == addr) {
			continue
		}
		if _, err := netip.ParseAddr(addr); err != nil {
			domains = append(domains, addr)
		}
	}
	lookup := func(wantV4 bool) netip.Addr {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		network := "ip6"
		if wantV4 {
			network = "ip4"
		}
		for _, domain := range domains {
			ips, err := dns.LookupNetIP(ctx, network, domain)
			if err != nil {
				dnsFailed = true
				continue
			}
			for _, ip := range ips {
				if !ip.IsValid() || ip.Unmap().Is4() != wantV4 {
					continue
				}
				if public(ip) {
					return ip.Unmap()
				}
				if wantV4 {
					privateV4 = true
				} else {
					privateV6 = true
				}
			}
		}
		return netip.Addr{}
	}
	for _, candidate := range []struct{ addr, source string }{{v4, "server_ip_v4"}, {general, "server_ip"}} {
		if ip := literal(candidate.addr, true); ip.IsValid() {
			return resultFor(ip, candidate.source, "")
		}
	}
	if ip := lookup(true); ip.IsValid() {
		return resultFor(ip, "dns_a", "")
	}
	fallbackReason := "未找到公网 IPv4，已改用 IPv6 识别"
	if privateV4 {
		fallbackReason = "IPv4 为内网地址，已改用 IPv6 识别"
	} else if dnsFailed {
		fallbackReason = "IPv4 域名解析失败，已改用 IPv6 识别"
	}
	for _, candidate := range []struct{ addr, source string }{{v6, "server_ip_v6"}, {general, "server_ip"}} {
		if ip := literal(candidate.addr, false); ip.IsValid() {
			return resultFor(ip, candidate.source, fallbackReason)
		}
	}
	if ip := lookup(false); ip.IsValid() {
		return resultFor(ip, "dns_aaaa", fallbackReason)
	}
	reason := "未找到公网地址，请手动选择地区"
	if privateV4 && privateV6 {
		reason = "IPv4/IPv6 均为内网地址，请手动选择地区"
	} else if dnsFailed {
		reason = "域名解析失败"
	} else if privateV4 {
		reason = "IPv4 为内网地址，请手动选择地区"
	} else if privateV6 {
		reason = "IPv6 为内网地址，请手动选择地区"
	}
	return DetectionResult{Reason: reason}
}
