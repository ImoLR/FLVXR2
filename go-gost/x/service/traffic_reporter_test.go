package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBuildReportURLCandidatesSecureFirst(t *testing.T) {
	upload, config := buildReportURLCandidates("panel.example.com:443", "abc")

	if len(upload) != 2 {
		t.Fatalf("expected 2 upload candidates, got %d", len(upload))
	}
	if len(config) != 2 {
		t.Fatalf("expected 2 config candidates, got %d", len(config))
	}

	if upload[0] != "https://panel.example.com:443/flow/upload?secret=abc" {
		t.Fatalf("unexpected upload[0]: %s", upload[0])
	}
	if upload[1] != "http://panel.example.com:443/flow/upload?secret=abc" {
		t.Fatalf("unexpected upload[1]: %s", upload[1])
	}
	if config[0] != "https://panel.example.com:443/flow/config?secret=abc" {
		t.Fatalf("unexpected config[0]: %s", config[0])
	}
	if config[1] != "http://panel.example.com:443/flow/config?secret=abc" {
		t.Fatalf("unexpected config[1]: %s", config[1])
	}
}

func TestBuildReportURLCandidatesNormalizeSchemeAddr(t *testing.T) {
	upload, config := buildReportURLCandidates("https://panel.example.com:8443/path", "abc")

	if upload[0] != "https://panel.example.com:8443/flow/upload?secret=abc" {
		t.Fatalf("unexpected upload[0]: %s", upload[0])
	}
	if upload[1] != "http://panel.example.com:8443/flow/upload?secret=abc" {
		t.Fatalf("unexpected upload[1]: %s", upload[1])
	}
	if config[0] != "https://panel.example.com:8443/flow/config?secret=abc" {
		t.Fatalf("unexpected config[0]: %s", config[0])
	}
	if config[1] != "http://panel.example.com:8443/flow/config?secret=abc" {
		t.Fatalf("unexpected config[1]: %s", config[1])
	}
}

func TestPostJSONWithFallbackUsesHTTPAfterHTTPSFailure(t *testing.T) {
	orig := reportDo
	defer func() { reportDo = orig }()

	var calls []string
	reportDo = func(_ context.Context, req *http.Request, _ time.Duration) (*http.Response, error) {
		calls = append(calls, req.URL.String())
		if strings.HasPrefix(req.URL.String(), "https://") {
			return nil, errors.New("tls handshake failed")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	}

	ok, err := postJSONWithFallback(
		context.Background(),
		[]string{
			"https://panel.example.com:443/flow/upload?secret=abc",
			"http://panel.example.com:443/flow/upload?secret=abc",
		},
		[]byte(`[]`),
		"GOST-Traffic-Reporter/1.0",
		5*time.Second,
		nil,
	)
	if !ok || err != nil {
		t.Fatalf("expected fallback success, ok=%v err=%v", ok, err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if !strings.HasPrefix(calls[0], "https://") || !strings.HasPrefix(calls[1], "http://") {
		t.Fatalf("unexpected call order: %#v", calls)
	}
}

func TestPostJSONWithFallbackRemembersDetectedURL(t *testing.T) {
	orig := reportDo
	defer func() { reportDo = orig }()

	targets := []string{
		"https://panel.example.com:443/flow/upload?secret=abc",
		"http://panel.example.com:443/flow/upload?secret=abc",
	}

	var preferred string
	var calls []string
	reportDo = func(_ context.Context, req *http.Request, _ time.Duration) (*http.Response, error) {
		calls = append(calls, req.URL.String())
		if strings.HasPrefix(req.URL.String(), "https://") {
			return nil, errors.New("tls handshake failed")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
		}, nil
	}

	ok, err := postJSONWithFallback(
		context.Background(),
		targets,
		[]byte(`[]`),
		"GOST-Traffic-Reporter/1.0",
		5*time.Second,
		&preferred,
	)
	if !ok || err != nil {
		t.Fatalf("expected first call success, ok=%v err=%v", ok, err)
	}
	if preferred != targets[1] {
		t.Fatalf("expected preferred url to be remembered as %s, got %s", targets[1], preferred)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls on first attempt, got %d", len(calls))
	}

	calls = nil
	ok, err = postJSONWithFallback(
		context.Background(),
		targets,
		[]byte(`[]`),
		"GOST-Traffic-Reporter/1.0",
		5*time.Second,
		&preferred,
	)
	if !ok || err != nil {
		t.Fatalf("expected second call success, ok=%v err=%v", ok, err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected second call to use remembered url once, got %d calls", len(calls))
	}
	if !strings.HasPrefix(calls[0], "http://") {
		t.Fatalf("expected remembered http url first, got %s", calls[0])
	}
}

func TestPeriodicConfigReportsAreQuietAndFailuresRecoverOnce(t *testing.T) {
	oldDo, oldURL, oldPreferred := reportDo, configReportURL, preferredConfigURL
	defer func() { reportDo, configReportURL, preferredConfigURL = oldDo, oldURL, oldPreferred }()
	configReportURL = "https://quiet-panel/config?secret=private-psk"
	fail := false
	reportDo = func(_ context.Context, _ *http.Request, _ time.Duration) (*http.Response, error) {
		if fail {
			return nil, errors.New("unavailable")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldout; w.Close(); r.Close() }()
	for i := 0; i < 20; i++ {
		reportConfigResult(context.Background())
	}
	fail = true
	for i := 0; i < 20; i++ {
		reportConfigResult(context.Background())
	}
	fail = false
	for i := 0; i < 20; i++ {
		reportConfigResult(context.Background())
	}
	w.Close()
	os.Stdout = oldout
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if strings.Count(text, "\n") != 2 || !strings.Contains(text, "首次失败") || !strings.Contains(text, "已恢复") || strings.Contains(text, "private-psk") {
		t.Fatal(text)
	}
}

func TestPostJSONWithFallbackHTTPPreferenceExpires(t *testing.T) {
	orig, origNow, origTTL := reportDo, reportNow, reportPreferenceTTL
	defer func() { reportDo, reportNow, reportPreferenceTTL = orig, origNow, origTTL }()
	now := time.Unix(1_800_000_000, 0)
	reportNow = func() time.Time { return now }
	reportPreferenceTTL = 5 * time.Minute

	targets := []string{"https://panel.example.com/flow/upload?secret=abc", "http://panel.example.com/flow/upload?secret=abc"}
	httpsUp := false
	var calls []string
	reportDo = func(_ context.Context, req *http.Request, _ time.Duration) (*http.Response, error) {
		calls = append(calls, req.URL.Scheme)
		if req.URL.Scheme == "https" && !httpsUp {
			return nil, errors.New("tls handshake timeout")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	}
	var preferred string
	post := func() {
		t.Helper()
		calls = nil
		if ok, err := postJSONWithFallback(context.Background(), targets, []byte(`[]`), "t", time.Second, &preferred); !ok || err != nil {
			t.Fatalf("post: %v %v", ok, err)
		}
	}
	post() // https fails → http
	if strings.Join(calls, ",") != "https,http" {
		t.Fatalf("first: %v", calls)
	}
	now = now.Add(4 * time.Minute)
	post() // within the TTL: http directly, and using it does not extend the TTL
	if strings.Join(calls, ",") != "http" {
		t.Fatalf("within TTL: %v", calls)
	}
	now = now.Add(2 * time.Minute) // 6 min after the fallback was chosen
	httpsUp = true
	post()
	if strings.Join(calls, ",") != "https" {
		t.Fatalf("after the TTL https must be tried first again: %v", calls)
	}
	post()
	if strings.Join(calls, ",") != "https" || preferred != targets[0] {
		t.Fatalf("back on https: %v %s", calls, preferred)
	}
}

func TestPostJSONWithFallbackPlainPortKeepsHTTP(t *testing.T) {
	orig, origNow, origDo := reportDo, reportNow, reportPreferenceTTL
	defer func() { reportDo, reportNow, reportPreferenceTTL = orig, origNow, origDo }()
	now := time.Unix(1_800_000_000, 0)
	reportNow = func() time.Time { return now }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer srv.Close()
	hostPort := strings.TrimPrefix(srv.URL, "http://")
	targets := []string{"https://" + hostPort + "/flow/upload", "http://" + hostPort + "/flow/upload"}
	var calls []string
	reportDo = func(ctx context.Context, req *http.Request, timeout time.Duration) (*http.Response, error) {
		calls = append(calls, req.URL.Scheme)
		return (&http.Client{Timeout: timeout}).Do(req.WithContext(ctx))
	}
	var preferred string
	if ok, err := postJSONWithFallback(context.Background(), targets, []byte(`[]`), "t", 3*time.Second, &preferred); !ok || err != nil {
		t.Fatalf("post: %v %v (%v)", ok, err, calls)
	}
	now = now.Add(time.Hour)
	calls = nil
	if ok, err := postJSONWithFallback(context.Background(), targets, []byte(`[]`), "t", 3*time.Second, &preferred); !ok || err != nil {
		t.Fatalf("post: %v %v", ok, err)
	}
	if strings.Join(calls, ",") != "http" {
		t.Fatalf("a port without TLS keeps http first like before: %v", calls)
	}
}
