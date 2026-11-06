package ipkey

import "testing"

func TestOf(t *testing.T) {
	cases := map[string]string{
		"192.0.2.1":                               "192.0.2.1",
		"::ffff:192.0.2.1":                        "192.0.2.1",
		"[2409:8a4c:5c4f:26e1::1]":                "2409:8a4c:5c4f:26e1::/64",
		"2409:8a4c:5c4f:26e1:1111:2222:3333:4444": "2409:8a4c:5c4f:26e1::/64",
		"2409:8a4c:5c4f:26e1::/64":                "2409:8a4c:5c4f:26e1::/64",
		"2409:8a4c:5c4f:26e1::9/128":              "2409:8a4c:5c4f:26e1::/64",
		"fe80::1%eth0":                            "fe80::/64",
		"192.0.2.1/32":                            "192.0.2.1",
		"junk":                                    "junk",
		" ":                                       "",
	}
	for in, want := range cases {
		if got := Of(in); got != want || Of(got) != got {
			t.Errorf("Of(%q) = %q, want %q (idempotent)", in, got, want)
		}
	}
	got := Set(map[string]struct{}{"2001:db8::1": {}, "2001:db8::2": {}, "2001:db8:0:1::1": {}, "192.0.2.1": {}, "": {}})
	if len(got) != 3 {
		t.Fatalf("Set = %v", got)
	}
}
