# 314 — 利群 京德 (PEK→FRA) 专线 + measurement forward, 5th 「利群专线端内」 line (ops, no code, no release)

Task `leikwan-jingde-endin` (11-26 evening TW). Prod fork.174, data ops through the admin API only.
Work dir: /root/flvx-workers/runs/leikwan-jingde-endin/work (ROLLBACK.md, logs, DB backup).

Decisions:
- 专线 「利群·京德」 = LeiKwan route 25 (PEK -> FRA), tcp+udp, accel like lines 2–4 (warm 4/4/2, 首包过滤 enforce, 日志快切, 切回前复查).
- No 备线 tunnel: it is a measurement line. Without a 备线 the host config turns 日志快切 / 切回前复查 off by itself.
- Rule 「🇩🇪 利群京德」, owner admin imolr, landing = new ss2022 native landing node on Boil HKT 99u (landing server 15) on its own picked port; rule warm override = 1 (low).
- Chart 3 「利群专线端内」 gets line 「京德」 (kind lk_endin, 入口 auto).

## Checklist
- [x] Read prod state (lines 1–4, rules 1,2,4,5,6,8, admin grant 主入口 百度云广州, 备线 on; 99u server 15 has admin node 221 only)
- [x] Validated DB backup (work/flvx-gost.db.pre313-20261126T112553Z, integrity ok)
- [x] Vendor check: route 25 available, quota 6/8
- [x] Create 专线 「利群·京德」
- [x] Create landing node on Boil HKT 99u (own port)
- [x] Create rule 「🇩🇪 利群京德」 → vendor forward #7; rule warm override 1; clear the cosmetic 备线 error
- [x] Verify client on 百度云广州 / 阿里广州 (status.json active_udp), agent report seg, 京德 header 「端内 X ms」
- [x] One real request end to end — SS2022 client on the dev box (xray upload to 阿里广州 too slow, stopped + cleaned) → 百度云广州 106.13.17.23:30001 → 京德 → 99u: 5/5 generate_204, exit IP 58.152.255.186 (= 99u), 10 MB at 13.9 Mbps
- [x] Chart 3 line 「京德」 = line_monitor_line 9 (colour 4); 7 consecutive minute points 19:35–19:41 TW, 106–114 ms (first gap 19:33–34 while the warm stream settled)
- [x] ROLLBACK.md, summary, memory updates

## Result (11-26 19:42 TW)
| 专线 | entry (主入口) | vendor fwd | landing | port |
|---|---|---|---|---|
| 5 「利群·京德」 PEK -> FRA | 百度云广州 106.13.17.23:30001 | 672 (quota 7/8) | node 232 「利群京德 99U」 Boil HKT 99u | 30001 |

- Rule 9 「🇩🇪 利群京德」 (admin, source panel), no 备线 (backup_error cleared by hand: it only said 「专线没有设置备线隧道」), warm override 1 (主入口 only).
- 端内 header: 120 → 107–114 ms (全程 145–160 − 入口段 38–46), only from 百度云广州: 阿里广州 has no warm stream (not 主入口) and no traffic → no connection to the ingress → no seg reading. If 百度云广州 is offline the line shows gaps.
- Other 6 ultra-forward services and the front relay were not restarted. Panel healthy, no new errors.
- Chart is keyed by 专线 (line_monitor_line.leikwan_line_id = 5), not by the forward or landing.
