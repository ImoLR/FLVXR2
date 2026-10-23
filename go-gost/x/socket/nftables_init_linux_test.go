//go:build linux

package socket

import (
	"errors"
	"testing"
)

type closeTrackingNftManager struct {
	NftablesManagerInterface
	id     int
	closed *[]int
}

func (m *closeTrackingNftManager) Close() { *m.closed = append(*m.closed, m.id) }

// The nftables manager is built once per process (plan 138): a WebSocket
// reconnect keeps it, so the forwarding rules it installed stay in place. A
// failed build is retried on the next connect.
func TestReconnectKeepsNftablesManager(t *testing.T) {
	var closed, created []int
	next := 0
	fail := true
	orig := newNftablesManager
	t.Cleanup(func() { newNftablesManager = orig })
	newNftablesManager = func() (NftablesManagerInterface, error) {
		if fail {
			fail = false
			return nil, errTestNftUnavailable
		}
		next++
		created = append(created, next)
		return &closeTrackingNftManager{id: next, closed: &closed}, nil
	}
	w := &WebSocketReporter{}
	w.initNftablesManager() // first build fails
	if w.nftablesMgr != nil {
		t.Fatal("failed build left a manager")
	}
	for i := 0; i < 5; i++ {
		w.initNftablesManager()
	}
	if len(created) != 1 || len(closed) != 0 {
		t.Fatalf("created %v closed %v", created, closed)
	}
	if w.nftablesMgr.(*closeTrackingNftManager).id != 1 {
		t.Fatal("reporter replaced its manager on reconnect")
	}
}

var errTestNftUnavailable = errors.New("nft unavailable")
