package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPanelIPFamilyConfig(t *testing.T) {
	for _, tc := range []struct {
		field, want string
		invalid     bool
	}{
		{"", "auto", false}, {`,"panel_ip_family":""`, "auto", false},
		{`,"panel_ip_family":"auto"`, "auto", false},
		{`,"panel_ip_family":"prefer_ipv4"`, "prefer_ipv4", false},
		{`,"panel_ip_family":"ipv4_only"`, "ipv4_only", false},
		{`,"panel_ip_family":"ipv6_only"`, "ipv6_only", false},
		{`,"panel_ip_family":"bad"`, "", true},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"addr":"panel.test","secret":"test"`+tc.field+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadConfig(path)
		if (err != nil) != tc.invalid {
			t.Fatalf("%s: %v", tc.field, err)
		}
		if !tc.invalid && cfg.PanelIPFamily != tc.want {
			t.Fatalf("got %q", cfg.PanelIPFamily)
		}
	}
}
