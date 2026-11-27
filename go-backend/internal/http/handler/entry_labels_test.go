package handler

import "testing"

// Plan 321: the entry host of a share link (the 节点链接 card tag).
func TestLinkURLHost(t *testing.T) {
	for link, want := range map[string]string{
		"vless://uuid@atsks7i.imgamer.top:24189?security=reality#a":    "atsks7i.imgamer.top",
		"trojan://pw@[240e:b8f::1]:443?sni=x#b":                        "240e:b8f::1",
		"ss://YWVzLTEyOC1nY206cHc@paid-v4.bgp1.leikwanhost.com:8388#c": "paid-v4.bgp1.leikwanhost.com",
		"anytls://pw@fh82sge.imgamer.top:30001/?sni=x#d":               "fh82sge.imgamer.top",
		"vmess://eyJhZGQiOiIxLjIuMy40In0=":                             "",
		"":                                                             "",
		"%zz":                                                          "",
	} {
		if got := linkURLHost(link); got != want {
			t.Errorf("linkURLHost(%q) = %q, want %q", link, got, want)
		}
	}
}
