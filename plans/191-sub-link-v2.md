# 191 — 订阅链接第二期：Surge / Surfboard / Loon / Quantumult X 原生格式（sub-link-v2）

Build only (no tag / release / prod writes / OTA). Base = `3.0.27-fork.106` (tag → 926bd86d).
Branch `feature/3.0.27-sub-link-v2`, worktree `/root/projects/flvxx/flvxx-sub-link-v2`.
Run dir `/root/flvx-workers/runs/sub-link-v2`. User approval 2026-11-02 「开始做吧」 (phase 2 of plan 185).

Scope: backend generators + UA table + `?t=` values + small text changes only. No new settings,
pages, per-node options, tables or columns. The editor's saved format stays auto|clash|singbox|base64.
Panel only, no agent change, no OTA, no DB change, no new goroutines/timers, same render cache.

## Tasks

- [x] 1. Plan doc + branch/worktree, WIP push
- [ ] 2. Official docs read (Surge manual, Surfboard docs, Loon docs, QX sample) → support matrix below
- [ ] 3. Golden test: Clash / sing-box / Base64 output byte-identical to fork.106 (captured before any change)
- [ ] 4. Generators: `surge.go` (Surge + Surfboard flag), `loon.go`, `qx.go`; name escaping; empty results valid
- [ ] 5. UA table (Surge iOS/Mac, Surfboard, Loon, QX) + comment; `?t=` surge/surfboard/loon/qx
- [ ] 6. Public handler: formats, managed-config line built from the request (cache stays per token+format), headers, Content-Type
- [ ] 7. Text: browser HTML page client list + 订阅 page QR hint (no layout change)
- [ ] 8. Unit tests (protocol × client, skip, empty, names, rule modes, managed line, real UA strings) + handler/contract tests
- [ ] 9. Sub-Store parser round-trip oracle (test only, deleted afterwards)
- [ ] 10. Demo panel: curl with each UA, samples; render timing 300 nodes (Surge / QX cold + cached vs Clash)
- [ ] 11. Gates: go build / vet / test (SQLite + PostgreSQL contract), frontend tsc / build / lint vs base; push; CI green
- [ ] 12. Gallery :8791 /sub-link-v2/ + `runs/sub-link-v2/release-notes.md` + summary

## Support matrix

(filled in by task 2)

## Notes

