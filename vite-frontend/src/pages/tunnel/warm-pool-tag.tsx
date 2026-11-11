import { ChevronDown, Zap } from "lucide-react";

import { landingToneClass } from "@/pages/landing/pill";
import {
  Dropdown,
  DropdownMenu,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// Plan 244 「预热池」: how much the warm pool saves on this tunnel, measured
// by the pool itself over the last hour (cold = the pool's own refill dials +
// streams that found it empty; warm = streams that took a pooled conn, incl.
// the redial when one was dead). A separate tag beside the 最优 chips (which
// stay exactly as they are); tap / click for the per-hop detail (plan 156:
// never on hover). Nothing when the switch is off, the tunnel has no active
// rule or there are fewer than 10 warm streams yet.

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
}

export interface WarmPoolState {
  status: "faster" | "slower" | "unsupported";
  savedMs: number;
  reason?: string;
  hitPct: number;
  coldMs: number;
  extraMs: number;
  warmN: number;
  kind: "tunnel" | "forward";
  hops: WarmPoolHop[];
  windowMin: number;
}

/** Placement on the tunnel page: "inline" (A) = right after the 最优 chips in
 * the same row; "line" (B) = own small line under them. */
export type WarmTagPlacement = "inline" | "line";

export const WARM_TAG_PLACEMENT_DEFAULT: WarmTagPlacement = "inline";

/** Preview override (gallery): localStorage `flvx:warm-tag` = inline | line. */
export function warmTagPlacement(): WarmTagPlacement {
  try {
    const v = window.localStorage.getItem("flvx:warm-tag");

    if (v === "inline" || v === "line") return v;
  } catch {
    // storage blocked: default
  }

  return WARM_TAG_PLACEMENT_DEFAULT;
}

const pillBase =
  "inline-flex max-w-full shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 text-xs font-semibold leading-[18px]";

const num = (v: number) => {
  const a = Math.abs(v);

  if (a > 0 && a < 1) return "<1";

  return String(Math.round(a));
};

const SHORT_REASON: Record<string, string> = {
  预热连接常被对方断开: "常被对方断开",
};

function tagText(state: WarmPoolState): string {
  switch (state.status) {
    case "faster":
      return `预热 −${num(state.savedMs)} ms`;
    case "slower":
      return `预热 +${num(state.savedMs)} ms`;
    default:
      return "目标不支持预热";
  }
}

function hopLine(hop: WarmPoolHop): string {
  if (hop.state === "unsupported") {
    return "目标不支持预热（对方很快就断开空闲连接，30 分钟后再试）";
  }
  if (!hop.enough) {
    return `样本不足（近 1 小时用上预热 ${hop.warmN} 次，满 10 次才算）`;
  }
  const parts = [
    `用上预热 ${hop.hitPct}%`,
    `冷连接 ${num(hop.coldMs)} ms`,
    `预热多等 ${num(hop.extraMs)} ms`,
  ];

  if (hop.deadN) parts.push(`失效重连 ${hop.deadN} 次`);

  return parts.join(" · ");
}

function savedTag(v?: number | null) {
  if (v === undefined || v === null) return null;
  const tone = v >= 0 ? landingToneClass.green : landingToneClass.red;

  return (
    <span className={`${pillBase} ${tone} px-1.5 text-[11px]`}>
      {v >= 0 ? "−" : "+"}
      {num(v)} ms
    </span>
  );
}

export function WarmPoolTag({
  state,
  className = "",
}: {
  state?: WarmPoolState | null;
  className?: string;
}) {
  if (!state || !state.status) return null;
  const tone =
    state.status === "faster"
      ? landingToneClass.green
      : state.status === "slower"
        ? landingToneClass.red
        : landingToneClass.slate;
  const text = tagText(state);
  const reason = state.status === "slower" ? state.reason : undefined;
  const windowText = `近 ${state.windowMin >= 60 ? `${Math.round(state.windowMin / 60)} 小时` : `${state.windowMin} 分钟`}`;

  return (
    <Dropdown>
      <DropdownTrigger>
        <button
          aria-label={`${text}${reason ? `，${reason}` : ""}，点按查看详情`}
          className={`${pillBase} ${tone} ${className} cursor-pointer hover:brightness-95 focus-visible:outline-2 focus-visible:outline-offset-1 dark:hover:brightness-110`}
          data-warm-pool-tag={state.status}
          type="button"
          onClick={(e) => e.stopPropagation()}
        >
          <Zap aria-hidden className="h-3 w-3 shrink-0" />
          <span>{text}</span>
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
            <span>预热池</span>
            {state.status !== "unsupported" ? savedTag(state.savedMs) : null}
          </div>
          {state.status !== "unsupported" ? (
            <p className="text-xs text-default-600 dark:text-default-500">
              用上预热 {state.hitPct}% · 冷连接 {num(state.coldMs)} ms ·
              预热多等 {num(state.extraMs)} ms · {windowText}
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
          {state.hops.length > 0 && (
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
          <p className="text-[11px] text-default-400">
            省下的 = 冷连接的建连时间 −
            预热连接多等的时间（预热连接失效时要重连，所以会多等）
          </p>
        </div>
      </DropdownMenu>
    </Dropdown>
  );
}
