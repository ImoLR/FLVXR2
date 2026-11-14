package linemon

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"sort"
	"strings"
)

// Plan 255 part 2 「检测端口」 for a custom target (a machine that is not an
// FLVX node): the panel cannot open a port there, so it generates a command
// the admin pastes as root on that machine (Debian / Ubuntu, systemd). The
// command installs a self-expiring check port:
//
//	flvx-linemon-<port>.socket          Accept=yes, accepts and closes
//	flvx-linemon-<port>@.service        /bin/true per connection, sandboxed
//	flvx-linemon-<port>-watch.timer     every 10 min
//	flvx-linemon-<port>-watch.service   runs /var/lib/flvx-linemon/<port>/watch.sh
//
// The watchdog compares the socket's NAccepted with the value it saw last
// time; no new connection for the idle window → it disables, stops and
// deletes its own 4 units and its state directory (nothing else).
//
// Only integers and fixed words are interpolated (port, window seconds, IPs
// re-printed by netip); no label, host or other user text ever appears in the
// command, and nothing is downloaded.

// CheckWindows are the idle windows the dialog offers (hours).
var CheckWindows = []int{1, 6, 24, 72}

// DefaultCheckWindow is the default idle window (hours).
const DefaultCheckWindow = 24

// MaxCheckAllow bounds the source IPs written into IPAddressAllow.
const MaxCheckAllow = 32

const checkPortTick = 600 // watchdog interval (s)

// ValidCheckWindow reports whether hours is one of CheckWindows.
func ValidCheckWindow(hours int) bool {
	for _, h := range CheckWindows {
		if h == hours {
			return true
		}
	}
	return false
}

// ValidCheckPort: 1024–65535 and not 22.
func ValidCheckPort(port int) bool { return port >= 1024 && port <= 65535 && port != 22 }

// CheckPortScript returns the install and undo commands. allow = source IPs
// that may connect (empty = everyone).
func CheckPortScript(port, windowHours int, allow []netip.Addr) (install, undo string, err error) {
	if !ValidCheckPort(port) {
		return "", "", errors.New("检测端口必须在 1024-65535 之间，且不能是 22")
	}
	if !ValidCheckWindow(windowHours) {
		return "", "", errors.New("自动删除时间不正确")
	}
	if len(allow) > MaxCheckAllow {
		return "", "", fmt.Errorf("来源 IP 最多 %d 个", MaxCheckAllow)
	}
	ips := make([]string, 0, len(allow))
	seen := map[string]bool{}
	for _, a := range allow {
		if !a.IsValid() {
			return "", "", errors.New("来源 IP 不正确")
		}
		s := a.Unmap().String() // re-printed: digits, hex, '.' and ':' only
		if !seen[s] {
			seen[s] = true
			ips = append(ips, s)
		}
	}
	sort.Strings(ips)
	idle := windowHours * 3600

	var b strings.Builder
	w := func(format string, args ...interface{}) { fmt.Fprintf(&b, format+"\n", args...) }
	w("sh <<'FLVX_LINEMON'")
	w("# FLVX 线路监测检测端口：只接受连接并立即关闭，%d 小时无连接自动删除", windowHours)
	w("set -eu")
	w("P=%d", port)
	w("W=${FLVX_LINEMON_TEST_IDLE:-%d}", idle)
	w("T=${FLVX_LINEMON_TEST_TICK:-%d}", checkPortTick)
	w(`case "$W:$T" in *[!0-9:]*|:*|*:) echo "FLVX: 参数错误" >&2; exit 1;; esac`)
	w(`[ "$(id -u)" = 0 ] || { echo "FLVX: 请用 root 执行" >&2; exit 1; }`)
	w(`command -v systemctl >/dev/null 2>&1 || { echo "FLVX: 需要 systemd" >&2; exit 1; }`)
	w(`if ! systemctl is-active --quiet "flvx-linemon-$P.socket" && ss -Hltn "sport = :$P" 2>/dev/null | grep -q .; then`)
	w(`  echo "FLVX: 端口 $P 已被其它程序占用，请在面板里换一个端口" >&2; exit 1`)
	w(`fi`)
	w(`U=/etc/systemd/system/flvx-linemon-$P`)
	w(`D=/var/lib/flvx-linemon/$P`)
	w(`mkdir -p "$D"`)
	w(`cat > "$U.socket" <<EOF`)
	w(`[Unit]`)
	w(`Description=FLVX line monitor check port $P (accept and close)`)
	w(`[Socket]`)
	w(`ListenStream=$P`)
	w(`Accept=yes`)
	w(`MaxConnections=16`)
	w(`MaxConnectionsPerSource=4`)
	if len(ips) > 0 {
		w(`IPAddressAllow=%s`, strings.Join(ips, " "))
		w(`IPAddressDeny=any`)
	}
	w(`[Install]`)
	w(`WantedBy=sockets.target`)
	w(`EOF`)
	w(`cat > "$U@.service" <<EOF`)
	w(`[Unit]`)
	w(`Description=FLVX line monitor check port $P (close at once)`)
	w(`[Service]`)
	w(`ExecStart=/bin/true`)
	w(`StandardInput=null`)
	w(`StandardOutput=null`)
	w(`DynamicUser=yes`)
	w(`ProtectSystem=strict`)
	w(`ProtectHome=yes`)
	w(`PrivateTmp=yes`)
	w(`NoNewPrivileges=yes`)
	w(`CapabilityBoundingSet=`)
	w(`RestrictAddressFamilies=AF_UNIX`)
	w(`RuntimeMaxSec=5`)
	w(`MemoryMax=8M`)
	w(`TasksMax=4`)
	w(`LogLevelMax=warning`)
	w(`EOF`)
	w(`cat > "$U-watch.service" <<EOF`)
	w(`[Unit]`)
	w(`Description=FLVX line monitor check port $P idle watchdog`)
	w(`[Service]`)
	w(`Type=oneshot`)
	w(`ExecStart=/bin/sh $D/watch.sh`)
	w(`NoNewPrivileges=yes`)
	w(`ProtectSystem=strict`)
	w(`ReadWritePaths=/etc/systemd/system /var/lib/flvx-linemon`)
	w(`ProtectHome=yes`)
	w(`PrivateTmp=yes`)
	w(`LogLevelMax=notice`)
	w(`EOF`)
	w(`cat > "$U-watch.timer" <<EOF`)
	w(`[Unit]`)
	w(`Description=FLVX line monitor check port $P idle watchdog`)
	w(`[Timer]`)
	w(`OnActiveSec=${T}s`)
	w(`OnUnitActiveSec=${T}s`)
	w(`AccuracySec=30s`)
	w(`[Install]`)
	w(`WantedBy=timers.target`)
	w(`EOF`)
	w(`{ echo "P=$P"; echo "W=$W"; cat <<'EOW'`)
	w(`# FLVX line monitor check port watchdog: removes only its own units and state.`)
	w(`S=/var/lib/flvx-linemon/$P/state`)
	w(`n=$(systemctl show -p NAccepted --value "flvx-linemon-$P.socket" 2>/dev/null) || exit 0`)
	w(`case "$n" in ''|*[!0-9]*) exit 0;; esac`)
	w(`now=$(date +%%s)`)
	w(`last_n=x; last_t=$now`)
	w(`[ -f "$S" ] && read -r last_n last_t < "$S" || true`)
	w(`case "$last_t" in ''|*[!0-9]*) last_t=$now;; esac`)
	w(`if [ "$n" != "$last_n" ]; then echo "$n $now" > "$S"; exit 0; fi`)
	w(`[ $((now - last_t)) -ge "$W" ] || exit 0`)
	w(`if [ "$W" -ge 3600 ]; then idle="$((W / 3600)) h"; else idle="$W s"; fi`)
	w(`echo "<5>flvx-linemon $P: idle $idle, removed"`)
	w(`systemctl disable --now "flvx-linemon-$P.socket" "flvx-linemon-$P-watch.timer" >/dev/null 2>&1 || true`)
	w(`U=/etc/systemd/system/flvx-linemon-$P`)
	w(`rm -f "$U.socket" "$U@.service" "$U-watch.service" "$U-watch.timer"`)
	w(`rm -f "/var/lib/flvx-linemon/$P/watch.sh" "$S"`)
	w(`rmdir "/var/lib/flvx-linemon/$P" 2>/dev/null || true`)
	w(`rmdir /var/lib/flvx-linemon 2>/dev/null || true`)
	w(`systemctl daemon-reload`)
	w(`EOW`)
	w(`} > "$D/watch.sh"`)
	w(`rm -f "$D/state"`)
	w(`systemctl daemon-reload`)
	w(`systemctl enable "flvx-linemon-$P.socket" "flvx-linemon-$P-watch.timer" >/dev/null 2>&1 || { echo "FLVX: systemctl enable 失败" >&2; exit 1; }`)
	w(`systemctl restart "flvx-linemon-$P.socket" "flvx-linemon-$P-watch.timer"`)
	w(`echo "FLVX: 检测端口 $P 已开启（$((W / 3600)) 小时没有连接会自动删除）"`)
	w(`FLVX_LINEMON`)
	install = b.String()

	b.Reset()
	w("sh <<'FLVX_LINEMON'")
	w("P=%d", port)
	w(`systemctl disable --now "flvx-linemon-$P.socket" "flvx-linemon-$P-watch.timer" >/dev/null 2>&1 || true`)
	w(`U=/etc/systemd/system/flvx-linemon-$P`)
	w(`rm -f "$U.socket" "$U@.service" "$U-watch.service" "$U-watch.timer"`)
	w(`rm -f "/var/lib/flvx-linemon/$P/watch.sh" "/var/lib/flvx-linemon/$P/state"`)
	w(`rmdir "/var/lib/flvx-linemon/$P" 2>/dev/null || true`)
	w(`rmdir /var/lib/flvx-linemon 2>/dev/null || true`)
	w(`systemctl daemon-reload`)
	w(`echo "FLVX: 检测端口 $P 已删除"`)
	w(`FLVX_LINEMON`)
	undo = b.String()
	return install, undo, nil
}

// wellKnownCustom are ports in 20000–29999 that common software uses.
var wellKnownCustom = [][2]int{
	{20000, 20000}, {20005, 20005}, {20808, 20808}, {21025, 21027}, {22000, 22000}, {22222, 22222},
	{23399, 23399}, {23456, 23456}, {24800, 24800}, {25000, 25000}, {25565, 25565}, {25575, 25575},
	{25826, 25826}, {26000, 26000}, {27000, 27050}, {28015, 28017}, {28960, 28960}, {29900, 29900},
}

// SuggestCustomPort picks a free-looking port in 20000–29999: not a
// well-known one, not a round x000, not in used.
func SuggestCustomPort(used map[int]bool) (int, error) {
	var free []int
	for p := 20000; p <= 29999; p++ {
		if p%1000 == 0 || used[p] || !ValidCheckPort(p) {
			continue
		}
		known := false
		for _, r := range wellKnownCustom {
			if p >= r[0] && p <= r[1] {
				known = true
				break
			}
		}
		if !known {
			free = append(free, p)
		}
	}
	if len(free) == 0 {
		return 0, errors.New("没有可用的端口")
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(free))))
	if err != nil {
		return free[0], nil
	}
	return free[n.Int64()], nil
}
