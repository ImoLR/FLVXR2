package handler

// Plan 271 利群备线流量.
//
// A 利群 rule's 备线 is an ordinary FLVX forward owned by the admin (auto
// backups: leiKwanCreateBackup; linked ones: the hand-made 備線·NNN rules), so
// until now its bytes were billed to the admin. After 日志快切 moves a rule to
// its 备线 the user's traffic is counted twice on purpose (user 11-17: the 利群
// port counters + the 备线 forward), and both must land on the 利群 rule owner:
//
//   - a forward that is the 备线 of 利群 rules owned by exactly one user U
//     (≠ the forward's owner) bills its user-level parts — 总流量, quota
//     usage, per-server quotas of its path, traffic pools, flow policies — to
//     U instead of the forward owner, with the landing exemption of the rule's
//     landing node; the forward's own counters stay on the forward;
//   - two or more different owners on one shared 备线 are ambiguous: the bytes
//     stay with the forward owner (no invented split, logged once);
//   - every rule using the forward gets its billed bytes in backup_in_flow /
//     backup_out_flow (a shared 备线 adds its full bytes to each rule), reset
//     with the owner's reset like the rule's 利群流量.
//
// The forward → rules index is cached for 30 s (one small query), and dropped
// whenever a 利群 rule is saved or deleted through the handler.

import (
	"log"
	"sort"
	"sync"
	"time"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

const lkBackupIndexTTL = 30 * time.Second

type lkBackupIndex struct {
	mu     sync.Mutex
	at     time.Time
	refs   map[int64][]repo.LeiKwanBackupRef // 备线 forward id → rules
	warned map[int64]bool
}

func (h *Handler) leiKwanBackupIndexDirty() {
	h.lkBackups.mu.Lock()
	h.lkBackups.at = time.Time{}
	h.lkBackups.mu.Unlock()
}

// leiKwanBackupRefsFor: the 利群 rules whose 备线 is forwardID (nil = none).
func (h *Handler) leiKwanBackupRefsFor(forwardID int64) ([]repo.LeiKwanBackupRef, error) {
	if h == nil || h.repo == nil || forwardID <= 0 {
		return nil, nil
	}
	x := &h.lkBackups
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.refs == nil || time.Since(x.at) >= lkBackupIndexTTL {
		rows, err := h.repo.ListLeiKwanBackupRefs()
		if err != nil {
			return nil, err
		}
		refs := map[int64][]repo.LeiKwanBackupRef{}
		for _, r := range rows {
			refs[r.BackupForwardID] = append(refs[r.BackupForwardID], r)
		}
		x.refs, x.at = refs, time.Now()
	}
	return x.refs[forwardID], nil
}

// leiKwanBackupPayer: the user who pays a 备线 forward's bytes when that is
// not the forward's owner, plus the landing node whose exemption applies.
func (h *Handler) leiKwanBackupPayer(f *forwardRecord, refs []repo.LeiKwanBackupRef) (int64, int64, bool) {
	owners := map[int64]bool{}
	landing := int64(-1)
	for _, r := range refs {
		owners[r.UserID] = true
		if landing == -1 {
			landing = r.LandingNodeID
		} else if landing != r.LandingNodeID {
			landing = 0
		}
	}
	if len(owners) != 1 {
		h.lkBackups.mu.Lock()
		if h.lkBackups.warned == nil {
			h.lkBackups.warned = map[int64]bool{}
		}
		if !h.lkBackups.warned[f.ID] {
			h.lkBackups.warned[f.ID] = true
			ids := make([]int64, 0, len(owners))
			for id := range owners {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			log.Printf("[leikwan] 备线 forward %d serves 利群 rules of users %v: its bytes stay with forward owner %d (no split)", f.ID, ids, f.UserID)
		}
		h.lkBackups.mu.Unlock()
		return 0, 0, false
	}
	var owner int64
	for id := range owners {
		owner = id
	}
	if owner <= 0 || owner == f.UserID {
		return 0, 0, false
	}
	return owner, max(landing, 0), true
}

// planLeiKwanBackupFlow handles a forward that is a 利群 备线. done = the
// forward's billing was fully planned here (the caller stops).
func (h *Handler) planLeiKwanBackupFlow(b *flowIngestBatch, c *flowIngestCache, nodeID int64, serviceName string, f *forwardRecord, upload, download, in, out int64) (bool, error) {
	refs, err := h.leiKwanBackupRefsFor(f.ID)
	if err != nil || len(refs) == 0 {
		return false, err
	}
	if b.write.LeiKwanBackups == nil {
		b.write.LeiKwanBackups = map[int64]repo.FlowCounterDelta{}
	}
	for _, r := range refs {
		addFlowDelta(b.write.LeiKwanBackups, r.RuleID, in, out)
	}
	owner, landing, ok := h.leiKwanBackupPayer(f, refs)
	if !ok {
		return false, nil
	}
	exists, err := c.userExists(owner)
	if err != nil || !exists {
		return false, err
	}
	b.addLocalFlow(f.ID, owner, 0, in, out, true, true)
	check := flowPolicyCheck{userID: owner}
	if _, seen := b.seenPolicies[check]; !seen {
		b.seenPolicies[check] = struct{}{}
		b.policyChecks = append(b.policyChecks, check)
	}
	billed := *f
	billed.UserID = owner
	if billed.LandingNodeID == 0 {
		billed.LandingNodeID = landing
	}
	if err := h.planLandingForwardBilling(b, &billed, nodeID, upload, download, in, out); err != nil {
		return true, err
	}
	return true, h.planForwardPeerShareFlow(b, c, nodeID, serviceName, f, upload+download)
}

// saveLeiKwanRule / deleteLeiKwanRule: repo writes that also drop the 备线
// index (a new or moved 备线 is billed right away).
func (h *Handler) saveLeiKwanRule(x *model.LeiKwanRule) error {
	err := h.repo.SaveLeiKwanRule(x)
	h.leiKwanBackupIndexDirty()
	return err
}

func (h *Handler) deleteLeiKwanRule(id int64) error {
	err := h.repo.DeleteLeiKwanRule(id)
	h.leiKwanBackupIndexDirty()
	return err
}

// leiKwanUserBackupFlow: a user's 备线流量 — every distinct 备线 forward of the
// user's rules once (a 备线 shared by two of the user's rules counts once:
// the larger of their counters, i.e. the one attached longer).
func leiKwanUserBackupFlow(refs []repo.LeiKwanBackupRef, userID int64) int64 {
	best := map[int64]int64{}
	for _, r := range refs {
		if r.UserID != userID {
			continue
		}
		if v := r.BackupInFlow + r.BackupOutFlow; v > best[r.BackupForwardID] {
			best[r.BackupForwardID] = v
		}
	}
	var total int64
	for _, v := range best {
		total += v
	}
	return total
}
