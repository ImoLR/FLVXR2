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
	NextID              int64
	creates             map[string]savedCreate
}

func New() *Fake {
	f := &Fake{Token: "test-token", ServiceID: 39, Forwards: map[int64]leikwan.Forward{}, NextID: 300, creates: map[string]savedCreate{}}
	f.Options.ForwardLimit = 8
	f.Options.Transports = []string{"tcp", "udp", "tcp+udp"}
	f.Options.Capabilities.AllowManage = true
	f.Options.Capabilities.AllowDelete = true
	for _, r := range []leikwan.RouteRef{{21, "CAN → HKG 广港"}, {24, "SHA → TYO 沪日"}, {29, "CAN → TPE 广台"}, {23, "FOC → TPE 福台"}, {25, "PEK → FRA 京德"}} {
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
	if r.URL.Path == "/api/v1/user/services" && r.Method == "GET" {
		reply(w, 200, map[string]interface{}{"ok": true, "services": []map[string]interface{}{{"id": f.ServiceID, "name": "Fake LeiKwan"}}})
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
			w.Header().Set("ETag", tag(v))
			reply(w, 200, map[string]interface{}{"ok": true, "forward": v})
			return
		}
	}
	fail(w, 404, "not_found")
}
