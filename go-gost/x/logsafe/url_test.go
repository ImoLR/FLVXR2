package logsafe

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestURLAndWrappedErrors(t *testing.T) {
	for _, raw := range []string{
		"https://panel/flow?secret=private-psk&version=47",
		"wss://panel/ws?secret=private-psk&secret=second-secret",
		"https://user:password-secret@panel/?JWT_SECRET=jwt-secret&api_token=leikwan-secret",
		"https://panel/%zz?secret=private-psk",
		"https://panel/?%73ecret=private-psk&token=leikwan%2Dsecret",
		"https://panel/?secret=private-psk&bad=%zz",
	} {
		err := fmt.Errorf("outer: %w", &url.Error{Op: "Get", URL: raw, Err: errors.New("connection refused")})
		for _, got := range []string{URL(raw), Error(err)} {
			for _, secret := range []string{"private-psk", "second-secret", "password-secret", "jwt-secret", "leikwan-secret", "leikwan%2Dsecret"} {
				if strings.Contains(got, secret) {
					t.Errorf("secret leaked: %s", got)
				}
			}
		}
	}
}
