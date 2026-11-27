package handler

import (
	"database/sql"
	"log"
	"net/url"
	"strings"

	"go-backend/internal/store/repo"
)

// Plan 321: 电信 / 联通 / IPv6 tags next to entry addresses (see
// repo.EntryLabels). Additive fields only: a list without any tagged
// address gets no field at all.

func (h *Handler) loadEntryLabels() *repo.EntryLabels {
	if h == nil || h.repo == nil {
		return nil
	}
	x, err := h.repo.LoadEntryLabels()
	if err != nil {
		log.Printf("[entry-labels] %v", err)
		return nil
	}
	return x
}

func listString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case *string:
		if s != nil {
			return *s
		}
	case sql.NullString:
		if s.Valid {
			return s.String
		}
	}
	return ""
}

// attachEntryLabels sets item[dst] = host → tag for the addresses in
// item[src] ("host:port,…" or bare hosts).
func (h *Handler) attachEntryLabels(items []map[string]interface{}, src, dst string) {
	if len(items) == 0 {
		return
	}
	x := h.loadEntryLabels()
	if x == nil {
		return
	}
	for _, item := range items {
		if labels := x.ForList(listString(item[src])); len(labels) > 0 {
			item[dst] = labels
		}
	}
}

// linkURLHost: the server host of a share link (vless:// trojan:// ss://
// anytls:// hysteria2:// tuic:// …; "" for vmess:// base64 and bad links).
func linkURLHost(link string) string {
	link = strings.TrimSpace(link)
	if link == "" || strings.HasPrefix(strings.ToLower(link), "vmess://") {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
