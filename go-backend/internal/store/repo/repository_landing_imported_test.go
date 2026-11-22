package repo

import (
	"strings"
	"testing"

	"go-backend/internal/linechain"
	"go-backend/internal/store/model"
	"go-backend/internal/sublink"
)

// Plan 291: nodes moved over from mmwx with their credentials — a single-key
// SS2022 server and a Reality node without flow — give the links their
// clients already use; panel-made nodes keep today's links byte for byte.
func TestLandingURLImportedCredentials(t *testing.T) {
	const server, user = "c2VydmVyLWtleS0xNmJ5dA==", "dXNlci1rZXktMTZieXRlcw=="
	two := model.LandingNode{Protocol: "ss2022", Name: "a", Method: "2022-blake3-aes-128-gcm", ServerKey: server, UserKey: user}
	if got, want := LandingURL(two, "h.example.com", 10016), "ss://2022-blake3-aes-128-gcm:c2VydmVyLWtleS0xNmJ5dA%3D%3D%3AdXNlci1rZXktMTZieXRlcw%3D%3D@h.example.com:10016#a"; got != want {
		t.Fatalf("two-key link changed:\n got %s\nwant %s", got, want)
	}
	one := two
	one.UserKey = ""
	link := LandingURL(one, "h.example.com", 12311)
	if want := "ss://2022-blake3-aes-128-gcm:c2VydmVyLWtleS0xNmJ5dA%3D%3D@h.example.com:12311#a"; link != want {
		t.Fatalf("single-key link:\n got %s\nwant %s", link, want)
	}
	if LandingSS2022Password(one) != server || LandingSS2022Password(two) != server+":"+user {
		t.Fatal("SS2022 password helper wrong")
	}
	up, _, err := linechain.ParseLink(link)
	if err != nil || up.Password != server {
		t.Fatalf("single-key link does not parse back: %v %+v", err, up)
	}

	vision := model.LandingNode{Protocol: "vless_reality_vision", Name: "v", UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Flow: "xtls-rprx-vision", PublicKey: "d5ItLiva6HbMpnS4ygBBpMH-yv7zeVMSwcitOGNE8AM", ServerNames: `["www.lovelive-anime.jp"]`}
	if got, want := LandingURL(vision, "h.example.com", 10017), "vless://d77c8b43-40ed-42cd-9150-f3f47d0f534d@h.example.com:10017?encryption=none&flow=xtls-rprx-vision&fp=chrome&pbk=d5ItLiva6HbMpnS4ygBBpMH-yv7zeVMSwcitOGNE8AM&security=reality&sid=&sni=www.lovelive-anime.jp&type=tcp#v"; got != want {
		t.Fatalf("vision link changed:\n got %s\nwant %s", got, want)
	}
	plain := vision
	plain.Flow = ""
	link = LandingURL(plain, "h.example.com", 10017)
	if strings.Contains(link, "flow=") {
		t.Fatalf("no-flow node got a flow: %s", link)
	}
	up, _, err = linechain.ParseLink(link)
	if err != nil || up.Flow != "" {
		t.Fatalf("no-flow link does not parse back: %v %+v", err, up)
	}
	clash, _ := sublink.Clash([]sublink.Node{{Name: "v", Up: up, URI: link}}, sublink.ModeGlobal, false)
	if strings.Contains(string(clash), "xtls-rprx-vision") {
		t.Fatal("Clash output of a no-flow node carries a flow")
	}
}
