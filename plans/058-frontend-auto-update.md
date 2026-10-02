# 058 Frontend auto-update for stale tabs (3.0.27-fork.12)

## Problem
The panel is a PWA (`vite-plugin-pwa`, `registerType: "autoUpdate"`, `skipWaiting`,
`clientsClaim`). On a fresh page load the browser re-checks `sw.js`, the new worker
takes over and the page reloads into the new release — that part already works.

Gaps:
1. Long-lived tabs never navigate (SPA routing), so the browser never re-checks
   `sw.js` and the tab stays on the old release until a manual reload.
2. `nginx.conf` matched `sw.js` with the `\.(js|...)$` rule and served it with
   `Cache-Control: public, immutable` + 1y expiry; `index.html` had no Cache-Control.
   Browsers bypass HTTP cache for the SW script by default, but a CDN/proxy in front
   could pin `sw.js` for a year.

## Fix
- `vite-frontend/src/main.tsx`: in `onRegisteredSW`, call `registration.update()`
  every 30 min while the tab is hidden, and when the tab becomes visible again
  (at most once per 5 min). The autoUpdate reload therefore happens in the
  background or right as the user returns, not mid-form.
- `vite-frontend/nginx.conf`: `Cache-Control: no-cache` for `/sw.js`, `/index.html`
  (also covers SPA fallback routes) and `/manifest.webmanifest`. Hashed `assets/*`
  keep the 1y immutable cache.

## Checklist
- [x] main.tsx periodic / visibility update check + `onRegisteredSW` typing
- [x] nginx no-cache for entry files; `nginx -t` OK
- [x] `tsc --noEmit`, eslint, `npm run build` OK
- [x] Header check against built dist in nginx:stable-alpine (`/`, `/index.html`,
      `/sw.js`, `/manifest.webmanifest`, `/dashboard` → no-cache; `assets/*.js` → immutable)
- [ ] Rebase on final fork.11 branch head, push branch, tag `3.0.27-fork.12`, CI + release verified
- [ ] Backup production (rollback dir, validated sqlite, image tags, metadata)
- [ ] Upgrade `/opt/flvx-svc` to fork.12
- [ ] Verify health, node metrics, live `/sw.js` + `/` headers are `no-cache`
- [ ] Update memory, write report
