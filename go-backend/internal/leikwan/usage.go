package leikwan

// Plan 273: read-only traffic usage, device and service-summary reads of the
// Forward Lines API (doc updated 2026-11-16). They go through the same
// request path as every other call: one rate limiter per token, the Bearer
// header, the 429 Retry-After block and no token in any error text. The
// decoder is not strict, so new vendor fields (e.g. `ingress {host,port}`,
// `clients[]`) are ignored instead of failing.

import (
	"context"
	"fmt"
	"time"
)

// UsagePeriod times are UTC strings ("2026-11-04 12:08:03").
type UsagePeriod struct {
	Name           string  `json:"name"`
	CycleStartedAt string  `json:"cycle_started_at"`
	CycleEndsAt    string  `json:"cycle_ends_at"`
	CountedSince   string  `json:"counted_since"`
	TrafficResetAt *string `json:"traffic_reset_at"`
}

// UsageTotals: RemainingBytes / UsedPercent are null without included
// traffic; LifetimeUsedBytes is absent in the previous-cycle report.
type UsageTotals struct {
	BillingMode       string   `json:"billing_mode"`
	UsedBytes         int64    `json:"used_bytes"`
	IncludedBytes     int64    `json:"included_bytes"`
	RemainingBytes    *int64   `json:"remaining_bytes"`
	UsedPercent       *float64 `json:"used_percent"`
	BillableBytes     int64    `json:"billable_bytes"`
	LifetimeUsedBytes *int64   `json:"lifetime_used_bytes,omitempty"`
}

// ForwardUsage is one forward's traffic in the cycle (deleted forwards that
// carried traffic in the cycle are listed too, state "deleted").
type ForwardUsage struct {
	ForwardID        int64    `json:"forward_id"`
	State            string   `json:"state"`
	ListenPort       int      `json:"listen_port"`
	Transport        string   `json:"transport"`
	Route            RouteRef `json:"route"`
	Target           Target   `json:"target"`
	BytesIn          int64    `json:"bytes_in"`
	BytesOut         int64    `json:"bytes_out"`
	ExtraBilledBytes int64    `json:"extra_billed_bytes"`
	TotalBytes       int64    `json:"total_bytes"`
	SharePercent     float64  `json:"share_percent"`
}

// DailyUsage is one UTC day of the cycle.
type DailyUsage struct {
	Date             string `json:"date"`
	BytesIn          int64  `json:"bytes_in"`
	BytesOut         int64  `json:"bytes_out"`
	ExtraBilledBytes int64  `json:"extra_billed_bytes"`
	TotalBytes       int64  `json:"total_bytes"`
}

// ServiceUsage = GET /api/v1/user/services/{id}/usage[?period=previous].
type ServiceUsage struct {
	ServiceID         int64          `json:"service_id"`
	Status            string         `json:"status"`
	Period            UsagePeriod    `json:"period"`
	Usage             UsageTotals    `json:"usage"`
	Forwards          []ForwardUsage `json:"forwards"`
	AttributedBytes   int64          `json:"attributed_bytes"`
	UnattributedBytes int64          `json:"unattributed_bytes"`
	Daily             []DailyUsage   `json:"daily"`
}

// Device is one client device (online ones first, then the last 30 days and
// blocked ones). Platform / OS version / build may be null.
type Device struct {
	DeviceID          string  `json:"device_id"`
	ClientID          int64   `json:"client_id"`
	DeviceName        *string `json:"device_name"`
	Platform          *string `json:"platform"`
	OSVersion         *string `json:"os_version"`
	ClientBuild       *string `json:"client_build"`
	UserAgent         *string `json:"user_agent"`
	IP                *string `json:"ip"`
	Online            bool    `json:"online"`
	State             string  `json:"state"`
	CanConnect        bool    `json:"can_connect"`
	CountsTowardLimit bool    `json:"counts_toward_limit"`
	BlockedUntil      *string `json:"blocked_until"`
	FirstSeenAt       *string `json:"first_seen_at"`
	LastSeenAt        *string `json:"last_seen_at"`
	LastHeartbeatAt   *string `json:"last_heartbeat_at"`
}

// DeviceClient is the per-client limit block of the devices report.
type DeviceClient struct {
	ClientID             int64  `json:"client_id"`
	Name                 string `json:"name"`
	OnlineDeviceLimit    int    `json:"online_device_limit"`
	OnlineDeviceCount    int    `json:"online_device_count"`
	AvailableDeviceSlots int    `json:"available_device_slots"`
}

// DeviceList = GET /api/v1/user/devices.
type DeviceList struct {
	OnlineDeviceLimit    int            `json:"online_device_limit"`
	OnlineDeviceCount    int            `json:"online_device_count"`
	AvailableDeviceSlots int            `json:"available_device_slots"`
	LimitSource          string         `json:"limit_source"`
	OnlineWindowSeconds  int            `json:"online_window_seconds"`
	Clients              []DeviceClient `json:"clients"`
	Devices              []Device       `json:"devices"`
}

// ServiceSummary is one row of GET /api/v1/user/services (usage summary +
// expires_at since 2026-11-16).
type ServiceSummary struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Plan   struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"plan"`
	ActiveForwardCount int          `json:"active_forward_count"`
	ForwardLimit       int          `json:"forward_limit"`
	ExpiresAt          *string      `json:"expires_at"`
	Usage              *UsageTotals `json:"usage"`
}

// Bounds on what one report may carry into the panel (a service stores at
// most 512 forward records; a cycle is ≤ 31 days).
const (
	maxUsageForwards = 600
	maxUsageDaily    = 62
	maxDevices       = 64
)

// ServiceUsage reads the current (previous=false) or the last closed billing
// cycle. A missing closed cycle is a 404 APIError with code
// usage_period_not_found.
func (c *Client) ServiceUsage(ctx context.Context, token string, sid int64, previous bool) (ServiceUsage, error) {
	path := fmt.Sprintf("/api/v1/user/services/%d/usage", sid)
	if previous {
		path += "?period=previous"
	}
	var out ServiceUsage
	if _, err := c.request(ctx, token, "GET", path, "", "", nil, &out); err != nil {
		return ServiceUsage{}, err
	}
	if len(out.Forwards) > maxUsageForwards {
		out.Forwards = out.Forwards[:maxUsageForwards]
	}
	if len(out.Daily) > maxUsageDaily {
		out.Daily = out.Daily[len(out.Daily)-maxUsageDaily:]
	}
	if out.Forwards == nil {
		out.Forwards = []ForwardUsage{}
	}
	return out, nil
}

// Devices reads the account's device limit and devices (online first).
func (c *Client) Devices(ctx context.Context, token string) (DeviceList, error) {
	var out DeviceList
	if _, err := c.request(ctx, token, "GET", "/api/v1/user/devices", "", "", nil, &out); err != nil {
		return DeviceList{}, err
	}
	if len(out.Devices) > maxDevices {
		out.Devices = out.Devices[:maxDevices]
	}
	if out.Devices == nil {
		out.Devices = []Device{}
	}
	return out, nil
}

// Services reads the account's services (name, plan, expires_at, usage).
func (c *Client) Services(ctx context.Context, token string) ([]ServiceSummary, error) {
	var out struct {
		Services []ServiceSummary `json:"services"`
	}
	if _, err := c.request(ctx, token, "GET", "/api/v1/user/services", "", "", nil, &out); err != nil {
		return nil, err
	}
	if len(out.Services) > 128 {
		out.Services = out.Services[:128]
	}
	return out.Services, nil
}

// ParseTime reads a vendor time string ("2026-11-04 12:08:03", UTC; RFC 3339
// accepted too). Zero time when empty or unknown.
func ParseTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}
