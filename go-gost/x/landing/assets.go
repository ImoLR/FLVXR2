package landing

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-gost/x/logsafe"
)

// Pinned assets (the Core binary, geo data) are verified by SHA256 once per
// file change, not on every reconcile tick: the hash is remembered together
// with the file's stat stamp (size, mtime, inode, ctime), and any rewrite,
// replacement or chmod changes the stamp and forces one new hash.

// Failed downloads wait before the next attempt: 1 min after the first
// failure, doubling up to 30 min. Each attempt may walk four mirrors and pull
// tens of MiB, so a broken mirror or GitHub outage must not turn the 10 s
// reconcile tick (or the panel's 1 min re-push) into a download loop.
const (
	downloadRetryMin = 30 * time.Second
	downloadRetryMax = 30 * time.Minute
)

type downloadRetry struct {
	backoff *logsafe.Backoff
	next    time.Time
	err     error
}

func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Manager) logger() *logsafe.RoutineLogger {
	if m.logs == nil {
		m.logs = logsafe.NewRoutineLogger(nil)
	}
	return m.logs
}

func (m *Manager) hash(path string) (string, error) {
	if m.hashFile != nil {
		return m.hashFile(path)
	}
	return fileHash(path)
}

// verifiedFile reports whether path holds the pinned content. size < 0 skips
// the size pre-check.
func (m *Manager) verifiedFile(path, expected string, size int64) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	stamp := stampOf(info)
	if m.verified == nil {
		m.verified = map[string]fileStamp{}
	}
	if old, ok := m.verified[path]; ok && old == stamp {
		return true
	}
	delete(m.verified, path)
	if size >= 0 && info.Size() != size {
		return false
	}
	if sum, err := m.hash(path); err != nil || sum != expected {
		return false
	}
	m.verified[path] = stamp
	return true
}

// retryDownload runs fetch unless an earlier failure of the same asset is
// still waiting out its backoff, in which case the last error is returned
// without touching the network. Failures are logged through the shared
// RoutineLogger: the first one, a count every 10 min, and one recovery line.
func (m *Manager) retryDownload(ctx context.Context, name string, fetch func(context.Context) error) error {
	now := m.clock()
	r := m.downloads[name]
	if r != nil && now.Before(r.next) {
		return fmt.Errorf("%w，%s后重试", r.err, retryIn(r.next.Sub(now)))
	}
	err := fetch(ctx)
	if err == nil {
		delete(m.downloads, name)
		m.logger().Record("landing download", name, "")
		return nil
	}
	if ctx.Err() != nil {
		// Shutdown or a caller deadline is not a download failure.
		return err
	}
	if r == nil {
		r = &downloadRetry{backoff: logsafe.NewBackoff(downloadRetryMin, downloadRetryMax)}
		if m.downloads == nil {
			m.downloads = map[string]*downloadRetry{}
		}
		m.downloads[name] = r
	}
	delay := r.backoff.Failure()
	r.next, r.err = now.Add(delay), err
	m.logger().Record("landing download", name, err.Error())
	return fmt.Errorf("%w，%s后重试", err, retryIn(delay))
}

func retryIn(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d 秒", int((d+time.Second-1)/time.Second))
	}
	return fmt.Sprintf("%d 分钟", int((d+time.Minute-1)/time.Minute))
}
