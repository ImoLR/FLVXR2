# 188 — 监控页去卡顿

Build only, same branch as plan 187 (`feature/3.0.27-probe-page`), released together later. Behaviour and look stay the
same; only the lag goes. Base fork.105.

## Findings to verify and fix
1. Node history auto-fetch every 15 s even with no detail open; ~1 h of raw 1 s rows per fetch.
   → fetch only while the detail is open; request downsampled points (≈ container width).
2. recharts animation replays on every refresh → `isAnimationActive={false}` everywhere on the monitor page.
3. Every WS message → several setters and new objects even when unchanged; unmemoized cards/rows → whole view
   re-renders many times per second. → skip no-op updates, batch WS updates (flush ≤ 1×/s), memo cards/rows.
4. Admin service monitors poll 2 requests every 5 s on any tab + full `monitorResults` rebuild → only while visible /
   needed; merge without replacing unchanged entries.
5. 30 s node list poll replaces `nodeMap` → merge, keep identity of unchanged nodes.
6. Hidden tabs keep polling → pause while `document.hidden`.
7. gzip for JSON: nginx already has `gzip on` + `application/json` + `gzip_proxied any` (verify); probe endpoints serve
   pre-gzipped bytes themselves.

## Tasks
- [x] Verify each finding against fork.105 code (file:line)
- [x] Measure before (renders/s, bytes/min with no detail open, long tasks) on the demo panel (~25 nodes)
- [x] Fix 1–6 (backend param not needed: tail append + client thinning)
- [x] Measure after; screenshot shows the page unchanged
- [x] tsc / build / lint clean vs base

## Results (2026-11-02 TW)

### Findings verified on fork.105 (all 7 true)
1. `monitor-view.tsx` auto-selected the first online node (`selectedNodeId`) and fetched its 1 h history on load and
   every 15 s, also with no detail open and after 「返回列表」 (~3 600 raw rows, ~550 KB per fetch on the demo).
2. No `isAnimationActive` on any monitor `<Line>` / `<Tooltip>` (node, service-latency, tunnel charts).
3. Each WS metric → up to 3 setters with new objects; `nodes` rebuilt with new objects; `ServerCard` unmemoized with an
   inline handler; list rows inline JSX.
4. Admin service monitors: 2 requests every 5 s on every tab, `setServiceMonitors` / `setMonitorResults` always new.
5. `monitor.tsx` 30 s `/monitor/nodes` poll replaced the list and rebuilt every node object.
6. Node list 30 s, service 5 s, history 15 s, tunnel list 60 s, tunnel detail 30 s: none paused when hidden (only the
   落地 view did).
7. Backend: no gzip in Go; nginx already `gzip on` + `application/json` + `gzip_proxied any` → nothing to add; the probe
   endpoints send pre-gzipped bytes themselves.
Extra bug: the card / row 👁 button called stub functions that threw 「Function not implemented」 on every click.

### Fixes
History only while a detail is open; ≤ 1 h ranges refresh by appending rows after the last one (15 s), longer ranges
reload every 60 s; charts draw at most 1 000 points (largest-triangle-three-buckets on the shown series); realtime
updates merged once per second, unchanged values keep their objects; stable node objects; `ServerCard` and the new
`NodeTableRow` memoized with a stable `onOpen`; service monitors / tunnel list / node list poll only while their tab is
shown and the browser tab is visible (catch up on return); unchanged responses keep identity; all monitor chart
animations off; 👁 button fixed. Look unchanged (screens ⑦ in the gallery).

### Numbers (demo, 25 nodes / 22 live, admin, 1440 px, list view = default, 60 s each, Chromium headless)
| | list before | list after | detail before | detail after |
|---|---|---|---|---|
| React commits / s | 1.92 | 1.18 | 6.95 | 1.33 |
| main thread busy ms / s | 72 | 53 | 55 | 15 |
| script ms / s | 35 | 16 | 31 | 8 |
| long tasks / 60 s (total ms) | 5 (482) | 0 (0) | 8 (678) | 8 (460) |
| HTTP KB / min (uncompressed) | 2 243 | 17 | 2 341 | 42 |
| WS KB / min | 255 | 254 | 255 | 255 |
DOM mutations stay ~172 / s on the list (the numbers really change). A first run gave the same picture (list busy 78 → 41,
HTTP 1 916 → 17 KB/min). The remaining detail long tasks are the chart redraw after each 15 s tail append (~55 ms each).
