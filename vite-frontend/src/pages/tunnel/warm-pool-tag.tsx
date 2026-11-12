import { ChevronDown, Zap } from "lucide-react";

import { LATENCY_TAG_STYLE, MsTag } from "@/components/latency-tag";
import { landingToneClass } from "@/pages/landing/pill";
import {
  Dropdown,
  DropdownMenu,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// Plan 244 「预热池」: how much the warm pool saves on this tunnel, measured
// by the pool itself over the last hour (cold = the pool's own refill dials +
// streams that found it empty; warm = streams that took a pooled conn, incl.
// the redial when one was dead). Its own small line under the 最优 chips
// (user 2026-11-12: layout B; the 最优 chips stay exactly as they are); the ms
// is its own tinted tag, the same one as the 最优 ms (components/latency-tag);
// tap / click for the per-hop detail (plan 156: never on hover). Nothing when the switch is off, the tunnel has no active
// rule or there are fewer than 10 warm streams yet.
//
// Plan 249: a pool key with no stream for 30 min sleeps on the agent (0
// conns, the next stream wakes it). Nothing when every path sleeps; while
// a path had streams in the last hour but < 10 warm ones → a light-grey
// 「预热中 · 数据不足」 pill (detail: the counts so far). The rules page uses
// the same tag (size "pick" = the rules-page 最优 line size); a normal user
// only ever gets the green one, without the per-hop detail.

export interface WarmPoolHop {
  from: string;
  to: string;
  fromNodeId?: number;
  toNodeId?: number;
  warmN: number;
  hitPct: number;
  coldMs: number;
  extraMs: number;
  savedMs?: number | null;
  deadN?: number;
  state?: string; // unsupported | rest | capped
  enough: boolean;
  streams?: number; // plan 249: streams in the window (warm + cold)
  sleep?: boolean; // plan 249: no stream for 30 min
}

export interface WarmPoolState {
  status: "faster" | "slower" | "unsupported" | "warming";
  savedMs: number;
  reason?: string;
  // Absent in a normal user's rule view (plan 249: status + ms only).
  hitPct?: number;
  coldMs?: number;
  extraMs?: number;
  warmN?: number;
  kind: "tunnel" | "forward";
  hops: WarmPoolHop[];
  windowMin: number;
  streams?: number; // plan 249 warming: streams so far
  minWarm?: number; // plan 249 warming: warm streams needed (10)
}

const WARM_STATUSES = ["faster", "slower", "unsupported", "warming"];

// Plan 249: the API value of item.warmPoolState (tunnel or rule list).
export const normalizeWarmPoolState = (raw: unknown): WarmPoolState | null => {
  if (!raw || typeof raw !== "object") return null;
  const st = raw as WarmPoolState;

  if (!WARM_STATUSES.includes(st.status)) return null;

  return {
    ...st,
    savedMs: typeof st.savedMs === "number" ? st.savedMs : 0,
    hops: Array.isArray(st.hops) ? st.hops : [],
    windowMin: typeof st.windowMin === "number" ? st.windowMin : 60,
  };
};

const pillBase =
  "inline-flex max-w-full shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 text-xs font-semibold leading-[18px]";

// Plan 249: on the rules page the tag has the size of the 最优 line there
// (pages/forward/best-pick.tsx pillBase, one line).
const pillPick =
  "inline-flex max-w-full shrink-0 items-center gap-1 whitespace-nowrap rounded-lg px-2 py-px text-[11px] font-semibold leading-[17px]";

// Plan 249 「预热中 · 数据不足」: lighter than the 不支持 grey.
const warmingTone =
  "bg-slate-500/10 text-slate-500 dark:bg-slate-400/10 dark:text-slate-400";

const num = (v: number) => {
  const a = Math.abs(v);

  if (a > 0 && a < 1) return "<1";

  return String(Math.round(a));
};

const SHORT_REASON: Record<string, string> = {
  预热连接常被对方断开: "常被对方断开",
};

// 「−283 ms」 (faster, savedMs ≥ 0) / 「+40 ms」 (slower, savedMs < 0).
const savedText = (v: number) => `${v >= 0 ? "−" : "+"}${num(v)} ms`;

function tagText(state: WarmPoolState): string {
  switch (state.status) {
    case "faster":
    case "slower":
      return `预热 ${savedText(state.savedMs)}`;
    case "warming":
      return "预热中 · 数据不足";
    default:
      return "目标不支持预热";
  }
}

// The 最优 chips nest their ms tag inside the pill (plan 184 B) — same here.
const nestedMs = LATENCY_TAG_STYLE === "nested";

function hopLine(hop: WarmPoolHop): string {
  if (hop.sleep) {
    return "已休眠：30 分钟没有新连接，不占连接；下一个连接冷建并马上唤醒";
  }
  if (hop.state === "unsupported") {
    return "目标不支持预热（对方很快就断开空闲连接，30 分钟后再试）";
  }
  if (!hop.enough) {
    return typeof hop.streams === "number"
      ? `样本不足（近 1 小时 ${hop.streams} 个新连接，用上预热 ${hop.warmN} 次，满 10 次才算）`
      : `样本不足（近 1 小时用上预热 ${hop.warmN} 次，满 10 次才算）`;
  }
  const parts = [
    `用上预热 ${hop.hitPct}%`,
    `冷连接 ${num(hop.coldMs)} ms`,
    `预热多等 ${num(hop.extraMs)} ms`,
  ];

  if (hop.deadN) parts.push(`失效重连 ${hop.deadN} 次`);

  return parts.join(" · ");
}

// Detail rows: the same tag as the 最优 dropdown rows (size "row").
function savedTag(v?: number | null) {
  if (v === undefined || v === null) return null;

  return (
    <MsTag size="row" text={savedText(v)} tone={v >= 0 ? "green" : "red"} />
  );
}

export function WarmPoolTag({
  state,
  className = "",
  size = "chip",
}: {
  state?: WarmPoolState | null;
  className?: string;
  // chip = tunnel page (as plan 244); pick = rules page (plan 249)
  size?: "chip" | "pick";
}) {
  if (!state || !state.status) return null;
  const tone =
    state.status === "faster"
      ? landingToneClass.green
      : state.status === "slower"
        ? landingToneClass.red
        : state.status === "warming"
          ? warmingTone
          : landingToneClass.slate;
  const text = tagText(state);
  const reason = state.status === "slower" ? state.reason : undefined;
  // 「预热 [−283 ms]」: the ms is its own tag, like 「出口最优：HK-1 [42 ms]」.
  const msTag =
    state.status === "unsupported" || state.status === "warming" ? null : (
      <MsTag
        nested={nestedMs}
        size={size}
        text={savedText(state.savedMs)}
        tone={state.status === "faster" ? "green" : "red"}
      />
    );
  const windowText = `近 ${state.windowMin >= 60 ? `${Math.round(state.windowMin / 60)} 小时` : `${state.windowMin} 分钟`}`;
  // A normal user's rule view carries status + ms only (plan 249).
  const detailed = typeof state.hitPct === "number";

  return (
    <Dropdown>
      <DropdownTrigger>
        <button
          aria-label={`${text}${reason ? `，${reason}` : ""}，点按查看详情`}
          className={`${size === "pick" ? pillPick : pillBase} ${tone} ${className} cursor-pointer hover:brightness-95 focus-visible:outline-2 focus-visible:outline-offset-1 dark:hover:brightness-110`}
          data-warm-pool-size={size === "pick" ? "pick" : undefined}
          data-warm-pool-tag={state.status}
          type="button"
          onClick={(e) => e.stopPropagation()}
        >
          <Zap aria-hidden className="h-3 w-3 shrink-0" />
          {msTag ? (
            <>
              <span>预热</span>
              {msTag}
            </>
          ) : (
            <span>{text}</span>
          )}
          {reason && (
            <span className="font-medium opacity-80">
              · {SHORT_REASON[reason] ?? reason}
            </span>
          )}
          <ChevronDown aria-hidden className="h-3 w-3 shrink-0" />
        </button>
      </DropdownTrigger>
      <DropdownMenu aria-label="预热池详情">
        <div className="max-w-[86vw] space-y-2 p-2 text-left sm:max-w-sm">
          <div className="flex items-center gap-1 text-xs font-semibold text-foreground">
            <span>
              {state.status === "warming" ? "预热池 · 预热中" : "预热池"}
            </span>
            {state.status !== "unsupported" && state.status !== "warming"
              ? savedTag(state.savedMs)
              : null}
          </div>
          {!detailed ? (
            <p className="text-xs text-default-600 dark:text-default-500">
              提前建好到下一跳的连接，这条规则的新连接平均快{" "}
              {num(state.savedMs)} ms（{windowText}实测）
            </p>
          ) : state.status === "warming" ? (
            <p className="text-xs text-default-600 dark:text-default-500">
              {windowText} {state.streams ?? 0} 个新连接，用上预热{" "}
              {state.warmN ?? 0} 次；满 {state.minWarm ?? 10} 次后显示省了多少
              ms
            </p>
          ) : state.status !== "unsupported" ? (
            <p className="text-xs text-default-600 dark:text-default-500">
              用上预热 {state.hitPct}% · 冷连接 {num(state.coldMs ?? 0)} ms ·
              预热多等 {num(state.extraMs ?? 0)} ms · {windowText}
            </p>
          ) : (
            <p className="text-xs text-default-600 dark:text-default-500">
              目标会很快断开空闲连接，暂不预热（30 分钟后自动再试）
            </p>
          )}
          {reason && (
            <p className="text-xs font-medium text-red-700 dark:text-red-300">
              {reason}：用到的预热连接已失效，只能重新连接
            </p>
          )}
          {detailed && state.hops.length > 0 && (
            <div className="space-y-1">
              {state.hops.map((hop, i) => (
                <div
                  key={`${hop.from}-${hop.to}-${i}`}
                  className="space-y-0.5 rounded-md bg-default-100/60 p-2 dark:bg-default-100/30"
                >
                  <div className="flex flex-wrap items-center gap-x-1 gap-y-0.5 text-xs font-semibold text-foreground">
                    <span className="break-all">{hop.from}</span>
                    <span className="text-default-400">→</span>
                    <span className="break-all">{hop.to}</span>
                    {hop.enough && hop.state !== "unsupported"
                      ? savedTag(hop.savedMs)
                      : null}
                  </div>
                  <p
                    className={`text-[11px] ${hop.state === "unsupported" ? "text-slate-600 dark:text-slate-300" : "text-default-500"}`}
                  >
                    {hopLine(hop)}
                  </p>
                </div>
              ))}
            </div>
          )}
          {detailed && (
            <p className="text-[11px] text-default-400">
              省下的 = 冷连接的建连时间 −
              预热连接多等的时间（预热连接失效时要重连，所以会多等）
            </p>
          )}
          {detailed && (
            <p className="text-[11px] text-default-400">
              30 分钟没有新连接的线路自动休眠（不占连接），下一个连接会唤醒
            </p>
          )}
        </div>
      </DropdownMenu>
    </Dropdown>
  );
}
