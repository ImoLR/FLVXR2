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
- [ ] Verify each finding against fork.105 code (file:line)
- [ ] Measure before (renders/s, bytes/min with no detail open, long tasks) on the demo panel (~25 nodes)
- [ ] Fix 1–6 (+ backend downsampled history param if needed)
- [ ] Measure after; screenshot shows the page unchanged
- [ ] tsc / build / lint clean vs base

## Results
(filled in as work completes)
