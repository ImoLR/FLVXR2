package landing

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCoreUpdateState (plan 129): a binary written after the running Core
// started means the Core still runs the previous executable.
func TestCoreUpdateState(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	started := time.Now().Add(-time.Hour)
	if got := m.coreUpdateState(coreSnapshot{StartedAt: started}); got != "" {
		t.Fatalf("missing binary: %q", got)
	}
	if err := os.WriteFile(m.binary(), []byte("core"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mtime, started time.Time
		want           string
	}{
		{started.Add(-time.Minute), started, ""},                     // binary older than the Core: current
		{started.Add(500 * time.Millisecond), started, ""},           // within the 1 s margin
		{started.Add(10 * time.Second), started, "restart_required"}, // replaced under the running Core
		{started.Add(10 * time.Second), time.Time{}, ""},             // no Core
	} {
		if err := os.Chtimes(m.binary(), tc.mtime, tc.mtime); err != nil {
			t.Fatal(err)
		}
		if got := m.coreUpdateState(coreSnapshot{StartedAt: tc.started}); got != tc.want {
			t.Fatalf("mtime %v started %v: %q want %q", tc.mtime, tc.started, got, tc.want)
		}
	}
}

// TestLandingRealCoreUpgradeRestart (plan 129) with real Cores: an agent with
// the new pin downloads the new binary under a running old Core and reports
// core_update without restarting; only upgrade_core_epoch naming that Core
// restarts it, exactly once, onto the new binary; inbounds come back.
// FLVX_LANDING_OLD_CORE (the previous pin's asset, v1.4.74 = b01adef) makes the
// old Core really old; without it the pinned binary plays both roles. Plan 148:
// the restarted Core must advertise the guard-hop feature that only the new pin
// (2bf03c9) has, so the test proves the new binary really runs.
func TestLandingRealCoreUpgradeRestart(t *testing.T) {
	dir := t.TempDir()
	newCore := realCore(t, dir)
	newBytes, err := os.ReadFile(newCore)
	if err != nil {
		t.Fatal(err)
	}
	oldBytes := newBytes
	if p := os.Getenv("FLVX_LANDING_OLD_CORE"); p != "" {
		if oldBytes, err = os.ReadFile(p); err != nil {
			t.Fatal(err)
		}
	}
	oldSum, _ := fileHash(writeTemp(t, oldBytes))
	newSum := CoreSHA256["amd64"]
	saved := coreExpected
	defer func() { coreExpected = saved }()
	coreExpected = func() (string, bool) { return oldSum, true }
	m := NewManager(dir, true)
	m.resourceCheck = func(string) error { return nil }
	downloads := 0
	download := func(_ context.Context, _, path, expected string, _ int64, mode os.FileMode) error {
		downloads++
		if expected != newSum {
			t.Errorf("download of %s, want the pinned %s", expected, newSum)
		}
		tmp := path + ".download"
		if err := os.WriteFile(tmp, newBytes, mode); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	m.coreDownload = download
	if err := os.WriteFile(m.binary(), oldBytes, 0755); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	n := testNode(t, 1)
	n.Port = freePort(t)
	d := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, Nodes: []Node{n}}
	status, err := m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	oldEpoch := status.Epoch
	if status.CoreUpdate != "" || downloads != 0 {
		t.Fatalf("old pin: update %q downloads %d", status.CoreUpdate, downloads)
	}
	oldFeatures, oldList := coreFeatures(ctx, m)

	// OTA: a new agent process (the Core keeps running) pins the new Core.
	time.Sleep(1200 * time.Millisecond)
	coreExpected = func() (string, bool) { return newSum, true }
	next := NewManager(dir, true)
	next.resourceCheck, next.coreDownload, next.proc = m.resourceCheck, download, m.proc
	m.proc = nil
	m = next
	d.Revision++
	status, err = m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if downloads != 1 || status.Epoch != oldEpoch || status.CoreUpdate != "restart_required" {
		t.Fatalf("after OTA: downloads %d epoch %s→%s update %q", downloads, oldEpoch, status.Epoch, status.CoreUpdate)
	}
	// A confirmation for another Core epoch never restarts.
	d.Revision++
	d.UpgradeCoreEpoch = "1"
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != oldEpoch || status.CoreUpdate != "restart_required" {
		t.Fatalf("foreign epoch restarted: %v %+v", err, status)
	}
	// Confirmed for this Core: one restart onto the new binary.
	d.Revision++
	d.UpgradeCoreEpoch = oldEpoch
	status, err = m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if status.Epoch == oldEpoch || status.CoreUpdate != "" || status.State != "running" {
		t.Fatalf("confirmed upgrade: epoch %s→%s update %q state %s", oldEpoch, status.Epoch, status.CoreUpdate, status.State)
	}
	if listed := string(mustAPI(t, m, "lsi")); !strings.Contains(listed, `"`+n.InboundTag+`"`) {
		t.Fatalf("inbound missing after the upgrade restart: %s", listed)
	}
	newFeatures, newList := coreFeatures(ctx, m)
	if newFeatures != 200 || !strings.Contains(newList, guardHopFeature) {
		t.Fatalf("restarted Core lacks %s on /v1/features (%d %s): not the new binary", guardHopFeature, newFeatures, newList)
	}
	if os.Getenv("FLVX_LANDING_OLD_CORE") != "" && strings.Contains(oldList, guardHopFeature) {
		t.Fatalf("old Core already advertises %s: not the previous pin", guardHopFeature)
	}
	upgraded := status.Epoch
	// The persisted confirmation names the old epoch: later applies never restart.
	for i := 0; i < 2; i++ {
		d.Revision++
		if status, err = m.Apply(ctx, d); err != nil || status.Epoch != upgraded {
			t.Fatalf("restarted again: %v epoch %s→%s", err, upgraded, status.Epoch)
		}
	}
	t.Logf("old Core %s (/v1/features %d %s) → one confirmed restart → %s (/v1/features %d %s)", oldEpoch, oldFeatures, oldList, upgraded, newFeatures, newList)
}

func writeTemp(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "core")
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// guardHopFeature: Core 2bf03c9+ (plan 148 pin) keeps the Reality guard's
// loopback hop out of the connection counts and says so on /v1/features.
const guardHopFeature = "flvx.reality_guard_hop_uncounted"

// coreFeatures returns the HTTP status and body of the Core's /v1/features
// control endpoint (404 on Cores before b01adef).
func coreFeatures(ctx context.Context, m *Manager) (int, string) {
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", m.socket())
	}}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://core/v1/features", nil)
	resp, err := client.Do(req)
	if err != nil {
		return 0, ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, string(body)
}

// TestCoreUpgradeDownloadFailureKeepsPrevious (plan 129): after an OTA the
// previous verified Core keeps the reconcile going when the new pin cannot be
// downloaded (mirrors down, no disk/memory headroom); anything else on disk
// still fails closed.
func TestCoreUpgradeDownloadFailureKeepsPrevious(t *testing.T) {
	oldBytes, newBytes := []byte("previous core"), []byte("pinned core")
	savedE, savedP := coreExpected, corePrevious
	defer func() { coreExpected, corePrevious = savedE, savedP }()
	sum := func(b []byte) string { h, _ := fileHash(writeTemp(t, b)); return h }
	coreExpected = func() (string, bool) { return sum(newBytes), true }
	corePrevious = func() (string, bool) { return sum(oldBytes), true }
	m := NewManager(t.TempDir(), true)
	downloads := 0
	m.coreDownload = func(context.Context, string, string, string, int64, os.FileMode) error {
		downloads++
		return os.ErrDeadlineExceeded
	}
	resources := error(nil)
	m.resourceCheck = func(string) error { return resources }
	if err := os.WriteFile(m.binary(), oldBytes, 0755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := m.install(ctx); err != nil || downloads != 1 {
		t.Fatalf("previous pin must keep the reconcile going: %v (downloads %d)", err, downloads)
	}
	if err := m.install(ctx); err != nil || downloads != 1 {
		t.Fatalf("retry must wait for its backoff: %v (downloads %d)", err, downloads)
	}
	if b, _ := os.ReadFile(m.binary()); string(b) != string(oldBytes) {
		t.Fatal("previous binary touched")
	}
	m.downloads = nil
	resources = os.ErrPermission
	if err := m.install(ctx); err != nil || downloads != 1 {
		t.Fatalf("low headroom must not block the previous Core: %v (downloads %d)", err, downloads)
	}
	// An unknown binary is never accepted.
	resources = nil
	m.downloads = nil
	if err := os.WriteFile(m.binary(), []byte("tampered"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := m.install(ctx); err == nil {
		t.Fatal("unknown binary accepted")
	}
}

// TestVerifiedFileKeyedByHash: one file checked against two pins never lets
// a cached match for one hash answer for the other.
func TestVerifiedFileKeyedByHash(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	p := filepath.Join(m.dir, "f")
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	good, _ := fileHash(p)
	for i := 0; i < 2; i++ {
		if !m.verifiedFile(p, good, -1) || m.verifiedFile(p, "deadbeef", -1) {
			t.Fatal("cache answered for the wrong hash")
		}
	}
}
