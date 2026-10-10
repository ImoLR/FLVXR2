# 088 — Stale frontend kick + build version check (fork.39)

## Problem
On 2026-10-09 one Android Chrome was stuck on a pre-fork.16 frontend: its old
service worker never fetched `/sw.js` from origin, kept serving the old
precached `index.html`, and its JS still called `/api/v1/license/info`
(removed in fork.16, commit 37089dc4). Nothing on the server could make it
update; the only fix was "clear site data" by hand.

## Design (approved by the user)

### Layer 1 — backend kill switch for already-stuck clients
- `/api/v1/license/info` (any method) is registered again in go-backend and
  skipped by the JWT middleware (works with no / invalid / valid token).
- Response: HTTP **401**, `Clear-Site-Data: "storage"`, `Cache-Control: no-store`,
  envelope `{code:401,msg:"未登录或token已过期",ts}`. Constant-time, no DB access.
- Only `"storage"` (not `"cache"`: Chrome's HTTP-cache clearing can be very slow
  and assets are hash-named). `"storage"` unregisters the SW and clears
  CacheStorage + localStorage → the user is logged out once (intended).
- Old frontend (verified from `git show 3.0.27-fork.15:vite-frontend/src/...`):
  `getLicenseInfo = () => Network.post("/license/info")` — the *authed* `post`
  path (not `getPublic`). It is called from `layouts/admin.tsx` and
  `layouts/h5.tsx` on mount + every 5 min (and `pages/config.tsx`), i.e. only on
  logged-in pages. `post().catch` → `isUnauthorizedError` (HTTP 401) →
  `handleTokenExpired()` → `clearSession()` + `window.location.href = "/"` if
  `pathname !== "/"`. (The HTTP-200 path `isTokenExpired(code 401 + this msg)`
  would trigger the same function.)
- Expected flow on an old client: 401 arrives (Chrome holds the response until
  the storage is cleared) → SW unregistered + CacheStorage/localStorage gone →
  old JS navigates to "/" → no SW intercepts → nginx serves the fresh
  `index.html` → new assets + new SW → new login page.
- **No-loop conclusion (browser without Clear-Site-Data support):** the old
  frontend only calls `/license/info` from the logged-in layouts (`/dashboard`
  etc.); `/` renders `LoginRoute` (no admin/H5 layout → no license call), and
  `handleTokenExpired` does not navigate when `pathname === "/"`. So the chain
  stops after exactly one navigation to `/`: no automatic redirect loop. Such a
  browser stays on the old (SW-cached) frontend and is logged out each time the
  user logs in again and reaches a layout page — degraded but not looping; the
  manual fix ("clear site data") still applies. Clear-Site-Data `"storage"` is
  supported by Chrome/Android WebView 61+, Firefox 63+, Safari/iOS 17+ (MDN BCD),
  so this only affects very old browsers.
- The current frontend never calls `/license/info` (grep-verified), so current
  clients are not affected.

### Layer 2 — build version check in the new frontend
- One build id per build (`<VITE_APP_VERSION|local>-<base36 timestamp>`) is
  injected via Vite `define` (`__APP_BUILD_ID__`) and emitted as
  `dist/version.json` = `{"build":"<id>"}`; version.json is excluded from the
  workbox precache (`globIgnores`).
- nginx: `location = /version.json` with `Cache-Control: no-store` (exact match,
  no SPA fallback → a missing file is a 404, not index.html).
- Fetch: `GET /version.json?t=<now>` with `cache: "no-store"`, 10 s abort
  timeout; anything but `200` + JSON `{build: string}` is ignored silently.
- Triggers (no new timers, nothing while the page is visible):
  - once at app start;
  - after a successful login: login/register/default-password flows all do a
    full navigation (`window.location.href = "/dashboard"` / `/change-password`),
    so the post-login check is the app-start check of that page (no extra
    request that the navigation would abort anyway);
  - `visibilitychange` → visible, sharing the existing 5-min min gap of
    `watchForSWUpdates` (merged: one gap, one listener; the SW `update()` and
    the version check run together);
  - `pageshow` with `persisted` (bfcache), same gap;
  - skipped when `navigator.onLine === false`, in dev mode, or without `fetch`.
  The existing 30-min hidden-tab SW update interval is unchanged (SW only).
- At most one check in flight per page (module-level promise).
- On mismatch: reload guard first, then `registration.update()` (5 s cap),
  unregister all SW registrations, delete all CacheStorage entries,
  `location.reload()`. The login token is kept (refresh, not logout).
- **Reload-loop guard:** sessionStorage key `flvx-build-reload` =
  `{target, at}`; a forced reload for target build T is allowed only if no
  reload for T happened in the last 10 min in this tab. The guard is written
  and read back *before* reloading; if sessionStorage is unavailable the client
  does not force-reload at all. After the guarded reload, a still-mismatching
  build stops silently (the app-start check right after the reload is inside
  the 10-min window). No retries, no backoff, no timers; a later reload for the
  same T needs a user-driven trigger ≥10 min later.
- Feature detection everywhere (`navigator.serviceWorker`, `caches`,
  `sessionStorage`, `AbortController`), errors swallowed. H5/WebView: the
  check is same-origin; a WebView-bundled frontend compares against its own
  version.json (match or 404 → nothing happens).

## Efficiency
- Per page: 1 tiny static GET at start + at most 1 per visible transition
  (≥5 min apart) + bfcache restores (same gap). Served by nginx, never the Go
  backend. No WebSocket/interval/long-lived connection, nothing per API request.
- Layer 1 handler: constant-time, no DB, no auth parsing.

## Tasks
- [x] Backend: `/api/v1/license/info` kill-switch handler + JWT skip
- [x] Backend contract test (status 401, `Clear-Site-Data: "storage"`, envelope; no/invalid/valid token; GET + POST)
- [x] Frontend: build id define + `version.json` emit + precache exclusion
- [x] Frontend: `build-freshness` module merged with SW update watcher in `main.tsx`
- [x] nginx: `location = /version.json` no-store
- [x] `go test ./...` (compare failures with fork.38 base), `npm run build` + `npm run lint`
- [x] Playwright: (a) old build id → exactly one reload; persistent mismatch → at most one; (b) 2-min idle visible tab → no extra requests; (c) `/api/v1/license/info` → 401 + header; old-frontend simulation
- [x] Push branch, CI green (CI Build Check run 38036008219)
- [x] Tag `3.0.27-fork.39`, Build and Push Images green (run 38036698569), release assets verified
- [x] Prod backup (rollback dir + image tags), prune to 2 newest
- [x] Upgrade /opt/flvx-svc to fork.39, verify health, nodes reporting
- [x] Prod curl: `/version.json` JSON + no-store; `/api/v1/license/info` 401 + `Clear-Site-Data`
- [x] Plan marked complete + pushed

## Verification notes
- `go test ./...`: 15 failing tests/subtests on the branch = exactly the 15 on the
  unmodified fork.38 base (federation dual panel ×3, backup export, legacy
  migrations ×2, renewal TZ, service monitor, address-in-use, connect-IP …;
  lists in the run dir `base.fails` / `new.fails`). `TestRemovedCommercialRoutes`
  asserted 404 for `/license/info`; it now covers only `/license/config` and
  `/license/transfer` (the kill switch has its own test).
- New contract tests: `TestStaleFrontendKickFullRouter` (full router incl. JWT
  middleware; POST/GET/PUT × no token / junk / foreign-secret / admin / user /
  session cookie → 401, `Clear-Site-Data: "storage"`, `no-store`, envelope
  code 401 + old-frontend message) and
  `TestCurrentFrontendNeverCallsStaleKickEndpoint` (no `license/info` in
  vite-frontend/src).
- `npm run build` ok (main JS 2.75 MB, precache 9 entries, version.json not
  precached), `tsc --noEmit` ok, `eslint src` totals identical to fork.38
  (3679 problems / 96 errors, all pre-existing).
- Playwright (chromium 1243, local paneld :16365 + programmable static server
  mimicking nginx.conf; scripts/results in
  /root/flvx-workers/runs/stale-client-kick/e2e/):
  - (a) no SW: running X, deploy Y, visible <5 min → no check; after 5 min →
    exactly 1 reload onto Y, stable, 3 version.json GETs total, `?t=` buster.
    Persistent mismatch (bundle X, version.json Y): exactly 1 reload, no loop
    over 14 s, visible at +6 min (inside the 10-min window) → no reload, at +13
    min → 1 more reload then stable. version.json HTML / 404 / connection reset /
    malformed → no reload, no page error. Offline → no request. 20 visible + 20
    bfcache `pageshow` events in the gap → no extra request. 19/19 pass.
  - with SW, logged in: deploy Y → tab on Y after 1 reload, still logged in on
    /dashboard, new SW registered again, stable.
  - Old client: real fork.15 build logged in with its SW, then new frontend
    deployed with a failing sw.js update (stuck, still fork.15 bundle). With the
    kill switch: 1 × `license/info` 401 + `Clear-Site-Data: "storage"` → SW
    unregistered, CacheStorage empty, localStorage wiped, lands on the NEW
    bundle's login page at `/`, no further navigations; relogin works and the
    new frontend never calls `license/info`. Header stripped (browser without
    Clear-Site-Data): old JS logs out to `/` once and stops (no loop), stays on
    the old SW bundle (expected degrade). 16/16 pass.
  - (b) idle visible logged-in dashboard for 120 s, desktop 1366×900 and mobile
    390×844: new build 48 requests = fork.38 build 48 requests (both only the
    dashboard's existing `user/package` + `node/list` polling); 0 version.json,
    0 sw.js.
  - (c) the real `nginx.conf` in the fork.38 nginx image (listen/upstream
    rewritten for the test): `/version.json` 200 `application/json` +
    `Cache-Control: no-store`; missing file → 404 (not index.html);
    `/api/v1/license/info` → 401 + `Clear-Site-Data: "storage"` passed through;
    index/sw.js still `no-cache`, assets immutable.

## Release / deploy notes
- Tag `3.0.27-fork.39` on 61d78f2f (branch `maintenance/3.0.27-fork.39-stale-client-kick`);
  release Latest, not prerelease, same 10 assets as fork.38, compose images
  `ghcr.io/imolr/flvxr2-svc-*:3.0.27-fork.39`, `PINNED_VERSION`/`REPO=ImoLR/FLVXR2` in both
  scripts, gost sha256 OK; v6 compose identical to fork.38's apart from the version.
- Rollback point `/opt/flvx-svc/rollback/pre-fork39-20261010T080623Z` (compose + .env,
  online DB backup quick_check ok, local tags `local/flvxx-{backend,frontend}:pre-fork39-20261010T080623Z`
  = fork.38 images). Prune removed pre-fork37 + fork.36 images. No schema change.
- Panel upgraded 2026-10-10 08:23:53Z. Backend healthy; 26/26 reporting nodes sent
  metrics <15 s after restart (node 24 long-offline as before); the 3 startup
  `SetQuotaGroups: 节点不在线` lines are the usual reconnect race (retried after the interval).
- Prod checks through Cloudflare (`https://flvxx.imgamer.top`): `/version.json` → 200
  `application/json`, `cache-control: no-store`, `cf-cache-status: DYNAMIC`,
  `{"build":"3.0.27-fork.39-mv2445vv"}` = the id inside the served
  `index-EX9EPoq-.js`; `POST`/`GET` `/api/v1/license/info` (no token / junk token) →
  401, `clear-site-data: "storage"`, envelope `{code:401,msg:"未登录或token已过期"}`.
