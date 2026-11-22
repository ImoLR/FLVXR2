# 290 — Reality 域名池: auto-block Cloudflare-hosted domains + admin ⚠ when 防盗 is not in effect

Task `reality-cf-check` (11-22 TW, user: 「这个可以做，但不要影响到转发效率以及延迟」). Base 3.0.27-fork.157,
branch `feature/3.0.27-reality-cf-check`, release → fork.158 (panel only).

## Why
DMIT 11-22 notice: proxies whose unauthenticated fallback reaches Cloudflare are abused as botnet C2 relays
(「偷 CF」). Reality relays every unauthenticated connection to `dest`; Cloudflare routes by SNI, so a dest on
Cloudflare lets anyone reach any CF site through the landing. Today only `www.cloudflare.com` is blocked, by name.

## Design (control plane only — nothing on the forwarding path)
- `internal/cfnet`: built-in Cloudflare IPv4 + IPv6 ranges (= api.cloudflare.com/client/v4/ips, etag
  38f79d050aa027e3be3865e495dcc9bc, checked 11-22). Built-in list is the source of truth; no runtime refresh.
- `landing_reality_domain` + 2 default columns: `cf_hosted` (bool, default false), `cf_evidence` (text, default '').
  Effective block = manual `blocked` OR `cf_hosted`; manual blocks are never touched by the check.
- Evidence = the panel's own A/AAAA lookup (cached 6 h, failures keep the previous verdict) + the IPs the landings
  already reported (stored 测速 table `reality_probe` IPs/best IP and `reality_pins`, ≤ 48 h old).
- When: admin add (immediately), pool import (right after), the existing 10-min reality loop every 6 h (incl. the
  built-in seed rows, first run 3 min after start), and inside every 测速 record (a candidate the landing resolved
  to a CF IP is excluded from that pick and its row is marked).
- Lift: a CF-blocked domain is unblocked when the panel lookup succeeds with no CF IP and no fresh landing evidence.
  A geo-DNS domain that is on Cloudflare only from some landings may flip daily (lifted → next 测速 on that landing
  re-marks it before its pick), but it is never picked by a landing that resolves it to Cloudflare.
- Admin cannot unblock a CF-hosted domain (decision: not allowed — refused with the reason; it lifts itself when the
  domain leaves Cloudflare). A manual 伪装域名 that is CF-hosted is refused on save.
- Existing nodes are never changed. Admin-only ⚠ (plan 218 `serverAdminAttention`) on the 落地 server card when
  (a) the server default dest or a live VLESS Reality node's dest is CF-hosted, or (b) the server has live VLESS
  Reality nodes and 防盗 is not in effect (off / unsupported share / idle while nodes exist / share error) — states
  already flagged by plan 218 (native restart / error / agent_old) are not duplicated.

## Checklist
- [x] Worktree from fork.157, plan doc
- [x] cfnet range matcher + tests (v4/v6 edges)
- [x] model columns + repo: effective block, verdict apply/lift, landing evidence, probe-record exclusion, unblock refuse
- [x] handler: cached resolver, CF check job (add / pool / 6 h loop), manual dest refuse, server `realityRisk` view
- [x] backend tests: matcher, auto-block / lift / manual kept, probe exclusion, marker conditions; contract for payload
- [x] frontend: 域名池 row pill + reason, no 解除屏蔽 for CF rows, 测速 table pill, ⚠ reasons
- [x] `go vet`, `go test ./...` (compare failures with fork.157), `npm run build`
- [x] lab (isolated netns, hosts-file fake CF domain): blocked with reason, non-CF untouched, ⚠ for guard-off server;
      screenshots desktop + 390 px → gallery /reality-cf-check/
- [x] prod pool pre-scan (read-only, DNS only) — expected only www.cloudflare.com
- [x] release fork.158: tag, CI + Images + assets, Latest
- [x] prod backup + rollback script, prune backups, swap, verify (nodes, forwards, 利群 tables, log 15 min, 5 busiest
      forwards RTT), report pool scan + ⚠ markers
- [x] ledger + memory, cleanup

## Results
- Prod pre-scan 11-22 (read-only DB, DNS from the dev box): 102 candidates = every live dest (all dests are
  candidates), 0 lookup failures, only Cloudflare hit = `www.cloudflare.com` (104.16.123.96 / .124.96), already
  blocked by name. No stored 测速 table / pin of any server shows a Cloudflare IP.
- Tests: `go vet ./...` clean; `go test -p 1 ./...` (SQLite) all green; PG (postgres:18 throwaway container)
  repo + handler + contract green after fixing my own new contract test's field-order string (no pre-existing
  failures this time). `npm run build` ok (PWA precache 49 entries, 930 KiB).
- Lab (isolated netns, synthetic DB, hosts-file fake domains, real lab panel, no mocks): 30/30 checks — custom
  cf-lab (104.21.55.66) blocked at add with reason, unblock refused; akamai-lab untouched; pool cf-pool blocked by the
  loop's first check, fastly-pool untouched; www.cloudflare.com marked next to its name block; ⚠ on the guard-off,
  CF-dest and shared-no-guard servers only; users see nothing new; no page errors. Gallery
  http://104.145.236.29:8791/reality-cf-check/ (15 PNG, desktop + 390 px).
- Rollback check on a prod DB copy (isolated netns): fork.158 added cf_hosted / cf_evidence and marked only
  www.cloudflare.com; fork.157 then started cleanly on the same DB. Copy deleted.
- Release: tag 3.0.27-fork.158 → 8bcb87e9 (branch maintenance/3.0.27-fork.158-reality-cf-check = fork.157 + merge
  of the feature head df103365, same tree), CI 45021792526, Images 45021794787 green, Latest, 10 assets (same set as
  fork.157), compose images :3.0.27-fork.158, PINNED_VERSION / REPO ok, gost-amd64 sha256 ok.
- Prod: backup + rollback `/opt/flvx-svc/rollback/pre-fork158-20261122T123303Z/rollback.sh` (→ fork.157); prune kept
  pre-fork157 + pre-fork158 (pre-fork156 + fork.155 images removed). Swap 20:33:04–20:33:12 TW.
  First Cloudflare check 20:36:13 TW: only `www.cloudflare.com` marked (104.16.124.96, panel lookup). realityRisk: no
  ⚠ on any server (Reality listeners only on Boil Hinet 158 ×1, Bestvm US ×11, Bestvm JP B ×1 — guard on everywhere;
  no dest on Cloudflare). State before/after identical (29/31 online — 火山广州 / 火山上海 offline before the swap too,
  64 forwards, 利群 tables), log 15 min clean (only 火山上海 timeouts, offline before), 5 busiest forwards: diagnose
  3/3 each, latency unchanged (26.5→26.6, 0.8→0.9, 26.6→26.4, 1.0→1.0, 33.2→32.8 ms). Backend RSS 66 MiB after.
