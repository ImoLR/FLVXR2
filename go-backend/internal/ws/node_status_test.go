package ws

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go-backend/internal/store/repo"
)

func nodeStatusTestServer(t *testing.T) (*Server, *repo.Repository) {
	t.Helper()
	r, err := repo.Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	for _, sql := range []string{
		"INSERT INTO node(id,name,secret,server_ip,port,created_time,status) VALUES(1,'live','s1','::1','1000',0,0)",
		"INSERT INTO node(id,name,secret,server_ip,port,created_time,status) VALUES(2,'ghost','s2','::1','1000',0,1)",
		"INSERT INTO node(id,name,secret,server_ip,port,created_time,status) VALUES(3,'just-ended','s3','::1','1000',0,1)",
		"INSERT INTO node(id,name,secret,server_ip,port,created_time,status,is_remote) VALUES(4,'federation','s4','::1','1000',0,1,1)",
	} {
		if err := r.DB().Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	return NewServer(r, "jwt"), r
}

func storedStatus(t *testing.T, r *repo.Repository, id int64) int {
	t.Helper()
	st, _, _, _, _, err := r.GetNodeStatusFields(id)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// Plan 248: a live session heals a stored 离线; the cleanup clears 在线 of a
// local node without a session, but not a session that just ended (it writes
// its own 离线) nor a federation node.
func TestNodeStatusSelfHealBothDirections(t *testing.T) {
	s, r := nodeStatusTestServer(t)
	live := &nodeSession{nodeID: 1}
	s.mu.Lock()
	s.nodes[1] = live
	s.nodeOfflineTime[3] = time.Now().Unix()
	s.mu.Unlock()

	if !s.healNodeOnline(live) || storedStatus(t, r, 1) != 1 {
		t.Fatalf("live session did not heal stored 离线")
	}
	if s.healNodeOnline(live) {
		t.Fatalf("heal wrote although the stored flag was already 在线")
	}
	replaced := &nodeSession{nodeID: 1}
	_ = r.UpdateNodeStatus(1, 0)
	if s.healNodeOnline(replaced) || storedStatus(t, r, 1) != 0 {
		t.Fatalf("a session that is not the node's current one healed the flag")
	}
	_ = r.UpdateNodeStatus(1, 1)

	if n := s.healStaleOnline(time.Now()); n != 1 {
		t.Fatalf("stale-online heal count %d, want 1 (ghost only)", n)
	}
	for id, want := range map[int64]int{1: 1, 2: 0, 3: 1, 4: 1} {
		if got := storedStatus(t, r, id); got != want {
			t.Fatalf("node %d status %d, want %d", id, got, want)
		}
	}
	// After the grace the just-ended node is cleared too.
	if n := s.healStaleOnline(time.Now().Add(nodeStaleOnlineGrace + time.Second)); n != 1 || storedStatus(t, r, 3) != 0 {
		t.Fatalf("ended session not cleared after the grace (healed %d)", n)
	}
}

// The metric-driven check runs at most once per nodeStatusHealEvery.
func TestNodeStatusHealCheckRateLimited(t *testing.T) {
	s, r := nodeStatusTestServer(t)
	live := &nodeSession{nodeID: 1}
	s.mu.Lock()
	s.nodes[1] = live
	s.mu.Unlock()
	now := time.Now()
	s.maybeHealNodeOnline(live, now)
	deadline := time.Now().Add(2 * time.Second)
	for storedStatus(t, r, 1) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if storedStatus(t, r, 1) != 1 {
		t.Fatal("first metric did not heal")
	}
	_ = r.UpdateNodeStatus(1, 0)
	s.maybeHealNodeOnline(live, now.Add(nodeStatusHealEvery/2))
	time.Sleep(100 * time.Millisecond)
	if storedStatus(t, r, 1) != 0 {
		t.Fatal("heal check ran again inside the rate limit")
	}
	s.maybeHealNodeOnline(live, now.Add(nodeStatusHealEvery+time.Second))
	deadline = time.Now().Add(2 * time.Second)
	for storedStatus(t, r, 1) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if storedStatus(t, r, 1) != 1 {
		t.Fatal("heal check did not run after the interval")
	}
}

// Status writes of one node never interleave: an old session's 离线 write
// that is still running blocks the new session's 在线 write until it is done,
// and is skipped entirely once the new session is registered.
func TestNodeStatusWritesSerialisedPerNode(t *testing.T) {
	s, r := nodeStatusTestServer(t)
	lk := s.nodeStatusLock(1)
	lk.Lock() // an old session's write in progress
	fresh := &nodeSession{nodeID: 1}
	s.mu.Lock()
	s.nodes[1] = fresh
	s.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	wrote := make(chan struct{})
	go func() {
		defer wg.Done()
		s.markNodeOnline(fresh, func() error { close(wrote); return r.UpdateNodeStatus(1, 1) })
	}()
	select {
	case <-wrote:
		t.Fatal("new session wrote while the old write held the node's status lock")
	case <-time.After(100 * time.Millisecond):
	}
	_ = r.UpdateNodeStatus(1, 0) // the old write lands
	lk.Unlock()
	wg.Wait()
	if storedStatus(t, r, 1) != 1 {
		t.Fatal("new session's 在线 did not land after the old write")
	}
	if s.markNodeOffline(1) || storedStatus(t, r, 1) != 1 {
		t.Fatal("离线 written while a session is registered")
	}
}

// Commands of a newer session survive the end of the old one.
func TestFailPendingOnlyForClosingSession(t *testing.T) {
	s := NewServer(nil, "jwt")
	oldNS, newNS := &nodeSession{nodeID: 1}, &nodeSession{nodeID: 1}
	oldCh, newCh := make(chan CommandResult, 1), make(chan CommandResult, 1)
	s.mu.Lock()
	s.pending["old"] = pendingRequest{nodeID: 1, session: oldNS, ch: oldCh}
	s.pending["new"] = pendingRequest{nodeID: 1, session: newNS, ch: newCh}
	s.mu.Unlock()
	s.failPendingForSession(oldNS, "节点连接已断开")
	if res, ok := <-oldCh; !ok || res.Success {
		t.Fatalf("old session command not failed: %+v %v", res, ok)
	}
	s.mu.RLock()
	_, kept := s.pending["new"]
	s.mu.RUnlock()
	if !kept {
		t.Fatal("new session command failed by the old session's close")
	}
}
