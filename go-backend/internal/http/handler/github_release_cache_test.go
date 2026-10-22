package handler

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubReleaseListIsCachedAndShared(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`[{"tag_name":"3.0.27-fork.74","draft":false,"prerelease":false}]`))
	}))
	defer srv.Close()
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	githubReleaseCache.mu.Lock()
	githubReleaseCache.releases, githubReleaseCache.at, githubReleaseCache.err = nil, time.Time{}, nil
	githubReleaseCache.mu.Unlock()
	t.Cleanup(func() {
		githubAPIBase = oldBase
		githubReleaseCache.mu.Lock()
		githubReleaseCache.releases, githubReleaseCache.at, githubReleaseCache.err = nil, time.Time{}, nil
		githubReleaseCache.mu.Unlock()
	})

	// Many page loads (sidebar check, release lists) -> one GitHub request.
	for i := 0; i < 20; i++ {
		if _, err := fetchGitHubReleases(50); err != nil {
			t.Fatal(err)
		}
		if tag, err := resolveLatestReleaseByChannel(releaseChannelStable); err != nil || tag != "3.0.27-fork.74" {
			t.Fatalf("latest %q %v", tag, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("GitHub called %d times, want 1", n)
	}
	// An actual upgrade asks GitHub again.
	if _, err := resolveLatestReleaseByChannelFresh(releaseChannelStable); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("fresh fetch did not reach GitHub (%d calls)", n)
	}
}
