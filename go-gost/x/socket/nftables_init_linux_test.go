//go:build linux

package socket

import "testing"

type closeTrackingNftManager struct {
	NftablesManagerInterface
	id     int
	closed *[]int
}

func (m *closeTrackingNftManager) Close() { *m.closed = append(*m.closed, m.id) }

// Every WebSocket reconnect builds a new nftables manager; the previous one must be closed
// (its quota poll stopped) before the new one clears the tables.
func TestReconnectClosesPreviousNftablesManager(t *testing.T) {
	var closed, created []int
	next := 0
	orig := newNftablesManager
	t.Cleanup(func() { newNftablesManager = orig })
	newNftablesManager = func() (NftablesManagerInterface, error) {
		if len(closed) != len(created) {
			t.Fatalf("new manager built before the previous one was closed: created %v closed %v", created, closed)
		}
		next++
		created = append(created, next)
		return &closeTrackingNftManager{id: next, closed: &closed}, nil
	}
	w := &WebSocketReporter{}
	for i := 0; i < 5; i++ {
		w.initNftablesManager()
	}
	if len(created) != 5 || len(closed) != 4 || closed[3] != 4 {
		t.Fatalf("created %v closed %v", created, closed)
	}
	if w.nftablesMgr.(*closeTrackingNftManager).id != 5 {
		t.Fatal("reporter does not use the newest manager")
	}
}
