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
- [x] 2. Models `user_subscription` / `user_sub_source` (+ AutoMigrate, user-delete cascade)
- [x] 3. `internal/sublink`: node model, UA table, Clash / sing-box / Base64 generators, dedupe names
- [x] 4. landingsub: lenient (allow-insecure) parse option for sources
- [x] 5. Repository: CRUD, caps, batch pool / resolve (no N+1), last-pull update
- [x] 6. Handlers: `/api/v1/sub/*` CRUD + pool + sources (fetch/create/refresh/delete), encryption of source URLs
- [x] 7. Public `GET /s/{token}`: switch, token/IP limits, browser page (QR), formats, headers, 60 s LRU cache, lazy source refresh (single-flight), no logs
- [x] 8. Admin switch `sub_link_enabled` (default on) in 设置; nginx.conf `/s/` (no access log) + vite dev proxy
- [x] 9. Backend unit tests (UA, generators, resolve, refresh, caps, reset, rate limit)
- [x] 10. Contract tests SQLite + PostgreSQL (CRUD, ownership, /s 200/404/403, headers, HTML no secrets)
- [x] 11. Real client checks: mihomo `-t` and `sing-box check` on generated configs
- [x] 12. Frontend: menu 订阅 (+新), list page, editor (desktop two columns, H5 tabs), sources dialog, confirms
- [x] 13. Gates: go build/vet/test, frontend tsc/build/lint vs base
- [x] 14. Demo panel + screens gallery :8791 /sub-link/ (1440 + 390, light + dark) + text samples
- [x] 15. Resource numbers (render 50 / 300 nodes cold+cached, cache memory, DB queries/pull, bundle delta)
- [x] 16. Push, CI green, `runs/sub-link/release-notes.md`, summary

## Results (build round 1, 2026-11-01)

- Screens: http://104.145.236.29:8791/sub-link/ (50 shots, 1440 + 390, light + dark; 33/33 checks, 0 page errors).
  Demo = synthetic SQLite panel in a loopback-only netns; sources really fetched from a local fake airport
  (demo-only build tag maps two `.example` hosts to 127.0.0.1; not committed, prod SSRF guard unchanged).
- Real clients: mihomo v1.19.33 `-t` and sing-box 1.14.4 `check` accept the generated configs (unit samples, both
  rule modes, and the demo panel's real output).
- Backend: `go build` / `go vet` (5 pre-existing leikwan/testapi findings, same on base); full `go test ./...` with
  PostgreSQL: 20/21 packages ok, only `TestExternalNodesBothDialects` fails — identical on base a50e3896 (hard-coded
  31 Oct 2026 timestamp → its users now count as expired; pre-existing, not touched).
- Frontend: tsc clean; eslint 0 errors (new files clean, touched files same warning count as base); build ok.
- Resources (demo, GOMAXPROCS=2): 50 nodes cold 2.5–16 ms / cached ≈1 ms; 300 nodes cold 5–84 ms / cached 1–2 ms;
  DB statements per pull 14 cold / 3 cached; cache fill 270 × 300-node renders → RSS 55 → 86 MB (cache capped 256
  entries / 16 MiB), 270 × 50-node → no RSS change; 0 log lines per pull; bundle main chunk +31.6 kB (+7.9 kB gzip).
- CI green on 903ec836 (run 41304626548). Release notes: /root/flvx-workers/runs/sub-link/release-notes.md. Waiting for the user's screen approval; release in a batch.
