# 308 — every 利群 forward on its own landing port + audit of shared designs (ops, no release)

Task `leikwan-landing-split` (11-25 night TW). User: 「每条转发一个端口，不要共用（类似 NAT 不稳），每个落地对应一个端口；还有没有其他类似共用的设计？」

Ops only (admin API + vendor PATCH through the panel), no code, no release, no panel restart.
Run dir: /root/flvx-workers/runs/leikwan-landing-split/ (work/ = scripts, logs, backups, ROLLBACK.md).

## State before (prod DB 11-25 ~20:45 TW)
| 利群 rule | vendor fwd | line | landing node | port | 备线 |
|---|---|---|---|---|---|
| 1 🇭🇰 利群广港 | 263 | 广港 | 221 Boil HKT 99u | 30000 | 148 備線·263 |
| 2 🇯🇵 利群沪日 | 264 | 沪日 | 219 Bestvm JP B | 31004 | 595 備線·264 |
| 4 🇹🇼 利群广台 Hinet | 280 | 广台 | **215 马年 (shared)** | 12311 | **587 備線·280/282 (shared)** |
| 6 🇹🇼 利群福台 Hinet | 282 | 福台 | **215 马年 (shared)** | 12311 | **587 (shared)** |
| 5 🇹🇼 利群广台 Seednet | 281 | 广台 | **223 Fachost (shared)** | 30000 | 599 備線·281 |
| 8 🇹🇼 利群福台 Seednet | 615 | 福台 | **223 Fachost (shared)** | 30000 | 711 備線·615 (auto) |

马年 :12311 (node 215) is also the landing of 救命版 72/73/99 → stays as it is.

## Decisions
- 12311 stays node 215 (救命版 + nothing 利群); 280 and 282 each get a new native node on 马年 (port picker).
- Fachost :30000 stays 281 (+ its 备线 599); rule 8 gets a new node on Fachost.
- New nodes get new keys (215's single key is also on 12311 / 223's key is 281's) → links of 280, 282, 615 change; sub 27 carries them.
- 备线 = second path of the same line → points at that line's new landing. 587 is shared by 280/282: the panel gives the first
  switched rule (282) its own automatic 备线 「備線·282」; 587 then moves with 280 alone (renamed 「備線·280」).

## Checklist
- [x] Read-only snapshot (state-before.json, rules, links, sub 27 render, vendor forwards)
- [x] Backup: prod gost.db online copy + quick_check; landing host listeners/configs (马年, Fachost); prune-backups
- [x] 282 福台 Hinet: new node on 马年 → listens + direct SS request → rule switch (vendor PATCH) → new 備線·282 → e2e via line → ss on landing = one 利群 source
- [x] 280 广台 Hinet: new node on 马年 → same steps → 587 follows → rename 587 「備線·280」
- [x] 615 福台 Seednet: new node on Fachost → same steps → 711 follows
- [x] 263 / 264 / 281: confirm alone on their ports (FLVX, 救命版, mmwx)
- [x] Sub 27 re-rendered: 6 lines, new nodes, diff vs before
- [x] 诊断 for all 6 lines OK; each landing port shows exactly one 利群 source
- [x] ROLLBACK.md + vendor PATCH log (old/new target)
- [x] Audit of other shared designs (report only)
- [x] Summary (Chinese), ledger row 308 (ops), memories updated, plan committed

## Result (11-25 20:45–21:07 TW)
| 利群 rule | vendor fwd | landing node | port | sources seen on the port | 备线 |
|---|---|---|---|---|---|
| 1 🇭🇰 利群广港 | 263 | 221 「利群263 99U」 | Boil HKT 99u :30000 | 广港 exits + Mkcloud 深港ixp1 89.43.141.189 (備線) | 148 |
| 2 🇯🇵 利群沪日 | 264 | 219 「利群264 沪日」 | Bestvm JP B :31004 | 沪日 exits + 89.43.141.189 (備線) | 595 |
| 4 🇹🇼 利群广台 Hinet | 280 | **227 「利群广台 Hinet 马年」** | 马年 **:10020** | 广台 exits (98.98.108.x, 210.61.104.x, 103.167.50.63) + AWS HK (備線) | 587 「備線·280」 (entry 31070 kept) |
| 5 🇹🇼 利群广台 Seednet | 281 | 223 「利群281 Fachost」 | Fachost :30000 | 281 exit only (no warm pool → idle when unused) | 599 |
| 6 🇹🇼 利群福台 Hinet | 282 | **226 「利群福台 Hinet 马年」** | 马年 **:10019** | 福台 exits + AWS HK (備線) | **718 「備線·282」 (new, auto)** |
| 8 🇹🇼 利群福台 Seednet | 615 | **228 「利群福台 Seednet Fachost」** | Fachost **:30001** | 福台 exits + AWS HK (備線) | 711 (entry 48927 kept) |

- Vendor PATCH (If-Match, via panel) ×3: 282 → :10019, 280 → :10020, 615 → :30001. Entry ports unchanged.
- New keys → links of 280 / 282 / 615 changed; sub 27 verified (6 nodes, keys = landing nodes); old keys refused by 首包过滤.
- Each switch restarted that forward's client on the entry hosts → ~90 s on 备线 (日志快切 warm_death) then back (check ok).
- 诊断: 263 / 264 / 280 green; 281 green with a live conn (idle = no exit to measure); 282 / 615 landing green, vendor entry ④
  150.242.97.22 refuses connections since 20:43:56 (before the first switch, vendor side).
- 马年 :12311 (node 215) kept for 救命版 72/73/99, 0 利群 connections.
- landing_server 14 max_nodes_per_user 3 → 7 → 3 around the two creates (direct DB, panel-only limit).
- Rollback: /root/flvx-workers/runs/leikwan-landing-split/work/ROLLBACK.md. Audit: runs/leikwan-landing-split/summary.md (report only).
