import type { TrafficRole } from "@/api/traffic-stats";

import { useEffect, useRef } from "react";

import { Pill, type LandingTone } from "@/pages/landing/pill";

// Plan 212: helpers shared by the 主页 traffic cards (lazy chunks only).

/** Same units as the 主页 stat cards (已用流量): B / KB / MB / GB, 2 decimals. */
export function formatBytes(value: number): string {
  if (!value || value <= 0) return "0 B";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(2)} KB`;
  if (value < 1024 * 1024 * 1024)
    return `${(value / (1024 * 1024)).toFixed(2)} MB`;

  return `${(value / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

/** A limit as typed by the admin: whole GB without decimals. */
export function formatLimit(value: number): string {
  const gb = value / (1024 * 1024 * 1024);

  if (gb >= 1 && Math.abs(gb - Math.round(gb)) < 0.005) {
    return `${Math.round(gb)} GB`;
  }

  return formatBytes(value);
}

/** "2026-11-05" → "11-05" */
export const shortDay = (day: string) => (day ? day.slice(5) : "");

export type MeterLevel = "normal" | "warn" | "full";

export const meterLevel = (percent: number): MeterLevel =>
  percent >= 100 ? "full" : percent >= 80 ? "warn" : "normal";

// Validated with the dataviz palette checks (light on #fff, dark on #18181b):
// fill carries the state, the track is the same hue, lighter.
const meterFill: Record<MeterLevel, string> = {
  normal: "bg-[#0d9488]",
  warn: "bg-[#f59e0b] dark:bg-[#bf8a00]",
  full: "bg-[#dc2626] dark:bg-[#e83f5b]",
};
const meterTrack: Record<MeterLevel, string> = {
  normal: "bg-teal-500/15 dark:bg-teal-400/15",
  warn: "bg-amber-500/15 dark:bg-amber-400/15",
  full: "bg-red-500/15 dark:bg-red-400/20",
};

/** Slim rounded bar; percent is clamped to 0–100 (a sliver stays visible). */
export function Meter({
  percent,
  level = "normal",
  label,
}: {
  percent: number;
  level?: MeterLevel;
  label: string;
}) {
  const width = Math.max(0, Math.min(100, percent));

  return (
    <div
      aria-label={label}
      aria-valuemax={100}
      aria-valuemin={0}
      aria-valuenow={Math.round(width)}
      className={`h-1.5 w-full overflow-hidden rounded-full ${meterTrack[level]}`}
      role="meter"
    >
      <div
        className={`h-full rounded-full ${meterFill[level]}`}
        style={{ width: width > 0 ? `max(${width}%, 3px)` : "0%" }}
      />
    </div>
  );
}

const roleText: Record<TrafficRole, string> = {
  entry: "入口",
  landing: "落地",
};
const roleTone: Record<TrafficRole, LandingTone> = {
  entry: "teal",
  landing: "purple",
};

export function RolePill({ kind }: { kind: TrafficRole }) {
  return <Pill tone={roleTone[kind]}>{roleText[kind]}</Pill>;
}

/**
 * Runs load() now, every intervalMs while the tab is visible (ticks of a
 * hidden tab are skipped) and once more when the tab becomes visible again.
 */
export function useVisiblePoll(load: () => void, intervalMs: number) {
  const loadRef = useRef(load);

  loadRef.current = load;
  useEffect(() => {
    loadRef.current();
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") loadRef.current();
    }, intervalMs);
    const onVisible = () => {
      if (document.visibilityState === "visible") loadRef.current();
    };

    document.addEventListener("visibilitychange", onVisible);

    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [intervalMs]);
}
