package handler

import "testing"

func TestReleaseChannelFromTag(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		expects string
	}{
		{name: "stable semantic version", tag: "2.1.4", expects: releaseChannelStable},
		{name: "stable fork maintenance release", tag: "3.0.27-fork.10", expects: releaseChannelStable},
		{name: "v prefix should be dev", tag: "v2.1.4", expects: releaseChannelDev},
		{name: "rc release", tag: "2.1.4-rc2", expects: releaseChannelDev},
		{name: "beta release", tag: "2.1.4-beta.1", expects: releaseChannelDev},
		{name: "alpha release", tag: "2.1.4-alpha", expects: releaseChannelDev},
		{name: "non numeric tag", tag: "nightly", expects: releaseChannelDev},
		{name: "empty tag", tag: "", expects: releaseChannelDev},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseChannelFromTag(tc.tag); got != tc.expects {
				t.Fatalf("releaseChannelFromTag(%q) = %q, want %q", tc.tag, got, tc.expects)
			}
		})
	}
}

func TestForkVersionComparison(t *testing.T) {
	tests := []struct {
		name        string
		left, right string
		expects     int
	}{
		{name: "baseline precedes first fork", left: "3.0.27", right: "3.0.27-fork.1", expects: -1},
		{name: "fork revisions are numeric", left: "3.0.27-fork.2", right: "3.0.27-fork.10", expects: -1},
		{name: "newer fork revision", left: "3.0.27-fork.10", right: "3.0.27-fork.2", expects: 1},
		{name: "official release from another baseline is separate", left: "3.0.28", right: "3.0.27-fork.10", expects: 0},
		{name: "fork is separate from another official baseline", left: "3.0.27-fork.10", right: "3.0.28", expects: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := compareVersions(tc.left, tc.right)
			if got < 0 {
				got = -1
			} else if got > 0 {
				got = 1
			}
			if got != tc.expects {
				t.Fatalf("compareVersions(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.expects)
			}
		})
	}
}

func TestSystemUpdateDecisionKeepsForkLineSeparate(t *testing.T) {
	if got := systemUpgradeVersionResponse("3.0.27-fork.2", releaseChannelStable, "3.0.27-fork.10", nil, systemUpgradeCapabilityData{}); !got.HasUpdate {
		t.Fatal("fork.2 should offer fork.10 as an update")
	}
	if got := systemUpgradeVersionResponse("3.0.28", releaseChannelStable, "3.0.27-fork.10", nil, systemUpgradeCapabilityData{}); got.HasUpdate {
		t.Fatal("an official release from another baseline must not auto-update to the fork line")
	}
}

func TestForkUpdateSource(t *testing.T) {
	if githubRepo != "ImoLR/FLVXR2" {
		t.Fatalf("githubRepo = %q, want ImoLR/FLVXR2", githubRepo)
	}
}

func TestNormalizeReleaseChannel(t *testing.T) {
	tests := []struct {
		input   string
		expects string
	}{
		{input: "", expects: ""},
		{input: "stable", expects: releaseChannelStable},
		{input: "dev", expects: releaseChannelDev},
		{input: "DEV", expects: releaseChannelDev},
		{input: "preview", expects: releaseChannelStable},
	}

	for _, tc := range tests {
		if got := normalizeReleaseChannel(tc.input); got != tc.expects {
			t.Fatalf("normalizeReleaseChannel(%q) = %q, want %q", tc.input, got, tc.expects)
		}
	}
}
