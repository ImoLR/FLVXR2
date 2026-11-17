# 273 — 利群汇聚: 利群自己的流量 (套餐流量 + 每条规则的利群计费流量) + 只读在线设备

User 2026-11-17 TW evening (「按你推荐的做」): pull the vendor's new Forward Lines API usage
numbers into 利群汇聚. Build + screenshot gallery only; release waits for the user's OK on the
screens (memory `ui-add-only-preview-first`).

Base: tag `3.0.27-fork.144` → branch `feature/3.0.27-leikwan-usage`
(worktree /root/projects/flvxx/flvxx-leikwan-usage).

## Decisions
- Vendor reads only (GET). No pause/enable, no device kick/restore, no UI for them.
- One background poller for every configured 利群 account (`leikwan_account` rows). Each
  account has 4 jobs: `usage` (current cycle, every 5 min), `devices` (every 5 min),
  `service` (services list for plan name / `expires_at`, every 30 min) and `previous`
  (last closed cycle, once per cycle). A round runs every 2.5 min and does at most 2 due
  jobs, 15 s apart → ≈ 26 requests / hour per account (limit: 120 / min).
- Back-off: 401/403 → 30 min; 429 → the vendor's Retry-After (at least the job interval);
  other errors → interval × 2ⁿ, from the 3rd error in a row 30 min. The last good snapshot
  stays; the card shows 「更新于」 + a stale / error chip.
- Snapshots persist in a new table `leikwan_usage_snapshot` (account_id + kind unique,
  data = trimmed JSON) so a restart shows numbers at once; the handler keeps them in memory.
- Visibility: admin = 套餐流量 card, devices, all chips (+ share / extra bytes). A granted user
  = the 「利群计费流量」 chip on their own rules only (bytes + 更新于 + cycle dates), no
  account totals, no devices, no other users' bytes.
- Vendor bytes are shown beside our nft 利群流量 (plan 269) and 备线 (plan 271) chips,
  never merged.
- Devices: IP → node name for our nodes (`server_ip` / v4 / v6), e.g. 火山上海.
- Times shown in TW (UTC+8); the vendor sends UTC strings.

## Checklist
- [x] Client: typed `ServiceUsage` / `DeviceList` / `ServiceSummary` reads (same limiter, Bearer,
      no token in errors); tolerant decode test with the new `ingress {host,port}` shape
- [x] Fake API (`testapi`): `/services/{id}/usage` (+ `?period=previous`), `/devices`,
      `/devices/online`, services `expires_at` + usage; lab knobs for errors
- [x] Model + repo: `leikwan_usage_snapshot` (TableName, no jsonb/serial) + AutoMigrate
- [x] Poller: rounds, due jobs, back-off, persist, memory cache, exported `LeiKwanUsageRound`
- [x] API: admin `/api/v1/leikwan/usage` (card + devices + per-forward); `leikwan-rule/list`
      rules carry `vendorFlow`
- [x] Unit tests (client decode, poller back-off / spacing) + contract tests (visibility:
      admin / granted owner / other user / no grant)
- [x] Frontend types + API call
- [x] Admin desktop: 套餐流量 card (right column top), rule-row chip, 利群在线设备 card
- [x] Admin H5: 套餐流量 block + devices at the end of the 入口机 tab (＋/− rows; a 4th tab overflowed
      the tab row at 390 px, so the row stays unchanged), chip in the rule ＋ details
- [x] User desktop + H5: chip on own rules only
- [ ] go vet / go test ./... / contract / npm run build; CI green on the branch
- [x] Lab (fake API with real numbers) + gallery /root/mmwx-preview/leikwan-usage-built/ (:8791)
- [x] Release brief /root/flvx-workers/briefs/release-leikwan-usage.md (panel only)
- [ ] Ledger row 273 「built, waits for user screen approval」, summary

## Result
(filled in at the end)
