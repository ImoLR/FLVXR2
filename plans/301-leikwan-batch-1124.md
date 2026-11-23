# 301 — 利群 batch 11-24: hide 首包过滤, App 手机本地 = VPS latest, 入口机 status pills, 转发/诊断 per chosen entry

Task `leikwan-batch-1124` (user 11-24 ~00:00–00:35 TW, voice). Branch `maintenance/3.0.27-fork.165-leikwan-batch`
from tag `3.0.27-fork.164`. One release: panel **fork.165** + App private prerelease **1.0.10-panel**.

## Decisions recorded

1. **首包过滤 (front ssfilter) stays ON on prod** — the logswitch trigger `eof1s` (1 s 0-byte close → switch) only acts
   where ssfilter enforces, and the user wants that trigger (11-24 ~00:35 TW: 「那就开着吧」). On 11-24 it is only
   **hidden from the UI**: no pill, no 「首包过滤没打开」 / 「入口不支持首包过滤」, no line in 换主入口 previews,
   capability lists, host drawers, 手机本地 modules, rule chips or 诊断. Backend, API fields, host files and the
   专线 setting stay as they are (code kept, just not rendered).
2. App 手机本地 runs what 百度云广州 runs: vendor core 2026.11.13.1 (same release; Android build), 日志快切 v12
   (LogSwitchRelay = the Baidu v11/v12 port) with Baidu's triggers, 备线 per forward from the panel (phone dials the
   备线 forward's entry directly; only addresses the panel hands out), 预热池 sizes as the panel generates them,
   `bytes4=on`, `zero_fin=on`, logswitch `bytes4=record`, burst off.
3. 入口机 rows: no 「主入口 N」 pill; one status pill only: 「就绪」 / 「离线」 (手机本地: 「就绪」 / 「连接中 x/y」 /
   today's not-connected state).
4. 转发 tab shows the viewer's chosen entry only (server 主入口, or 手机本地 in the App): address, chips, status,
   诊断. No 「其他利群入口机」 in 诊断, no 「N 台入口不支持预热」 on the 专线 header.
5. 诊断 = path from the chosen entry: 本机 → 入口 (App measures; browser starts at the entry) · 入口 → 专线入口
   (measured on the entry; 手机本地: on the phone) · 专线入口 → 专线出口 (估算 = 全程 − measured hops, coloured by
   the latency thresholds) · 专线出口 → 落地 (measured on the native landing via kernel TCP rtt of the inbound
   connections; fallback: 专线入口 → 落地 估算 as before).

## Checklist

- [x] Plan doc + worktree (this file)
- [x] Panel: hide every 首包过滤 trace (caps lists, CapPills, EntryLimitPills, preview diffs, host drawer values,
      专线 chips, 手机本地 caps, line dialog toggle, text mentions); tests updated
- [x] Panel: 入口机 status pills (desktop tiles + H5 rows + 手机本地 row)
- [x] Panel: 转发 rows per chosen entry (admin rows follow the admin's entry; drop mixed-entry info)
- [x] Backend: phone plan endpoint (`leikwan/phone/plan`, admin) = Baidu-equivalent values per forward
- [x] Backend: 诊断 v2 (hops, landing rtt, no other hosts, `local` mode for 手机本地)
- [x] Agent: `LandingPeerRtt` command (sock_diag tcp_info of inbound conns on a landing port)
- [x] Frontend: 诊断 path strip v2 + App phone measurements
- [ ] App 1.0.10-panel: PanelPlane (日志快切 + 备线 + 预热 from the plan), bridge setPlan / diag / tcpPing,
      features in AppInfo, PhoneState protect fields; unit tests; lab run
- [x] lkPhoneCaps / phoneCaps / lkPhoneRuleDiff follow the App's features
- [x] Tests (panel): go vet clean; go test ./... with PostgreSQL — all packages pass except one contract assertion that pinned the old 「phoneLimits only on own rules」 behaviour (updated to plan 301, re-run ok); npm build + eslint clean; go-gost builds, LandingPeerRtt tests pass
- [ ] Gallery http://104.145.236.29:8791/leikwan-batch-1124/
- [ ] Release fork.165 (after plan 300 finished) + prod upgrade + checks
- [ ] OTA of the native landing nodes used by 利群 forwards (agent change; busy nodes only 02–06 TW)
- [ ] App private prerelease v1.0.10-panel
- [ ] Ledger row 301, memories, cleanup

## As built (panel)

- **首包过滤 hidden, not removed.** Backend: `lkCapsOf.Missing`, `ruleLimits`, `lkSwitchRuleDiff`, `lkPhoneRuleDiff` no
  longer list it (JSON field `ssFilter` kept); host file names for `ssfilter` / `sskeys` read 「入口内部文件 …」 (only
  seen when such a file drifts / fails); trigger note of `eof1s` reworded. Frontend: `CapPills`, `EntryLimitPills`
  (also skips key `ssfilter`), 专线 chips (「SS 过滤」), host values table column, override text, host drawer file list
  (`ssfilter` / `sskeys` not listed), 专线 dialog toggle (value kept as saved), 换落地 text. Host files and the
  专线 setting are untouched — the filter stays **on** on prod.
- **入口机 status pill** (`HostStatusPill`): online + client running = green 「就绪」, offline = 「离线」 (same pill as
  before), online without a running client = the slate status text. No 「主入口 N」 on tiles / rows (the host drawer
  header keeps its 「主入口 N 人」). 手机本地 (`phoneEntryPill`): 「就绪」 (connected, ready = total) /
  「连接中 x/y」 / today's 未连接 · 连接失败 · 正在断开; 「当前入口」 / 「未选用」 pills dropped.
- **Rows per chosen entry.** Admin rule list: `leiKwanRuleContextFor(viewer)` — every row's `entry` and
  `entryLimits` are the admin's own 主入口 (fallback: the owner's); `entryClosed` + grey 「这个入口没开」 when
  「只开主入口」 keeps another user's forward closed there. App with 手机本地: every row follows the phone
  (`phoneLimits` for all rows, `localFor` all). Dropped: 「N 台入口不支持预热」 (专线 header), the static amber
  「所有入口可用」 on admin-owned rows (the toggle on other users' rows stays).
- **诊断 v2** (`leikwan_diag.go`): rows `device` (本机 → 入口, the App's `tcpPing`), `hop1` (入口 → 专线入口; 手机本地 =
  the App's `diag`), `vendor` (专线入口 → 专线出口 = 全程 − hop1 − landing, 估算) + `landing` (专线出口 → 落地, agent
  `LandingPeerRtt` on the native landing node: kernel `min_rtt` of the relay exit's inbound connections; FLVX node IPs
  skipped, non-mainland peers preferred, then the busiest) or, when the landing cannot be measured (manual / external /
  shared landing, old agent, no exit connection), `hop2` (专线 → 落地 估算) + a 未测 landing row with the reason. Admin
  sees the exit IP + carrier label; users never see relay / exit addresses. No 「其他利群入口机」. The entry is the
  viewer's own (admin on another user's forward → the admin's 主入口). Estimates use the latency colours (<100 green,
  100–180 amber, >180 red), the 「估算」 word stays.
- **Phone plan** `POST leikwan/phone/plan` (admin, read-only): reference host = the caller's 主入口 when it runs warm +
  logswitch + triggers, else the first such 利群入口; per rule `effective()` with the phone as the owner's 主入口 (host
  pins apply, e.g. 540 预热 10 on 百度云广州); 备线 = the 备线 forward's own entry (IP literal only), record-only
  forwards get no 日志快切; global triggers = the host's `trigGlobal` (+ `grace` / `io_error` from the 利群加速 global),
  warm `backup_standby` / `pace_ms` / `bytes4` / `zero_fin` from the host's warm extras, burst always off, coalesce as
  that host (百度云广州: off). The App page pushes it with `setPlan` on every list load (the App ignores an unchanged
  plan). Phone caps / limits / 换主入口 preview follow `AppInfo.features` (1.0.10-panel = full, 备线 = 「手机直连备线」).
- **Agent** `LandingPeerRtt {port, max}` (go-gost/x/socket): one sock_diag dump per family, ESTABLISHED sockets on the
  local port, per remote IP: conns, median srtt, lowest min_rtt; ≤ 64 peers. Read-only, nothing kept.

Prod facts used (read-only, 11-24 ~00:30 TW): 百度云广州 runs vendor agent 2026.11.18.1, core 2026.11.13.1
(1ed417036c60), front `…-headroom-bytes4` ab4b7795, logswitch v12 e1de109d; triggers `timeout=switch
warm_death=switch io_error=record bytes4=record return_check=off` (+ 263/264/280/282/540 on); warm `263=10 264/280/282=4
540=10 backup:*=4 backup_standby=2 pace_ms=100 bytes4=on zero_fin=on burst=off`; hold 30 s, settle 30 s, grace switch,
return check 2.5 s. The App bundles core 2026.11.13.1.1add2836 (same release, Android build). 备线 forwards
(148/587/595/599/601/619) are plain FLVX forwards on 深港ixp1 without IP limits.

Review fixes during the lab run: the 诊断 body's `phone` part is decoded leniently (the App's diag / tcpPing replies carry
extra fields such as `count` — the strict decode answered 「请求参数错误」); desktop 诊断 dialog wider (5 path nodes); the
phone path label 「手机 → 专线入口」; the 手机本地 「快切」 chip only on forwards that are up.

## fork.165 → fork.166 (same night, panel only)

Live after the landing OTA (05:00 TW, 5 lines): with 全程 − 入口段 − 落地段 the vendor part came out ≈0 on 263 / 264 /
282 (入口段 + 落地段 ≥ 全程), while 全程 − 入口段 alone gives plausible vendor times (广港 6 ms, 沪日 24 ms, 广台 23 ms,
福台 7 ms). The LeiKwan client's 全程 (UDP probe / TCP selection rtt) ends at the line's exit — its probes never reach
the landing. Fix: 专线入口 → 专线出口 = 全程 − 入口段 (估算); new summary `endToEndMs` ≈ 全程 + 落地段 (shown as
「到落地 ≈N ms」); texts say 全程 = 入口机 → 专线出口. Without a landing measurement the old 「专线 → 落地」 estimate
stays as plan 272 had it. Panel only (no agent change).
