// Plan 187 公开「探针」页 (/tz). Public, read-only, no login. Smoothness rules:
// first data from a GET fired before this chunk loaded, no spinner after the
// first paint, memoized cards keyed by the opaque node key, CSS transitions
// for bars, tabular numbers, nothing animated on entry.
import type { ProbeNode, ProbeSnapshot } from "./api";

import {
  Suspense,
  lazy,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import * as Dialog from "@radix-ui/react-dialog";

import { isProbeOnlyHost, speedHistory, useProbe } from "./api";
import { CompactRow, NodeTitle, ServerCard, StatusPill } from "./card";
import { formatBytes, formatSpeed } from "./format";
import { Sparkline, seriesColors } from "./sparkline";

import { compareRegions, regionFlag, regionName } from "@/utils/region";
import {
  getEffectiveMode,
  reapplyActiveTheme,
  saveMode,
} from "@/themes/registry";

const loadDetail = () => import("./detail");
const Detail = lazy(loadDetail);

type SortKey = "default" | "cpu" | "net";
type ViewMode = "cards" | "table";

const VIEW_KEY = "flvx-probe-view";
const SORT_KEY = "flvx-probe-sort";

function useCoarseNow(everyMs: number) {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), everyMs);

    return () => clearInterval(id);
  }, [everyMs]);

  return now;
}

function useDarkMode() {
  const [dark, setDark] = useState(() =>
    document.documentElement.classList.contains("dark"),
  );

  useEffect(() => {
    const obs = new MutationObserver(() =>
      setDark(document.documentElement.classList.contains("dark")),
    );

    obs.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });

    return () => obs.disconnect();
  }, []);

  return dark;
}

function stored<T extends string>(
  key: string,
  allowed: readonly T[],
  fallback: T,
): T {
  try {
    const v = localStorage.getItem(key) as T | null;

    if (v && allowed.includes(v)) return v;
  } catch {
    /* private mode */
  }

  return fallback;
}

function Kpi({
  label,
  children,
  sub,
}: {
  label: string;
  children: React.ReactNode;
  sub?: React.ReactNode;
}) {
  return (
    <div className="min-w-0">
      <div className="text-xs text-default-500">{label}</div>
      <div className="mt-0.5 truncate text-lg font-semibold tabular-nums text-foreground sm:text-xl">
        {children}
      </div>
      {sub && (
        <div className="truncate text-xs tabular-nums text-default-500">
          {sub}
        </div>
      )}
    </div>
  );
}

function Header({ snap, dark }: { snap: ProbeSnapshot; dark: boolean }) {
  const online = snap.nodes.filter((n) => n.online);
  let rx = 0;
  let tx = 0;
  let tin = 0;
  let tout = 0;
  let load = 0;

  for (const n of online) {
    rx += n.net_in;
    tx += n.net_out;
    tin += n.traffic_in;
    tout += n.traffic_out;
    load += n.load1;
  }
  const colors = seriesColors(dark);
  const hist = speedHistory;
  const top = Math.max(1, ...hist.map((p) => Math.max(p.rx, p.tx))) * 1.2;

  return (
    <div className="grid grid-cols-2 gap-4 rounded-xl border border-divider bg-content1 p-4 shadow-sm sm:grid-cols-4">
      <Kpi label="在线" sub={`共 ${snap.nodes.length} 台`}>
        <span className="text-green-700 dark:text-green-300">
          {online.length}
        </span>
        <span className="text-default-400"> / {snap.nodes.length}</span>
      </Kpi>
      <div className="col-span-2 min-w-0 sm:col-span-1">
        <div className="flex items-baseline justify-between gap-2">
          <span className="text-xs text-default-500">总网速</span>
          <span className="text-[11px] text-default-400">近 3 分钟</span>
        </div>
        <div className="mt-0.5 flex flex-wrap gap-x-3 text-sm font-semibold tabular-nums">
          <span style={{ color: colors.a }}>↓ {formatSpeed(rx)}</span>
          <span style={{ color: colors.b }}>↑ {formatSpeed(tx)}</span>
        </div>
        <div className="relative mt-1 h-7">
          <Sparkline
            className="absolute inset-0"
            color={colors.a}
            label="总下行速度"
            max={top}
            values={hist.map((p) => p.rx)}
          />
          <Sparkline
            className="absolute inset-0"
            color={colors.b}
            label="总上行速度"
            max={top}
            values={hist.map((p) => p.tx)}
          />
        </div>
      </div>
      <Kpi label="累计流量" sub="在线机器开机以来">
        <span className="whitespace-normal text-sm sm:text-lg">
          ↓ {formatBytes(tin)} <span className="text-default-400">·</span> ↑{" "}
          {formatBytes(tout)}
        </span>
      </Kpi>
      <Kpi label="平均负载" sub="1 分钟">
        {online.length ? (load / online.length).toFixed(2) : "—"}
      </Kpi>
    </div>
  );
}

function ThemeToggle({ dark }: { dark: boolean }) {
  return (
    <button
      aria-label={dark ? "切换到亮色" : "切换到暗色"}
      className="flex h-8 w-8 items-center justify-center rounded-full bg-slate-500/15 text-slate-600 hover:bg-slate-500/25 dark:bg-slate-400/20 dark:text-slate-300"
      type="button"
      onClick={() => {
        saveMode(getEffectiveMode() === "dark" ? "light" : "dark");
        reapplyActiveTheme();
      }}
    >
      {dark ? (
        <svg
          className="h-4 w-4"
          fill="none"
          stroke="currentColor"
          strokeWidth={2}
          viewBox="0 0 24 24"
        >
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
        </svg>
      ) : (
        <svg
          className="h-4 w-4"
          fill="none"
          stroke="currentColor"
          strokeWidth={2}
          viewBox="0 0 24 24"
        >
          <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />
        </svg>
      )}
    </button>
  );
}

function Skeleton() {
  return (
    <div
      aria-hidden
      className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
    >
      {Array.from({ length: 8 }, (_, i) => (
        <div
          key={i}
          className="h-[214px] rounded-xl border border-divider bg-content1 p-4"
        >
          <div className="h-9 w-2/3 rounded-lg bg-default-200/60 dark:bg-default-100/30" />
          <div className="mt-5 grid grid-cols-2 gap-4">
            {Array.from({ length: 4 }, (_, j) => (
              <div
                key={j}
                className="h-6 rounded bg-default-200/50 dark:bg-default-100/20"
              />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

function Notice({ title, text }: { title: string; text: string }) {
  return (
    <div className="mx-auto mt-24 max-w-md rounded-xl border border-divider bg-content1 p-8 text-center shadow-sm">
      <div className="text-lg font-semibold text-foreground">{title}</div>
      <div className="mt-2 text-sm text-default-500">{text}</div>
    </div>
  );
}

const SEG = "rounded-full px-3 py-1 text-xs font-semibold transition-colors";

function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T;
  options: { v: T; t: string }[];
  onChange: (v: T) => void;
  label: string;
}) {
  return (
    <div
      aria-label={label}
      className="flex shrink-0 items-center gap-1 rounded-full bg-slate-500/10 p-0.5 dark:bg-slate-400/10"
      role="group"
    >
      {options.map((o) => (
        <button
          key={o.v}
          aria-pressed={value === o.v}
          className={`${SEG} ${value === o.v ? "bg-teal-500/20 text-teal-700 dark:bg-teal-400/20 dark:text-teal-200" : "text-default-500 hover:text-foreground"}`}
          type="button"
          onClick={() => onChange(o.v)}
        >
          {o.t}
        </button>
      ))}
    </div>
  );
}

export default function ProbeApp() {
  const { snap, status, live } = useProbe();
  const dark = useDarkMode();
  const now = useCoarseNow(30_000);
  const [query, setQuery] = useState("");
  const [region, setRegion] = useState("");
  const [sort, setSort] = useState<SortKey>(() =>
    stored(SORT_KEY, ["default", "cpu", "net"] as const, "default"),
  );
  const [view, setView] = useState<ViewMode>(() =>
    stored(VIEW_KEY, ["cards", "table"] as const, "cards"),
  );
  const [openKey, setOpenKey] = useState<string | null>(null);
  const [probeHost, setProbeHost] = useState<boolean | null>(() => {
    try {
      return localStorage.getItem(
        `flvx_probe_host:${window.location.hostname}`,
      ) === "1"
        ? true
        : null;
    } catch {
      return null;
    }
  });

  useEffect(() => {
    void isProbeOnlyHost().then(setProbeHost);
    // Chart chunk: preload once the page is idle, so the detail opens at once.
    const idle = (window as any).requestIdleCallback as
      | ((cb: () => void) => number)
      | undefined;
    const t = idle
      ? idle(() => void loadDetail())
      : window.setTimeout(() => void loadDetail(), 1500);

    return () => {
      if (!idle) clearTimeout(t);
    };
  }, []);

  useEffect(() => {
    if (snap?.title) document.title = snap.title;
  }, [snap?.title]);

  // A long-open public tab picks up a new release (version.json changes).
  useEffect(() => {
    const id = setInterval(async () => {
      if (document.hidden) return;
      try {
        const res = await fetch("/version.json", { cache: "no-store" });
        const body = await res.json();

        if (
          body?.build &&
          body.build !== __APP_BUILD_ID__ &&
          !String(__APP_BUILD_ID__).startsWith("local")
        ) {
          window.location.reload();
        }
      } catch {
        /* offline */
      }
    }, 15 * 60_000);

    return () => clearInterval(id);
  }, []);

  const nodes = snap?.nodes;
  const nameSig = nodes ? nodes.map((n) => `${n.k}:${n.name}`).join("|") : "";
  const names = useMemo(
    () => new Map((nodes ?? []).map((n) => [n.k, n.name])),
    [nameSig],
  );

  const regions = useMemo(() => {
    const counts = new Map<string, number>();

    for (const n of nodes ?? [])
      counts.set(n.region, (counts.get(n.region) ?? 0) + 1);

    return [...counts.entries()].sort((a, b) => compareRegions(a[0], b[0]));
  }, [nodes]);

  const shown = useMemo(() => {
    let list: ProbeNode[] = nodes ?? [];
    const q = query.trim().toLowerCase();

    if (q)
      list = list.filter(
        (n) =>
          n.name.toLowerCase().includes(q) || regionName(n.region).includes(q),
      );
    if (region)
      list = list.filter((n) => n.region === (region === "-" ? "" : region));
    if (sort !== "default") {
      const val = (n: ProbeNode) =>
        sort === "cpu" ? n.cpu : n.net_in + n.net_out;

      list = [...list].sort(
        (a, b) => Number(b.online) - Number(a.online) || val(b) - val(a),
      );
    }

    return list;
  }, [nodes, query, region, sort]);

  const onOpen = useCallback((k: string) => setOpenKey(k), []);
  const openNode = openKey ? nodes?.find((n) => n.k === openKey) : undefined;
  const lastOpen = useRef<ProbeNode | undefined>(undefined);

  if (openNode) lastOpen.current = openNode;

  const setSortStored = (v: SortKey) => {
    setSort(v);
    try {
      localStorage.setItem(SORT_KEY, v);
    } catch {
      /* ignore */
    }
  };
  const setViewStored = (v: ViewMode) => {
    setView(v);
    try {
      localStorage.setItem(VIEW_KEY, v);
    } catch {
      /* ignore */
    }
  };

  return (
    <div className="min-h-screen bg-default-50/60 text-foreground dark:bg-background">
      <div className="mx-auto max-w-[1400px] px-3 pb-10 pt-4 sm:px-6">
        <header className="mb-4 flex items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2">
            <h1 className="truncate text-lg font-bold text-foreground sm:text-xl">
              {snap?.title ?? "探针"}
            </h1>
            {status === "ok" && (
              <span
                className={`h-2 w-2 shrink-0 rounded-full ${live ? "bg-green-500" : "bg-amber-500"}`}
                title={live ? "实时更新中" : "实时连接中断，每 5 秒刷新"}
              />
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {probeHost === false && (
              <a
                className="rounded-full bg-teal-500/15 px-3 py-1 text-xs font-semibold text-teal-700 hover:bg-teal-500/25 dark:bg-teal-400/15 dark:text-teal-300"
                href="/"
              >
                登录面板
              </a>
            )}
            <ThemeToggle dark={dark} />
          </div>
        </header>

        {status === "off" && (
          <Notice text="管理员还没有打开公开探针页。" title="探针未开启" />
        )}
        {status === "error" && !snap && (
          <Notice
            text="暂时无法读取服务器状态，稍后会自动重试。"
            title="连接失败"
          />
        )}
        {status === "loading" && !snap && <Skeleton />}

        {snap && status !== "off" && (
          <>
            <Header dark={dark} snap={snap} />
            <div className="my-4 flex flex-wrap items-center gap-2">
              <input
                aria-label="搜索服务器"
                className="h-8 w-full rounded-full border border-divider bg-content1 px-3 text-sm text-foreground outline-none placeholder:text-default-400 focus:border-teal-500 sm:w-56"
                placeholder="搜索名称或地区"
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              {regions.length > 1 && (
                <div className="flex flex-wrap gap-1.5">
                  <button
                    className={`${SEG} ${region === "" ? "bg-teal-500/20 text-teal-700 dark:bg-teal-400/20 dark:text-teal-200" : "bg-slate-500/10 text-default-600 dark:bg-slate-400/10"}`}
                    type="button"
                    onClick={() => setRegion("")}
                  >
                    全部 {snap.nodes.length}
                  </button>
                  {regions.map(([code, count]) => (
                    <button
                      key={code || "-"}
                      className={`${SEG} ${region === (code || "-") ? "bg-teal-500/20 text-teal-700 dark:bg-teal-400/20 dark:text-teal-200" : "bg-slate-500/10 text-default-600 dark:bg-slate-400/10"}`}
                      type="button"
                      onClick={() =>
                        setRegion(region === (code || "-") ? "" : code || "-")
                      }
                    >
                      {regionFlag(code)} {regionName(code)} {count}
                    </button>
                  ))}
                </div>
              )}
              <div className="ml-auto flex items-center gap-2">
                <Segmented
                  label="排序"
                  options={[
                    { v: "default", t: "默认" },
                    { v: "cpu", t: "CPU" },
                    { v: "net", t: "网速" },
                  ]}
                  value={sort}
                  onChange={setSortStored}
                />
                <Segmented
                  label="显示方式"
                  options={[
                    { v: "cards", t: "卡片" },
                    { v: "table", t: "表格" },
                  ]}
                  value={view}
                  onChange={setViewStored}
                />
              </div>
            </div>

            {shown.length === 0 && (
              <div className="py-16 text-center text-sm text-default-500">
                没有符合条件的服务器
              </div>
            )}

            {view === "cards" ? (
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                {shown.map((n) => (
                  <ServerCard
                    key={n.k}
                    names={names}
                    node={n}
                    now={n.online ? 0 : now}
                    onOpen={onOpen}
                  />
                ))}
              </div>
            ) : (
              shown.length > 0 && (
                <div className="overflow-x-auto rounded-xl border border-divider bg-content1 shadow-sm">
                  <table className="w-full min-w-[960px] text-sm">
                    <thead>
                      <tr className="text-left text-xs text-default-500">
                        {[
                          "服务器",
                          "状态",
                          "CPU",
                          "内存",
                          "硬盘",
                          "负载",
                          "网速",
                          "流量",
                          "TCP",
                          "延迟",
                        ].map((h) => (
                          <th key={h} className="px-3 py-2 font-medium">
                            {h}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {shown.map((n) => (
                        <CompactRow
                          key={n.k}
                          names={names}
                          node={n}
                          now={n.online ? 0 : now}
                          onOpen={onOpen}
                        />
                      ))}
                    </tbody>
                  </table>
                </div>
              )
            )}
            <div className="mt-6 text-center text-[11px] text-default-400">
              只显示管理员公开的服务器状态 · 每 2 秒更新
            </div>
          </>
        )}
      </div>

      <Dialog.Root
        open={!!openKey}
        onOpenChange={(o) => !o && setOpenKey(null)}
      >
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40 dark:bg-black/60" />
          <Dialog.Content
            aria-describedby={undefined}
            className="fixed inset-x-0 bottom-0 z-50 max-h-[92dvh] overflow-y-auto rounded-t-2xl border border-divider bg-content1 p-4 shadow-xl focus:outline-none sm:inset-auto sm:left-1/2 sm:top-1/2 sm:w-[min(920px,94vw)] sm:-translate-x-1/2 sm:-translate-y-1/2 sm:rounded-2xl sm:p-6"
          >
            {lastOpen.current && (
              <>
                <div className="mb-3 flex items-start justify-between gap-3">
                  <Dialog.Title asChild>
                    <div>
                      <NodeTitle node={lastOpen.current} size="lg" />
                    </div>
                  </Dialog.Title>
                  <div className="flex shrink-0 items-center gap-2">
                    <StatusPill node={lastOpen.current} now={now} />
                    <Dialog.Close asChild>
                      <button
                        className="rounded-full bg-red-500/15 px-3 py-1 text-xs font-semibold text-red-700 hover:bg-red-500/25 dark:bg-red-400/20 dark:text-red-300"
                        type="button"
                      >
                        关闭
                      </button>
                    </Dialog.Close>
                  </div>
                </div>
                <Suspense fallback={<div className="h-[520px]" />}>
                  <Detail dark={dark} names={names} node={lastOpen.current} />
                </Suspense>
              </>
            )}
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
      {!snap && status !== "off" && <span className="sr-only">正在读取</span>}
    </div>
  );
}
