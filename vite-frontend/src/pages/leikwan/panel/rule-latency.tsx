import type { LeiKwanRule, LeiKwanRuleLatency } from "@/api/leikwan-line";

import { MsTag } from "@/components/latency-tag";
import { LkField } from "@/pages/leikwan/panel/layout-parts";
import { latencyText, latencyTone } from "@/utils/latency-tone";

// Plan 310: the 全程 latency tag of a 利群 rule row (replaces 「利群 #id」):
// 入口机 → 利群专线出口, the LeiKwan client's own measurement from the entry's
// latest host report (NOT to the 落地). Colours = the shared latency scale
// (utils/latency-tone.ts); no value = grey 「— ms」 with the reason on hover.
// 手机本地 rows (the phone is the entry) show no tag: the App bridge has no
// cheap call for the phone client's 全程 (its `diag` makes TCP handshakes).

const ageText = (s: number) =>
  s < 60
    ? `${Math.max(0, Math.round(s))} 秒前`
    : `${Math.round(s / 60)} 分钟前`;

const srcText = (l: LeiKwanRuleLatency) =>
  l.src === "udp" ? "UDP 探测" : "TCP 最近一次";

/** The hover text of a rule's tag (the forward number lives only here). */
export function ruleLatencyTitle(r: LeiKwanRule): string {
  const entry = r.entry?.nodeName ? `入口机 ${r.entry.nodeName}` : "入口机";
  const tail = [
    r.forwardId ? `利群 #${r.forwardId}` : "",
    r.source === "import" ? "导入时登记的利群转发" : "",
  ].filter(Boolean);
  const l = r.latency;

  if (!l)
    return [
      `全程（${entry} → 利群专线出口）暂无：${r.latencyNote || "还没有数据"}`,
      ...tail,
    ].join(" · ");

  return [
    `全程：${entry} → 利群专线出口，利群客户端实测（${srcText(l)}），面板约每 2 分钟更新`,
    ageText(l.ageS),
    ...tail,
  ].join(" · ");
}

/** 「62 ms」 / grey 「— ms」 — same height as the row's pills. */
export function RuleLatencyTag({ rule: r }: { rule: LeiKwanRule }) {
  const l = r.latency;

  return (
    <span
      className="inline-flex shrink-0"
      data-leikwan-latency={l ? latencyTone(l.ms) : "none"}
      title={ruleLatencyTitle(r)}
    >
      <MsTag
        size="chip"
        text={l ? latencyText(l.ms) : "— ms"}
        tone={l ? latencyTone(l.ms) : "slate"}
      />
    </span>
  );
}

/** The 专线 header tag: the lowest current value among its rules. */
export function LineLatencyTag({
  rules,
  phoneLocal,
}: {
  rules: LeiKwanRule[];
  /** 手机本地: the rows show no entry value, so neither does the header */
  phoneLocal?: boolean;
}) {
  if (phoneLocal) return null;
  const withValue = rules.filter((r) => r.latency);

  if (!withValue.length) return null;
  const best = withValue.reduce((a, b) =>
    b.latency!.ms < a.latency!.ms ? b : a,
  );
  const l = best.latency!;
  const entry = best.entry?.nodeName
    ? `入口机 ${best.entry.nodeName}`
    : "入口机";
  const title = [
    `本专线最低全程 ${latencyText(l.ms)}：${best.name}（${entry} → 利群专线出口，利群客户端实测）`,
    `${rules.length} 条转发里 ${withValue.length} 条有数据`,
    ageText(l.ageS),
  ].join(" · ");

  return (
    <span
      className="inline-flex shrink-0"
      data-leikwan-line-latency={latencyTone(l.ms)}
      title={title}
    >
      <MsTag size="chip" text={latencyText(l.ms)} tone={latencyTone(l.ms)} />
    </span>
  );
}

/** H5 open card: the tag + what it measures / why there is none (touch
 * screens have no hover text). */
export function RuleLatencyField({ rule: r }: { rule: LeiKwanRule }) {
  const l = r.latency;

  return (
    <LkField label="延迟">
      <RuleLatencyTag rule={r} />
      <span className="break-normal text-xs text-default-500">
        {l
          ? `入口机 → 利群专线出口，利群客户端实测 · ${ageText(l.ageS)}`
          : r.latencyNote || "还没有数据"}
      </span>
    </LkField>
  );
}
