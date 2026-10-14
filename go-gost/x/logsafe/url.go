// Package logsafe formats diagnostic text without URL credentials.
package logsafe

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var embeddedURL = regexp.MustCompile(`(?:https?|wss?)://[^\s"'<>]+`)

// URL must never fall back to printing an unparseable input: url.Parse errors
// themselves contain the original URL, including its query credentials.
func URL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[redacted URL]"
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		u.RawQuery = "redacted"
	} else {
		for key := range q {
			switch strings.ToLower(key) {
			case "secret", "token", "jwt_secret", "access_token", "api_token", "password", "config":
				q.Set(key, "***")
			}
		}
		u.RawQuery = q.Encode()
	}
	if u.User != nil {
		u.User = url.User("***")
	}
	u.Fragment, u.RawFragment = "", ""
	return u.String()
}

// Text also removes known secrets if a peer echoes a credential outside a URL.
func Text(s string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			for _, value := range []string{url.QueryEscape(secret), url.PathEscape(secret), secret} {
				s = strings.ReplaceAll(s, value, "***")
			}
		}
	}
	return embeddedURL.ReplaceAllStringFunc(s, URL)
}

func Error(err error) string {
	if err == nil {
		return ""
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Sprintf("%s %s: %s", ue.Op, URL(ue.URL), Error(ue.Err))
	}
	return Text(err.Error())
}
