import type { ReactNode } from "react";

import { ChevronDown } from "lucide-react";

import { landingProtocolLabel, landingProtocolShortLabel } from "@/api/landing";
import {
  Dropdown,
  DropdownMenu,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";

// Plan 149: the one tinted chip of the 落地 pages. The bridge Chip renders
// solid colours (its bg-opacity-15 has no effect) or white bordered chips; the
// user wants neither: one consistent palette of tinted pills, readable in light
// and dark mode.
//
// Palette (keep it the same everywhere on 落地):
//   green  = online / 运行中 / 直连 / 剩余 > 7 天 / 长期有效
//   slate  = offline / 已停用 / disabled / neutral info
//   purple = 单向/双向 · 倍率, 规则 N, counts, 经规则转发, 妙妙屋共享
//   teal   = 出站 (强制 IPv4 …), other outbounds
//   pink   = 协议
//   cyan   = FLVX 自有落地 (same accent as the monitor tab)
//   amber  = warnings (≤ 7 天, 待升级, 同步中 …)
//   red    = errors / 封锁 / 已过期
export type LandingTone =
  | "green"
  | "slate"
  | "purple"
  | "teal"
  | "pink"
  | "cyan"
  | "amber"
  | "red";

export const landingToneClass: Record<LandingTone, string> = {
  green:
    "bg-green-500/15 text-green-700 dark:bg-green-400/15 dark:text-green-300",
  slate:
    "bg-slate-500/15 text-slate-600 dark:bg-slate-400/20 dark:text-slate-300",
  purple:
    "bg-purple-500/15 text-purple-700 dark:bg-purple-400/20 dark:text-purple-300",
  teal: "bg-teal-500/15 text-teal-700 dark:bg-teal-400/15 dark:text-teal-300",
  pink: "bg-pink-500/15 text-pink-700 dark:bg-pink-400/20 dark:text-pink-300",
  cyan: "bg-cyan-500/15 text-cyan-700 dark:bg-cyan-400/15 dark:text-cyan-300",
  amber:
    "bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300",
  red: "bg-red-500/15 text-red-700 dark:bg-red-400/20 dark:text-red-300",
};

const pillBase =
  "inline-flex max-w-full shrink-0 items-center gap-1 whitespace-nowrap rounded-full px-2 text-xs font-semibold leading-[18px]";

/** Bridge Chip colours mapped onto the palette (for shared colour tables). */
export function toneOf(
  color: "default" | "primary" | "secondary" | "success" | "warning" | "danger",
): LandingTone {
  switch (color) {
    case "success":
      return "green";
    case "warning":
      return "amber";
    case "danger":
      return "red";
    case "secondary":
      return "purple";
    case "primary":
      return "teal";
  }

  return "slate";
}

/** Rule action tone: 封锁 red, 直连 green, other outbounds teal, 插入时选择 purple. */
export function actionTone(action: string): LandingTone {
  if (action === "block") return "red";
  if (!action || action === "direct") return "green";
  if (action === "ask") return "purple";

  return "teal";
}

export function Pill({
  tone,
  children,
  className = "",
  icon,
  title,
}: {
  tone: LandingTone;
  children: ReactNode;
  className?: string;
  icon?: ReactNode;
  title?: string;
}) {
  return (
    <span
      className={`${pillBase} ${landingToneClass[tone]} ${className}`}
      data-landing-pill={tone}
      title={title}
    >
      {icon}
      <span className="min-w-0 truncate">{children}</span>
    </span>
  );
}

/**
 * Plan 149: all protocols of a server in ONE chip. One protocol shows its
 * name; more show 「SS2022 等 N 种」 (compact: 「协议 · N」). Tap / click opens the
 * full list (works on touch, uses only the data already loaded).
 */
export function ProtocolPill({
  protocols,
  compact = false,
}: {
  protocols: string[];
  compact?: boolean;
}) {
  if (!protocols.length) return null;
  const labels = protocols.map(landingProtocolLabel);
  const first = landingProtocolShortLabel(protocols[0]);
  const text =
    labels.length === 1
      ? first
      : compact
        ? `协议 · ${labels.length}`
        : `${first} 等 ${labels.length} 种`;

  return (
    <Dropdown>
      <DropdownTrigger>
        <button
          aria-label={`协议：${labels.join("、")}`}
          className={`${pillBase} ${landingToneClass.pink} cursor-pointer hover:brightness-95 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-pink-500 dark:hover:brightness-110`}
          data-landing-pill="pink"
          title={labels.join("、")}
          type="button"
        >
          <span className="min-w-0 truncate">{text}</span>
          <ChevronDown aria-hidden className="h-3 w-3 shrink-0" />
        </button>
      </DropdownTrigger>
      <DropdownMenu aria-label="协议">
        <div className="flvx-landing max-w-[80vw] space-y-1.5 p-2">
          <p className="text-xs text-default-500">
            支持的协议（{labels.length}）
          </p>
          <div className="flex flex-wrap gap-1">
            {labels.map((label) => (
              <Pill key={label} tone="pink">
                {label}
              </Pill>
            ))}
          </div>
        </div>
      </DropdownMenu>
    </Dropdown>
  );
}

type ChipColor =
  | "default"
  | "primary"
  | "secondary"
  | "success"
  | "warning"
  | "danger";

/**
 * Plan 149: drop-in for the bridge Chip on the 落地 admin pages (same props):
 * flat colours become their tinted tone, bordered / default chips tinted
 * slate. Text-colour overrides of the old white chips are dropped.
 */
export function TintChip({
  color = "default",
  variant,
  className = "",
  children,
  title,
}: {
  color?: ChipColor;
  variant?: "solid" | "flat" | "light" | "bordered";
  size?: "sm" | "md" | "lg";
  className?: string;
  children: ReactNode;
  title?: string;
}) {
  const tone = variant === "bordered" ? "slate" : toneOf(color);
  const kept = className
    .split(/\s+/)
    .filter((c) => c && !/^(dark:)?text-default-\d+$/.test(c))
    .join(" ");

  return (
    <Pill className={kept} title={title} tone={tone}>
      {children}
    </Pill>
  );
}
