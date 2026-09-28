package handler

import (
	"os"
	"strings"
	"testing"
)

const testPanelVersion = "3.0.27-fork.5"

func TestPanelAgentInstallCommandUsesExactPanelVersion(t *testing.T) {
	h := &Handler{fluxVersion: testPanelVersion}
	version, err := h.currentPanelAgentVersion("")
	if err != nil {
		t.Fatal(err)
	}

	command := buildNodeInstallCommand(version, "https://panel.example.test", "test-secret")
	wants := []string{
		"https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.5/install.sh",
		"VERSION=3.0.27-fork.5",
	}
	for _, want := range wants {
		if !strings.Contains(command, want) {
			t.Fatalf("install command %q does not contain %q", command, want)
		}
	}

	forbidden := []string{"raw.githubusercontent.com", "/releases/latest/", "iKeilo/FLVXR2"}
	for _, value := range forbidden {
		if strings.Contains(command, value) {
			t.Fatalf("install command %q contains forbidden source %q", command, value)
		}
	}
}

func TestPanelAgentVersionRejectsDifferentRequestedVersion(t *testing.T) {
	h := &Handler{fluxVersion: testPanelVersion}
	if _, err := h.currentPanelAgentVersion("3.0.28"); err == nil {
		t.Fatal("expected a requested version different from the Panel version to fail")
	}
}

func TestPanelAgentUpgradeUsesExactPanelVersion(t *testing.T) {
	data := agentUpgradeCommandData(testPanelVersion)
	downloadURLs, ok := data["downloadUrls"].([]string)
	if !ok || len(downloadURLs) != 1 {
		t.Fatalf("downloadUrls = %#v, want exactly one URL", data["downloadUrls"])
	}
	want := "https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.5/gost-{ARCH}"
	if downloadURLs[0] != want {
		t.Fatalf("download URL = %q, want %q", downloadURLs[0], want)
	}
	if data["version"] != testPanelVersion {
		t.Fatalf("upgrade version = %#v, want %q", data["version"], testPanelVersion)
	}
}

func TestExplicitVersionInstallerHasNoAgentFallback(t *testing.T) {
	script, err := os.ReadFile("../../../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)

	forbidden := []string{
		"releases/latest/download/gost-",
		"raw.githubusercontent.com/${REPO}/main/go-gost/flux_agent",
	}
	for _, value := range forbidden {
		if strings.Contains(content, value) {
			t.Fatalf("install.sh still contains Agent fallback %q", value)
		}
	}

	want := `DOWNLOAD_HOST="https://github.com/${REPO}/releases/download/${RESOLVED_VERSION}"`
	if !strings.Contains(content, want) {
		t.Fatalf("install.sh does not lock its self-update URL to the resolved version")
	}
	if !strings.Contains(content, "update_service\n        exit $?") {
		t.Fatal("install.sh does not propagate an update download failure")
	}
}
