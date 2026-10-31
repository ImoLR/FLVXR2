import { ChevronDown } from "lucide-react";

import { landingToneClass, type LandingTone } from "@/pages/landing/pill";
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
const num = (v?: number) => ((v ?? 0) < 1 ? "<1" : String(Math.round(v ?? 0)));
const ms = (v?: number) => `${num(v)} ms`;

const layerMs = (layer: BestPathLayer) => {
  if (layer.status !== "applied") return "";
  const min = num(layer.minMs);
  const max = num(layer.maxMs);

  return min === max ? ` · ${min} ms` : ` · ${min}–${max} ms`;
};

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

  return {
    layers: layers.map((layer: BestPathLayer) => ({
      ...layer,
      items: Array.isArray(layer.items) ? layer.items : [],
    })),
  };
};

function BestPathLayerChip({ layer }: { layer: BestPathLayer }) {
  const tone = statusTone[layer.status] ?? "slate";
  const text = `${layer.label}最优：${layer.summary || "等待探测"}${layerMs(layer)}`;

  return (
    <Dropdown>
      <DropdownTrigger>
        <button
          aria-label={`${text}，点按查看详情`}
          className={`${pillBase} ${landingToneClass[tone]} cursor-pointer hover:brightness-95 focus-visible:outline-2 focus-visible:outline-offset-1 dark:hover:brightness-110`}
          data-best-path-chip={layer.status}
          type="button"
        >
          <span className="min-w-0 truncate">{text}</span>
          <ChevronDown aria-hidden className="h-3 w-3 shrink-0" />
        </button>
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
                  <span className="break-all text-green-700 dark:text-green-300">
                    {item.bestName} · {ms(item.costMs)}
                  </span>
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
                    更快的不到 5 ms 或还没连续 2 分钟，先不换
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
        <BestPathLayerChip key={layer.layer} layer={layer} />
      ))}
    </div>
  );
}
