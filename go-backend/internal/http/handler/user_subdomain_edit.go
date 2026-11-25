package handler

import (
	"log"
	"net/http"
)

// Plan 307 (user 11-25): a forward / native landing node kept on its old
// address (已有转发保持原地址) moves to the new design as soon as its edit
// dialog is saved — by the user or an admin: the save drops its keep rows,
// the kicker that runs after the response reserves the owner's 专属域名, and
// the address shows that name (「正在就绪」 until ready). Pause / resume,
// redeploy, batch ops, traffic resets, expiry / limit jobs and tunnel or
// node edits never come through here, so they keep the old address.

// userSubDropKeep drops the keep rows of an edited forward / landing node.
func (h *Handler) userSubDropKeep(kind string, refID int64) {
	if refID <= 0 || h.repo == nil {
		return
	}
	if n, err := h.repo.DropUserSubdomainKeep(kind, refID); err != nil {
		log.Printf("[user-subdomain] %s %d edited: drop 保持原地址: %v", kind, refID, err)
	} else if n > 0 {
		log.Printf("[user-subdomain] %s %d edited: no longer kept on its old address (%d nodes)", kind, refID, n)
	}
}

// userSubEditSave runs an edit handler into a buffer and drops the keep rows
// of kind/refID when it succeeded (code 0), then writes the response.
func (h *Handler) userSubEditSave(w http.ResponseWriter, kind string, refID int64, run func(w http.ResponseWriter)) {
	cw := &captureWriter{}
	run(cw)
	if code, _, _ := cw.envelope(); code == 0 {
		h.userSubDropKeep(kind, refID)
	}
	cw.flushTo(w)
}

// landingNodeEditFields: a landing/node/update that is the edit dialog (any
// field besides id / status); the 停用 / 启用 switch sends only the status.
func landingNodeEditFields(keys []string) bool {
	for _, k := range keys {
		if k != "id" && k != "status" {
			return true
		}
	}
	return false
}
