import type { ReactNode } from "react";

import { useCallback, useEffect } from "react";

import { LandingTone, Pill } from "./pill";
import {
  AdminAttention,
  AttentionIcon,
  AttentionLevel,
} from "./admin-attention";

import { LandingNode, LandingServer } from "@/api/landing";
import { TreeGroupChevron, TreeGroupHeader } from "@/components/tree-group";
import { useLocalStorageState } from "@/hooks/use-local-storage-state";
import {
  compareRegions,
  regionFlag,
  regionName,
  regionTone,
} from "@/utils/region";

// Plan 286 — 落地 page groups. Admin: 落地服务器 by region, 全部节点 by owner.
// Round 2: users get the same look — 可用落地 by region, 我的节点 by 落地服务器 —
// and every group starts collapsed. Grouping is client-side over the lists the
// page already loaded.

const expandedList = (value: unknown): string[] =>
  Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];

/**
 * Open / closed state of a set of groups. Round 2: only the EXPANDED keys are
 * stored (per browser, localStorage), so every group — also a new one — starts
 * collapsed. `legacyKeys` (round-1 open/closed maps, never released) are
 * removed once.
 */
export function useGroupOpenState(storageKey: string, legacyKeys: string[]) {
  const [stored, setStored] = useLocalStorageState<string[]>(storageKey, []);
  const legacy = legacyKeys.join("\n");

  useEffect(() => {
    if (!legacy) return;
    try {
      legacy.split("\n").forEach((key) => localStorage.removeItem(key));
    } catch {}
  }, [legacy]);
  const expanded = new Set(expandedList(stored));
  const isOpen = (key: string) => expanded.has(key);
  const toggle = (key: string) =>
    setStored((prev) => {
      const list = expandedList(prev);

      return list.includes(key)
        ? list.filter((item) => item !== key)
        : [...list, key];
    });
  // Jumps (⚠ 查看, 创建节点 …) open their target group.
  const open = useCallback(
    (keys: string[]) =>
      setStored((prev) => {
        const list = expandedList(prev);

        if (keys.every((key) => list.includes(key))) return prev;

        return [...list, ...keys.filter((key) => !list.includes(key))];
      }),
    [setStored],
  );

  return { isOpen, toggle, open };
}

// ---------------------------------------------------------------- servers

export interface ServerRegionGroup {
  key: string;
  region: string;
  servers: LandingServer[];
  offline: LandingServer[];
  attention: LandingServer[];
  attentionLevel: AttentionLevel;
  upgrade: LandingServer[];
  /** Users only (round 2): 已停用 servers, as their card says. */
  disabled: LandingServer[];
}

export const serverRegionCode = (server: LandingServer) =>
  (server.region || "").toUpperCase();
export const serverRegionKey = (server: LandingServer) =>
  `region:${serverRegionCode(server) || "none"}`;

export function groupServersByRegion(
  servers: LandingServer[],
  attentionById: Map<number, AdminAttention | null>,
  admin: boolean,
): ServerRegionGroup[] {
  const groups = new Map<string, ServerRegionGroup>();

  for (const server of servers) {
    const key = serverRegionKey(server);
    let group = groups.get(key);

    if (!group) {
      group = {
        key,
        region: serverRegionCode(server),
        servers: [],
        offline: [],
        attention: [],
        attentionLevel: "warn",
        upgrade: [],
        disabled: [],
      };
      groups.set(key, group);
    }
    group.servers.push(server);
    // Same states as the card: 离线 = enabled but not online (停用 is the
    // admin's choice, not a problem).
    if (server.status === "active" && !server.online)
      group.offline.push(server);
    // Users: only what their card shows (离线 / 已停用); ⚠ and 待升级 are
    // admin states.
    if (!admin) {
      if (server.status !== "active") group.disabled.push(server);
      continue;
    }
    const attention = attentionById.get(server.id);

    if (attention) {
      group.attention.push(server);
      if (attention.level === "error") group.attentionLevel = "error";
    }
    if (server.egress?.coreUpgradeRequired) group.upgrade.push(server);
  }

  return [...groups.values()].sort((a, b) =>
    compareRegions(a.region, b.region),
  );
}

const names = (items: { name: string }[]) =>
  items.map((item) => item.name).join("、");

export function ServerRegionHeader({
  group,
  open,
  onToggle,
}: {
  group: ServerRegionGroup;
  open: boolean;
  onToggle: () => void;
}) {
  const flag = regionFlag(group.region);

  return (
    <TreeGroupHeader
      className={`flvx-landing-region-head${open ? " flvx-landing-region-head-open" : ""} flex items-center justify-between gap-2 px-2 py-1.5 md:px-3`}
      collapsed={!open}
      onToggle={onToggle}
    >
      <span className="flex min-w-0 items-center gap-2">
        <TreeGroupChevron collapsed={!open} />
        {flag && (
          <span aria-hidden className="shrink-0 text-base leading-none">
            {flag}
          </span>
        )}
        <span className="min-w-0 truncate text-sm font-semibold text-foreground">
          {regionName(group.region)}
        </span>
        <span className="flvx-landing-region-count shrink-0 whitespace-nowrap text-xs font-medium">
          {group.servers.length} 台
        </span>
      </span>
      {/* Problems only; an all-good region shows nothing here. */}
      <span
        className="flex shrink-0 items-center gap-1.5"
        data-region-summary=""
      >
        {group.attention.length > 0 && (
          <span
            className="inline-flex items-center"
            data-region-attention={group.attentionLevel}
            title={`${group.attention.length} 台需要处理：${names(group.attention)}`}
          >
            <AttentionIcon level={group.attentionLevel} />
          </span>
        )}
        {group.offline.length > 0 && (
          <Pill title={`离线：${names(group.offline)}`} tone="red">
            离线 {group.offline.length}
          </Pill>
        )}
        {group.upgrade.length > 0 && (
          <Pill
            title={`落地 Core 待升级：${names(group.upgrade)}`}
            tone="amber"
          >
            待升级 {group.upgrade.length}
          </Pill>
        )}
        {group.disabled.length > 0 && (
          <Pill title={`已停用：${names(group.disabled)}`} tone="slate">
            已停用 {group.disabled.length}
          </Pill>
        )}
      </span>
    </TreeGroupHeader>
  );
}

/** The region group box: header + (when open) the unchanged card grid. */
export function ServerRegionSection({
  group,
  open,
  onToggle,
  children,
}: {
  group: ServerRegionGroup;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  return (
    <section
      className="flvx-landing-region"
      data-landing-region-group={group.key}
      data-region-tone={regionTone(group.region)}
    >
      <ServerRegionHeader group={group} open={open} onToggle={onToggle} />
      {open && children}
    </section>
  );
}

// ------------------------------------------------------------------ nodes

/** One header problem kind; the row shows the same state (row helpers). */
export type NodeProblemKey =
  | "error"
  | "sync"
  | "scan"
  | "expired"
  | "quota"
  | "conn"
  | "pending"
  | "expiring"
  | "quota90";

export const NODE_PROBLEMS: {
  key: NodeProblemKey;
  tone: LandingTone;
  label: string;
}[] = [
  { key: "error", tone: "red", label: "异常" },
  { key: "sync", tone: "red", label: "同步失败" },
  { key: "scan", tone: "red", label: "扫描禁用" },
  { key: "expired", tone: "red", label: "已到期" },
  { key: "quota", tone: "red", label: "流量用完" },
  { key: "conn", tone: "red", label: "连接满" },
  { key: "pending", tone: "amber", label: "待同步" },
  { key: "expiring", tone: "amber", label: "即将到期" },
  { key: "quota90", tone: "amber", label: "流量≥90%" },
];

export interface NodeUserGroup {
  key: string;
  userId: number;
  name: string;
  admin: boolean;
  nodes: LandingNode[];
  used: number;
  problems: Map<NodeProblemKey, LandingNode[]>;
}

export const nodeUserKey = (node: LandingNode) => `user:${node.userId}`;

const addProblems = (
  problems: Map<NodeProblemKey, LandingNode[]>,
  node: LandingNode,
  keys: NodeProblemKey[],
) => {
  for (const problem of new Set(keys)) {
    const list = problems.get(problem) || [];

    list.push(node);
    problems.set(problem, list);
  }
};

export function groupNodesByUser(
  nodes: LandingNode[],
  currentUserId: number,
  problemsOf: (node: LandingNode) => NodeProblemKey[],
): NodeUserGroup[] {
  const groups = new Map<string, NodeUserGroup>();

  for (const node of nodes) {
    const key = nodeUserKey(node);
    let group = groups.get(key);

    if (!group) {
      group = {
        key,
        userId: node.userId,
        name: node.userName || `用户 ${node.userId}`,
        admin: Boolean(node.userAdmin) || node.userId === currentUserId,
        nodes: [],
        used: 0,
        problems: new Map(),
      };
      groups.set(key, group);
    }
    group.nodes.push(node);
    group.used += (node.billedIn || 0) + (node.billedOut || 0);
    addProblems(group.problems, node, problemsOf(node));
  }
  // The signed-in admin first, then other admin accounts, then users by name.
  const rank = (group: NodeUserGroup) =>
    group.userId === currentUserId ? 0 : group.admin ? 1 : 2;

  return [...groups.values()].sort(
    (a, b) =>
      rank(a) - rank(b) ||
      a.name.localeCompare(b.name, "zh-CN") ||
      a.userId - b.userId,
  );
}

export function NodeProblemPills({
  group,
}: {
  group: { problems: Map<NodeProblemKey, LandingNode[]> };
}) {
  return (
    <>
      {NODE_PROBLEMS.map(({ key, tone, label }) => {
        const list = group.problems.get(key);

        if (!list?.length) return null;

        return (
          <Pill key={key} title={`${label}：${names(list)}`} tone={tone}>
            {label} {list.length}
          </Pill>
        );
      })}
    </>
  );
}

export function NodeUserHeaderContent({
  group,
  collapsed,
  used,
  compact,
}: {
  group: NodeUserGroup;
  collapsed: boolean;
  /** 已用 text, same unit formatting as the rows. */
  used: string;
  /** Phones / H5: meta + problem pills on a second line. */
  compact: boolean;
}) {
  const hasProblems = group.problems.size > 0;
  const meta = (
    <span className="whitespace-nowrap text-xs text-default-500">
      {group.nodes.length} 个节点 · 已用 {used}
    </span>
  );

  return (
    <>
      <div className="flex min-w-0 flex-1 items-center gap-2">
        <TreeGroupChevron collapsed={collapsed} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-1.5">
            <span
              className={`min-w-0 truncate text-sm ${collapsed ? "font-semibold" : "font-bold"} text-foreground`}
              title={group.name}
            >
              {group.name}
            </span>
            {group.admin && (
              <Pill title="管理员账号的节点" tone="cyan">
                管理员
              </Pill>
            )}
          </div>
          {compact && (
            <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-1">
              {meta}
              {hasProblems && <NodeProblemPills group={group} />}
            </div>
          )}
        </div>
      </div>
      {!compact && (
        <div
          className="flex shrink-0 flex-wrap items-center justify-end gap-1.5"
          data-user-summary=""
        >
          {hasProblems && <NodeProblemPills group={group} />}
          <span className="ml-1">{meta}</span>
        </div>
      )}
    </>
  );
}

// ------------------------------------------------- user 我的节点 (round 2)

/** A user's own nodes on one 落地服务器 (all nodes are the user's). */
export interface NodeServerGroup {
  key: string;
  serverId: number;
  name: string;
  region: string;
  nodes: LandingNode[];
  used: number;
  problems: Map<NodeProblemKey, LandingNode[]>;
}

export const nodeServerKey = (node: LandingNode) =>
  `server:${node.landingServerId}`;

export function groupNodesByServer(
  nodes: LandingNode[],
  servers: LandingServer[],
  problemsOf: (node: LandingNode) => NodeProblemKey[],
): NodeServerGroup[] {
  const byId = new Map(servers.map((server) => [server.id, server]));
  const groups = new Map<string, NodeServerGroup>();

  for (const node of nodes) {
    const key = nodeServerKey(node);
    let group = groups.get(key);

    if (!group) {
      const server = byId.get(node.landingServerId);

      group = {
        key,
        serverId: node.landingServerId,
        name:
          server?.name || node.serverName || `服务器 ${node.landingServerId}`,
        region: server ? serverRegionCode(server) : "",
        nodes: [],
        used: 0,
        problems: new Map(),
      };
      groups.set(key, group);
    }
    group.nodes.push(node);
    group.used += (node.billedIn || 0) + (node.billedOut || 0);
    addProblems(group.problems, node, problemsOf(node));
  }

  // Same order as the 可用落地 region groups, then by server name.
  return [...groups.values()].sort(
    (a, b) =>
      compareRegions(a.region, b.region) ||
      a.name.localeCompare(b.name, "zh-CN") ||
      a.serverId - b.serverId,
  );
}

export function NodeServerHeaderContent({
  group,
  collapsed,
  used,
  compact,
}: {
  group: NodeServerGroup;
  collapsed: boolean;
  used: string;
  /** Phones / H5: meta + problem pills on a second line. */
  compact: boolean;
}) {
  const flag = regionFlag(group.region);
  const hasProblems = group.problems.size > 0;
  const meta = (
    <span className="whitespace-nowrap text-xs text-default-500">
      {group.nodes.length} 个节点 · 已用 {used}
    </span>
  );

  return (
    <>
      <div className="flex min-w-0 flex-1 items-center gap-2">
        <TreeGroupChevron collapsed={collapsed} />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-1.5">
            {flag && (
              <span
                aria-hidden
                className="shrink-0 text-base leading-none"
                title={regionName(group.region)}
              >
                {flag}
              </span>
            )}
            <span
              className={`min-w-0 truncate text-sm ${collapsed ? "font-semibold" : "font-bold"} text-foreground`}
              title={group.name}
            >
              {group.name}
            </span>
          </div>
          {compact && (
            <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-1">
              {meta}
              {hasProblems && <NodeProblemPills group={group} />}
            </div>
          )}
        </div>
      </div>
      {!compact && (
        <div
          className="flex shrink-0 flex-wrap items-center justify-end gap-1.5"
          data-server-summary=""
        >
          {hasProblems && <NodeProblemPills group={group} />}
          <span className="ml-1">{meta}</span>
        </div>
      )}
    </>
  );
}
