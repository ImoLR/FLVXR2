import type { ReactNode } from "react";

import { ChevronDown, ChevronUp } from "lucide-react";

import {
  LayerRuleRow,
  LockLead,
  RuleRows,
  actionWords,
  ruleSummaryDetails,
  ruleSummaryText,
} from "./layer-rule-row";
import { LandingTone, Pill, actionTone, landingToneClass } from "./pill";

import { RuleSummary } from "@/api/landing-egress";

// Plan 149: the 路由规则 waterfall — one numbered, always visible header per
// layer (① 内置 … ⑥ 默认出站) with its rule count and meaning, joined by a
// thin connector; compact one-line rule rows with the matchers behind 「详情」.
// Plan 214: every layer uses the shared LayerRuleRow (layer-rule-row.tsx) and
// one 「展开详情」 toggle in its header instead of a 「详情」 button per rule.

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
          {/* Plan 214: with a header toggle, the hint takes its own line on
              phones so the toggle stays on the title line. */}
          <span
            className={`min-w-0 text-xs text-default-500 ${aside ? "order-last basis-full sm:order-none sm:basis-auto" : ""}`}
          >
            {hint}
          </span>
          {aside && (
            <span className="ml-auto flex shrink-0 flex-wrap items-center justify-end gap-1">
              {aside}
            </span>
          )}
        </div>
        {children}
      </div>
    </li>
  );
}

// Plan 214: the tinted rule box lives with the shared row.
export { RuleRows };

export function EmptyLayer({ text }: { text: string }) {
  return (
    <p className="rounded-lg bg-default-100/60 px-3 py-2 text-xs text-default-500 dark:bg-default-50/60">
      {text}
    </p>
  );
}

/**
 * Plan 214: a read-only rule (全局·强制 / 服务器 / 全局·默认) on the shared
 * layer row: lock + name + action chip + summary, ✓ at the far right; the
 * matchers open with the layer's 「展开详情」 (no per-row 「详情」 button).
 */
export function SummaryRuleRow({
  rule,
  locked,
  open,
  inactive,
}: {
  rule: RuleSummary;
  locked: boolean;
  open: boolean;
  /** Why this rule does not apply here (e.g. 链式代理: only block rules). */
  inactive?: string;
}) {
  const state = inactive
    ? { on: false, label: inactive }
    : {
        on: true,
        label: locked ? "生效中（不能覆盖）" : "生效中（可被你的规则覆盖）",
      };

  return (
    <LayerRuleRow
      action={{ label: rule.actionLabel, tone: actionTone(rule.action) }}
      dataKey={rule.id}
      details={[
        ...ruleSummaryDetails(rule),
        { label: "命中后", value: actionWords(rule.action, rule.actionLabel) },
      ]}
      dim={Boolean(inactive)}
      lead={<LockLead locked={locked} />}
      name={rule.name}
      offNote={inactive}
      open={open}
      state={state}
      summary={ruleSummaryText(rule)}
    />
  );
}

/** Small tinted toggle used in layer headers (展开详情 / 收起). */
export function LayerToggle({
  open,
  onToggle,
  label = ["展开详情", "收起"],
  layer,
}: {
  open: boolean;
  onToggle: () => void;
  label?: [string, string];
  /** Layer title, for the accessible name (「展开详情（全局·强制）」). */
  layer?: string;
}) {
  return (
    <button
      aria-expanded={open}
      aria-label={
        layer ? `${open ? label[1] : label[0]}（${layer}）` : undefined
      }
      className={`inline-flex h-6 items-center gap-0.5 rounded-full px-2 text-xs font-semibold ${landingToneClass.slate}`}
      data-layer-toggle={layer}
      type="button"
      onClick={onToggle}
    >
      {open ? label[1] : label[0]}
      {open ? <ChevronUp size={13} /> : <ChevronDown size={13} />}
    </button>
  );
}
