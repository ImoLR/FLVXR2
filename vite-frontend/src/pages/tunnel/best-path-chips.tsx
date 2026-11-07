import { ChevronDown } from "lucide-react";

import { LATENCY_TAG_STYLE, LatencyTag } from "@/components/latency-tag";
import { landingToneClass, type LandingTone } from "@/pages/landing/pill";
import { latencyNumber, latencyText } from "@/utils/latency-tone";
import {
  Dropdown,
  DropdownMenu,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// Plan 165: 「最优」 decisions of a tunnel (admin list only). One tinted chip
// per 最优 layer — 「出口最优：HK-1 · 42 ms」 — and a tap / click opens the
// per-owner detail (works on touch, never on hover; plan 156).

export interface BestPathCandidate {
  nodeId: number;
  name: string;
  costMs?: number;
  reachable: boolean;
}

export interface BestPathItem {
  ownerNodeId: number;
  ownerName: string;
  status: string;
  bestNodeId?: number;
  bestName?: string;
  costMs?: number;
  switchedAt?: number;
  updatedAt?: number;
  candidates?: BestPathCandidate[];
}

export interface BestPathLayer {
  layer: number;
  label: string;
  status: string;
  summary: string;
  minMs?: number;
  maxMs?: number;
  items: BestPathItem[];
}

export interface BestPathState {
  layers: BestPathLayer[];
  /** Plan 234: 最优 switch margin in force (网站配置 → 自动切换门槛). */
  marginMs?: number;
}

export const BEST_PATH_HINT =
  "最优：每分钟按入口到出口延迟自动选最快的，断线自动切备用";

const statusTone: Record<string, LandingTone> = {
  applied: "green",
  waiting: "slate",
  offline: "slate",
  remote: "slate",
  upgrade: "amber",
  unreachable: "red",
};

const statusText: Record<string, string> = {
  waiting: "等待探测",
  offline: "节点离线",
  remote: "远程节点，按顺序",
  upgrade: "节点需升级，暂按顺序",
  unreachable: "全部不可达",
};

const pillBase =
  "inline-flex max-w-full shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 text-xs font-semibold leading-[18px]";

// Same-room paths are sub-millisecond: 「<1 ms」, never 「0 ms」.
const ms = (v?: number) => `${latencyNumber(v)} ms`;

// Plan 184: the ms is its own coloured tag (components/latency-tag.tsx) —
// a sibling chip after the 最优 chip (A) or nested inside it (B). In A the
// chip may shrink (its text truncates) so chip + tag stay on one line.
const nestedMs = LATENCY_TAG_STYLE === "nested";
const triggerBase = nestedMs
  ? pillBase
  : pillBase.replace("shrink-0", "min-w-0 shrink");

const timeText = (at?: number) => {
  if (!at) return "";
  const d = new Date(at);
  const pad = (n: number) => String(n).padStart(2, "0");

  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
};

export const normalizeBestPathState = (raw: unknown): BestPathState | null => {
  if (!raw || typeof raw !== "object") return null;
  const layers = (raw as { layers?: unknown }).layers;

  if (!Array.isArray(layers) || layers.length === 0) return null;

  const marginMs = (raw as { marginMs?: unknown }).marginMs;

  return {
    layers: layers.map((layer: BestPathLayer) => ({
      ...layer,
      items: Array.isArray(layer.items) ? layer.items : [],
    })),
    marginMs: typeof marginMs === "number" ? marginMs : undefined,
  };
};

// Plan 184 r3: no min–max range. When the layer's owners differ in ms (e.g.
// two entries), the chip shows no ms and one row per owner follows —
// 「深港1 → AWS HK [25 ms]」 (first 3 + 「还有 N 个」); chip + rows are one tap
// target opening the per-owner detail as before.
const MAX_CHIP_ROWS = 3;

function OwnerRowChip({ item }: { item: BestPathItem }) {
  const applied = item.status === "applied";
  const tag = applied ? (
    <LatencyTag ms={item.costMs} nested={nestedMs} size="chip" />
  ) : null;
  const chip = (
    <span
      className={`${pillBase.replace("shrink-0", "min-w-0 shrink")} ${landingToneClass[applied ? "green" : (statusTone[item.status] ?? "slate")]}`}
      data-best-path-row={item.status}
    >
      <span className="min-w-0 truncate">
        {item.ownerName} →{" "}
        {applied ? item.bestName : (statusText[item.status] ?? item.status)}
      </span>
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

function BestPathLayerChip({
  layer,
  marginMs = 5,
}: {
  layer: BestPathLayer;
  marginMs?: number;
}) {
  const tone = statusTone[layer.status] ?? "slate";
  const applied = layer.status === "applied";
  const text = `${layer.label}最优：${layer.summary || "等待探测"}`;
  const appliedItems = layer.items.filter((it) => it.status === "applied");
  const split =
    applied &&
    appliedItems.some(
      (it) =>
        latencyNumber(it.costMs) !== latencyNumber(appliedItems[0].costMs),
    );
  const rows = split ? layer.items : [];
  const shown = rows.slice(0, MAX_CHIP_ROWS);
  const more = rows.length - shown.length;
  // One ms for the chip: the owners agree (or an old payload without items).
  const chipMs =
    appliedItems[0]?.costMs ?? Math.max(layer.minMs ?? 0, layer.maxMs ?? 0);
  const msLabel = split
    ? `，${rows.map((it) => `${it.ownerName} ${it.status === "applied" ? latencyText(it.costMs) : (statusText[it.status] ?? it.status)}`).join("，")}`
    : applied
      ? ` · ${latencyText(chipMs)}`
      : "";
  const tag =
    applied && !split ? (
      <LatencyTag ms={chipMs} nested={nestedMs} size="chip" />
    ) : null;
  const chipClass = `${landingToneClass[tone]}`;
  const chipInner = (
    <>
      <span className="min-w-0 truncate">{text}</span>
      {nestedMs && tag}
      <ChevronDown aria-hidden className="h-3 w-3 shrink-0" />
    </>
  );

  const dropdown = (
    <Dropdown>
      <DropdownTrigger>
        {split ? (
          <button
            aria-label={`${text}${msLabel}，点按查看详情`}
            className="group inline-flex min-w-0 max-w-full cursor-pointer flex-col items-center gap-1 rounded-lg focus-visible:outline-2 focus-visible:outline-offset-1"
            data-best-path-chip={layer.status}
            data-best-path-split={rows.length}
            type="button"
          >
            <span
              className={`${triggerBase} ${chipClass} group-hover:brightness-95 dark:group-hover:brightness-110`}
            >
              {chipInner}
            </span>
            <span className="flex min-w-0 max-w-full flex-wrap items-center justify-center gap-1 group-hover:brightness-95 dark:group-hover:brightness-110">
              {shown.map((item) => (
                <OwnerRowChip key={item.ownerNodeId} item={item} />
              ))}
              {more > 0 && (
                <span
                  className={`${pillBase} ${landingToneClass.slate}`}
                  data-best-path-more={more}
                >
                  还有 {more} 个
                </span>
              )}
            </span>
          </button>
        ) : (
          <button
            aria-label={`${text}${msLabel}，点按查看详情`}
            className={`${triggerBase} ${chipClass} cursor-pointer hover:brightness-95 focus-visible:outline-2 focus-visible:outline-offset-1 dark:hover:brightness-110`}
            data-best-path-chip={layer.status}
            type="button"
          >
            {chipInner}
          </button>
        )}
      </DropdownTrigger>
      <DropdownMenu aria-label={`${layer.label}最优详情`}>
        <div className="max-w-[86vw] space-y-2 p-2 text-left sm:max-w-sm">
          <p className="text-xs text-default-500">{BEST_PATH_HINT}</p>
          {layer.items.map((item) => (
            <div
              key={item.ownerNodeId}
              className="space-y-1 rounded-md bg-default-100/60 p-2 dark:bg-default-100/30"
            >
              <div className="flex flex-wrap items-center gap-x-1 text-xs font-semibold text-foreground">
                <span className="break-all">{item.ownerName}</span>
                <span className="text-default-400">→</span>
                {item.status === "applied" ? (
                  <>
                    <span className="break-all text-green-700 dark:text-green-300">
                      {item.bestName}
                    </span>
                    <LatencyTag ms={item.costMs} size="row" />
                  </>
                ) : (
                  <span className="text-default-500">
                    {statusText[item.status] ?? item.status}
                  </span>
                )}
              </div>
              {item.candidates && item.candidates.length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {item.candidates.map((c) => (
                    <span
                      key={c.nodeId}
                      className={`${pillBase} ${
                        landingToneClass[
                          !c.reachable
                            ? "red"
                            : c.nodeId === item.bestNodeId
                              ? "green"
                              : "slate"
                        ]
                      }`}
                    >
                      <span className="min-w-0 truncate">
                        {c.nodeId === item.bestNodeId ? "当前 " : ""}
                        {c.name} · {c.reachable ? ms(c.costMs) : "不可达"}
                      </span>
                    </span>
                  ))}
                </div>
              )}
              {item.status === "applied" &&
                item.candidates?.[0]?.reachable &&
                item.candidates[0].nodeId !== item.bestNodeId && (
                  <p className="text-[11px] text-default-400">
                    {marginMs > 0
                      ? `更快的不到 ${marginMs} ms 或还没连续 2 分钟，先不换`
                      : "还没连续 2 分钟更快，先不换"}
                  </p>
                )}
              {item.switchedAt ? (
                <p className="text-[11px] text-default-400">
                  上次切换 {timeText(item.switchedAt)}
                </p>
              ) : null}
            </div>
          ))}
        </div>
      </DropdownMenu>
    </Dropdown>
  );

  if (nestedMs || !tag) return dropdown;

  return (
    <span className="inline-flex min-w-0 max-w-full items-center gap-1">
      {dropdown}
      {tag}
    </span>
  );
}

export function BestPathChips({
  state,
  className = "",
}: {
  state?: BestPathState | null;
  className?: string;
}) {
  if (!state?.layers?.length) return null;

  return (
    <div
      className={`flex flex-wrap items-center justify-center gap-1 ${className}`}
    >
      {state.layers.map((layer) => (
        <BestPathLayerChip
          key={layer.layer}
          layer={layer}
          marginMs={state.marginMs}
        />
      ))}
    </div>
  );
}
