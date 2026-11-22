package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"go-backend/internal/store/model"
)

// Plan 294 (im.gamer App): 复制链接 and the 利群汇聚 subscription can point at
// another 入口机 than the owner's 主入口 — a front machine of the 「利群入口」
// group, or 手机本地 (127.0.0.1 = the LeiKwan client on the device itself,
// same vendor port, works on any device that runs one). Nothing is stored:
// the choice rides on the request (link) or the subscription URL (?lk=).

const (
	lkLocalHost = "127.0.0.1"
	lkLocalName = "手机本地"
	// lkSubEntryQuery is the subscription URL parameter: "local" or a node id.
	lkSubEntryQuery = "lk"
)

// lkEntryPick is a requested entry: Local, or NodeID > 0 (zero value = the
// owner's 主入口, the plan-272 behaviour).
type lkEntryPick struct {
	Local  bool
	NodeID int64
}

func (p lkEntryPick) set() bool { return p.Local || p.NodeID > 0 }

// key is the pick's part of a subscription render-cache key.
func (p lkEntryPick) key() string {
	switch {
	case p.Local:
		return "local"
	case p.NodeID > 0:
		return strconv.FormatInt(p.NodeID, 10)
	}
	return ""
}

// parseLKEntry reads ?lk= ("local" | node id); anything else = no pick.
func parseLKEntry(v string) lkEntryPick {
	v = strings.TrimSpace(v)
	if v == "local" {
		return lkEntryPick{Local: true}
	}
	if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
		return lkEntryPick{NodeID: id}
	}
	return lkEntryPick{}
}

var (
	errLKEntryNotInGroup = errors.New("这个入口机不在「利群入口」分组里")
	errLKEntryNotOwn     = errors.New("只能用自己的主入口或手机本地")
)

// leiKwanPickEntry resolves a pick for the rules of owner `userID`. Admins may
// pick any node of the 利群入口 group; users only 手机本地 or their own 主入口
// (they never see the other entry machines' addresses).
func (h *Handler) leiKwanPickEntry(userID int64, admin bool, p lkEntryPick) (*lkEntry, error) {
	if p.Local {
		return &lkEntry{Host: lkLocalHost, Name: lkLocalName}, nil
	}
	if p.NodeID <= 0 {
		return h.leiKwanUserEntry(userID), nil
	}
	s, err := h.repo.GetLeiKwanSetting()
	if err != nil || s == nil {
		return nil, errors.New("读取利群设置失败")
	}
	e := leiKwanEntryByID(h.leiKwanEntryNodes(s.EntryGroupID), p.NodeID)
	if e == nil || strings.TrimSpace(e.Host) == "" {
		return nil, errLKEntryNotInGroup
	}
	if !admin {
		own := h.leiKwanUserEntry(userID)
		if own == nil || own.NodeID != e.ID {
			return nil, errLKEntryNotOwn
		}
	}
	return &lkEntry{NodeID: e.ID, Host: strings.Trim(strings.TrimSpace(e.Host), "[]"), Name: e.Name}, nil
}

// leiKwanPickLocked: the rule cannot be used on the picked front machine
// because of 「只开主入口」 (手机本地 is never locked: the hosts' nft only).
func (h *Handler) leiKwanPickLocked(x model.LeiKwanRule, e *lkEntry) bool {
	if e == nil || e.NodeID <= 0 {
		return false
	}
	g, err := h.newLKGen()
	if err != nil || g == nil {
		return false
	}
	return g.entryLocked(x, e.NodeID)
}

type lkEntryCtxKey struct{}

// withLKEntry carries a subscription's ?lk= entry into subLoad.
func withLKEntry(ctx context.Context, e *lkEntry) context.Context {
	if e == nil {
		return ctx
	}
	return context.WithValue(ctx, lkEntryCtxKey{}, e)
}

func lkEntryFrom(ctx context.Context) *lkEntry {
	if ctx == nil {
		return nil
	}
	e, _ := ctx.Value(lkEntryCtxKey{}).(*lkEntry)
	return e
}
