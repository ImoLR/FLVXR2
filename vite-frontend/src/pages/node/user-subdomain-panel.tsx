import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowRight, ChevronDown, ChevronRight, RefreshCw } from "lucide-react";
import toast from "react-hot-toast";

import {
  ExpandActions,
  ExpandChip,
  ExpandField,
  ExpandRow,
  useExpandOne,
} from "@/components/expand-row";
import {
  TreeGroupFooter,
  TreeGroupHeader,
  treeGroupHeaderClass,
  treeGroupWrapperClass,
} from "@/components/tree-group";
import {
  ENTRY_TAG_TEXT,
  PAUSE_REASON,
  RETIRE_REASON,
  UserSubItem,
  UserSubList,
  allowUserSubdomain,
  deleteUserSubdomain,
  listUserSubdomains,
  renameUserSubdomain,
  repointUserSubdomain,
  syncUserSubdomains,
} from "@/api/user-subdomain";
import { AttentionIcon } from "@/pages/landing/admin-attention";
import { landingToneClass } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";

// Plan 303 「专属域名」: the admin list of per-user names. mode "node" (the
// node drawer): users as an accordion with the rules-page tree line, every
// name a line (moved-in names marked 「来自 …」, moved-out ones greyed
// 「已改指 → …」). mode "user" (用户 edit dialog): that one user's names on
// every node. H5: one expand row per name.

const fmtTime = (ms: number) =>
  ms
    ? new Date(ms).toLocaleString("zh-CN", {
        hour12: false,
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
      })
    : "—";

type Tone = keyof typeof landingToneClass;

export function stateChip(item: UserSubItem): {
  text: string;
  tone: Tone;
  warn?: boolean;
} {
  if (item.moved === "out" && item.state !== "retired") {
    return { text: "已改指", tone: "slate" };
  }
  if (item.state === "retired") {
    return {
      text: `已作废 · ${RETIRE_REASON[item.reason] || item.reason}`,
      tone: "slate",
    };
  }
  if (item.status === "failed")
    return { text: "同步失败", tone: "red", warn: true };
  if (item.state === "paused") {
    // Round 2: 到期 / 停用 keep the name (still in his links), record deleted.
    if (item.reason === "expired" || item.reason === "disabled") {
      return { text: ENTRY_TAG_TEXT[item.reason], tone: "amber" };
    }
    if (item.reason === "unused") return { text: "同步中", tone: "cyan" };

    return {
      text: `暂停 · ${PAUSE_REASON[item.reason] || item.reason}`,
      tone: "amber",
    };
  }
  if (item.status === "pending") return { text: "同步中", tone: "cyan" };

  return { text: "生效", tone: "green" };
}

function StateChip({ item }: { item: UserSubItem }) {
  const c = stateChip(item);

  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 py-0.5 text-[11px] font-semibold leading-4 ${landingToneClass[c.tone]}`}
      data-user-sub-state={
        item.state + (item.status === "failed" ? "-failed" : "")
      }
      title={item.lastError || undefined}
    >
      {c.warn ? <AttentionIcon className="h-3.5 w-3.5" level="error" /> : null}
      {c.text}
    </span>
  );
}

/** 指向 column: this node, 「来自 X」 (moved in) or 「已改指 → Y」 (moved out). */
function PointsTo({
  item,
  mode,
}: {
  item: UserSubItem;
  mode: "node" | "user";
}) {
  if (mode === "user") {
    return (
      <span className="min-w-0 truncate">
        {item.nodeName || `#${item.nodeId}`}
        {item.originNodeId !== item.nodeId ? (
          <span className="text-default-400">
            （来自 {item.originNodeName}）
          </span>
        ) : null}
      </span>
    );
  }
  if (item.moved === "out") {
    return (
      <span className="inline-flex min-w-0 items-center gap-1 text-default-500">
        已改指 <ArrowRight className="shrink-0" size={12} />
        <span className="truncate">{item.nodeName}</span>
      </span>
    );
  }
  if (item.moved === "in") {
    return (
      <span
        className={`inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-[11px] font-semibold ${landingToneClass.violet}`}
      >
        来自 {item.originNodeName}
      </span>
    );
  }

  return <span className="text-default-500">本节点</span>;
}

function UsesButton({
  item,
  open,
  onToggle,
}: {
  item: UserSubItem;
  open: boolean;
  onToggle: () => void;
}) {
  const n = item.forwards.length;
  const text =
    n + item.landings === 0
      ? "暂无转发"
      : [n ? `${n} 条转发` : "", item.landings ? `${item.landings} 个落地` : ""]
          .filter(Boolean)
          .join(" · ");

  if (n === 0) {
    return <span className="pl-5 text-xs text-default-500">{text}</span>;
  }

  return (
    <button
      className="inline-flex items-center gap-0.5 rounded-full px-1.5 py-0.5 text-xs text-default-600 hover:bg-default-100"
      data-user-sub-uses={item.id}
      type="button"
      onClick={onToggle}
    >
      {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
      {text}
    </button>
  );
}

function ForwardList({ item }: { item: UserSubItem }) {
  return (
    <div className="flex flex-wrap gap-1.5" data-user-sub-forwards={item.id}>
      {item.forwards.map((f) => (
        <span
          key={f.id}
          className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] ${landingToneClass.slate}`}
        >
          <span className="max-w-[12rem] truncate">{f.name}</span>
          <span className="font-mono">:{f.port}</span>
        </span>
      ))}
    </div>
  );
}

interface Actions {
  repoint: (item: UserSubItem) => void;
  rename: (item: UserSubItem) => void;
  remove: (item: UserSubItem) => void;
}

function ActionPills({
  item,
  actions,
}: {
  item: UserSubItem;
  actions: Actions;
}) {
  if (item.state === "retired" || item.moved === "out") return null;

  return (
    <div className="flex shrink-0 items-center gap-1.5">
      <Button
        className="h-7 min-w-0 px-2.5"
        color="primary"
        size="sm"
        variant="flat"
        onPress={() => actions.repoint(item)}
      >
        改指
      </Button>
      <Button
        className="h-7 min-w-0 px-2.5"
        color="warning"
        size="sm"
        variant="flat"
        onPress={() => actions.rename(item)}
      >
        换新名
      </Button>
      <Button
        className="h-7 min-w-0 px-2.5"
        color="danger"
        size="sm"
        variant="flat"
        onPress={() => actions.remove(item)}
      >
        删除
      </Button>
    </div>
  );
}

/** Desktop: one name = one tree line. */
function NameLine({
  item,
  mode,
  actions,
}: {
  item: UserSubItem;
  mode: "node" | "user";
  actions: Actions;
}) {
  const [open, setOpen] = useState(false);
  const dim = item.moved === "out" || item.state === "retired";

  return (
    <div
      className={`flvx-rule-tree-node rounded-lg py-1.5 pl-2.5 pr-1 hover:bg-default-100/60${dim ? " opacity-60" : ""}`}
      data-user-sub-row={item.id}
    >
      <div className="flex min-h-9 items-center gap-3">
        <span
          className="w-48 shrink-0 truncate font-mono text-[13px] font-medium text-foreground"
          title={item.domain}
        >
          {item.domain}
        </span>
        <StateChip item={item} />
        <span className="flex w-36 min-w-0 shrink-0 items-center text-xs">
          <PointsTo item={item} mode={mode} />
        </span>
        <span className="w-28 shrink-0">
          <UsesButton item={item} open={open} onToggle={() => setOpen(!open)} />
        </span>
        <span
          className="min-w-0 flex-1 truncate text-xs text-default-500"
          title={item.lastError || undefined}
        >
          {item.lastError ? (
            <span className="text-danger">{item.lastError}</span>
          ) : (
            `同步 ${fmtTime(item.lastSyncAt)}`
          )}
        </span>
        <ActionPills actions={actions} item={item} />
      </div>
      {open ? (
        <div className="pb-1 pl-1 pt-1.5">
          <ForwardList item={item} />
        </div>
      ) : null}
    </div>
  );
}

/** H5: name row = user (or node) + short label + state chip; the rest opens. */
function NameRowH5({
  item,
  mode,
  open,
  onToggle,
  actions,
}: {
  item: UserSubItem;
  mode: "node" | "user";
  open: boolean;
  onToggle: () => void;
  actions: Actions;
}) {
  const c = stateChip(item);
  const lead = mode === "node" ? item.userName : item.nodeName;

  return (
    <ExpandRow
      chip={
        <ExpandChip className={landingToneClass[c.tone]} title={c.text}>
          {c.warn ? "⚠ " : ""}
          {c.text.split(" · ")[0]}
        </ExpandChip>
      }
      className={`rounded-xl border border-divider bg-content1${item.moved === "out" ? " opacity-60" : ""}`}
      name={
        <span className="flex min-w-0 items-baseline gap-1.5">
          <span className="min-w-0 truncate">{lead}</span>
          <span className="shrink-0 font-mono text-xs font-normal text-default-500">
            {item.label}
          </span>
        </span>
      }
      nameTitle={`${lead} · ${item.domain}`}
      open={open}
      rowProps={{ "data-user-sub-row": item.id }}
      onToggle={onToggle}
    >
      <ExpandField label="名字">
        <span className="font-mono">{item.domain}</span>
      </ExpandField>
      <ExpandField label="状态">
        <StateChip item={item} />
      </ExpandField>
      <ExpandField label="指向">
        <PointsTo item={item} mode={mode} />
      </ExpandField>
      <ExpandField label="转发">
        {item.forwards.length ? <ForwardList item={item} /> : "暂无转发"}
      </ExpandField>
      <ExpandField label="同步">
        {item.lastError ? (
          <span className="text-danger">{item.lastError}</span>
        ) : (
          fmtTime(item.lastSyncAt)
        )}
      </ExpandField>
      <ExpandActions>
        <ActionPills actions={actions} item={item} />
      </ExpandActions>
    </ExpandRow>
  );
}

function groupByUser(items: UserSubItem[]) {
  const map = new Map<
    number,
    { id: number; name: string; items: UserSubItem[] }
  >();

  for (const it of items) {
    const g = map.get(it.userId) || {
      id: it.userId,
      name: it.userName,
      items: [],
    };

    g.items.push(it);
    map.set(it.userId, g);
  }

  return Array.from(map.values()).sort((a, b) =>
    a.name.localeCompare(b.name, "zh"),
  );
}

function Summary({
  data,
  onSync,
  syncing,
}: {
  data: UserSubList;
  onSync: () => void;
  syncing: boolean;
}) {
  const node = data.node;
  const zone = data.zone;
  const c = data.counts;
  const near = zone && zone.quota > 0 && zone.used / zone.quota > 0.8;

  return (
    <div
      data-user-sub-summary
      className="space-y-2 rounded-xl border border-divider bg-default-50 p-3 dark:bg-default-100/40"
    >
      {node ? (
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
          <span className="text-default-500">主域名</span>
          <span className="font-mono font-medium">
            {node.mainDomain || "—"}
          </span>
          {node.mainIps?.length ? (
            <>
              <ArrowRight className="text-default-400" size={13} />
              <span className="break-all font-mono text-xs text-default-600">
                {node.mainIps.join("、")}
              </span>
            </>
          ) : null}
          {node.v6Only ? (
            <span
              className={`rounded-full px-2 py-0.5 text-[11px] font-semibold ${landingToneClass.cyan}`}
            >
              仅 IPv6
            </span>
          ) : null}
        </div>
      ) : null}
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <span
          className={`rounded-full px-2 py-0.5 font-semibold ${landingToneClass.green}`}
        >
          生效 {c.active || 0}
        </span>
        {c.paused_expired ? (
          <span
            className={`rounded-full px-2 py-0.5 font-semibold ${landingToneClass.amber}`}
          >
            已过期 {c.paused_expired}
          </span>
        ) : null}
        {c.paused_disabled ? (
          <span
            className={`rounded-full px-2 py-0.5 font-semibold ${landingToneClass.amber}`}
          >
            已停用 {c.paused_disabled}
          </span>
        ) : null}
        {(c.paused || 0) - (c.paused_expired || 0) - (c.paused_disabled || 0) >
        0 ? (
          <span
            className={`rounded-full px-2 py-0.5 font-semibold ${landingToneClass.amber}`}
          >
            暂停{" "}
            {(c.paused || 0) -
              (c.paused_expired || 0) -
              (c.paused_disabled || 0)}
          </span>
        ) : null}
        {c.failed ? (
          <span
            className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-semibold ${landingToneClass.red}`}
          >
            <AttentionIcon className="h-3.5 w-3.5" level="error" />
            同步失败 {c.failed}
          </span>
        ) : null}
        {zone ? (
          <span
            data-user-sub-quota
            className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-semibold ${near ? landingToneClass.red : landingToneClass.slate}`}
            title={
              zone.quotaGuess
                ? "Cloudflare 没有返回上限，按免费版 200 条估计"
                : undefined
            }
          >
            {near ? (
              <AttentionIcon className="h-3.5 w-3.5" level="error" />
            ) : null}
            Cloudflare 记录 {zone.used} / {zone.quota}
            {zone.quotaGuess ? "（估）" : ""}
          </span>
        ) : null}
        <Button
          className="ml-auto h-7 min-w-0 px-2.5"
          color="success"
          isLoading={syncing}
          size="sm"
          startContent={syncing ? null : <RefreshCw size={13} />}
          variant="flat"
          onPress={onSync}
        >
          立即同步
        </Button>
      </div>
      {data.capped ? (
        <p className="flex items-center gap-1 text-xs text-danger">
          <AttentionIcon level="error" />
          Cloudflare 记录数快满了：{data.capped}{" "}
          个用户还没有专属域名，暂时用主域名（不会给
          IP）。删掉不用的记录或升级套餐后自动补上。
        </p>
      ) : null}
      {zone?.wildcard ? (
        <p className="flex items-center gap-1 text-xs text-danger">
          <AttentionIcon level="error" />
          区域里出现了通配记录 *.{zone.name}
          ：暂停或作废的名字仍会解析到它，请删除这条通配记录。
        </p>
      ) : null}
      {zone?.error ? (
        <p className="flex items-center gap-1 text-xs text-danger">
          <AttentionIcon level="error" />
          {zone.error}
        </p>
      ) : null}
      {node && !node.effective ? (
        <p className="text-xs text-warning">
          {!node.toggle
            ? "这台节点没有开启「用户专属域名」（在节点编辑 → DDNS 里开启）。"
            : "等待主地址记录同步，之后开始分配。"}
        </p>
      ) : null}
    </div>
  );
}

export function UserSubdomainPanel({
  nodeId,
  userId,
  isH5,
  onChanged,
  hideWhenEmpty = false,
  title,
}: {
  nodeId?: number;
  userId?: number;
  isH5: boolean;
  onChanged?: () => void;
  /** user dialog: render nothing for a user without names */
  hideWhenEmpty?: boolean;
  /** user dialog: section heading shown above the list */
  title?: string;
}) {
  const mode: "node" | "user" = nodeId ? "node" : "user";
  const [data, setData] = useState<UserSubList | null>(null);
  const [history, setHistory] = useState<UserSubItem[] | null>(null);
  const [showHistory, setShowHistory] = useState(false);
  const [query, setQuery] = useState("");
  const [openUser, setOpenUser] = useState<number | null>(null);
  const [syncing, setSyncing] = useState(false);
  const [repointing, setRepointing] = useState<UserSubItem | null>(null);
  const [repointTo, setRepointTo] = useState<number>(0);
  const [confirm, setConfirm] = useState<{
    kind: "rename" | "remove";
    item: UserSubItem;
  } | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const res = await listUserSubdomains({ nodeId, userId });

    if (res.code === 0 && res.data) setData(res.data);
  }, [nodeId, userId]);

  useEffect(() => {
    load();
  }, [load]);

  const loadHistory = async () => {
    const res = await listUserSubdomains({ nodeId, userId, retired: true });

    if (res.code === 0 && res.data) setHistory(res.data.history || []);
  };

  const after = async () => {
    await load();
    if (showHistory) await loadHistory();
    onChanged?.();
  };

  const run = async (p: Promise<{ code: number; msg: string }>, ok: string) => {
    setBusy(true);
    try {
      const res = await p;

      if (res.code === 0) {
        toast.success(ok);
        setRepointing(null);
        setConfirm(null);
        await after();
      } else toast.error(res.msg || "操作失败");
    } catch {
      toast.error("网络错误，请重试");
    } finally {
      setBusy(false);
    }
  };

  const actions: Actions = {
    repoint: (item) => {
      setRepointTo(0);
      setRepointing(item);
    },
    rename: (item) => setConfirm({ kind: "rename", item }),
    remove: (item) => setConfirm({ kind: "remove", item }),
  };

  const items = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = data?.items || [];

    return q
      ? list.filter(
          (it) =>
            it.userName.toLowerCase().includes(q) || it.domain.includes(q),
        )
      : list;
  }, [data, query]);
  const groups = useMemo(() => groupByUser(items), [items]);
  const rowKeys = useMemo(() => items.map((it) => it.id), [items]);
  const h5 = useExpandOne(rowKeys);
  const retired = data?.counts.retired || 0;

  if (!data) {
    return hideWhenEmpty ? null : (
      <p className="text-sm text-default-500">加载中…</p>
    );
  }
  if (hideWhenEmpty && data.items.length === 0 && retired === 0) return null;

  const sync = async () => {
    setSyncing(true);
    try {
      const res = await syncUserSubdomains({ nodeId, userId });

      if (res.code === 0 && res.data) {
        setData(res.data);
        toast.success("已同步");
        onChanged?.();
      } else toast.error(res.msg || "同步失败");
    } catch {
      toast.error("网络错误，请重试");
    } finally {
      setSyncing(false);
    }
  };

  const list =
    items.length === 0 ? (
      <p className="py-3 text-center text-sm text-default-500">
        {query ? "没有匹配的用户" : "还没有专属域名"}
      </p>
    ) : isH5 || mode === "user" ? (
      <div className="space-y-2">
        {items.map((it) => (
          <NameRowH5
            key={it.id}
            actions={actions}
            item={it}
            mode={mode}
            open={h5.open === it.id}
            onToggle={() => h5.toggle(it.id)}
          />
        ))}
      </div>
    ) : (
      <div className="space-y-2">
        {groups.map((g) => {
          const open =
            openUser === g.id || (groups.length === 1 && openUser !== -g.id);
          const live = g.items.filter(
            (it) => it.state === "active" && it.moved !== "out",
          ).length;
          const warn = g.items.some((it) => it.status === "failed");

          return (
            <div
              key={g.id}
              className={treeGroupWrapperClass(!open)}
              data-group-key={`user-sub:${g.id}`}
              data-user-sub-user={g.id}
            >
              <TreeGroupHeader
                className={treeGroupHeaderClass(!open)}
                collapsed={!open}
                onToggle={() =>
                  setOpenUser(
                    open ? (groups.length === 1 ? -g.id : null) : g.id,
                  )
                }
              >
                <div className="flex min-w-0 flex-1 items-center gap-2.5">
                  <span className="flex w-7 shrink-0 justify-center">
                    <span
                      className={`h-2 w-2 rounded-full ${live ? "bg-green-500" : "bg-amber-500"}`}
                    />
                  </span>
                  <span className="truncate text-sm font-semibold">
                    {g.name}
                  </span>
                  <span className="shrink-0 text-xs text-default-500">
                    {g.items.length} 个名字
                  </span>
                  {warn ? <AttentionIcon level="error" /> : null}
                </div>
                <span className="shrink-0 pl-3 text-xs text-default-500">
                  {g.items.map((it) => it.label).join(" · ")}
                </span>
              </TreeGroupHeader>
              {open ? (
                <>
                  <div className="flvx-rule-tree-cards space-y-0.5 py-1.5 pr-2">
                    {g.items.map((it) => (
                      <NameLine
                        key={it.id}
                        actions={actions}
                        item={it}
                        mode={mode}
                      />
                    ))}
                  </div>
                  <TreeGroupFooter
                    name={g.name}
                    onToggle={() =>
                      setOpenUser(groups.length === 1 ? -g.id : null)
                    }
                  />
                </>
              ) : null}
            </div>
          );
        })}
      </div>
    );

  return (
    <div
      className={`space-y-3${title ? " rounded-xl border border-divider p-3" : ""}`}
      data-user-sub-panel={mode}
    >
      {title ? (
        <div className="flex items-end justify-between gap-3">
          <div>
            <h4 className="text-sm font-semibold">{title}</h4>
            <p className="text-xs text-default-500">
              这个用户在各入口节点的私人域名（CNAME →
              节点主域名），只有他自己的订阅、复制链接和转发地址里出现。过期或停用时记录删除、名字保留，续期或启用后用原名恢复。
            </p>
          </div>
          <Button
            className="h-7 min-w-0 shrink-0 px-2.5"
            color="success"
            isLoading={syncing}
            size="sm"
            startContent={syncing ? null : <RefreshCw size={13} />}
            variant="flat"
            onPress={sync}
          >
            立即同步
          </Button>
        </div>
      ) : null}
      {mode === "node" ? (
        <Summary data={data} syncing={syncing} onSync={sync} />
      ) : null}
      {mode === "node" && (data.items.length > 6 || query) ? (
        <div className={isH5 ? "w-full" : "w-64"}>
          <Input
            aria-label="搜索用户"
            placeholder="搜索用户或名字"
            size="sm"
            value={query}
            variant="bordered"
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
      ) : null}
      {list}
      {retired > 0 ? (
        <div data-user-sub-history className="space-y-2">
          <button
            className="inline-flex items-center gap-1 text-xs text-default-500 hover:text-foreground"
            type="button"
            onClick={async () => {
              const next = !showHistory;

              setShowHistory(next);
              if (next && history === null) await loadHistory();
            }}
          >
            {showHistory ? (
              <ChevronDown size={14} />
            ) : (
              <ChevronRight size={14} />
            )}
            已作废 {retired}（名字永不复用）
          </button>
          {showHistory && history ? (
            <div className="space-y-1 rounded-lg border border-divider p-2">
              {history.map((it) => (
                <div
                  key={it.id}
                  className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-default-500"
                  data-user-sub-retired={it.id}
                >
                  <span className="font-mono text-default-600 line-through decoration-default-300">
                    {it.domain}
                  </span>
                  <span>{mode === "node" ? it.userName : it.nodeName}</span>
                  <span>{RETIRE_REASON[it.reason] || it.reason}</span>
                  <span>{fmtTime(it.retiredAt)}</span>
                  {it.blocked ? (
                    <Button
                      className="h-6 min-w-0 px-2"
                      color="success"
                      isLoading={busy}
                      size="sm"
                      variant="flat"
                      onPress={() =>
                        run(allowUserSubdomain(it.id), "已重新分配")
                      }
                    >
                      重新分配
                    </Button>
                  ) : null}
                </div>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

      <Modal
        isOpen={!!repointing}
        size="md"
        onOpenChange={(o) => !o && setRepointing(null)}
      >
        <ModalContent>
          <ModalHeader>改指 · {repointing?.domain}</ModalHeader>
          <ModalBody className="space-y-3">
            <p className="text-sm text-default-600">
              把 <span className="font-medium">{repointing?.userName}</span>{" "}
              的这个名字改为指向另一台节点的主域名。他的客户端不用改，2
              分钟内连到新节点。
            </p>
            <Select
              label="指向节点"
              placeholder="选择节点"
              selectedKeys={repointTo ? [String(repointTo)] : []}
              variant="bordered"
              onSelectionChange={(keys) =>
                setRepointTo(Number(Array.from(keys)[0] || 0))
              }
            >
              {(data.targets || [])
                .filter((t) => t.id !== repointing?.nodeId)
                .map((t) => (
                  <SelectItem key={String(t.id)} textValue={t.name}>
                    {t.name}（{t.mainDomain}）
                  </SelectItem>
                ))}
            </Select>
            <p className="text-xs text-warning">
              目标节点上要先有他的同端口转发，否则面板会拒绝（客户端会连不上）。
            </p>
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => setRepointing(null)}
            >
              取消
            </Button>
            <Button
              color="primary"
              isDisabled={!repointTo}
              isLoading={busy}
              variant="flat"
              onPress={() =>
                repointing &&
                run(repointUserSubdomain(repointing.id, repointTo), "已改指")
              }
            >
              改指
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>

      <Modal
        isOpen={!!confirm}
        size="md"
        onOpenChange={(o) => !o && setConfirm(null)}
      >
        <ModalContent>
          <ModalHeader>
            {confirm?.kind === "rename" ? "换新名" : "删除专属域名"} ·{" "}
            {confirm?.item.domain}
          </ModalHeader>
          <ModalBody>
            {confirm?.kind === "rename" ? (
              <p className="text-sm text-default-600">
                旧名字立即删除并作废（永不复用），{confirm.item.userName}{" "}
                换一个新的随机名字。用订阅的更新订阅即可，手动填地址的要改成新名字。适合名字泄露时用。
              </p>
            ) : (
              <p className="text-sm text-default-600">
                名字立即删除并作废（永不复用）。{confirm?.item.userName}{" "}
                的订阅和转发地址里仍是这个名字（不会换成主域名），所以他在这台节点上连不上；不再自动分配新名字。需要时在「已作废」里点「重新分配」。
              </p>
            )}
          </ModalBody>
          <ModalFooter>
            <Button
              color="danger"
              variant="flat"
              onPress={() => setConfirm(null)}
            >
              取消
            </Button>
            <Button
              color={confirm?.kind === "rename" ? "warning" : "danger"}
              isLoading={busy}
              variant="flat"
              onPress={() =>
                confirm &&
                run(
                  confirm.kind === "rename"
                    ? renameUserSubdomain(confirm.item.id)
                    : deleteUserSubdomain(confirm.item.id),
                  confirm.kind === "rename" ? "已换新名" : "已删除",
                )
              }
            >
              {confirm?.kind === "rename" ? "换新名" : "删除"}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </div>
  );
}
