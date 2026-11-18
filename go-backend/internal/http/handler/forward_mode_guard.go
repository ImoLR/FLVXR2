package handler

import (
	"errors"
	"strings"
)

// Plan 282: nftables mode is kernel DNAT on every hop, so the tunnel
// 协议过滤 (SOCKS / HTTP / TLS / 屏蔽其它协议, enforced inside gost only)
// never sees that traffic. A forward may therefore be nftables only when
// the actor is an admin AND the forward's owner is an admin.
const nftAdminOnlyMsg = "NFtables 模式仅管理员自己的转发可用；普通用户转发只能用 Gost 模式（协议过滤只在 Gost 模式生效）"

var errNftAdminOnly = errors.New(nftAdminOnlyMsg)

func isNftablesMode(mode string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), "nftables")
}

// ensureForwardModeAllowed is the single gate for every path that sets or
// keeps a forward's mode. Non-nftables modes pass (wg_path keeps its own
// admin-only checks at the call sites).
func (h *Handler) ensureForwardModeAllowed(mode string, actorRole int, ownerID int64) error {
	if !isNftablesMode(mode) {
		return nil
	}
	if actorRole != 0 || ownerID <= 0 || h == nil || h.repo == nil {
		return errNftAdminOnly
	}
	role, err := h.repo.GetUserRoleID(ownerID)
	if err != nil || role != 0 {
		return errNftAdminOnly
	}
	return nil
}

// normalizeForwardImportModes rewrites imported forward rows whose owner is
// not an admin from nftables to gost (backup restore stays possible; the
// rule just loses kernel mode). The owner's role comes from the backup's own
// user rows when they are imported too, else from the current DB. Returns
// the names of the converted rules.
func (h *Handler) normalizeForwardImportModes(raw map[string]interface{}, types []string) []string {
	if !stringSliceContains(types, "forward") {
		return nil
	}
	items, ok := raw["forward"].([]interface{})
	if !ok || len(items) == 0 {
		return nil
	}
	importedRoles := map[int64]int64{}
	if stringSliceContains(types, "user") {
		if users, ok := raw["user"].([]interface{}); ok {
			for _, item := range users {
				if row, ok := item.(map[string]interface{}); ok {
					if id := asInt64(row["id"], 0); id > 0 {
						importedRoles[id] = asInt64(row["role_id"], asInt64(row["roleId"], 1))
					}
				}
			}
		}
	}
	var converted []string
	for _, item := range items {
		row, ok := item.(map[string]interface{})
		if !ok || !isNftablesMode(asString(row["mode"])) {
			continue
		}
		owner := asInt64(row["user_id"], asInt64(row["userId"], 0))
		admin := false
		if role, ok := importedRoles[owner]; ok {
			admin = role == 0
		} else if owner > 0 && h != nil && h.repo != nil {
			if role, err := h.repo.GetUserRoleID(owner); err == nil && role == 0 {
				admin = true
			}
		}
		if !admin {
			row["mode"] = "gost"
			converted = append(converted, asString(row["name"]))
		}
	}
	return converted
}
