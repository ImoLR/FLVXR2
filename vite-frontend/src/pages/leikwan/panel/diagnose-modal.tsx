import type { ReactNode } from "react";
import type {
  LeiKwanDiagItem,
  LeiKwanDiagPath,
  LeiKwanDiagSection,
  LeiKwanDiagSummary,
  LeiKwanRule,
} from "@/api/leikwan-line";

import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronRight, Loader2, X } from "lucide-react";
import toast from "react-hot-toast";

import { diagnoseLeiKwanRuleStream } from "@/api/diagnosis-stream";
import { useH5Mode } from "@/hooks/useH5Mode";
import { getForwardDiagnosisQualityDisplay } from "@/pages/forward/diagnosis";
import { Pill } from "@/pages/landing/pill";
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
// Path strip 入口机 → 利群专线 → 落地 with the 第一跳 / 第二跳 values and the
// 全程 the 利群 client measured; tiles; sections 第一跳 / 第二跳 / 出口测试 /
// 备线 and, for admins, the collapsed 「其他利群入口机」. Desktop = tables,
// H5 = cards (vertical path strip).

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
    item.averageTime,
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
            {ms(item.averageTime)}
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
              {ms(item.averageTime)} ms
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
  estimate,
  pending,
}: {
  value?: number;
  estimate?: boolean;
  pending: boolean;
}) {
  if (value === undefined || value === null)
    return <Pill tone="slate">{pending ? "测试中" : "— ms"}</Pill>;

  return estimate ? (
    <Pill title="全程减第一跳的估算" tone="slate">
      ≈{ms(value)} ms
    </Pill>
  ) : (
    <Pill tone="green">{ms(value)} ms</Pill>
  );
}

function HopArrow({
  label,
  value,
  estimate,
  pending,
  h5,
}: {
  label: string;
  value?: number;
  estimate?: boolean;
  pending: boolean;
  h5: boolean;
}) {
  if (h5)
    return (
      <div className="flex flex-wrap items-center gap-2 pl-4 text-xs text-default-500">
        <span className="text-default-400">│</span>
        {label}
        <HopValue estimate={estimate} pending={pending} value={value} />
        {estimate && value !== undefined && <Pill tone="amber">估算</Pill>}
      </div>
    );

  return (
    <div className="flex w-24 shrink-0 flex-col items-center">
      <HopValue estimate={estimate} pending={pending} value={value} />
      <div className="relative my-1 h-px w-full bg-default-300 dark:bg-default-500">
        <span className="absolute -right-1 -top-[7px] text-xs text-default-400">
          ▶
        </span>
      </div>
      <span className="text-[11px] text-default-500">
        {label}
        {estimate ? "·估算" : ""}
      </span>
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
  const est = !!summary?.hop2Estimate;
  const age = summary?.totalAgeS || 0;
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
        <span>
          = 入口机 → 落地，利群客户端实测
          {age > 120 ? `（${Math.round(age / 60)} 分钟前）` : ""}
          {est ? "；第二跳是全程减第一跳的估算" : ""}
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
          h5 ? "flex flex-col items-stretch gap-1.5" : "flex items-center gap-2"
        }
      >
        <PathNode
          mono
          h5={h5}
          icon="🚪"
          kind="入口机"
          name={path.entryName}
          sub={path.entryAddress}
        />
        <HopArrow
          h5={h5}
          label="第一跳"
          pending={pending}
          value={summary?.hop1Ms}
        />
        <PathNode
          h5={h5}
          icon="🔗"
          kind="利群专线"
          name={path.lineName}
          sub={routeShort(path.routeLabel)}
        />
        <HopArrow
          estimate={est}
          h5={h5}
          label="第二跳"
          pending={pending}
          value={summary?.hop2Ms}
        />
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
  const hop2 = of("hop2");
  const hop2Est =
    !!state?.summary?.hop2Estimate || hop2.some((x) => x.estimate);
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
      size="lg"
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
                desc={
                  isAdmin
                    ? "在入口机上测，到利群专线各入口（客户端自动挑最快的用）"
                    : "在你的主入口上测"
                }
                h5={h5}
                items={of("hop1")}
                title="🚪 第一跳 · 入口机 → 利群专线"
              />
              <Section
                desc={
                  hop2Est
                    ? "落地机上看不到利群出口，用全程减第一跳估算"
                    : "利群专线出口到你的落地（落地机上实测）"
                }
                h5={h5}
                items={hop2}
                title="🔗 第二跳 · 利群专线 → 落地"
              />
              <Section
                desc="落地机直连外网（可不看）"
                h5={h5}
                items={of("exit")}
                title="🚀 出口测试"
              />
              <Section
                desc="经主入口、走 FLVX 隧道到同一个落地"
                h5={h5}
                items={of("backup")}
                title="🛟 备线（断线时自动走）"
              />
              {isAdmin && (
                <Section
                  collapsible
                  desc="其余利群入口机到利群专线（各取最快的入口），点开看"
                  h5={h5}
                  items={of("other")}
                  title="其他利群入口机"
                />
              )}
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
