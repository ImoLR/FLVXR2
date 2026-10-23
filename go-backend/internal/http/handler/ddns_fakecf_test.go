package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go-backend/internal/ddns"
)

// fakeCF is an in-memory Cloudflare v4 API (zones + dns_records). DELETE is
// a test failure: FLVX never deletes records.
type fakeCF struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	zones   []ddns.Zone
	records map[string]*ddns.Record // id -> record
	nextID  int
	calls   map[string]int // "GET /zones/…/dns_records" style, ids stripped
	total   int
	fail    int // answer the next N calls with HTTP 500
	auth    bool
	token   string
}

func newFakeCF(t *testing.T) *fakeCF {
	f := &fakeCF{t: t, records: map[string]*ddns.Record{}, calls: map[string]int{}, token: "test-token",
		zones: []ddns.Zone{{ID: "zone1", Name: "imgamer.top", Status: "active", Permissions: []string{"#dns_records:edit", "#zone:read"}}}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCF) client(token string) *ddns.Client {
	c := ddns.NewCloudflare(token)
	c.BaseURL = f.srv.URL
	return c
}

func (f *fakeCF) add(rec ddns.Record) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	rec.ID = fmt.Sprintf("rec%d", f.nextID)
	f.records[rec.ID] = &rec
	return rec.ID
}

func (f *fakeCF) find(name, typ string) []ddns.Record {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ddns.Record
	for _, r := range f.records {
		if r.Name == name && (typ == "" || r.Type == typ) {
			out = append(out, *r)
		}
	}
	return out
}

func (f *fakeCF) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeCF) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total
}

func (f *fakeCF) reply(w http.ResponseWriter, status int, result interface{}, errs ...string) {
	body := map[string]interface{}{"success": status < 300, "errors": []interface{}{}, "result": result}
	if len(errs) > 0 {
		body["errors"] = []map[string]interface{}{{"code": 1000 + status, "message": errs[0]}}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeCF) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	path := r.URL.Path
	key := r.Method + " " + path
	if i := strings.Index(path, "/dns_records/"); i >= 0 {
		key = r.Method + " " + path[:i] + "/dns_records/:id"
	}
	f.calls[key]++
	f.total++
	if r.Header.Get("Authorization") != "Bearer "+f.token || f.auth {
		f.mu.Unlock()
		f.reply(w, 403, nil, "Invalid API Token")
		return
	}
	if f.fail > 0 {
		f.fail--
		f.mu.Unlock()
		f.reply(w, 500, nil, "internal error")
		return
	}
	f.mu.Unlock()
	if r.Method == http.MethodDelete {
		f.t.Errorf("DELETE %s called: FLVX must never delete DNS records", path)
		f.reply(w, 405, nil, "no")
		return
	}
	switch {
	case path == "/user/tokens/verify":
		f.reply(w, 200, map[string]interface{}{"id": "x", "status": "active"})
	case path == "/zones":
		f.reply(w, 200, f.zones)
	case strings.HasPrefix(path, "/zones/zone1/dns_records"):
		id := strings.TrimPrefix(strings.TrimPrefix(path, "/zones/zone1/dns_records"), "/")
		switch r.Method {
		case http.MethodGet:
			if id != "" {
				f.mu.Lock()
				rec := f.records[id]
				f.mu.Unlock()
				if rec == nil {
					f.reply(w, 404, nil, "Record does not exist.")
					return
				}
				f.reply(w, 200, rec)
				return
			}
			f.reply(w, 200, f.find(r.URL.Query().Get("name"), r.URL.Query().Get("type")))
		case http.MethodPost, http.MethodPut:
			var in ddns.Record
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				f.reply(w, 400, nil, "bad json")
				return
			}
			if in.Proxied || in.TTL != ddns.RecordTTL {
				f.t.Errorf("%s wrote proxied=%v ttl=%d", r.Method, in.Proxied, in.TTL)
			}
			if r.Method == http.MethodPost {
				f.reply(w, 200, map[string]interface{}{"id": f.add(in)})
				return
			}
			f.mu.Lock()
			rec := f.records[id]
			if rec != nil {
				in.ID = id
				*rec = in
			}
			f.mu.Unlock()
			if rec == nil {
				f.reply(w, 404, nil, "Record does not exist.")
				return
			}
			f.reply(w, 200, in)
		default:
			f.reply(w, 405, nil, "method")
		}
	default:
		f.reply(w, 404, nil, "not found")
	}
}
