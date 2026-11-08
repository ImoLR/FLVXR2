package handler

import (
	"strings"
	"testing"
	"time"
)

func TestRegistrableZone(t *testing.T) {
	for in, want := range map[string]string{
		"flvxxht.imgamer.top":         "imgamer.top",
		"https://flvxx.imgamer.top/x": "imgamer.top",
		"flvxxht.imgamer.top:443":     "imgamer.top",
		"IMGAMER.TOP.":                "imgamer.top",
		"a.b.example.co.uk":           "example.co.uk",
		"panel.example.com.cn":        "example.com.cn",
		"203.0.113.5":                 "",
		"[2001:db8::1]:443":           "",
		"localhost":                   "",
		"bad host.com":                "",
		"":                            "",
	} {
		if got := registrableZone(in); got != want {
			t.Errorf("registrableZone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDNSCacheGuard(t *testing.T) {
	var g dnsCacheGuard
	t0 := time.Unix(1000, 0)
	if err := g.begin(7, t0); err != nil {
		t.Fatal(err)
	}
	if err := g.begin(7, t0.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "正在配置") {
		t.Fatalf("busy: %v", err)
	}
	if err := g.begin(8, t0); err != nil {
		t.Fatalf("other node must not wait: %v", err)
	}
	g.end(7)
	if err := g.begin(7, t0.Add(30*time.Second)); err == nil || !strings.Contains(err.Error(), "秒后再试") {
		t.Fatalf("cooldown: %v", err)
	}
	if err := g.begin(7, t0.Add(dnsCacheCooldown+time.Second)); err != nil {
		t.Fatalf("after cooldown: %v", err)
	}
}
