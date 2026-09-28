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
		{name: "fork revisions are numeric", left: "3.0.27-fork.2", right: "3.0.27-fork.10", expects: -1},
		{name: "newer fork revision", left: "3.0.27-fork.10", right: "3.0.27-fork.2", expects: 1},
		{name: "fork follows matching baseline", left: "3.0.27", right: "3.0.27-fork.1", expects: -1},
		{name: "unrelated official version is incomparable", left: "3.0.28", right: "3.0.27-fork.10", expects: 0},
		{name: "unrelated fork version is incomparable in reverse", left: "3.0.27-fork.10", right: "3.0.28", expects: 0},
		{name: "release candidate precedes official", left: "3.0.27-rc2", right: "3.0.27", expects: -1},
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
		t.Fatal("unrelated official version must not auto-update to the fork line")
	}
}

func TestForkUpdateSourceURLs(t *testing.T) {
	if githubRepo != "ImoLR/FLVXR2" {
		t.Fatalf("githubRepo = %q, want ImoLR/FLVXR2", githubRepo)
	}
	tests := map[string]string{
		"agent template":          releaseAssetURL("3.0.27-fork.2", "gost-{ARCH}"),
		"agent amd64":             releaseAssetURL("3.0.27-fork.2", "gost-amd64"),
		"agent arm64":             releaseAssetURL("3.0.27-fork.2", "gost-arm64"),
		"agent checksum template": releaseAssetURL("3.0.27-fork.2", "gost-{ARCH}.sha256"),
		"agent amd64 checksum":    releaseAssetURL("3.0.27-fork.2", "gost-amd64.sha256"),
		"agent arm64 checksum":    releaseAssetURL("3.0.27-fork.2", "gost-arm64.sha256"),
		"node installer":          releaseAssetURL("3.0.27-fork.2", "install.sh"),
		"offline amd64":           latestReleaseAssetURL("offline-amd64.zip"),
		"offline arm64":           latestReleaseAssetURL("offline-arm64.zip"),
	}
	wants := map[string]string{
		"agent template":          "https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.2/gost-{ARCH}",
		"agent amd64":             "https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.2/gost-amd64",
		"agent arm64":             "https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.2/gost-arm64",
		"agent checksum template": "https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.2/gost-{ARCH}.sha256",
		"agent amd64 checksum":    "https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.2/gost-amd64.sha256",
		"agent arm64 checksum":    "https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.2/gost-arm64.sha256",
		"node installer":          "https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.2/install.sh",
		"offline amd64":           "https://github.com/ImoLR/FLVXR2/releases/latest/download/offline-amd64.zip",
		"offline arm64":           "https://github.com/ImoLR/FLVXR2/releases/latest/download/offline-arm64.zip",
	}
	for name, got := range tests {
		if got != wants[name] {
			t.Errorf("%s = %q, want %q", name, got, wants[name])
		}
	}

	command := agentUpgradeCommandData("3.0.27-fork.2")
	downloadURLs, ok := command["downloadUrls"].([]string)
	if !ok || len(downloadURLs) != 1 || downloadURLs[0] != wants["agent template"] {
		t.Fatalf("UpgradeAgent downloadUrls = %#v", command["downloadUrls"])
	}
	if got := strings.ReplaceAll(downloadURLs[0], "{ARCH}", "amd64"); got != wants["agent amd64"] {
		t.Fatalf("UpgradeAgent amd64 URL = %q", got)
	}
	if got := strings.ReplaceAll(downloadURLs[0], "{ARCH}", "arm64"); got != wants["agent arm64"] {
		t.Fatalf("UpgradeAgent arm64 URL = %q", got)
	}
	checksumURLs, ok := command["checksumUrls"].([]string)
	if !ok || len(checksumURLs) != 1 || checksumURLs[0] != wants["agent checksum template"] {
		t.Fatalf("UpgradeAgent checksumUrls = %#v", command["checksumUrls"])
	}
	if got := strings.ReplaceAll(checksumURLs[0], "{ARCH}", "amd64"); got != wants["agent amd64 checksum"] {
		t.Fatalf("UpgradeAgent amd64 checksum URL = %q", got)
	}
	if got := strings.ReplaceAll(checksumURLs[0], "{ARCH}", "arm64"); got != wants["agent arm64 checksum"] {
		t.Fatalf("UpgradeAgent arm64 checksum URL = %q", got)
	}
	if got := command["version"]; got != "3.0.27-fork.2" {
		t.Fatalf("UpgradeAgent version = %#v", got)
	}
}

func TestNormalizeReleaseChannel(t *testing.T) {
	tests := []struct {
		input   string
		expects string
	}{
		{input: "", expects: releaseChannelStable},
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
