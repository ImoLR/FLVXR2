import { useCallback, useEffect, useState } from "react";
import { Download, Minus, Plus, RefreshCw } from "lucide-react";

import { SubAdminDetailDialog, SubAdminTarget } from "./admin-detail";

import {
  ExpandChip,
  ExpandField,
  ExpandRow,
  useExpandOne,
} from "@/components/expand-row";
import {
  TreeGroupHeader,
  treeGroupHeaderClass,
  treeGroupWrapperClass,
} from "@/components/tree-group";
import { useGroupScrollSettle } from "@/hooks/use-group-scroll-settle";
import { landingToneClass } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  SubAdminListData,
  SubAdminSource,
  SubAdminSub,
  SubAdminUser,
  SubFormat,
  subAdminList,
  subRelative,
} from "@/api/sub-link";
import { SubEntryBadge } from "@/pages/sub-link/entry-badge";

// Plan 260 「全部用户」: the admin's read-only overview of every user's
// subscriptions — summary + search, users as an accordion (latest pull
// first), one compact line per subscription / external source, 查看 opens
// the read-only detail. No edit, delete, reset or 停用 anywhere.

const DAY = 86_400_000;

const formatShort: Record<SubFormat, string> = {
  auto: "自动识别",
  clash: "Clash",
  singbox: "sing-box",
  base64: "节点链接",
};

function routingWord(s: SubAdminSub) {
  if (s.routing === "none") return "未设置";
  if (s.routing === "default") return `默认 ${s.routingCount} 组`;

  return `自定义 ${s.routingCount} 组`;
}

function subMeta(s: SubAdminSub) {
  return `${formatShort[s.format] || s.format} · ${s.nodes} 个节点 · 分流：${routingWord(s)}`;
}

function userCounts(u: SubAdminUser) {
  return `${u.subs.length} 条订阅${u.sources.length ? ` · 外部订阅源 ${u.sources.length}` : ""}`;
}

/** green = pulled ≤ 24 h; grey = 1–7 d; hollow grey = > 7 d or never. */
function PullDot({ at }: { at: number }) {
  const age = at ? Date.now() - at : Infinity;
  const [cls, title] =
    age <= DAY
      ? ["bg-green-500", "24 小时内有拉取"]
      : age <= 7 * DAY
        ? ["bg-slate-400 dark:bg-slate-500", "1–7 天内有拉取"]
        : [
            "border-[1.5px] border-slate-400 dark:border-slate-500",
            at ? "超过 7 天没有拉取" : "从未拉取",
          ];

  return (
    <span
      aria-label={title}
      className={`inline-block h-2 w-2 shrink-0 rounded-full ${cls}`}
      data-pull-dot={age <= DAY ? "green" : age <= 7 * DAY ? "grey" : "hollow"}
      role="img"
      title={title}
    />
  );
}

function SourceState({ s }: { s: SubAdminSource }) {
  const ok = !s.error;

  return (
    <span
      className={
        ok
          ? "text-green-700 dark:text-green-300"
          : "text-red-600 dark:text-red-300"
      }
    >
      {ok ? "OK" : "失败"}
    </span>
  );
}

function sourceTitle(s: SubAdminSource) {
  const when = s.lastFetchAt ? `最近更新：${subRelative(s.lastFetchAt)}` : "";

  return [when, s.error].filter(Boolean).join("\n") || undefined;
}

function PlusMinus({ open }: { open: boolean }) {
  return (
    <span
      aria-hidden="true"
      className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-slate-500/15 text-slate-600 dark:bg-slate-400/20 dark:text-slate-300"
    >
      {open ? <Minus size={16} /> : <Plus size={16} />}
    </span>
  );
}

const subUserGroupKey = (id: number) => `sub-user:${id}`;

/** Desktop: one user = one group of the rules-page accordion. */
function UserGroup({
  u,
  open,
  onToggle,
  onView,
}: {
  u: SubAdminUser;
  open: boolean;
  onToggle: () => void;
  onView: (s: SubAdminSub) => void;
}) {
  return (
    <div
      className={treeGroupWrapperClass(!open)}
      data-admin-user={u.id}
      data-group-key={subUserGroupKey(u.id)}
    >
      <TreeGroupHeader
        className={treeGroupHeaderClass(!open)}
        collapsed={!open}
        onToggle={onToggle}
      >
        <div className="flex min-w-0 flex-1 items-center gap-2.5">
          <span className="flex w-7 shrink-0 justify-center">
            <PullDot at={u.lastPullAt} />
          </span>
          <span className="truncate text-sm font-semibold" title={u.name}>
            {u.name}
          </span>
          <span className="shrink-0 text-xs text-default-500">
            {userCounts(u)}
          </span>
        </div>
        <div className="flex shrink-0 items-center gap-3 pl-3">
          <span className="text-xs text-default-500">
            {subRelative(u.lastPullAt)}
          </span>
          <PlusMinus open={open} />
        </div>
      </TreeGroupHeader>
      {open && (
        // The rules-page tree: the line drops from under the header dot and
        // elbows into every subscription / source line.
        <div data-admin-user-body className="flvx-rule-tree-cards py-1.5 pr-3">
          {u.subs.length === 0 && (
            <p className="flvx-rule-tree-node flex h-12 items-center pl-2.5 text-xs text-default-400">
              还没有订阅
            </p>
          )}
          {u.subs.map((s) => (
            <div
              key={s.id}
              className="flvx-rule-tree-node flex h-12 items-center gap-3 rounded-lg pl-2.5 pr-1 hover:bg-default-100/60"
              data-admin-sub={s.id}
            >
              <span
                className="flex w-60 shrink-0 items-center gap-1.5 text-sm font-medium"
                title={s.name}
              >
                <span className="min-w-0 truncate">{s.name}</span>
                <SubEntryBadge mode={s.entryMode} />
              </span>
              <span className="min-w-0 flex-1 truncate text-xs text-default-500">
                {subMeta(s)}
              </span>
              <span className="w-20 shrink-0 text-right text-xs text-default-500">
                {subRelative(s.lastPullAt)}
              </span>
              <Button
                className="min-h-7 px-3"
                color="primary"
                data-admin-view={s.id}
                size="sm"
                variant="flat"
                onPress={() => onView(s)}
              >
                查看 ›
              </Button>
            </div>
          ))}
          {u.sources.map((s) => (
            <div
              key={`src-${s.id}`}
              className="flvx-rule-tree-node flex h-12 items-center gap-2 pl-2.5 text-xs text-default-600"
              data-admin-source={s.id}
              title={sourceTitle(s)}
            >
              <Download className="shrink-0 text-default-400" size={13} />
              <span className="min-w-0 truncate">
                <span className="font-medium text-foreground">{s.name}</span>
                {` · ${s.nodes} 个节点 · 最近更新 `}
                <SourceState s={s} />
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** H5: name + 「N 条订阅」 chip + dot + ＋; the lines live in the expand area. */
function UserRowH5({
  u,
  open,
  onToggle,
  onView,
}: {
  u: SubAdminUser;
  open: boolean;
  onToggle: () => void;
  onView: (s: SubAdminSub) => void;
}) {
  return (
    <ExpandRow
      chip={
        <>
          <ExpandChip className={landingToneClass.purple}>
            {u.subs.length} 条订阅
          </ExpandChip>
          <span className="flex w-3 shrink-0 justify-center">
            <PullDot at={u.lastPullAt} />
          </span>
        </>
      }
      className="border-b border-divider last:border-b-0"
      name={u.name}
      nameTitle={u.name}
      open={open}
      rowProps={{ "data-admin-user": u.id }}
      onToggle={onToggle}
    >
      {u.name.length > 14 && <ExpandField label="用户名">{u.name}</ExpandField>}
      <ExpandField label="最近拉取">{subRelative(u.lastPullAt)}</ExpandField>
      {u.subs.map((s) => (
        <div
          key={s.id}
          className="rounded-lg bg-content1 px-2.5 py-2 shadow-sm"
          data-admin-sub={s.id}
        >
          <div className="flex items-start gap-2">
            <span className="min-w-0 flex-1 break-words text-sm font-medium">
              {s.name}
              {s.entryMode === "local" && (
                <SubEntryBadge className="ml-1.5 align-middle" mode="local" />
              )}
            </span>
            <Button
              className="min-h-7 shrink-0 px-3"
              color="primary"
              data-admin-view={s.id}
              size="sm"
              variant="flat"
              onPress={() => onView(s)}
            >
              查看 ›
            </Button>
          </div>
          <div className="mt-0.5 text-xs text-default-500">
            {subMeta(s)} · {subRelative(s.lastPullAt)}
          </div>
        </div>
      ))}
      {u.sources.map((s) => (
        <div
          key={`src-${s.id}`}
          className="text-xs text-default-600"
          data-admin-source={s.id}
        >
          <div className="flex items-start gap-1.5">
            <Download className="mt-0.5 shrink-0 text-default-400" size={13} />
            <span className="min-w-0 break-words">
              <span className="font-medium text-foreground">{s.name}</span>
              {` · ${s.nodes} 个节点 · 最近更新 `}
              <SourceState s={s} />
            </span>
          </div>
          {s.error && (
            <p className="mt-0.5 break-words pl-[1.2rem] text-red-600 dark:text-red-300">
              {s.error}
            </p>
          )}
        </div>
      ))}
    </ExpandRow>
  );
}

export default function SubAdminView({ isH5 }: { isH5: boolean }) {
  const [data, setData] = useState<SubAdminListData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [q, setQ] = useState("");
  const [openId, setOpenId] = useState<number | null>(null);
  const [viewing, setViewing] = useState<SubAdminTarget | null>(null);
  // Plan 287: the 转发 / 隧道 page position correction on a header tap — the
  // tapped header stays where it was while the open user above collapses,
  // then an opened one glides to the top (utils/group-accordion).
  const queueGroupScroll = useGroupScrollSettle();
  const toggleUser = (id: number) => {
    queueGroupScroll(subUserGroupKey(id), openId !== id);
    setOpenId((cur) => (cur === id ? null : id));
  };

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await subAdminList();

      if (res.code === 0) {
        setData(res.data);
        setError("");
      } else setError(res.msg || "加载失败");
    } catch {
      setError("加载失败");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const users = data?.users || [];
  const needle = q.trim().toLowerCase();
  const shown = needle
    ? users.filter((u) => u.name.toLowerCase().includes(needle))
    : users;
  const subTotal = users.reduce((n, u) => n + u.subs.length, 0);
  const now = Date.now();
  const recent = users.reduce(
    (n, u) =>
      n +
      u.subs.filter((s) => s.lastPullAt && now - s.lastPullAt <= DAY).length,
    0,
  );
  const h5Open = useExpandOne(shown.map((u) => u.id));
  const view = (u: SubAdminUser) => (s: SubAdminSub) =>
    setViewing({ id: s.id, user: u.name, name: s.name });

  const search = (
    <Input
      aria-label="搜索用户名"
      className={isH5 ? "w-full" : "w-56"}
      placeholder="搜索用户名"
      size="sm"
      value={q}
      onChange={(e) => setQ(e.target.value)}
    />
  );
  const refresh = (
    <Button
      isIconOnly
      aria-label="刷新"
      isLoading={loading}
      size="sm"
      variant="flat"
      onPress={load}
    >
      <RefreshCw size={16} />
    </Button>
  );

  return (
    <div data-sub-admin-view className="space-y-3">
      {data?.enabled === false ? (
        <div className="rounded-2xl border border-divider bg-content1 p-6 text-center text-sm text-default-500">
          订阅功能已被管理员关闭。
        </div>
      ) : (
        <>
          <div
            className={
              isH5 ? "space-y-2" : "flex items-center justify-between gap-3"
            }
          >
            <div className="flex items-center justify-between gap-2">
              <p
                data-admin-summary
                className="min-w-0 text-sm text-default-600"
              >
                {users.length} 位用户 · {subTotal} 条订阅 · 24 小时内有拉取{" "}
                {recent}
              </p>
              {isH5 && refresh}
            </div>
            {isH5 ? (
              search
            ) : (
              <div className="flex shrink-0 items-center gap-2">
                {search}
                {refresh}
              </div>
            )}
          </div>
          {error && (
            <p className="rounded-lg bg-danger-50 p-2 text-sm text-danger">
              {error}
            </p>
          )}
          {data && users.length === 0 && (
            <div className="rounded-2xl border border-divider bg-content1 p-6 text-center text-sm text-default-500">
              还没有用户创建订阅。
            </div>
          )}
          {data && users.length > 0 && shown.length === 0 && (
            <div className="rounded-2xl border border-divider bg-content1 p-6 text-center text-sm text-default-500">
              没有匹配「{q.trim()}」的用户
            </div>
          )}
          {isH5 ? (
            shown.length > 0 && (
              <div className="overflow-hidden rounded-2xl border border-divider bg-content1">
                {shown.map((u) => (
                  <UserRowH5
                    key={u.id}
                    open={h5Open.open === u.id}
                    u={u}
                    onToggle={() => h5Open.toggle(u.id)}
                    onView={view(u)}
                  />
                ))}
              </div>
            )
          ) : (
            <div className="space-y-2">
              {shown.map((u) => (
                <UserGroup
                  key={u.id}
                  open={openId === u.id}
                  u={u}
                  onToggle={() => toggleUser(u.id)}
                  onView={view(u)}
                />
              ))}
            </div>
          )}
        </>
      )}
      {viewing && (
        <SubAdminDetailDialog
          isH5={isH5}
          target={viewing}
          onClose={() => setViewing(null)}
        />
      )}
    </div>
  );
}
