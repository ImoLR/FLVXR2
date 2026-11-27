import type { LineMonitorChart } from "@/api/line-monitor";

import { useCallback, useEffect, useState } from "react";
import { Activity } from "lucide-react";

import { ChartCard } from "./chart-card";

import {
  getUserLineMonitorData,
  getUserLineMonitorList,
  userLineAsLine,
} from "@/api/line-monitor";
import { AnimatedPage } from "@/components/animated-page";
import { useMobileBreakpoint } from "@/hooks/useMobileBreakpoint";
import { Card, CardBody } from "@/shadcn-bridge/heroui/card";
import { useThemeContext } from "@/themes/context";

// Plan 321 (user 11-28): 线路监测 for normal users — the admin's charts left
// 「用户可见」, read-only: same chart, ranges, 统计 and 断流记录 as the admin
// page, nothing to create, edit, pause or test, no addresses (the API sends
// none).
export function UserLineMonitorView() {
  const { effectiveMode } = useThemeContext();
  const dark = effectiveMode === "dark";
  const isMobile = useMobileBreakpoint();
  const [charts, setCharts] = useState<LineMonitorChart[] | null>(null);
  const [error, setError] = useState("");

  const reload = useCallback(async () => {
    const res = await getUserLineMonitorList();

    if (res.code === 0 && res.data) {
      setCharts(
        (res.data.charts ?? []).map((c) => ({
          id: c.id,
          name: c.name,
          sort: c.sort,
          lines: (c.lines ?? []).map(userLineAsLine),
        })),
      );
      setError("");
    } else {
      setError(res.msg || "加载失败");
    }
  }, []);

  useEffect(() => {
    void reload();
    const timer = window.setInterval(() => {
      if (!document.hidden) void reload();
    }, 60_000);

    return () => window.clearInterval(timer);
  }, [reload]);

  return (
    <AnimatedPage className="px-3 lg:px-6 py-8">
      <div className="mb-5 min-w-0">
        <h1 className="text-xl font-bold">线路监测</h1>
        <p className="mt-0.5 text-sm text-default-500">
          各条线路每 30 秒测一次 TCP 延迟，看抖动、丢包和断流，数据保留 30 天。
        </p>
      </div>
      {error && !charts ? (
        <Card>
          <CardBody className="py-10 text-center text-sm text-danger">
            {error}
          </CardBody>
        </Card>
      ) : !charts ? (
        <div className="h-72 animate-pulse rounded-xl bg-default-100 dark:bg-default-50/10" />
      ) : charts.length === 0 ? (
        <Card data-testid="lm-user-empty">
          <CardBody className="flex flex-col items-center gap-3 py-14 text-center">
            <span className="flex h-12 w-12 items-center justify-center rounded-full bg-cyan-500/15 text-cyan-700 dark:bg-cyan-400/15 dark:text-cyan-300">
              <Activity className="h-6 w-6" />
            </span>
            <div className="text-base font-semibold">暂时没有可查看的线路</div>
            <p className="max-w-md text-sm text-default-500">
              管理员开放线路监测后会显示在这里。
            </p>
          </CardBody>
        </Card>
      ) : (
        <div className="space-y-4" data-testid="lm-user-view">
          {charts.map((c, i) => (
            <ChartCard
              key={c.id}
              readOnly
              chart={c}
              dark={dark}
              index={i}
              isMobile={isMobile}
              loadData={getUserLineMonitorData}
              total={charts.length}
            />
          ))}
        </div>
      )}
    </AnimatedPage>
  );
}
