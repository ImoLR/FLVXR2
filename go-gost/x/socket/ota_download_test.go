package socket

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpgradeDownloadFallback(t *testing.T) {
	for _, mode := range []string{"stall", "trickle", "headers", "http-error", "empty"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/good" {
					fmt.Fprint(w, "verified binary")
					return
				}
				if mode == "http-error" {
					w.WriteHeader(503)
					return
				}
				if mode == "empty" {
					return
				}
				if mode != "headers" {
					fmt.Fprint(w, "partial body that must be discarded")
					w.(http.Flusher).Flush()
				}
				tick := time.NewTicker(10 * time.Millisecond)
				defer tick.Stop()
				for {
					select {
					case <-r.Context().Done():
						return
					case <-tick.C:
						if mode == "trickle" {
							fmt.Fprint(w, ".")
							w.(http.Flusher).Flush()
						}
					}
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "agent.new")
			var messages []string
			start := time.Now()
			source, n, digest, err := downloadUpgradeSources([]string{server.URL + "/bad", server.URL + "/good"}, path,
				80*time.Millisecond, 240*time.Millisecond, func(_ string, _ int, msg string) { messages = append(messages, msg) })
			if err != nil || source != 1 || n != int64(len("verified binary")) {
				t.Fatalf("fallback: %d %d %v", source, n, err)
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second || (mode == "trickle" && elapsed < 200*time.Millisecond) {
				t.Fatalf("wrong deadline: %v", elapsed)
			}
			body, _ := os.ReadFile(path)
			wantHash := sha256.Sum256([]byte("verified binary"))
			if string(body) != "verified binary" || string(digest) != string(wantHash[:]) {
				t.Fatalf("partial source contaminated file or checksum: %q %x", body, digest)
			}
			if !strings.Contains(strings.Join(messages, "\n"), "源 2/2") {
				t.Fatalf("missing source progress: %v", messages)
			}
		})
	}
}

func TestUpgradeDownloadAllSourcesFail(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "agent.new")
	_, _, _, err := downloadUpgradeSources([]string{s.URL, s.URL}, path, time.Second, time.Second, func(string, int, string) {})
	if err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed temporary binary remains: %v", err)
	}
}
