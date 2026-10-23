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

// initNftablesManager runs on every WebSocket (re)connect, but builds the
// manager only once per process (plan 138). NewManager clears the agent's nft
// tables (DNAT rules, accounting, quota): on a fresh process that removes what
// a previous process left, and the panel deploys every forward again. A mere
// reconnect must keep the running rules: rebuilding there cut every
// nftables-mode forward (and its byte accounting) until the panel's redeploy
// ~10 s later, and the panel now skips unchanged forwards on reconnect.
// A failed first build is retried on the next connect.
func (w *WebSocketReporter) initNftablesManager() {
	if w.nftablesMgr != nil {
		return
	}
	mgr, err := newNftablesManager()
	if err != nil {
		fmt.Printf("nftables manager initialization failed: %v\n", err)
		return
	}
	if mgr != nil {
		w.setNftablesManager(mgr)
		fmt.Println("nftables manager initialized successfully")
	}
}
