import { ChevronDown, Route } from "lucide-react";

import { landingToneClass, type LandingTone } from "@/pages/landing/pill";
import {
  Dropdown,
  DropdownMenu,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// Plan 180: under a rule on a tunnel with a 「最优」 layer, the node(s) the
// 最优 layers use right now — 「中转 东京B → 出口 新加坡A · 38 ms」. With
// 「规则最优」 the ms include exit → target (含落地). When the rule's entries or
// targets pick different nodes the line says 「N 个节点」 and a tap / click
// opens the per-entry / per-target picks (touch first, never hover; plan 156).
// The data comes with the rule list (no extra request).

export interface RuleBestPickHop {
  label: string;
  name: string;
}

export interface RuleBestPickItem {
  entry?: string;
  target?: string;
  status: string;
  summary?: string;
  hops?: RuleBestPickHop[];
  ms?: number;
  withTarget?: boolean;
}

export interface RuleBestPickLayer {
  label: string;
  name: string;
  count: number;
}

export interface RuleBestPick {
  status: string;
  summary: string;
  layers?: RuleBestPickLayer[];
  minMs?: number;
  maxMs?: number;
  withTarget?: boolean;
  multi?: boolean;
  items?: RuleBestPickItem[];
  updatedAt?: number;
}

export const RULE_BEST_PICK_HINT =
  "最优：每分钟按延迟自动选最快的节点，断线自动切备用；「含落地」= 出口到目标的延迟也算在内";

const statusTone: Record<string, LandingTone> = {
  applied: "teal",
  waiting: "slate",
  offline: "slate",
  remote: "slate",
  upgrade: "amber",
  unreachable: "red",
};

const statusText: Record<string, string> = {
  waiting: "等待探测",
  offline: "节点离线",
  remote: "远程节点按顺序",
  upgrade: "节点需升级",
  unreachable: "全部不可达",
};

const pillBase =
  "inline-flex max-w-full flex-wrap items-center gap-x-1 rounded-lg px-2 py-px text-left text-[11px] font-semibold leading-[17px]";

// Same-room paths are sub-millisecond: 「<1 ms」, never 「0 ms」.
const num = (v?: number) => ((v ?? 0) < 1 ? "<1" : String(Math.round(v ?? 0)));

const pickMs = (pick: RuleBestPick) => {
  if (pick.status !== "applied") return "";
  const min = num(pick.minMs);

  if (!pick.maxMs || num(pick.maxMs) === min) return `${min} ms`;

  return `${min}–${num(pick.maxMs)} ms`;
};

export const normalizeRuleBestPick = (raw: unknown): RuleBestPick | null => {
  if (!raw || typeof raw !== "object") return null;
  const pick = raw as RuleBestPick;

  if (typeof pick.status !== "string") return null;

  return {
    ...pick,
    summary: typeof pick.summary === "string" ? pick.summary : "",
    items: Array.isArray(pick.items) ? pick.items : [],
    layers: Array.isArray(pick.layers) ? pick.layers : [],
  };
};

function PickText({ pick }: { pick: RuleBestPick }) {
  const applied = pick.status === "applied";
  const msText = pickMs(pick);
  // Segments break only between hops: 「中转 东京B」「→ 出口 新加坡A」「· 38 ms」.
  const segs =
    applied && pick.layers && pick.layers.length > 0
      ? pick.layers.map((l, i) => `${i > 0 ? "→ " : ""}${l.label} ${l.name}`)
      : [applied ? pick.summary : statusText[pick.status] || pick.summary];

  return (
    <>
      <Route aria-label="最优" className="h-3 w-3 shrink-0" />
      {segs.map((seg, i) => (
        <span key={i} className="whitespace-nowrap">
          {seg}
        </span>
      ))}
      {msText && (
        <span className="inline-flex items-center gap-1 whitespace-nowrap">
          · {msText}
          {pick.withTarget && (
            <span className="rounded-full bg-white/60 px-1 text-[10px] font-medium leading-[14px] dark:bg-black/20">
              含落地
            </span>
          )}
        </span>
      )}
    </>
  );
}

function PickItemRow({ item }: { item: RuleBestPickItem }) {
  const head = [
    item.entry && `入口 ${item.entry}`,
    item.target && `目标 ${item.target}`,
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <div className="space-y-0.5 rounded-md bg-default-100/60 p-2 dark:bg-default-100/30">
      {head && (
        <div className="break-all text-[11px] font-medium text-default-500">
          {head}
        </div>
      )}
      {item.status === "applied" ? (
        <div className="flex flex-wrap items-center gap-x-1 text-xs font-semibold text-foreground">
          {(item.hops || []).map((hop, i) => (
            <span key={`${hop.label}-${i}`} className="break-all">
              {i > 0 && <span className="mx-0.5 text-default-400">→</span>}
              <span className="text-default-500">{hop.label}</span>{" "}
              <span className="text-teal-700 dark:text-teal-300">
                {hop.name}
              </span>
            </span>
          ))}
          <span className="text-default-500">
            · {num(item.ms)} ms{item.withTarget ? "（含落地）" : ""}
          </span>
        </div>
      ) : (
        <div className="text-xs text-default-500">
          {statusText[item.status] || item.summary || item.status}
        </div>
      )}
    </div>
  );
}

export function RuleBestPickLine({
  pick,
  className = "",
}: {
  pick?: RuleBestPick | null;
  className?: string;
}) {
  if (!pick) return null;
  const tone = statusTone[pick.status] ?? "slate";
  const items = pick.items || [];

  if (!pick.multi || items.length < 2) {
    return (
      <div className={`mt-1 flex min-w-0 ${className}`}>
        <span
          className={`${pillBase} ${landingToneClass[tone]}`}
          data-rule-best-pick={pick.status}
        >
          <PickText pick={pick} />
        </span>
      </div>
    );
  }

  return (
    <div className={`mt-1 flex min-w-0 ${className}`}>
      <Dropdown>
        <DropdownTrigger>
          <button
            aria-label="最优线路，点按查看每个入口 / 目标"
            className={`${pillBase} ${landingToneClass[tone]} cursor-pointer hover:brightness-95 focus-visible:outline-2 focus-visible:outline-offset-1 dark:hover:brightness-110`}
            data-rule-best-pick={pick.status}
            type="button"
            onClick={(e) => e.stopPropagation()}
          >
            <PickText pick={pick} />
            <ChevronDown aria-hidden className="h-3 w-3 shrink-0" />
          </button>
        </DropdownTrigger>
        <DropdownMenu aria-label="最优线路详情">
          <div className="max-w-[86vw] space-y-2 p-2 text-left sm:max-w-sm">
            <p className="text-xs text-default-500">{RULE_BEST_PICK_HINT}</p>
            {items.map((item, i) => (
              <PickItemRow
                key={`${item.entry}-${item.target}-${i}`}
                item={item}
              />
            ))}
          </div>
        </DropdownMenu>
      </Dropdown>
    </div>
  );
}
