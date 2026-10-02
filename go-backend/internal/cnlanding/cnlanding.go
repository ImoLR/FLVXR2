// Package cnlanding checks forward landing targets against the APNIC mainland
// China address allocations embedded in the panel binary.
package cnlanding

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
)

//go:generate go run ./gen -output data/cn_cidrs.txt

//go:embed data/cn_cidrs.txt
var embeddedCIDRs string

const lookupTimeout = 3 * time.Second

// Resolver is the subset of net.Resolver used by Checker.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// ErrorKind identifies whether a target was positively matched or could not be
// resolved. Jobs use this distinction so transient DNS failures do not pause a
// forward.
type ErrorKind uint8

const (
	ErrorInvalid ErrorKind = iota + 1
	ErrorResolution
	ErrorMainland
)

// CheckError is returned for rejected landing targets.
type CheckError struct {
	Kind ErrorKind
	Msg  string
}

func (e *CheckError) Error() string { return e.Msg }

// IsMainland reports whether err is a positive mainland-China match.
func IsMainland(err error) bool {
	var checkErr *CheckError
	return errors.As(err, &checkErr) && checkErr.Kind == ErrorMainland
}

// IsResolutionFailure reports whether err came from a failed or empty DNS lookup.
func IsResolutionFailure(err error) bool {
	var checkErr *CheckError
	return errors.As(err, &checkErr) && checkErr.Kind == ErrorResolution
}

type prefixNode struct {
	child      [2]*prefixNode
	blocked    bool
	hasBlocked bool
}

type prefixSet struct {
	v4 prefixNode
	v6 prefixNode
}

func (s *prefixSet) add(prefix netip.Prefix) {
	prefix = prefix.Masked()
	root := &s.v6
	if prefix.Addr().Is4() {
		root = &s.v4
	}
	node := root
	node.hasBlocked = true
	for bit := 0; bit < prefix.Bits(); bit++ {
		index := addressBit(prefix.Addr(), bit)
		if node.child[index] == nil {
			node.child[index] = &prefixNode{}
		}
		node = node.child[index]
		node.hasBlocked = true
	}
	node.blocked = true
}

func (s *prefixSet) contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	node := &s.v6
	bits := 128
	if addr.Is4() {
		node = &s.v4
		bits = 32
	}
	for bit := 0; node != nil && bit <= bits; bit++ {
		if node.blocked {
			return true
		}
		if bit == bits {
			break
		}
		node = node.child[addressBit(addr, bit)]
	}
	return false
}

func (s *prefixSet) overlaps(prefix netip.Prefix) bool {
	prefix = prefix.Masked()
	node := &s.v6
	if prefix.Addr().Is4() {
		node = &s.v4
	}
	for bit := 0; node != nil && bit < prefix.Bits(); bit++ {
		if node.blocked {
			return true
		}
		node = node.child[addressBit(prefix.Addr(), bit)]
	}
	return node != nil && (node.blocked || node.hasBlocked)
}

func addressBit(addr netip.Addr, bit int) byte {
	if addr.Is4() {
		bytes := addr.As4()
		return (bytes[bit/8] >> (7 - uint(bit%8))) & 1
	}
	bytes := addr.As16()
	return (bytes[bit/8] >> (7 - uint(bit%8))) & 1
}

// Checker validates remote addresses and optional WG target CIDRs.
type Checker struct {
	prefixes *prefixSet
	resolver Resolver
	timeout  time.Duration
}

var defaultPrefixes = mustLoadPrefixes(embeddedCIDRs)

// New returns a checker using resolver. A nil resolver uses the system DNS
// resolver. DNS lookups have a three-second timeout per domain.
func New(resolver Resolver) *Checker {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &Checker{prefixes: defaultPrefixes, resolver: resolver, timeout: lookupTimeout}
}

// Check validates all comma-separated remote targets and an optional CIDR.
func (c *Checker) Check(ctx context.Context, remoteAddr, targetCIDR string) error {
	if c == nil {
		c = New(nil)
	}
	for _, target := range splitTargets(remoteAddr) {
		host, _, err := net.SplitHostPort(target)
		if err != nil {
			return &CheckError{Kind: ErrorInvalid, Msg: fmt.Sprintf("落地地址 %s 格式错误，请使用地址:端口", target)}
		}
		host = strings.Trim(strings.TrimSpace(host), "[]")
		if host == "" {
			return &CheckError{Kind: ErrorInvalid, Msg: fmt.Sprintf("落地地址 %s 格式错误，请使用地址:端口", target)}
		}

		if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
			addr = addr.Unmap()
			if c.prefixes.contains(addr) {
				return &CheckError{Kind: ErrorMainland, Msg: fmt.Sprintf("落地地址 %s 位于中国大陆，不允许使用", addr)}
			}
			continue
		}

		lookupCtx, cancel := context.WithTimeout(ctx, c.timeout)
		addresses, lookupErr := c.resolver.LookupNetIP(lookupCtx, "ip", host)
		cancel()
		if lookupErr != nil || len(addresses) == 0 {
			return &CheckError{Kind: ErrorResolution, Msg: fmt.Sprintf("域名 %s 解析失败，请检查域名是否正确或更换落地地址", host)}
		}
		addresses = uniqueSortedAddresses(addresses)
		for _, addr := range addresses {
			if c.prefixes.contains(addr) {
				return &CheckError{Kind: ErrorMainland, Msg: fmt.Sprintf("落地地址 %s → %s 位于中国大陆，不允许使用", host, addr)}
			}
		}
	}

	targetCIDR = strings.TrimSpace(targetCIDR)
	if targetCIDR == "" {
		return nil
	}
	prefix, err := netip.ParsePrefix(targetCIDR)
	if err != nil {
		return &CheckError{Kind: ErrorInvalid, Msg: fmt.Sprintf("落地网段 %s 格式错误", targetCIDR)}
	}
	if c.prefixes.overlaps(prefix) {
		return &CheckError{Kind: ErrorMainland, Msg: fmt.Sprintf("落地网段 %s 与中国大陆地址范围重叠，不允许使用", prefix.Masked())}
	}
	return nil
}

func splitTargets(remoteAddr string) []string {
	parts := strings.Split(remoteAddr, ",")
	targets := make([]string, 0, len(parts))
	for _, part := range parts {
		if target := strings.TrimSpace(part); target != "" {
			targets = append(targets, target)
		}
	}
	return targets
}

func uniqueSortedAddresses(addresses []netip.Addr) []netip.Addr {
	seen := make(map[netip.Addr]struct{}, len(addresses))
	result := make([]netip.Addr, 0, len(addresses))
	for _, addr := range addresses {
		addr = addr.Unmap()
		if !addr.IsValid() {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		result = append(result, addr)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Compare(result[j]) < 0 })
	return result
}

func mustLoadPrefixes(data string) *prefixSet {
	set := &prefixSet{}
	for lineNumber, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		prefix, err := netip.ParsePrefix(line)
		if err != nil {
			panic(fmt.Sprintf("invalid embedded CN CIDR on line %d: %v", lineNumber+1, err))
		}
		set.add(prefix)
	}
	return set
}
