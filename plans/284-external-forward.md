# 284 — 普通转发目标「我的外部节点」+ 目标名称改名 (build + screenshots only)

Task `external-forward` (11-18 TW, coordinator fa017f57; round 2 `external-forward-r2`). Base `3.0.27-fork.149`,
merged the prod tags released while this was building: `3.0.27-fork.150` (plan 280 规则→转发 + user.leikwan_num),
`3.0.27-fork.151` and `3.0.27-fork.152` (plan 283 quic-block), branch
`feature/3.0.27-external-forward`, worktree `/root/projects/flvxx/flvxx-284`. Panel only, no agent change.
**Not merged / tagged / released** — the user looks at the screenshots first.

## Design (agreed decisions, see brief)
- Target choices renamed: 「我的落地节点」→「我的面板节点」, 「我自己的节点」→「我的外部节点」 (dialog + hint/title
  texts that name the choices). Pages / nav / 落地 page unchanged. 「手动填写」 stays.
- Normal tunnels: new target 「我的外部节点」 = pick from the owner's 外部节点 list (or 「＋ 粘贴新节点」 →
  `AddExternalDialog`, same list, cap 20). Stays an ordinary port forward (same agent payload); the target
  host:port comes from the node and is read-only while linked.
- DB: additive `forward.external_node_id` (int64, NOT NULL DEFAULT 0, index). AutoMigrate on SQLite + PG.
- 线路机 tunnels: option 「我的外部节点」 keeps the plan 108 chain; the chain create/update also accepts
  `chainExternalId` (resolved server-side to the same upstream as pasting its link). Chain forwards are not
  linked to the list (their upstream stays a copy, as today).
- Link = the external node's upstream re-pointed at each entry address:port (`forwardEntries`, one per entry
  address, IPv6 bracketed); domain server without SNI → `sni=<domain>`, ws without Host → `host=<domain>`;
  Reality untouched; SS / SOCKS / HTTP / plain VMess = address swap only. Name = forward name
  (+ « · 入口名» with several entries, same rule as 「我的外部节点」 chain links).
- Protocol filter (backend refuses, UI greys with reason): 屏蔽 TLS ⇒ trojan, vless/vmess tls+reality, https
  proxy, hysteria2 (QUIC, plan 283); 屏蔽 HTTP ⇒ http proxy + ws without TLS; 屏蔽 SOCKS ⇒ socks5;
  屏蔽其它协议 ⇒ SS / VLESS Enc / plain vless/vmess tcp/grpc. Every forward mode carries TCP + UDP, so the
  UDP need of Hysteria2 never greys on its own.
- Read-time auto-match: a plain forward whose single target equals exactly one of the owner's external nodes
  (and the tunnel filter lets it through) is shown as linked (「自动识别」); no DB write.
- Delete guard: an external node linked by a forward (explicit only) cannot be deleted; rename allowed.

## Notes
- Bridge `Select` is a native `<select>` unless `listboxVariant="grouped"`: the 外部节点 picker uses the grouped
  listbox so each greyed row can show its reason (目标类型 stays native, unchanged).
- 利群汇聚 pickers (「选择我的落地节点」) and the sub editor 来源 label 「我的落地节点」 (落地 直连 group) are not
  the 转发 target choice and were left as they are.
- Chain forwards created from the list are not linked to it (the chain keeps its own copy of the upstream, as
  before); only normal-tunnel forwards count as 「N 条转发在用」.

## Checklist
- [x] Backend: model column + ForwardRecord + create/update params
- [x] Backend: filter features for upstreams + tunnel `blockedFilters`
- [x] Backend: create / update / batch change-tunnel / copy (frontend) / backup import paths
- [x] Backend: chain create/update from `chainExternalId`
- [x] Backend: forward list decoration (`externalNode`), links endpoint, auto-match
- [x] Backend: external list uses + delete guard
- [x] Backend: subscription panel links + pool labels + plain count
- [x] Backend tests (link rewrite per protocol, create/update, guard, sub, auto-match, chain parity)
- [x] Frontend: rename texts
- [x] Frontend: rule-target 「我的外部节点」 picker (normal + 线路机), paste dialog, hint
- [x] Frontend: forward rows (tag, → 名, 复制链接, no 设置路由), H5
- [x] Frontend: externals list 「N 条转发在用」 + refusal; sub editor item labels
- [x] Round 2: finish the `3.0.27-fork.150` merge (both renames kept: 「我的外部节点」 + 规则→转发; leikwan_num untouched)
- [x] Round 2: sub pull cold render back to 15 statements (SubPanelLinks hands its 外部节点 rows to the render,
      no second `landing_external_node` query); picker contract keeps `blockedFilters` (used by the picker) and
      now also checks users get no key outside the picker list
- [x] Round 2: PG run found the 转发 list query broken on PostgreSQL since fork.100 (`LEFT JOIN user` /
      `user.name` — `user` is reserved in PG; prod is SQLite so never seen): quoted as `"user"` (same on SQLite)
- [x] Round 2: prod moved to `3.0.27-fork.151` (plan 283 quic-block) while building → merged it too (clean;
      only agent + ws counter + node/tunnel page, nothing plan 284 touches). fork.152 was not on prod yet → later
      prod moved to fork.152 (agent QUIC log only) → merged it as well (clean, no panel change)
- [x] Round 2: picker hint only claims 「只能选 SS、VLESS Encryption」 when TLS + HTTP + SOCKS are all blocked;
      外部节点 list note mentions forwards
- [x] go test ./... (SQLite) + PG contract where touched; npm run build — SQLite all green (also after the
      fork.151 merge); PG (temp postgres:18) contract + repo + handler green; `npm run build` ok
- [x] Push branch, CI green (CI Build Check 44325747555: frontend, backend, PG contract, agent)
- [x] Screenshot gallery `/root/mmwx-preview/external-forward/` (40 PNG, a–i, desktop 1440 + H5 390, dark for
      b3 / f; lab = scratch SQLite + isolated netns, 27/27 checks, 0 page errors) — http://104.145.236.29:8791/external-forward/
- [x] Ledger row 284, summary (`runs/external-forward-r2/summary.md`), cache cleanup

## Status
BUILT 11-18 TW, waiting for the user's look at the gallery. No merge to main, no tag, no release, prod untouched.
