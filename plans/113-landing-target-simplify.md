# 113 — Rule landing target: one place creates nodes (mmwx-style)

Branch `feature/3.0.27-landing-target` (from plan 108 head 40e42ed8). No release until the user approves screenshots;
ships together with 108.

## Why
Today a landing node can be created in two places: on the landing page, and implicitly in the rule dialog via
「落地服务器（自动建节点）」. The rule path ALWAYS creates a new node (never reuses), the node becomes a normal
「我的节点」 entry that counts against the node cap and survives rule deletion, and editing a rule creates yet another one.
On 线路机 tunnels nothing is auto-detected: an SS node or SS protocol is just refused.

mmwx (miaomiaowuX) keeps one object — the node. A forward chain either binds an existing node (any protocol, creates an
entry-variant link) or creates a chain-owned VLESS node; users never get nodes created behind their back.

## Target model
- Landing nodes are created in ONE place (the create-node dialog), reachable from the landing page and from the rule dialog.
- A rule only REFERENCES a landing node (except 108 「我自己的节点」 chain nodes, which are rule-owned implementation details).
- One node may be used directly and by several rules; billing already counts the landing once.

## Checklist
- [ ] Rule dialog: remove 「落地服务器（自动建节点）」; 目标类型 = 手动填写 / 我的落地节点 / (线路机) 我自己的节点
- [ ] 我的落地节点 picker grouped by server; incompatible nodes shown disabled with reason (线路机 → Reality only, stopped,
      server unavailable); empty state
- [ ] 「＋ 新建落地节点」 in the picker opens the shared create-node dialog (owner = rule owner, protocol locked on 线路机),
      auto-selects the new node
- [ ] Reuse hint in the create dialog when the user already has a compatible node on that server (「使用它」 / 「仍然新建」)
- [ ] Backend `landingServerId` = reuse-or-create (API compat for stale frontends), per-user single-flight, never another
      user's or a chain-internal node
- [ ] Landing node lists show 「规则 N」 (one grouped query); delete refusal names the referencing rules
- [ ] Rule rows targeting a landing node offer 「复制链接」 (entry-variant link) — verify existing, add if missing
- [ ] Tests (handler + repo + acceptance runner extension), full go test vs baseline, frontend build/lint
- [ ] Resource numbers, screenshot gallery /landing-target/, Chinese summary
