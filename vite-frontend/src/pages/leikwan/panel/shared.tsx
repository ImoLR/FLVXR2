import type { ReactNode } from "react";
import type {
  LeiKwanHostValue,
  LeiKwanQuota,
  LeiKwanTrigger,
} from "@/api/leikwan-line";

import { triggerSourceTone } from "@/api/leikwan-line";
import { Pill, type LandingTone } from "@/pages/landing/pill";
import { Switch } from "@/shadcn-bridge/heroui/switch";

// Plan 266: the small shared pieces of the 利群专线 screens. Colours follow
// the tinted palette of the 落地 / 规则 pages (no white or solid-blue chips):
// teal = 利群专线, slate = 管道 / neutral, purple = counts, green = on,
// amber = 测试中 / attention, red = errors.

export function LineChip() {
  return <Pill tone="teal">利群专线</Pill>;
}

export function PipeChip() {
  return (
    <Pill title="plan 094 的管道模式隧道，不在利群汇聚组里" tone="slate">
      管道模式
    </Pill>
  );
}

export function QuotaPill({ quota }: { quota?: LeiKwanQuota }) {
  if (!quota) return null;
  if (quota.error)
    return (
      <Pill title={quota.error} tone="amber">
        名额读取失败
      </Pill>
    );
  const full = quota.limit > 0 && quota.active >= quota.limit;

  return (
    <Pill
      title="名额是整个利群账号共用的；授权时不扣，建规则时才扣"
      tone={full ? "amber" : "purple"}
    >
      利群名额 {quota.active} / {quota.limit}
      {full ? " · 已满" : ""}
    </Pill>
  );
}

export function OnOff({ on, label }: { on: boolean; label?: string }) {
  return (
    <Pill tone={on ? "green" : "slate"}>
      {label ? `${label} ` : ""}
      {on ? "开" : "关"}
    </Pill>
  );
}

/** A label / value row of the read-only status lists. */
export function KV({
  label,
  children,
  labelClassName = "w-28",
}: {
  label: string;
  children: ReactNode;
  /** label column width (H5 rows use a narrower one) */
  labelClassName?: string;
}) {
  return (
    <div className="flex items-start gap-3 py-1 text-sm">
      <span className={`${labelClassName} shrink-0 text-default-500`}>
        {label}
      </span>
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5 break-all">
        {children}
      </div>
    </div>
  );
}

export function SectionTitle({
  title,
  hint,
  right,
}: {
  title: string;
  hint?: ReactNode;
  right?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2">
      <div className="min-w-0">
        <h3 className="text-sm font-semibold">{title}</h3>
        {hint && <p className="mt-0.5 text-xs text-default-500">{hint}</p>}
      </div>
      {right}
    </div>
  );
}

const hv = (v?: number | null) =>
  v === undefined || v === null ? "—" : String(v);

/**
 * 「主机现值」: what each 利群入口 machine runs today for one LeiKwan forward
 * (imported read-only; step 1 never writes these files).
 */
export function HostValuesTable({
  values,
  compact = false,
}: {
  values?: LeiKwanHostValue[];
  compact?: boolean;
}) {
  if (!values || values.length === 0)
    return <span className="text-xs text-default-400">还没有主机快照</span>;

  return (
    <div className="overflow-x-auto rounded-lg bg-default-100/60 dark:bg-default-50/40">
      <table className="w-full min-w-[420px] text-xs">
        <thead className="text-default-500">
          <tr>
            <th className="px-2 py-1 text-left font-medium">入口机</th>
            <th className="px-2 py-1 text-left font-medium">预热 主/备</th>
            <th className="px-2 py-1 text-left font-medium">首包过滤</th>
            <th className="px-2 py-1 text-left font-medium">日志快切</th>
            {!compact && (
              <th className="px-2 py-1 text-left font-medium">切回复查</th>
            )}
          </tr>
        </thead>
        <tbody>
          {values.map((v) => (
            <tr key={v.nodeId} className="border-t border-divider/60">
              <td className="whitespace-nowrap px-2 py-1">{v.nodeName}</td>
              <td className="px-2 py-1 tabular-nums">
                {hv(v.warmMain)} / {hv(v.warmBackup)}
              </td>
              <td className="px-2 py-1">{v.ssFilter || "—"}</td>
              <td className="px-2 py-1">
                {v.logswitch ? (
                  <span title={v.backup}>开 → {v.backup}</span>
                ) : (
                  "—"
                )}
              </td>
              {!compact && (
                <td className="px-2 py-1">{v.returnCheck || "—"}</td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** The global 日志快切 trigger set, read-only, each with its signal source. */
export function TriggerList({ triggers }: { triggers: LeiKwanTrigger[] }) {
  return (
    <ul className="space-y-1.5">
      {triggers.map((t) => (
        <li key={t.key} className="flex flex-wrap items-center gap-1.5 text-xs">
          <Pill tone={triggerSourceTone(t.source) as LandingTone}>
            {t.source}
          </Pill>
          {t.recover && <Pill tone="amber">恢复：{t.recover}</Pill>}
          <span className="font-medium">{t.name}</span>
          <span className="font-mono text-[11px] text-default-400">
            {t.key}
          </span>
          {t.note && <span className="text-default-500">· {t.note}</span>}
        </li>
      ))}
    </ul>
  );
}

export const fmtBytes = (n?: number) => {
  const v = n || 0;

  if (v < 1024) return `${v} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let x = v / 1024;
  let i = 0;

  while (x >= 1024 && i < units.length - 1) {
    x /= 1024;
    i++;
  }

  return `${x.toFixed(x >= 100 ? 0 : 1)} ${units[i]}`;
};

/** Bridge Switch + text label (the bridge Switch renders no children). */
export function LabeledSwitch({
  isSelected,
  isDisabled,
  onValueChange,
  children,
}: {
  isSelected: boolean;
  isDisabled?: boolean;
  onValueChange: (v: boolean) => void;
  children: ReactNode;
}) {
  return (
    <label
      className={`inline-flex cursor-pointer items-center gap-2 text-sm ${isDisabled ? "opacity-50" : ""}`}
    >
      <Switch
        isDisabled={isDisabled}
        isSelected={isSelected}
        size="sm"
        onValueChange={onValueChange}
      />
      <span>{children}</span>
    </label>
  );
}
