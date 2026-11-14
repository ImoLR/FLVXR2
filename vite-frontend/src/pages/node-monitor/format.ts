import type { LineMonitorLine, LineMonitorRange } from "@/api/line-monitor";

// Plan 255 「线路监测」 helpers.

// Line colours: 8 hues in a fixed order (validated light + dark: adjacent CVD
// ΔE ≥ 12, normal-vision ΔE ≥ 19.7). Red / yellow / grey are left out on
// purpose: they mean 断流 / 丢包 / 无数据 on this page.
const PALETTE_LIGHT = [
  "#2a78d6", // blue
  "#1baf7a", // aqua
  "#4a3aa7", // violet
  "#e87ba4", // magenta
  "#008300", // green
  "#9333ea", // purple
  "#0891b2", // cyan
  "#eb6834", // orange
];
const PALETTE_DARK = [
  "#3987e5",
  "#199e70",
  "#9085e9",
  "#d55181",
  "#008300",
  "#a855f7",
  "#0e9ab8",
  "#d95926",
];

export const lineColor = (index: number, dark: boolean) => {
  const p = dark ? PALETTE_DARK : PALETTE_LIGHT;

  return p[((index % p.length) + p.length) % p.length];
};

// Status colours (reserved): 断流 red, 丢包 amber, 无数据 grey.
export const STATUS_DOWN = "#e5484d";
export const STATUS_LOSS = "#f5a524";
export const STATUS_NODATA_LIGHT = "#a1a1aa";
export const STATUS_NODATA_DARK = "#71717a";

export const RANGE_OPTIONS: {
  key: Exclude<LineMonitorRange, "custom">;
  label: string;
  seconds: number;
}[] = [
  { key: "1h", label: "1小时", seconds: 3600 },
  { key: "6h", label: "6小时", seconds: 6 * 3600 },
  { key: "12h", label: "12小时", seconds: 12 * 3600 },
  { key: "1d", label: "1天", seconds: 86400 },
  { key: "30d", label: "30天", seconds: 30 * 86400 },
];

/** Presets that refresh every 30 s (short ranges only). */
export const LIVE_RANGES = new Set<LineMonitorRange>(["1h", "6h", "12h"]);

const pad = (n: number) => String(n).padStart(2, "0");

/** Local time (the admin's browser = 台湾时间 for the user). */
export const fmtClock = (ts: number, seconds = false) => {
  const d = new Date(ts * 1000);

  return `${pad(d.getHours())}:${pad(d.getMinutes())}${seconds ? `:${pad(d.getSeconds())}` : ""}`;
};

export const fmtDate = (ts: number) => {
  const d = new Date(ts * 1000);

  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
};

export const fmtDateTime = (ts: number, seconds = false) =>
  `${fmtDate(ts)} ${fmtClock(ts, seconds)}`;

/** Value for <input type="datetime-local">. */
export const toLocalInput = (ts: number) => {
  const d = new Date(ts * 1000);

  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
};

export const fromLocalInput = (v: string) => {
  const t = new Date(v).getTime();

  return Number.isFinite(t) ? Math.floor(t / 1000) : 0;
};

export const fmtDuration = (sec: number) => {
  if (!Number.isFinite(sec) || sec <= 0) return "0秒";
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = Math.floor(sec % 60);

  if (d > 0) return `${d}天${h ? `${h}小时` : ""}`;
  if (h > 0) return `${h}小时${m ? `${m}分` : ""}`;
  if (m > 0) return `${m}分${s ? `${s}秒` : ""}`;

  return `${s}秒`;
};

export const fmtMs = (v: number | null | undefined) =>
  v == null || !Number.isFinite(v)
    ? "—"
    : `${v >= 100 ? Math.round(v) : v.toFixed(1)} ms`;

export const fmtPct = (v: number | null | undefined, digits = 2) =>
  v == null || !Number.isFinite(v)
    ? "—"
    : `${v >= 99.995 && v < 100 ? "99.99" : v.toFixed(v === 100 || v === 0 ? 0 : digits)}%`;

/** 「AWS HK → Fachost seednet 黑五」 (label wins when set). */
export const lineTitle = (l: LineMonitorLine) => {
  const target =
    l.targetNodeId > 0
      ? l.targetName || `节点 ${l.targetNodeId}`
      : l.effectiveHost || l.host;

  return `${l.sourceName || `节点 ${l.sourceNodeId}`} → ${target}`;
};

export const lineSubtitle = (l: LineMonitorLine) =>
  `${l.effectiveHost || "—"}:${l.port}`;

// Time ticks at whole local minutes / hours / days.
const TICK_STEPS = [
  60,
  300,
  600,
  900,
  1800,
  3600,
  7200,
  10800,
  21600,
  43200,
  86400,
  2 * 86400,
  5 * 86400,
  7 * 86400,
];

export function timeTicks(
  from: number,
  to: number,
  maxTicks: number,
): number[] {
  const span = Math.max(1, to - from);
  const step =
    TICK_STEPS.find((s) => span / s <= maxTicks) ??
    TICK_STEPS[TICK_STEPS.length - 1];
  const tz = new Date(from * 1000).getTimezoneOffset() * 60; // seconds, local = utc - tz
  const first = Math.ceil((from - tz) / step) * step + tz;
  const out: number[] = [];

  for (let t = first; t <= to; t += step) out.push(t);

  return out;
}

export const tickLabel = (t: number, span: number) =>
  span > 86400 * 1.5
    ? new Date(t * 1000).getHours() === 0 &&
      new Date(t * 1000).getMinutes() === 0
      ? fmtDate(t)
      : fmtDateTime(t)
    : fmtClock(t);

// ── localStorage: last range per chart (plan 255) ──
export interface StoredRange {
  range: LineMonitorRange;
  from?: number;
  to?: number;
}

const rangeKey = (chartId: number) => `flvx.lineMonitor.range.${chartId}`;

export function loadRange(chartId: number): StoredRange {
  try {
    const raw = localStorage.getItem(rangeKey(chartId));

    if (!raw) return { range: "1h" };
    const v = JSON.parse(raw) as StoredRange;

    if (v.range === "custom") {
      const oldest = Date.now() / 1000 - 30 * 86400;

      if (!v.from || !v.to || v.to <= v.from || v.to < oldest)
        return { range: "1h" };

      return v;
    }
    if (RANGE_OPTIONS.some((o) => o.key === v.range)) return { range: v.range };
  } catch {
    // ignore (private mode / bad JSON)
  }

  return { range: "1h" };
}

export function saveRange(chartId: number, v: StoredRange) {
  try {
    localStorage.setItem(rangeKey(chartId), JSON.stringify(v));
  } catch {
    // ignore
  }
}
