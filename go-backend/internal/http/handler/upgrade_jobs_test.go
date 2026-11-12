package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"go-backend/internal/security"
	"go-backend/internal/store/repo"
	"go-backend/internal/ws"
)

// Plan 248: OTA jobs follow the agent's progress and its reconnect version.

func shortUpgradeTimers(t *testing.T) {
	t.Helper()
	saved := []time.Duration{upgradeHTTPWait, upgradeIdleTimeout, upgradeRestartWait, upgradeMaxDuration, upgradePollEvery}
	upgradeHTTPWait, upgradeIdleTimeout, upgradeRestartWait = 150*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond
	upgradeMaxDuration, upgradePollEvery = 5*time.Second, 10*time.Millisecond
	t.Cleanup(func() {
		upgradeHTTPWait, upgradeIdleTimeout, upgradeRestartWait = saved[0], saved[1], saved[2]
		upgradeMaxDuration, upgradePollEvery = saved[3], saved[4]
	})
}

type fakeUpgradeAgents struct {
	mu         sync.Mutex
	online     bool
	version    string
	startedAt  time.Time
	sent       int
	replies    chan upgradeReply
	broadcasts []ws.UpgradeProgress
}

func newFakeUpgradeAgents() *fakeUpgradeAgents {
	return &fakeUpgradeAgents{online: true, version: "3.0.27-fork.128 (debian/amd64)", startedAt: time.Now().Add(-time.Hour), replies: make(chan upgradeReply, 1)}
}

func (f *fakeUpgradeAgents) NodeOnline(int64) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.online }
func (f *fakeUpgradeAgents) SendCommandContext(ctx context.Context, _ int64, cmd string, _ interface{}, _ time.Duration) (ws.CommandResult, error) {
	f.mu.Lock()
	f.sent++
	f.mu.Unlock()
	select {
	case r := <-f.replies:
		return r.res, r.err
	case <-ctx.Done():
		return ws.CommandResult{}, ctx.Err()
	}
}
func (f *fakeUpgradeAgents) NodeSessionVersion(int64) (string, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version, f.startedAt, f.online
}
func (f *fakeUpgradeAgents) BroadcastUpgradeProgress(_ int64, p ws.UpgradeProgress) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broadcasts = append(f.broadcasts, p)
}
func (f *fakeUpgradeAgents) reconnect(version string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.online, f.version, f.startedAt = true, version, time.Now()
}
func (f *fakeUpgradeAgents) lastBroadcast() ws.UpgradeProgress {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.broadcasts) == 0 {
		return ws.UpgradeProgress{}
	}
	return f.broadcasts[len(f.broadcasts)-1]
}

const upgradeTarget = "3.0.27-fork.130"

func waitJob(t *testing.T, jobs *agentUpgradeJobs, job *agentUpgradeJob) upgradeJobSnapshot {
	t.Helper()
	snap := jobs.wait(job, 5*time.Second)
	if snap.State != upgradeJobSucceeded && snap.State != upgradeJobFailed {
		t.Fatalf("job did not finish: %+v", snap)
	}
	return snap
}

// 火山广州 11-12: the download took longer than the old fixed 300 s; the
// agent kept reporting progress, answered late and came back on the target.
func TestUpgradeJobSlowDownloadSucceeds(t *testing.T) {
	shortUpgradeTimers(t)
	var jobs agentUpgradeJobs
	agents := newFakeUpgradeAgents()
	job, err := jobs.start(agents, 52, upgradeTarget, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Progress for 4× the idle timeout (≈ the old fixed timeout was hit).
	for i := 1; i <= 12; i++ {
		time.Sleep(100 * time.Millisecond)
		jobs.progress(52, ws.UpgradeProgress{Stage: "downloading", Percent: i * 8, Message: "下载中"})
		if snap, _ := jobs.snapshot(52); snap.State != upgradeJobRunning {
			t.Fatalf("job ended while the agent still reports progress: %+v", snap)
		}
	}
	agents.replies <- upgradeReply{res: ws.CommandResult{Type: "UpgradeAgentResponse", Success: true}}
	time.Sleep(50 * time.Millisecond)
	if snap, _ := jobs.snapshot(52); snap.State != upgradeJobRunning {
		t.Fatalf("job ended before the agent reconnected: %+v", snap)
	}
	agents.reconnect(upgradeTarget + " (debian/amd64)")
	snap := waitJob(t, &jobs, job)
	if snap.State != upgradeJobSucceeded || !strings.Contains(snap.Message, upgradeTarget) {
		t.Fatalf("snapshot %+v", snap)
	}
	if b := agents.lastBroadcast(); b.Stage != "done" || b.Percent != 100 {
		t.Fatalf("final broadcast %+v", b)
	}
}

// The reply is lost (the session dropped mid-download, the agent reconnected
// while still downloading, then restarted on the new version).
func TestUpgradeJobLostReplyFollowsReconnectVersion(t *testing.T) {
	shortUpgradeTimers(t)
	var jobs agentUpgradeJobs
	agents := newFakeUpgradeAgents()
	job, _ := jobs.start(agents, 7, upgradeTarget, nil)
	time.Sleep(30 * time.Millisecond)
	agents.replies <- upgradeReply{err: errors.New("节点连接已断开")}
	agents.reconnect("3.0.27-fork.128 (debian/amd64)") // mid-download reconnect, old binary
	for i := 0; i < 5; i++ {
		time.Sleep(100 * time.Millisecond)
		jobs.progress(7, ws.UpgradeProgress{Stage: "downloading", Percent: 50 + i*10})
	}
	if snap, _ := jobs.snapshot(7); snap.State != upgradeJobRunning {
		t.Fatalf("mid-download reconnect ended the job: %+v", snap)
	}
	agents.reconnect(upgradeTarget + " (debian/amd64)")
	if snap := waitJob(t, &jobs, job); snap.State != upgradeJobSucceeded {
		t.Fatalf("snapshot %+v", snap)
	}
}

func TestUpgradeJobFailures(t *testing.T) {
	shortUpgradeTimers(t)
	t.Run("agent error", func(t *testing.T) {
		var jobs agentUpgradeJobs
		agents := newFakeUpgradeAgents()
		job, _ := jobs.start(agents, 1, upgradeTarget, nil)
		agents.replies <- upgradeReply{res: ws.CommandResult{Type: "UpgradeAgentResponse", Message: "所有下载源都不可用: x"}, err: errors.New("所有下载源都不可用: x")}
		snap := waitJob(t, &jobs, job)
		if snap.State != upgradeJobFailed || !strings.Contains(snap.Message, "所有下载源都不可用") {
			t.Fatalf("snapshot %+v", snap)
		}
		if b := agents.lastBroadcast(); b.Stage != "failed" || !strings.HasPrefix(b.Message, "升级失败：") {
			t.Fatalf("final broadcast %+v", b)
		}
	})
	t.Run("no progress", func(t *testing.T) {
		var jobs agentUpgradeJobs
		agents := newFakeUpgradeAgents()
		job, _ := jobs.start(agents, 2, upgradeTarget, nil)
		snap := waitJob(t, &jobs, job)
		if snap.State != upgradeJobFailed || !strings.Contains(snap.Message, "没有升级进度") {
			t.Fatalf("snapshot %+v", snap)
		}
	})
	t.Run("restarted on the old version", func(t *testing.T) {
		var jobs agentUpgradeJobs
		agents := newFakeUpgradeAgents()
		job, _ := jobs.start(agents, 3, upgradeTarget, nil)
		agents.replies <- upgradeReply{res: ws.CommandResult{Type: "UpgradeAgentResponse", Success: true}}
		time.Sleep(30 * time.Millisecond)
		agents.reconnect("3.0.27-fork.128 (debian/amd64)")
		snap := waitJob(t, &jobs, job)
		if snap.State != upgradeJobFailed || !strings.Contains(snap.Message, "3.0.27-fork.128") {
			t.Fatalf("snapshot %+v", snap)
		}
	})
	t.Run("not back after restart", func(t *testing.T) {
		var jobs agentUpgradeJobs
		agents := newFakeUpgradeAgents()
		job, _ := jobs.start(agents, 4, upgradeTarget, nil)
		agents.replies <- upgradeReply{res: ws.CommandResult{Type: "UpgradeAgentResponse", Success: true}}
		snap := waitJob(t, &jobs, job)
		if snap.State != upgradeJobFailed || !strings.Contains(snap.Message, "没有重新连接") {
			t.Fatalf("snapshot %+v", snap)
		}
	})
	t.Run("agent reported failed, reply lost", func(t *testing.T) {
		var jobs agentUpgradeJobs
		agents := newFakeUpgradeAgents()
		job, _ := jobs.start(agents, 5, upgradeTarget, nil)
		time.Sleep(30 * time.Millisecond)
		jobs.progress(5, ws.UpgradeProgress{Stage: "failed", Percent: 100, Message: "升级失败：校验失败"})
		agents.replies <- upgradeReply{err: errors.New("节点连接已断开")}
		snap := waitJob(t, &jobs, job)
		if snap.State != upgradeJobFailed || snap.Message != "校验失败" {
			t.Fatalf("snapshot %+v", snap)
		}
	})
	t.Run("offline and busy", func(t *testing.T) {
		var jobs agentUpgradeJobs
		agents := newFakeUpgradeAgents()
		agents.online = false
		if _, err := jobs.start(agents, 6, upgradeTarget, nil); err == nil || err.Error() != "节点不在线" {
			t.Fatalf("offline start: %v", err)
		}
		agents.online = true
		job, _ := jobs.start(agents, 6, upgradeTarget, nil)
		if again, err := jobs.start(agents, 6, upgradeTarget, nil); err != nil || again != job {
			t.Fatalf("same target did not join the running job: %v", err)
		}
		if _, err := jobs.start(agents, 6, "3.0.27-fork.131", nil); err == nil || !strings.Contains(err.Error(), "正在升级到") {
			t.Fatalf("other target while running: %v", err)
		}
		agents.reconnect(upgradeTarget)
		waitJob(t, &jobs, job)
	})
}

// End to end through the real WS server and the HTTP endpoints: the request
// answers 「仍在升级」 within upgradeHTTPWait, the job goes on and ends with
// the version the mock agent reports when it connects again.
func TestNodeUpgradeHTTPPendingThenReconnectVersion(t *testing.T) {
	shortUpgradeTimers(t)
	r, err := repo.Open(filepath.Join(t.TempDir(), "ota.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.DB().Exec("INSERT INTO node(id,name,secret,server_ip,port,created_time,status) VALUES(52,'火山广州','ota-secret','::1','1000',0,0)").Error; err != nil {
		t.Fatal(err)
	}
	h := New(r, "test-jwt-secret", upgradeTarget)
	wsSrv := httptest.NewServer(h.wsServer)
	defer wsSrv.Close()

	dial := func(version string) *websocket.Conn {
		u, _ := url.Parse(wsSrv.URL)
		u.Scheme = "ws"
		q := u.Query()
		q.Set("type", "1")
		q.Set("secret", "ota-secret")
		q.Set("version", version)
		u.RawQuery = q.Encode()
		c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	waitOnline := func(want bool) {
		deadline := time.Now().Add(3 * time.Second)
		for h.wsServer.NodeOnline(52) != want {
			if time.Now().After(deadline) {
				t.Fatalf("node online != %v", want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	old := dial("3.0.27-fork.129 (debian/amd64)")
	waitOnline(true)
	gotCommand := make(chan struct{})
	crypto, _ := security.NewAESCrypto("ota-secret")
	go func() {
		for {
			_, raw, err := old.ReadMessage()
			if err != nil {
				return
			}
			plain := string(raw)
			var wrap struct {
				Encrypted bool   `json:"encrypted"`
				Data      string `json:"data"`
			}
			if json.Unmarshal(raw, &wrap) == nil && wrap.Encrypted {
				if dec, err := crypto.Decrypt(wrap.Data); err == nil {
					plain = string(dec)
				}
			}
			if strings.Contains(plain, `"UpgradeAgent"`) {
				close(gotCommand)
				break
			}
		}
		// Slow download: progress beyond the HTTP wait, never a reply.
		for i := 1; i <= 4; i++ {
			time.Sleep(80 * time.Millisecond)
			msg, _ := json.Marshal(map[string]interface{}{"type": "UpgradeProgress", "success": true, "message": "下载中", "data": map[string]interface{}{"stage": "downloading", "percent": i * 20}})
			_ = old.WriteMessage(websocket.TextMessage, msg)
		}
	}()

	post := func(fn http.HandlerFunc, body string) map[string]interface{} {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
		res := httptest.NewRecorder()
		fn(res, req)
		var out map[string]interface{}
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
			t.Fatalf("bad JSON %q", res.Body.String())
		}
		return out
	}
	started := time.Now()
	out := post(h.nodeUpgrade, `{"id":52}`)
	if took := time.Since(started); took > time.Second {
		t.Fatalf("upgrade request took %s (HTTP wait %s)", took, upgradeHTTPWait)
	}
	select {
	case <-gotCommand:
	case <-time.After(3 * time.Second):
		t.Fatal("mock agent never got UpgradeAgent")
	}
	data, _ := out["data"].(map[string]interface{})
	if out["code"].(float64) != 0 || data["pending"] != true || !strings.Contains(data["message"].(string), "仍在升级") {
		t.Fatalf("pending answer %v", out)
	}
	time.Sleep(400 * time.Millisecond) // progress keeps the job alive past the idle timeout
	if snap, _ := h.upgradeJobs.snapshot(52); snap.State != upgradeJobRunning || snap.Stage != "downloading" {
		t.Fatalf("job while downloading: %+v", snap)
	}
	// The agent restarts on the new binary without replying.
	_ = old.Close()
	waitOnline(false)
	fresh := dial(upgradeTarget + " (debian/amd64)")
	defer fresh.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		st := post(h.nodeUpgradeStatus, `{"id":52}`)
		if d, _ := st["data"].(map[string]interface{}); d != nil && d["state"] == upgradeJobSucceeded {
			if !strings.Contains(d["message"].(string), upgradeTarget) {
				t.Fatalf("status %v", d)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not succeed after the reconnect: %v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st := mustNodeStatus(t, r, 52); st != 1 {
		t.Fatalf("stored status %d after reconnect", st)
	}
}

func mustNodeStatus(t *testing.T, r *repo.Repository, id int64) int {
	t.Helper()
	st, _, _, _, _, err := r.GetNodeStatusFields(id)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
