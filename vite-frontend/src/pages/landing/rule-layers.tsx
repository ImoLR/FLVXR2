import type { ReactNode } from "react";

import { useState } from "react";
import { ChevronDown, ChevronUp, Lock } from "lucide-react";

import { LandingTone, Pill, actionTone, landingToneClass } from "./pill";

import { RuleSummary, inboundProtocolChip } from "@/api/landing-egress";

// Plan 149: the 路由规则 waterfall — one numbered, always visible header per
// layer (① 内置 … ⑥ 默认出站) with its rule count and meaning, joined by a
// thin connector; compact one-line rule rows with the matchers behind 「详情」.

const dotClass: Record<LandingTone, string> = {
  green: "bg-green-600 text-white dark:bg-green-500 dark:text-green-950",
  slate: "bg-slate-500 text-white dark:bg-slate-400 dark:text-slate-950",
  purple: "bg-purple-600 text-white dark:bg-purple-400 dark:text-purple-950",
  teal: "bg-teal-600 text-white dark:bg-teal-400 dark:text-teal-950",
  pink: "bg-pink-600 text-white dark:bg-pink-400 dark:text-pink-950",
  cyan: "bg-cyan-600 text-white dark:bg-cyan-400 dark:text-cyan-950",
  amber: "bg-amber-500 text-white dark:bg-amber-400 dark:text-amber-950",
  red: "bg-red-600 text-white dark:bg-red-400 dark:text-red-950",
};

/** Layer tones of the waterfall (plan-149 palette, no amber / red). */
export const layerTones = {
  builtin: "slate",
  forced: "purple",
  node: "green",
  server: "teal",
  default: "cyan",
  outbound: "pink",
} as const satisfies Record<string, LandingTone>;

export function LayerStep({
  index,
  tone,
  title,
  count,
  hint,
  last = false,
  aside,
  children,
}: {
  index: number;
  tone: LandingTone;
  title: string;
  /** 「3 条」, 「暂无」 … (undefined = no count, e.g. 默认出站). */
  count?: string;
  hint: string;
  last?: boolean;
  aside?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <li className="relative pl-8" data-rule-layer={index}>
      {!last && (
        <span
          aria-hidden
          className="absolute bottom-0 left-[11px] top-7 w-px bg-default-300 dark:bg-default-200"
        />
      )}
      <span
        aria-hidden
        className={`absolute left-0 top-0.5 flex h-6 w-6 items-center justify-center rounded-full text-xs font-bold ${dotClass[tone]}`}
      >
        {index}
      </span>
      <div className={`${last ? "" : "pb-4"} space-y-2`}>
        <div className="flex min-h-7 flex-wrap items-center gap-x-2 gap-y-1">
          <h3 className="font-semibold">{title}</h3>
          {count !== undefined && <Pill tone={tone}>{count}</Pill>}
          <span className="min-w-0 text-xs text-default-500">{hint}</span>
          {aside && <span className="ml-auto flex shrink-0">{aside}</span>}
        </div>
        {children}
      </div>
    </li>
  );
}

export function ruleSummaryChips(r: RuleSummary) {
  const chips: string[] = [];

  if (r.domains) chips.push(`目标域名 ${r.domains}`);
  if (r.ips) chips.push(`目标 IP ${r.ips}`);
  if (r.ports) chips.push(`目标端口 ${r.ports}`);
  if (r.sourceIps) chips.push(`来源 IP ${r.sourceIps}`);
  if (r.network) chips.push(r.network === "tcp" ? "TCP" : "UDP");
  if (r.protocols && r.protocols.length)
    chips.push(`协议 ${r.protocols.join("/")}`);
  const scope = inboundProtocolChip(r.inboundProtocols);

  if (scope) chips.push(scope);

  return chips;
}

/** One rule list box: rows separated by thin lines, tinted (no white). */
export function RuleRows({ children }: { children: ReactNode }) {
  return (
    <div className="divide-y divide-default-200 overflow-hidden rounded-lg bg-default-100/60 dark:divide-default-100 dark:bg-default-50/60">
      {children}
    </div>
  );
}

export function EmptyLayer({ text }: { text: string }) {
  return (
    <p className="rounded-lg bg-default-100/60 px-3 py-2 text-xs text-default-500 dark:bg-default-50/60">
      {text}
    </p>
  );
}

/**
 * A read-only rule as one compact line: lock + name (one line, full name in
 * the title) + action chip + 「详情」 toggle; the matcher chips open below.
 * defaultOpen: desktop keeps details visible, H5 starts collapsed.
 */
export function CompactRuleRow({
  rule,
  locked,
  defaultOpen,
  note,
}: {
  rule: RuleSummary;
  locked: boolean;
  defaultOpen: boolean;
  note?: string;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const chips = ruleSummaryChips(rule);

  return (
    <div data-compact-rule className="px-3 py-2">
      <div className="grid grid-cols-[14px_minmax(0,1fr)_auto_auto] items-center gap-2">
        <Lock
          aria-label={locked ? "锁定，不能覆盖" : "可被你的规则覆盖"}
          className={locked ? "text-default-500" : "text-default-300"}
          size={14}
        />
        <span className="truncate text-sm font-medium" title={rule.name}>
          {rule.name}
        </span>
        <Pill tone={actionTone(rule.action)}>→ {rule.actionLabel}</Pill>
        {chips.length > 0 ? (
          <button
            aria-expanded={open}
            aria-label={`${open ? "收起" : "展开"}「${rule.name}」的匹配条件`}
            className="inline-flex h-6 items-center gap-0.5 rounded-full px-1.5 text-xs text-default-500 hover:bg-default-200/70 hover:text-default-700"
            type="button"
            onClick={() => setOpen((v) => !v)}
          >
            详情
            {open ? <ChevronUp size={13} /> : <ChevronDown size={13} />}
          </button>
        ) : (
          <span className="w-0" />
        )}
      </div>
      {open && chips.length > 0 && (
        <div className="mt-1.5 flex flex-wrap gap-1 pl-[22px]">
          {chips.map((chip) => (
            <Pill key={chip} tone="slate">
              {chip}
            </Pill>
          ))}
        </div>
      )}
      {note && <p className="mt-1 pl-[22px] text-xs text-warning">{note}</p>}
    </div>
  );
}

/** Small tinted toggle used in layer headers (内置 展开 / 收起). */
export function LayerToggle({
  open,
  onToggle,
  label,
}: {
  open: boolean;
  onToggle: () => void;
  label: [string, string];
}) {
  return (
    <button
      aria-expanded={open}
      className={`inline-flex h-6 items-center gap-0.5 rounded-full px-2 text-xs font-semibold ${landingToneClass.slate}`}
      type="button"
      onClick={onToggle}
    >
      {open ? label[1] : label[0]}
      {open ? <ChevronUp size={13} /> : <ChevronDown size={13} />}
    </button>
  );
}
