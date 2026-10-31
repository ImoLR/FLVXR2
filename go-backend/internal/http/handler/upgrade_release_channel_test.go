package handler

import (
	"strings"
	"testing"
)

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
		{name: "three-digit fork is newer than fork.99", left: "3.0.27-fork.99", right: "3.0.27-fork.100", expects: -1},
		{name: "fork.100 is not older than fork.99", left: "3.0.27-fork.100", right: "3.0.27-fork.99", expects: 1},
		{name: "fork.100 equals itself", left: "3.0.27-fork.100", right: "3.0.27-fork.100", expects: 0},
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
	if got := systemUpgradeVersionResponse("3.0.27-fork.99", releaseChannelStable, "3.0.27-fork.100", nil, systemUpgradeCapabilityData{}); !got.HasUpdate {
		t.Fatal("fork.99 should offer fork.100 as an update")
	}
	if got := systemUpgradeVersionResponse("3.0.27-fork.100", releaseChannelStable, "3.0.27-fork.99", nil, systemUpgradeCapabilityData{}); got.HasUpdate {
		t.Fatal("fork.100 must not offer fork.99 as an update")
	}
	if got := systemUpgradeVersionResponse("3.0.27-fork.100", releaseChannelStable, "3.0.27-fork.100", nil, systemUpgradeCapabilityData{}); got.HasUpdate {
		t.Fatal("fork.100 must not offer itself as an update")
	}
	if releaseChannelFromTag("3.0.27-fork.100") != releaseChannelStable {
		t.Fatal("fork.100 must be on the stable channel")
	}
	if quotaAgentSupport("3.0.27-fork.100") != quotaAgentSupported {
		t.Fatal("fork.100 agents support quota groups")
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

// GitHub's release list is not in version order (seen 2026-10-31 right after
// 3.0.27-fork.100: fork.99, fork.100, fork.98, …, fork.16, fork.9, fork.8, fork.15).
func githubOrderAfterFork100() []githubRelease {
	tags := []string{"3.0.27-fork.99", "3.0.27-fork.100", "3.0.27-fork.98", "3.0.27-fork.16", "3.0.27-fork.9",
		"3.0.27-fork.8", "3.0.27-fork.15", "3.0.27-fork.10", "3.0.27-fork.101-rc1"}
	out := make([]githubRelease, 0, len(tags))
	for _, tag := range tags {
		out = append(out, githubRelease{TagName: tag})
	}
	return out
}

func TestResolveLatestReleaseUsesVersionOrderNotGitHubOrder(t *testing.T) {
	fetch := func(int) ([]githubRelease, error) { return githubOrderAfterFork100(), nil }
	if got, err := resolveLatestReleaseFrom(releaseChannelStable, fetch); err != nil || got != "3.0.27-fork.100" {
		t.Fatalf("stable latest = %q, %v; want 3.0.27-fork.100", got, err)
	}
	if got, err := resolveLatestReleaseFrom("", fetch); err != nil || got != "3.0.27-fork.101-rc1" {
		t.Fatalf("any-channel latest = %q, %v; want 3.0.27-fork.101-rc1", got, err)
	}
	if got, err := resolveLatestReleaseFrom(releaseChannelDev, fetch); err != nil || got != "3.0.27-fork.101-rc1" {
		t.Fatalf("dev latest = %q, %v; want 3.0.27-fork.101-rc1", got, err)
	}
}

func TestCheckUpdatesReleaseListIsVersionOrdered(t *testing.T) {
	items := releasesForChannel(sortReleasesNewestFirst(githubOrderAfterFork100()), releaseChannelStable)
	var got []string
	for _, item := range items {
		got = append(got, item.Version)
	}
	want := []string{"3.0.27-fork.100", "3.0.27-fork.99", "3.0.27-fork.98", "3.0.27-fork.16", "3.0.27-fork.15",
		"3.0.27-fork.10", "3.0.27-fork.9", "3.0.27-fork.8"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stable releases = %v, want %v", got, want)
	}
	if r := systemUpgradeVersionResponse("3.0.27-fork.99", releaseChannelStable, items[0].Version, nil, systemUpgradeCapabilityData{}); !r.HasUpdate {
		t.Fatal("fork.99 panel must see fork.100 as an update")
	}
	if r := systemUpgradeVersionResponse("3.0.27-fork.100", releaseChannelStable, items[0].Version, nil, systemUpgradeCapabilityData{}); r.HasUpdate {
		t.Fatal("fork.100 panel must not see an update")
	}
}

func TestReleaseTagOrderIsNumeric(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"3.0.27-fork.100", "3.0.27-fork.99", 1},
		{"3.0.27-fork.9", "3.0.27-fork.10", -1},
		{"3.0.27", "3.0.27-fork.1", -1},
		{"3.0.28", "3.0.27-fork.100", 1},
		{"3.0.27-fork.100", "3.0.27-fork.100", 0},
	}
	for _, c := range cases {
		if got := releaseTagOrder(c.left, c.right); got != c.want {
			t.Fatalf("releaseTagOrder(%q, %q) = %d, want %d", c.left, c.right, got, c.want)
		}
	}
}
