package logsafe

import (
	"fmt"
	"sync"
	"time"
)

const routineLogInterval = 10 * time.Minute

type routineFailure struct {
	last    time.Time
	count   uint64
	pending uint64
}

// RoutineLogger keeps successful periodic operations silent. Failures are
// grouped by command and target, even when their error text changes. Its zero
// value is ready for concurrent use; state is removed on recovery.
type RoutineLogger struct {
	mu      sync.Mutex
	entries map[[2]string]*routineFailure
	now     func() time.Time
	emit    func(string)
}

// Record logs the first failure, a count at most every ten minutes on subsequent
// failures, and one recovery. An empty failure means the operation succeeded.
func (l *RoutineLogger) Record(command, target, failure string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	emit := l.emit
	if emit == nil {
		emit = func(s string) { fmt.Println(s) }
	}
	key := [2]string{command, target}
	entry := l.entries[key]
	if failure == "" {
		if entry != nil {
			emit(fmt.Sprintf("✅ %s target=%q 已恢复（累计失败 %d 次，未汇总 %d 次）", command, Text(target), entry.count, entry.pending))
			delete(l.entries, key)
		}
		return
	}
	if entry == nil {
		if l.entries == nil {
			l.entries = make(map[[2]string]*routineFailure)
		}
		l.entries[key] = &routineFailure{last: now, count: 1}
		emit(fmt.Sprintf("❌ %s target=%q 首次失败: %q", command, Text(target), Text(failure)))
		return
	}
	entry.count++
	entry.pending++
	if now.Sub(entry.last) >= routineLogInterval {
		emit(fmt.Sprintf("❌ %s target=%q 失败汇总: %d 次（累计 %d 次），最近错误: %q", command, Text(target), entry.pending, entry.count, Text(failure)))
		entry.last = now
		entry.pending = 0
	}
}
