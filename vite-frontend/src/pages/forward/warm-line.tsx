import { WarmPoolTag, type WarmPoolState } from "@/pages/tunnel/warm-pool-tag";

// Plan 249: the 「预热」 tag of a rule — its own small line under the 最优
// line (layout B, as on the tunnel page), or where that line would be when
// the rule has none. Same pill as the tunnel page at the rules-page 最优 size.
// Plan 285: a normal user gets the same tag and dropdown as an admin on their
// own rules (green / red / 不支持 / 预热中 · 数据不足, hop rows); the backend
// only relabels an unmapped next-hop node address as 「下一跳」 for users.
export function RuleWarmPoolLine({
  state,
  className = "",
}: {
  state?: WarmPoolState | null;
  className?: string;
}) {
  if (!state) return null;

  return (
    <div
      className={`mt-1 flex min-w-0 max-w-full ${className}`}
      data-warm-pool-placement="rule-line"
    >
      <WarmPoolTag size="pick" state={state} />
    </div>
  );
}
