import type { ProbeLatency, ProbeNode } from "./api";

import { memo } from "react";

import {
  formatAgo,
  formatBytes,
  formatShort,
  formatSpeed,
  latencyTone,
  pct,
  pillBase,
  toneBar,
  toneText,
  usageTone,
  type Tone,
} from "./format";

import { DistroIcon, getDistroColor } from "@/components/distro-icon";
import { regionFlag, regionName } from "@/utils/region";

export function Pill({
  tone,
  children,
  title,
  className = "",
}: {
  tone: Tone;
  children: React.ReactNode;
  title?: string;
  className?: string;
}) {
  return (
    <span
      className={`${pillBase} ${toneText[tone]} ${className}`}
      title={title}
    >
      {children}
    </span>
  );
}

/** One metric: label + value on top, bar below (width transition 500 ms). */
export function Meter({
  label,
  value,
  fill,
  text,
  muted,
}: {
  label: string;
  value: number;
  fill: number;
  text: string;
  muted?: boolean;
}) {
  const width = Math.max(0, Math.min(100, fill));
  const tone = muted ? "slate" : usageTone(fill);

  return (
    <div className="min-w-0">
      <div className="flex items-baseline justify-between gap-2 text-xs">
        <span className="text-default-500">{label}</span>
        <span className="font-semibold tabular-nums text-foreground">
          {muted ? "—" : text}
        </span>
      </div>
      <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-default-200/70 dark:bg-default-100/40">
        <div
          aria-valuenow={muted ? 0 : value}
          className={`h-full rounded-full ${toneBar[tone]} transition-[width] duration-500 ease-out`}
          role="meter"
          style={{ width: `${muted ? 0 : width}%` }}
        />
      </div>
    </div>
  );
}

export function NodeTitle({
  node,
  size = "md",
}: {
  node: ProbeNode;
  size?: "md" | "lg";
}) {
  const flag = regionFlag(node.region);
  const os = node.os || "linux";

  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <div
        className={`flex shrink-0 items-center justify-center rounded-xl border border-divider bg-default-100/70 dark:bg-default-50/10 ${size === "lg" ? "h-11 w-11" : "h-9 w-9"}`}
        style={{ color: getDistroColor(os) }}
        title={
          node.os
            ? `${node.os}${node.arch ? ` · ${node.arch}` : ""}`
            : undefined
        }
      >
        <DistroIcon
          className={size === "lg" ? "h-6 w-6" : "h-5 w-5"}
          distro={os}
        />
      </div>
      <div className="min-w-0">
        <div
          className={`flex min-w-0 items-center gap-1.5 font-semibold text-foreground ${size === "lg" ? "text-base" : "text-sm"}`}
        >
          {flag && (
            <span
              aria-label={regionName(node.region)}
              className="shrink-0"
              role="img"
            >
              {flag}
            </span>
          )}
          <span className="truncate">{node.name}</span>
        </div>
        <div className="truncate text-[11px] text-default-500">
          {[node.region ? regionName(node.region) : "", node.os, node.arch]
            .filter(Boolean)
            .join(" · ") || "Linux"}
        </div>
      </div>
    </div>
  );
}

export function StatusPill({ node, now }: { node: ProbeNode; now: number }) {
  if (node.online) {
    return (
      <Pill title="系统已运行时长" tone="green">
        <span className="h-1.5 w-1.5 rounded-full bg-current" />
        在线 {formatShort(node.uptime)}
      </Pill>
    );
  }

  return (
    <Pill
      title={
        node.seen
          ? `最后在线 ${new Date(node.seen).toLocaleString()}`
          : undefined
      }
      tone="red"
    >
      离线
      {node.seen
        ? ` ${formatShort(Math.max(0, (now - node.seen) / 1000))}`
        : ""}
    </Pill>
  );
}

export function LatencyRows({
  rows,
  names,
  limit = 4,
}: {
  rows?: ProbeLatency[];
  names: Map<string, string>;
  limit?: number;
}) {
  if (!rows || rows.length === 0) return null;
  const shown = rows.slice(0, limit);

  return (
    <div className="flex flex-wrap gap-1.5">
      {shown.map((r) => (
        <span
          key={r.to}
          className="inline-flex max-w-full items-center gap-1 text-xs text-default-600"
        >
          <span className="truncate">→ {names.get(r.to) ?? "?"}</span>
          <Pill className="tabular-nums" tone={latencyTone(r.ms, r.ok)}>
            {r.ok ? `${Math.round(r.ms)} ms` : "超时"}
          </Pill>
        </span>
      ))}
      {rows.length > limit && (
        <span className="text-xs text-default-400">
          还有 {rows.length - limit} 条
        </span>
      )}
    </div>
  );
}

function MiniStat({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone?: "teal" | "purple";
}) {
  const color =
    tone === "teal"
      ? "text-teal-700 dark:text-teal-300"
      : tone === "purple"
        ? "text-purple-700 dark:text-purple-300"
        : "text-foreground";

  return (
    <div className="min-w-0 rounded-lg bg-default-100/60 px-2 py-1 dark:bg-default-50/10">
      <div className="text-[11px] text-default-500">{label}</div>
      <div className={`truncate font-semibold ${color}`}>{value}</div>
    </div>
  );
}

const LOAD_FULL = 4; // bar scale: load 4.0 = full (cores are not reported)

interface CardProps {
  node: ProbeNode;
  names: Map<string, string>;
  now: number;
  onOpen: (k: string) => void;
}

/** Server card. Memoized per node object: an unchanged node never re-renders. */
export const ServerCard = memo(function ServerCard({
  node,
  names,
  now,
  onOpen,
}: CardProps) {
  const off = !node.online;

  return (
    <button
      className={`group flex h-full w-full flex-col gap-3 rounded-xl border border-divider bg-content1 p-4 text-left shadow-sm transition-shadow duration-200 hover:shadow-md focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 ${off ? "opacity-60" : ""}`}
      type="button"
      onClick={() => onOpen(node.k)}
    >
      <div className="flex w-full items-start justify-between gap-2">
        <NodeTitle node={node} />
        <StatusPill node={node} now={now} />
      </div>
      <div className="grid w-full grid-cols-2 gap-x-4 gap-y-2.5">
        <Meter
          fill={node.cpu}
          label="CPU"
          muted={off}
          text={pct(node.cpu)}
          value={node.cpu}
        />
        <Meter
          fill={node.mem}
          label="内存"
          muted={off}
          text={pct(node.mem)}
          value={node.mem}
        />
        <Meter
          fill={node.disk}
          label="硬盘"
          muted={off}
          text={pct(node.disk)}
          value={node.disk}
        />
        <Meter
          fill={(node.load1 / LOAD_FULL) * 100}
          label="负载"
          muted={off}
          text={node.load1.toFixed(2)}
          value={node.load1}
        />
      </div>
      <div className="grid w-full grid-cols-3 gap-2 text-xs tabular-nums">
        <MiniStat
          label="下行"
          tone="teal"
          value={off ? "—" : formatSpeed(node.net_in)}
        />
        <MiniStat
          label="上行"
          tone="purple"
          value={off ? "—" : formatSpeed(node.net_out)}
        />
        <MiniStat label="TCP" value={off ? "—" : String(node.tcp)} />
      </div>
      <div className="flex w-full justify-between gap-2 text-xs tabular-nums">
        <span className="shrink-0 text-default-500">
          {off ? "最后在线" : "流量"}
        </span>
        <span className="truncate text-foreground">
          {off
            ? node.seen
              ? formatAgo(node.seen, now)
              : "—"
            : `↓ ${formatBytes(node.traffic_in)} · ↑ ${formatBytes(node.traffic_out)}`}
        </span>
      </div>
      {!off && <LatencyRows names={names} rows={node.latency} />}
    </button>
  );
});

/** Compact table row (same memo rule). */
export const CompactRow = memo(function CompactRow({
  node,
  names,
  now,
  onOpen,
}: CardProps) {
  const off = !node.online;
  const cell = "px-3 py-2 align-middle tabular-nums";
  const first = node.latency?.[0];

  return (
    <tr
      className={`cursor-pointer border-t border-divider hover:bg-default-100/60 dark:hover:bg-default-50/10 ${off ? "opacity-60" : ""}`}
      onClick={() => onOpen(node.k)}
    >
      <td className={`${cell} max-w-[220px]`}>
        <NodeTitle node={node} />
      </td>
      <td className={cell}>
        <StatusPill node={node} now={now} />
      </td>
      <td className={cell}>
        {off ? "—" : <Pill tone={usageTone(node.cpu)}>{pct(node.cpu)}</Pill>}
      </td>
      <td className={cell}>
        {off ? "—" : <Pill tone={usageTone(node.mem)}>{pct(node.mem)}</Pill>}
      </td>
      <td className={cell}>
        {off ? "—" : <Pill tone={usageTone(node.disk)}>{pct(node.disk)}</Pill>}
      </td>
      <td className={cell}>{off ? "—" : node.load1.toFixed(2)}</td>
      <td className={`${cell} whitespace-nowrap text-xs`}>
        {off
          ? "—"
          : `↓ ${formatSpeed(node.net_in)} ↑ ${formatSpeed(node.net_out)}`}
      </td>
      <td className={`${cell} whitespace-nowrap text-xs`}>
        {off
          ? "—"
          : `↓ ${formatBytes(node.traffic_in)} ↑ ${formatBytes(node.traffic_out)}`}
      </td>
      <td className={cell}>{off ? "—" : node.tcp}</td>
      <td className={`${cell} whitespace-nowrap text-xs`}>
        {!off && first ? (
          <span className="inline-flex items-center gap-1">
            → {names.get(first.to) ?? "?"}
            <Pill tone={latencyTone(first.ms, first.ok)}>
              {first.ok ? `${Math.round(first.ms)} ms` : "超时"}
            </Pill>
          </span>
        ) : (
          "—"
        )}
      </td>
    </tr>
  );
});
