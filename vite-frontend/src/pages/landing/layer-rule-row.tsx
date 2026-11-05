import type { ReactNode } from "react";

import { Lock } from "lucide-react";

import { StateIcon } from "./egress-builtin-list";
import { LandingTone, Pill, landingToneClass } from "./pill";

import {
  EgressRule,
  RuleSummary,
  inboundProtocolLabel,
} from "@/api/landing-egress";

// Plan 214 r2: the one rule row of the 路由规则 dialog for the layers after
// 内置 (全局·强制 / 我的规则 / 服务器 / 全局·默认). It is an exact clone of the
// fork.115 内置 compact row (BuiltinCompactRows in egress-builtin-list.tsx):
//   lead icon │ name (one line, ellipsis) │ short action chip │ ✓ / ✗
// The row never wraps: the name truncates first, the chip is capped and
// truncates too. The layer's single 「展开详情」 toggle opens the matchers and
// 「命中后」 under every row; nothing else is shown in the collapsed row.

export type RowState = { on: boolean; label: string };

/** One line of the expanded details: label + plain value or value chips. */
export type DetailItem = { label: string; value: string | string[] };

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

// Cap of the action chip: the name gets the room first, a long outbound
// name (「→ 直连-强制ipv4出站」) is cut with an ellipsis (full text as title).
const chipCap = "max-w-[7.5rem] sm:max-w-[12rem]";

export function LayerRuleRow({
  lead,
  name,
  action,
  open,
  details = [],
  note,
  offNote,
  state,
  dim = false,
  controls,
  dataKey,
}: {
  lead: ReactNode;
  name: string;
  /** Action chip: 「封锁」, 「直连」, 「强制 IPv4」 … (shown as 「→ …」). */
  action?: { label: string; tone: LandingTone };
  /** The layer's 「展开详情」 state. */
  open: boolean;
  details?: DetailItem[];
  /** Explanation shown with the details. */
  note?: ReactNode;
  /** Why the rule is not in force (shown with the details). */
  offNote?: string;
  state?: RowState;
  dim?: boolean;
  /** 我的规则: switch + 「更多」 menu (all h-5, the row stays one line). */
  controls?: ReactNode;
  dataKey?: string;
}) {
  return (
    <div className="px-3 py-2" data-layer-rule={dataKey}>
      <div
        data-layer-rule-line
        className={`grid ${controls ? "grid-cols-[14px_minmax(0,1fr)_auto_auto_auto]" : "grid-cols-[14px_minmax(0,1fr)_auto_auto]"} items-center gap-2 ${dim ? "opacity-70" : ""}`}
      >
        {lead}
        <span className="truncate text-sm font-medium" title={name}>
          {name}
        </span>
        {action ? (
          <span className={`flex min-w-0 ${chipCap}`}>
            <Pill title={`→ ${action.label}`} tone={action.tone}>
              → {action.label}
            </Pill>
          </span>
        ) : (
          <span className="w-0" />
        )}
        {controls && (
          <span className="flex items-center gap-1">{controls}</span>
        )}
        {state ? (
          <StateIcon label={state.label} on={state.on} />
        ) : (
          <span className="w-0" />
        )}
      </div>
      {open && (details.length > 0 || note || offNote) && (
        <div data-layer-rule-details className="pl-[22px]">
          <div className={dim ? "opacity-80" : ""}>
            <DetailList items={details} />
            {note && <p className="mt-1 text-xs text-default-500">{note}</p>}
          </div>
          {offNote && <p className="mt-1 text-xs text-danger">{offNote}</p>}
        </div>
      )}
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

export const ruleSummaryDetails = (r: RuleSummary) => detailsOf(r);
export const editableDetails = (r: EgressRule) => detailsOf(fromRule(r));
