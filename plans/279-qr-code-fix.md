# 279 — Panel QR codes cannot be scanned → replace the hand-written encoder

User report 11-18: 落地「节点链接」→ 二维码, scanned with NekoBox → nothing usable.

## Cause
`OfflineQRCode` / `createQRMatrix` in `vite-frontend/src/pages/node/node-deploy-modal.tsx` is a hand-written QR encoder
(upstream commit c064d522, 3.0.6, 2026-06-02). Decoding 40 generated codes (v1–v20) with jsQR and ZXing: 0 / 40.
- Format-information bits written transposed (row/col swapped) in both copies → breaks every code.
- Alignment patterns centred on timing row/col 6 skipped (v ≥ 7) → only the 3 finder corners may be skipped.
- After fixing both, ZXing still failed on some v13/v19 codes → a third unknown defect. Do not keep patching it.

Affected: every panel QR — 落地 节点链接 / 规则页 复制链接 / chain links (`landing/link-card.tsx`), 订阅链接
(`sub-link.tsx`), 利群 生成订阅 (`leikwan/panel/sub-dialog.tsx`), node deploy QR (`node/node-deploy-modal.tsx` ×2).

## Fix
- New `vite-frontend/src/components/qr-code.tsx`: matrix from the `qrcode` npm lib (MIT, `QRCode.create`, one byte
  segment UTF-8, EC level L, auto mask), rendered as our own inline SVG (offline, no canvas, white square, quiet zone 4).
  Too long → the old warning box.
- Box grows with the code: 4 CSS px per module (min 208 px = old `h-52`), capped at the container width (H5 too).
- Imports re-pointed; old encoder removed from `node-deploy-modal.tsx`.

## Checklist
- [x] Worktree `flvxx-rel279` from fork.147, branch `maintenance/3.0.27-fork.148-qr-fix`
- [x] Replace encoder (`components/qr-code.tsx`, add `qrcode` + `@types/qrcode`), re-point 5 users, remove old code
- [x] Decode matrix (node script in the run dir, jsQR + ZXing): v1–v20 at both capacity edges + realistic links
- [x] `npm run build` + eslint on touched files
- [x] Lab panel + headless screenshots (落地 节点链接 desktop + H5, 订阅链接, 规则页 复制链接), decode the PNGs; gallery `/qr-fix/`
- [ ] Ledger row 279
- [ ] Release: push, tag, CI + images + release assets, Latest
- [ ] Prod backup + rollback script, prune backups, panel swap, health / node checks
- [ ] Prod check: real 落地 节点链接 QR screenshot decodes to the copied link
- [ ] OTA brief note, ledger → 已发布, memory, cleanup (worktree, caches)

## Results (before release)
- Decode matrix (run dir `runs/qr-fix/work/decode-matrix.mjs`, imports `src/utils/qr-matrix.ts`): v1–v20 at both byte
  capacity edges + VLESS Reality (#name encoded / raw UTF-8), AnyTLS, SS2022, vless_enc (1770 B → v31), 订阅 URLs,
  multi-line deploy payload, > v40 fallback → **49/49** (jsQR + ZXing C++ exact). Old encoder: 0/48 codes.
- ZXing Java 3.5.3 (NekoBox scans with zxing-lite = ZXing Java): 300 random codes rendered sharp 3/4/6 px →
  297/292/292 — same as Nayuki's encoder (uqr) 297/293/293 → single-frame detector limit, not the encoder.
  zxing-js (port) is weaker (~90 %, also equal for both encoders; PURE_BARCODE decodes all) → not used as the gate.
- Lab (fork.147 backend, scrubbed prod-DB copy, loopback netns) screenshots: 落地 节点链接 SS2022 ×2 + VLESS Reality
  (desktop + H5 390), 订阅链接 (desktop + H5), 规则页 复制链接 (desktop): 16/16 PNGs (crop + full page) decode exactly
  with ZXing Java, ZXing C++ and jsQR. Same dialogs on fork.147: 0/10. Gallery http://104.145.236.29:8791/qr-fix/.
- `npm run build` ok (qr-code chunk 24.6 kB / 9.4 kB gzip); eslint: 0 new problems (node-deploy-modal keeps its 2
  pre-existing label a11y errors, same at base).
