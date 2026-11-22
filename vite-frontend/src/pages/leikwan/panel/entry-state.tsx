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

const CAP_ITEMS: { key: keyof LeiKwanEntryCaps; label: string }[] = [
  { key: "warm", label: "预热池" },
  { key: "ssFilter", label: "首包过滤" },
  { key: "triggers", label: "快切触发" },
  { key: "phone", label: "手机备线" },
];

/** What a host can run: green = has it, slate 「无 …」 = missing. */
export function CapPills({ caps }: { caps?: LeiKwanEntryCaps }) {
  if (!caps) return null;
  if (!caps.known)
    return (
      <Pill title="这台还没采用主机现值，面板不知道它能跑什么" tone="slate">
        能力未知
      </Pill>
    );

  return (
    <>
      {CAP_ITEMS.map((c) =>
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

/** One grey 「入口不支持 首包过滤 · 快切触发 …」 pill of a rule (预热 is
 * shown by the warm chip itself); the tooltip names the entry and why. */
export function EntryLimitPills({
  limits,
  skip = ["warm"],
}: {
  limits?: LeiKwanEntryLimit[];
  skip?: string[];
}) {
  const list = (limits || []).filter((l) => !skip.includes(l.key));

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
