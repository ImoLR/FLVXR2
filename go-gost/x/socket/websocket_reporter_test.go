package socket

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestBuildWebSocketCandidatesSecureFirst(t *testing.T) {
	candidates := buildWebSocketCandidates("panel.example.com:443", "abc", "2.0.2", 1, 0, 1, 0, "")

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if !strings.HasPrefix(candidates[0], "wss://") {
		t.Fatalf("expected first candidate to start with wss://, got %s", candidates[0])
	}
	if !strings.HasPrefix(candidates[1], "ws://") {
		t.Fatalf("expected second candidate to start with ws://, got %s", candidates[1])
	}
}

func TestBuildWebSocketCandidatesUsesPreferredScheme(t *testing.T) {
	candidates := buildWebSocketCandidates("panel.example.com:443", "abc", "2.0.2", 1, 0, 1, 0, "ws")

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if !strings.HasPrefix(candidates[0], "ws://") {
		t.Fatalf("expected preferred ws:// candidate first, got %s", candidates[0])
	}
	if !strings.HasPrefix(candidates[1], "wss://") {
		t.Fatalf("expected fallback wss:// candidate second, got %s", candidates[1])
	}
}

func TestBuildWebSocketCandidatesNormalizesSchemePrefixedAddr(t *testing.T) {
	candidates := buildWebSocketCandidates("https://panel.example.com:443/path?q=1", "abc", "2.0.2", 0, 0, 0, 0, "")

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if !strings.HasPrefix(candidates[0], "wss://panel.example.com:443/") {
		t.Fatalf("expected normalized wss candidate, got %s", candidates[0])
	}
	if !strings.HasPrefix(candidates[1], "ws://panel.example.com:443/") {
		t.Fatalf("expected normalized ws fallback candidate, got %s", candidates[1])
	}
}

func TestDialWebSocketWithFallbackTriesWSAfterWSSFailure(t *testing.T) {
	orig := wsDial
	defer func() { wsDial = orig }()

	var attempts []string
	wsDial = func(_ *websocket.Dialer, rawURL string) (*websocket.Conn, *http.Response, error) {
		attempts = append(attempts, rawURL)
		if strings.HasPrefix(rawURL, "wss://") {
			return nil, nil, errors.New("tls failed")
		}
		return &websocket.Conn{}, nil, nil
	}

	_, usedURL, err := dialWebSocketWithFallback(
		&websocket.Dialer{},
		[]string{
			"wss://panel.example.com/system-info?type=1&secret=abc",
			"ws://panel.example.com/system-info?type=1&secret=abc",
		},
	)
	if err != nil {
		t.Fatalf("expected fallback success, got err=%v", err)
	}
	if !strings.HasPrefix(usedURL, "ws://") {
		t.Fatalf("expected fallback ws:// url, got %s", usedURL)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if !strings.HasPrefix(attempts[0], "wss://") || !strings.HasPrefix(attempts[1], "ws://") {
		t.Fatalf("unexpected attempt order: %#v", attempts)
	}
}

func TestDetectWebSocketScheme(t *testing.T) {
	if detectWebSocketScheme("wss://panel.example.com/system-info") != "wss" {
		t.Fatalf("expected wss detection")
	}
	if detectWebSocketScheme("ws://panel.example.com/system-info") != "ws" {
		t.Fatalf("expected ws detection")
	}
	if detectWebSocketScheme("http://panel.example.com/system-info") != "" {
		t.Fatalf("expected empty detection for non-websocket scheme")
	}
}

func TestSanitizeWebSocketURL(t *testing.T) {
	raw := "wss://panel.example.com/system-info?type=1&secret=abc&version=2.0.2"
	sanitized := sanitizeWebSocketURL(raw)

	if strings.Contains(sanitized, "secret=abc") {
		t.Fatalf("expected secret to be masked, got %s", sanitized)
	}
	if !strings.Contains(sanitized, "secret=%2A%2A%2A") {
		t.Fatalf("expected masked secret in url, got %s", sanitized)
	}
}

func TestFormatWebSocketDialErrorIncludesHTTPStatus(t *testing.T) {
	err := errors.New("websocket: bad handshake")
	resp := &http.Response{
		Status: "403 Forbidden",
		Body:   io.NopCloser(strings.NewReader("forbidden")),
	}

	msg := formatWebSocketDialError(err, resp)
	if !strings.Contains(msg, "HTTP 403 Forbidden") {
		t.Fatalf("expected status in message, got %s", msg)
	}
	if !strings.Contains(msg, "forbidden") {
		t.Fatalf("expected response body in message, got %s", msg)
	}
}

func TestUpdateProtocolSettingsPreservesConfigAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := []byte(`{
  "addr": "panel.example.com:443",
  "secret": "keep-secret",
  "node_id": 47,
  "service_name": "flvxx",
  "domestic_download_host": "mirror.example.com",
  "unknown": {"nested": true},
  "http": 0,
  "tls": 1,
  "socks": 0,
  "block_other": 1
}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}

	if err := updateProtocolSettings(path, 1, 0, 1, 0); err != nil {
		t.Fatalf("update protocol settings: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("expected mode 0600, got %04o", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal updated config: %v", err)
	}
	wantJSON := map[string]string{
		"addr":                   `"panel.example.com:443"`,
		"secret":                 `"keep-secret"`,
		"node_id":                `47`,
		"service_name":           `"flvxx"`,
		"domestic_download_host": `"mirror.example.com"`,
		"http":                   `1`,
		"tls":                    `0`,
		"socks":                  `1`,
		"block_other":            `0`,
	}
	for key, want := range wantJSON {
		if gotValue := string(got[key]); gotValue != want {
			t.Errorf("%s: expected %s, got %s", key, want, gotValue)
		}
	}
	var unknown struct {
		Nested bool `json:"nested"`
	}
	if err := json.Unmarshal(got["unknown"], &unknown); err != nil || !unknown.Nested {
		t.Errorf("unknown field was not preserved: value=%s err=%v", got["unknown"], err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("expected only atomically replaced config.json, got %#v", entries)
	}
}
