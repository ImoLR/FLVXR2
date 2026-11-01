import { landingToneClass } from "@/pages/landing/pill";
import {
  latencyText,
  latencyTone,
  type LatencyTone,
} from "@/utils/latency-tone";

// Plan 184: the 最优 latency as its own small coloured tag (green < 100 ms,
// amber 100–180 ms, red > 180 ms; utils/latency-tone.ts), used on the rules
// page pick line + its dropdown rows and on the tunnel page 最优 chips.
// 「含落地」 rides inside the tag.
//
// A build-time choice, not a user setting (like TUNNEL_REGION_COLORS):
//   "sibling" (A) — a separate chip right after the route pill, same line,
//             wraps together with it on phones (kept as the dormant option);
//   "nested"  (B, picked by the user 2026-11-01) — the chip sits inside the
//             route pill with its own background.
export const LATENCY_TAG_STYLE = "nested" as "sibling" | "nested";

type LatencyTagSize = "pick" | "chip" | "row";

// Same height as the pill next to it: rules-page pick pill (11px / 17px),
// tunnel-page 最优 chip (12px / 18px), dropdown detail row.
const sizeClass: Record<LatencyTagSize, string> = {
  pick: "rounded-lg px-1.5 py-px text-[11px] leading-[17px]",
  chip: "rounded-full px-2 text-xs leading-[18px]",
  row: "rounded-md px-1.5 text-[11px] leading-4",
};

// Nested (B): a slightly stronger tint + hairline ring so the tag stands out
// on the pill's own tint (green on the green tunnel chip).
const nestedClass: Record<LatencyTone, string> = {
  green:
    "bg-green-500/25 ring-1 ring-inset ring-green-600/30 dark:bg-green-400/25 dark:ring-green-300/30",
  amber:
    "bg-amber-500/25 ring-1 ring-inset ring-amber-600/30 dark:bg-amber-400/25 dark:ring-amber-300/30",
  red: "bg-red-500/25 ring-1 ring-inset ring-red-600/30 dark:bg-red-400/25 dark:ring-red-300/30",
};

export function LatencyTag({
  ms,
  withTarget = false,
  size = "pick",
  nested = false,
  className = "",
}: {
  ms?: number;
  withTarget?: boolean;
  size?: LatencyTagSize;
  nested?: boolean;
  className?: string;
}) {
  const tone = latencyTone(ms);

  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 whitespace-nowrap font-semibold ${sizeClass[size]} ${landingToneClass[tone]} ${nested ? nestedClass[tone] : ""} ${className}`}
      data-latency-tone={tone}
    >
      {latencyText(ms)}
      {withTarget && (
        <span className="rounded-full bg-white/60 px-1 text-[10px] font-medium leading-[14px] dark:bg-black/20">
          含落地
        </span>
      )}
    </span>
  );
}
