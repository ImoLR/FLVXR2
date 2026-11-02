# 196 — Telegram 通知 bot：机器续费提醒 +「已续费」按钮

Branch `feature/3.0.27-probe-expiry` (after 194 + 195, same worktree). BUILD ONLY + screens; ships with the batch release
(fork.109). Brief: user 2026-11-02 (coordinator 3d7c53c7) 「搞一个转发面板的通知 bot……要到期的话就发一个通知给 bot 去通知管理员，
然后顺便给一个按钮……管理员已经续费的话就直接点一下『已续费』……这边就不再显示那个通知」. Rolling cycles (plan 194) stay: the bot
never asks anyone to edit dates.

## Design
- **Settings (admin only)** 设置 →「Telegram 通知」 card: bot token (write-only; after save only `@username` from getMe +
  「更换」/「删除」), 「测试发送」, 「生成绑定码」 (8 chars, 10 min, one use, one pending at a time) → admin sends
  `/start <code>` (or opens `t.me/<bot>?start=<code>`) → chat bound (≤ 5 chats) → list with 解绑. Reminder settings: on/off,
  days before (default `7,3,1,0`, 0–60, ≤ 8 values), send time (default `10:00` TW), machine list (FLVX nodes + 救命版-only
  machines, deduped like the probe: plan 195 `oldPanelAdminMerge`) with per-machine 提醒 switch (mute list).
- **Storage:** `vite_config` keys `tg_bot_token`, `tg_bot_secret` (HMAC key, random 32 bytes), `tg_bot_username`,
  `tg_bot_settings` (JSON: enabled, days, time, muted keys), `tg_bot_last_run` (TW date of the last complete daily run).
  All `tg_bot_*` keys: hidden from `config/list` + `config/get` (every role incl. admin), refused by `config/update*`,
  stripped from backup exports. New tables (TableName, no jsonb/serial): `tg_bot_chat` (bound chats), `tg_bot_sent`
  (one row per sent reminder: machine key, due ms, days, chat, message id, text; unique (key, due, days, chat) = dedupe
  across restarts), `tg_bot_ack` (「已续费」 per (machine key, due ms)). Old rows pruned (due < now − 90 days) by the daily run.
- **Machine key:** `f<id>` FLVX node (a node that also runs in the 救命版 stays `f<id>`; borrows the 救命版 expiry when it
  has none, like the probe), `o<id>` 救命版-only machine. Due date = `renewal.Compute` (plan 194 rolling helper).
- **Runtime:** package `internal/tgbot`. No goroutine unless token set AND (≥ 1 chat bound OR bind code pending);
  `Reconcile()` (token / chat / code changes, start) starts or stops the single loop goroutine. The loop: daily run when due,
  then `getUpdates` long poll (timeout 50 s, capped so the send time is hit on time, `allowed_updates` message +
  callback_query), handle updates. Errors → back-off 30 s, ×2, cap 10 min (429 `retry_after` honoured); one log line per
  10 min max (first error immediately, then a counted summary); the token is redacted from every error / URL. No webhook,
  no new public route. API base overridable (`FLVX_TELEGRAM_API_BASE`, http/https only) for tests, demo, or a relay.
- **Updates accepted:** callback queries / messages whose chat is bound, plus `/start <valid code>` from any chat;
  everything else ignored silently (no reply, no answer). Callback data `<a>.<key>.<due36>.<sig>` with
  sig = HMAC-SHA256(tg_bot_secret, `<a>.<key>.<due36>`) truncated to 96 bits (base64url) → a forged or edited button
  cannot act on another machine / due date.
- **Daily run** (once per TW day at/after the send time; missed time → once on start if still the same day): machines whose
  rolling due date is N days away (N in the list), not muted, not 「已续费」 for that due date → one message per machine and
  bound chat: 「⏰ Bestvm US 月付 明天到期（11-03）」 + buttons 「✅ 已续费」 「🔕 不再提醒这台」. Sent keys persisted (dedupe);
  the day counts as done only when every send succeeded or failed permanently (403/400 → chat error shown in settings);
  transient failures retried with the back-off on the same day. ≥ 1 s between sends.
- **「已续费」:** ack row (machine, due); FLVX node → `expiry_reminder_dismissed = 1`, `expiry_reminder_dismissed_until =
  due (s)` (the existing mechanism; node/list now returns the until value and the 节点 card colour honours it, the dashboard
  reminder list skips dismissed nodes) — the 救命版 is never written. Every copy of that reminder (all chats, all N) is
  edited to 「…\n✅ 已续费，下次到期 12-03」 without buttons. Not clicked → next listed day reminds again; due passed →
  cycle rolls, new key, nothing to clean (no 「已过期」 spam).
- **「不再提醒这台」:** key added to the mute list (settings switch re-enables), copies edited.

## Checklist
- [x] Plan doc
- [x] Backend: models + migrations, repo methods (chats, sent, ack, prune, renewal nodes, node dismissal), config key hiding
      + export strip, node/list `expiryReminderDismissedUntil`
- [x] Backend: `internal/tgbot` (client + redaction, signing, settings, reminder selection, texts, manager loop / back-off /
      daily run / updates / bind codes / test send / status)
- [x] Backend: handler glue (machines FLVX + 救命版, renewed hook, admin API `config/tgbot*`, start/stop with jobs), admin
      middleware
- [x] Unit tests (tgbot): signing / forgery, settings validation, reminder selection (rolling, N list, mute, ack), texts,
      back-off sequence + log throttle, token redaction, fake-server client
- [x] Contract tests SQLite + PostgreSQL with a fake Telegram server: bind flow, wrong chat ignored, forged callback
      rejected, 已续费 / 不再提醒 effects incl. FLVX dismissal + 救命版 untouched, dedupe across restart, rolling reset,
      missed-send catch-up, token never returned / logged, generic config API refusal, export strip
- [x] Frontend: 「Telegram 通知」 card + API; 节点 card colour honours dismissed-until; dashboard list skips dismissed
- [x] Gates: go build OK; go vet only the pre-existing `internal/leikwan/testapi` unkeyed-field notes; `go test ./...` with
      `FLVX_POSTGRES_TEST_DSN` rc=0 (717 s, 25 packages, 0 failures; `TestTelegramBotContract/{sqlite,postgres}` both ran,
      also `-race -count=2`); tsc 0 errors, vite build OK, eslint: new files clean, touched files same counts as base
      (node.tsx 8 errors / 52 warnings before and after, pre-existing)
- [x] Resource numbers (demo binary, loopback fake Telegram, 60 s each): no token → 0 bot goroutines / 0 connections /
      CPU 0.0 % / RSS 36.1 MB; token but no chat → still idle; running (1 chat) → 1 loop goroutine + 1 keep-alive
      connection, CPU 0.017 %, RSS 36.0 MB (unchanged), 2 long polls/min with the 25 s fake (≈ 1.2/min on Telegram's
      50 s). Daily run: 12 messages in 11.0 s (= the 1 s spacing between sends); machine list (FLVX + 救命版) + card view
      0.7–1.4 ms per call. One daily log line.
- [x] Demo + screens: 24 screens (unset / token / bind code / bound + list / after 已续费 + 不再提醒 / Telegram side), 1440 +
      390, light + dark, 14/14 checks, 0 page errors; gallery `/root/mmwx-preview/renew-bot/` (:8791/renew-bot/); demo runs in
      a loopback-only netns (no route to Telegram)
- [x] Push, CI green: run 41533094219 on e9898843 (Go backend, frontend, Go PostgreSQL contract, agent); dce953de's first
      frontend job failed on an npm registry miss (@tailwindcss/node@4.3.4 ETARGET, no package change), rerun green.
      `runs/renew-bot/release-notes.md` (token step, user binding steps, verify, rollback); summary `runs/renew-bot/summary.md`
