package handler

import (
	"strings"
	"testing"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

// A 「我自己的节点」 rule is counted once: at num=1 the create finishes (its
// in-process replay is not refused for the pending chain it belongs to), and
// afterwards both chain and plain creates hit the same limit message.
func TestChainCreateSharesUserRuleLimit(t *testing.T) {
	e, _ := newChainEnv(t)
	e.exec("UPDATE user SET num = 1 WHERE id = 7")
	c := createChain(t, e, 10, "edge", testSSLink, "key-edge-0001")
	if c.State != model.ChainStateActive || c.ForwardID <= 0 {
		t.Fatalf("chain at the limit edge did not finish: %+v", c)
	}
	code, msg, _ := callAs(t, e.h.forwardCreate, 7, 1, chainCreateBody(10, "second", "socks5://9.9.9.9:1080", ""))
	e.h.chains.wg.Wait()
	if code == 0 || msg != "已达到规则数上限（1 条），请删除不用的规则后再新建" {
		t.Fatalf("second chain: code=%d msg=%q", code, msg)
	}
	if err := e.r.CheckUserRuleLimit(7, 1); !repo.IsRuleLimitError(err) {
		t.Fatalf("plain create check: %v", err)
	}
	// Admin acting for the user is exempt.
	if err := e.h.forwardRuleLimit(7, 0); err != nil {
		t.Fatalf("admin not exempt: %v", err)
	}
}

// A chain create still in flight (no rule row yet) holds its slot.
func TestPendingChainCountsAgainstRuleLimit(t *testing.T) {
	e, _ := newChainEnv(t)
	e.exec("UPDATE user SET num = 2 WHERE id = 7")
	e.addForward(900, 7, 30)
	if err := e.r.CheckUserRuleLimit(7, 1); err != nil {
		t.Fatalf("1 of 2 used: %v", err)
	}
	e.exec(`INSERT INTO forward_chain(user_id, tunnel_id, state, op, forward_id, port, created_time, updated_time) VALUES(7, 10, ?, 'create', 0, 31000, 1, 1)`, model.ChainStateCreating)
	err := e.r.CheckUserRuleLimit(7, 1)
	if !repo.IsRuleLimitError(err) || !strings.Contains(err.Error(), "（2 条）") {
		t.Fatalf("pending chain not counted: %v", err)
	}
}
