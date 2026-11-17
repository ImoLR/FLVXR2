//go:build leikwanlab

// Command leikwan-fake (plan 266 lab only) serves the local LeiKwan v1 test
// double with the eight forwards of 2026-11-16 (quota 8 / 8). Loopback only.
// POST /_lab/limit?n=30 changes forward_limit (to show the full and the
// not-full state); GET /_lab/counts prints requests / writes.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"go-backend/internal/leikwan/testapi"
)

func main() {
	addr := os.Getenv("LEIKWAN_FAKE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18466"
	}
	f := testapi.New()
	if t := os.Getenv("LEIKWAN_FAKE_TOKEN"); t != "" {
		f.Token = t
	}
	f.SeedReal8()
	f.PortBase = 10015
	// Plan 273: raw usage / previous / devices / services reports (e.g. the
	// real 11-17 reads, scrubbed) and a read failure status for stale screens.
	for env, dst := range map[string]*[]byte{"LEIKWAN_FAKE_USAGE": &f.UsageReport, "LEIKWAN_FAKE_PREV": &f.PrevReport,
		"LEIKWAN_FAKE_DEVICES": &f.DevicesReport, "LEIKWAN_FAKE_SERVICES": &f.ServicesReport} {
		if p := os.Getenv(env); p != "" {
			raw, err := os.ReadFile(p)
			if err != nil {
				log.Fatal(err)
			}
			*dst = raw
		}
	}
	if n, err := strconv.Atoi(os.Getenv("LEIKWAN_FAKE_FAIL_READS")); err == nil {
		f.FailReads = n
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", f)
	mux.HandleFunc("/_lab/limit", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.URL.Query().Get("n"))
		if err != nil || n < 0 {
			http.Error(w, "n", 400)
			return
		}
		f.Mu.Lock()
		f.Options.ForwardLimit = n
		f.Mu.Unlock()
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/_lab/reads-fail", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("status"))
		f.Mu.Lock()
		f.FailReads = n
		f.Mu.Unlock()
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/_lab/counts", func(w http.ResponseWriter, r *http.Request) {
		req, writes := f.Counts()
		f.Mu.Lock()
		ids := []int64{}
		for id := range f.Forwards {
			ids = append(ids, id)
		}
		f.Mu.Unlock()
		f.Mu.Lock()
		reads := fmt.Sprintf("usage=%d previous=%d devices=%d services=%d", f.UsageReads, f.PrevReads, f.DeviceReads, f.ServiceReads)
		f.Mu.Unlock()
		fmt.Fprintf(w, "requests=%d writes=%d forwards=%v %s\n", req, writes, ids, reads)
	})
	log.Fatal(http.ListenAndServe(addr, mux))
}
