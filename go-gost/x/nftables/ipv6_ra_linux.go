//go:build linux

package nftables

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type raCompatibility struct {
	mu      sync.Mutex
	enabled bool // Only report when this process successfully enabled forwarding.
	changed map[string]time.Time
	failed  map[string]string
}

var ipv6RA raCompatibility

func osWriteRA(path string) error { return os.WriteFile(path, []byte("2\n"), 0644) }

// Called under mu, before all.forwarding=1. Keep the first change time across
// reconnects, so retries cannot postpone the recovery deadline or repeat logs.
func (r *raCompatibility) preserve(root string, now time.Time, write func(string) error) {
	if r.changed == nil {
		r.changed = make(map[string]time.Time)
		r.failed = make(map[string]string)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		r.failed["IPv6"] = "读取 accept_ra 失败，请检查 /proc/sys 权限"
		return
	}
	delete(r.failed, "IPv6")
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == "lo" || name == "all" {
			continue
		}
		path := filepath.Join(root, name, "accept_ra")
		value, err := readRA(path)
		if err != nil {
			r.failed[name] = "读取 accept_ra 失败，请检查 /proc/sys 权限"
			continue
		}
		if value != 1 {
			continue
		}
		if err := write(path); err != nil {
			r.failed[name] = "accept_ra=1，写入 2 失败；请检查权限并设为 2"
			continue
		}
		delete(r.failed, name)
		if _, ok := r.changed[name]; !ok {
			r.changed[name] = now
			fmt.Printf("IPv6 RA 兼容: %s accept_ra 1→2\n", name)
		}
	}
}

func readRA(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(raw)))
}

type raInterface struct {
	name                      string
	index, acceptRA           int
	dependent, address, route bool
	readError                 bool
}

func collectRAInterfaces(root string) ([]raInterface, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}
	addrs, err := netlink.AddrList(nil, netlink.FAMILY_V6)
	if err != nil {
		return nil, err
	}
	// Include policy-routing tables as well as main.
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V6, &netlink.Route{Table: unix.RT_TABLE_UNSPEC}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return nil, err
	}
	var result []raInterface
	for _, link := range links {
		attrs := link.Attrs()
		if attrs.Name == "lo" {
			continue
		}
		iface := raInterface{name: attrs.Name, index: attrs.Index}
		iface.acceptRA, err = readRA(filepath.Join(root, attrs.Name, "accept_ra"))
		iface.readError = err != nil
		for _, addr := range addrs {
			if addr.LinkIndex != attrs.Index || addr.IPNet == nil || addr.Scope != unix.RT_SCOPE_UNIVERSE || !addr.IP.IsGlobalUnicast() || addr.IP.To4() != nil {
				continue
			}
			iface.dependent = iface.dependent || raDynamicAddress(addr)
			if addr.Flags&(unix.IFA_F_TENTATIVE|unix.IFA_F_DADFAILED) == 0 {
				iface.address = true
			}
		}
		for _, route := range routes {
			if route.Type != unix.RTN_UNICAST {
				continue
			}
			if route.Dst != nil {
				ones, _ := route.Dst.Mask.Size()
				if ones != 0 {
					continue
				}
			}
			matches := route.LinkIndex == attrs.Index
			for _, hop := range route.MultiPath {
				matches = matches || hop.LinkIndex == attrs.Index
			}
			if matches {
				iface.route = true
				iface.dependent = iface.dependent || route.Protocol == unix.RTPROT_RA
			}
		}
		result = append(result, iface)
	}
	return result, nil
}

func raDynamicAddress(addr netlink.Addr) bool {
	return uint32(addr.ValidLft) != ^uint32(0) || addr.Flags&unix.IFA_F_MANAGETEMPADDR != 0 || addr.Flags&unix.IFA_F_PERMANENT == 0
}

func raManagerRemedy(index int) string {
	if raw, err := os.ReadFile(filepath.Join("/run/systemd/netif/links", strconv.Itoa(index))); err == nil && strings.Contains(string(raw), "NETWORK_FILE=") {
		return "systemd-networkd：配置 IPv6AcceptRA=yes 并确认转发下仍接收 RA"
	}
	if _, err := exec.LookPath("nmcli"); err == nil {
		return "检测到 NetworkManager：配置 ipv6.method auto 并确认转发下仍接收 RA"
	}
	return "检查用户态网络管理器的 RA 配置，确保转发下仍接收 RA"
}

func (r *raCompatibility) classify(ifaces []raInterface, now time.Time, remedy func(int) string) (string, string) {
	if !r.enabled {
		return "", ""
	}
	type issue struct {
		level  int
		detail string
	}
	var issues []issue
	for name, reason := range r.failed {
		issues = append(issues, issue{3, name + ": " + reason})
	}
	for _, iface := range ifaces {
		changed, byUs := r.changed[iface.name]
		if iface.name == "lo" || iface.name == "default" || iface.name == "all" || (!iface.dependent && !byUs) {
			continue
		}
		if _, failed := r.failed[iface.name]; failed {
			continue
		}
		detail := fmt.Sprintf("%s accept_ra=%d", iface.name, iface.acceptRA)
		level := 1
		switch {
		case iface.readError:
			level, detail = 3, iface.name+"：读取 accept_ra 失败，请检查 /proc/sys 权限"
		case iface.acceptRA == 0:
			level, detail = 3, detail+"；"+remedy(iface.index)
		case iface.acceptRA != 2:
			level, detail = 3, detail+"；转发下无法接收 RA，请设为 2 并检查网络管理器"
		default:
			if byUs {
				detail += "（本次由 agent 1→2）"
			} else {
				detail += "（原本即为 2）"
			}
			if !iface.address || !iface.route {
				missing := "全局 IPv6 地址"
				if !iface.route {
					missing = "IPv6 默认路由"
					if !iface.address {
						missing = "全局 IPv6 地址及默认路由"
					}
				}
				level = 2
				detail += "；缺少" + missing
				if !byUs || now.Sub(changed) >= 30*time.Minute {
					level = 3
					if byUs {
						detail += "，兼容后已满30分钟"
					}
					detail += "；请检查上游 RA 和网络管理器"
				} else {
					detail += "，等待下一次 RA"
				}
			}
		}
		issues = append(issues, issue{level, detail})
	}
	if len(issues) == 0 {
		return "", ""
	}
	// Put actionable errors first so truncation never hides the worst result.
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].level != issues[j].level {
			return issues[i].level > issues[j].level
		}
		return issues[i].detail < issues[j].detail
	})
	var details []string
	for _, issue := range issues {
		details = append(details, issue.detail)
	}
	return []string{"", "ok", "warn", "error"}[issues[0].level], limitRADetail(strings.Join(details, "；"))
}

func limitRADetail(detail string) string {
	runes := []rune(detail)
	if len(runes) > 300 {
		return string(runes[:299]) + "…"
	}
	return detail
}

// IPv6RAStatus is a read-only snapshot, called at startup and every ten minutes.
func IPv6RAStatus() (string, string) {
	ipv6RA.mu.Lock()
	defer ipv6RA.mu.Unlock()
	if !ipv6RA.enabled {
		return "", ""
	}
	root := "/proc/sys/net/ipv6/conf"
	forwarding, err := readRA(filepath.Join(root, "all/forwarding"))
	if err == nil && forwarding != 1 {
		return "", ""
	}
	if err != nil {
		return "error", "读取 IPv6 forwarding 失败；请检查 /proc/sys 权限"
	}
	ifaces, err := collectRAInterfaces(root)
	if err != nil {
		return "error", "IPv6 RA 状态检测失败；请检查 netlink 访问权限"
	}
	return ipv6RA.classify(ifaces, time.Now(), raManagerRemedy)
}
