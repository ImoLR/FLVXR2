# 126 · fork.68 — Mieru UDP hint (panel only)

User 2026-10-22 ~14:30 (台湾): after the Mieru transport screenshots, 「提示你加上去吧」.
Measured in embedded-share-mmwx: 10 active Mieru UDP connections ≈ 19 % server CPU, UDP upload slow.

Base: `origin/maintenance/3.0.27-fork.67-agent-fixes` (= prod fork.67). Branch
`maintenance/3.0.27-fork.68-mieru-hint`. Text-only, add-only frontend change; no backend / DB / agent
change, TCP stays the default. Run dir `/root/flvx-workers/runs/release-fork68-mieru-hint`.

## Tasks
- [x] Shared hint text + `isMieruUdp` helper in `api/landing.ts`
- [x] 落地服务器设置「Mieru 客户端传输」: hint sentence in the description (TCP), warning note when UDP
- [x] 创建落地节点 dialog: warning line when the server's Mieru transport is UDP
- [x] 我的节点 (desktop table + cards/H5) and 节点链接 window: warning line for UDP Mieru nodes
- [x] tsc + lint + build; backend build sanity check
- [x] Screenshots before/after (desktop + H5, light + dark, TCP + UDP) → run dir `screens/` + `index.html`
- [ ] Commit, push, CI green, tag `3.0.27-fork.68`, release Latest with the same asset set
- [ ] Prod panel backup (`pre-fork68-*`), prune to 2 backups, upgrade, verify health / version / nodes
- [ ] Observe fork.67 agents (21 / 48 / 53 and all) stay on wss during the panel restart
- [ ] Summary + plan marked complete

## Notes
- Hint text `MIERU_UDP_HINT` = 「UDP 更耗服务器 CPU、上传较慢，一般建议用 TCP」. TCP selected → appended to the select
  description; UDP selected → description unchanged + warning note (`bg-warning-50 text-warning`, dark `bg-warning-500/10`).
- Create dialog / 我的节点 (table + card) / 节点链接: warning line only for Mieru nodes (or server default) on UDP.
- tsc 0 errors; eslint 0 errors (16 warnings, all on pre-existing lines); vite build OK; go-backend build OK.
- Screenshots: local demo panel (fork.66 demo seed + a UDP Mieru node), 21 before/after pairs, 21/21 checks pass,
  0 console errors → `runs/release-fork68-mieru-hint/screens/index.html`.
