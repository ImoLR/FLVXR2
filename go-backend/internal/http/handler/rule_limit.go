package handler

// ruleLimitApplies: the user's 规则数 (user.num) binds normal users; an admin
// actor is exempt, also when creating a rule owned by a normal user.
func ruleLimitApplies(actorRole int) bool { return actorRole != 0 }

// forwardRuleLimit is the single early rule-allowance check shared by every
// create path (create, import, copy, idempotent create, 「我自己的节点」). The
// authoritative check repeats inside the create transaction.
func (h *Handler) forwardRuleLimit(ownerID int64, actorRole int) error {
	if !ruleLimitApplies(actorRole) {
		return nil
	}
	return h.repo.CheckUserRuleLimit(ownerID, 1)
}
