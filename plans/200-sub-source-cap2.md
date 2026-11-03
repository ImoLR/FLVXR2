# 200 — 外部订阅源上限 3 → 2 +「如需更多请联系管理员」

Base: tag `3.0.27-fork.110` · branch `feature/3.0.27-sub-source-cap2` · build only (ships in a later batch release).

User 2026-11-03: 「外部连接就搞两个就可以了……如果他还有多的你就让他联系管理员」.
Each account (admins included, as before) may keep at most **2** external subscription sources. At the cap the
add action shows 「外部订阅最多 2 个，如需更多请联系管理员」 (toast ~4 s + text in the dialog + backend error).
No admin per-user override, no new setting. Unchanged: 5 subscriptions per user, 100 nodes per subscription /
source, 链式代理 外部节点 cap 20.

## Existing data
Prod (fork.110) checked read-only on a DB copy (copy deleted afterwards): `user_sub_source` has **0 rows**,
`user_subscription` has 0 rows → nobody is over the new cap. Users who are over it (e.g. 3 sources saved before) keep
all of them: they still refresh and render; only adding is blocked until they are below 2, and the dialog says
「已有 3 个（上限 2 个），如需更多请联系管理员」.

## Changes
| Where | Before | After |
|---|---|---|
| `model.MaxSubSourcesPerUser` | 3 | 2 (drives `sourceCap` in `GET /sub/list`) |
| Backend save error (`sub/source/create`) | 最多 3 个外部订阅源 | 外部订阅最多 2 个，如需更多请联系管理员 |
| Backend `sub/source/fetch` at cap | fetched the URL anyway, failed only on save | same error before any outbound fetch (one COUNT query) |
| Dialog 「获取」 at cap | disabled (dead button) | enabled; press → 4 s toast with the hint, no request |
| Dialog text at cap | 最多 3 个订阅源，删除一个才能再添加。 | 外部订阅最多 2 个，如需更多请联系管理员 / 已有 3 个（上限 2 个），如需更多请联系管理员 |
| Dialog footer | 最多 3 个源 | 最多 2 个源 (from API) |

## Checklist
- [x] Prod read-only count of users with > 2 sources (0)
- [x] Backend cap 2 + error text + pre-fetch check (repo `CountSubSources`)
- [x] Frontend dialog hint / toast / over-cap wording (desktop + H5 share the dialog)
- [x] Tests: cap 2, error text, existing 3 still list/render; update tests assuming 3
- [x] go build / vet / go test (SQLite + PG contract) vs fork.110 baseline
- [x] Frontend tsc / build / lint vs fork.110
- [x] Push branch, CI green (41616851925)
- [x] Demo panel screens 1440 + 390, light + dark → /root/mmwx-preview/sub-source-cap2/
- [x] runs/sub-source-cap2/release-notes.md
- [x] Clean demo panels, summary

## Result (2026-11-03, build only)
- go build / vet (only pre-existing unkeyed-field warnings in `leikwan/testapi/fake.go`), `go test ./...` 25/25 packages OK
  (SQLite), contract tests OK on PostgreSQL 18 (temp container, removed).
- Frontend: tsc 0 errors, build OK, eslint 0 problems in `sources.tsx` (repo-wide errors are in untouched files).
- Demo screens 1440 + 390, light + dark, at cap (2) and over cap (3): 56/56 automated checks OK, no page errors →
  `/root/mmwx-preview/sub-source-cap2/` (:8791).
- Resource use: no new goroutines/timers/background work; one indexed COUNT per 获取 click.
- Not released (batch rule). Release notes: `runs/sub-source-cap2/release-notes.md` (panel only, no OTA, no DB change).
