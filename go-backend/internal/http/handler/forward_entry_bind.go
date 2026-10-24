package handler

import (
	"errors"
	"log"
	"strings"

	"go-backend/internal/store/repo"
)

// Plan 152 「只开所选入口」: a rule made under a 线路分组 that covers only some
// of a tunnel's entries is bound to those entries (forward.entry_node_ids).
// forward_port rows always equal the rule's effective entries, so every
// deploy / redeploy / runtime path that reads forward_port honours the
// binding; only the paths that compute entries apply it here.

var errEntryKeyChanged = errors.New("入口已变化，请重新选择隧道")

// entryBindingFromRequest resolves the optional entryKey of a create/update
// request against the owner's allowed entries. present=false: the field was
// not sent (create → unbound, update → keep). An empty key unbinds; a key
// covering every allowed entry is stored unbound too.
func (h *Handler) entryBindingFromRequest(req map[string]interface{}, tunnelID int64, allowed []int64, actorAdmin bool) (string, bool, error) {
	raw, ok := req["entryKey"]
	if !ok || raw == nil {
		return "", false, nil
	}
	key := strings.TrimSpace(asString(raw))
	if key == "" || tunnelID <= 0 {
		return "", true, nil
	}
	ids, err := h.repo.ResolveTunnelEntryKey(tunnelID, allowed, key, actorAdmin)
	if err != nil {
		return "", true, err
	}
	if len(ids) == 0 {
		return "", true, errEntryKeyChanged
	}
	if sameInt64Set(ids, allowed) {
		return "", true, nil
	}
	return repo.FormatEntryNodeIDs(ids), true, nil
}

// applyForwardEntryBinding narrows allowed to the rule's stored binding and
// persists a pruned/cleared binding once (stale ids: the entry left the
// tunnel or the owner lost its grant). Idempotent: the next pass sees the
// stored result and neither writes nor logs again.
func (h *Handler) applyForwardEntryBinding(f *forwardRecord, allowed []int64) []int64 {
	if f == nil || strings.TrimSpace(f.EntryNodeIDs) == "" {
		return allowed
	}
	effective, kept, changed := repo.EffectiveEntryNodes(allowed, f.EntryNodeIDs)
	if !changed {
		return effective
	}
	if err := h.repo.SetForwardEntryBinding(f.ID, kept); err != nil {
		log.Printf("[entry-bind] forward=%d keep binding %q: %v", f.ID, f.EntryNodeIDs, err)
		return effective
	}
	if kept == "" {
		log.Printf("[entry-bind] forward=%d binding %q no longer allowed, cleared (all permitted entries)", f.ID, f.EntryNodeIDs)
	} else {
		log.Printf("[entry-bind] forward=%d binding %q pruned to %q", f.ID, f.EntryNodeIDs, kept)
	}
	f.EntryNodeIDs = kept
	return effective
}

// forwardEntryNodes is the rule's effective entries on tunnelID: the owner's
// allowed entries, narrowed by the binding while the rule stays on the tunnel
// it was bound on.
func (h *Handler) forwardEntryNodes(f *forwardRecord, tunnelID int64, admin ...bool) ([]int64, error) {
	allowed, err := h.allowedTunnelEntries(f.UserID, tunnelID, admin...)
	if err != nil {
		return nil, err
	}
	if f.TunnelID != tunnelID {
		return allowed, nil
	}
	return h.applyForwardEntryBinding(f, allowed), nil
}

func forwardBound(f *forwardRecord) bool {
	return f != nil && strings.TrimSpace(f.EntryNodeIDs) != ""
}

func filterForwardPortsByNode(ports []forwardPortRecord, nodeID int64) []forwardPortRecord {
	out := make([]forwardPortRecord, 0, 1)
	for _, fp := range ports {
		if fp.NodeID == nodeID {
			out = append(out, fp)
		}
	}
	return out
}
