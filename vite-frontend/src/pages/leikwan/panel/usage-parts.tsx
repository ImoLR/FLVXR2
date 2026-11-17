import type { ReactNode } from "react";
import type {
  LeiKwanDevice,
  LeiKwanRule,
  LeiKwanUsageStatus,
  LeiKwanUsageView,
} from "@/api/leikwan-line";

import { useExpandOne } from "@/components/expand-row";
import { Pill } from "@/pages/landing/pill";
import {
  LkCard,
  LkCardTitle,
  LkExpandCard,
  LkField,
} from "@/pages/leikwan/panel/layout-parts";
import { fmtBytes } from "@/pages/leikwan/panel/shared";

// Plan 273 利群自己的流量: the vendor's billing numbers, read by the panel
// every 5 min (read-only) — the 「套餐流量」 card, the per-rule 「利群计费」
// chip (kept apart from our own 利群流量 / 备线 chips) and the read-only
// 「利群在线设备」 list. Times are shown in 台湾时间.

const TW = "Asia/Taipei";
const DAY = 86_400_000;

const twParts = (ms: number) => {
  const p = new Intl.DateTimeFormat("en-CA", {
    timeZone: TW,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).formatToParts(new Date(ms));
  const get = (t: string) => p.find((x) => x.type === t)?.value || "";

  return {
    y: get("year"),
    md: `${get("month")}-${get("day")}`,
    hm: `${get("hour") === "24" ? "00" : get("hour")}:${get("minute")}`,
  };
};

/** 「11-04 20:08」 (台湾). */
export const twShort = (ms?: number | null) => {
  if (!ms) return "—";
  const p = twParts(ms);

  return `${p.md} ${p.hm}`;
};
/** 「2026-12-08 07:59」 (台湾). */
export const twFull = (ms?: number | null) => {
  if (!ms) return "—";
  const p = twParts(ms);

  return `${p.y}-${p.md} ${p.hm}`;
};
/** 「19:47」 today, else 「11-16 19:47」 (台湾). */
export const twWhen = (ms?: number | null) => {
  if (!ms) return "—";
  const p = twParts(ms);

  return p.md === twParts(Date.now()).md ? p.hm : `${p.md} ${p.hm}`;
};

const agoText = (ms: number) => {
  const m = Math.max(0, Math.round((Date.now() - ms) / 60_000));

  if (m < 1) return "刚刚";
  if (m < 60) return `${m} 分钟前`;
  if (m < 48 * 60) return `${Math.round(m / 60)} 小时前`;

  return `${Math.round(m / 1440)} 天前`;
};

const pctText = (p?: number | null) =>
  p === null || p === undefined
    ? ""
    : `${p < 10 ? p.toFixed(2) : p.toFixed(1)}%`;

/** 「更新于 19:47」 / 「数据旧了」 / 「读取失败」 / 「等第一次读取」. */
export function UsageStatusPill({
  status,
  compact = false,
}: {
  status?: LeiKwanUsageStatus;
  compact?: boolean;
}) {
  if (!status || (status.pending && !status.error))
    return (
      <Pill title="面板启动后约 1 分钟读第一次，之后每 5 分钟一次" tone="slate">
        等第一次读取
      </Pill>
    );
  const at = status.fetchedAt
    ? `显示的是 ${twWhen(status.fetchedAt)}（台湾）读到的数`
    : "还没读到过";
  // Old / failed: the last good read's time stays visible beside the chip.
  const last = status.fetchedAt ? (
    <Pill title={at} tone="slate">
      更新于 {twWhen(status.fetchedAt)}
    </Pill>
  ) : null;

  if (status.error)
    return (
      <>
        {last}
        <Pill
          title={`利群那边最近一次没读到：${status.error}\n${at}；面板会自己隔一会儿再读，不用处理`}
          tone="red"
        >
          {compact ? "读取失败" : `读取失败 · ${twWhen(status.checkedAt)}`}
        </Pill>
      </>
    );
  if (status.stale)
    return (
      <>
        {last}
        <Pill title={at} tone="amber">
          数据旧了 · {agoText(status.fetchedAt)}
        </Pill>
      </>
    );

  return (
    <Pill
      title={`台湾时间 ${twFull(status.fetchedAt)} 读到；每 5 分钟读一次`}
      tone="green"
    >
      更新于 {twWhen(status.fetchedAt)}
    </Pill>
  );
}

/** Rule row chip: the vendor's bytes of this forward in the current cycle. */
export function VendorFlowPill({ rule }: { rule: LeiKwanRule }) {
  const v = rule.vendorFlow;

  if (!v) return null;
  const admin = v.share !== undefined;
  const lines = [
    `利群计费流量：利群后台本周期（${twShort(v.cycleStart)} → ${twShort(v.cycleEnd)}，台湾时间）给这条转发记的流量`,
    `入 ${fmtBytes(v.in)} · 出 ${fmtBytes(v.out)}${admin && v.extra ? ` · 额外计费 ${fmtBytes(v.extra)}` : ""}${admin ? ` · 占套餐已用 ${pctText(v.share)}` : ""}`,
    "和面板自己统计的利群流量、备线流量分开算，不合并",
    v.stale
      ? `利群那边暂时没读到新数，显示的是 ${twWhen(v.updatedAt)} 的数${admin && v.error ? `（${v.error}）` : ""}`
      : `更新于 ${twWhen(v.updatedAt)}，每 5 分钟读一次`,
  ];

  return (
    <Pill title={lines.join("\n")} tone="cyan">
      利群计费 {fmtBytes(v.total)}
      {v.stale ? " · 旧" : ""}
    </Pill>
  );
}

const barTone = (p: number) =>
  p >= 90 ? "bg-red-500" : p >= 70 ? "bg-amber-500" : "bg-cyan-500";

function UsageBar({ percent }: { percent: number }) {
  const w = Math.max(0, Math.min(100, percent));

  return (
    <div
      aria-label={`已用 ${pctText(percent)}`}
      className="h-2 w-full overflow-hidden rounded-full bg-default-200 dark:bg-default-100"
      role="img"
    >
      <div
        className={`h-full rounded-full ${barTone(w)}`}
        style={{ width: w > 0 ? `max(${w}%, 4px)` : 0 }}
      />
    </div>
  );
}

/** Label / value line of the desktop card. */
function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex gap-3 text-[13px] leading-6">
      <span className="w-14 shrink-0 text-default-500">{label}</span>
      <div className="min-w-0 flex-1 break-words">{children}</div>
    </div>
  );
}

const daysLeft = (ms?: number | null) =>
  ms ? Math.ceil((ms - Date.now()) / DAY) : null;

/** The card body (desktop card and H5 block share it). */
function PackageBody({ data }: { data: LeiKwanUsageView }) {
  const u = data.usage;
  const s = data.service;
  const exp = daysLeft(s?.expiresAt);
  const reset = daysLeft(u?.cycleEnd);
  const pct =
    u?.usedPercent ??
    (u && u.includedBytes > 0 ? (u.usedBytes * 100) / u.includedBytes : null);

  if (!u)
    return (
      <p className="text-sm text-default-500">
        {data.usageStatus?.error
          ? `还没读到利群的流量：${data.usageStatus.error}`
          : "还没读到利群的流量，面板会自己读（每 5 分钟一次）"}
      </p>
    );

  return (
    <div className="space-y-2.5">
      {s?.planName && (
        <div className="break-words text-xs text-default-500">{s.planName}</div>
      )}
      <div className="flex items-baseline justify-between gap-2">
        <div className="min-w-0">
          <span className="text-xl font-bold">{fmtBytes(u.usedBytes)}</span>
          <span className="text-sm text-default-500">
            {" "}
            / {u.includedBytes > 0 ? fmtBytes(u.includedBytes) : "不限量"}
          </span>
        </div>
        {pct !== null && (
          <span className="shrink-0 text-sm font-semibold text-cyan-700 dark:text-cyan-300">
            {pctText(pct)}
          </span>
        )}
      </div>
      {pct !== null && <UsageBar percent={pct} />}
      <div className="space-y-0.5">
        {u.remainingBytes !== null && (
          <Row label="剩余">{fmtBytes(u.remainingBytes)}</Row>
        )}
        <Row label="周期">
          {twShort(u.cycleStart)} → {twShort(u.cycleEnd)}
          {reset !== null && reset >= 0 && (
            <span className="text-default-500">（{reset} 天后重置）</span>
          )}
        </Row>
        {s && (
          <Row label="到期">
            <span
              title={`台湾时间 ${twFull(s.expiresAt)}；利群后台写的是 ${s.expiresAtRaw}（UTC）`}
            >
              {twShort(s.expiresAt)}
            </span>
            {exp !== null && (
              <span
                className={
                  exp <= 7
                    ? "text-amber-700 dark:text-amber-300"
                    : "text-default-500"
                }
              >
                （{exp >= 0 ? `还有 ${exp} 天` : "已到期"}）
              </span>
            )}
          </Row>
        )}
        {data.previous && (
          <Row label="上周期">
            {fmtBytes(data.previous.usedBytes)}
            <span className="text-default-500">
              （{twShort(data.previous.cycleStart).slice(0, 5)} →{" "}
              {twShort(data.previous.cycleEnd).slice(0, 5)}）
            </span>
          </Row>
        )}
        {(u.otherBytes > 0 || u.unattributedBytes > 0) && (
          <Row label="不在规则">
            <span
              title={`利群本周期记了流量、但不是现在任何一条利群规则的转发${
                u.otherForwards.length
                  ? `（${u.otherForwards.map((id) => `#${id}`).join("、")}，多半是本周期删掉的）`
                  : ""
              }${u.unattributedBytes > 0 ? `；另有 ${fmtBytes(u.unattributedBytes)} 利群没归到任何转发` : ""}`}
            >
              {fmtBytes(u.otherBytes + u.unattributedBytes)}
              {u.otherForwards.length > 0 && (
                <span className="text-default-500">
                  {" "}
                  · {u.otherForwards.map((id) => `#${id}`).join(" ")}
                </span>
              )}
            </span>
          </Row>
        )}
        {u.billableBytes > 0 && (
          <Row label="超额">
            <span className="text-red-700 dark:text-red-300">
              {fmtBytes(u.billableBytes)} 要另外付费
            </span>
          </Row>
        )}
      </div>
    </div>
  );
}

/** Admin desktop: right column, above 授权. */
export function PackageCard({ data }: { data: LeiKwanUsageView | null }) {
  if (!data?.accountReady) return null;

  return (
    <LkCard className="space-y-3 p-4" data-leikwan-usage="">
      <LkCardTitle
        chip={<UsageStatusPill compact status={data.usageStatus} />}
        title="套餐流量"
      />
      <PackageBody data={data} />
      <p className="text-xs leading-relaxed text-default-500">
        利群后台的计费数，面板每 5 分钟读一次（只读）。时间都是台湾时间。
      </p>
    </LkCard>
  );
}

/** Admin H5: one ＋/− block under the header chips. */
export function PackagePhoneBlock({
  data,
  open,
  onToggle,
}: {
  data: LeiKwanUsageView | null;
  open: boolean;
  onToggle: () => void;
}) {
  if (!data?.accountReady) return null;
  const u = data.usage;

  return (
    <LkExpandCard
      chip={
        u ? (
          <Pill
            tone={
              (u.usedPercent || 0) >= 90
                ? "red"
                : data.usageStatus?.error
                  ? "red"
                  : data.usageStatus?.stale
                    ? "amber"
                    : "cyan"
            }
          >
            {fmtBytes(u.usedBytes)} /{" "}
            {u.includedBytes > 0 ? fmtBytes(u.includedBytes) : "不限"}
          </Pill>
        ) : (
          <UsageStatusPill compact status={data.usageStatus} />
        )
      }
      data-leikwan-usage="h5"
      name="套餐流量"
      open={open}
      onToggle={onToggle}
    >
      <div className="flex flex-wrap gap-1">
        <UsageStatusPill compact status={data.usageStatus} />
      </div>
      <PackageBody data={data} />
    </LkExpandCard>
  );
}

const platformText = (d: LeiKwanDevice) => {
  const p = (d.platform || "").toLowerCase();

  if (p.startsWith("android")) return "Android";
  if (p.startsWith("ios")) return "iOS";
  if (p.startsWith("windows")) return "Windows";
  if (p.startsWith("mac")) return "macOS";
  if (p.startsWith("linux") || /client-agent/i.test(d.name)) return "Linux";

  return d.platform || "未知";
};

const deviceTitle = (d: LeiKwanDevice) => d.nodeName || d.name || d.deviceId;

function DeviceState({ d }: { d: LeiKwanDevice }) {
  if (d.state === "blocked" || d.state === "banned" || d.state === "kicked")
    return (
      <Pill
        title={
          d.blockedUntil ? `封到 ${twFull(d.blockedUntil)}（台湾）` : undefined
        }
        tone="red"
      >
        {d.state === "banned" ? "已封禁" : "已踢下线"}
      </Pill>
    );

  return d.online ? (
    <Pill tone="green">在线</Pill>
  ) : (
    <Pill tone="slate">离线</Pill>
  );
}

function DeviceDot({ d }: { d: LeiKwanDevice }) {
  const tone = d.online
    ? "bg-green-500"
    : d.state === "offline"
      ? "bg-slate-400"
      : "bg-red-500";

  return <span className={`h-2 w-2 shrink-0 rounded-full ${tone}`} />;
}

const devicesChip = (data: LeiKwanUsageView) => {
  const d = data.devices;

  if (!d) return <UsageStatusPill compact status={data.devicesStatus} />;
  const full = d.limit > 0 && d.slots <= 0;

  return (
    <Pill
      title={
        full
          ? "在线设备已满：再上线一台会挤不进去，先在利群后台踢掉不用的"
          : `还能再上线 ${d.slots} 台`
      }
      tone={full ? "amber" : "green"}
    >
      {d.count} / {d.limit} 在线{full ? " · 满" : ""}
    </Pill>
  );
};

/** Admin desktop: right column, under 入口机. Read-only. */
export function DevicesCard({ data }: { data: LeiKwanUsageView | null }) {
  if (!data?.accountReady) return null;
  const items = data.devices?.items || [];

  return (
    <LkCard className="space-y-3 p-4" data-leikwan-devices="">
      <LkCardTitle chip={devicesChip(data)} title="利群在线设备" />
      <div className="space-y-1.5">
        {items.map((d) => (
          <div
            key={`${d.clientId}:${d.deviceId}`}
            className={`space-y-1 rounded-xl border border-divider px-2.5 py-2 ${
              d.online ? "bg-default-50/60 dark:bg-default-50/30" : "opacity-70"
            }`}
            data-leikwan-device={deviceTitle(d)}
          >
            <div className="flex min-w-0 items-center gap-1.5">
              <DeviceDot d={d} />
              <span
                className="min-w-0 flex-1 break-words text-sm font-semibold leading-5"
                title={d.nodeName ? d.name : undefined}
              >
                {deviceTitle(d)}
              </span>
              <Pill
                title={d.osVersion ? `系统 ${d.osVersion}` : undefined}
                tone={d.nodeName ? "teal" : "purple"}
              >
                {platformText(d)}
              </Pill>
            </div>
            <div className="break-all pl-3.5 font-mono text-[11px] leading-4 text-default-500">
              {d.ip || "—"}
            </div>
            <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 pl-3.5 text-[11px] text-default-500">
              {d.clientBuild && <span>版本 {d.clientBuild}</span>}
              <span>
                {d.online ? "最后报到" : "最后在线"} {twWhen(d.lastSeenAt)}
              </span>
              {!d.online && <DeviceState d={d} />}
            </div>
          </div>
        ))}
        {items.length === 0 && (
          <p className="text-sm text-default-500">
            {data.devicesStatus?.error
              ? `还没读到设备：${data.devicesStatus.error}`
              : "还没读到设备列表"}
          </p>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        {data.devices && (
          <UsageStatusPill compact status={data.devicesStatus} />
        )}
        <span className="text-xs leading-relaxed text-default-500">
          只读。踢下线、恢复去利群后台。在线 = 最近{" "}
          {data.devices?.windowSec || 180} 秒报到过。
        </span>
      </div>
    </LkCard>
  );
}

/** Admin H5 tab 「设备」: ＋/− rows, one open. */
export function DevicesPhoneList({ data }: { data: LeiKwanUsageView | null }) {
  const items = data?.devices?.items || [];
  const expand = useExpandOne(items.map((d) => `${d.clientId}:${d.deviceId}`));

  if (!data?.accountReady) return null;

  return (
    <div className="space-y-3 pt-2" data-leikwan-devices="h5">
      <div className="flex flex-wrap items-center gap-2 px-0.5">
        <h3 className="text-[15px] font-semibold">利群在线设备</h3>
        {devicesChip(data)}
        {data.devices && (
          <UsageStatusPill compact status={data.devicesStatus} />
        )}
      </div>
      {items.map((d) => {
        const key = `${d.clientId}:${d.deviceId}`;

        return (
          <LkExpandCard
            key={key}
            chip={
              <Pill tone={d.nodeName ? "teal" : "purple"}>
                {platformText(d)}
              </Pill>
            }
            className={d.online ? "" : "opacity-75"}
            data-leikwan-device={deviceTitle(d)}
            dot={<DeviceDot d={d} />}
            name={deviceTitle(d)}
            open={expand.open === key}
            onToggle={() => expand.toggle(key)}
          >
            <LkField label="状态">
              <DeviceState d={d} />
            </LkField>
            <LkField label="IP">
              <span className="font-mono text-xs">{d.ip || "—"}</span>
            </LkField>
            {d.nodeName && d.name && (
              <LkField label="设备">
                <span className="text-xs">{d.name}</span>
              </LkField>
            )}
            {d.clientBuild && (
              <LkField label="版本">
                {d.clientBuild}
                {d.osVersion && (
                  <span className="text-xs text-default-500">
                    · 系统 {d.osVersion}
                  </span>
                )}
              </LkField>
            )}
            <LkField label="报到">{twFull(d.lastSeenAt)}</LkField>
          </LkExpandCard>
        );
      })}
      {items.length === 0 && (
        <p className="rounded-xl border border-dashed border-divider px-3 py-4 text-center text-sm text-default-500">
          还没读到设备列表
        </p>
      )}
      <p className="px-1 text-xs text-default-500">
        只读。踢下线、恢复去利群后台。
      </p>
    </div>
  );
}
