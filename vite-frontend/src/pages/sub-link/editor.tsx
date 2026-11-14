import { useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";
import { Check, ChevronLeft, GripVertical, Plus, X } from "lucide-react";
import {
  DndContext,
  DragEndEvent,
  KeyboardSensor,
  MouseSensor,
  TouchSensor,
  closestCenter,
  useSensor,
  useSensors,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";

import {
  DEFAULT_LIMITS,
  RouteNodeOption,
  RoutingPanel,
  normalizeRouting,
} from "./routing";

import { AddExternalDialog } from "@/pages/landing/chain-proxy";
import { Pill } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import {
  SubFormat,
  SubItem,
  SubPool,
  SubPoolNode,
  SubPoolSource,
  SubRouteEntry,
  SubRuleMode,
  SubSelected,
  subCreate,
  subPool,
  subUpdate,
} from "@/api/sub-link";

// Plan 185 编辑订阅: left = everything the user can add (grouped by source),
// right = the ordered list that becomes the subscription. Phones get two
// tabs (可选 / 已选) with checkboxes and a sticky 取消 / 保存 bar.
// Plan 256: a 「分流」 tab (desktop 节点 | 分流, phones a third tab); the
// 国内直连 / 全部走代理 switch is its fixed 「国内网站」 row. The 外部节点
// group is always shown with 「＋ 添加」.

type Filter = "all" | "rule" | "landing" | "external" | `src:${number}`;

const FORMATS: [SubFormat, string][] = [
  ["auto", "自动识别"],
  ["clash", "Clash"],
  ["singbox", "sing-box"],
  ["base64", "通用"],
];

function Seg<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: [T, string][];
  onChange: (v: T) => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <span className="shrink-0 text-xs text-default-500">{label}</span>
      <div
        aria-label={label}
        className="inline-flex rounded-xl bg-slate-500/10 p-0.5 dark:bg-slate-400/15"
        role="radiogroup"
      >
        {options.map(([v, text]) => (
          <button
            key={v}
            aria-checked={value === v}
            className={`whitespace-nowrap rounded-lg px-2.5 py-1 text-[13px] ${
              value === v
                ? "bg-white font-semibold text-foreground shadow-sm dark:bg-slate-700"
                : "text-default-600"
            }`}
            role="radio"
            type="button"
            onClick={() => onChange(v)}
          >
            {text}
          </button>
        ))}
      </div>
    </div>
  );
}

const groupChip: Record<
  SubSelected["group"],
  { tone: "green" | "teal" | "purple" | "amber"; text: string }
> = {
  rule: { tone: "green", text: "规则" },
  landing: { tone: "teal", text: "落地" },
  external: { tone: "purple", text: "外部节点" },
  source: { tone: "amber", text: "" },
  source_all: { tone: "amber", text: "整组" },
};

function protocolText(n: { protocol?: string; chain?: boolean }) {
  return `${n.protocol || ""}${n.chain ? "（链式）" : ""}`;
}

function SelectedRow({
  item,
  onRemove,
}: {
  item: SubSelected;
  onRemove: () => void;
}) {
  const {
    setNodeRef,
    transform,
    transition,
    isDragging,
    attributes,
    listeners,
  } = useSortable({ id: item.key });
  const chip = groupChip[item.group];
  const all = item.group === "source_all";

  return (
    <div
      ref={setNodeRef}
      className={`flex items-center gap-2 border-b border-divider px-3 py-2 last:border-b-0 ${
        all ? "bg-amber-500/10 dark:bg-amber-400/10" : ""
      } ${item.missing ? "bg-red-500/10 dark:bg-red-400/10" : ""}`}
      data-sub-selected={item.key}
      style={{
        transform: CSS.Transform.toString(transform),
        transition,
        opacity: isDragging ? 0.6 : 1,
        position: isDragging ? "relative" : undefined,
        zIndex: isDragging ? 10 : undefined,
      }}
    >
      <button
        aria-label="拖动排序"
        className="shrink-0 cursor-grab touch-none text-default-400"
        type="button"
        {...attributes}
        {...listeners}
      >
        <GripVertical size={16} />
      </button>
      <div className="min-w-0 flex-1">
        <div
          className={`truncate text-sm ${item.missing ? "text-red-700 line-through dark:text-red-300" : ""}`}
        >
          {all
            ? `${item.sourceName} · 全部节点（自动跟随）`
            : item.name || "（已删除的节点）"}
        </div>
        <div
          className={`truncate text-xs ${item.missing ? "text-red-600 dark:text-red-300" : "text-default-500"}`}
        >
          {item.missing
            ? "已失效 · 保存时自动移除"
            : all
              ? `现在 ${item.count || 0} 个 · 机场那边增删节点会自动同步`
              : protocolText(item)}
        </div>
      </div>
      {!item.missing && (
        <Pill tone={chip.tone}>
          {item.group === "source" ? item.sourceName : chip.text}
        </Pill>
      )}
      <Button
        isIconOnly
        aria-label="移除"
        className="min-h-7 min-w-7"
        color="danger"
        size="sm"
        variant="flat"
        onPress={onRemove}
      >
        <X size={14} />
      </Button>
    </div>
  );
}

interface Group {
  key: string;
  title: string;
  filter: Filter;
  nodes: SubPoolNode[];
  source?: SubPoolSource;
  sel: SubSelected["group"];
}

export function SubEditor({
  sub,
  isH5,
  onClose,
  onSaved,
}: {
  sub: SubItem | null;
  isH5: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [pool, setPool] = useState<SubPool | null>(null);
  const [loadError, setLoadError] = useState("");
  const [name, setName] = useState(sub?.name || "");
  const [format, setFormat] = useState<SubFormat>(sub?.format || "auto");
  const [mode, setMode] = useState<SubRuleMode>(sub?.ruleMode || "cn_direct");
  const [selected, setSelected] = useState<SubSelected[]>([]);
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [tab, setTab] = useState<"pool" | "selected" | "routing">("pool");
  const [view, setView] = useState<"nodes" | "routing">("nodes");
  const [saving, setSaving] = useState(false);
  const [routing, setRouting] = useState<SubRouteEntry[]>([]);
  const [addingExternal, setAddingExternal] = useState(false);

  useEffect(() => {
    void subPool(sub?.id).then((res) => {
      if (res.code !== 0) {
        setLoadError(res.msg || "加载失败");

        return;
      }
      setPool(res.data);
      setSelected(res.data.selected || []);
      setRouting(
        normalizeRouting(res.data.routing || [], res.data.routingCatalog || []),
      );
    });
  }, [sub?.id]);

  // After 「＋ 添加」 in the 外部节点 group: refresh the pool only (the
  // unsaved selection and 分流 stay as they are).
  const reloadPool = async () => {
    const res = await subPool(sub?.id);

    if (res.code === 0) setPool(res.data);
  };
  const routingOn = !!pool?.routingCatalog;
  const catalog = pool?.routingCatalog || [];
  const limits = pool?.routingLimits || DEFAULT_LIMITS;

  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 6 } }),
    useSensor(TouchSensor, {
      activationConstraint: { delay: 200, tolerance: 8 },
    }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );

  const keys = useMemo(() => new Set(selected.map((s) => s.key)), [selected]);
  const followed = useMemo(
    () =>
      new Set(
        selected
          .filter((s) => s.group === "source_all")
          .map((s) => s.ref.sourceId || 0),
      ),
    [selected],
  );
  const nodeTotal = selected.reduce(
    (n, s) => n + (s.missing ? 0 : s.group === "source_all" ? s.count || 0 : 1),
    0,
  );
  // Plan 193: at most maxNodes (100) nodes per subscription.
  const maxNodes = pool?.maxNodes || 100;
  const over = nodeTotal > maxNodes;
  // Plan 197: over-limit hints point to the admin (~4 s toast).
  const capToast = { id: "sub-node-cap", duration: 4000 };
  const capMsg = `每条订阅最多 ${maxNodes} 个节点，如需更多请联系管理员`;

  // 分流 「走哪里 → 某个节点」: the nodes now in the subscription (picked
  // ones, and every node of a followed source).
  const nodeOptions: RouteNodeOption[] = useMemo(() => {
    const out: RouteNodeOption[] = [];

    for (const s of selected) {
      if (s.missing) continue;
      if (s.group === "source_all") {
        const src = pool?.sources.find((x) => x.id === s.ref.sourceId);

        src?.nodes.forEach((n) =>
          out.push({ key: n.key, name: n.name, detail: src.name }),
        );
      } else out.push({ key: s.key, name: s.name || s.key });
    }

    return out;
  }, [selected, pool]);
  const nodeNames = useMemo(
    () => new Set(nodeOptions.map((n) => n.name.trim())),
    [nodeOptions],
  );

  const groups: Group[] = useMemo(() => {
    if (!pool) return [];
    const out: Group[] = [
      {
        key: "rule",
        title: "规则 · 我的面板节点",
        filter: "rule",
        nodes: pool.rules,
        sel: "rule",
      },
      {
        key: "landing",
        title: "落地 · 直连",
        filter: "landing",
        nodes: pool.landing,
        sel: "landing",
      },
      {
        key: "external",
        title: "外部节点",
        filter: "external",
        nodes: pool.external,
        sel: "external",
      },
    ];

    for (const s of pool.sources) {
      out.push({
        key: `src:${s.id}`,
        title: `外部订阅 · ${s.name}`,
        filter: `src:${s.id}`,
        nodes: s.nodes,
        source: s,
        sel: "source",
      });
    }

    return out;
  }, [pool]);

  const q = search.trim().toLowerCase();
  const match = (n: SubPoolNode) =>
    !q || `${n.name} ${n.protocol} ${n.detail}`.toLowerCase().includes(q);
  const visibleGroups = groups
    .filter((g) => filter === "all" || g.filter === filter)
    .map((g) => ({ ...g, shown: g.nodes.filter(match) }))
    .filter(
      (g) =>
        g.shown.length > 0 ||
        (!q && (g.key === "rule" || g.key === "external" || !!g.source)),
    );
  const poolTotal = groups.reduce((n, g) => n + g.nodes.length, 0);

  const toItem = (n: SubPoolNode, g: Group): SubSelected => ({
    ref: n.ref,
    key: n.key,
    name: n.name,
    protocol: n.protocol,
    chain: n.chain,
    group: g.sel,
    sourceName: g.source?.name,
  });
  const add = (n: SubPoolNode, g: Group) => {
    if (!keys.has(n.key) && nodeTotal + 1 > maxNodes) {
      toast.error(capMsg, capToast);

      return;
    }
    setSelected((cur) =>
      cur.some((s) => s.key === n.key) ? cur : [...cur, toItem(n, g)],
    );
  };
  const remove = (key: string) =>
    setSelected((cur) => cur.filter((s) => s.key !== key));
  const toggle = (n: SubPoolNode, g: Group) =>
    keys.has(n.key) ? remove(n.key) : add(n, g);
  const addAll = (g: Group) => {
    const adding = g.nodes.filter((n) => !keys.has(n.key)).length;

    if (nodeTotal + adding > maxNodes) {
      toast.error(capMsg, capToast);

      return;
    }
    setSelected((cur) => {
      const have = new Set(cur.map((s) => s.key));

      return [
        ...cur,
        ...g.nodes.filter((n) => !have.has(n.key)).map((n) => toItem(n, g)),
      ];
    });
  };
  const toggleFollow = (s: SubPoolSource) => {
    if (!keys.has(s.followKey)) {
      const singles = selected.filter(
        (x) => x.group === "source" && x.ref.sourceId === s.id && !x.missing,
      ).length;
      const total = nodeTotal - singles + s.nodeCount;

      if (total > maxNodes) {
        toast.error(capMsg, capToast);

        return;
      }
    }
    setSelected((cur) => {
      if (cur.some((x) => x.key === s.followKey))
        return cur.filter((x) => x.key !== s.followKey);

      return [
        ...cur.filter(
          (x) => !(x.group === "source" && x.ref.sourceId === s.id),
        ),
        {
          ref: { kind: "source_all", sourceId: s.id },
          key: s.followKey,
          group: "source_all",
          sourceName: s.name,
          count: s.nodeCount,
        },
      ];
    });
  };

  const onDragEnd = (e: DragEndEvent) => {
    if (!e.over || e.active.id === e.over.id) return;
    setSelected((cur) => {
      const from = cur.findIndex((s) => s.key === e.active.id);
      const to = cur.findIndex((s) => s.key === e.over?.id);

      return from < 0 || to < 0 ? cur : arrayMove(cur, from, to);
    });
  };

  const save = async () => {
    if (over) {
      toast.error(
        `每条订阅最多 ${maxNodes} 个节点，现在是 ${nodeTotal} 个，如需更多请联系管理员`,
        capToast,
      );

      return;
    }
    setSaving(true);
    try {
      const body = {
        id: sub?.id,
        name: name.trim() || "我的订阅",
        format,
        ruleMode: mode,
        items: selected.filter((s) => !s.missing).map((s) => s.ref),
        ...(routingOn ? { routing } : {}),
      };
      const res = sub ? await subUpdate(body) : await subCreate(body);

      if (res.code !== 0) {
        toast.error(res.msg || "保存失败");

        return;
      }
      toast.success(sub ? "已保存，客户端下次更新订阅时生效" : "订阅已创建");
      onSaved();
    } finally {
      setSaving(false);
    }
  };

  const filters: [Filter, string][] = [
    ["all", "全部"],
    ...groups
      .filter((g) => g.nodes.length > 0 || g.source || g.key === "external")
      .map((g): [Filter, string] => [
        g.filter,
        `${g.source ? g.source.name : g.key === "rule" ? "规则" : g.key === "landing" ? "落地" : isH5 ? "外部" : "外部节点"}${isH5 ? "" : ` ${g.nodes.length}`}`,
      ]),
  ];

  const poolColumn = (
    <div className="flex min-h-0 flex-col overflow-hidden rounded-2xl border border-divider">
      <div className="space-y-2 border-b border-divider bg-default-50 px-3 py-2.5 dark:bg-default-100/40">
        {!isH5 && (
          <div className="flex items-center justify-between">
            <span className="text-sm font-semibold">可选节点</span>
            <span className="text-xs text-default-500">共 {poolTotal} 个</span>
          </div>
        )}
        <Input
          aria-label="搜索"
          placeholder={isH5 ? "搜索" : "搜索名称 / 地址 / 协议"}
          size="sm"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <div className="flex flex-wrap gap-1.5">
          {filters.map(([f, text]) => (
            <button
              key={f}
              className={`rounded-lg px-2 py-0.5 text-xs font-semibold ${
                filter === f
                  ? "bg-blue-500/15 text-blue-700 dark:bg-blue-400/20 dark:text-blue-300"
                  : "bg-slate-500/10 text-slate-600 dark:bg-slate-400/15 dark:text-slate-300"
              }`}
              type="button"
              onClick={() => setFilter(f)}
            >
              {text}
            </button>
          ))}
        </div>
      </div>
      <div
        className={`overflow-y-auto ${isH5 ? "flex-1" : "max-h-[52vh] min-h-[320px]"}`}
      >
        {visibleGroups.map((g) => {
          const follow = g.source ? followed.has(g.source.id) : false;
          const allIn =
            !g.source &&
            g.nodes.length > 0 &&
            g.nodes.every((n) => keys.has(n.key));

          return (
            <div key={g.key}>
              <div className="sticky top-0 z-[1] flex items-center gap-2 border-b border-divider bg-default-100 px-3 py-1.5 text-[13px] font-semibold dark:bg-default-50">
                <span className="min-w-0 truncate">
                  {isH5
                    ? g.source
                      ? g.source.name
                      : g.title.split(" · ")[0]
                    : g.title}
                </span>
                {g.source && !isH5 && <Pill tone="amber">每 6 小时更新</Pill>}
                <span className="flex-1" />
                {g.key === "external" && (
                  <button
                    data-external-add-open
                    className="rounded-lg bg-purple-500/15 px-2 py-0.5 text-xs font-semibold text-purple-700 dark:bg-purple-400/20 dark:text-purple-300"
                    title="粘贴你自己的节点链接，或从订阅导入"
                    type="button"
                    onClick={() => setAddingExternal(true)}
                  >
                    ＋ 添加
                  </button>
                )}
                {g.source ? (
                  <button
                    className={`rounded-lg px-2 py-0.5 text-xs font-semibold ${
                      follow
                        ? "bg-amber-500/25 text-amber-800 dark:bg-amber-400/25 dark:text-amber-200"
                        : "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300"
                    }`}
                    title="机场那边加了新节点会自动出现在订阅里"
                    type="button"
                    onClick={() => toggleFollow(g.source!)}
                  >
                    {follow ? "整组跟随 ✓" : "整组跟随"}
                  </button>
                ) : (
                  g.nodes.length > 0 && (
                    <button
                      className="rounded-lg bg-green-500/15 px-2 py-0.5 text-xs font-semibold text-green-700 disabled:opacity-50 dark:bg-green-400/15 dark:text-green-300"
                      disabled={allIn}
                      type="button"
                      onClick={() => addAll(g)}
                    >
                      全部加入
                    </button>
                  )
                )}
              </div>
              {g.key === "rule" && (pool?.portForwards || 0) > 0 && !q && (
                <p className="border-b border-divider px-3 py-1.5 text-xs text-default-400">
                  另有 {pool?.portForwards}{" "}
                  条普通端口转发规则，没有代理协议，不能放进订阅
                </p>
              )}
              {g.key === "rule" && g.nodes.length === 0 && !q && (
                <p className="border-b border-divider px-3 py-2 text-xs text-default-400">
                  接了落地或「我自己的节点」的规则会出现在这里
                </p>
              )}
              {g.key === "external" && g.nodes.length === 0 && !q && (
                <p className="border-b border-divider px-3 py-2 text-xs text-default-400">
                  还没有外部节点，点「＋ 添加」粘贴你自己的节点链接
                </p>
              )}
              {g.source && g.nodes.length === 0 && (
                <p className="border-b border-divider px-3 py-2 text-xs text-default-400">
                  {g.source.lastError
                    ? `还没有拿到节点（${g.source.lastError}）`
                    : "这个订阅源暂时没有节点"}
                </p>
              )}
              {g.shown.map((n) => {
                const inSub = follow || keys.has(n.key);
                const detail = `${protocolText(n)} · ${n.detail}`;

                return isH5 ? (
                  <button
                    key={n.key}
                    className={`flex w-full items-center gap-3 border-b border-divider px-3 py-2 text-left ${follow ? "opacity-70" : ""}`}
                    disabled={follow}
                    type="button"
                    onClick={() => toggle(n, g)}
                  >
                    <span
                      className={`flex h-4 w-4 shrink-0 items-center justify-center rounded border ${
                        inSub
                          ? "border-blue-600 bg-blue-600 text-white"
                          : "border-default-300"
                      }`}
                    >
                      {inSub && <Check size={12} strokeWidth={3} />}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm">{n.name}</span>
                      <span className="block truncate text-xs text-default-500">
                        {detail}
                      </span>
                    </span>
                  </button>
                ) : (
                  <div
                    key={n.key}
                    className="flex items-center gap-2 border-b border-divider px-3 py-2 last:border-b-0"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm">{n.name}</div>
                      <div className="truncate text-xs text-default-500">
                        {detail}
                      </div>
                    </div>
                    {inSub ? (
                      <Button
                        isIconOnly
                        aria-label={follow ? "整组跟随中" : "从订阅移除"}
                        className="min-h-7 min-w-7"
                        isDisabled={follow}
                        size="sm"
                        variant="flat"
                        onPress={() => remove(n.key)}
                      >
                        <Check size={14} />
                      </Button>
                    ) : (
                      <Button
                        isIconOnly
                        aria-label="加入订阅"
                        className="min-h-7 min-w-7"
                        color="success"
                        size="sm"
                        variant="flat"
                        onPress={() => add(n, g)}
                      >
                        <Plus size={14} />
                      </Button>
                    )}
                  </div>
                );
              })}
            </div>
          );
        })}
        {pool && visibleGroups.length === 0 && (
          <p className="p-4 text-center text-sm text-default-400">
            {q ? "没有匹配的节点" : "还没有可以放进订阅的节点"}
          </p>
        )}
      </div>
    </div>
  );

  const selectedColumn = (
    <div className="flex min-h-0 flex-col overflow-hidden rounded-2xl border border-divider">
      {!isH5 && (
        <div className="flex items-center justify-between border-b border-divider bg-default-50 px-3 py-2.5 dark:bg-default-100/40">
          <span className="text-sm font-semibold">
            订阅里的节点 ·{" "}
            <span className={over ? "text-danger" : ""}>
              {nodeTotal} / {maxNodes}
            </span>
          </span>
          <span className="text-xs text-default-500">
            拖动排序 = 客户端里的顺序
          </span>
        </div>
      )}
      {over && (
        <p className="border-b border-divider bg-red-500/10 px-3 py-1.5 text-xs text-red-700 dark:text-red-300">
          超过 {maxNodes} 个节点的上限：客户端只会拿到前 {maxNodes}{" "}
          个，如需更多请联系管理员
        </p>
      )}
      <div
        className={`overflow-y-auto ${isH5 ? "flex-1" : "max-h-[52vh] min-h-[320px]"}`}
      >
        {selected.length === 0 ? (
          <p className="p-6 text-center text-sm text-default-400">
            {isH5
              ? "还没有选节点，去「可选」里勾选"
              : "从左边点「＋」把节点加进来"}
          </p>
        ) : (
          <DndContext
            collisionDetection={closestCenter}
            sensors={sensors}
            onDragEnd={onDragEnd}
          >
            <SortableContext
              items={selected.map((s) => s.key)}
              strategy={verticalListSortingStrategy}
            >
              {selected.map((s) => (
                <SelectedRow
                  key={s.key}
                  item={s}
                  onRemove={() => remove(s.key)}
                />
              ))}
            </SortableContext>
          </DndContext>
        )}
      </div>
    </div>
  );

  const routingPanel = (
    <RoutingPanel
      catalog={catalog}
      defaults={pool?.routingDefaults || []}
      isH5={isH5}
      limits={limits}
      mode={mode}
      nodeNames={nodeNames}
      nodeOptions={nodeOptions}
      routing={routing}
      onChange={setRouting}
      onModeChange={setMode}
    />
  );
  const addExternal = addingExternal && (
    <AddExternalDialog
      shared={false}
      onAdded={() => reloadPool()}
      onClose={() => setAddingExternal(false)}
    />
  );

  const settings = (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
      <Seg
        label="客户端"
        options={FORMATS}
        value={format}
        onChange={setFormat}
      />
    </div>
  );
  const hint = (
    <p className="text-xs text-default-400">
      客户端不支持的协议会自动跳过（例如 sing-box 没有
      Snell），不会让整条订阅报错。
    </p>
  );

  if (isH5) {
    return (
      <>
        <Modal isOpen size="full" onOpenChange={(o) => !o && onClose()}>
          <ModalContent className="flex flex-col gap-0 overflow-hidden p-0">
            <div className="flex items-center gap-1 px-3 pb-2 pt-3">
              <button
                aria-label="返回"
                className="p-1"
                type="button"
                onClick={onClose}
              >
                <ChevronLeft size={20} />
              </button>
              <span className="truncate text-base font-semibold">
                {sub ? `编辑「${sub.name}」` : "新建订阅"}
              </span>
            </div>
            <div className="space-y-2 px-3 pb-2">
              <Input
                aria-label="名称"
                placeholder="名称（客户端里显示为订阅名）"
                size="sm"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <div className="space-y-1.5">
                <Seg
                  label="客户端"
                  options={FORMATS}
                  value={format}
                  onChange={setFormat}
                />
              </div>
              <div className="flex rounded-xl bg-slate-500/10 p-0.5 dark:bg-slate-400/15">
                {(
                  [
                    ["pool", `可选 ${poolTotal}`],
                    ["selected", `已选 ${nodeTotal}/${maxNodes}`],
                    ...(routingOn
                      ? ([["routing", `分流 ${routing.length}`]] as const)
                      : []),
                  ] as const
                ).map(([k, text]) => (
                  <button
                    key={k}
                    className={`flex-1 rounded-lg py-1.5 text-sm ${
                      tab === k
                        ? "bg-white font-semibold shadow-sm dark:bg-slate-700"
                        : "text-default-600"
                    }`}
                    data-editor-tab={k}
                    type="button"
                    onClick={() => setTab(k)}
                  >
                    {text}
                  </button>
                ))}
              </div>
            </div>
            <div className="flex min-h-0 flex-1 flex-col px-3 pb-2">
              {loadError ? (
                <p className="text-sm text-danger">{loadError}</p>
              ) : tab === "pool" ? (
                poolColumn
              ) : tab === "routing" ? (
                routingPanel
              ) : (
                selectedColumn
              )}
            </div>
            <div className="sticky bottom-0 flex gap-2 border-t border-divider bg-content1 px-3 py-2.5">
              <Button
                className="flex-1"
                color="danger"
                variant="flat"
                onPress={onClose}
              >
                取消
              </Button>
              <Button
                className="flex-[2]"
                color="success"
                isDisabled={!pool}
                isLoading={saving}
                onPress={save}
              >
                保存（{nodeTotal}）
              </Button>
            </div>
          </ModalContent>
        </Modal>
        {addExternal}
      </>
    );
  }

  return (
    <>
      <Modal
        isOpen
        scrollBehavior="inside"
        size="4xl"
        onOpenChange={(o) => !o && onClose()}
      >
        <ModalContent>
          <ModalHeader>{sub ? "编辑订阅" : "新建订阅"}</ModalHeader>
          <ModalBody>
            <div className="flex flex-wrap items-center gap-3">
              <Input
                aria-label="名称"
                className="w-72"
                placeholder="订阅名称，例如：日常"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <span className="text-xs text-default-500">
                名称只给自己看，客户端里显示为订阅名
              </span>
            </div>
            {routingOn && (
              <div className="flex gap-1 border-b border-divider">
                {(
                  [
                    ["nodes", "节点", nodeTotal],
                    ["routing", "分流", routing.length],
                  ] as const
                ).map(([k, text, n]) => (
                  <button
                    key={k}
                    className={`-mb-px flex items-center gap-1.5 border-b-2 px-3 pb-2 text-sm ${
                      view === k
                        ? "border-primary font-semibold text-foreground"
                        : "border-transparent text-default-500"
                    }`}
                    data-editor-view={k}
                    type="button"
                    onClick={() => setView(k)}
                  >
                    {text}
                    <Pill tone={k === "routing" ? "cyan" : "slate"}>{n}</Pill>
                  </button>
                ))}
              </div>
            )}
            {loadError ? (
              <p className="text-sm text-danger">{loadError}</p>
            ) : view === "routing" && routingOn ? (
              <div className="min-h-[420px]">
                {routingPanel}
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-4">
                {poolColumn}
                {selectedColumn}
              </div>
            )}
          </ModalBody>
          <ModalFooter className="items-center justify-between">
            <div className="space-y-1">
              {settings}
              {hint}
            </div>
            <div className="flex gap-2">
              <Button color="danger" variant="flat" onPress={onClose}>
                取消
              </Button>
              <Button
                color="success"
                isDisabled={!pool}
                isLoading={saving}
                onPress={save}
              >
                保存
              </Button>
            </div>
          </ModalFooter>
        </ModalContent>
      </Modal>
      {addExternal}
    </>
  );
}
