# 300 — 利群 forwards (+ 备线) → FLVX native panel landings, old landings deleted, forward 279 deleted

Task `leikwan-landing-native` (Claude xhigh, coordinator 73a52678, 11-24 TW). Ops only: no FLVX code change, no release.
User 11-24 ~00:00 TW: every 利群 forward and its 备线 must use our own FLVX panel landings (never shared mmwx landing
servers), used through the 订阅; delete the old landings. 11-24 ~00:35 TW (final): **首包过滤 stays ON** (logswitch
trigger eof1s needs it) and **forward 279 (AWS HK 专线) + its 备线 are deleted completely**.

Base: tag `3.0.27-fork.164` (prod). Branch `ops/3.0.27-leikwan-landing-native` (docs only), worktree
`/root/projects/flvxx/flvxx-300`. Run dir `/root/flvx-workers/runs/leikwan-landing-native/` (work/ = scripts, backups).

## Inventory (read-only, 11-24 00:15–00:35 TW)
All 7 利群 rules are admin imolr's (user 1); grant: 主入口 百度云广州, 备线 on. Sub 27 「利群汇聚」 = `leikwan_all` (auto).

| rule | vendor fwd | entry port | target before | 备线 forward (tunnel) → target |
|---|---|---|---|---|
| 1 | 263 | 10018 | 99u :10016 (mmwx node 110 「自己用 ss-10016」) | 148 備線·263 (t90 深港ixp1→99U 端口转发) → 99u :10017 |
| 7 | 540 | 10021 | 99u :10019 | 619 備線·540 (t90) → 99u :10019 |
| 5 | 281 | 10016 | Fachost :10015 (mmwx node 58) | 599 備線·281 (t4 深港ixp1→TW) → Fachost :10015 |
| 3 | 279 | 10019 | AWS HK :10022 | 601 備線·279 (t105 深港ixp1→HK 端口转发) → AWS HK :10022 |
| 2 | 264 | 10015 | native node 219 Bestvm JP B :31004 (plan 299) | 595 → :31004 (bound to 219) |
| 4/6 | 280/282 | 10020/10017 | native node 215 马年 :12311 (plan 299) | 587 備線·280/282 (t4) → boilhinet2 :12311 (same addr as 215, not bound) |

What is behind the old ports / who else dials them:
- **99u :10016** = mmwx official xray, node 110 (imolr only). Also dialed by **救命版 fwd 76 「t1」** (tunnel 89 火山云广州 →
  MKCLOUD 新春版 (deleted 11-14) → Boil HKT 99; lifetime 58 KB) → keep (救命版 rule), list.
- **99u :10017** = our own `leikwan-backup-ss.service` (lkbk-core, copy of the :10016 key) — only 148.
- **99u :10019** = our own `leikwan-540-ss.service` (lkbk-core, own key) — only 540 + 619.
- 99u :10015 / :10018 = mmwx nodes 111 「max」 / 136 「king」 (other mmwx users) — untouched.
- 99u :30000/:30001 = FLVX **shared** server 11 (mmwx share of server 2), only nodes 194 「利群263 99U」 / 195 「利群540 99U」
  (admin, prepared 「待切换」 for rules 1/7, never used, 3 MB tests, 0 live conns).
- **Fachost :10015** = mmwx official xray (nodes 58–62, imolr) — **救命版 fwd 83/84/85/86/96/97 use it heavily** → keep.
- **AWS HK :10022 is NOT an mmwx inbound**: it is **救命版 fwd 96 「利群广港 aws seednet」** (tunnel 109, flux_agent on AWS
  Lightsail HK → Fachost :10015). 救命版 → keep untouched, list.
- **Bestvm JP B :60061** = standalone `xray-jp-60061.service` (not mmwx), old 264 landing; still dialed by FLVX fwd 597
  「测试·264备线·深港ixp1→Akari HK→JP」 (admin test, 3.9 MB lifetime, idle) → retarget 597 to :31004, then stop the service.
- 利群入口 hosts are all 接管 (manage=1): warm / ssfilter / sskeys / triggers / logswitch LINES / phone DNAT are rendered by
  the panel. LINES and phone DNAT (百度云广州 19263…19282) point at the 备线 forwards' entry ports on Mkcloud 深港ixp 1
  (163.53.52.189:35046/40304/52914/31070/52405/59349), not at landing ports → unchanged by a 备线 retarget.
- **Design constraint found:** `landing/server/enable` refuses a native landing on a machine that mmwx shares to FLVX
  (「该机器已由妙妙屋共享给 FLVX，不能同时启用自有落地」). 99u has share server 2 → FLVX shared server 11. Server 11 holds
  only the 2 unused 利群 nodes → delete 194/195, revoke the mmwx share of server 2 (Helper stops only the FLVX shared
  Core; official xray / mmw-agent untouched), then enable the native landing.
- Hosts: 99u (Debian 12, 1 vCPU, 960 MB, **no swap, ~250 MB available**, static IP, no FLVX agent yet, also 救命版
  flux_agent :51378); Fachost = FLVX node 45 (agent fork.154, 2 vCPU / 2 GB, **IP changes 04:00 TW via boil**, domain
  facseednet.imgamer.top) → finish Fachost before 03:45 TW.

## 首包过滤 (ssfilter) per host — unchanged by this plan
| 利群入口 | enforce | off / not rendered |
|---|---|---|
| 百度云广州 | 263 264 279 280 281 282 540 | — |
| 阿里广州 | 263 264 279 280 281 282 | 540 (rule override off) |
| 火山广州 (offline since 11-22) | 263 264 279 280 281 282 | 540 |
| 火山上海 (offline) | no ssfilter file (stable front, eof1s etc. record-only) | all |

Switched rules keep their ssfilter value; the new landing keys go into sskeys through the panel (all hosts 接管).

## Checklist
- [x] Read-only inventory: each 利群 forward + 备线, current target, inbound behind it, who else uses it, free ports
- [x] Backups (00:25 TW, TS 20261123T162538Z): FLVX DB copy (quick_check ok) + rows-before.json, mmwx pg_dump
      (mmwx-prod /root/backups/pre-leikwan-native-…/mmwx.dump), host configs on 99u / Fachost / JP B
      (/root/backup-20261123T162538Z-before-flvx-leikwan-native/), pre-switch sskeys copy on 百度云广州 / 阿里广州;
      undo = run dir work/lk300.py undo + work/ROLLBACK.md
- [x] Baseline 00:27–00:33 TW: all 利群 rules + 备线 diagnose OK (263 13 ms, 540 10–11, 281 23, 279 11); 5 busiest 3/3
- [x] 99u: FLVX node 63 「Boil HKT 99u」 (group 落地, port 40000-50999) + agent `flvxx` fork.164 (migrate answered n:
      flux_agent PID 502, xray 2123259, mmw-agent 214346 unchanged); shared nodes 194/195 deleted (ports freed), mmwx
      share of server 2 revoked 00:31:52 TW (Helper stopped only mmwxc-flvx-core), native landing server 15
      (30000–39999, Core 256 MB / 50 %)
- [x] Fachost seednet 黑五 (node 45): native landing server 16 (30000–39999 carved → node ports 10000-29999,40000-65535)
- [x] 263 → node 221 「利群263 99U」 :30000 (00:35:35), 540 → node 222 「利群540 99U」 :30001 (00:35:42), 281 → node 223
      「利群281 Fachost」 :30000 (00:55:54); 备线 148 / 619 / 599 followed (bound to the new nodes); rules renamed to the new ports
- [x] 279 deleted 00:58:43 TW: 利群 rule 3 + vendor forward (quota active 7 → 6 of 8) + 备线 601 (forwards 65 → 64);
      logswitch on 百度云广州 restarted itself with the new LINES, phone DNAT 19279 dropped, 279 key removed from sskeys;
      AWS HK :10022 (救命版 96) kept untouched
- [x] 597 → :31004 (bound 219); 587 bound to native node 215 (same address)
- [x] Verify: diagnose 6 rules 8/8 (263 13 ms, 540 12, 281 22, 264 64, 280 22, 282 25), 备线 148/619/599/587/595/597 OK;
      real SS2022 client from 阿里深圳: 263 via 百度云广州 89 ms first byte / 140 Mbps, via 备线 63 ms / 157 Mbps;
      540 93 ms / 135, 备线 65 / 151; 281 201 ms / 81, 备线 118 / 114; exit IPs = 99u / Fachost; direct from AWS HK
      115 / 133 / 126 Mbps; sskeys/ssfilter diff 0 on 百度云广州 + 阿里广州; sub 27 = 6 利群 nodes; temp main + 手机本地
      subs rendered 6 nodes (106.13.17.23 / 127.0.0.1) and were deleted
- [x] Old landings removed (stopped + disabled, files kept): 99u `leikwan-backup-ss` (:10017) + `leikwan-540-ss` (:10019),
      Bestvm JP B `xray-jp-60061` (:60061) — all had 0 conns; kept: 99u :10016 (mmwx node 110, 救命版 fwd 76),
      Fachost :10015 (mmwx + 救命版 83–86/96/97), AWS HK :10022 (救命版 fwd 96)
- [x] Post-change check: 30/32 nodes online (only 火山 pair, as before), 5 busiest forwards 3/3 same ms, host + panel logs
      clean; RAM available 99u 250 → 294 MB, Fachost 1269 → 1225 MB, JP B 195 → 215 MB; new Core RSS 81 / 59 MB
- [x] Memory + summary

## Notes / open
- 首包过滤 unchanged everywhere (table above); 279 left the lists with its forward.
- 火山广州 (offline) still has the old files; the panel pushes the new keys / LINES when it comes back.
- Host listeners inside the new landing ranges: xray metrics 127.0.0.1:38889 on 99u and Fachost (+ xray's ephemeral UDP
  ports on Fachost); the landing Core API on 99u is 127.0.0.1:40021 inside node 63's rule range. No clash today
  (nodes on 30000/30001); a future node on 38889 would fail to bind.
- FLVX shared server 11 「Boil HKT 99u」 stays as a revoked history row (same as server 3 for 马年).
