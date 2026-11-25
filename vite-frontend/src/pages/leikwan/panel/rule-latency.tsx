import type {
  LeiKwanLineEndIn,
  LeiKwanRule,
  LeiKwanRuleLatency,
} from "@/api/leikwan-line";
import type { PhoneRuleView } from "@/pages/leikwan/panel/phone-entry";
import type { PhoneForward } from "@/utils/app-bridge";

import { MsTag } from "@/components/latency-tag";
import { Pill } from "@/pages/landing/pill";
import { LkField } from "@/pages/leikwan/panel/layout-parts";
import { latencyText, latencyTone } from "@/utils/latency-tone";

// Plan 310 r3 (user 11-26): the rule row tag is 「到落地」 = 全程 (入口机 →
// 利群专线出口, the LeiKwan client's UDP probe, from the entry's report) + 专线
// 出口 → 落地 (the landing pings the exit once a minute). No landing value
// (手动落地 / 外部节点 / landing offline / no exit known) or no UDP 全程 → NO
// tag (「没有落地的就不用显示」). The forward number lives only in the admin
// hover text. 手机本地 rows (App): 本机 → 落地 = the phone client's own 全程
// (from the App's state, when the App reports it — no handshake) + the same
// 出口 → 落地. The 专线 header shows 「端内」 (全程 − 入口段, amber like 诊断).

/** 「12 秒前」 / 「5 分钟前」 / 「3 小时前」 / 「2 天前」 (floored). */
const ageText = (s: number) => {
  const v = Math.max(0, Math.floor(s));

  if (v < 60) return `${v} 秒前`;
  if (v < 3600) return `${Math.floor(v / 60)} 分钟前`;
  if (v < 86400) return `${Math.floor(v / 3600)} 小时前`;

  return `${Math.floor(v / 86400)} 天前`;
};

/** 「0.4」 / 「4.7」 under 10 ms (one decimal), 「19」 above (like 诊断). */
const fineMs = (v: number) =>
  v < 9.95 ? String(Math.round(v * 10) / 10) : String(Math.round(v));

const landSrc = (approx?: boolean) =>
  approx
    ? "落地机连接延迟；出口不回 ping，用连接延迟，可能偏高"
    : "落地机 ping，每分钟";

/** The phone's own 全程 from the App state (an App that reports it). */
const phoneTotal = (f?: PhoneForward | null) => {
  const t = f?.total;

  if (!t || t.source !== "udp" || !(t.rttMs > 0) || t.rttMs > 2000) return null;

  return t;
};

/** The `phone` prop of a row: undefined = server row; 手机本地 → its ready
 * forward (false = not ready / not on the phone → no tag). */
export const phoneArg = (pv: PhoneRuleView | null) =>
  pv ? (pv.status ? false : (pv.forward ?? false)) : undefined;

/** What a row shows: server rows r.latency, 手机本地 rows phone + landing. */
function rowValue(r: LeiKwanRule, phone?: PhoneForward | null | false) {
  if (phone === undefined) {
    const l = r.latency;

    return l ? { l, local: false } : null;
  }
  const t = phoneTotal(phone || null);
  const land = r.landLatency;

  if (!t || !land) return null;
  const l: LeiKwanRuleLatency = {
    ms: Math.round((t.rttMs + land.ms) * 10) / 10,
    src: "udp",
    ageS: t.ageS,
    totalMs: t.rttMs,
    landMs: land.ms,
    landAgeS: land.ageS,
    approx: land.approx,
  };

  return { l, local: true };
}

/** The hover text of a row's tag (the forward number lives only here). */
export function ruleLatencyTitle(
  r: LeiKwanRule,
  l: LeiKwanRuleLatency,
  local = false,
): string {
  const from = local
    ? "本机"
    : r.entry?.nodeName
      ? `入口机 ${r.entry.nodeName}`
      : "入口机";
  const head = `${local ? "本机 → 落地" : "到落地"} ${l.approx ? "≈ " : ""}${latencyText(l.ms)}`;
  const parts = `${from} → 利群专线出口 ${fineMs(l.totalMs)} ms（UDP，${local ? "手机上的利群客户端" : "利群客户端"}选线时测的 · ${ageText(l.ageS)}）+ 专线出口 → 落地 ${fineMs(l.landMs)} ms（${landSrc(l.approx)}）`;
  const tail = [
    r.forwardId ? `利群 #${r.forwardId}` : "",
    r.source === "import" ? "导入时登记的利群转发" : "",
  ].filter(Boolean);

  return [`${head} = ${parts}`, ...tail].join(" · ");
}

/**
 * 「24 ms」 (「≈ 35 ms」 when the exit does not answer ping) — nothing when
 * there is no landing value. phone: the 手机本地 forward (false = 手机本地
 * without this forward) → 本机 → 落地; undefined = a server row.
 */
export function RuleLatencyTag({
  rule: r,
  phone,
}: {
  rule: LeiKwanRule;
  phone?: PhoneForward | null | false;
}) {
  const v = rowValue(r, phone);

  if (!v) return null;
  const { l, local } = v;
  const tone = latencyTone(l.ms);

  return (
    <span
      className="inline-flex shrink-0"
      data-leikwan-latency={tone}
      data-leikwan-latency-local={local ? "" : undefined}
      title={ruleLatencyTitle(r, l, local)}
    >
      <MsTag
        size="chip"
        text={`${l.approx ? "≈ " : ""}${latencyText(l.ms)}`}
        tone={tone}
      />
    </span>
  );
}

/** H5 open card: the tag + its parts (touch screens have no hover text);
 * nothing when there is no value. */
export function RuleLatencyField({
  rule: r,
  phone,
}: {
  rule: LeiKwanRule;
  phone?: PhoneForward | null | false;
}) {
  const v = rowValue(r, phone);

  if (!v) return null;
  const { l, local } = v;

  return (
    <LkField label="到落地">
      <RuleLatencyTag phone={phone} rule={r} />
      <span className="break-normal text-xs text-default-500">
        {`${local ? "本机" : "入口机"} → 利群专线出口 ${fineMs(l.totalMs)} ms（UDP · ${ageText(l.ageS)}）+ 专线出口 → 落地 ${fineMs(l.landMs)} ms（${l.approx ? "出口不回 ping，用连接延迟，可能偏高" : "落地机 ping，每分钟"}）`}
      </span>
    </LkField>
  );
}

/** 专线 header: 「端内 X ms」 (amber like 诊断) — nothing without a reading. */
export function LineEndInTag({
  endIn: e,
}: {
  endIn?: LeiKwanLineEndIn | null;
}) {
  if (!e) return null;
  const title = [
    `端内 ${fineMs(e.ms)} ms = 全程 ${fineMs(e.totalMs)} ms − 入口段 ${fineMs(e.segMs)} ms（利群专线入口 → 专线出口）`,
    `入口机 ${e.nodeName}`,
    e.ruleName
      ? `${e.ruleName}${e.forwardId ? `（利群 #${e.forwardId}）` : ""}`
      : "",
    e.ingress ? `入口 ${e.ingress}` : "",
    `全程 ${ageText(e.ageS)}测的（UDP）`,
    "入口段 = 入口机到这个入口已有连接的内核延迟，每分钟读",
    e.readings > 1 ? `这条专线现在有 ${e.readings} 个读数，显示其中一个` : "",
  ].filter(Boolean);

  return (
    <span
      className="inline-flex shrink-0"
      data-leikwan-line-endin=""
      title={title.join(" · ")}
    >
      <Pill tone="amber">端内 {fineMs(e.ms)} ms</Pill>
    </span>
  );
}
