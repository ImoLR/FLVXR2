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

- [ ] Plan doc + worktree (this file)
- [ ] Panel: hide every 首包过滤 trace (caps lists, CapPills, EntryLimitPills, preview diffs, host drawer values,
      专线 chips, 手机本地 caps, line dialog toggle, text mentions); tests updated
- [ ] Panel: 入口机 status pills (desktop tiles + H5 rows + 手机本地 row)
- [ ] Panel: 转发 rows per chosen entry (admin rows follow the admin's entry; drop mixed-entry info)
- [ ] Backend: phone plan endpoint (`leikwan/phone/plan`, admin) = Baidu-equivalent values per forward
- [ ] Backend: 诊断 v2 (hops, landing rtt, no other hosts, `local` mode for 手机本地)
- [ ] Agent: `LandingPeerRtt` command (sock_diag tcp_info of inbound conns on a landing port)
- [ ] Frontend: 诊断 path strip v2 + App phone measurements
- [ ] App 1.0.10-panel: PanelPlane (日志快切 + 备线 + 预热 from the plan), bridge setPlan / diag / tcpPing,
      features in AppInfo, PhoneState protect fields; unit tests; lab run
- [ ] lkPhoneCaps / phoneCaps / lkPhoneRuleDiff follow the App's features
- [ ] Tests: go vet, go test (PG), npm build + lint, App unit tests
- [ ] Gallery http://104.145.236.29:8791/leikwan-batch-1124/
- [ ] Release fork.165 (after plan 300 finished) + prod upgrade + checks
- [ ] OTA of the native landing nodes used by 利群 forwards (agent change; busy nodes only 02–06 TW)
- [ ] App private prerelease v1.0.10-panel
- [ ] Ledger row 301, memories, cleanup
