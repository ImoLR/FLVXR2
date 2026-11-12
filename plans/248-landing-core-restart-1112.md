# 248 — Restart native landing Cores for plan 244 handshake 120 s (11-12, ops)

Task `landing-core-restart` (run dir /root/flvx-workers/runs/landing-core-restart). User 11-12 20:50 TW: restart now.
Scope: only FLVX native landings Bestvm US (node 17, landing server 9) and NexKR (node 55, server 5), unit
`flvx-landing-core.service`. Shared mmwx landings (Boil Hinet 马年 / Dmit T1 / PQS TW / Boil Hinet 158) untouched;
flux_agent untouched. Restart method = the agent's own `restartCore` path (`systemctl restart flvx-landing-core`);
the panel's confirm-restart switches (egress / core upgrade) don't apply (egress ready, no stale epoch).

## Checklist
- [x] Pre-check: panel fork.129, both landings online, Core running, live config handshake, conn counts
- [x] Restart Core on Bestvm US (no landing nodes; no FLVX rule targets it) → Core up, handshake 120, inbounds re-applied
- [x] Restart Core on NexKR (1 landing node 自己用kr) → Core up, handshake 120, direct-link proxy request OK
- [x] Health check: all FLVX nodes online; one request per tunnel with rules; pool counts; 预热 tags; PQS TW / 「TW」 rule
- [x] Logs on 3 sample hosts
- [x] Re-check tags after ~30 min
- [x] summary.md + memory notes

## Result (11-12 21:28 TW)
- Bestvm US restarted 20:52:25 TW, NexKR 20:54:40 TW; 0 live conns each; inbounds back in ~4 s.
- NexKR idle conn reset 4.2 s → 120.3 s. Bestvm US only has a guarded Reality chain inbound (rule 602, created by
  user 74 at 20:52:03; re-applied after restart; e2e OK; tunnel 116 tag 预热 −4.6 ms).
- 28/28 nodes online; rule diagnose identical to the release (same 5 partial: 6/37/69/77/95).
- Real proxy requests OK via rules 156 (PQS TW, now 118.170.89.219), 346, 347, 602.
- PQS TW 「TW」 hop tag = 不支持 (target closes pooled idle conns early; mmwx side, read-only).
- Backups: /root/flvx-landing-core-backup-20261112-pre-restart on both hosts.
