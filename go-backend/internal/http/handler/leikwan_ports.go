package handler

import (
	"fmt"

	"gorm.io/gorm"
)

// Plan 265: ports the node's LeiKwan client listens on (node.leikwan_forwards)
// are never handed out, and a typed / changed port there is refused. Rows that
// already hold such a port keep saving; nothing is moved automatically.

func leiKwanPortTakenError(nodeName string, port int) error {
	return fmt.Errorf("节点 %s 端口 %d 已被本机利群转发占用，请换一个", nodeName, port)
}

// checkLeiKwanForwardPortTx: a forward's new / changed port on the node.
func (h *Handler) checkLeiKwanForwardPortTx(tx *gorm.DB, node *nodeRecord, port int, currentForwardID int64) error {
	if h == nil || h.repo == nil || node == nil || port <= 0 {
		return nil
	}
	var blocked bool
	var err error
	if tx != nil {
		blocked, err = h.repo.LeiKwanPortBlocksForwardTx(tx, node.ID, port, currentForwardID)
	} else {
		blocked, err = h.repo.LeiKwanPortBlocksForward(node.ID, port, currentForwardID)
	}
	if err != nil {
		return err
	}
	if blocked {
		return leiKwanPortTakenError(node.Name, port)
	}
	return nil
}

// checkLeiKwanTunnelPortsTx: typed out / chain-hop ports of a tunnel create or
// edit. tunnelID = the tunnel being edited (its unchanged ports keep saving).
func (h *Handler) checkLeiKwanTunnelPortsTx(tx *gorm.DB, state *tunnelCreateState, tunnelID int64) error {
	if h == nil || h.repo == nil || tx == nil || state == nil {
		return nil
	}
	check := func(n tunnelRuntimeNode) error {
		if n.Port <= 0 {
			return nil
		}
		node := state.Nodes[n.NodeID]
		if node == nil || node.IsRemote == 1 {
			return nil
		}
		blocked, err := h.repo.LeiKwanPortBlocksTunnelTx(tx, n.NodeID, n.Port, tunnelID)
		if err != nil {
			return err
		}
		if blocked {
			return leiKwanPortTakenError(node.Name, n.Port)
		}
		return nil
	}
	for _, n := range state.OutNodes {
		if err := check(n); err != nil {
			return err
		}
	}
	for _, hop := range state.ChainHops {
		for _, n := range hop {
			if err := check(n); err != nil {
				return err
			}
		}
	}
	return nil
}

// randomPortAvoiding: node-deploy's random 10000–59999 listen port, never one
// in avoid (a handful of entries, so a few draws are enough).
func randomPortAvoiding(avoid map[int]bool) int {
	for i := 0; i < 64; i++ {
		p := randomPort()
		if !avoid[p] {
			return p
		}
	}
	for p := 10000; p < 60000; p++ {
		if !avoid[p] {
			return p
		}
	}
	return randomPort()
}
