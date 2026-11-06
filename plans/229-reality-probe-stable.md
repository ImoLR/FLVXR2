# 229 — Reality 测速多测几次 + 不留 TIME_WAIT + 妙妙屋池小网站「稳定才用」

Branch: `feature/3.0.27-reality-probe-stable` (from prod `3.0.27-fork.121`, tag 57c5bb41). BUILD ONLY — rides the
next batch release (panel + agent OTA of the native landings later).
Run dir: `/root/flvx-workers/runs/reality-probe-stable/`. Contract: `/root/flvx-workers/contracts/reality-pick-v1.md`.
mmwx side (Helper linger + concurrency 2) = separate worker `helper-probe-quiet`; FLVX works with old and new Helpers.
Approval: user 2026-11-06 ~23:40 TW (answers to plan 227 「需要你决定」 1 + 2).

## Findings (fork.121, before any change)
- Agent probe: ≤ 4 candidates at once and, per candidate, every IP (≤ 8) pinged **in parallel** → up to 32 sockets
  open at once. Every socket closed with a normal FIN → our side keeps TIME_WAIT 60 s per socket (~170 per probe).
- Pin: first pin / 6 h re-check pings every IP in parallel (≤ 8 at once), 10-min liveness = 1 connect; FIN close.
- Pick = the single fastest sample (Bestvm US picked portal.citygrainla.com: one lucky sample, really rank 48).
- Pool (prod 11-06): 81 pool rows, 22 auto-blocked; 59 usable, most of them are not big vendors.

## Design
(filled in as it is built — see sections below)

## Tasks
- [ ] Plan doc + branch
- [ ] Agent: linger-0 close on every probe / ping / pin socket, ≤ 2 sockets open at once per machine, per-request
      `concurrency` (1 = one domain at a time), landing_reality 2; unit tests
- [ ] One-time pool small-site report (6 machines, 3 runs ≥ 1 h apart) → threshold
- [ ] Panel: pass 1 + 2 re-sample passes (top 5 + ≤ 2 试用 contenders, ≥ 5 s apart), worst-of-3 ranking
- [ ] Panel: per-server sample history (rolling), 试用 stability rule, reasons
- [ ] UI: 次数 / 抖动 columns, 「试用」 tag, help text; screens phone 390 + desktop 1440
- [ ] Tests: go test + contract SQLite + PostgreSQL, agent tests, tsc / build / lint
- [ ] TIME_WAIT proof (lab + NexKR), resource numbers
- [ ] Push, CI green, ledger row ready, final.md
