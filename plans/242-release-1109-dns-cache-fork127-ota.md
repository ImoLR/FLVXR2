# 242 — release-1109: fork.127 (plan 240 本机 DNS 缓存常驻服务) + 全网 agent OTA

Base: `feature/3.0.27-dns-cache-default` @ a2814fa9 (code 1455ba60, base fork.126 a5966795) + docs 238 (a3fdb94d) + 241 (16edb50a).
Branch `maintenance/3.0.27-fork.127-release-1109`. Run dir `/root/flvx-workers/runs/release-1109-dns-cache`.

Brief: release ledger row 240 as `3.0.27-fork.127`, panel upgrade (backup first), fleet OTA of every online FLVX agent
(canary Leikwanhost 沪台IPLC, then one host at a time; skip Breadcloud JP2 = mmwx-prod and offline Nobrand ×2; never
04:40–06:10 TW), Dmit Echo last via panel OTA + one 「修复 DNS 缓存」 (resolv.conf backed up first; never touch its
`flux_agent.service`). Expected chip: 正常 on the 33 configured hosts, 精简模式 on Lightlayer TW.

## Tasks
- [x] branch + plan doc, gates (backend build/vet/test, go-gost/x dnscache + socket + landing tests) vs base
- [ ] push branch, branch CI green, tag `3.0.27-fork.127`, Images workflow, release Latest + assets validated
- [ ] panel backup + rollback dir (no schema change → image swap), upgrade /opt/flvx-svc, health + node state
- [ ] OTA canary Leikwanhost 沪台IPLC (backup, RSS before/after, chip state, ensure no-op check)
- [ ] OTA the rest one host at a time (SSH backup each), chip state after each
- [ ] Dmit Echo: panel OTA, resolv.conf backup, 「修复 DNS 缓存」 once, report state
- [ ] ensure no-op proof on 3 hosts (DNS files vs plan-239 backup timestamps, agent/core PIDs)
- [ ] fleet DNS-cache state table, ⚠ list
- [ ] ledger 240 → 已发布, prune backups, summary, plan complete

## Results
