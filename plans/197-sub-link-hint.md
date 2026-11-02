# 197 — 订阅 100 节点上限提示改为「联系管理员」

Base: tag `3.0.27-fork.108` · branch `feature/3.0.27-sub-link-hint` · build only (ships in release-batch-1103).
Plan numbers 194/195/196 are taken by the probe-expiry chain, so this is 197.

User 2026-11-02: keep the 2 MiB source download cap and the 100-node cap; the over-limit hints tell the user to
contact the admin instead of only telling them to remove nodes; the hint is a toast that disappears after ~4 s.
No per-user admin override. Text / toast duration only — no logic, limit or style change.

## Text changes (前 → 后)
| Where | Before | After |
|---|---|---|
| Editor toast (add 1 / 全部加入 / 整组跟随 over cap) | 一条订阅最多 N 个节点，加入后是 M 个，先移除一些再加 (2 s) | 每条订阅最多 N 个节点，如需更多请联系管理员 (4 s) |
| Editor save toast (over cap) | 一条订阅最多 N 个节点，现在是 M 个，请先移除一些 (2 s) | 每条订阅最多 N 个节点，现在是 M 个，如需更多请联系管理员 (4 s) |
| Editor red bar under 「订阅里的节点 · M / N」 | 超过 N 个节点的上限：客户端只会拿到前 N 个，请移除 K 个再保存 | 超过 N 个节点的上限：客户端只会拿到前 N 个，如需更多请联系管理员 |
| Source fetch preview | 这个订阅有 T 个节点，只保留前 N 个 | 这个订阅有 T 个节点，只保留前 N 个，如需更多请联系管理员 |
| Backend save error (handler + sublink.Resolve guard) | 一条订阅最多 N 个节点，现在是 M 个，请先移除一些 | 每条订阅最多 N 个节点，现在是 M 个，如需更多请联系管理员 |

N always comes from the API (`maxNodes`) / the Go constant, never a hard-coded 100 in the text.

## Checklist
- [x] Frontend texts + 4 s toast duration (editor.tsx, sources.tsx)
- [x] Backend messages (sub_link.go, sublink/node.go) + tests still match
- [x] go build / go test (SQLite + PG contract) vs fork.108 baseline
- [x] Frontend tsc / build / lint vs fork.108
- [x] Push branch, CI green
- [x] Demo panel screens 1440 + 390 → /root/mmwx-preview/sub-link-hint/
- [x] runs/sub-link-hint/release-notes.md
- [x] Clean demo panels, summary

## Results (2026-11-03 00:10 TW)
- Code commit 31a2682f; CI Build Check 41478146887 green.
- `go build ./...` ok; `go test ./...` (SQLite) 22 packages ok, 0 FAIL (same as fork.108); PostgreSQL contract + handler
  tests (`FLVX_POSTGRES_TEST_DSN`, throwaway postgres:18 container, removed) ok.
- Frontend `tsc --noEmit` ok, eslint 0 problems on the changed files, `npm run build` ok.
- Demo panel (loopback-only netns, synthetic DB, plan-193 kit) checks at 1440 and 390: all 5 texts found, the cap toast is
  still shown at 3.5 s and gone at 6 s; 0 page errors. Gallery /root/mmwx-preview/sub-link-hint/.
- The subscription list cards have no over-limit indicator (plan 193 only flags it inside the editor), so item 2 of the
  brief = the editor's red counter bar; no new UI added.
- Release notes: /root/flvx-workers/runs/sub-link-hint/release-notes.md. Build only — ships in release-batch-1103.
