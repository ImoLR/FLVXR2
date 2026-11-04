# 209 — frontend-speed: faster login → 主页, first open of 规则/隧道, no blank page after a release

Base: tag `3.0.27-fork.114` (ce8cd5fa). Branch `feature/3.0.27-frontend-speed`, worktree
`/root/projects/flvxx/flvxx-frontend-speed`. Run dir `/root/flvx-workers/runs/frontend-speed`.
**BUILD ONLY** — no release, no prod change, no backend / agent / DB change, no OTA.

## Why
Prod log (11-04 22:55 TW): backend answers in 6–52 ms, but 7 s pass between `user/login` 200 and the first 主页 API
call → client side (download + parse of a 3.3 MB raw / ~820 KB gzip bundle; `treeshake: false`; every page is eagerly
imported in `App.tsx`; no modulepreload; login does a full navigation). After a release the freshness check nukes SW +
CacheStorage and reloads once, so the first visit downloads everything again while the new SW precaches it too.

## Checklist
### Analysis
- [x] Base build (fork.114) with sourcemaps in run dir; bundle composition top-15 (admin-main, egress-rule-list,
      useTapTooltip, jsx-runtime) → final.md
### A. Smaller / earlier JS
- [x] A2 tree-shaking on; fix side-effect imports / CSS order / bridge; mark real side effects only
- [x] A3 route-level lazy pages + heavy libs (charts, markdown, dnd, turnstile, motion …) only in pages that use them;
      lucide per-icon (no whole-set barrel)
- [x] A4 idle prefetch: login idle → 主页 chunks; 主页 rendered → 规则 / 隧道 / 落地 chunks (once per session; skip on
      saveData / 2G; never blocks)
- [x] A5 login → 主页 in-app navigation (only if freshness check still runs explicitly and session handling is identical)
### B. After a release
- [x] B1 measure bytes of "release N → N+1" today; make it one download (no needless CacheStorage nuke when the SW is
      not serving a stale index; keep kill switch, renamed SW, no Clear-Site-Data, 10-min reload guard, token kept)
- [x] B2 no white page while reloading / loading chunks (static splash in index.html, same look as login background)
- [x] Extra (found while measuring): build id moved from the JS into index.html `<meta name="flvx-build">` + Vite
      preload helper in its own chunk → a release changes only the chunks whose code changed (规则-only release:
      3 small chunks instead of the 1.6 MB admin-main); SW precaches only the app shell (no PWA icons, no page
      chunks); first render waits for the current page chunk (no suspend/retry round); navigation preload
- [x] Chunk-load failure after a release (old tab, files gone) → version check → reload onto the new build;
      otherwise retry once, then one guarded reload
### Measure + UI parity
- [x] Demo panel (synthetic data ≈ prod shape: 42 rules, 48 tunnels, 29 nodes, admin + user), Playwright Slow 4G + 4× CPU
      and unthrottled: cold login, login → 主页, first 规则 / 隧道, warm reload, release N → N+1, JS bytes per page
- [x] Screens base vs branch (admin + user, 1440 + 390, light + dark), pixel diff, gallery `/root/mmwx-preview/frontend-speed/`
- [x] Click-through (login, 主页, 规则 expand/popups/复制链接, 隧道, 节点, 落地 tabs, 用户, 设置, 探针, H5); console clean
### Gates + hand-off
- [x] tsc / build / lint (no new errors vs base); go build still passes (backend untouched)
- [x] Push branch, CI green
- [x] `runs/frontend-speed/release-notes.md` + `summary.md`; clean demo panels / browsers

## Results (details: /root/flvx-workers/runs/frontend-speed/final.md)
- JS gzip: login page 826 → 213 KB (−74 %), login + 主页 826 → 377 KB (−54 %), all JS 851 → 742 KB.
- Slow 4G + 4× CPU, medians of 3: cold login page 5961 → 2920 ms; click 登录 → 主页 data 1904 → 1293 ms (first
  visit), 1169 → 988 ms (returning); after a release 4959 → 1298 ms, 1426 → 74 KB, no old build, 1 page load.
  Returning user's login page +121 ms (network-first page load, by design). 规则 first open noisy (1104–1611 vs
  1126–1532 ms).
- UI: 144/144 scenes pixel-identical (admin + user, 1440 + 390, light + dark); no new console errors/warnings.
- Checks: Data Saver skips prefetch; old tab after a release reloads once onto the new build when a page file is
  gone; login across a release lands on the new build with the session kept; /s/ links reach the server.
- Gates: tsc / build OK, eslint 94 errors = base (no new), go build OK (backend untouched), CI 41869243697 green.
- Build only — not released.

