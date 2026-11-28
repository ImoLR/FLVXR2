import type { NodeDDNSView } from "@/api/ddns";
import type { NodeDNSCacheView } from "@/api";

import { ddnsBadgeState } from "./ddns-badge";
import { ipv6RaBadgeState } from "./ipv6-ra-badge";
import { dnsCacheState } from "./dns-cache-badge";
import { nodeVendorAttention } from "./vendor-field";
import {
  NODE_EXPIRING_SOON_DAYS,
  getNodeRenewalSnapshot,
  isNodeExpiryReminderDismissed,
} from "./renewal";

// Plan 324: the marker on a 节点 page group tab (admin only). Built from the
// SAME functions the card badges use, so a tab never disagrees with its cards.
// The rule list below is the one place to add / drop a rule (e.g. a later
// 连接数 threshold). NOT counted on purpose: 节点提示 (admin free text), plain
// agent version lag, info-only tags.

export type NodeAttentionLevel = "danger" | "warning";

export interface NodeAttention {
  level: NodeAttentionLevel;
  text: string;
}

export interface NodeAttentionInput {
  id: number;
  name: string;
  isRemote?: number;
  /** Already respects the offline grace (use-node-offline-timers). */
  connectionStatus: "online" | "offline";
  ddns?: NodeDDNSView | null;
  ipv6RaStatus?: string;
  dnsCache?: NodeDNSCacheView | null;
  vendors?: string[];
  vendorOther?: string;
  expiryTime?: number;
  renewalCycle?: string;
  expiryReminderDismissed?: number;
  expiryReminderDismissedUntil?: number | null;
}

type Rule = (node: NodeAttentionInput) => NodeAttention | null;

const RULES: Rule[] = [
  // 🔴 offline — the same state as the card's red dot.
  (n) =>
    n.connectionStatus === "offline" ? { level: "danger", text: "离线" } : null,
  // 🟡 DDNS 失败 / 待同步 / 需升级节点 (「已同步」 is fine).
  (n) => {
    const b = ddnsBadgeState(n.ddns);

    return b && b.color !== "success"
      ? { level: "warning", text: b.label }
      : null;
  },
  // 🟡 IPv6 RA 待恢复 / 需处理.
  (n) => {
    const s = ipv6RaBadgeState(n);

    return s && s.color !== "success"
      ? { level: "warning", text: s.label }
      : null;
  },
  // 🟡 本机 DNS 缓存 warning tone (未启用 / 未运行).
  (n) => {
    const s = dnsCacheState(n);

    return s?.tone === "warning"
      ? { level: "warning", text: `DNS 缓存${s.label}` }
      : null;
  },
  // 🟡 VPS 厂商 未设置 / 不在列表.
  (n) => {
    const a = nodeVendorAttention(n);

    if (!a) return null;

    return {
      level: "warning",
      text: a.level === "error" ? "厂商不在列表" : "未设置 VPS 厂商",
    };
  },
  // 🟡 到期 ≤ 7 天 / 已过期 (the card's amber / red accent; 「已续费」 dismissed = none).
  (n) => {
    if (isNodeExpiryReminderDismissed(n)) return null;
    const r = getNodeRenewalSnapshot(
      n.expiryTime,
      n.renewalCycle,
      NODE_EXPIRING_SOON_DAYS,
    );

    if (r.state === "expired") return { level: "warning", text: "已过期" };
    if (r.state === "dueSoon") return { level: "warning", text: r.label };

    return null;
  },
];

export function nodeGroupAttention(node: NodeAttentionInput): NodeAttention[] {
  const out: NodeAttention[] = [];

  for (const rule of RULES) {
    const a = rule(node);

    if (a) out.push(a);
  }

  return out;
}

export interface GroupAttentionEntry {
  id: number;
  name: string;
  items: NodeAttention[];
}

export interface GroupAttention {
  /** Red wins over amber; null = clean. */
  level: NodeAttentionLevel | null;
  /** Machines with a problem (not problems). */
  count: number;
  entries: GroupAttentionEntry[];
}

export function groupAttention(
  nodes: NodeAttentionInput[],
  byNode: Map<number, NodeAttention[]>,
): GroupAttention {
  const entries: GroupAttentionEntry[] = [];
  let level: NodeAttentionLevel | null = null;

  for (const n of nodes) {
    const items = byNode.get(n.id) || [];

    if (items.length === 0) continue;
    entries.push({ id: n.id, name: n.name, items });
    if (items.some((i) => i.level === "danger")) level = "danger";
    else if (!level) level = "warning";
  }
  // Offline first, then by name order as on the page.
  entries.sort(
    (a, b) =>
      Number(b.items.some((i) => i.level === "danger")) -
      Number(a.items.some((i) => i.level === "danger")),
  );

  return { level, count: entries.length, entries };
}
