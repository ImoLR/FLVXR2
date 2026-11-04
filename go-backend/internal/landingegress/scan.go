package landingegress

import (
	"errors"
	"fmt"
	"time"
)

// Plan 202 「禁止端口扫描」: a built-in behaviour rule of FLVX-native landing
// nodes. The pinned landing Core (Custom v1.4.83, scan_guard.v1) counts every
// outbound TCP dial per node and blocks the node itself when one of three
// patterns crosses its threshold within the window; the panel then disables
// the node until an admin lifts it. UDP is never counted. Plan 205: mmwx
// shares whose Helper + FLVX Core advertise it (landing-scan-guard-v1 §9) run
// the same rule; older shares show 共享服务器暂不支持.

// Fixed windows and web ports (only the thresholds are editable).
const (
	ScanWindowS      = 60
	ScanTotalWindowS = 300
)

// ScanWebPorts use HorizontalWeb instead of Horizontal.
var ScanWebPorts = []int{80, 443}

// Hit types reported by the Core.
const (
	ScanHorizontal = "horizontal"
	ScanVertical   = "vertical"
	ScanTotal      = "total"
)

// ScanGuard is the admin's global setting.
type ScanGuard struct {
	On            bool `json:"on"`
	Horizontal    int  `json:"horizontal"`
	HorizontalWeb int  `json:"horizontalWeb"`
	Vertical      int  `json:"vertical"`
	Total         int  `json:"total"`
}

// ScanGuardDefaults: ON (a 内置 rule), 横向 100 IPs / 60 s (80/443: 500),
// 纵向 30 ports / 60 s, 总量 1000 IPs / 5 min.
func ScanGuardDefaults() ScanGuard {
	return ScanGuard{On: true, Horizontal: 100, HorizontalWeb: 500, Vertical: 30, Total: 1000}
}

// Validate keeps the thresholds in the Core's accepted range; the minimums
// stop an admin from disabling every node with a typo (a normal page load
// reaches a few dozen IPs).
func (g ScanGuard) Validate() error {
	check := func(v, min int, name string) error {
		if v < min || v > 100000 {
			return fmt.Errorf("%s需在 %d–100000 之间", name, min)
		}
		return nil
	}
	return errors.Join(
		check(g.Horizontal, 20, "横向扫描阈值"),
		check(g.HorizontalWeb, 20, "80/443 横向扫描阈值"),
		check(g.Vertical, 10, "纵向扫描阈值"),
		check(g.Total, 100, "总量阈值"),
	)
}

// ScanReason is the short reason of a hit, e.g. 「同一端口 22 在 60 秒内连接了
// 100 个不同 IP」; used for the user message, the node card and Telegram.
func ScanReason(kind string, count, port int, host string, windowS int) string {
	if windowS <= 0 {
		windowS = ScanWindowS
	}
	span := fmt.Sprintf("%d 秒", windowS)
	if windowS%60 == 0 && windowS >= 120 {
		span = fmt.Sprintf("%d 分钟", windowS/60)
	}
	switch kind {
	case ScanHorizontal:
		return fmt.Sprintf("同一端口 %d 在 %s内连接了 %d 个不同 IP", port, span, count)
	case ScanVertical:
		if host == "" {
			return fmt.Sprintf("同一个 IP 在 %s内被连接了 %d 个不同端口", span, count)
		}
		return fmt.Sprintf("同一个 IP %s 在 %s内被连接了 %d 个不同端口", host, span, count)
	case ScanTotal:
		return fmt.Sprintf("%s内连接了 %d 个不同 IP", span, count)
	}
	return fmt.Sprintf("%s内连接数异常（%d）", span, count)
}

// ScanTypeLabel: 横向 / 纵向 / 总量.
func ScanTypeLabel(kind string) string {
	switch kind {
	case ScanHorizontal:
		return "横向"
	case ScanVertical:
		return "纵向"
	case ScanTotal:
		return "总量"
	}
	return kind
}

// ScanUserMessage is what the owner sees on a disabled node.
func ScanUserMessage(reason string) string {
	return "该落地节点因检测到端口扫描（" + reason + "）已被禁用，如有疑问请联系管理员"
}

// ScanCreateMessage (plan 205) refuses a new landing node to a user whose
// node is (or was, until an admin lifts it) disabled by a scan.
func ScanCreateMessage(reason string) string {
	return "你的落地节点因检测到端口扫描（" + reason + "）已被禁用，暂不能新建落地节点，如有疑问请联系管理员"
}

// ScanTime formats a hit time for messages (UTC+8, the panel's zone).
func ScanTime(ms int64) string {
	return time.UnixMilli(ms).In(time.FixedZone("UTC+8", 8*3600)).Format("2006-01-02 15:04:05")
}
