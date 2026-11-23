import type { LandingTone } from "@/pages/landing/pill";
import type {
  LeiKwanEntryCaps,
  LeiKwanEntryLimit,
  LeiKwanHostList,
} from "@/api/leikwan-line";

import { Pill } from "@/pages/landing/pill";

// Plan 295: one place for a 利群入口's state. Offline (node.online false)
// wins over the last-known LeiKwan client snapshot: red dot + red 「离线」
// everywhere (tile, H5 row, drawer, grant dialog, user rows).
// Plan 301: an 入口机 row / tile has one status pill only — 「就绪」 (online,
// client running) or 「离线」 (same size and place); no 「主入口 N」 pill.

type HostItem = LeiKwanHostList["hosts"][number];

export const hostOnline = (h: HostItem) => !!h.node.online;

export const hostRunning = (h: HostItem) =>
  hostOnline(h) && h.node.leikwanStatus === "running";

export const hostStatusText = (h: HostItem) =>
  !hostOnline(h)
    ? "离线"
    : hostRunning(h)
      ? "运行中"
      : h.node.leikwanStatus || "未检测到";

export const hostStatusTone = (h: HostItem): LandingTone =>
  !hostOnline(h) ? "red" : hostRunning(h) ? "green" : "slate";

/** 「11-22 23:58」 in 台湾 time. */
export function twShort(ms?: number) {
  if (!ms) return "";
  const parts = new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Taipei",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).formatToParts(new Date(ms));
  const get = (t: string) => parts.find((p) => p.type === t)?.value || "";

  return `${get("month")}-${get("day")} ${get("hour")}:${get("minute")}`;
}

/** 「离线 · 自 11-22 23:58 台湾」 (or just 「离线」). */
export const offlineText = (since?: number) =>
  since ? `离线 · 自 ${twShort(since)} 台湾` : "离线";

/** Red 「离线」 pill; the since-when sits in the tooltip (or inline). */
export function OfflinePill({
  since,
  inline = false,
  label = "离线",
}: {
  since?: number;
  inline?: boolean;
  label?: string;
}) {
  return (
    <Pill title={offlineText(since)} tone="red" wrap={inline}>
      {inline && since ? `${label} · 自 ${twShort(since)}` : label}
    </Pill>
  );
}

// Plan 301: 首包过滤 stays on the hosts but is never shown (no pill, no 「无」).
const CAP_ITEMS: { key: keyof LeiKwanEntryCaps; label: string }[] = [
  { key: "warm", label: "预热池" },
  { key: "triggers", label: "快切触发" },
  { key: "phone", label: "手机备线" },
];

/** Plan 301: 手机本地's own list (it runs 日志快切 only from 1.0.10-panel). */
const PHONE_CAP_ITEMS: { key: keyof LeiKwanEntryCaps; label: string }[] = [
  { key: "warm", label: "预热池" },
  { key: "logswitch", label: "日志快切" },
  { key: "triggers", label: "快切触发" },
  { key: "phone", label: "备线" },
];

/** Keys of entry limits that are never rendered (plan 301). */
export const HIDDEN_LIMIT_KEYS = ["ssfilter"];

/** What a host can run: green = has it, slate 「无 …」 = missing. */
export function CapPills({
  caps,
  phone = false,
}: {
  caps?: LeiKwanEntryCaps;
  /** 手机本地 (plan 301: + 日志快切, 备线 = the phone dials it itself) */
  phone?: boolean;
}) {
  if (!caps) return null;
  if (!caps.known)
    return (
      <Pill title="这台还没采用主机现值，面板不知道它能跑什么" tone="slate">
        能力未知
      </Pill>
    );

  return (
    <>
      {(phone ? PHONE_CAP_ITEMS : CAP_ITEMS).map((c) =>
        caps[c.key] ? (
          <Pill key={c.key} tone="green">
            {c.label}
          </Pill>
        ) : (
          <Pill key={c.key} title={`${caps.name} 没有${c.label}`} tone="slate">
            无{c.label}
          </Pill>
        ),
      )}
    </>
  );
}

/** 「稳定版」 / 「测试版」 pill of the host's LeiKwan front. */
export function ClientPill({ caps }: { caps?: LeiKwanEntryCaps }) {
  if (!caps) return null;

  return (
    <Pill
      title={caps.frontBuild ? `利群客户端 ${caps.frontBuild}` : undefined}
      tone={caps.front === "test" ? "amber" : "slate"}
    >
      {caps.client}
    </Pill>
  );
}

/** Plan 295: the rule's entry limits, by key. */
export const limitOf = (limits: LeiKwanEntryLimit[] | undefined, key: string) =>
  (limits || []).find((l) => l.key === key);

/** One grey 「入口不支持 快切触发 · …」 pill of a rule (预热 is shown by the
 * warm chip itself); the tooltip names the entry and why. */
export function EntryLimitPills({
  limits,
  skip = ["warm"],
}: {
  limits?: LeiKwanEntryLimit[];
  skip?: string[];
}) {
  const list = (limits || []).filter(
    (l) => !skip.includes(l.key) && !HIDDEN_LIMIT_KEYS.includes(l.key),
  );

  if (!list.length) return null;

  return (
    <Pill
      wrap
      title={list.map((l) => `${l.label}：${l.why}`).join("\n")}
      tone="slate"
    >
      入口不支持 {list.map((l) => l.label).join(" · ")}
    </Pill>
  );
}

/** Plan 301: the one status pill of an 入口机 row / tile. */
export function HostStatusPill({ host }: { host: HostItem }) {
  if (!hostOnline(host)) return <OfflinePill since={host.node.offlineSince} />;
  if (hostRunning(host))
    return (
      <Pill title="在线，利群客户端在跑" tone="green">
        就绪
      </Pill>
    );

  return (
    <Pill title="节点在线，但没检测到利群客户端在跑" tone="slate">
      {hostStatusText(host)}
    </Pill>
  );
}
