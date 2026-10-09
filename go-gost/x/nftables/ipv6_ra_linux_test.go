//go:build linux

package nftables

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestIPv6RAPreserve(t *testing.T) {
	root := t.TempDir()
	values := map[string]int{"all": 1, "lo": 1, "default": 1, "eth0": 1, "eth1": 0, "eth2": 2}
	for name, value := range values {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "accept_ra"), []byte(fmt.Sprint(value)), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r := &raCompatibility{}
	now := time.Unix(1000, 0)
	writes := 0
	write := func(path string) error { writes++; return osWriteRA(path) }
	r.preserve(root, now, write)
	r.preserve(root, now.Add(time.Minute), write)
	if writes != 2 || len(r.changed) != 2 || !r.changed["eth0"].Equal(now) || !r.changed["default"].Equal(now) {
		t.Fatalf("writes=%d changes=%v", writes, r.changed)
	}
	for name, original := range values {
		want := original
		if name == "eth0" || name == "default" {
			want = 2
		}
		if got, err := readRA(filepath.Join(root, name, "accept_ra")); err != nil || got != want {
			t.Fatalf("%s=%d want %d: %v", name, got, want, err)
		}
	}
}

func TestIPv6RAWriteFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "eth0"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "eth0/accept_ra"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	r := &raCompatibility{enabled: true}
	r.preserve(root, time.Now(), func(string) error { return errors.New("read-only proc") })
	status, detail := r.classify(nil, time.Now(), raManagerRemedy)
	if status != "error" || len(r.changed) != 0 || !strings.Contains(detail, "eth0") || !strings.Contains(detail, "写入 2 失败") {
		t.Fatalf("%s %s %+v", status, detail, r)
	}
}

func TestIPv6RAClassification(t *testing.T) {
	now := time.Unix(10000, 0)
	for _, tc := range []struct {
		name                               string
		enabled, dependent, address, route bool
		accept                             int
		changed                            bool
		elapsed                            time.Duration
		want, detail                       string
	}{
		{"already compatible", true, true, true, true, 2, false, 0, "ok", "原本即为 2"},
		{"changed compatible", true, true, true, true, 2, true, 0, "ok", "本次由 agent 1→2"},
		{"waiting for address", true, true, false, true, 2, true, time.Minute, "warn", "等待下一次 RA"},
		{"waiting for route", true, true, true, false, 2, true, 29 * time.Minute, "warn", "IPv6 默认路由"},
		{"already lost both", true, false, false, false, 2, true, 0, "warn", "全局 IPv6 地址及默认路由"},
		{"timeout", true, false, false, false, 2, true, 30 * time.Minute, "error", "已满30分钟"},
		{"userspace RA", true, true, true, true, 0, false, 0, "error", "IPv6AcceptRA=yes"},
		{"not RA based", true, false, true, true, 2, false, 0, "", ""},
		{"not enabled by us", false, true, true, true, 0, false, 0, "", ""},
		{"setting reverted", true, true, true, true, 1, true, 0, "error", "请设为 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &raCompatibility{enabled: tc.enabled, changed: map[string]time.Time{}}
			if tc.changed {
				r.changed["eth0"] = now.Add(-tc.elapsed)
			}
			status, detail := r.classify([]raInterface{{name: "eth0", acceptRA: tc.accept, dependent: tc.dependent, address: tc.address, route: tc.route}}, now, func(int) string { return "systemd-networkd：IPv6AcceptRA=yes" })
			if status != tc.want || !strings.Contains(detail, tc.detail) {
				t.Fatalf("got %q %q", status, detail)
			}
		})
	}
	r := &raCompatibility{enabled: true, changed: map[string]time.Time{"default": now}}
	if status, detail := r.classify(nil, now, raManagerRemedy); status != "" || detail != "" {
		t.Fatalf("default is not an interface: %s %s", status, detail)
	}
	var ifaces []raInterface
	for i := 0; i < 20; i++ {
		ifaces = append(ifaces, raInterface{name: fmt.Sprintf("eth%d", i), acceptRA: 2, dependent: true, address: true, route: true})
	}
	ifaces = append(ifaces, raInterface{name: "broken", acceptRA: 0, dependent: true})
	status, detail := r.classify(ifaces, now, func(int) string { return "请检查网络管理器" })
	if status != "error" || !strings.HasPrefix(detail, "broken") || len([]rune(detail)) > 300 {
		t.Fatalf("worst first/bounded: %s %s", status, detail)
	}
}

func TestIPv6RADynamicAddress(t *testing.T) {
	for _, tc := range []struct {
		name            string
		lifetime, flags int
		want            bool
	}{
		{"static", int(^uint32(0)), unix.IFA_F_PERMANENT, false},
		{"finite", 60, unix.IFA_F_PERMANENT, true},
		{"temporary manager", int(^uint32(0)), unix.IFA_F_PERMANENT | unix.IFA_F_MANAGETEMPADDR, true},
		{"non permanent", int(^uint32(0)), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := raDynamicAddress(netlink.Addr{ValidLft: tc.lifetime, Flags: tc.flags}); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

// Run only via a compiled test binary in a throwaway named netns. This calls
// the actual forwarding function, never NewManager or nft rule mutations.
func TestIPv6RANetNS(t *testing.T) {
	mode := os.Getenv("FLVX_RA_NETNS")
	if mode == "" {
		t.Skip("isolated netns E2E only")
	}
	self, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	host, err := os.Readlink("/proc/1/ns/net")
	if err != nil || self == host {
		t.Fatal("refusing real host network namespace")
	}
	enableIPForwarding()
	if mode == "userspace" {
		status, detail := IPv6RAStatus()
		t.Logf("status=%s detail=%s", status, detail)
		if status != "error" || !strings.Contains(detail, "accept_ra=0") {
			t.Fatal("expected userspace RA error")
		}
		return
	}
	for elapsed := 0; elapsed <= 140; elapsed += 10 {
		status, detail := IPv6RAStatus()
		t.Logf("elapsed=%ds status=%s detail=%s", elapsed, status, detail)
		if elapsed >= 20 && status != "ok" {
			t.Fatalf("RA did not survive forwarding at %ds", elapsed)
		}
		if elapsed < 140 {
			time.Sleep(10 * time.Second)
		}
	}
}
