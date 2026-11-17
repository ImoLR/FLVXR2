package handler

// Plan 276 (F01): deleting a user deletes their 利群 rules like their normal
// rules — the LeiKwan forward (vendor API), the automatic 备线 (a linked one
// stays as a normal rule), the rule row and the grant (the auto 利群汇聚
// subscription goes with the user's subscriptions). A LeiKwan delete that
// fails refuses the user delete with that error: no forward is left running
// for a user who no longer exists.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

func (h *Handler) leiKwanDeleteUserRules(userID int64) error {
	if h == nil || h.repo == nil || userID <= 0 {
		return nil
	}
	leiKwanRuleMu.Lock()
	defer leiKwanRuleMu.Unlock()
	rules, err := h.repo.ListLeiKwanRules(userID)
	if err != nil {
		return errors.New("读取利群规则失败，用户未删除")
	}
	for i := range rules {
		x := &rules[i]
		// The LeiKwan client spaces writes; one rule at a time, bounded.
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		err := h.leiKwanDeleteForward(ctx, x)
		cancel()
		if err != nil {
			return fmt.Errorf("利群规则「%s」删除失败：%s（用户未删除，可重试）", x.Name, err.Error())
		}
		if err := h.leiKwanDropBackup(x); err != nil {
			log.Printf("删除用户 %d：利群规则 %s 的备线未删掉: %v", userID, x.Name, err)
		}
		if err := h.deleteLeiKwanRule(x.ID); err != nil {
			return fmt.Errorf("利群规则「%s」记录删除失败（利群转发已删除，用户未删除，可重试）", x.Name)
		}
		log.Printf("删除用户 %d：利群规则 %s（转发 %d）已一起删除", userID, x.Name, x.ForwardID)
	}
	if err := h.repo.DeleteLeiKwanGrant(userID); err != nil {
		return errors.New("取消利群授权失败，用户未删除：" + err.Error())
	}
	if len(rules) > 0 {
		h.leiKwanSubsChanged(userID)
		h.leiKwanHostKick() // ports / keys / warm pools leave the 利群入口 hosts
	}
	return nil
}
