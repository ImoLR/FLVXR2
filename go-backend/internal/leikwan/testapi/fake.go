// Package testapi supplies a stateful, local-only LeiKwan v1 test double. It is
// used by contract tests and the isolated browser/data-path verification harness.
package testapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-backend/internal/leikwan"
)

type savedCreate struct {
	Request leikwan.CreateRequest
	ID      int64
}
type Fake struct {
	Mu                  sync.Mutex
	Token               string
	ServiceID           int64
	Forwards            map[int64]leikwan.Forward
	Options             leikwan.Options
	Stored              int
	Requests            int
	Writes              int
	FailNext            int
	RetryAfter          string
	FailCreateAfterSave bool
	FailPatchAfterSave  bool
	NextID              int64
	PortBase            int // > 0: assign the lowest free listen port from here (plan 266 lab)
	creates             map[string]savedCreate
	// Plan 273 usage / devices / services reads. Raw reports (e.g. the real
	// 11-17 samples) are served as-is when set; otherwise the fake builds them
	// from Forwards + ForwardBytes. FailReads > 0 fails every usage / devices
	// / services read with that status (stale-state screens, back-off tests).
	UsageReport, PrevReport, DevicesReport, ServicesReport []byte
	ForwardBytes                                           map[int64][2]int64 // id → in, out
	NoPrevious                                             bool
	FailReads                                              int
	UsageReads, DeviceReads, ServiceReads, PrevReads       int
}

func New() *Fake {
	f := &Fake{Token: "test-token", ServiceID: 39, Forwards: map[int64]leikwan.Forward{}, NextID: 300, creates: map[string]savedCreate{}}
	f.Options.ForwardLimit = 8
	f.Options.Transports = []string{"tcp", "udp", "tcp+udp"}
	f.Options.Capabilities.AllowManage = true
	f.Options.Capabilities.AllowDelete = true
	for _, r := range []leikwan.RouteRef{
		{ID: 21, Label: "CAN → HKG 广港"}, {ID: 24, Label: "SHA → TYO 沪日"}, {ID: 29, Label: "CAN → TPE 广台"},
		{ID: 23, Label: "FOC → TPE 福台"}, {ID: 25, Label: "PEK → FRA 京德"},
	} {
		f.Options.Routes = append(f.Options.Routes, leikwan.Route{ID: r.ID, Label: r.Label, Badge: "stable", Transports: map[string]leikwan.Availability{"tcp": {Available: true}, "udp": {Available: true}, "tcp+udp": {Available: true}}})
	}
	return f
}
func (f *Fake) SeedSix() {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for i, id := range []int64{263, 264, 279, 280, 281, 282} {
		route := f.Options.Routes[[]int{0, 1, 0, 2, 2, 3}[i]]
		f.Forwards[id] = leikwan.Forward{ID: id, ServiceID: f.ServiceID, Route: leikwan.RouteRef{ID: route.ID, Label: route.Label}, ListenPort: 10015 + i, Transport: "tcp+udp", Target: leikwan.Target{Source: "manual", Host: "8.8.8.8", Port: 20015 + i}, State: "active", Revision: 1, CreatedAt: "2026-10-14 00:00:00", UpdatedAt: "2026-10-14 00:00:00"}
	}
	f.Stored = 6
}
func (f *Fake) Counts() (int, int) { f.Mu.Lock(); defer f.Mu.Unlock(); return f.Requests, f.Writes }
func fail(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": map[string]string{"code": code, "message": "fake API error"}})
}
func tag(v leikwan.Forward) string { return fmt.Sprintf(`"forward-%d-r%d"`, v.ID, v.Revision) }
func reply(w http.ResponseWriter, status int, v interface{}) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v interface{}) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	f.Requests++
	if r.Method != "GET" {
		f.Writes++
	}
	if r.Header.Get("Authorization") != "Bearer "+f.Token {
		fail(w, 401, "user_auth_failed")
		return
	}
	if f.FailNext > 0 {
		status := f.FailNext
		f.FailNext = 0
		if status == 429 {
			w.Header().Set("Retry-After", f.RetryAfter)
		}
		fail(w, status, map[int]string{429: "rate_limited", 409: "route_unavailable", 412: "forward_version_conflict", 422: "validation_failed"}[status])
		return
	}
	if f.serveUsage(w, r) {
		return
	}
	prefix := fmt.Sprintf("/api/v1/user/services/%d/", f.ServiceID)
	if r.URL.Path == prefix+"forward-options" && r.Method == "GET" {
		options := f.Options
		options.ActiveForwardCount = len(f.Forwards)
		reply(w, 200, options)
		return
	}
	if r.URL.Path == prefix+"forwards" {
		switch r.Method {
		case "GET":
			list := make([]leikwan.Forward, 0, len(f.Forwards))
			for _, v := range f.Forwards {
				list = append(list, v)
			}
			sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
			reply(w, 200, map[string]interface{}{"ok": true, "forwards": list})
			return
		case "POST":
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				fail(w, 415, "unsupported_media_type")
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				fail(w, 428, "idempotency_key_required")
				return
			}
			if !keyPattern.MatchString(key) {
				fail(w, 422, "idempotency_key_invalid")
				return
			}
			var req leikwan.CreateRequest
			if decode(r, &req) != nil {
				fail(w, 422, "unknown_fields")
				return
			}
			if saved, ok := f.creates[key]; ok {
				if !reflect.DeepEqual(saved.Request, req) {
					fail(w, 409, "idempotency_conflict")
					return
				}
				v, active := f.Forwards[saved.ID]
				if !active {
					fail(w, 409, "idempotency_resource_inactive")
					return
				}
				w.Header().Set("ETag", tag(v))
				reply(w, 200, map[string]interface{}{"ok": true, "forward": v})
				return
			}
			if len(f.Forwards) >= f.Options.ForwardLimit {
				fail(w, 409, "forward_quota_exceeded")
				return
			}
			if f.Stored >= 512 {
				fail(w, 409, "forward_storage_quota_exceeded")
				return
			}
			if req.Target.Port < 1 || req.Target.Port > 65535 || req.Target.Host == "" || req.Target.Source != "manual" || req.ListenPort != nil && (*req.ListenPort < 1 || *req.ListenPort > 65535) {
				fail(w, 422, "validation_failed")
				return
			}
			var route *leikwan.Route
			for i := range f.Options.Routes {
				if f.Options.Routes[i].ID == req.RouteID {
					route = &f.Options.Routes[i]
				}
			}
			if route == nil {
				fail(w, 422, "forward_route_not_allowed")
				return
			}
			if !route.Transports[req.Transport].Available {
				fail(w, 409, "route_unavailable")
				return
			}
			port := 11000 + int(f.NextID)
			if f.PortBase > 0 {
				// Like the platform: the lowest free listen port from PortBase.
				used := map[int]bool{}
				for _, v := range f.Forwards {
					used[v.ListenPort] = true
				}
				for port = f.PortBase; used[port]; port++ {
				}
			}
			if req.ListenPort != nil {
				port = *req.ListenPort
			}
			v := leikwan.Forward{ID: f.NextID, ServiceID: f.ServiceID, Route: leikwan.RouteRef{ID: route.ID, Label: route.Label}, ListenPort: port, Transport: req.Transport, Target: req.Target, State: "active", Revision: 1, UpdatedAt: time.Now().UTC().Format("2006-01-02 15:04:05")}
			f.NextID++
			f.Stored++
			f.Forwards[v.ID] = v
			f.creates[key] = savedCreate{req, v.ID}
			if f.FailCreateAfterSave {
				f.FailCreateAfterSave = false
				fail(w, 503, "operation_failed")
				return
			}
			w.Header().Set("ETag", tag(v))
			reply(w, 201, map[string]interface{}{"ok": true, "forward": v})
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/user/forwards/") {
		id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/user/forwards/"), 10, 64)
		if err != nil {
			fail(w, 404, "forward_not_found")
			return
		}
		v, ok := f.Forwards[id]
		if !ok {
			fail(w, 404, "forward_not_found")
			return
		}
		if r.Method == "GET" {
			w.Header().Set("ETag", tag(v))
			reply(w, 200, map[string]interface{}{"ok": true, "forward": v})
			return
		}
		if r.Header.Get("If-Match") == "" {
			fail(w, 428, "precondition_required")
			return
		}
		if r.Header.Get("If-Match") != tag(v) {
			fail(w, 412, "forward_version_conflict")
			return
		}
		switch r.Method {
		case "DELETE":
			delete(f.Forwards, id)
			w.WriteHeader(204)
			return
		case "PATCH":
			var req struct {
				Target leikwan.Target `json:"target"`
			}
			if decode(r, &req) != nil {
				fail(w, 422, "unknown_fields")
				return
			}
			if req.Target.Port < 1 || req.Target.Port > 65535 || req.Target.Source != "manual" {
				fail(w, 422, "validation_failed")
				return
			}
			v.Target = req.Target
			v.Revision++
			v.UpdatedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
			f.Forwards[id] = v
			if f.FailPatchAfterSave {
				f.FailPatchAfterSave = false
				fail(w, 503, "operation_failed")
				return
			}
			w.Header().Set("ETag", tag(v))
			reply(w, 200, map[string]interface{}{"ok": true, "forward": v})
			return
		}
	}
	fail(w, 404, "not_found")
}

// SeedReal8 (plan 266) seeds the eight forwards of service 39 as they ran on
// 2026-11-16 (route, listen port, target from the hosts' /etc/ultra files):
// quota 8 / 8, so creating one more must be refused.
func (f *Fake) SeedReal8() {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	route := map[int64]leikwan.RouteRef{}
	for _, r := range f.Options.Routes {
		route[r.ID] = leikwan.RouteRef{ID: r.ID, Label: r.Label}
	}
	for _, v := range []struct {
		id, route int64
		port      int
		host      string
		target    int
		transport string
	}{
		{263, 21, 10018, "boilhkt99u.imgamer.top", 10016, "tcp+udp"},
		{264, 24, 10015, "188.253.118.132", 60061, "tcp+udp"},
		{279, 21, 10019, "43.199.143.188", 10022, "tcp+udp"},
		{280, 29, 10020, "boilhinet2.imgamer.top", 12311, "tcp+udp"},
		{281, 29, 10016, "facseednet.imgamer.top", 10015, "tcp+udp"},
		{282, 23, 10017, "boilhinet2.imgamer.top", 12311, "tcp+udp"},
		{318, 21, 10044, "43.199.143.188", 10044, "tcp"},
		{540, 21, 10021, "boilhkt99u.imgamer.top", 10019, "tcp+udp"},
	} {
		f.Forwards[v.id] = leikwan.Forward{ID: v.id, ServiceID: f.ServiceID, Route: route[v.route], ListenPort: v.port, Transport: v.transport, Target: leikwan.Target{Source: "manual", Host: v.host, Port: v.target}, State: "active", Revision: 1, CreatedAt: "2026-11-16 00:00:00", UpdatedAt: "2026-11-16 00:00:00"}
	}
	f.Stored = 8
	if f.NextID < 600 {
		f.NextID = 600
	}
}

// ---- plan 273: usage / devices / services (read-only) ----

const fakeCycleStart, fakeCycleEnd = "2026-11-04 12:08:03", "2026-12-04 12:08:03"

func rawReply(w http.ResponseWriter, raw []byte) {
	w.WriteHeader(200)
	_, _ = w.Write(raw)
}

// serveUsage answers the plan-273 read endpoints (lock held by ServeHTTP).
func (f *Fake) serveUsage(w http.ResponseWriter, r *http.Request) bool {
	usagePath := fmt.Sprintf("/api/v1/user/services/%d/usage", f.ServiceID)
	switch {
	case r.URL.Path == "/api/v1/user/services", r.URL.Path == usagePath, r.URL.Path == "/api/v1/user/devices", r.URL.Path == "/api/v1/user/devices/online":
	default:
		return false
	}
	if r.Method != "GET" {
		fail(w, 405, "method_not_allowed")
		return true
	}
	if f.FailReads > 0 {
		if f.FailReads == 429 {
			w.Header().Set("Retry-After", f.RetryAfter)
		}
		fail(w, f.FailReads, map[int]string{401: "user_auth_failed", 429: "rate_limited"}[f.FailReads])
		return true
	}
	switch r.URL.Path {
	case "/api/v1/user/services":
		f.ServiceReads++
		if f.ServicesReport != nil {
			rawReply(w, f.ServicesReport)
			return true
		}
		u := f.buildUsage(false)
		reply(w, 200, map[string]interface{}{"ok": true, "services": []map[string]interface{}{{
			"id": f.ServiceID, "name": "Fake LeiKwan", "status": "active",
			"plan":                 map[string]interface{}{"id": 12, "name": "1000M 滬日·廣港·京德·廣台·福台 1TB"},
			"active_forward_count": len(f.Forwards), "forward_limit": f.Options.ForwardLimit,
			"expires_at": "2026-12-07 23:59:59", "usage": u["usage"],
		}}})
	case usagePath:
		if r.URL.Query().Get("period") == "previous" {
			f.PrevReads++
			if f.NoPrevious {
				fail(w, 404, "usage_period_not_found")
			} else if f.PrevReport != nil {
				rawReply(w, f.PrevReport)
			} else {
				reply(w, 200, f.buildUsage(true))
			}
			return true
		}
		f.UsageReads++
		if f.UsageReport != nil {
			rawReply(w, f.UsageReport)
			return true
		}
		reply(w, 200, f.buildUsage(false))
	default:
		f.DeviceReads++
		if f.DevicesReport != nil {
			rawReply(w, f.DevicesReport)
			return true
		}
		reply(w, 200, map[string]interface{}{
			"ok": true, "online_device_limit": 5, "online_device_count": 2, "available_device_slots": 3,
			"limit_source": "plan", "online_window_seconds": 180,
			"clients": []map[string]interface{}{{"client_id": 44, "name": "Default Client", "online_device_limit": 5, "online_device_count": 2, "available_device_slots": 3}},
			"devices": []map[string]interface{}{
				{"device_id": "linux-a1", "client_id": 44, "device_name": "Leikwan-Client-Agent/2026.11.15.1", "platform": nil, "os_version": "6.1.0-33-amd64", "client_build": "2026.11.15.1", "ip": "8.134.172.154", "online": true, "state": "online", "can_connect": true, "counts_toward_limit": true, "blocked_until": nil, "first_seen_at": "2026-11-16 10:26:32", "last_seen_at": time.Now().UTC().Format("2006-01-02 15:04:05")},
				{"device_id": "android-client-44-b2", "client_id": 44, "device_name": "Xiaomi 26067PC09G", "platform": "android-host", "os_version": "15", "client_build": "0.2.3", "ip": "203.0.113.20", "online": true, "state": "online", "can_connect": true, "counts_toward_limit": true, "blocked_until": nil, "first_seen_at": "2026-11-17 11:36:59", "last_seen_at": time.Now().UTC().Format("2006-01-02 15:04:05")},
			},
		})
	}
	return true
}

// buildUsage: the plan report from Forwards + ForwardBytes (bytes default
// to id × 1 MiB in / id × 256 KiB out), 1000 GiB included.
func (f *Fake) buildUsage(previous bool) map[string]interface{} {
	start, end := fakeCycleStart, fakeCycleEnd
	if previous {
		start, end = "2026-10-04 12:08:03", fakeCycleStart
	}
	ids := make([]int64, 0, len(f.Forwards))
	for id := range f.Forwards {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	type row = map[string]interface{}
	rows := []row{}
	var used int64
	for _, id := range ids {
		v := f.Forwards[id]
		b, ok := f.ForwardBytes[id]
		if !ok {
			b = [2]int64{id << 20, id << 18}
		}
		if previous {
			b = [2]int64{b[0] / 2, b[1] / 2}
		}
		used += b[0] + b[1]
		rows = append(rows, row{"forward_id": id, "state": v.State, "listen_port": v.ListenPort, "transport": v.Transport, "native_outbound": false,
			"route": v.Route, "target": map[string]interface{}{"source": "manual", "host": v.Target.Host, "port": v.Target.Port, "service_id": nil},
			"bytes_in": b[0], "bytes_out": b[1], "extra_billed_bytes": 0, "total_bytes": b[0] + b[1]})
	}
	for _, x := range rows {
		share := 0.0
		if used > 0 {
			share = float64(x["total_bytes"].(int64)) * 100 / float64(used)
		}
		x["share_percent"] = float64(int(share*100)) / 100
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["total_bytes"].(int64) > rows[j]["total_bytes"].(int64) })
	included := int64(1000) << 30
	remaining := included - used
	usage := map[string]interface{}{"billing_mode": "monthly_bundle", "used_bytes": used, "included_bytes": included, "remaining_bytes": remaining,
		"used_percent": float64(int(float64(used)*10000/float64(included))) / 100, "billable_bytes": 0}
	if !previous {
		usage["lifetime_used_bytes"] = used * 3
	}
	name := "current"
	if previous {
		name = "previous"
	}
	return map[string]interface{}{"ok": true, "service_id": f.ServiceID, "status": "active",
		"period": map[string]interface{}{"name": name, "cycle_started_at": start, "cycle_ends_at": end, "counted_since": start, "traffic_reset_at": nil},
		"usage":  usage, "forwards": rows, "attributed_bytes": used, "unattributed_bytes": 0,
		"daily": []map[string]interface{}{{"date": start[:10], "bytes_in": used / 2, "bytes_out": used / 2, "extra_billed_bytes": 0, "total_bytes": used}}}
}
