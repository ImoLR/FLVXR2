// Plan 184: colour of a 最优 latency (rules page pick line, tunnel page 最优
// chips). The user picked the thresholds: < 100 ms green, 100–180 ms amber,
// > 180 ms red. A range (38–52 ms) is coloured by its larger number, and the
// tone follows the number shown (rounded), so 「100 ms」 is never green.
export const LATENCY_GREEN_BELOW_MS = 100;
export const LATENCY_RED_ABOVE_MS = 180;

export type LatencyTone = "green" | "amber" | "red";

// Same-room paths are sub-millisecond: 「<1 ms」, never 「0 ms」.
export const latencyNumber = (v?: number) =>
  (v ?? 0) < 1 ? "<1" : String(Math.round(v ?? 0));

/** 「38 ms」 / 「38–52 ms」 (max missing / 0 or equal after rounding = single). */
export function latencyText(minMs?: number, maxMs?: number): string {
  const min = latencyNumber(minMs);

  if (!maxMs) return `${min} ms`;
  const max = latencyNumber(maxMs);

  return max === min ? `${min} ms` : `${min}–${max} ms`;
}

export function latencyTone(minMs?: number, maxMs?: number): LatencyTone {
  const shown = Math.round(Math.max(minMs ?? 0, maxMs ?? 0));

  if (shown < LATENCY_GREEN_BELOW_MS) return "green";
  if (shown <= LATENCY_RED_ABOVE_MS) return "amber";

  return "red";
}
