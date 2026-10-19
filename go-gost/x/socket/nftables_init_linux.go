//go:build linux

package socket

import (
	"fmt"

	"github.com/go-gost/x/nftables"
)

// newNftablesManager builds the kernel nftables manager; tests replace it.
var newNftablesManager = func() (NftablesManagerInterface, error) {
	if supported, _ := nftables.CheckNftablesSupport(); !supported {
		return nil, nil
	}
	mgr, err := nftables.NewManager()
	if err != nil {
		return nil, err
	}
	return mgr, nil
}

// initNftablesManager runs on every WebSocket (re)connect. The new manager starts from a clean
// slate (NewManager clears the agent's nft tables; the panel re-syncs the node's forwards after
// a reconnect), so the previous manager must be closed first: otherwise its quota poll keeps
// running forever against objects that no longer exist (node 48: one leaked poller per
// reconnect, ~150 failure log lines per second).
func (w *WebSocketReporter) initNftablesManager() {
	if old := w.nftablesMgr; old != nil {
		old.Close()
		w.nftablesMgr = nil
	}
	mgr, err := newNftablesManager()
	if err != nil {
		fmt.Printf("nftables manager initialization failed: %v\n", err)
		return
	}
	if mgr != nil {
		w.nftablesMgr = mgr
		fmt.Println("nftables manager initialized successfully")
	}
}
