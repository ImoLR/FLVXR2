import { useState } from "react";
import toast from "react-hot-toast";
import { ChevronDown, Route, TriangleAlert } from "lucide-react";

import { reenableRuleBest } from "@/api";
import { LATENCY_TAG_STYLE, LatencyTag } from "@/components/latency-tag";
import { landingToneClass, type LandingTone } from "@/pages/landing/pill";
import { latencyNumber, latencyText } from "@/utils/latency-tone";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Dropdown,
  DropdownMenu,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// Plan 180: under a rule on a tunnel with a 「最优」 layer, the node(s) the
// 最优 layers use right now. With 「规则最优」 the ms include exit → target
// (含落地). When the rule's entries or targets pick different nodes the line
// says 「N 个节点」. The data comes with the rule list (no extra request).
//
// Plan 184 r2: worded like the 隧道 page 最优 chips — 「中转最优：东京B →
// 出口最优：新加坡A」 + the ms tag — and, like them, every line is tappable
// (tap / click, never hover; plan 156): the hint, then the pick row(s).

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

// Plan 184: the ms is its own coloured tag (components/latency-tag.tsx) —
// a sibling chip after the route pill (A) or nested inside it (B, picked).
const nestedMs = LATENCY_TAG_STYLE === "nested";

// Plan 184 r3: never a min–max range. One ms per line: the single pick's ms
// (an old backend may still send min ≠ max without items → the larger one).
const lineMs = (pick: RuleBestPick) =>
  Math.max(pick.minMs ?? 0, pick.maxMs ?? 0);

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

// Plan 184 r2: 「${label}最优：${name}」 per 最优 layer (backend labels stay
// 中转 / 中转N / 出口), joined by 「→」. Non-applied states keep their text.
const pickSegments = (pick: RuleBestPick): string[] => {
  const applied = pick.status === "applied";

  return applied && pick.layers && pick.layers.length > 0
    ? pick.layers.map((l, i) => `${i > 0 ? "→ " : ""}${l.label}最优：${l.name}`)
    : [applied ? pick.summary : statusText[pick.status] || pick.summary];
};

// Plan 184 r3: a rule with several targets (or a tunnel with several entries)
// whose picks differ gets one row per target / entry: 「落地1」 (rule target
// order), 「入口：X」, 「X · 落地1」 — each with its own ms tag. The address
// stays in the popup.
interface PickRow {
  label: string;
  item: RuleBestPickItem;
}

const MAX_LINE_ROWS = 3;

const itemSig = (it: RuleBestPickItem) =>
  it.status === "applied"
    ? `${(it.hops || []).map((h) => `${h.label}:${h.name}`).join(">")}|${latencyNumber(it.ms)}|${!!it.withTarget}`
    : `~${it.status}`;

const pickRows = (pick: RuleBestPick): PickRow[] => {
  const items = pick.items || [];

  if (!pick.multi || items.length < 2) return [];
  // Same path, same shown ms everywhere → the plain single line.
  if (items.every((it) => itemSig(it) === itemSig(items[0]))) return [];
  const targets: string[] = [];

  for (const it of items) {
    if (it.target && !targets.includes(it.target)) targets.push(it.target);
  }

  return items.map((item, i) => {
    const t = item.target ? `落地${targets.indexOf(item.target) + 1}` : "";
    const label = item.entry
      ? t
        ? `${item.entry} · ${t}`
        : `入口：${item.entry}`
      : t || `线路${i + 1}`;

    return { label, item };
  });
};

// A single pick comes without items (backend); its popover row is built from
// the line itself: same hops, same ms and 含落地. With equal items the first
// one stands for all.
const singlePickItem = (pick: RuleBestPick): RuleBestPickItem => {
  const items = pick.items || [];

  if (pick.multi && items.length > 0) return items[0];

  return {
    status: pick.status,
    summary: statusText[pick.status] || pick.summary,
    hops: (pick.layers || []).map((l) => ({ label: l.label, name: l.name })),
    ms: lineMs(pick),
    withTarget: pick.withTarget,
  };
};

const chevron = <ChevronDown aria-hidden className="h-3 w-3 shrink-0" />;

function PickText({
  pick,
  single,
}: {
  pick: RuleBestPick;
  single: RuleBestPickItem | null;
}) {
  const segs = pickSegments(pick);
  const tag =
    single && single.status === "applied" ? (
      <LatencyTag
        nested
        ms={single.ms}
        size="pick"
        withTarget={!!single.withTarget}
      />
    ) : null;
  // B: the chevron follows the nested ms tag; A / rows: it rides on the
  // last hop.
  const chevronOnTag = nestedMs && !!tag;

  // The line breaks only between hops: 「🔀 中转最优：东京B」「→ 出口最优：新加坡A ⌄」;
  // the icon / chevron stick to their hop, a long node name truncates inside
  // its hop (the popover row shows it in full).
  return (
    <>
      {segs.map((seg, i) => (
        <span
          key={i}
          className="inline-flex min-w-0 max-w-full items-center gap-1"
        >
          {i === 0 && <Route aria-label="最优" className="h-3 w-3 shrink-0" />}
          <span className="min-w-0 truncate" data-rule-best-seg={i}>
            {seg}
          </span>
          {i === segs.length - 1 && !chevronOnTag && chevron}
        </span>
      ))}
      {chevronOnTag && (
        <span className="inline-flex shrink-0 items-center gap-1">
          {tag}
          {chevron}
        </span>
      )}
    </>
  );
}

// One target / entry under the line: 「落地1 [28 ms]」 (B: tag inside the
// chip, A: right after it); not applied → its status text, no tag.
function PickRowChip({ row }: { row: PickRow }) {
  const { item } = row;
  const applied = item.status === "applied";
  const tone = applied ? "teal" : (statusTone[item.status] ?? "slate");
  const tag = applied ? (
    <LatencyTag
      ms={item.ms}
      nested={nestedMs}
      size="pick"
      withTarget={!!item.withTarget}
    />
  ) : null;
  const chip = (
    <span
      className={`${pillBase} min-w-0 flex-nowrap ${landingToneClass[tone]}`}
      data-rule-best-row={item.status}
    >
      <span data-rule-best-row-label className="min-w-0 truncate">
        {row.label}
      </span>
      {!applied && (
        <span className="shrink-0 whitespace-nowrap font-medium">
          · {statusText[item.status] || item.summary || item.status}
        </span>
      )}
      {nestedMs && tag}
    </span>
  );

  if (nestedMs || !tag) return chip;

  return (
    <span className="inline-flex min-w-0 max-w-full items-center gap-1">
      {chip}
      {tag}
    </span>
  );
}

function PickItemRow({
  item,
  label,
}: {
  item: RuleBestPickItem;
  label?: string;
}) {
  return (
    <div className="space-y-0.5 rounded-md bg-default-100/60 p-2 dark:bg-default-100/30">
      {(label || item.target) && (
        <div className="text-[11px] font-medium text-default-500">
          {label && <span className="text-foreground">{label}</span>}
          {label && item.target && " · "}
          {item.target && <span className="break-all">{item.target}</span>}
        </div>
      )}
      {item.status === "applied" ? (
        <div className="flex flex-wrap items-center gap-x-1 text-xs font-semibold text-foreground">
          {(item.hops || []).map((hop, i) => (
            <span key={`${hop.label}-${i}`} className="break-all">
              {i > 0 && <span className="mx-0.5 text-default-400">→</span>}
              <span className="text-default-500">{hop.label}最优</span>{" "}
              <span className="text-teal-700 dark:text-teal-300">
                {hop.name}
              </span>
            </span>
          ))}
          <LatencyTag ms={item.ms} size="row" withTarget={!!item.withTarget} />
        </div>
      ) : (
        <div className="text-xs text-default-500">
          {statusText[item.status] || item.summary || item.status}
        </div>
      )}
    </div>
  );
}

const focusRing =
  "cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-1";
const hoverPill = "hover:brightness-95 dark:hover:brightness-110";

export function RuleBestPickLine({
  pick,
  className = "",
}: {
  pick?: RuleBestPick | null;
  className?: string;
}) {
  if (!pick) return null;
  const tone = statusTone[pick.status] ?? "slate";
  const rows = pickRows(pick);
  const multi = rows.length > 0;
  const single = multi ? null : singlePickItem(pick);
  const shown = rows.slice(0, MAX_LINE_ROWS);
  const more = rows.length - shown.length;
  const popupRows: PickRow[] = multi ? rows : [{ label: "", item: single! }];
  const spoken = multi
    ? `，${rows.map((r) => `${r.label} ${r.item.status === "applied" ? latencyText(r.item.ms) : statusText[r.item.status] || r.item.status}`).join("，")}`
    : single && single.status === "applied"
      ? ` · ${latencyText(single.ms)}`
      : "";
  const pillClass = `${pillBase} ${landingToneClass[tone]}`;

  // max-w-full: in the phone card (flex-col items-start) the line would
  // otherwise grow with a long node name past the card edge. With rows the
  // whole block (line + rows) is the one tap target.
  return (
    <div
      className={`mt-1 flex min-w-0 max-w-full flex-wrap items-center gap-1 ${className}`}
    >
      <Dropdown>
        <DropdownTrigger>
          <button
            aria-label={`${pickSegments(pick).join(" ")}${spoken}，点按查看${multi ? "每个入口 / 目标" : "说明"}`}
            className={
              multi
                ? `group flex min-w-0 max-w-full flex-col items-start gap-1 rounded-lg text-left ${focusRing}`
                : `${pillClass} ${focusRing} ${hoverPill}`
            }
            data-rule-best-multi={multi ? "" : undefined}
            data-rule-best-pick={pick.status}
            type="button"
            onClick={(e) => e.stopPropagation()}
          >
            {multi ? (
              <>
                <span
                  className={`${pillClass} group-hover:brightness-95 dark:group-hover:brightness-110`}
                >
                  <PickText pick={pick} single={null} />
                </span>
                <span
                  className="flex min-w-0 max-w-full flex-wrap items-center gap-1 group-hover:brightness-95 dark:group-hover:brightness-110"
                  data-rule-best-rows={rows.length}
                >
                  {shown.map((row, i) => (
                    <PickRowChip key={`${row.label}-${i}`} row={row} />
                  ))}
                  {more > 0 && (
                    <span
                      className={`${pillBase} whitespace-nowrap ${landingToneClass.slate}`}
                      data-rule-best-more={more}
                    >
                      还有 {more} 个
                    </span>
                  )}
                </span>
              </>
            ) : (
              <PickText pick={pick} single={nestedMs ? single : null} />
            )}
          </button>
        </DropdownTrigger>
        <DropdownMenu aria-label="最优线路详情">
          <div className="max-w-[86vw] space-y-2 p-2 text-left sm:max-w-sm">
            <p className="text-xs text-default-500">{RULE_BEST_PICK_HINT}</p>
            {popupRows.map((row, i) => (
              <PickItemRow
                key={`${row.label}-${i}`}
                item={row.item}
                label={row.label}
              />
            ))}
          </div>
        </DropdownMenu>
      </Dropdown>
      {!nestedMs && !multi && single?.status === "applied" && (
        <LatencyTag
          ms={single.ms}
          size="pick"
          withTarget={!!single.withTarget}
        />
      )}
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
