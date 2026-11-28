import type { NodePageGroups, NodePageGroupsSave } from "@/api/node-groups";
import type { GroupAttention } from "./node-group-attention";

import { useEffect, useRef } from "react";
import { Lock, TriangleAlert } from "lucide-react";

import {
  Dropdown,
  DropdownItem,
  DropdownMenu,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";
import { groupTone } from "@/pages/probe-groups-editor";

// Plan 324: the 节点 page group tabs. One group set with the 探针 (probe_groups)
// + the 利群入口 node group at its stored position; 「综合」 = every machine
// (default on every visit, never remembered), 「其他」 = machines in no group
// (last, hidden when empty).

/** Id of the 利群入口 group inside the unified list (probe ids are > 0, new ones ≤ 0 near 0). */
export const LEIKWAN_GROUP_ID = -900000;

export const TAB_ALL = "all";
export const TAB_OTHER = "other";

export interface UnifiedGroup {
  id: number;
  name: string;
  nodes: number[];
}

export const LEIKWAN_TONE =
  "bg-cyan-500/15 text-cyan-800 dark:bg-cyan-400/20 dark:text-cyan-200";
const ALL_TONE =
  "bg-indigo-500/15 text-indigo-700 dark:bg-indigo-400/20 dark:text-indigo-200";
const OTHER_TONE =
  "bg-slate-500/15 text-slate-700 dark:bg-slate-400/20 dark:text-slate-200";

export const unifiedTone = (id: number) =>
  id === LEIKWAN_GROUP_ID ? LEIKWAN_TONE : groupTone(id);

/** The probe groups with the 利群入口 group inserted at its position. */
export function buildUnifiedGroups(
  data: NodePageGroups | null,
): UnifiedGroup[] {
  if (!data) return [];
  const list: UnifiedGroup[] = data.groups.map((g) => ({
    id: g.id,
    name: g.name,
    nodes: [...g.nodes],
  }));

  if (data.leikwan) {
    const pos = Math.max(0, Math.min(data.leikwan.pos, list.length));

    list.splice(pos, 0, {
      id: LEIKWAN_GROUP_ID,
      name: data.leikwan.name,
      nodes: [...data.leikwan.nodes],
    });
  }

  return list;
}

export function unifiedToSave(
  list: UnifiedGroup[],
  hasLeiKwan: boolean,
): NodePageGroupsSave {
  const groups = list.filter((g) => g.id !== LEIKWAN_GROUP_ID);
  const idx = list.findIndex((g) => g.id === LEIKWAN_GROUP_ID);
  const out: NodePageGroupsSave = {
    groups: groups.map((g) => ({ id: g.id, name: g.name, nodes: g.nodes })),
  };

  if (hasLeiKwan && idx >= 0) {
    const lk = list[idx];

    out.leikwan = { name: lk.name, nodes: lk.nodes, pos: idx };
  }

  return out;
}

/** The small 「利群 🔒」 mark of the 利群入口 group. */
export function LeiKwanGroupMark({ className = "" }: { className?: string }) {
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-0.5 rounded-full bg-cyan-600/15 px-1.5 py-px text-[10px] font-semibold text-cyan-800 dark:bg-cyan-300/15 dark:text-cyan-200 ${className}`}
      title="利群汇聚的入口机分组（只有管理员看得到，不会出现在探针页）"
    >
      <Lock aria-hidden="true" className="h-2.5 w-2.5" />
      利群
    </span>
  );
}

export interface NodeGroupTab {
  key: string;
  name: string;
  count: number;
  tone: string;
  leikwan?: boolean;
  attention: GroupAttention;
}

export function buildTabs(
  groups: UnifiedGroup[],
  visibleIds: number[],
  attentionOf: (ids: number[]) => GroupAttention,
): NodeGroupTab[] {
  const grouped = new Set<number>();
  const tabs: NodeGroupTab[] = [
    {
      key: TAB_ALL,
      name: "综合",
      count: visibleIds.length,
      tone: ALL_TONE,
      attention: attentionOf(visibleIds),
    },
  ];

  for (const g of groups) {
    const ids = visibleIds.filter((id) => g.nodes.includes(id));

    g.nodes.forEach((id) => grouped.add(id));
    tabs.push({
      key: String(g.id),
      name: g.name,
      count: ids.length,
      tone: unifiedTone(g.id),
      leikwan: g.id === LEIKWAN_GROUP_ID,
      attention: attentionOf(ids),
    });
  }
  const other = visibleIds.filter((id) => !grouped.has(id));

  if (other.length > 0) {
    tabs.push({
      key: TAB_OTHER,
      name: "其他",
      count: other.length,
      tone: OTHER_TONE,
      attention: attentionOf(other),
    });
  }

  return tabs;
}

/** Ids of the machines in a tab (null = every machine). */
export function tabMembers(
  key: string,
  groups: UnifiedGroup[],
): { ids: Set<number> | null; other: boolean } {
  if (key === TAB_ALL) return { ids: null, other: false };
  if (key === TAB_OTHER) {
    const grouped = new Set<number>();

    groups.forEach((g) => g.nodes.forEach((id) => grouped.add(id)));

    return { ids: grouped, other: true };
  }
  const g = groups.find((x) => String(x.id) === key);

  return { ids: new Set(g?.nodes || []), other: false };
}

function AttentionMarker({
  tab,
  onJump,
}: {
  tab: NodeGroupTab;
  onJump: (nodeId: number) => void;
}) {
  const a = tab.attention;

  if (!a.level) return null;
  const red = a.level === "danger";

  return (
    <Dropdown placement="bottom-start">
      <DropdownTrigger>
        <button
          aria-label={`${tab.name}：${a.count} 台机器有问题`}
          className={`-ml-1 inline-flex h-7 shrink-0 items-center gap-0.5 rounded-r-full py-0 pl-1.5 pr-2 text-[11px] font-bold tabular-nums transition-colors ${tab.tone} ${red ? "text-red-700 dark:text-red-300" : "text-amber-700 dark:text-amber-300"} hover:brightness-95`}
          data-group-marker={a.level}
          type="button"
        >
          <TriangleAlert
            aria-hidden="true"
            className={`h-3.5 w-3.5 ${red ? "text-red-600 dark:text-red-400" : "text-amber-600 dark:text-amber-400"}`}
            strokeWidth={red ? 2.6 : 2.2}
          />
          {a.count}
        </button>
      </DropdownTrigger>
      <DropdownMenu aria-label={`${tab.name} 需要注意的机器`}>
        <DropdownMenuLabel className="max-w-[min(22rem,calc(100vw-2rem))] whitespace-normal text-xs font-normal">
          <p className="font-medium text-foreground">
            「{tab.name}」里 {a.count} 台需要注意
          </p>
          <p className="mt-0.5 text-default-500">
            点名字跳到那台机器（仅管理员可见）
          </p>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {a.entries.map((e) => {
          const off = e.items.some((i) => i.level === "danger");

          return (
            <DropdownItem
              key={e.id}
              className="items-start whitespace-normal text-xs"
              onPress={() => onJump(e.id)}
            >
              <TriangleAlert
                aria-hidden="true"
                className={`mt-0.5 h-3.5 w-3.5 shrink-0 ${off ? "text-red-600 dark:text-red-400" : "text-amber-600 dark:text-amber-400"}`}
              />
              <span className="min-w-0 break-words">
                <span className="font-medium text-foreground">{e.name}</span>
                <span className="text-default-500"> — </span>
                {e.items.map((i, k) => (
                  <span
                    key={i.text}
                    className={
                      i.level === "danger"
                        ? "font-medium text-red-600 dark:text-red-400"
                        : "text-amber-700 dark:text-amber-300"
                    }
                  >
                    {k > 0 ? "、" : ""}
                    {i.text}
                  </span>
                ))}
              </span>
            </DropdownItem>
          );
        })}
      </DropdownMenu>
    </Dropdown>
  );
}

export function NodeGroupTabs({
  tabs,
  selected,
  onSelect,
  onJump,
  trailing,
}: {
  tabs: NodeGroupTab[];
  selected: string;
  onSelect: (key: string) => void;
  /** Marker popover: go to that machine inside this tab. */
  onJump: (tabKey: string, nodeId: number) => void;
  trailing?: React.ReactNode;
}) {
  const stripRef = useRef<HTMLDivElement>(null);

  // Phone: keep the selected tab in view of the horizontal strip.
  useEffect(() => {
    const strip = stripRef.current;
    const el = strip?.querySelector<HTMLElement>(
      `[data-group-tab="${CSS.escape(selected)}"]`,
    );

    if (!strip || !el) return;
    const left = el.offsetLeft - strip.offsetLeft;
    const right = left + el.offsetWidth;

    if (
      left < strip.scrollLeft ||
      right > strip.scrollLeft + strip.clientWidth
    ) {
      strip.scrollTo({
        left: Math.max(0, left - strip.clientWidth / 2 + el.offsetWidth / 2),
        behavior: "smooth",
      });
    }
  }, [selected]);

  return (
    <div
      className="flex min-w-0 items-center gap-2"
      data-node-group-tabs="true"
    >
      <div
        ref={stripRef}
        aria-label="节点分组"
        className="flvx-hide-scrollbar flex min-w-0 flex-1 items-center gap-1.5 overflow-x-auto pb-1 sm:flex-wrap sm:overflow-visible sm:pb-0"
        role="tablist"
      >
        {tabs.map((t) => {
          const on = t.key === selected;

          return (
            <div
              key={t.key}
              className={`inline-flex shrink-0 items-center rounded-full ${on ? "ring-2 ring-current/50 ring-offset-1 ring-offset-background" : ""} ${t.tone}`}
              data-group-tab={t.key}
            >
              <button
                aria-selected={on}
                className={`inline-flex h-7 items-center gap-1 rounded-full px-2.5 text-xs transition-colors ${on ? "font-bold" : "font-semibold opacity-90 hover:opacity-100"}`}
                role="tab"
                type="button"
                onClick={() => onSelect(t.key)}
              >
                <span className="max-w-[9rem] truncate">{t.name}</span>
                {t.leikwan && <Lock aria-label="利群" className="h-3 w-3" />}
                <span
                  className={`rounded-full px-1.5 text-[10px] tabular-nums ${on ? "bg-background/70" : "bg-background/40"}`}
                >
                  {t.count}
                </span>
              </button>
              <AttentionMarker
                tab={t}
                onJump={(nodeId) => onJump(t.key, nodeId)}
              />
            </div>
          );
        })}
      </div>
      {trailing}
    </div>
  );
}

/** Scroll to a machine's card / row and flash it (marker popover jump). */
export function flashNode(nodeId: number) {
  let tries = 0;
  const go = () => {
    const el = document.querySelector<HTMLElement>(
      `[data-node-anchor="${nodeId}"]`,
    );

    if (!el) {
      if (tries++ < 20) window.setTimeout(go, 50);

      return;
    }
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    el.animate?.(
      [
        { boxShadow: "0 0 0 0 rgba(245, 158, 11, 0)" },
        { boxShadow: "0 0 0 4px rgba(245, 158, 11, 0.85)" },
        { boxShadow: "0 0 0 0 rgba(245, 158, 11, 0)" },
      ],
      { duration: 900, iterations: 3 },
    );
  };

  window.setTimeout(go, 30);
}

/** Card / list 「分组」 chip: the node's first group (+N), or 「未分组」. */
export function NodeGroupChips({ groups }: { groups: UnifiedGroup[] }) {
  const base =
    "flex-shrink-0 inline-flex items-center justify-center gap-0.5 px-2 py-0.5 rounded text-xs font-medium";

  if (groups.length === 0) {
    return (
      <div className={`${base} bg-default-500/10 text-default-500`}>未分组</div>
    );
  }
  const [first, ...rest] = groups;

  return (
    <div
      className={`${base} max-w-[9.5rem] ${unifiedTone(first.id)}`}
      data-node-group-chip="true"
      title={groups.map((g) => g.name).join("、")}
    >
      {first.id === LEIKWAN_GROUP_ID && (
        <Lock aria-hidden="true" className="h-3 w-3 shrink-0" />
      )}
      <span className="truncate">{first.name}</span>
      {rest.length > 0 && (
        <span className="shrink-0 opacity-75">+{rest.length}</span>
      )}
    </div>
  );
}
