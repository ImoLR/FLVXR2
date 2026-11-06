# 223 — 共享落地服务器内存/CPU 上限 → 不限制

Branch `feature/3.0.27-shared-core-unlimited` from `3.0.27-fork.118` (5d0cc831). Build only; rides the next batch release.

User 2026-11-06 ~03:00 TW: 「共享服务器在转发面板上 CPU 和内存的限制取消掉，暂时都取消，不限制；共享过来的时候也不限制」.

## Facts
- `landing_server.core_memory_mb` / `core_cpu_percent` (256 / 50 defaults) were set for shared rows by the share sync (`repository_landing_shared.go`), but they were never sent to 妙妙屋: the shared push (`pushSharedLanding` → `SharedLandingDesired` → `landingcustom.Desired`) has no memory/CPU field. Only the native agent payload (`landing_runtime.go` `ApplyLandingDesired`, native rows only) carries them. So the numbers on the shared settings page were display-only and did not match what mmwx applied (128).
- Decision: keep sending nothing for shared servers (no older Custom/Helper can misread a new field); store 0 / 0 = 不限制 and show 「不限制 · 由共享方决定」 read-only.
- GORM gotcha: the model tag `default:256` / `default:50` makes `Create` skip a zero value → an explicit update after `Create` is needed.

## Tasks
- [x] Plan number: highest on any branch = 221 (222 reserved for release-1106) → 223
- [x] Backend: share sync writes 0/0 (fresh rows explicit update after Create; existing rows on Save)
- [x] Backend: save of a shared server forces 0/0 (read-only), validation allows 0 for shared only; native keeps ≥64 MiB / ≥1 %
- [x] Backend: startup data step — shared rows with non-zero core limits → 0/0 (idempotent, logs old values)
- [x] Frontend: server-settings shared → 「不限制」 read-only + 「由共享方决定」; native unchanged; validation skips shared
- [x] Tests: repo unit tests (sync, save, migration, native unchanged); go test ./...; contract SQLite + PostgreSQL
- [x] tsc / build / lint no new errors vs base
- [x] Screens 390 + 1440, shared + native → /root/mmwx-preview/shared-core-unlimited/
- [x] Resource numbers (landing list latency before/after)
- [x] Push, CI green (run 42112240754: Build Agent / Frontend / Go Backend / PostgreSQL Contract all success)
- [x] Ledger row (ready) + summary (runs/shared-core-unlimited-r2/summary.md)
- [x] r3: reverted out-of-scope `eslint --fix` reformatting (29 unrelated vite-frontend files back to 5d0cc831); only server-settings.tsx (2 plan hunks) remains; eslint (no-fix) 0 problems in that file, tsc 0, build ok

## Results (round 2, 11-06 TW)
- Item 4 re-checked: `internal/landingcustom` has no memory / CPU field at all; `core_memory_mb` / `core_cpu_percent` only appear in the native agent payload (`landing_runtime.go`, `Source != "native"` returns early). Shared push unchanged.
- New tests: `TestSharedCoreUnlimited` (fresh share → 0/0, save with 0/0 and with stale 512/80 → 0/0, existing row 256/50 → 0/0 on re-sync), `TestSharedCoreUnlimitedMigration` (shared 256/50 + 128/50 → 0/0, native 256/50 + 384/70 untouched, second run no-op), `TestLandingCoreLimitValidation` (native rejects 0/0, 63/50, 256/0), contract `TestLandingSharedCoreUnlimitedContract` (admin API: native stays 256/50 after share sync, native save 0/0 rejected, 300/60 kept; shared save → 0/0). SQLite + PostgreSQL 18 all pass.
- `go test ./...` (GOMAXPROCS=2, -p=1, PG DSN set, -timeout 40m): 25 packages ok, 0 FAIL (`internal/store/repo` 1248 s, `tests/contract` 726 s).
- Frontend: `tsc --noEmit` 0 errors; `npm run build` ok; `npm run lint` 173 problems (94 errors, 79 warnings) = identical to base fork.118, none in `server-settings.tsx`.
- Screens (local demo panel, synthetic DB): 12/12 checks, 0 console errors → `/root/mmwx-preview/shared-core-unlimited/` (index.html).
- Resource: `/api/v1/landing/server/list` (4 servers, 20 requests after 3 warm-up) base median 2.39 ms / p90 3.36 ms vs new 2.41 / 2.89 ms (2nd start 2.55 / 3.11) = no change. Startup migration = one SELECT over `landing_server` (median 4 µs on the demo DB) + one UPDATE per affected shared row on the first start only; later starts log nothing.
