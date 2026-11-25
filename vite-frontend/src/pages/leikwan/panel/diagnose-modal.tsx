import type { ReactNode } from "react";
import type {
  LeiKwanDiagEndIn,
  LeiKwanDiagItem,
  LeiKwanDiagPath,
  LeiKwanDiagSection,
  LeiKwanDiagSummary,
  LeiKwanRule,
} from "@/api/leikwan-line";
import type { PhoneDiag, PhoneHop } from "@/utils/app-bridge";

import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronRight, Loader2, X } from "lucide-react";
import toast from "react-hot-toast";

import { diagnoseLeiKwanRuleStream } from "@/api/diagnosis-stream";
import { useH5Mode } from "@/hooks/useH5Mode";
import { getForwardDiagnosisQualityDisplay } from "@/pages/forward/diagnosis";
import { Pill } from "@/pages/landing/pill";
import { usePhoneCtx } from "@/pages/leikwan/panel/phone-entry";
import { appCall, appHas } from "@/utils/app-bridge";
import { latencyTone } from "@/utils/latency-tone";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
import { Spinner } from "@/shadcn-bridge/heroui/spinner";

// Plan 272 (approved design 02–07): 「利群规则诊断结果」. Runs only on click
// (no polling): the panel streams start / item / done like the rules page.
// Plan 301: the path from the chosen entry only — [本机 →] 入口机 (手机本地:
// 手机) → 利群专线入口 → 专线出口 → 落地: 本机 → 入口 measured by the App
// (browsers start at the entry), 入口 → 专线入口 measured on the entry (or
// the phone), 专线出口 → 落地 measured on the native landing, the vendor part
// in between = 全程 − measured hops (估算, coloured like every latency tag).
// No 「其他利群入口机」. Desktop = tables, H5 = cards (vertical path strip).

const routeShort = (label?: string) =>
  (label || "")
    .replace(/\s*->\s*/g, " → ")
    .replace(/\s+[\p{Script=Han}·]+$/u, "") ||
  label ||
  "";

type RowState = "diagnosing" | "skipped" | "fail" | "estimate" | "ok";

const rowState = (x: LeiKwanDiagItem): RowState =>
  x.diagnosing
    ? "diagnosing"
    : x.skipped
      ? "skipped"
      : !x.success
        ? "fail"
        : x.estimate
          ? "estimate"
          : "ok";

const statusChip: Record<RowState, { text: string; cls: string }> = {
  ok: {
    text: "成功",
    cls: "bg-success-500/10 text-success-600 dark:text-success-400",
  },
  fail: {
    text: "失败",
    cls: "bg-danger-500/10 text-danger-600 dark:text-danger-400",
  },
  estimate: {
    text: "估算",
    cls: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  },
  skipped: {
    text: "未测",
    cls: "bg-slate-500/15 text-slate-600 dark:text-slate-300",
  },
  diagnosing: {
    text: "诊断中",
    cls: "bg-warning-500/10 text-warning-600 dark:text-warning-400",
  },
};

const qualityCls = {
  success: "bg-success-500/10 text-success-600 dark:text-success-400",
  primary: "bg-primary-500/10 text-primary-600 dark:text-primary-400",
  warning: "bg-warning-500/10 text-warning-600 dark:text-warning-400",
  danger: "bg-danger-500/10 text-danger-600 dark:text-danger-400",
};

const chipBase =
  "inline-flex items-center justify-center gap-1 whitespace-nowrap rounded px-2 py-0.5 text-xs font-medium";

function StatusChip({ state }: { state: RowState }) {
  const c = statusChip[state];

  return (
    <span className={`${chipBase} ${c.cls}`}>
      {state === "diagnosing" && <Loader2 className="animate-spin" size={11} />}
      {c.text}
    </span>
  );
}

function StateIcon({ state, h5 }: { state: RowState; h5?: boolean }) {
  if (state === "diagnosing") return <Spinner size="sm" />;
  const look: Record<Exclude<RowState, "diagnosing">, [string, string]> = {
    ok: ["bg-success", "✓"],
    fail: ["bg-danger", "✗"],
    estimate: ["bg-amber-500", "≈"],
    skipped: ["bg-slate-400 dark:bg-slate-500", "–"],
  };
  const [bg, mark] = look[state];

  return (
    <span
      aria-hidden="true"
      className={`flex shrink-0 items-center justify-center rounded-full text-xs text-white ${bg} ${h5 ? "h-6 w-6" : "h-5 w-5"}`}
    >
      {mark}
    </span>
  );
}

function QualityChip({ item }: { item: LeiKwanDiagItem }) {
  const q = getForwardDiagnosisQualityDisplay(
    item.averageMaxMs ?? item.averageTime,
    item.packetLoss,
  );

  if (!q) return <Dash />;

  return (
    <span className={`${chipBase} ${qualityCls[q.color]}`}>
      {q.text.replace(/^[^\p{Script=Han}]+/u, "")}
    </span>
  );
}

const Dash = () => <span className="text-default-400">-</span>;

const ms = (v?: number) => Math.round(v || 0);

/** Plan 306: 「5–22」 for a range (shared landing), else 「5」. */
const msText = (v?: number, max?: number) =>
  max !== undefined && ms(max) > ms(v) ? `${ms(v)}–${ms(max)}` : `${ms(v)}`;

/** Plan 306: 「含端内 18 ms」 — the part of the 专线 behind the exit IP. */
function EndInPill({ e }: { e?: LeiKwanDiagEndIn }) {
  if (!e) return null;

  return (
    <Pill title={e.title} tone={e.tone}>
      {e.label}
    </Pill>
  );
}

/** The muted line under a row title: failures / skips show their message. */
function subLine(x: LeiKwanDiagItem, state: RowState) {
  if (state === "fail") return x.message || x.sub || "连接失败";
  if (state === "skipped") return x.message || x.sub || "";

  return x.sub || "";
}

function DesktopRow({ item }: { item: LeiKwanDiagItem }) {
  const state = rowState(item);
  const sub = subLine(item, state);
  const ok = state === "ok";

  return (
    <tr
      className={
        state === "fail"
          ? "bg-danger-50 dark:bg-danger-900/30"
          : state === "diagnosing"
            ? "bg-warning-50 dark:bg-warning-900/20"
            : "bg-white dark:bg-gray-800"
      }
      data-lk-diag-row={state}
    >
      <td className="px-3 py-2">
        <div className="flex items-center gap-2">
          <StateIcon state={state} />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 font-medium text-foreground">
              <span className="min-w-0 break-words">{item.title}</span>
              {item.inUse && (
                <Pill title={`利群客户端在用（${item.inUse}）`} tone="teal">
                  在用
                </Pill>
              )}
              <EndInPill e={item.endIn} />
            </div>
            {sub && (
              <div
                className={`break-words text-xs ${state === "fail" ? "text-danger-600 dark:text-danger-400" : "text-default-500"}`}
              >
                {sub}
              </div>
            )}
          </div>
        </div>
      </td>
      <td className="px-3 py-2 text-center">
        <StatusChip state={state} />
      </td>
      <td className="px-3 py-2 text-center">
        {ok || state === "estimate" ? (
          <span className="font-semibold text-primary">
            {state === "estimate" ? "≈" : ""}
            {msText(item.averageTime, item.averageMaxMs)}
          </span>
        ) : (
          <Dash />
        )}
      </td>
      <td className="px-3 py-2 text-center">
        {ok ? (
          <span
            className={`font-semibold ${(item.packetLoss || 0) > 0 ? "text-warning" : "text-success"}`}
          >
            {(item.packetLoss || 0).toFixed(1)}%
          </span>
        ) : (
          <Dash />
        )}
      </td>
      <td className="px-3 py-2 text-center">
        {ok ? <QualityChip item={item} /> : <Dash />}
      </td>
    </tr>
  );
}

function PhoneCard({ item }: { item: LeiKwanDiagItem }) {
  const state = rowState(item);
  const sub = subLine(item, state);
  const ok = state === "ok";
  const est = state === "estimate";

  return (
    <div
      className={`rounded-lg border p-3 ${
        state === "fail"
          ? "border-danger-200 bg-danger-50 dark:border-danger-300/30 dark:bg-danger-900/30"
          : state === "diagnosing"
            ? "border-warning-200 bg-warning-50 dark:border-warning-300/30 dark:bg-warning-900/20"
            : "border-divider bg-white dark:bg-gray-800"
      }`}
      data-lk-diag-row={state}
    >
      <div className="flex items-start gap-2">
        <StateIcon h5 state={state} />
        <div className="min-w-0 flex-1">
          <div className="break-words text-sm font-medium text-foreground">
            {item.title}
            {item.endIn && (
              <span className="ml-1.5 inline-block align-middle">
                <EndInPill e={item.endIn} />
              </span>
            )}
          </div>
          {(sub || item.inUse) && (
            <div
              className={`break-words text-xs ${state === "fail" ? "text-danger-600 dark:text-danger-400" : "text-default-500"}`}
            >
              {sub}
              {item.inUse && (
                <b className="font-semibold text-teal-600 dark:text-teal-400">
                  {sub ? " · " : ""}在用
                </b>
              )}
            </div>
          )}
        </div>
        <StatusChip state={state} />
      </div>
      {(ok || est) && (
        <div className="mt-2 grid grid-cols-3 gap-2 border-t border-divider pt-2 text-center text-xs">
          <div>
            <div className="text-default-500">延迟</div>
            <div className="font-semibold text-primary">
              {est ? "≈" : ""}
              {msText(item.averageTime, item.averageMaxMs)} ms
            </div>
          </div>
          <div>
            <div className="text-default-500">丢包</div>
            <div
              className={`font-semibold ${est ? "text-default-400" : (item.packetLoss || 0) > 0 ? "text-warning" : "text-success"}`}
            >
              {est ? "—" : `${(item.packetLoss || 0).toFixed(1)}%`}
            </div>
          </div>
          <div>
            <div className="text-default-500">质量</div>
            <div>{est ? <Dash /> : <QualityChip item={item} />}</div>
          </div>
        </div>
      )}
    </div>
  );
}

function Section({
  title,
  desc,
  items,
  h5,
  collapsible = false,
}: {
  title: string;
  desc: string;
  items: LeiKwanDiagItem[];
  h5: boolean;
  /** admin 「其他利群入口机」: closed until the header is clicked */
  collapsible?: boolean;
}) {
  const [open, setOpen] = useState(!collapsible);

  if (!items.length) return null;
  const heading = (
    <>
      <h3 className="flex items-center gap-1 text-sm font-semibold text-primary">
        {collapsible && (
          <ChevronRight
            aria-hidden="true"
            className={`shrink-0 transition-transform ${open ? "rotate-90" : ""}`}
            size={15}
          />
        )}
        {title}
        {collapsible && (
          <Pill className="ml-1" tone="slate">
            {items.length}
          </Pill>
        )}
      </h3>
      <span
        className={`text-default-500 ${h5 ? "block text-[11px]" : "text-xs"}`}
      >
        {desc}
      </span>
    </>
  );
  const headerCls = h5
    ? "rounded-lg border border-primary/30 bg-primary/10 px-2 py-1.5 dark:bg-primary/20"
    : `flex flex-wrap items-baseline gap-x-3 gap-y-0.5 bg-primary/10 px-3 py-2 dark:bg-primary/20 ${open ? "border-b border-divider" : ""}`;
  const header = collapsible ? (
    <button
      aria-expanded={open}
      className={`${headerCls} w-full text-left`}
      type="button"
      onClick={() => setOpen(!open)}
    >
      {heading}
    </button>
  ) : (
    <div className={headerCls}>{heading}</div>
  );

  if (h5)
    return (
      <div className="space-y-2" data-lk-diag-section={title}>
        {header}
        {open && items.map((x, i) => <PhoneCard key={i} item={x} />)}
      </div>
    );

  return (
    <div
      className="overflow-hidden rounded-lg border border-divider bg-white dark:bg-gray-800"
      data-lk-diag-section={title}
    >
      {header}
      {open && (
        <table className="w-full text-sm">
          <thead className="bg-default-100 dark:bg-gray-700">
            <tr>
              <th className="px-3 py-2 text-left text-xs font-semibold">
                路径
              </th>
              <th className="w-20 px-3 py-2 text-center text-xs font-semibold">
                状态
              </th>
              <th className="w-24 px-3 py-2 text-center text-xs font-semibold">
                延迟(ms)
              </th>
              <th className="w-24 px-3 py-2 text-center text-xs font-semibold">
                丢包率
              </th>
              <th className="w-20 px-3 py-2 text-center text-xs font-semibold">
                质量
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-divider">
            {items.map((x, i) => (
              <DesktopRow key={i} item={x} />
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function PathNode({
  icon,
  kind,
  name,
  sub,
  mono = false,
  h5,
}: {
  icon: string;
  kind: string;
  name: string;
  sub: string;
  mono?: boolean;
  h5: boolean;
}) {
  return (
    <div
      className={`${h5 ? "" : "min-w-0 flex-1"} rounded-lg border border-divider bg-content1 px-3 py-2`}
    >
      <div className="text-[11px] text-default-500">
        {icon} {kind}
      </div>
      <div className="truncate text-sm font-semibold" title={name}>
        {name || "—"}
      </div>
      <div
        className={`truncate text-xs text-default-500 ${mono ? "font-mono" : ""}`}
        title={sub}
      >
        {sub || "—"}
      </div>
    </div>
  );
}

function HopValue({
  value,
  max,
  estimate,
  pending,
}: {
  value?: number;
  /** Plan 306: upper end of a range (shared landing) */
  max?: number;
  estimate?: boolean;
  pending: boolean;
}) {
  if (value === undefined || value === null)
    return <Pill tone="slate">{pending ? "测试中" : "— ms"}</Pill>;
  const tone = latencyTone(max ?? value);

  // Plan 301: estimates use the same ms colours as every latency tag.
  return estimate ? (
    <Pill title="全程减去实测各段的估算" tone={tone}>
      ≈{msText(value, max)} ms
    </Pill>
  ) : (
    <Pill tone={tone}>{msText(value, max)} ms</Pill>
  );
}

function HopArrow({
  label,
  value,
  max,
  estimate,
  pending,
  h5,
  extra,
}: {
  label: string;
  value?: number;
  max?: number;
  estimate?: boolean;
  pending: boolean;
  h5: boolean;
  /** Plan 306: the 「含端内」 tag under / after the arrow */
  extra?: ReactNode;
}) {
  if (h5)
    return (
      <div className="flex flex-wrap items-center gap-2 pl-4 text-xs text-default-500">
        <span className="text-default-400">│</span>
        {label}
        <HopValue
          estimate={estimate}
          max={max}
          pending={pending}
          value={value}
        />
        {estimate && value !== undefined && <Pill tone="amber">估算</Pill>}
        {extra}
      </div>
    );

  return (
    <div
      className={`flex shrink-0 flex-col items-center ${extra ? "w-24" : "w-20"}`}
    >
      <HopValue estimate={estimate} max={max} pending={pending} value={value} />
      <div className="relative my-1 h-px w-full bg-default-300 dark:bg-default-500">
        <span className="absolute -right-1 -top-[7px] text-xs text-default-400">
          ▶
        </span>
      </div>
      <span className="text-[11px] text-default-500">
        {label}
        {estimate ? "·估算" : ""}
      </span>
      {extra && <div className="mt-0.5">{extra}</div>}
    </div>
  );
}

function PathStrip({
  path,
  summary,
  pending,
  h5,
}: {
  path: LeiKwanDiagPath;
  summary?: LeiKwanDiagSummary;
  pending: boolean;
  h5: boolean;
}) {
  const age = summary?.totalAgeS || 0;
  const local = !!path.local;
  // Plan 301: 专线出口 → 落地 measured → the vendor part is the estimate;
  // otherwise one 专线 → 落地 estimate as before.
  const measured = summary
    ? !!summary.landingMeasured
    : !!path.landingMeasurable;
  let total: ReactNode;

  if (!summary)
    total = pending ? (
      <span>正在读取利群客户端的全程延迟…</span>
    ) : (
      <span>没有拿到全程延迟</span>
    );
  else if (!summary.totalSource || summary.totalMs === undefined)
    total = (
      <>
        <Pill tone="slate">全程 —</Pill>
        <span>利群客户端暂无数据</span>
      </>
    );
  else
    total = (
      <>
        <Pill
          title={`${summary.totalSource === "udp" ? "UDP 探测" : "TCP 最近一次"}${
            summary.totalLoss !== undefined && summary.totalSource === "udp"
              ? ` · 丢包 ${summary.totalLoss.toFixed(1)}%`
              : ""
          }`}
          tone="violet"
        >
          全程 {ms(summary.totalMs)} ms
        </Pill>
        {measured && summary.endToEndMs !== undefined && (
          <Pill
            title={
              summary.endInMs !== undefined
                ? "入口段 + 专线内（含端内）+ 落地机实测的 专线出口 → 落地（ping 出口）"
                : "全程（到专线出口）+ 落地机实测的 专线出口 → 落地"
            }
            tone={latencyTone(summary.endToEndMaxMs ?? summary.endToEndMs)}
          >
            到落地 ≈{msText(summary.endToEndMs, summary.endToEndMaxMs)} ms
          </Pill>
        )}
        <span>
          {measured
            ? `全程 = ${local ? "手机" : "入口机"} → 专线出口，利群客户端实测`
            : `全程 = ${local ? "手机" : "入口机"} → 利群专线，利群客户端实测`}
          {age > 120 ? `（${Math.round(age / 60)} 分钟前）` : ""}
          {measured
            ? "；专线内 = 全程减入口段（估算），到落地再加上落地机实测的一段"
            : "；专线 → 落地是全程减第一跳的估算"}
          {measured && summary.endInMs !== undefined
            ? "；端内（出口 IP 后面利群内部的一段）已经算在专线内，不重复加"
            : ""}
        </span>
      </>
    );

  return (
    <div
      className="rounded-xl border border-divider bg-default-50 p-3 dark:bg-default-100/40"
      data-lk-diag-path=""
    >
      <div
        className={
          h5
            ? "flex flex-col items-stretch gap-1.5"
            : "flex items-center gap-2 overflow-x-auto"
        }
      >
        {path.device && !local && (
          <>
            <PathNode
              h5={h5}
              icon="📱"
              kind="本机"
              name="这台手机"
              sub="App 实测"
            />
            <HopArrow
              h5={h5}
              label="本机 → 入口"
              pending={pending}
              value={summary?.deviceMs}
            />
          </>
        )}
        <PathNode
          mono
          h5={h5}
          icon={local ? "📱" : "🚪"}
          kind={local ? "手机本地" : "入口机"}
          name={local ? "这台手机" : path.entryName}
          sub={path.entryAddress}
        />
        <HopArrow
          h5={h5}
          label={local ? "手机 → 专线入口" : "入口 → 专线入口"}
          pending={pending}
          value={summary?.hop1Ms}
        />
        <PathNode
          h5={h5}
          icon="🔗"
          kind="利群专线入口"
          name={path.lineName}
          sub={routeShort(path.routeLabel)}
        />
        {measured ? (
          <>
            <HopArrow
              estimate
              extra={<EndInPill e={summary?.endIn} />}
              h5={h5}
              label="专线内"
              max={summary?.vendorMaxMs}
              pending={pending}
              value={summary?.vendorMs}
            />
            <PathNode
              h5={h5}
              icon="🔀"
              kind="利群专线出口"
              name="专线出口"
              sub="利群那边的出口"
            />
            <HopArrow
              h5={h5}
              label="出口 → 落地"
              max={summary?.landingMaxMs}
              pending={pending}
              value={summary?.landingMs}
            />
          </>
        ) : (
          <HopArrow
            estimate
            h5={h5}
            label="专线 → 落地"
            pending={pending}
            value={summary?.hop2Ms}
          />
        )}
        <PathNode
          h5={h5}
          icon="🏁"
          kind="落地"
          name={path.landingName}
          sub={path.landingSub}
        />
      </div>
      <div className="mt-2.5 flex flex-wrap items-center gap-2 text-xs text-default-500">
        {total}
      </div>
    </div>
  );
}

function Tiles({ items }: { items: LeiKwanDiagItem[] }) {
  const counted = items.filter((x) => !x.skipped);
  const success = counted.filter((x) => !x.diagnosing && x.success).length;
  const failed = counted.filter((x) => !x.diagnosing && !x.success).length;

  return (
    <div className="grid grid-cols-3 gap-3">
      <div className="rounded-lg border border-divider bg-default-100 p-3 text-center dark:bg-gray-800">
        <div className="text-2xl font-bold text-foreground">
          {counted.length}
        </div>
        <div className="mt-1 text-xs text-default-500">总测试数</div>
      </div>
      <div className="rounded-lg border border-success-200 bg-success-50 p-3 text-center dark:border-success-700 dark:bg-success-900/20">
        <div className="text-2xl font-bold text-success-600 dark:text-success-400">
          {success}
        </div>
        <div className="mt-1 text-xs text-success-600 dark:text-success-400/80">
          成功
        </div>
      </div>
      <div className="rounded-lg border border-danger-200 bg-danger-50 p-3 text-center dark:border-danger-700 dark:bg-danger-900/20">
        <div className="text-2xl font-bold text-danger-600 dark:text-danger-400">
          {failed}
        </div>
        <div className="mt-1 text-xs text-danger-600 dark:text-danger-400/80">
          失败
        </div>
      </div>
    </div>
  );
}

interface DiagState {
  ruleName: string;
  admin: boolean;
  path: LeiKwanDiagPath;
  items: LeiKwanDiagItem[];
  summary?: LeiKwanDiagSummary;
  timedOut: boolean;
}

export function LeiKwanDiagnoseModal({
  rule,
  admin,
  onClose,
}: {
  /** the rule to diagnose (null = closed); a new object re-runs */
  rule: LeiKwanRule | null;
  admin: boolean;
  onClose: () => void;
}) {
  const h5 = useH5Mode();
  // Plan 301 (im.gamer App): the phone measures its own part of the path.
  const phoneCtx = usePhoneCtx();
  const phoneRef = useRef(phoneCtx);

  phoneRef.current = phoneCtx;
  const [state, setState] = useState<DiagState | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const abortRef = useRef<AbortController | null>(null);

  const run = useCallback(async (r: LeiKwanRule) => {
    abortRef.current?.abort();
    const ac = new AbortController();

    abortRef.current = ac;
    setLoading(true);
    setError("");
    let failMsg = "";

    try {
      const extra = await phoneMeasure(phoneRef.current, r);

      if (ac.signal.aborted) return;
      const res = await diagnoseLeiKwanRuleStream(
        r.id,
        {
          onStart: (d) =>
            setState({
              ruleName: d.ruleName || r.name,
              admin: !!d.admin,
              path: d.path,
              items: d.items || [],
              timedOut: false,
            }),
          // index < length replaces a placeholder; a larger index (admin
          // 第一跳: one row per relay ingress, unknown up front) appends.
          onItem: ({ index, result }) =>
            setState((cur) => {
              if (!cur) return cur;
              const items = [...cur.items];

              if (Number.isInteger(index) && index >= 0 && index < items.length)
                items[index] = { ...result, diagnosing: false };
              else items.push({ ...result, diagnosing: false });

              return { ...cur, items };
            }),
          // done.items is the final list (sorted by section): take it whole.
          onDone: (d) =>
            setState((cur) =>
              cur
                ? {
                    ...cur,
                    items: Array.isArray(d.items) ? d.items : cur.items,
                    summary: d.summary,
                    timedOut: !!d.timedOut,
                  }
                : cur,
            ),
          onError: (msg) => {
            failMsg = msg;
            toast.error(msg);
          },
        },
        ac.signal,
        extra,
      );

      if (ac.signal.aborted) return;
      // Rows the stream never answered (cut off / timed out) stop spinning.
      setState((cur) =>
        cur
          ? {
              ...cur,
              timedOut: cur.timedOut || res.timedOut,
              items: cur.items.map((x) =>
                x.diagnosing
                  ? {
                      ...x,
                      diagnosing: false,
                      success: false,
                      message:
                        x.message || (res.timedOut ? "诊断超时" : "没有结果"),
                    }
                  : x,
              ),
            }
          : cur,
      );
      if (failMsg) setError(failMsg);
      if (res.timedOut && !failMsg) toast.error("诊断超时，已显示当前结果");
    } catch {
      if (ac.signal.aborted) return;
      setError("网络错误，请重试");
      toast.error("网络错误，请重试");
    } finally {
      if (abortRef.current === ac) {
        abortRef.current = null;
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    if (!rule) return;
    setState(null);
    void run(rule);
  }, [rule, run]);
  useEffect(() => () => abortRef.current?.abort(), []);

  const close = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    setLoading(false);
    onClose();
  };

  const items = state?.items || [];
  const isAdmin = state ? state.admin : admin;
  const of = (s: LeiKwanDiagSection) => items.filter((x) => x.section === s);
  const local = !!state?.path.local;
  const done = items.filter((x) => !x.diagnosing).length;
  const pct = items.length ? Math.round((done / items.length) * 100) : 0;
  const lineName = state?.path.lineName || rule?.lineName || "";
  const route = routeShort(state?.path.routeLabel || rule?.routeLabel);

  return (
    <Modal
      classNames={{
        base: "!w-[calc(100%-24px)] !mx-auto sm:!w-full rounded-2xl overflow-hidden",
      }}
      isOpen={!!rule}
      placement="center"
      scrollBehavior="inside"
      // Plan 301: up to 5 path nodes side by side on desktop.
      size={h5 ? "lg" : "xl"}
      onClose={close}
    >
      <ModalContent data-leikwan-diagnose="">
        <ModalHeader className="flex flex-col gap-1">
          <div className="flex items-start justify-between gap-2">
            <h2 className="text-xl font-bold">利群转发诊断结果</h2>
            <button
              aria-label="关闭"
              className="-mr-1 shrink-0 rounded p-1 text-default-400 hover:text-default-600"
              type="button"
              onClick={close}
            >
              <X size={18} />
            </button>
          </div>
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className="min-w-[6rem] flex-1 truncate text-sm font-normal text-default-500">
              {state?.ruleName || rule?.name}
            </span>
            {lineName && (
              <Pill className="!shrink" tone="teal">
                {lineName}
              </Pill>
            )}
            {route && (
              <Pill
                className="!shrink"
                title={state?.path.routeLabel}
                tone="teal"
              >
                {route}
              </Pill>
            )}
          </div>
        </ModalHeader>
        <ModalBody className="space-y-4">
          {loading && (
            <div className="space-y-1.5 rounded-lg border border-primary/20 bg-primary/5 px-3 py-2">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 text-sm text-primary">
                  <Spinner size="sm" />
                  <span>
                    正在诊断 {done}/{items.length || "?"}
                  </span>
                </div>
                <span className={`${chipBase} ${qualityCls.primary}`}>
                  流式更新中
                </span>
              </div>
              <div className="h-1 overflow-hidden rounded bg-primary/15">
                <div
                  className="h-1 bg-primary transition-all"
                  style={{ width: `${pct}%` }}
                />
              </div>
            </div>
          )}
          {state?.timedOut && !loading && (
            <p className="rounded-lg bg-amber-500/15 px-3 py-2 text-xs text-amber-700 dark:bg-amber-400/15 dark:text-amber-300">
              诊断超时，以下是已经完成的结果。
            </p>
          )}
          {!state && !loading && (
            <div className="py-10 text-center text-sm text-default-500">
              {error || "暂无诊断数据"}
            </div>
          )}
          {state && (
            <>
              <PathStrip
                h5={h5}
                path={state.path}
                pending={loading}
                summary={state.summary}
              />
              <Tiles items={items} />
              <Section
                desc="这台手机连到入口机（TCP 握手，App 实测）"
                h5={h5}
                items={of("device")}
                title="📱 本机 → 入口"
              />
              <Section
                desc={
                  local
                    ? "在这台手机上测，到利群专线入口"
                    : isAdmin
                      ? "在入口机上测，到利群专线各入口（客户端自动挑最快的用）"
                      : "在你的入口上测"
                }
                h5={h5}
                items={of("hop1")}
                title={
                  local ? "📱 手机 → 利群专线入口" : "🚪 入口 → 利群专线入口"
                }
              />
              <Section
                desc="利群自己的专线：全程（到专线出口）减入口段的估算"
                h5={h5}
                items={of("vendor")}
                title="🔗 利群专线入口 → 专线出口（估算）"
              />
              <Section
                desc="落地机上实测（ping 专线出口；出口 IP 后面利群内部的一段算端内，在专线里）"
                h5={h5}
                items={of("landing")}
                title="🏁 专线出口 → 落地"
              />
              <Section
                desc="落地机上测不到专线出口，用全程减第一跳估算"
                h5={h5}
                items={of("hop2")}
                title="🔗 利群专线 → 落地（估算）"
              />
              <Section
                desc="落地机直连外网（可不看）"
                h5={h5}
                items={of("exit")}
                title="🚀 出口测试"
              />
              <Section
                desc={
                  local
                    ? "手机直接连备线入口，走 FLVX 隧道到同一个落地"
                    : "经入口、走 FLVX 隧道到同一个落地"
                }
                h5={h5}
                items={of("backup")}
                title="🛟 备线（断线时自动走）"
              />
            </>
          )}
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={close}>
            关闭
          </Button>
          {rule && (
            <Button
              color="primary"
              isLoading={loading}
              variant="flat"
              onPress={() => void run(rule)}
            >
              重新诊断
            </Button>
          )}
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}

/**
 * Plan 301: what the im.gamer App measures before the panel runs the rest —
 * 手机本地: 手机 → 专线入口, 全程 and 手机 → 备线入口 (bridge diag); a server
 * entry: 本机 → 入口 (bridge tcpPing). Browsers: nothing (start at the entry).
 */
async function phoneMeasure(
  ctx: ReturnType<typeof usePhoneCtx>,
  r: LeiKwanRule,
): Promise<{ local?: boolean; phone?: unknown } | undefined> {
  if (!ctx) return undefined;
  if (ctx.local) {
    if (!appHas(ctx.info, "diag") || !r.forwardId)
      return {
        local: true,
        phone: { error: "App 太旧：升级到 im.gamer 1.0.10 后在手机上测这一段" },
      };
    try {
      const d = await appCall<PhoneDiag>(
        "diag",
        { forwardId: r.forwardId },
        12000,
      );

      return {
        local: true,
        phone: d.found
          ? { ingress: d.ingress, total: d.total, backup: d.backup }
          : { error: "手机本地没有这条转发（还没连接或商家账号不同）" },
      };
    } catch (e) {
      return {
        local: true,
        phone: { error: (e as Error).message || "App 没有测到" },
      };
    }
  }
  if (!appHas(ctx.info, "tcpPing") || !r.entry?.host || !r.listenPort)
    return undefined;
  try {
    const d = await appCall<PhoneHop>(
      "tcpPing",
      { host: r.entry.host, port: r.listenPort, count: 3, timeoutMs: 1500 },
      10000,
    );

    return { phone: { device: d } };
  } catch {
    return undefined; // not a plan entry / old App: start at the entry
  }
}
