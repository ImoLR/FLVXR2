package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestLegacyPanelUpgradeUsesSystemUpgradeFlow(t *testing.T) {
	// An empty deploy dir makes the system upgrade capability check fail before
	// any download or helper start, so the legacy endpoint must report that
	// instead of accepting a background main-branch script run.
	t.Setenv(panelDeployDirEnv, t.TempDir())

	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/panel/upgrade", strings.NewReader(`{"version":"3.0.27-fork.6","channel":"stable"}`))
	res := httptest.NewRecorder()
	h.panelUpgrade(res, req)

	var body struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code == 0 || !strings.Contains(body.Msg, "当前环境不支持面板自升级") {
		t.Fatalf("legacy panel upgrade response = %+v, want system upgrade capability rejection", body)
	}
}

func TestPanelImageCleanupPatternTargetsPanelImages(t *testing.T) {
	pattern := regexp.MustCompile(panelImageCleanupPattern)
	for _, image := range []string{
		"ghcr.io/imolr/flvxr2-svc-backend:3.0.27-fork.5",
		"ghcr.io/imolr/flvxr2-svc-frontend:3.0.27-fork.5",
		"ghcr.io/ikeilo/flvxr2-svc-backend:3.0.27",
	} {
		if !pattern.MatchString(image) {
			t.Errorf("cleanup pattern should match %q", image)
		}
	}
	for _, image := range []string{
		"ghcr.io/imolr/other-service:latest",
		"docker.io/library/nginx:latest",
	} {
		if pattern.MatchString(image) {
			t.Errorf("cleanup pattern should not match %q", image)
		}
	}

	if !strings.Contains(newSystemUpgradeExecutor().helperScript(), "grep -E '"+panelImageCleanupPattern+"'") {
		t.Fatal("upgrade helper script does not use the panel image cleanup pattern")
	}
	script, err := os.ReadFile("../../../../panel_install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "grep -oE '"+panelImageCleanupPattern) {
		t.Fatal("panel_install.sh does not use the panel image cleanup pattern")
	}
}
