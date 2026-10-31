import { useState } from "react";
import toast from "react-hot-toast";
import { ChevronDown, Route, TriangleAlert } from "lucide-react";

import { reenableRuleBest } from "@/api";
import { landingToneClass, type LandingTone } from "@/pages/landing/pill";
import { Button } from "@/shadcn-bridge/heroui/button";
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

// Plan 180 r2: 「规则最优」 turned off automatically — the rule's landing was
// unreachable from every exit through the retries (1/3/10/60 min). Admin
// only (the data only reaches admins): an amber pill under the rule name;
// a tap opens the notice and 「重新开启规则最优」 (one click). The rule keeps
// running on the tunnel pick meanwhile.

export interface RuleBestOff {
  target: string;
  at: number;
  text: string;
}

export const RULE_BEST_OFF_CHANGED = "flvx:rule-best-off-changed";

export const normalizeRuleBestOff = (raw: unknown): RuleBestOff | null => {
  if (!raw || typeof raw !== "object") return null;
  const off = raw as Partial<RuleBestOff>;

  if (typeof off.text !== "string" || off.text === "") return null;

  return {
    target: typeof off.target === "string" ? off.target : "",
    at: typeof off.at === "number" ? off.at : 0,
    text: off.text,
  };
};

const offTime = (at: number) => {
  if (!at) return "";
  const d = new Date(at);
  const pad = (n: number) => String(n).padStart(2, "0");

  return `${d.getMonth() + 1}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
};

export function RuleBestOffBadge({
  forwardId,
  off,
  className = "",
}: {
  forwardId: number;
  off?: RuleBestOff | null;
  className?: string;
}) {
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);

  if (!off || done) return null;
  const reenable = async () => {
    setBusy(true);
    try {
      const res = await reenableRuleBest(forwardId);

      if (res.code === 0) {
        toast.success("已重新开启规则最优，下一轮探测（约 1 分钟内）开始");
        setDone(true);
        window.dispatchEvent(new Event(RULE_BEST_OFF_CHANGED));
      } else {
        toast.error(res.msg || "重新开启失败");
      }
    } catch {
      toast.error("重新开启失败");
    } finally {
      setBusy(false);
    }
  };
  const when = offTime(off.at);

  return (
    <div className={`mt-1 flex min-w-0 ${className}`}>
      <Dropdown>
        <DropdownTrigger>
          <button
            aria-label="规则最优已自动关闭，点按查看并重新开启"
            className={`${pillBase} ${landingToneClass.amber} cursor-pointer hover:brightness-95 focus-visible:outline-2 focus-visible:outline-offset-1 dark:hover:brightness-110`}
            data-rule-best-off={forwardId}
            type="button"
            onClick={(e) => e.stopPropagation()}
          >
            <TriangleAlert aria-hidden className="h-3 w-3 shrink-0" />
            <span className="whitespace-nowrap">规则最优已自动关闭</span>
            <ChevronDown aria-hidden className="h-3 w-3 shrink-0" />
          </button>
        </DropdownTrigger>
        <DropdownMenu aria-label="规则最优已自动关闭">
          <div className="max-w-[86vw] space-y-2 p-2 text-left sm:max-w-sm">
            <p className="whitespace-normal break-words text-xs leading-relaxed text-foreground">
              {off.text}
            </p>
            <p className="text-[11px] text-default-500">
              {when ? `${when} 关闭 · ` : ""}
              规则仍在运行，按隧道选路；只有管理员能看到
            </p>
            <Button
              color="success"
              data-rule-best-reenable={forwardId}
              isLoading={busy}
              size="sm"
              variant="flat"
              onPress={reenable}
            >
              重新开启规则最优
            </Button>
          </div>
        </DropdownMenu>
      </Dropdown>
    </div>
  );
}
