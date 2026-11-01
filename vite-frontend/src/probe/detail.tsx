// Plan 187: probe detail (lazy chunk with recharts). History is fetched only
// while this is open; all chart animation is off; tooltips open on tap/click
// only (plan 156).
import type { ProbeNode, ProbeSeries } from "./api";

import {
  memo,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { fetchSeries } from "./api";
import { LatencyRows, Pill } from "./card";
import {
  formatBytes,
  formatDuration,
  formatSpeed,
  latencyTone,
  pct,
} from "./format";
import { Sparkline, seriesColors } from "./sparkline";

import { useTapTooltip } from "@/hooks/useTapTooltip";
import { regionName } from "@/utils/region";

type Range = "1h" | "6h" | "24h";

interface Row {
  t: number;
  a: number;
  b?: number;
}

function timeLabel(t: number, range: Range) {
  const d = new Date(t);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");

  void range;

  return `${hh}:${mm}`;
}

const ChartCard = memo(function ChartCard({
  title,
  rows,
  range,
  color,
  colorB,
  nameA,
  nameB,
  fmt,
  domain,
  tick,
}: {
  title: string;
  rows: Row[];
  range: Range;
  color: string;
  colorB?: string;
  nameA: string;
  nameB?: string;
  fmt: (v: number) => string;
  domain?: [number, number];
  tick?: (v: number) => string;
}) {
  const tap = useTapTooltip();
  const two = nameB !== undefined && colorB !== undefined;

  return (
    <div className="rounded-xl border border-divider p-3">
      <div className="mb-1 flex items-center justify-between gap-2 text-xs">
        <span className="font-semibold text-foreground">{title}</span>
        {two && (
          <span className="flex items-center gap-3 text-default-500">
            <span className="flex items-center gap-1">
              <span
                className="h-0.5 w-3 rounded"
                style={{ background: color }}
              />
              {nameA}
            </span>
            <span className="flex items-center gap-1">
              <span
                className="h-0.5 w-3 rounded"
                style={{ background: colorB }}
              />
              {nameB}
            </span>
          </span>
        )}
      </div>
      <div ref={tap.containerRef} className="h-40">
        <ResponsiveContainer height="100%" width="100%">
          <LineChart
            data={rows}
            margin={{ top: 6, right: 8, bottom: 0, left: 0 }}
            onClick={tap.onChartClick}
          >
            <CartesianGrid
              stroke="currentColor"
              strokeOpacity={0.08}
              vertical={false}
            />
            <XAxis
              axisLine={false}
              dataKey="t"
              domain={["dataMin", "dataMax"]}
              minTickGap={40}
              scale="time"
              stroke="currentColor"
              tick={{ fontSize: 10, fill: "currentColor", opacity: 0.55 }}
              tickFormatter={(t: number) => timeLabel(t, range)}
              tickLine={false}
              type="number"
            />
            <YAxis
              axisLine={false}
              domain={domain ?? [0, "auto"]}
              stroke="currentColor"
              tick={{ fontSize: 10, fill: "currentColor", opacity: 0.55 }}
              tickFormatter={tick ?? fmt}
              tickLine={false}
              width={58}
            />
            <Tooltip
              {...tap.tooltipProps}
              contentStyle={{ fontSize: 12, borderRadius: 8 }}
              formatter={(v: number, key: string) => [
                fmt(v),
                key === "a" ? nameA : (nameB ?? ""),
              ]}
              isAnimationActive={false}
              labelFormatter={(t: number) => new Date(t).toLocaleString()}
            />
            <Line
              activeDot={tap.activeDot()}
              dataKey="a"
              dot={false}
              isAnimationActive={false}
              stroke={color}
              strokeWidth={2}
              type="monotone"
            />
            {two && (
              <Line
                activeDot={tap.activeDot()}
                dataKey="b"
                dot={false}
                isAnimationActive={false}
                stroke={colorB}
                strokeWidth={2}
                type="monotone"
              />
            )}
          </LineChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
});

function Spec({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 justify-between gap-2 border-b border-divider py-1.5 text-xs last:border-b-0">
      <span className="text-default-500">{label}</span>
      <span className="truncate font-medium tabular-nums text-foreground">
        {value}
      </span>
    </div>
  );
}

export default function Detail({
  node,
  names,
  dark,
}: {
  node: ProbeNode;
  names: Map<string, string>;
  dark: boolean;
}) {
  const [range, setRange] = useState<Range>("1h");
  const [series, setSeries] = useState<ProbeSeries | null>(null);
  const boxRef = useRef<HTMLDivElement | null>(null);
  const [points, setPoints] = useState(360);
  const colors = seriesColors(dark);

  useLayoutEffect(() => {
    const w = boxRef.current?.clientWidth ?? 720;

    setPoints(Math.max(60, Math.min(720, Math.round(w / 2))));
  }, []);

  useEffect(() => {
    let alive = true;
    const load = () => {
      if (document.hidden) return;
      void fetchSeries(node.k, range, points).then((s) => {
        if (alive && s) setSeries(s);
      });
    };

    load();
    const id = setInterval(load, 60_000);

    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [node.k, range, points]);

  // While a new range loads, the previous range of the SAME node stays (dimmed);
  // another node's data is never shown.
  const exact =
    series && series.k === node.k && series.range === range ? series : null;
  const current = exact ?? (series && series.k === node.k ? series : null);
  const stale = !exact && !!current;
  const rows = useMemo(() => {
    const pts = current?.points ?? [];

    return {
      usage: pts.map((p) => ({ t: p[0], a: p[1], b: p[2] })),
      net: pts.map((p) => ({ t: p[0], a: p[3], b: p[4] })),
      tcp: pts.map((p) => ({ t: p[0], a: p[5] })),
    };
  }, [current]);
  const lat = current?.latency ?? [];

  return (
    <div ref={boxRef} className="flex flex-col gap-4">
      <div className="grid grid-cols-1 gap-x-6 sm:grid-cols-3">
        <div>
          <Spec
            label="系统"
            value={[node.os || "linux", node.arch].filter(Boolean).join(" · ")}
          />
          <Spec
            label="地区"
            value={node.region ? regionName(node.region) : "未设置"}
          />
          <Spec
            label="运行"
            value={node.online ? formatDuration(node.uptime) : "—"}
          />
        </div>
        <div>
          <Spec label="CPU" value={node.online ? pct(node.cpu) : "—"} />
          <Spec label="内存" value={node.online ? pct(node.mem) : "—"} />
          <Spec label="硬盘" value={node.online ? pct(node.disk) : "—"} />
        </div>
        <div>
          <Spec
            label="网速"
            value={
              node.online
                ? `↓ ${formatSpeed(node.net_in)} ↑ ${formatSpeed(node.net_out)}`
                : "—"
            }
          />
          <Spec
            label="累计"
            value={
              node.online
                ? `↓ ${formatBytes(node.traffic_in)} ↑ ${formatBytes(node.traffic_out)}`
                : "—"
            }
          />
          <Spec label="TCP 连接" value={node.online ? String(node.tcp) : "—"} />
        </div>
      </div>

      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-semibold text-foreground">历史</span>
        <div
          className="flex items-center gap-1 rounded-full bg-slate-500/10 p-0.5 dark:bg-slate-400/10"
          role="group"
        >
          {(["1h", "6h", "24h"] as Range[]).map((r) => (
            <button
              key={r}
              aria-pressed={range === r}
              className={`rounded-full px-3 py-1 text-xs font-semibold ${range === r ? "bg-teal-500/20 text-teal-700 dark:bg-teal-400/20 dark:text-teal-200" : "text-default-500 hover:text-foreground"}`}
              type="button"
              onClick={() => setRange(r)}
            >
              {r === "1h" ? "1 小时" : r === "6h" ? "6 小时" : "24 小时"}
            </button>
          ))}
        </div>
      </div>

      <div
        className={`grid grid-cols-1 gap-3 text-default-500 transition-opacity duration-300 md:grid-cols-2 ${stale ? "opacity-60" : ""}`}
      >
        <ChartCard
          color={colors.a}
          colorB={colors.b}
          domain={[0, 100]}
          fmt={(v) => `${Math.round(v)}%`}
          nameA="CPU"
          nameB="内存"
          range={range}
          rows={rows.usage}
          title="CPU / 内存"
        />
        <ChartCard
          color={colors.a}
          colorB={colors.b}
          fmt={(v) => formatSpeed(v)}
          nameA="↓ 下行"
          nameB="↑ 上行"
          range={range}
          rows={rows.net}
          tick={(v) => formatBytes(v).replace(" ", "")}
          title="网速"
        />
        <ChartCard
          color={colors.a}
          fmt={(v) => String(Math.round(v))}
          nameA="TCP"
          range={range}
          rows={rows.tcp}
          title="TCP 连接数"
        />
        {lat.length > 0 ? (
          <div className="rounded-xl border border-divider p-3">
            <div className="mb-2 flex items-center justify-between text-xs">
              <span className="font-semibold text-foreground">
                延迟（近 1 小时，每分钟一次）
              </span>
            </div>
            <div className="flex flex-col gap-2">
              {lat.map((l) => {
                const vals = l.points.map((p) => (p[1] >= 0 ? p[1] : null));
                const last = l.points[l.points.length - 1];
                const ok = last ? last[1] >= 0 : false;
                const okVals = vals.filter((v): v is number => v != null);
                const avg = okVals.length
                  ? okVals.reduce((s, v) => s + v, 0) / okVals.length
                  : 0;

                return (
                  <div
                    key={l.to}
                    className="grid grid-cols-[minmax(0,1fr)_96px_auto] items-center gap-2 text-xs"
                  >
                    <span className="truncate text-default-600">
                      → {names.get(l.to) ?? "?"}
                    </span>
                    <Sparkline
                      color={colors.a}
                      height={20}
                      label={`到 ${names.get(l.to) ?? ""} 的延迟`}
                      values={vals}
                      width={96}
                    />
                    <span className="flex items-center gap-1 tabular-nums">
                      <Pill tone={latencyTone(last?.[1] ?? 0, ok)}>
                        {ok ? `${Math.round(last[1])} ms` : "超时"}
                      </Pill>
                      <span className="text-default-400">
                        均 {Math.round(avg)}
                      </span>
                    </span>
                  </div>
                );
              })}
            </div>
          </div>
        ) : (
          node.latency &&
          node.latency.length > 0 && (
            <div className="rounded-xl border border-divider p-3">
              <div className="mb-2 text-xs font-semibold text-foreground">
                延迟
              </div>
              <LatencyRows limit={12} names={names} rows={node.latency} />
            </div>
          )
        )}
      </div>
    </div>
  );
}
