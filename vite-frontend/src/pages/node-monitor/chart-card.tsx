import type {
  LineMonitorChart,
  LineMonitorData,
  LineMonitorLine,
  LineMonitorRange,
  LineMonitorSeries,
  LineMonitorStats,
} from "@/api/line-monitor";

import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceArea,
  ReferenceDot,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { ArrowDown, ArrowUp } from "lucide-react";

import {
  LIVE_RANGES,
  RANGE_OPTIONS,
  STATUS_CLOSED_DARK,
  STATUS_CLOSED_LIGHT,
  STATUS_DOWN,
  STATUS_LOSS,
  STATUS_NODATA_DARK,
  STATUS_NODATA_LIGHT,
  type StoredRange,
  fmtClock,
  fmtDateTime,
  fmtDuration,
  fmtMs,
  fmtPct,
  fromLocalInput,
  lineColor,
  lineSubtitle,
  lineTitle,
  loadRange,
  saveRange,
  tickLabel,
  timeTicks,
  toLocalInput,
} from "./format";
import { CLOSED_PILL, CheckPortRows } from "./check-port";

import { getLineMonitorData } from "@/api/line-monitor";
import { useTapTooltip } from "@/hooks/useTapTooltip";
import { landingToneClass, type LandingTone } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Input } from "@/shadcn-bridge/heroui/input";

// Plot geometry shared by the recharts plot, the status lanes and the time
// axis below them (same left/right insets → aligned columns).
const Y_AXIS_W = 46;
const RIGHT_INSET = 12;
const TOP_INSET = 8;
const LIVE_REFRESH_MS = 30_000;

type BucketState = "ok" | "loss" | "down" | "closed" | "nodata" | "none";

// Merged buckets (step > 60 s) mark 丢包 only when ≥ 5 % of the measured
// slots were 丢包 (one lost probe in a 30-min bucket is noise at 30 天); the
// tooltip and the stats table keep the exact counts. 断流 is always shown.
const LOSS_SHOW_RATIO = 0.05;

function bucketState(s: LineMonitorSeries, j: number, step = 30): BucketState {
  const down = s.down[j] ?? 0;
  const loss = s.loss[j] ?? 0;
  const closed = s.closed?.[j] ?? 0;
  const measured = (s.ok[j] ?? 0) + loss + down;
  const exp = s.exp[j] ?? 0;

  if (down > 0) return "down";
  // plan 255 part 2: 端口未开启 (both attempts refused) is not 断流
  if (closed > 0 && closed >= measured) return "closed";
  if (loss > 0 && (step <= 60 || loss >= measured * LOSS_SHOW_RATIO))
    return "loss";
  if (exp > 0 && (measured + closed) * 2 < exp) return "nodata";
  if (measured > 0) return "ok";

  return "none";
}

/** Consecutive bucket runs [start, end] (inclusive) where pred holds. */
function runs(n: number, pred: (j: number) => boolean): [number, number][] {
  const out: [number, number][] = [];
  let start = -1;

  for (let j = 0; j <= n; j++) {
    const on = j < n && pred(j);

    if (on && start < 0) start = j;
    if (!on && start >= 0) {
      out.push([start, j - 1]);
      start = -1;
    }
  }

  return out;
}

const niceCeil = (v: number) => {
  if (!Number.isFinite(v) || v <= 0) return 10;
  const exp = Math.pow(10, Math.floor(Math.log10(v)));
  const f = v / exp;
  const nice =
    [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10].find((n) => f <= n) ?? 10;

  return nice * exp;
};

function Pill({
  tone,
  children,
  title,
  className,
}: {
  tone: LandingTone;
  children: React.ReactNode;
  title?: string;
  className?: string;
}) {
  return (
    <span
      className={`inline-flex max-w-full shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 text-xs font-semibold leading-[18px] ${className ?? landingToneClass[tone]}`}
      title={title}
    >
      {children}
    </span>
  );
}

function liveTone(status: number): LandingTone {
  return status === 1
    ? "green"
    : status === 2
      ? "amber"
      : status === 3
        ? "red"
        : "slate";
}

const statusText = (status: number) =>
  status === 1
    ? "正常"
    : status === 2
      ? "丢包"
      : status === 3
        ? "断流"
        : status === 4
          ? "端口未开启"
          : "无数据";

/** grey-blue pill class for 端口未开启 (plan 255 part 2). */
const liveClass = (status: number) => (status === 4 ? CLOSED_PILL : undefined);

interface View extends StoredRange {
  /** drag / 断流记录 zoom window (returns to `range` on 重置) */
  zoom?: { from: number; to: number };
}

interface ChartCardProps {
  chart: LineMonitorChart;
  index: number;
  total: number;
  dark: boolean;
  isMobile: boolean;
  onEdit: (chart: LineMonitorChart) => void;
  onDelete: (chart: LineMonitorChart) => void;
  onMove: (chart: LineMonitorChart, dir: -1 | 1) => void;
  onPause: (line: LineMonitorLine, paused: boolean) => void;
  /** plan 255 part 2: 换一个端口 / 重新开启 / 复制开启命令 */
  onCheckPort?: (
    line: LineMonitorLine,
    action: "rotate" | "verify" | "command",
  ) => void;
  checkBusyId?: number | null;
}

export const ChartCard = memo(function ChartCard({
  chart,
  index,
  total,
  dark,
  isMobile,
  onEdit,
  onDelete,
  onMove,
  onPause,
  onCheckPort,
  checkBusyId = null,
}: ChartCardProps) {
  const [view, setView] = useState<View>(() => loadRange(chart.id));
  const [data, setData] = useState<LineMonitorData | null>(null);
  const [error, setError] = useState("");
  const [customOpen, setCustomOpen] = useState(false);
  const [customFrom, setCustomFrom] = useState("");
  const [customTo, setCustomTo] = useState("");
  const [showAllEvents, setShowAllEvents] = useState(false);
  const reqSeq = useRef(0);

  const linesKey = chart.lines
    .map((l) =>
      [
        l.id,
        l.paused,
        l.port,
        l.effectiveHost,
        l.label,
        l.color,
        l.sourceName,
        l.targetName,
      ].join(":"),
    )
    .join(",");
  const query = useMemo(() => {
    if (view.zoom)
      return {
        range: "custom" as LineMonitorRange,
        from: view.zoom.from,
        to: view.zoom.to,
      };
    if (view.range === "custom")
      return {
        range: "custom" as LineMonitorRange,
        from: view.from,
        to: view.to,
      };

    return { range: view.range };
  }, [view]);
  const live = !view.zoom && LIVE_RANGES.has(view.range);

  const load = useCallback(async () => {
    const seq = ++reqSeq.current;
    const res = await getLineMonitorData({ chartId: chart.id, ...query });

    if (seq !== reqSeq.current) return;
    if (res.code === 0 && res.data) {
      setData(res.data);
      setError("");
    } else {
      setError(res.msg || "加载失败");
    }
  }, [chart.id, query]);

  useEffect(() => {
    void load();
  }, [load, linesKey]);

  useEffect(() => {
    if (!live) return;
    const tick = () => {
      if (!document.hidden) void load();
    };
    const timer = window.setInterval(tick, LIVE_REFRESH_MS);
    const onVisible = () => {
      if (!document.hidden) void load();
    };

    document.addEventListener("visibilitychange", onVisible);

    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [live, load]);

  const pickRange = (key: Exclude<LineMonitorRange, "custom">) => {
    const next: View = { range: key };

    setCustomOpen(false);
    setView(next);
    saveRange(chart.id, next);
  };

  const openCustom = () => {
    const now = Math.floor(Date.now() / 1000);
    const from = data?.from ?? now - 3600;
    const to = Math.min(data?.to ?? now, now);

    setCustomFrom(toLocalInput(from));
    setCustomTo(toLocalInput(to));
    setCustomOpen((v) => !v);
  };

  const applyCustom = () => {
    const from = fromLocalInput(customFrom);
    const to = fromLocalInput(customTo);

    if (!from || !to || to <= from) {
      setError("请选择正确的开始和结束时间");

      return;
    }
    const next: View = { range: "custom", from, to };

    setView(next);
    saveRange(chart.id, next);
    setCustomOpen(false);
  };

  const zoomTo = useCallback((from: number, to: number) => {
    const now = Math.floor(Date.now() / 1000);

    from = Math.max(from, now - 30 * 86400);
    to = Math.min(to, now);
    if (to - from < 120) {
      const mid = (from + to) / 2;

      from = Math.floor(mid - 60);
      to = Math.ceil(mid + 60);
    }
    setView((v) => ({
      ...v,
      zoom: { from: Math.floor(from), to: Math.ceil(to) },
    }));
  }, []);

  const resetZoom = () => setView((v) => ({ ...v, zoom: undefined }));

  // ── derived chart data ──
  const seriesById = useMemo(() => {
    const m = new Map<number, LineMonitorSeries>();

    data?.lines.forEach((s) => m.set(s.id, s));

    return m;
  }, [data]);
  const statsById = useMemo(() => {
    const m = new Map<number, LineMonitorStats>();

    data?.stats.forEach((s) => m.set(s.lineId, s));

    return m;
  }, [data]);
  // The page reloads the list every 60 s; keep the same lines (and skip the
  // chart re-render) unless something shown actually changed.

  const lines = useMemo(() => chart.lines, [linesKey]);
  const colors = useMemo(
    () => lines.map((l) => lineColor(l.color, dark)),
    [lines, dark],
  );

  const model = useMemo(() => {
    if (!data) return null;
    const n =
      data.lines[0]?.avg.length ??
      Math.max(0, Math.round((data.to - data.from) / data.step));
    const rows: Record<string, number | null>[] = new Array(n);
    let top = 0;

    for (let j = 0; j < n; j++) {
      const row: Record<string, number | null> = {
        t: data.from + j * data.step,
      };

      for (const l of lines) {
        const s = seriesById.get(l.id);
        const v = s ? s.avg[j] : null;

        row[`l${l.id}`] = v ?? null;
        if (v != null && v > top) top = v;
      }
      rows[j] = row;
    }
    const states = lines.map((l) => {
      const s = seriesById.get(l.id);

      return s
        ? Array.from({ length: n }, (_, j) => bucketState(s, j, data.step))
        : new Array<BucketState>(n).fill("none");
    });
    const downRuns = runs(n, (j) => states.some((st) => st[j] === "down"));
    const noDataRuns = runs(
      n,
      (j) =>
        states.some((st) => st[j] === "nodata") &&
        states.every((st) => st[j] === "nodata" || st[j] === "none"),
    );
    const markers: { key: string; t: number; v: number; color: string }[] = [];

    lines.forEach((l, li) => {
      const s = seriesById.get(l.id);

      if (!s) return;
      for (let j = 0; j < n; j++) {
        const v = s.avg[j];

        if (v == null) continue;
        if (s.down[j] > 0)
          markers.push({
            key: `d${l.id}-${j}`,
            t: data.from + j * data.step,
            v,
            color: STATUS_DOWN,
          });
        else if (states[li][j] === "loss")
          markers.push({
            key: `l${l.id}-${j}`,
            t: data.from + j * data.step,
            v,
            color: STATUS_LOSS,
          });
      }
    });

    return {
      n,
      rows,
      states,
      downRuns,
      noDataRuns,
      markers,
      yMax: niceCeil(top * 1.15),
    };
  }, [data, lines, seriesById]);

  const tap = useTapTooltip<HTMLDivElement>();
  const dragMoved = useRef(false);
  const onChartClick = tap.onChartClick;
  const handleChartClick = useCallback(
    (state?: unknown, event?: unknown) => {
      if (dragMoved.current) {
        dragMoved.current = false;

        return;
      }
      onChartClick(state as never, event as never);
    },
    [onChartClick],
  );

  const height = isMobile ? 220 : 280;
  const surface = dark ? "#18181b" : "#ffffff";
  const gridColor = dark ? "rgba(255,255,255,0.08)" : "rgba(0,0,0,0.07)";
  const nodata = dark ? STATUS_NODATA_DARK : STATUS_NODATA_LIGHT;
  const closedColor = dark ? STATUS_CLOSED_DARK : STATUS_CLOSED_LIGHT;

  const tooltipContent = useCallback(
    ({ label }: { label?: unknown }) => {
      if (!data || label == null) return null;
      const t = Number(label);
      const j = Math.round((t - data.from) / data.step);

      return (
        <div className="min-w-[220px] max-w-[320px] rounded-lg bg-black/85 px-3 py-2 text-xs text-white shadow-lg">
          <div className="mb-1 font-semibold">
            {data.step <= 30
              ? fmtDateTime(t, true)
              : `${fmtDateTime(t)} – ${fmtClock(t + data.step)}`}
          </div>
          {lines.map((l, i) => {
            const s = seriesById.get(l.id);

            if (!s) return null;
            const st = bucketState(s, j, data.step);
            const closed = s.closed?.[j] ?? 0;
            const measured = s.ok[j] + s.loss[j] + s.down[j];
            const gap = Math.max(0, s.exp[j] - measured - closed);

            return (
              <div key={l.id} className="flex items-start gap-2 py-0.5">
                <span
                  className="mt-1 h-2 w-2 shrink-0 rounded-full"
                  style={{ background: colors[i] }}
                />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-white/80">
                    {l.label || lineTitle(l)}
                  </div>
                  <div className="flex flex-wrap gap-x-2">
                    <span className="font-semibold">{fmtMs(s.avg[j])}</span>
                    {data.step > 30 && s.max[j] != null && (
                      <span className="text-white/70">
                        最高 {fmtMs(s.max[j])}
                      </span>
                    )}
                    {s.down[j] > 0 && (
                      <span style={{ color: "#ff8a8d" }}>
                        断流 {s.down[j]} 次
                      </span>
                    )}
                    {s.loss[j] > 0 && (
                      <span style={{ color: "#ffd17a" }}>
                        丢包 {s.loss[j]} 次
                      </span>
                    )}
                    {closed > 0 && (
                      <span style={{ color: "#a9c4ea" }}>
                        端口未开启 {closed} 次
                      </span>
                    )}
                    {st !== "none" && gap > 0 && (
                      <span className="text-white/60">无数据 {gap} 次</span>
                    )}
                    {st === "none" && <span className="text-white/60">—</span>}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      );
    },
    [data, lines, seriesById, colors],
  );

  const chartEl = useMemo(() => {
    if (!data || !model) return null;

    return (
      <ResponsiveContainer height="100%" width="100%">
        <LineChart
          accessibilityLayer={false}
          data={model.rows}
          margin={{ top: TOP_INSET, right: RIGHT_INSET, bottom: 0, left: 0 }}
          onClick={handleChartClick}
        >
          <defs>
            <pattern
              height="6"
              id={`lm-hatch-${chart.id}`}
              patternTransform="rotate(45)"
              patternUnits="userSpaceOnUse"
              width="6"
            >
              <rect fill={nodata} fillOpacity={0.35} height="6" width="2.5" />
            </pattern>
          </defs>
          <CartesianGrid stroke={gridColor} vertical={false} />
          <XAxis
            hide
            dataKey="t"
            domain={[data.from, data.to]}
            scale="time"
            type="number"
          />
          <YAxis
            allowDecimals={false}
            axisLine={false}
            domain={[0, model.yMax]}
            fontSize={11}
            tickFormatter={(v: number) => `${v}`}
            tickLine={false}
            width={Y_AXIS_W}
          />
          {model.noDataRuns.map(([a, b]) => (
            <ReferenceArea
              key={`n${a}`}
              fill={`url(#lm-hatch-${chart.id})`}
              fillOpacity={1}
              ifOverflow="hidden"
              x1={data.from + a * data.step}
              x2={data.from + (b + 1) * data.step}
            />
          ))}
          {model.downRuns.map(([a, b]) => (
            <ReferenceArea
              key={`d${a}`}
              fill={STATUS_DOWN}
              fillOpacity={dark ? 0.2 : 0.13}
              ifOverflow="hidden"
              x1={data.from + a * data.step}
              x2={data.from + Math.max(b + 1, a + 1) * data.step}
            />
          ))}
          <Tooltip
            content={tooltipContent as never}
            cursor={{
              stroke: dark ? "#a1a1aa" : "#71717a",
              strokeDasharray: "3 3",
            }}
            isAnimationActive={false}
            {...tap.tooltipProps}
          />
          {lines.map((l, i) => (
            <Line
              key={l.id}
              activeDot={tap.activeDot({
                r: 4,
                stroke: surface,
                strokeWidth: 2,
              })}
              connectNulls={false}
              dataKey={`l${l.id}`}
              dot={false}
              isAnimationActive={false}
              name={l.label || lineTitle(l)}
              stroke={colors[i]}
              strokeWidth={2}
              type="linear"
            />
          ))}
          {model.markers.map((m) => (
            <ReferenceDot
              key={m.key}
              isFront
              fill={m.color}
              ifOverflow="hidden"
              r={4}
              stroke={surface}
              strokeWidth={1.5}
              x={m.t}
              y={m.v}
            />
          ))}
        </LineChart>
      </ResponsiveContainer>
    );
  }, [
    data,
    model,
    chart.id,
    colors,
    dark,
    gridColor,
    handleChartClick,
    lines,
    nodata,
    surface,
    tap,
    tooltipContent,
  ]);

  // ── drag to zoom (mouse only; touch scrolls the page) ──
  const dragRef = useRef<{
    x0: number;
    left: number;
    width: number;
    offset: number;
    active: boolean;
  } | null>(null);
  const [sel, setSel] = useState<{ a: number; b: number } | null>(null);
  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.pointerType !== "mouse" || e.button !== 0 || !data) return;
    const grid = tap.containerRef.current?.querySelector(
      ".recharts-cartesian-grid",
    );
    const rect = (grid ?? tap.containerRef.current)?.getBoundingClientRect();

    const box = tap.containerRef.current?.getBoundingClientRect();

    if (!rect || !box || e.clientX < rect.left || e.clientX > rect.right)
      return;
    dragRef.current = {
      x0: e.clientX,
      left: rect.left,
      width: rect.width,
      offset: rect.left - box.left,
      active: true,
    };
    dragMoved.current = false;
  };
  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const d = dragRef.current;

    if (!d?.active) return;
    if (Math.abs(e.clientX - d.x0) > 6) dragMoved.current = true;
    if (dragMoved.current) {
      const clamp = (x: number) => Math.min(Math.max(x - d.left, 0), d.width);

      setSel({
        a: d.offset + clamp(Math.min(d.x0, e.clientX)),
        b: d.offset + clamp(Math.max(d.x0, e.clientX)),
      });
    }
  };
  const onPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    const d = dragRef.current;

    dragRef.current = null;
    setSel(null);
    if (!d?.active || !dragMoved.current || !data) return;
    const x = (px: number) =>
      data.from +
      (Math.min(Math.max(px - d.left, 0), d.width) / d.width) *
        (data.to - data.from);
    const from = x(Math.min(d.x0, e.clientX));
    const to = x(Math.max(d.x0, e.clientX));

    if (to - from >= 30) zoomTo(from, to);
  };

  const span = data ? data.to - data.from : 3600;
  const ticks = useMemo(
    () => (data ? timeTicks(data.from, data.to, isMobile ? 4 : 7) : []),
    [data, isMobile],
  );
  const events = data?.events ?? [];
  const shownEvents = showAllEvents ? events : events.slice(0, 8);
  const lineIndex = useMemo(
    () => new Map(lines.map((l, i) => [l.id, i])),
    [lines],
  );

  return (
    <Card className="overflow-hidden" data-testid={`lm-chart-${chart.id}`}>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <h3 className="truncate text-base font-semibold">{chart.name}</h3>
          <Pill tone="purple">{lines.length} 条线路</Pill>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {index > 0 && (
            <Button
              isIconOnly
              aria-label="上移"
              size="sm"
              variant="flat"
              onPress={() => onMove(chart, -1)}
            >
              <ArrowUp className="h-4 w-4" />
            </Button>
          )}
          {index < total - 1 && (
            <Button
              isIconOnly
              aria-label="下移"
              size="sm"
              variant="flat"
              onPress={() => onMove(chart, 1)}
            >
              <ArrowDown className="h-4 w-4" />
            </Button>
          )}
          <Button
            color="primary"
            size="sm"
            variant="flat"
            onPress={() => onEdit(chart)}
          >
            编辑
          </Button>
          <Button
            color="danger"
            size="sm"
            variant="flat"
            onPress={() => onDelete(chart)}
          >
            删除
          </Button>
        </div>
      </CardHeader>
      <CardBody className="space-y-3 pt-0">
        {/* range picker */}
        <div className="flex flex-wrap items-center gap-1.5">
          {RANGE_OPTIONS.map((o) => {
            const on = !view.zoom && view.range === o.key;

            return (
              <Button
                key={o.key}
                color={on ? "primary" : "default"}
                data-testid={`lm-range-${o.key}`}
                size="sm"
                variant={on ? "solid" : "flat"}
                onPress={() => pickRange(o.key)}
              >
                {o.label}
              </Button>
            );
          })}
          <Button
            color={
              !view.zoom && view.range === "custom" ? "primary" : "secondary"
            }
            data-testid="lm-range-custom"
            size="sm"
            variant={!view.zoom && view.range === "custom" ? "solid" : "flat"}
            onPress={openCustom}
          >
            自定义
          </Button>
          {view.zoom && (
            <>
              <Pill tone="cyan">
                已放大 {fmtDateTime(view.zoom.from)} –{" "}
                {fmtDateTime(view.zoom.to)}
              </Pill>
              <Button
                color="warning"
                data-testid="lm-reset"
                size="sm"
                variant="flat"
                onPress={resetZoom}
              >
                重置
              </Button>
            </>
          )}
          {!view.zoom && view.range === "custom" && view.from && view.to && (
            <Pill tone="slate">
              {fmtDateTime(view.from)} – {fmtDateTime(view.to)}
            </Pill>
          )}
        </div>
        {customOpen && (
          <div className="flex flex-wrap items-end gap-2 rounded-lg bg-default-100/60 p-2 dark:bg-default-50/10">
            <Input
              className="min-w-0"
              classNames={{ base: "w-full sm:w-56" }}
              label="开始"
              max={toLocalInput(Math.floor(Date.now() / 1000))}
              min={toLocalInput(Math.floor(Date.now() / 1000) - 30 * 86400)}
              size="sm"
              type="datetime-local"
              value={customFrom}
              onChange={(e) => setCustomFrom(e.target.value)}
            />
            <Input
              className="min-w-0"
              classNames={{ base: "w-full sm:w-56" }}
              label="结束"
              max={toLocalInput(Math.floor(Date.now() / 1000))}
              min={toLocalInput(Math.floor(Date.now() / 1000) - 30 * 86400)}
              size="sm"
              type="datetime-local"
              value={customTo}
              onChange={(e) => setCustomTo(e.target.value)}
            />
            <Button
              color="primary"
              size="sm"
              variant="flat"
              onPress={applyCustom}
            >
              应用
            </Button>
            <span className="w-full text-xs text-default-500">
              可选最近 30 天内任意时间段。
            </span>
          </div>
        )}

        {/* legend */}
        <div className="flex flex-wrap gap-x-4 gap-y-1.5">
          {lines.map((l, i) => {
            const lv = data?.live?.[String(l.id)] ?? l.live;
            const fresh = lv && Date.now() / 1000 - lv.ts < 150;

            return (
              <div
                key={l.id}
                className="flex min-w-0 max-w-full items-center gap-1.5 text-sm"
              >
                <span
                  className="h-2.5 w-2.5 shrink-0 rounded-full"
                  style={{ background: colors[i] }}
                />
                <span
                  className="min-w-0 truncate font-medium"
                  title={`${lineTitle(l)}  ${lineSubtitle(l)}`}
                >
                  {l.label ? `${l.label} · ` : ""}
                  {lineTitle(l)}
                </span>
                <span className="hidden shrink-0 text-xs text-default-400 sm:inline">
                  {lineSubtitle(l)}
                </span>
                {l.paused === 1 ? (
                  <Pill tone="slate">已暂停</Pill>
                ) : fresh ? (
                  <Pill
                    className={liveClass(lv.status)}
                    title={lv.reason}
                    tone={liveTone(lv.status)}
                  >
                    {lv.status === 1 || lv.status === 2
                      ? fmtMs(lv.ms)
                      : statusText(lv.status)}
                    {lv.status === 0 && lv.reason ? `·${lv.reason}` : ""}
                  </Pill>
                ) : null}
                <button
                  className={`shrink-0 rounded-full px-2 text-xs font-semibold leading-[18px] ${landingToneClass[l.paused === 1 ? "green" : "slate"]}`}
                  type="button"
                  onClick={() => onPause(l, l.paused !== 1)}
                >
                  {l.paused === 1 ? "恢复" : "暂停"}
                </button>
              </div>
            );
          })}
        </div>

        {/* plan 255 part 2: 检测端口 per line */}
        {onCheckPort && (
          <CheckPortRows
            busyId={checkBusyId}
            colors={colors}
            lines={lines}
            live={(l) => {
              const lv = data?.live?.[String(l.id)] ?? l.live;

              if (l.paused === 1 || !lv || Date.now() / 1000 - lv.ts > 150)
                return undefined;

              return lv.status;
            }}
            onAction={onCheckPort}
          />
        )}

        {/* chart */}
        {error && !data ? (
          <div className="py-10 text-center text-sm text-danger">{error}</div>
        ) : !data || !model ? (
          <div
            className="animate-pulse rounded-lg bg-default-100 dark:bg-default-50/10"
            style={{ height: height + 40 }}
          />
        ) : (
          <div
            ref={tap.containerRef}
            className="relative select-none [&_g:focus]:outline-none [&_svg:focus]:outline-none"
            data-testid="lm-plot"
            onPointerDown={onPointerDown}
            onPointerLeave={() => {
              dragRef.current = null;
              setSel(null);
            }}
            onPointerMove={onPointerMove}
            onPointerUp={onPointerUp}
          >
            <div
              className="text-[11px] text-default-400"
              style={{ paddingLeft: 4 }}
            >
              延迟 ms
            </div>
            <div style={{ height }}>{chartEl}</div>
            {/* status lanes: one per line, same x as the plot */}
            <div className="mt-1 space-y-[3px]" data-testid="lm-lanes">
              {lines.map((l, i) => {
                const st = model.states[i];
                const segs = runs(
                  model.n,
                  (j) => st[j] !== "ok" && st[j] !== "none",
                );

                return (
                  <div
                    key={l.id}
                    className="relative flex items-center"
                    style={{ height: isMobile ? 6 : 8 }}
                  >
                    <span
                      className="absolute h-2 w-2 rounded-full"
                      style={{ background: colors[i], left: Y_AXIS_W - 14 }}
                      title={lineTitle(l)}
                    />
                    <div
                      className="relative h-full overflow-hidden rounded-[3px]"
                      style={{
                        marginLeft: Y_AXIS_W,
                        marginRight: RIGHT_INSET,
                        width: `calc(100% - ${Y_AXIS_W + RIGHT_INSET}px)`,
                        background: `${colors[i]}${dark ? "40" : "33"}`,
                      }}
                    >
                      {segs.flatMap(([a, b]) => {
                        // split a run into same-state pieces
                        const out: React.ReactNode[] = [];
                        let s0 = a;

                        for (let j = a; j <= b + 1; j++) {
                          if (j === b + 1 || st[j] !== st[s0]) {
                            const kind = st[s0];
                            const style: React.CSSProperties = {
                              left: `${(s0 / model.n) * 100}%`,
                              width: `max(2px, ${((j - s0) / model.n) * 100}%)`,
                            };

                            if (kind === "down") style.background = STATUS_DOWN;
                            else if (kind === "loss")
                              style.background = STATUS_LOSS;
                            else if (kind === "closed")
                              style.background = closedColor;
                            else
                              style.background = `repeating-linear-gradient(45deg, ${nodata}99 0 2px, transparent 2px 5px)`;
                            out.push(
                              <span
                                key={`${s0}`}
                                className="absolute top-0 h-full"
                                data-kind={kind}
                                style={style}
                              />,
                            );
                            s0 = j;
                          }
                        }

                        return out;
                      })}
                    </div>
                  </div>
                );
              })}
            </div>
            {/* time axis */}
            <div
              className="relative mt-1 h-4 text-[11px] text-default-400"
              style={{ marginLeft: Y_AXIS_W, marginRight: RIGHT_INSET }}
            >
              {ticks.map((t) => {
                const pct = ((t - data.from) / (data.to - data.from)) * 100;

                if (pct < 3 || pct > 97) return null;

                return (
                  <span
                    key={t}
                    className="absolute -translate-x-1/2 whitespace-nowrap"
                    style={{ left: `${pct}%` }}
                  >
                    {tickLabel(t, span)}
                  </span>
                );
              })}
            </div>
            {sel && (
              <div
                className="pointer-events-none absolute bottom-6 border-x border-primary/60 bg-primary/15"
                style={{
                  left: sel.a,
                  width: Math.max(1, sel.b - sel.a),
                  top: 16,
                }}
              />
            )}
          </div>
        )}
        {data && (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-default-500">
            <span className="inline-flex items-center gap-1">
              <span
                className="h-2 w-3 rounded-sm"
                style={{ background: STATUS_DOWN }}
              />
              断流（两次连接都失败）
            </span>
            <span className="inline-flex items-center gap-1">
              <span
                className="h-2 w-3 rounded-sm"
                style={{ background: STATUS_LOSS }}
              />
              丢包（首次失败、2 秒后重试成功）
            </span>
            <span className="inline-flex items-center gap-1">
              <span
                className="h-2 w-3 rounded-sm"
                style={{
                  background: `repeating-linear-gradient(45deg, ${nodata} 0 2px, transparent 2px 4px)`,
                }}
              />
              无数据（源节点离线 / 面板未测到，不计入断流）
            </span>
            <span className="inline-flex items-center gap-1">
              <span
                className="h-2 w-3 rounded-sm"
                style={{ background: closedColor }}
              />
              端口未开启（连接被拒绝，不计入断流）
            </span>
            {!isMobile && <span>拖动图表可放大</span>}
            {error && <span className="text-danger">{error}</span>}
          </div>
        )}

        {/* stats */}
        {data && (
          <div
            className="overflow-x-auto rounded-lg border border-divider"
            data-testid="lm-stats"
          >
            <table className="w-full min-w-[760px] text-sm">
              <thead className="bg-default-100/70 text-xs text-default-500 dark:bg-default-50/10">
                <tr>
                  {[
                    "线路",
                    "当前延迟",
                    "平均",
                    "p95",
                    "抖动",
                    "可用率",
                    "丢包次数",
                    "断流次数",
                    "最长断流",
                    "无数据",
                  ].map((h, i) => (
                    <th
                      key={h}
                      className={`whitespace-nowrap px-3 py-2 font-medium ${i === 0 ? "text-left" : "text-right"}`}
                    >
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {lines.map((l, i) => {
                  const s = statsById.get(l.id);
                  const lv = data.live?.[String(l.id)];
                  const current =
                    !view.zoom &&
                    view.range !== "custom" &&
                    lv &&
                    Date.now() / 1000 - lv.ts < 150
                      ? lv
                      : null;

                  return (
                    <tr key={l.id} className="border-t border-divider">
                      <td className="max-w-[240px] px-3 py-2">
                        <div className="flex items-center gap-1.5">
                          <span
                            className="h-2.5 w-2.5 shrink-0 rounded-full"
                            style={{ background: colors[i] }}
                          />
                          <span className="truncate" title={lineTitle(l)}>
                            {l.label || lineTitle(l)}
                          </span>
                          {s && (s.closed ?? 0) > 0 && (
                            <Pill className={CLOSED_PILL} tone="slate">
                              端口未开启 {s.closed} 次
                            </Pill>
                          )}
                        </div>
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-right tabular-nums">
                        {current ? (
                          current.status === 1 || current.status === 2 ? (
                            fmtMs(current.ms)
                          ) : (
                            <Pill
                              className={liveClass(current.status)}
                              title={current.reason}
                              tone={liveTone(current.status)}
                            >
                              {statusText(current.status)}
                            </Pill>
                          )
                        ) : s && s.lastStatus === 3 ? (
                          <Pill tone="red">断流</Pill>
                        ) : s && s.lastStatus === 4 ? (
                          <Pill className={CLOSED_PILL} tone="slate">
                            端口未开启
                          </Pill>
                        ) : (
                          fmtMs(s?.lastMs)
                        )}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-right tabular-nums">
                        {fmtMs(s?.avg)}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-right tabular-nums">
                        {fmtMs(s?.p95)}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-right tabular-nums">
                        {fmtMs(s?.jitter)}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-right tabular-nums">
                        {fmtPct(s?.availability)}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-right tabular-nums">
                        {s && s.loss > 0 ? (
                          <span className="text-amber-600 dark:text-amber-300">
                            {s.loss}
                          </span>
                        ) : (
                          (s?.loss ?? "—")
                        )}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-right tabular-nums">
                        {s && s.downEvents > 0 ? (
                          <span className="font-semibold text-red-600 dark:text-red-400">
                            {s.downEvents}
                          </span>
                        ) : (
                          (s?.downEvents ?? "—")
                        )}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-right tabular-nums">
                        {s && s.longestDown > 0
                          ? fmtDuration(s.longestDown)
                          : "—"}
                      </td>
                      <td className="whitespace-nowrap px-3 py-2 text-right tabular-nums">
                        {fmtPct(s?.noDataPct, 1)}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        {data && (
          <p className="text-xs text-default-400">
            每 {data.slot} 秒从源节点 TCP 连接目标一次（超时 3 秒，失败 2
            秒后重试一次）。抖动 = 相邻两次测量延迟差的平均值；可用率 = （正常 +
            丢包）÷ 已测量次数，无数据不计入；p95 = 95% 的测量低于该值。
            {data.step > 30
              ? ` 当前每个点合并 ${fmtDuration(data.step)}：含任一断流的时段一定标红${data.step > 60 ? "，丢包 ≥ 5% 的时段才标黄（准确次数见提示框和统计表）" : ""}。`
              : ""}
          </p>
        )}

        {/* 断流记录 */}
        {data && (
          <div data-testid="lm-events">
            <div className="mb-1.5 flex items-center gap-2">
              <h4 className="text-sm font-semibold">断流记录</h4>
              <Pill tone={events.length ? "red" : "green"}>
                {events.length ? `${events.length} 次` : "无"}
              </Pill>
              {events.length > 0 && (
                <span className="text-xs text-default-400">
                  点一条放大到该时段
                </span>
              )}
            </div>
            {events.length === 0 ? (
              <p className="text-xs text-default-500">所选时间段内没有断流。</p>
            ) : (
              <ul className="divide-y divide-divider rounded-lg border border-divider">
                {shownEvents.map((ev) => {
                  const i = lineIndex.get(ev.lineId) ?? 0;
                  const l = lines[i];
                  const dur = ev.end - ev.start;
                  const pad = Math.max(300, dur * 2);

                  return (
                    <li key={`${ev.lineId}-${ev.start}`}>
                      <button
                        className="flex w-full flex-wrap items-center gap-x-3 gap-y-0.5 px-3 py-1.5 text-left text-sm hover:bg-default-100 dark:hover:bg-default-50/10"
                        type="button"
                        onClick={() => zoomTo(ev.start - pad, ev.end + pad)}
                      >
                        <span className="whitespace-nowrap tabular-nums">
                          {fmtDateTime(ev.start, true)} –{" "}
                          {fmtClock(ev.end, true)}
                        </span>
                        <span className="flex min-w-0 items-center gap-1.5">
                          <span
                            className="h-2 w-2 shrink-0 rounded-full"
                            style={{ background: colors[i] }}
                          />
                          <span className="truncate">
                            {l ? l.label || lineTitle(l) : `线路 ${ev.lineId}`}
                          </span>
                        </span>
                        <Pill tone="red">{fmtDuration(dur)}</Pill>
                        {ev.ongoing && <Pill tone="amber">进行中</Pill>}
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
            {events.length > 8 && (
              <Button
                className="mt-1.5"
                size="sm"
                variant="flat"
                onPress={() => setShowAllEvents((v) => !v)}
              >
                {showAllEvents ? "收起" : `显示全部 ${events.length} 条`}
              </Button>
            )}
          </div>
        )}
      </CardBody>
    </Card>
  );
});
