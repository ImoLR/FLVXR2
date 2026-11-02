// Formatting helpers of the probe page (plain functions, no deps).
import { getNodeRenewalCycleLabel } from "@/pages/node/renewal";

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

/** Badge length: 「3 天」「5 小时」「12 分钟」. */
export function formatShort(seconds: number): string {
  if (!seconds || seconds < 0) return "1 分钟";
  if (seconds >= 86400) return `${Math.floor(seconds / 86400)} 天`;
  if (seconds >= 3600) return `${Math.floor(seconds / 3600)} 小时`;

  return `${Math.max(1, Math.floor(seconds / 60))} 分钟`;
}

export function formatAgo(ms: number, now: number): string {
  if (!ms) return "很久以前";

  return formatDuration(Math.max(0, (now - ms) / 1000)) + "前";
}

export function pct(v: number): string {
  return `${v < 10 ? v.toFixed(1) : v.toFixed(0)}%`;
}

/** Usage colour: green < 60, amber < 85, red above. */
export type Tone =
  | "green"
  | "amber"
  | "red"
  | "slate"
  | "teal"
  | "purple"
  | "cyan";

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
  green:
    "bg-green-500/15 text-green-700 dark:bg-green-400/15 dark:text-green-300",
  amber:
    "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300",
  red: "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300",
  slate:
    "bg-slate-500/15 text-slate-600 dark:bg-slate-400/20 dark:text-slate-300",
  teal: "bg-teal-500/15 text-teal-700 dark:bg-teal-400/15 dark:text-teal-300",
  purple:
    "bg-purple-500/15 text-purple-700 dark:bg-purple-400/20 dark:text-purple-300",
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

/**
 * Plan 194 「剩余天数」 tag text. `days` comes from the panel (calendar days in
 * UTC+8; a node with a cycle has already rolled to its next due date, so only
 * no-cycle nodes go negative). The cycle prefix (月付 / 年付 …, same words as
 * the 节点 page) is separate so narrow cards can drop it.
 */
export function expiryParts(
  days: number,
  cycle?: string,
): { prefix: string; core: string } {
  const label = cycle ? getNodeRenewalCycleLabel(cycle) : "";

  return {
    prefix: label && label !== "未设置" ? label : "",
    core:
      days > 1
        ? `剩 ${days} 天`
        : days === 1
          ? "明天到期"
          : days === 0
            ? "今天到期"
            : `已过期 ${-days} 天`,
  };
}

export function expiryText(days: number, cycle?: string): string {
  const { prefix, core } = expiryParts(days, cycle);

  return prefix ? `${prefix} · ${core}` : core;
}

/** Same thresholds as the 节点 page: amber within 7 days, red today / expired. */
export const EXPIRY_WARN_DAYS = 7;

export function expiryTone(days: number): Tone {
  if (days <= 0) return "red";
  if (days <= EXPIRY_WARN_DAYS) return "amber";

  return "slate";
}
