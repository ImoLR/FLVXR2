package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go-backend/internal/http/response"
	"go-backend/internal/ws"
)

// Plan 248: an agent OTA follows the agent's real progress.
//
// 11-12 火山广州 downloaded fork.129 slowly from China: the panel gave up after
// a fixed 300 s (request write timeout = nginx = command timeout) and reported
// a failure, while the agent finished at 312 s and came back on fork.129.
// Now every upgrade is a job: it keeps waiting while the agent reports
// download progress, survives a WS reconnect in the middle, and its result is
// the version the agent reports when it connects again. The HTTP call answers
// within upgradeHTTPWait (Cloudflare cuts requests at 100 s) with the result
// or 「仍在升级」; the job goes on, its end is pushed to the node page as an
// upgrade_progress message and can be read at /node/upgrade/status.
var (
	upgradeHTTPWait    = 90 * time.Second
	upgradeIdleTimeout = 6 * time.Minute  // no progress for this long → failed (one source may take 5 min)
	upgradeRestartWait = 3 * time.Minute  // after the agent's 「重启中」 reply: back on the target within this
	upgradeMaxDuration = 30 * time.Minute // 4 sources × 5 min + verify + restart
	upgradePollEvery   = time.Second
	upgradeJobKeep     = time.Hour // finished jobs stay readable
)

const (
	upgradeJobQueued    = "queued"
	upgradeJobRunning   = "running"
	upgradeJobSucceeded = "succeeded"
	upgradeJobFailed    = "failed"
)

// agentUpgradeJob fields are guarded by agentUpgradeJobs.mu.
type agentUpgradeJob struct {
	nodeID         int64
	target         string
	state          string
	stage          string
	percent        int
	message        string
	createdAt      time.Time
	sentAt         time.Time
	lastProgressAt time.Time
	finishedAt     time.Time
	done           chan struct{}
}

type upgradeJobSnapshot struct {
	NodeID     int64  `json:"id"`
	Target     string `json:"targetVersion"`
	State      string `json:"state"`
	Stage      string `json:"stage"`
	Percent    int    `json:"percent"`
	Message    string `json:"message"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
}

func (j *agentUpgradeJob) snapshotLocked() upgradeJobSnapshot {
	s := upgradeJobSnapshot{NodeID: j.nodeID, Target: j.target, State: j.state, Stage: j.stage,
		Percent: j.percent, Message: j.message, StartedAt: j.createdAt.UnixMilli()}
	if !j.finishedAt.IsZero() {
		s.FinishedAt = j.finishedAt.UnixMilli()
	}
	return s
}

func (j *agentUpgradeJob) finishedLocked() bool {
	return j.state == upgradeJobSucceeded || j.state == upgradeJobFailed
}

// upgradeAgentPort is what a job needs from the agent sessions (ws.Server).
type upgradeAgentPort interface {
	NodeOnline(nodeID int64) bool
	SendCommandContext(ctx context.Context, nodeID int64, cmdType string, data interface{}, timeout time.Duration) (ws.CommandResult, error)
	NodeSessionVersion(nodeID int64) (string, time.Time, bool)
	BroadcastUpgradeProgress(nodeID int64, p ws.UpgradeProgress)
}

type agentUpgradeJobs struct {
	mu   sync.Mutex
	jobs map[int64]*agentUpgradeJob
}

// start begins an upgrade of nodeID to target, or joins the running one to the
// same target. sem (optional) bounds how many jobs of a batch run at once.
func (t *agentUpgradeJobs) start(agents upgradeAgentPort, nodeID int64, target string, sem chan struct{}) (*agentUpgradeJob, error) {
	if agents == nil || !agents.NodeOnline(nodeID) {
		return nil, fmt.Errorf("节点不在线")
	}
	now := time.Now()
	t.mu.Lock()
	if t.jobs == nil {
		t.jobs = make(map[int64]*agentUpgradeJob)
	}
	for id, job := range t.jobs {
		if job.finishedLocked() && now.Sub(job.finishedAt) > upgradeJobKeep {
			delete(t.jobs, id)
		}
	}
	if job := t.jobs[nodeID]; job != nil && !job.finishedLocked() {
		t.mu.Unlock()
		if job.target == target {
			return job, nil
		}
		return nil, fmt.Errorf("节点正在升级到 %s，请稍后再试", job.target)
	}
	job := &agentUpgradeJob{nodeID: nodeID, target: target, state: upgradeJobQueued, stage: "queued",
		message: "等待升级", createdAt: now, done: make(chan struct{})}
	t.jobs[nodeID] = job
	t.mu.Unlock()
	go t.run(agents, job, sem)
	return job, nil
}

// wait returns the job's state after it finished or after d, whichever is first.
func (t *agentUpgradeJobs) wait(job *agentUpgradeJob, d time.Duration) upgradeJobSnapshot {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-job.done:
	case <-timer.C:
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return job.snapshotLocked()
}

func (t *agentUpgradeJobs) snapshot(nodeID int64) (upgradeJobSnapshot, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	job := t.jobs[nodeID]
	if job == nil {
		return upgradeJobSnapshot{}, false
	}
	return job.snapshotLocked(), true
}

// progress is the ws UpgradeProgress hook (must not block).
func (t *agentUpgradeJobs) progress(nodeID int64, p ws.UpgradeProgress) {
	t.mu.Lock()
	defer t.mu.Unlock()
	job := t.jobs[nodeID]
	if job == nil || job.state != upgradeJobRunning {
		return
	}
	job.stage, job.percent, job.message = p.Stage, p.Percent, p.Message
	job.lastProgressAt = time.Now()
}

func upgradeVersionMatches(reported, target string) bool {
	reported, target = strings.TrimSpace(reported), strings.TrimSpace(target)
	return target != "" && (reported == target || strings.HasPrefix(reported, target+" "))
}

func upgradeVersionLabel(reported string) string {
	if v := strings.TrimSpace(strings.SplitN(strings.TrimSpace(reported), " ", 2)[0]); v != "" {
		return v
	}
	return "未知版本"
}

type upgradeReply struct {
	res ws.CommandResult
	err error
}

func (t *agentUpgradeJobs) run(agents upgradeAgentPort, job *agentUpgradeJob, sem chan struct{}) {
	if sem != nil {
		sem <- struct{}{}
		defer func() { <-sem }()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.mu.Lock()
	job.state, job.stage, job.message = upgradeJobRunning, "sending", "发送升级命令"
	job.sentAt = time.Now()
	sentAt := job.sentAt
	t.mu.Unlock()

	replies := make(chan upgradeReply, 1)
	go func() {
		res, err := agents.SendCommandContext(ctx, job.nodeID, "UpgradeAgent", agentUpgradeCommandData(job.target), upgradeMaxDuration)
		replies <- upgradeReply{res, err}
	}()
	ticker := time.NewTicker(upgradePollEvery)
	defer ticker.Stop()
	var repliedAt time.Time // the agent answered 「重启中」 (download + install done)
	for {
		select {
		case reply := <-replies:
			replies = nil
			switch {
			case reply.err == nil:
				repliedAt = time.Now()
			case reply.res.Type != "":
				// The agent itself answered with an error (sources down,
				// checksum, permissions …): final.
				t.finish(agents, job, upgradeJobFailed, reply.err.Error())
				return
			default:
				// 节点连接已断开 / 等待节点响应超时: the agent keeps downloading
				// on its side; follow its progress and its next session.
			}
		case <-ticker.C:
		}
		now := time.Now()
		if version, started, ok := agents.NodeSessionVersion(job.nodeID); ok && started.After(sentAt) {
			if upgradeVersionMatches(version, job.target) {
				t.finish(agents, job, upgradeJobSucceeded, "已升级到 "+job.target)
				return
			}
			if !repliedAt.IsZero() {
				t.finish(agents, job, upgradeJobFailed, "节点重启后仍是 "+upgradeVersionLabel(version))
				return
			}
			// A reconnect in the middle of the download: keep waiting.
		}
		t.mu.Lock()
		lastActivity := sentAt
		if job.lastProgressAt.After(lastActivity) {
			lastActivity = job.lastProgressAt
		}
		agentFailed := job.stage == "failed"
		agentMessage := job.message
		t.mu.Unlock()
		switch {
		case agentFailed && replies == nil, agentFailed && now.Sub(lastActivity) > 10*time.Second:
			// The agent reported 「升级失败」 and its reply is in or lost.
			t.finish(agents, job, upgradeJobFailed, strings.TrimPrefix(agentMessage, "升级失败："))
			return
		case !repliedAt.IsZero() && now.Sub(repliedAt) > upgradeRestartWait:
			t.finish(agents, job, upgradeJobFailed, fmt.Sprintf("节点重启后 %s 内没有重新连接", upgradeRestartWait))
			return
		case repliedAt.IsZero() && now.Sub(lastActivity) > upgradeIdleTimeout:
			t.finish(agents, job, upgradeJobFailed, fmt.Sprintf("节点 %s 没有升级进度", upgradeIdleTimeout))
			return
		case now.Sub(sentAt) > upgradeMaxDuration:
			t.finish(agents, job, upgradeJobFailed, fmt.Sprintf("升级超过 %s 仍未完成", upgradeMaxDuration))
			return
		}
	}
}

func (t *agentUpgradeJobs) finish(agents upgradeAgentPort, job *agentUpgradeJob, state, message string) {
	t.mu.Lock()
	job.state, job.message, job.percent = state, message, 100
	job.stage = "done"
	if state == upgradeJobFailed {
		job.stage = "failed"
	}
	job.finishedAt = time.Now()
	stage := job.stage
	t.mu.Unlock()
	close(job.done)
	text := message
	if state == upgradeJobFailed {
		text = "升级失败：" + message
	}
	// Ends the node page's progress bar and reloads the list (any percent ≥ 100).
	agents.BroadcastUpgradeProgress(job.nodeID, ws.UpgradeProgress{Stage: stage, Percent: 100, Message: text})
}

// upgradePendingMessage is the 「仍在升级」 answer of a request that stopped
// waiting before the job finished.
func upgradePendingMessage(s upgradeJobSnapshot) string {
	detail := strings.TrimSpace(s.Message)
	if detail == "" {
		detail = s.Stage
	}
	return fmt.Sprintf("节点仍在升级（%s），完成后自动刷新", detail)
}

// nodeUpgradeStatus: POST /api/v1/node/upgrade/status {id} — the node's last
// OTA job (data null when the panel has not run one since it started).
func (h *Handler) nodeUpgradeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.WriteJSON(w, response.ErrDefault("请求失败"))
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := decodeJSON(r.Body, &req); err != nil || req.ID <= 0 {
		response.WriteJSON(w, response.ErrDefault("请求参数错误"))
		return
	}
	snap, ok := h.upgradeJobs.snapshot(req.ID)
	if !ok {
		response.WriteJSON(w, response.OK(nil))
		return
	}
	response.WriteJSON(w, response.OK(snap))
}
