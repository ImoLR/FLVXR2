# 164 — line-compliance-ack: admin 「已确认合规」 tick + 「线路机不合规 N 条」 jumps to the rule

User 2026-10-29 (TW evening). Prod rule #109「Usb」 (user imolr, tunnel 74「拼车hinet1 Dmit」, entry node 49 = 线路机) has a
hand-typed target `boilhinet.imgamer.top:10020`; it really is VLESS + Reality + xtls-rprx-vision (mmwx server 12, checked
read-only), but the panel can't know that (only landing-list targets are checked), so it shows 「线路机不合规 1 条」 and the
rule's 「不合规」 chip. User asked for (1) a small admin tick that makes the panel ignore it, (2) a click on the toolbar chip
that jumps to the rule: its tunnel group, expanded, the rule located.

BUILD ONLY — branch `feature/3.0.27-line-compliance-ack` from `origin/feature/3.0.27-rule-tunnel-groups` (plan 163).
Released later in a batch.

## Design

Backend
- `forward.line_compliance_ack_sig` (varchar(80), not null, default '') — add-only column. '' = not acknowledged; otherwise
  the signature of the target the admin acknowledged: `v1:` + sha256(tunnel_id | landing_node_id | normalized remote_addr).
- Read-time check (safety net for every write path, incl. a rollback to fork.95 and back): in the existing batched
  `decorateForwardChains` pass, a non-compliant rule counts as acknowledged only when its stored signature equals the
  signature of its current target. One extra batched query, and only when the list has non-compliant hand-typed rules.
- Eligible: rule on a 线路机 tunnel, not wg_path, no chain row, `landing_node_id = 0` (hand-typed target — the panel can't
  judge it). Landing-node targets are known not to be Reality, chain rules are system-managed → never acknowledgeable.
- Explicit clears (so A→B→A does not bring an old ack back): `UpdateForward` keeps the signature only when tunnel /
  remote_addr / landing are unchanged (SQL `CASE` on the old row, same statement); `UpdateForwardTunnel` (batch 换隧道,
  tunnel-delete migration), `SetForwardRemoteAddr` (chain target), `SetForwardLandingNode` clear it on a change; backup
  import overwrites it with ''; a failed edit's rollback restores the old value.
- Set: admin only. (a) `lineComplianceAck: true|false` in `/forward/update` (rule dialog; applied after the save succeeded,
  ignored for non-admins); (b) `POST /api/v1/forward/line-compliance-ack {id, ack}` (admin only, 403 otherwise; DB-only, no
  node sync — for scripts / the release worker).
- List: acknowledged → no `lineNonCompliant`, not counted in `lineNonCompliantCount`; admins also get
  `lineComplianceAck: true` (users get nothing).

Frontend (`forward.tsx`, `chain-links.tsx`)
- Rule dialog (edit, admin, 线路机 tunnel, target 手动填写 unchanged tunnel, rule not compliant or acknowledged):
  checkbox 「已确认合规」 + helper text.
- Acknowledged rule (admin): small green 「已确认」 chip instead of 「不合规」.
- Toolbar chip 「线路机不合规 N 条」 is clickable: jumps to the next non-compliant rule in display order (wrap around),
  shows 「k/N」 after the first click. Makes it visible: resets filters that hide it (all users), right page, opens its tunnel
  group for this visit only (not saved — the saved open/closed state is untouched, a reload shows it collapsed again), scrolls
  it to the centre and flashes it ≈2 s (background/box-shadow animation, no layout change). Works in 分组 / 列表 / 卡片.
- Collapsed group header: orange 「N 条不合规」 hint next to 已暂停/异常 (from the loaded list, no request).

## Checklist
- [x] Backend: column + signature + repo set/get + clears on every target write path + rollback restore + import
- [x] Backend: decorate pass honours the ack (count, flags, admin vs user) + update field + admin endpoint + route
- [x] Backend tests: set/clear admin vs non-admin, target change clears, tunnel move clears, A→B→A stays cleared, count
      excludes acked, user view, rollback-safe signature check, ineligible rules refused
- [ ] Full `go test ./...` SQLite and PostgreSQL green (base compared for any failure)
- [ ] Frontend: dialog checkbox, 已确认 chip, group header hint, clickable chip with jump/expand/scroll/highlight + k/N
- [ ] tsc / build / lint — no new errors
- [ ] Local demo panel (seeded, no prod) + screens 390/1440 light/dark → `/root/mmwx-preview/line-compliance-ack/`
- [ ] Commit, push, CI green
- [ ] `runs/line-compliance-ack/release-notes.md` + summary/final.md, cleanup
