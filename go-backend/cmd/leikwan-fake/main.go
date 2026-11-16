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
	mux.HandleFunc("/_lab/counts", func(w http.ResponseWriter, r *http.Request) {
		req, writes := f.Counts()
		f.Mu.Lock()
		ids := []int64{}
		for id := range f.Forwards {
			ids = append(ids, id)
		}
		f.Mu.Unlock()
		fmt.Fprintf(w, "requests=%d writes=%d forwards=%v\n", req, writes, ids)
	})
	log.Fatal(http.ListenAndServe(addr, mux))
}
