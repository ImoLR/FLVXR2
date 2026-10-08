import type { MonitorTunnelApiItem, TunnelMetricApiItem } from "@/api/types";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import {
  RefreshCw,
  ArrowLeft,
  Activity,
  ArrowRightLeft,
  Eye,
} from "lucide-react";
import toast from "react-hot-toast";

import { getMonitorTunnels, getTunnelMetrics } from "@/api";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Chip } from "@/shadcn-bridge/heroui/chip";
import { Select, SelectItem } from "@/shadcn-bridge/heroui/select";
import {
  Table,
  TableHeader,
  TableColumn,
  TableBody,
  TableRow,
  TableCell,
} from "@/shadcn-bridge/heroui/table";

interface TunnelMonitorViewProps {
  viewMode?: "list" | "grid";
  refreshTrigger?: number;
  onLoadingChange?: (loading: boolean) => void;
}

const formatTimestamp = (ts: number, rangeMs?: number): string => {
  const date = new Date(ts);
  const includeDate = (rangeMs ?? 0) >= 24 * 60 * 60 * 1000;

  if (includeDate) {
    return date.toLocaleString("zh-CN", {
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    });
  }

  return date.toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
};

/** Animated pulse dot for live status */
function LiveDot() {
  return (
    <span className="relative flex h-2 w-2">
      <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-success opacity-75" />
      <span className="relative inline-flex rounded-full h-2 w-2 bg-success" />
    </span>
  );
}

/* ─── Module-level constants & memoized sub-components ────────────── */

const TIME_RANGE_OPTIONS = [
  { key: String(15 * 60 * 1000), label: "15分钟" },
  { key: String(60 * 60 * 1000), label: "1小时" },
  { key: String(6 * 60 * 60 * 1000), label: "6小时" },
  { key: String(24 * 60 * 60 * 1000), label: "24小时" },
];

function TimeRangeSelect({
  value,
  onChange,
}: {
  value: number;
  onChange: (v: number) => void;
}) {
  return (
    <Select
      className="w-22 sm:w-30"
      selectedKeys={[String(value)]}
      size="sm"
      onSelectionChange={(keys) => {
        const v = Number(Array.from(keys)[0]);

        if (v > 0) onChange(v);
      }}
    >
      {TIME_RANGE_OPTIONS.map((opt) => (
        <SelectItem key={opt.key}>{opt.label}</SelectItem>
      ))}
    </Select>
  );
}

interface TrafficChartCardProps {
  rangeMs: number;
  onRangeChange: (v: number) => void;
  loading: boolean;
  error: string | null;
  data: Array<{
    time: string;
    bytesIn: number;
    bytesOut: number;
    connections: number;
  }>;
  tunnelId: number;
  onRefresh: (id: number) => void;
}

const TrafficChartCard = React.memo(function TrafficChartCard({
  rangeMs,
  onRangeChange,
  loading,
  error,
  data,
  tunnelId,
  onRefresh,
}: TrafficChartCardProps) {
  const yFormatter = (value: unknown) => {
    const n = Number(value);

    if (!Number.isFinite(n) || n <= 0) return "0 B";
    const k = 1024;
    const sizes = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(n) / Math.log(k));

    return `${parseFloat((n / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`;
  };

  return (
    <Card className="overflow-hidden rounded-xl border border-divider bg-content1 shadow-md">
      <CardHeader className="flex flex-row items-center justify-between">
        <h3 className="text-lg font-semibold">流量趋势</h3>
        <div className="flex items-center gap-2">
          <TimeRangeSelect value={rangeMs} onChange={onRangeChange} />
          <Button
            isLoading={loading}
            size="sm"
            variant="flat"
            onPress={() => onRefresh(tunnelId)}
          >
            刷新
          </Button>
        </div>
      </CardHeader>
      <CardBody className="space-y-4">
        {loading ? (
          <div className="flex justify-center py-8">
            <RefreshCw className="w-6 h-6 animate-spin" />
          </div>
        ) : error ? (
          <div className="text-center py-8 text-danger text-sm">{error}</div>
        ) : data.length > 0 ? (
          <div className="h-64">
            <ResponsiveContainer height="100%" width="100%">
              <LineChart data={data}>
                <CartesianGrid opacity={0.3} strokeDasharray="3 3" />
                <XAxis dataKey="time" fontSize={11} tick={{ fill: "#888" }} />
                <YAxis
                  fontSize={11}
                  tick={{ fill: "#888" }}
                  tickFormatter={yFormatter}
                />
                <Tooltip
                  contentStyle={{
                    backgroundColor: "rgba(0,0,0,0.85)",
                    border: "none",
                    borderRadius: "8px",
                    fontSize: 12,
                  }}
                  formatter={yFormatter}
                  labelStyle={{ color: "#fff" }}
                />
                <Line
                  dataKey="bytesIn"
                  dot={false}
                  name="入站流量"
                  stroke="#10b981"
                  strokeWidth={2}
                  type="monotone"
                />
                <Line
                  dataKey="bytesOut"
                  dot={false}
                  name="出站流量"
                  stroke="#ef4444"
                  strokeWidth={2}
                  type="monotone"
                />
              </LineChart>
            </ResponsiveContainer>
          </div>
        ) : (
          <div className="text-center py-8 text-default-500">暂无流量数据</div>
        )}
      </CardBody>
    </Card>
  );
});

export function TunnelMonitorView({
  viewMode = "grid",
  refreshTrigger,
  onLoadingChange,
}: TunnelMonitorViewProps) {
  const [tunnels, setTunnels] = useState<MonitorTunnelApiItem[]>([]);
  const [tunnelsLoading, setTunnelsLoading] = useState(false);

  useEffect(() => {
    onLoadingChange?.(tunnelsLoading);
  }, [tunnelsLoading, onLoadingChange]);
  const [tunnelsError, setTunnelsError] = useState<string | null>(null);
  const [accessDenied, setAccessDenied] = useState<string | null>(null);

  // Detail view state
  const [detailTunnelId, setDetailTunnelId] = useState<number | null>(null);

  // Tunnel traffic metrics for chart
  const [tunnelMetrics, setTunnelMetrics] = useState<TunnelMetricApiItem[]>([]);
  const [tunnelMetricsLoading, setTunnelMetricsLoading] = useState(false);
  const [tunnelMetricsError, setTunnelMetricsError] = useState<string | null>(
    null,
  );
  const [tunnelRangeMs, setTunnelRangeMs] = useState<number>(() => {
    try {
      const saved = localStorage.getItem("tunnel-monitor-traffic-range");

      if (saved) return Number(saved);
    } catch {}

    return 60 * 60 * 1000;
  });

  useEffect(() => {
    try {
      localStorage.setItem(
        "tunnel-monitor-traffic-range",
        String(tunnelRangeMs),
      );
    } catch {}
  }, [tunnelRangeMs]);

  // --- Load tunnel list ---
  const loadTunnels = useCallback(async (options?: { silent?: boolean }) => {
    const silent = options?.silent ?? false;

    if (!silent) setTunnelsLoading(true);
    try {
      const response = await getMonitorTunnels();

      if (response.code === 0 && response.data) {
        setAccessDenied(null);
        setTunnelsError(null);
        setTunnels(response.data);

        return;
      }
      if (response.code === 403) {
        setAccessDenied(response.msg || "暂无监控权限，请联系管理员授权");
        setTunnelsError(null);
        setTunnels([]);

        return;
      }
      setTunnelsError(response.msg || "加载隧道列表失败");
      if (!silent) toast.error(response.msg || "加载隧道列表失败");
    } catch {
      if (!silent) {
        setTunnelsError("加载隧道列表失败");
        toast.error("加载隧道列表失败");
      }
    } finally {
      if (!silent) setTunnelsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadTunnels();
  }, [loadTunnels, refreshTrigger]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      void loadTunnels({ silent: true });
    }, 60_000);

    return () => window.clearInterval(timer);
  }, [loadTunnels]);

  // --- Load tunnel traffic metrics for detail chart ---
  const loadTunnelMetrics = useCallback(
    async (tunnelId: number, options?: { silent?: boolean }) => {
      const silent = options?.silent ?? false;

      if (!silent) setTunnelMetricsLoading(true);
      try {
        const end = Date.now();
        const start = end - tunnelRangeMs;
        const response = await getTunnelMetrics(tunnelId, start, end);

        if (response.code === 0 && Array.isArray(response.data)) {
          setTunnelMetricsError(null);
          const ordered = [...response.data].sort(
            (a, b) => a.timestamp - b.timestamp,
          );

          setTunnelMetrics(ordered);

          return;
        }
        setTunnelMetricsError(response.msg || "加载流量数据失败");
      } catch {
        if (!silent) setTunnelMetricsError("加载流量数据失败");
      } finally {
        if (!silent) setTunnelMetricsLoading(false);
      }
    },
    [tunnelRangeMs],
  );

  useEffect(() => {
    if (detailTunnelId) {
      void loadTunnelMetrics(detailTunnelId);
    }
  }, [detailTunnelId, loadTunnelMetrics]);

  // Auto-refresh detail charts
  useEffect(() => {
    if (!detailTunnelId) return;
    const timer = window.setInterval(() => {
      void loadTunnelMetrics(detailTunnelId, { silent: true });
    }, 30_000);

    return () => window.clearInterval(timer);
  }, [detailTunnelId, loadTunnelMetrics]);

  // Memoize chart data so React.memo sub-components see stable references
  const tunnelChartData = useMemo(
    () =>
      tunnelMetrics.map((m) => ({
        time: formatTimestamp(m.timestamp, tunnelRangeMs),
        bytesIn: m.bytesIn,
        bytesOut: m.bytesOut,
        connections: m.connections,
      })),
    [tunnelMetrics, tunnelRangeMs],
  );

  const detailTunnel =
    detailTunnelId != null
      ? tunnels.find((t) => t.id === detailTunnelId)
      : null;

  // Aggregate stats
  const tunnelStats = useMemo(() => {
    const enabled = tunnels.filter((t) => t.status === 1).length;

    return { total: tunnels.length, enabled };
  }, [tunnels]);

  // =====================
  // RENDER
  // =====================

  if (accessDenied) {
    return (
      <Card>
        <CardHeader className="flex flex-row items-center gap-2">
          <Activity className="w-5 h-5 text-warning" />
          <h3 className="text-lg font-semibold">监控权限</h3>
        </CardHeader>
        <CardBody className="space-y-4">
          <div className="text-sm text-default-600">{accessDenied}</div>
          <div className="text-xs text-default-500 mt-2">
            如需使用监控功能，请联系管理员在用户页面授予监控权限。
          </div>
        </CardBody>
      </Card>
    );
  }

  // ===== DETAIL VIEW =====
  if (detailTunnelId && detailTunnel) {
    return (
      <div className="space-y-6">
        {/* 详情页统一头部 */}
        <Card className="border border-divider bg-content1 shadow-sm">
          <CardBody className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 py-3 px-4">
            <div className="flex items-center gap-4">
              <Button
                className="bg-default-100 hover:bg-default-200 font-medium"
                size="sm"
                variant="flat"
                onPress={() => {
                  setDetailTunnelId(null);
                  setTunnelMetrics([]);
                }}
              >
                <ArrowLeft className="w-4 h-4 mr-1" />
                返回列表
              </Button>
              <div className="w-[1px] h-5 bg-divider hidden sm:block" />
              <div className="flex items-center gap-2.5">
                <ArrowRightLeft
                  className={`w-5 h-5 ${detailTunnel.status === 1 ? "text-success" : "text-default-400"}`}
                />
                <h3 className="text-lg font-bold text-foreground">
                  {detailTunnel.name}
                </h3>
                <Chip
                  className="rounded-md font-medium"
                  color={detailTunnel.status === 1 ? "success" : "danger"}
                  size="sm"
                  variant="flat"
                >
                  {detailTunnel.status === 1 ? "启用" : "禁用"}
                </Chip>
              </div>
            </div>
            <div className="flex items-center gap-2 text-xs text-default-600 bg-default-100/50 px-3 py-1.5 rounded-full border border-divider">
              <LiveDot />
              <span className="font-medium">实时已连接</span>
            </div>
          </CardBody>
        </Card>

        {/* ====== Traffic Chart — isolated with React.memo ====== */}
        <div className="overflow-hidden rounded-xl border border-divider bg-content1 shadow-md p-4">
          <TrafficChartCard
            data={tunnelChartData}
            error={tunnelMetricsError}
            loading={tunnelMetricsLoading}
            rangeMs={tunnelRangeMs}
            tunnelId={detailTunnelId}
            onRangeChange={setTunnelRangeMs}
            onRefresh={loadTunnelMetrics}
          />
        </div>
      </div>
    );
  }

  // ===== LIST/GRID VIEW =====
  return (
    <div className="flex flex-col gap-4 w-full">
      <div className="flex flex-wrap items-center gap-3 mb-0">
        <Chip className="rounded-md" color="primary" size="sm" variant="flat">
          隧道 {tunnelStats.enabled}/{tunnelStats.total}
        </Chip>
        {/* <div className="ml-auto">
          <Button isLoading={tunnelsLoading} size="sm" variant="flat" onPress={() => loadTunnels()}>
            刷新
          </Button>
        </div> */}
      </div>

      {tunnelsError ? (
        <Card>
          <CardBody className="space-y-4">
            <div className="text-sm text-default-600">{tunnelsError}</div>
          </CardBody>
        </Card>
      ) : null}

      {viewMode === "grid" ? (
        <div className="overflow-hidden rounded-xl border border-divider bg-content1 shadow-md">
          <div className="flex items-center justify-between border-b border-divider bg-default-100/40 px-4 py-3">
            <span className="text-sm font-semibold text-foreground">
              隧道监控数量
            </span>
            <span className="text-xs text-default-500 whitespace-nowrap">
              {tunnels.length} 个监控
            </span>
          </div>
          <div className="p-4">
            <div className="flvx-card-grid grid gap-4">
              {tunnels.map((tunnel) => {
                const isEnabled = tunnel.status === 1;

                return (
                  <Card
                    key={tunnel.id}
                    className="group h-full flex flex-col overflow-hidden border border-divider bg-content1 shadow-sm transition-shadow duration-200 hover:shadow-md cursor-pointer"
                    onClick={() => setDetailTunnelId(tunnel.id)}
                  >
                    <CardHeader className="pb-3 md:pb-3">
                      <div className="flex flex-col gap-2 w-full">
                        <div className="flex items-start justify-between w-full gap-3">
                          <div className="flex items-center gap-3 min-w-0 flex-1">
                            <div className="relative flex-shrink-0">
                              <div className="w-10 h-10 rounded-xl bg-default-100/70 dark:bg-default-50/10 flex items-center justify-center border border-divider">
                                <ArrowRightLeft
                                  className={`w-5 h-5 ${isEnabled ? "text-success" : "text-danger"}`}
                                />
                              </div>
                              <span
                                className={`absolute -bottom-0.5 -right-0.5 w-3 h-3 rounded-full border-2 border-background ${isEnabled ? "bg-success" : "bg-danger"}`}
                              />
                            </div>
                            <div className="flex flex-col min-w-0 flex-1">
                              <h3 className="font-semibold text-foreground text-sm truncate">
                                {tunnel.name}
                              </h3>
                              <div className="flex items-center gap-1.5 mt-1">
                                <span
                                  className={`inline-flex items-center justify-center px-2 py-0.5 rounded text-xs font-medium ${isEnabled ? "bg-success-500/10 text-success-600 dark:text-success-400" : "bg-danger-500/10 text-danger-600 dark:text-danger-400"}`}
                                >
                                  {isEnabled ? "启用" : "禁用"}
                                </span>
                              </div>
                            </div>
                          </div>
                          <div className="relative flex-shrink-0">
                            <Button
                              isIconOnly
                              size="sm"
                              variant="light"
                              onPress={() => setDetailTunnelId(tunnel.id)}
                            >
                              <Eye className="w-4 h-4 text-primary" />
                            </Button>
                          </div>
                        </div>
                      </div>
                    </CardHeader>
                    <CardBody className="flex flex-1 flex-col pt-0 pb-3">
                      <div className="flex justify-between items-center pt-2 border-t border-divider mt-1">
                        <span
                          className={`inline-flex items-center justify-center px-2 py-0.5 rounded text-xs font-medium ${isEnabled ? "bg-primary-500/10 text-primary-500 dark:text-primary-400" : "bg-default-500/10 text-default-500"}`}
                        >
                          {isEnabled ? "活跃" : "静止"}
                        </span>
                      </div>
                    </CardBody>
                  </Card>
                );
              })}
            </div>
          </div>
        </div>
      ) : (
        <Card className="w-full">
          <Table
            aria-label="隧道列表"
            className="overflow-x-auto min-w-full"
            classNames={{
              th: "bg-default-100/50 text-default-600 text-foreground font-semibold text-sm border-b border-divider py-3 uppercase tracking-wider whitespace-nowrap",
              td: "border-b border-divider/50 group-data-[last=true]:border-b-0 align-middle p-3",
              tr: "hover:bg-default-50/50 transition-colors",
            }}
          >
            <TableHeader>
              <TableColumn align="center" className="w-[100px] text-center">
                查看
              </TableColumn>
              <TableColumn align="start">
                隧道监控名称
                <span className="text-primary-600 font-bold text-[10px] ml-1">
                  ^{tunnels.length}个
                </span>
              </TableColumn>
            </TableHeader>
            <TableBody emptyContent="暂无隧道">
              {tunnels.map((tunnel) => {
                return (
                  <TableRow
                    key={tunnel.id}
                    className="cursor-pointer h-16"
                    onClick={() => setDetailTunnelId(tunnel.id)}
                  >
                    <TableCell>
                      <div className="flex justify-center w-full">
                        <Button
                          isIconOnly
                          size="sm"
                          variant="light"
                          onPress={() => setDetailTunnelId(tunnel.id)}
                        >
                          <Eye className="w-4 h-4 text-primary" />
                        </Button>
                      </div>
                    </TableCell>
                    <TableCell>
                      <span className="font-semibold text-sm whitespace-nowrap">
                        {tunnel.name}
                      </span>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Card>
      )}
    </div>
  );
}
