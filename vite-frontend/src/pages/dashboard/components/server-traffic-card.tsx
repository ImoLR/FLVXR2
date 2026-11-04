import type { ServerTrafficRow, ServerTrafficView } from "@/api/traffic-stats";

import { useCallback, useState } from "react";

import { getServerTraffic } from "@/api/traffic-stats";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Pill } from "@/pages/landing/pill";
import {
  Meter,
  RolePill,
  formatBytes,
  formatLimit,
  meterLevel,
  shortDay,
  useVisiblePoll,
} from "@/pages/dashboard/components/traffic-shared";

// Plan 212: the user's 「服务器流量」 — one row per granted server (入口 + 落地)
// with this period's billed usage against the per-server limit. The numbers
// are the ones the panel enforces (user_server_quota); hidden when the user
// has no granted server.

const POLL_MS = 60000;

function ServerRow({ row }: { row: ServerTrafficRow }) {
  const used = (row.usedIn || 0) + (row.usedOut || 0);
  const limited = row.limitBytes > 0;
  const percent = limited ? (used / row.limitBytes) * 100 : 0;
  const level = row.exhausted ? "full" : meterLevel(percent);

  return (
    <div
      className="space-y-2 rounded-lg border border-divider p-3"
      data-server-traffic-row={`${row.serverKind}:${row.serverRefId}`}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-1.5">
          <span
            className="min-w-0 truncate font-medium text-foreground"
            title={row.serverName}
          >
            {row.serverName || `#${row.serverRefId}`}
          </span>
          {row.entry && <RolePill kind="entry" />}
          {row.landing && <RolePill kind="landing" />}
        </div>
        {limited && level === "full" && (
          <Pill
            title="这台服务器的本期流量已用完：经过它的规则会暂停，到重置日或管理员调高额度后恢复"
            tone="red"
          >
            已用完
          </Pill>
        )}
        {limited && level === "warn" && <Pill tone="amber">接近上限</Pill>}
      </div>
      {limited && (
        <Meter
          label={`${row.serverName} 已用 ${percent.toFixed(1)}%`}
          level={level}
          percent={percent}
        />
      )}
      <div className="flex items-baseline justify-between gap-2 text-sm">
        <span className="min-w-0 truncate text-default-600">
          已用{" "}
          <span className="font-semibold text-foreground">
            {formatBytes(used)}
          </span>
          {limited && <> / {formatLimit(row.limitBytes)}</>}
        </span>
        <span className="shrink-0 text-xs text-default-500">
          {limited
            ? used >= row.limitBytes
              ? `${percent.toFixed(1)}%`
              : `剩余 ${formatBytes(row.limitBytes - used)}`
            : "不限"}
        </span>
      </div>
      {row.poolName && (
        <p className="truncate text-xs text-default-500">
          另受共享流量池「{row.poolName}」限制
        </p>
      )}
    </div>
  );
}

export default function ServerTrafficCard() {
  const [view, setView] = useState<ServerTrafficView | null>(null);

  const load = useCallback(() => {
    getServerTraffic()
      .then((res) => {
        if (res.code === 0 && res.data) setView(res.data);
      })
      .catch(() => undefined);
  }, []);

  useVisiblePoll(load, POLL_MS);

  if (!view || view.servers.length === 0) return null;
  // 入口 first, then 落地; the panel's order (by id) inside each group.
  const rows = [...view.servers].sort(
    (a, b) => Number(b.entry) - Number(a.entry),
  );
  const period = view.periodStart
    ? `本期 ${shortDay(view.periodStart)} 起，${shortDay(view.nextReset)} 重置`
    : "不自动重置（累计）";

  return (
    <Card
      data-server-traffic-card
      className="mb-6 lg:mb-8 border border-gray-200 dark:border-default-200 shadow-md"
    >
      <CardHeader className="pb-2">
        <div className="flex w-full flex-col gap-1">
          <div className="flex items-center gap-2">
            <svg
              aria-hidden="true"
              className="h-5 w-5 text-teal-600 dark:text-teal-400"
              fill="none"
              stroke="currentColor"
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              viewBox="0 0 24 24"
            >
              <rect height="7" rx="2" width="18" x="3" y="3" />
              <rect height="7" rx="2" width="18" x="3" y="14" />
              <path d="M7 6.5h.01M7 17.5h.01M11 6.5h6M11 17.5h6" />
            </svg>
            <h2 className="text-lg lg:text-xl font-semibold text-foreground">
              服务器流量
            </h2>
            <span className="rounded-full bg-default-100 px-2 py-1 text-xs text-default-600 dark:bg-default-50">
              {rows.length}
            </span>
          </div>
          <p className="text-xs text-default-500">{period} · 计费流量</p>
        </div>
      </CardHeader>
      <CardBody className="pt-0">
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {rows.map((row) => (
            <ServerRow key={`${row.serverKind}:${row.serverRefId}`} row={row} />
          ))}
        </div>
        <p className="mt-3 text-xs text-default-500">
          规则经过的每台服务器都会计入；某台服务器用完后，经过它的规则会暂停，到重置日或管理员调高额度后恢复。
        </p>
      </CardBody>
    </Card>
  );
}
