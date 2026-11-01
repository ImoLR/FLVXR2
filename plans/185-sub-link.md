# 185 — 用户订阅链接（sub-link）

Build only (no tag / release / prod writes). Base = `3.0.27-fork.104` (branch
`maintenance/3.0.27-fork.104-socks-strict`, a50e3896). Branch
`feature/3.0.27-sub-link`, worktree `/root/projects/flvxx/flvxx-sub-link`.
Design approved by the user 2026-11-01 (draft :8791 /sub-link-design/, all five
recommended decisions). Keep it as simple as the draft or simpler.

## Decisions taken while building (simpler variants)

- Plain port-forward rules are not listed in the picker; the 规则 group shows one
  grey line 「另有 N 条普通端口转发规则，不能放进订阅」.
- No latency tag in the picker (not available client-side without a new request).
- No 「清理失效」 button: missing references show red in the editor and are dropped on save.
- 「新」 badge = localStorage seen-flag, cleared on the first visit of 订阅.
- Expired / disabled owners: `/s/<token>` answers 403 (nothing served).
- UA mapping: Clash/mihomo/Stash/Egern → Clash YAML; sing-box/SFA/SFI/SFM/SFT/Hiddify →
  sing-box; Shadowrocket/v2rayN/v2rayNG/V2Box/Loon/QX → Base64; Surge/Surfboard → Base64
  (they cannot import it — Surge format is a later phase); unknown → Clash YAML;
  browsers (Mozilla, no client marker, no `?t=`) → small HTML page.
- External sources keep airport nodes that use self-signed certificates
  (`skip-cert-verify` / `allowInsecure`): the client connects, not FLVX, so they are passed
  through with the flag (landing 链式代理 import is unchanged and still refuses them).

## Tasks

- [x] 1. Plan doc + branch/worktree, WIP push
- [ ] 2. Models `user_subscription` / `user_sub_source` (+ AutoMigrate, user-delete cascade)
- [ ] 3. `internal/sublink`: node model, UA table, Clash / sing-box / Base64 generators, dedupe names
- [ ] 4. landingsub: lenient (allow-insecure) parse option for sources
- [ ] 5. Repository: CRUD, caps, batch pool / resolve (no N+1), last-pull update
- [ ] 6. Handlers: `/api/v1/sub/*` CRUD + pool + sources (fetch/create/refresh/delete), encryption of source URLs
- [ ] 7. Public `GET /s/{token}`: switch, token/IP limits, browser page (QR), formats, headers, 60 s LRU cache, lazy source refresh (single-flight), no logs
- [ ] 8. Admin switch `sub_link_enabled` (default on) in 设置; nginx.conf `/s/` (no access log) + vite dev proxy
- [ ] 9. Backend unit tests (UA, generators, resolve, refresh, caps, reset, rate limit)
- [ ] 10. Contract tests SQLite + PostgreSQL (CRUD, ownership, /s 200/404/403, headers, HTML no secrets)
- [ ] 11. Real client checks: mihomo `-t` and `sing-box check` on generated configs
- [ ] 12. Frontend: menu 订阅 (+新), list page, editor (desktop two columns, H5 tabs), sources dialog, confirms
- [ ] 13. Gates: go build/vet/test, frontend tsc/build/lint vs base
- [ ] 14. Demo panel + screens gallery :8791 /sub-link/ (1440 + 390, light + dark) + text samples
- [ ] 15. Resource numbers (render 50 / 300 nodes cold+cached, cache memory, DB queries/pull, bundle delta)
- [ ] 16. Push, CI green, `runs/sub-link/release-notes.md`, summary
