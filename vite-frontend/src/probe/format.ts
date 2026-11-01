// Formatting helpers of the probe page (plain functions, no deps).

const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

export function formatBytes(bytes: number): string {
  if (!bytes || bytes < 0) return "0 B";
  let v = bytes;
  let i = 0;

  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }

  return `${v >= 100 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${UNITS[i]}`;
}

export function formatSpeed(bps: number): string {
  return `${formatBytes(bps)}/s`;
}

export function formatDuration(seconds: number): string {
  if (!seconds || seconds < 0) return "0 分钟";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);

  if (d > 0) return h > 0 && d < 10 ? `${d} 天 ${h} 小时` : `${d} 天`;
  if (h > 0) return `${h} 小时`;

  return `${Math.max(1, m)} 分钟`;
}

export function formatAgo(ms: number, now: number): string {
  if (!ms) return "很久以前";

  return formatDuration(Math.max(0, (now - ms) / 1000)) + "前";
}

export function pct(v: number): string {
  return `${v < 10 ? v.toFixed(1) : v.toFixed(0)}%`;
}

/** Usage colour: green < 60, amber < 85, red above. */
export type Tone = "green" | "amber" | "red" | "slate" | "teal" | "purple" | "cyan";

export function usageTone(p: number): Tone {
  if (p >= 85) return "red";
  if (p >= 60) return "amber";

  return "green";
}

/** Latency colour (same thresholds as the rules page, plan 184). */
export function latencyTone(ms: number, ok: boolean): Tone {
  if (!ok) return "slate";
  if (ms > 180) return "red";
  if (ms >= 100) return "amber";

  return "green";
}

export const toneText: Record<Tone, string> = {
  green: "bg-green-500/15 text-green-700 dark:bg-green-400/15 dark:text-green-300",
  amber: "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300",
  red: "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300",
  slate: "bg-slate-500/15 text-slate-600 dark:bg-slate-400/20 dark:text-slate-300",
  teal: "bg-teal-500/15 text-teal-700 dark:bg-teal-400/15 dark:text-teal-300",
  purple: "bg-purple-500/15 text-purple-700 dark:bg-purple-400/20 dark:text-purple-300",
  cyan: "bg-cyan-500/15 text-cyan-700 dark:bg-cyan-400/15 dark:text-cyan-300",
};

export const toneBar: Record<Tone, string> = {
  green: "bg-green-500 dark:bg-green-400",
  amber: "bg-amber-500 dark:bg-amber-400",
  red: "bg-red-500 dark:bg-red-400",
  slate: "bg-slate-400",
  teal: "bg-teal-500 dark:bg-teal-400",
  purple: "bg-purple-500 dark:bg-purple-400",
  cyan: "bg-cyan-500 dark:bg-cyan-400",
};

export const pillBase =
  "inline-flex max-w-full shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 text-xs font-semibold leading-[18px]";
