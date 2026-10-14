package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHTTPFallbackLogsAndErrorsRedactSecret(t *testing.T) {
	old := reportDo
	defer func() { reportDo = old }()
	secret := "test-node-psk"
	for _, mode := range []string{"transport", "response", "status", "success", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			reportDo = func(_ context.Context, r *http.Request, _ time.Duration) (*http.Response, error) {
				calls++
				if mode == "transport" || mode == "success" && calls == 1 {
					return nil, &url.Error{Op: "Post", URL: r.URL.String(), Err: errors.New("refused")}
				}
				body := `{"secret":"` + secret + `","config":"private-config"}`
				code := 200
				if mode == "success" {
					body = "ok"
				}
				if mode == "status" {
					code = 500
				}
				return &http.Response{StatusCode: code, Status: "500 " + secret, Body: io.NopCloser(strings.NewReader(body))}, nil
			}
			urls := []string{"https://panel/flow?secret=" + secret, "http://panel/flow?secret=" + secret}
			if mode == "malformed" {
				urls = []string{"http://panel/%zz?secret=" + secret, "http://panel/%yy?secret=" + secret}
			}
			r, w, _ := os.Pipe()
			oldout := os.Stdout
			os.Stdout = w
			preferred := ""
			ok, err := postJSONWithFallback(context.Background(), urls, []byte("{}"), "test", time.Second, &preferred)
			w.Close()
			os.Stdout = oldout
			raw, _ := io.ReadAll(r)
			r.Close()
			got := string(raw)
			if err != nil {
				got += err.Error()
			}
			if strings.Contains(got, secret) || strings.Contains(got, "private-config") {
				t.Fatalf("leaked: %s", got)
			}
			if mode == "success" && (!ok || !strings.Contains(preferred, secret)) {
				t.Fatal("request/preference semantics changed")
			}
		})
	}
}
