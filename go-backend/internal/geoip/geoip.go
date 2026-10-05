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
	if code == "ZZ" {
		return ""
	}
	return code
}

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// DetectNodeRegion uses a literal IP or the first public DNS result. DNS has a
// two-second deadline; no external geolocation service is contacted.
func DetectNodeRegion(addr string) string {
	return detect(addr, net.DefaultResolver)
}

func detect(addr string, dns resolver) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if ip, err := netip.ParseAddr(addr); err == nil {
		return Lookup(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := dns.LookupNetIP(ctx, "ip", addr)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if public(ip) {
			return Lookup(ip)
		}
	}
	return ""
}

// DetectNodeAddresses prefers IPv4, then the general address, then IPv6.
func DetectNodeAddresses(v4, general, v6 string) string {
	seen := make(map[string]bool, 3)
	for _, addr := range []string{v4, general, v6} {
		addr = strings.TrimSpace(addr)
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		if region := DetectNodeRegion(addr); region != "" {
			return region
		}
	}
	return ""
}
