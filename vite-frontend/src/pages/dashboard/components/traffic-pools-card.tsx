import type { TrafficPool } from "@/api/landing";

import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Pill } from "@/pages/landing/pill";

// Plan 157: the user's 共享流量池, read-only (name, used, limit). Shown only
// when the admin set at least one pool for this user.

const gb = (bytes: number) =>
  (bytes / 1073741824).toLocaleString("zh-CN", { maximumFractionDigits: 2 });

export function TrafficPoolsCard({ pools }: { pools: TrafficPool[] }) {
  if (pools.length === 0) return null;

  return (
    <Card
      data-traffic-pools-card
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
              strokeWidth={2}
              viewBox="0 0 24 24"
            >
              <ellipse cx="12" cy="6" rx="8" ry="3" />
              <path d="M4 6v6c0 1.66 3.58 3 8 3s8-1.34 8-3V6" />
              <path d="M4 12v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6" />
            </svg>
            <h2 className="text-lg lg:text-xl font-semibold text-foreground">
              共享流量池
            </h2>
            <span className="rounded-full bg-default-100 px-2 py-1 text-xs text-default-600 dark:bg-default-50">
              {pools.length}
            </span>
          </div>
          <p className="text-xs text-default-500">
            池内服务器共用一个额度，同一条流量经过池内多台服务器只算一次；到流量重置日自动清零。
          </p>
        </div>
      </CardHeader>
      <CardBody className="pt-0">
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {pools.map((pool) => {
            const used = (pool.usedIn || 0) + (pool.usedOut || 0);
            const unlimited = pool.limitBytes <= 0;
            const percent = unlimited
              ? 0
              : Math.min(100, (used / pool.limitBytes) * 100);
            const bar = pool.paused
              ? "bg-red-500 dark:bg-red-600"
              : percent >= 90
                ? "bg-red-500 dark:bg-red-600"
                : percent >= 70
                  ? "bg-orange-500 dark:bg-orange-600"
                  : "bg-teal-500 dark:bg-teal-600";

            return (
              <div
                key={pool.id ?? pool.name}
                className="space-y-2 rounded-lg border border-divider p-3"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="min-w-0 truncate font-medium">
                    {pool.name}
                  </span>
                  {pool.paused ? (
                    <Pill tone="red">已用完</Pill>
                  ) : (
                    <Pill tone="green">正常</Pill>
                  )}
                </div>
                <div className="h-1.5 w-full rounded-full bg-gray-200 dark:bg-gray-800">
                  <div
                    className={`h-1.5 rounded-full ${unlimited ? "bg-teal-500/40" : bar}`}
                    style={{ width: unlimited ? "100%" : `${percent}%` }}
                  />
                </div>
                <p className="text-sm text-default-600">
                  已用 {gb(used)} GB
                  {unlimited ? "（不限）" : ` / ${gb(pool.limitBytes)} GB`}
                </p>
                {pool.paused && (
                  <p className="text-xs text-danger">
                    经过池内服务器的规则已暂停，到重置日或管理员调高额度后自动恢复
                  </p>
                )}
              </div>
            );
          })}
        </div>
      </CardBody>
    </Card>
  );
}
