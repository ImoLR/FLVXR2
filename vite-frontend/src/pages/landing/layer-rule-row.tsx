import type { ReactNode } from "react";

import { CircleCheck, CircleX, Lock } from "lucide-react";

import { LandingTone, Pill, landingToneClass } from "./pill";

import {
  EgressRule,
  RuleSummary,
  inboundProtocolLabel,
} from "@/api/landing-egress";

// Plan 214: the one rule row of the 路由规则 dialog, used by every layer
// (① 内置 … ⑤ 全局·默认, the editable 我的规则 included). It is the plan-149
// 内置 compact row made general:
//   lead icon │ name · action chip · one-line summary (wrapping) │ ✓ / ✗
// The action chip follows the name inside a wrapping line, so on a 390 px
// phone it moves under the name instead of squeezing it; nothing is drawn
// over text. The layer's single 「展开详情」 toggle opens a label / value list
// under every row (matchers + 「命中后」 in plain words).

export type RowState = { on: boolean; label: string };

/** One line of the expanded details: label + plain value or value chips. */
export type DetailItem = { label: string; value: string | string[] };

// In force = green ✓, not in force = red ✗; the label carries the reason.
export function StateIcon({ on, label }: { on: boolean; label: string }) {
  const Icon = on ? CircleCheck : CircleX;

  return (
    <span
      aria-label={label}
      className={`inline-flex shrink-0 ${on ? "text-success" : "text-danger"}`}
      role="img"
      title={label}
    >
      <Icon aria-hidden size={16} strokeWidth={2.25} />
    </span>
  );
}

/** Left column icon: locked (强制 / 内置) or overridable (服务器 / 默认). */
export function LockLead({ locked }: { locked: boolean }) {
  return (
    <Lock
      aria-label={locked ? "锁定，不能覆盖" : "可被你的规则覆盖"}
      className={locked ? "text-default-500" : "text-default-300"}
      size={14}
    />
  );
}

/** Left column of 我的规则: the rule's position (order matters). */
export function NumberLead({ n }: { n: number }) {
  return (
    <span
      aria-label={`第 ${n} 条`}
      className="w-full text-center text-[11px] font-semibold tabular-nums text-default-400"
    >
      {n}
    </span>
  );
}

const MAX_VALUES = 20;

/**
 * Value chips of a list matcher (capped, with 「等 N 条」). Tinted like the
 * other chips but allowed to wrap, so a long domain is never cut off; ASCII
 * values (domains, CIDRs) in monospace.
 */
function ValueChips({ values }: { values: string[] }) {
  const shown = values.slice(0, MAX_VALUES);
  const chip = `rounded-md px-1.5 text-xs font-semibold leading-[18px] ${landingToneClass.slate}`;

  return (
    <span className="flex flex-wrap gap-1">
      {shown.map((v) => (
        <span
          key={v}
          className={`${chip} break-all ${/^[\x20-\x7e]+$/.test(v) ? "font-mono" : ""}`}
        >
          {v}
        </span>
      ))}
      {values.length > shown.length && (
        <span className={chip}>等 {values.length} 条</span>
      )}
    </span>
  );
}

export function DetailList({ items }: { items: DetailItem[] }) {
  if (!items.length) return null;

  return (
    <dl
      data-rule-details
      className="mt-1.5 grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1 text-xs"
    >
      {items.map((item) => (
        <div key={item.label} className="contents">
          <dt className="whitespace-nowrap text-default-500">{item.label}</dt>
          <dd className="min-w-0 break-words text-default-700">
            {Array.isArray(item.value) ? (
              <ValueChips values={item.value} />
            ) : (
              item.value
            )}
          </dd>
        </div>
      ))}
    </dl>
  );
}

export function LayerRuleRow({
  lead,
  name,
  action,
  summary,
  open,
  details = [],
  note,
  offNote,
  state,
  dim = false,
  controls,
  controlsBelow = false,
  dataKey,
}: {
  lead: ReactNode;
  name: string;
  /** Action chip: 「→ 封锁」, 「→ 直连-强制ipv4出站」 … */
  action?: { label: string; tone: LandingTone };
  /** One-line gray summary of the matchers (collapsed rows only). */
  summary?: string;
  /** The layer's 「展开详情」 state. */
  open: boolean;
  details?: DetailItem[];
  /** Explanation shown with the details. */
  note?: ReactNode;
  /** Why the rule is not in force (shown with the details). */
  offNote?: string;
  state?: RowState;
  dim?: boolean;
  /** 我的规则: switch / move / edit / delete. */
  controls?: ReactNode;
  /** H5: controls on their own line under the content. */
  controlsBelow?: boolean;
  dataKey?: string;
}) {
  const inline = Boolean(controls) && !controlsBelow;
  // First line height = the right-side height, so icon, name and ✓ align.
  const lineH = inline ? "min-h-8" : "min-h-5";
  const boxH = inline ? "h-8" : "h-5";

  return (
    <div
      className="grid grid-cols-[14px_minmax(0,1fr)_auto] items-start gap-x-2 px-3 py-2"
      data-layer-rule={dataKey}
    >
      <span className={`flex ${boxH} items-center`}>{lead}</span>
      <div className="min-w-0">
        <div
          className={`flex ${lineH} flex-wrap items-center gap-x-2 gap-y-1 ${dim ? "opacity-70" : ""}`}
        >
          <span className="min-w-0 break-words text-sm font-medium leading-5">
            {name}
          </span>
          {action && <Pill tone={action.tone}>→ {action.label}</Pill>}
          {!open && summary && (
            <span
              className="min-w-0 max-w-full truncate text-xs text-default-500"
              title={summary}
            >
              {summary}
            </span>
          )}
        </div>
        {open && (
          <div className={dim ? "opacity-80" : ""}>
            <DetailList items={details} />
            {note && <p className="mt-1 text-xs text-default-500">{note}</p>}
          </div>
        )}
        {open && offNote && (
          <p className="mt-1 text-xs text-danger">{offNote}</p>
        )}
        {controls && controlsBelow && (
          <div className="mt-1 flex flex-wrap items-center justify-end gap-1">
            {controls}
          </div>
        )}
      </div>
      <span className={`flex ${boxH} items-center gap-1`}>
        {inline && controls}
        {state && <StateIcon label={state.label} on={state.on} />}
      </span>
    </div>
  );
}

/** One rule list box: rows separated by thin lines, tinted (no white). */
export function RuleRows({ children }: { children: ReactNode }) {
  return (
    <div className="divide-y divide-default-200 overflow-hidden rounded-lg bg-default-100/60 dark:divide-default-100 dark:bg-default-50/60">
      {children}
    </div>
  );
}

// ---- Matchers in words ----

type Matchers = {
  domains: number | string[];
  ips: number | string[];
  ports: string;
  sourceIps: number | string[];
  sourcePorts?: string;
  network: string;
  protocols: string[] | null;
  inboundProtocols?: string[] | null;
  landingNodes?: number;
};

const count = (v: number | string[]) => (Array.isArray(v) ? v.length : v);
const networkText = (n: string) =>
  n === "tcp" ? "TCP" : n === "udp" ? "UDP" : "";
const inboundText = (keys?: string[] | null) =>
  (keys || []).map(inboundProtocolLabel).join(" / ");

const fromRule = (r: EgressRule): Matchers => ({
  domains: r.domains,
  ips: r.ips,
  ports: r.ports,
  sourceIps: r.sourceIps,
  sourcePorts: r.sourcePorts,
  network: r.network,
  protocols: r.protocols,
  inboundProtocols: r.inboundProtocols,
  landingNodes: r.landingNodeIds.length,
});

/** 「目标域名 2 条 · TCP」 — the collapsed one-line summary. */
function summaryOf(m: Matchers) {
  const parts: string[] = [];

  if (count(m.domains)) parts.push(`目标域名 ${count(m.domains)} 条`);
  if (count(m.ips)) parts.push(`目标 IP ${count(m.ips)} 条`);
  if (m.ports) parts.push(`目标端口 ${m.ports}`);
  if (count(m.sourceIps)) parts.push(`来源 IP ${count(m.sourceIps)} 条`);
  if (m.sourcePorts) parts.push(`来源端口 ${m.sourcePorts}`);
  if (networkText(m.network)) parts.push(networkText(m.network));
  if (m.protocols && m.protocols.length)
    parts.push(`协议 ${m.protocols.join("/")}`);
  if (m.landingNodes) parts.push(`适用节点 ${m.landingNodes} 个`);
  if (m.inboundProtocols && m.inboundProtocols.length)
    parts.push(`仅 ${inboundText(m.inboundProtocols)} 节点`);

  return parts.join(" · ");
}

/** Label / value lines of the expanded details. */
function detailsOf(m: Matchers): DetailItem[] {
  const items: DetailItem[] = [];
  const list = (label: string, v: number | string[]) => {
    if (Array.isArray(v)) {
      if (v.length) items.push({ label, value: v });
    } else if (v) items.push({ label, value: `${v} 条` });
  };

  list("目标域名", m.domains);
  list("目标 IP", m.ips);
  if (m.ports) items.push({ label: "目标端口", value: m.ports });
  list("来源 IP", m.sourceIps);
  if (m.sourcePorts) items.push({ label: "来源端口", value: m.sourcePorts });
  if (networkText(m.network))
    items.push({ label: "网络", value: networkText(m.network) });
  if (m.protocols && m.protocols.length)
    items.push({ label: "协议", value: m.protocols.join("、") });
  if (m.inboundProtocols && m.inboundProtocols.length)
    items.push({
      label: "节点协议",
      value: `只对 ${inboundText(m.inboundProtocols)} 节点生效`,
    });
  if (m.landingNodes)
    items.push({ label: "适用节点", value: `${m.landingNodes} 个落地节点` });

  return items;
}

/** 「命中后」 in plain words. */
export function actionWords(action: string, label: string) {
  if (action === "block") return "封锁，连接直接断开";
  if (action === "ask") return "插入时选择出站";
  if (!action || action === "direct") return "直连（从落地服务器直接访问）";

  return `走出站「${label}」`;
}

export const ruleSummaryText = (r: RuleSummary) => summaryOf(r);
export const ruleSummaryDetails = (r: RuleSummary) => detailsOf(r);
export const editableSummaryText = (r: EgressRule) => summaryOf(fromRule(r));
export const editableDetails = (r: EgressRule) => detailsOf(fromRule(r));
