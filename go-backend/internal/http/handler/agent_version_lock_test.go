package handler

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestPanelAgentInstallCommandFallsBackThroughMirrors(t *testing.T) {
	const asset = "https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.5/install.sh"
	command := buildNodeInstallCommand(testPanelVersion, "https://panel.example.test", "test-secret")

	want := "{ " +
		"curl -fsSL --connect-timeout 5 -m 30 " + asset + " -o ./install.sh || " +
		"curl -fsSL --connect-timeout 5 -m 30 https://ghfast.top/" + asset + " -o ./install.sh || " +
		"curl -fsSL --connect-timeout 5 -m 30 https://gh-proxy.com/" + asset + " -o ./install.sh || " +
		"curl -fsSL --connect-timeout 5 -m 30 https://gcode.hostcentral.cc/" + asset + " -o ./install.sh; } " +
		"&& chmod +x ./install.sh && VERSION=3.0.27-fork.5 ./install.sh -a https://panel.example.test -s test-secret"
	if command != want {
		t.Fatalf("install command\n got %q\nwant %q", command, want)
	}
	// The frontend appends " -n <service>": the command must end with the install.sh arguments.
	if !strings.HasSuffix(command, "./install.sh -a https://panel.example.test -s test-secret") {
		t.Fatalf("install command %q must end with the install.sh arguments", command)
	}
}

func TestPanelAgentInstallCommandShellPrecedence(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake curl: fails for direct GitHub and the first mirror, succeeds (writing a
	// tiny install.sh that records its args) for the rest. Every call is logged.
	fakeCurl := `#!/bin/sh
echo "$@" >> "$CURL_LOG"
url=""; out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  https://github.com/*|https://ghfast.top/*) exit 7 ;;
esac
printf '#!/bin/sh\necho "RAN VERSION=$VERSION ARGS=$*" >> "$CURL_LOG"\n' > "$out"
`
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "curl.log")
	command := buildNodeInstallCommand(testPanelVersion, "panel.example.test:6365", "s3cret") + " -n flvx_agent"

	cmd := exec.Command(sh, "-c", command)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "CURL_LOG="+logPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command failed: %v\n%s", err, out)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 3 curl calls + 1 install.sh run, got %d lines:\n%s", len(lines), logData)
	}
	if !strings.Contains(lines[0], " https://github.com/") || !strings.Contains(lines[1], "https://ghfast.top/") ||
		!strings.Contains(lines[2], "https://gh-proxy.com/") {
		t.Fatalf("unexpected fetch order:\n%s", logData)
	}
	if lines[3] != "RAN VERSION=3.0.27-fork.5 ARGS=-a panel.example.test:6365 -s s3cret -n flvx_agent" {
		t.Fatalf("install.sh ran with %q", lines[3])
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
	if !ok {
		t.Fatalf("downloadUrls = %#v, want []string", data["downloadUrls"])
	}
	checksumURLs, ok := data["checksumUrls"].([]string)
	if !ok {
		t.Fatalf("checksumUrls = %#v, want []string", data["checksumUrls"])
	}
	const direct = "https://github.com/ImoLR/FLVXR2/releases/download/3.0.27-fork.5/gost-{ARCH}"
	wantDownloads := []string{
		direct,
		"https://ghfast.top/" + direct,
		"https://gh-proxy.com/" + direct,
		"https://gcode.hostcentral.cc/" + direct,
	}
	if len(downloadURLs) != len(wantDownloads) || len(checksumURLs) != len(wantDownloads) {
		t.Fatalf("downloadUrls = %#v checksumUrls = %#v, want %d paired URLs", downloadURLs, checksumURLs, len(wantDownloads))
	}
	for i, want := range wantDownloads {
		if downloadURLs[i] != want {
			t.Fatalf("downloadUrls[%d] = %q, want %q", i, downloadURLs[i], want)
		}
		// The agent pairs checksumUrls[i] with downloadUrls[i].
		if checksumURLs[i] != want+".sha256" {
			t.Fatalf("checksumUrls[%d] = %q, want %q", i, checksumURLs[i], want+".sha256")
		}
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

func TestInstallerMirrorChainMatchesPanel(t *testing.T) {
	script, err := os.ReadFile("../../../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)

	want := `GH_MIRRORS="` + strings.Join(githubMirrorPrefixes, " ") + `"`
	if !strings.Contains(content, want) {
		t.Fatalf("install.sh GH_MIRRORS must match githubMirrorPrefixes: want line %s", want)
	}
	// Binary installs/updates go through the mirror helper with the release checksum.
	if got := strings.Count(content, `gh_download "$DOWNLOAD_URL" "$INSTALL_DIR/${SERVICE_NAME}`); got != 2 {
		t.Fatalf("expected install and update to download the agent via gh_download, found %d", got)
	}
	if !strings.Contains(content, `bin "${DOWNLOAD_URL}.sha256"`) {
		t.Fatal("install.sh does not verify the agent binary against the release sha256")
	}
	if !strings.Contains(content, `gh_download "$SCRIPT_DOWNLOAD_URL"`) {
		t.Fatal("install.sh self-update does not use the mirror helper")
	}
	// The unauthenticated traffic reset always got 401 and printed a fake success.
	if strings.Contains(content, "batch-reset-traffic") {
		t.Fatal("install.sh must not call the admin-only batch-reset-traffic endpoint")
	}
	if !strings.Contains(content, `PINNED_VERSION=""`) {
		t.Fatal(`install.sh must keep the PINNED_VERSION="" line for the release pipeline`)
	}
}
