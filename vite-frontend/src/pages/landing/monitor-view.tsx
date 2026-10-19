import type {
  LandingMonitorData,
  LandingMonitorNode,
  LandingMonitorServer,
} from "@/api/landing";

import {
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Activity,
  ArrowDown,
  ArrowUp,
  Database,
  Server,
  Users,
} from "lucide-react";

import {
  landingGB,
  landingMonitor,
  landingProtocolLabel,
  landingStatus,
} from "@/api/landing";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableColumn,
  TableHeader,
  TableRow,
} from "@/shadcn-bridge/heroui/table";

// Same cadence as the node monitor's 5 s service-monitor refresh. The backend
// answers from memory plus a fixed handful of indexed queries.
const REFRESH_MS = 5_000;

type Tone =
  | "success"
  | "warning"
  | "danger"
  | "primary"
  | "secondary"
  | "default";

const toneClass: Record<Tone, string> = {
  success: "bg-success-500/10 text-success-600 dark:text-success-400",
  warning: "bg-warning-500/10 text-warning-600 dark:text-warning-400",
  danger: "bg-danger-500/10 text-danger-600 dark:text-danger-400",
  primary: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
  secondary: "bg-purple-500/10 text-purple-600 dark:text-purple-400",
  default:
    "border border-divider bg-transparent text-default-600 dark:text-default-500",
};

function Pill({
  tone,
  children,
  className = "",
}: {
  tone: Tone;
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={`inline-flex items-center justify-center gap-1 whitespace-nowrap rounded px-2 py-0.5 text-xs font-medium ${toneClass[tone]} ${className}`}
    >
      {children}
    </span>
  );
}

function LiveDot({ ok }: { ok: boolean }) {
  if (!ok) return <span className="h-2 w-2 rounded-full bg-default-300" />;

  return (
    <span className="relative flex h-2 w-2">
      <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success opacity-75" />
      <span className="relative inline-flex h-2 w-2 rounded-full bg-success" />
    </span>
  );
}

const formatRate = (value: number | null | undefined): string => {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return "-";
  }
  if (value <= 0) return "0 B/s";
  const units = ["B/s", "KB/s", "MB/s", "GB/s", "TB/s"];
  const i = Math.min(
    units.length - 1,
    Math.floor(Math.log(value) / Math.log(1024)),
  );

  const scaled = value / Math.pow(1024, i);
  // Three significant digits keep a live rate short enough for 16px text in
  // narrow H5 cards (350 KB/s, 2.71 MB/s, 10.8 MB/s).
  const digits = scaled >= 100 ? 0 : scaled >= 10 ? 1 : 2;

  return `${parseFloat(scaled.toFixed(digits))} ${units[i]}`;
};

const formatAge = (ms: number): string => {
  const seconds = Math.max(0, Math.round(ms / 1000));

  if (seconds < 60) return `数据 ${seconds} 秒前`;
  const minutes = Math.floor(seconds / 60);

  if (minutes < 60) return `数据 ${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);

  if (hours < 48) return `数据 ${hours} 小时前`;

  return `数据 ${Math.floor(hours / 24)} 天前`;
};

// Age against the server clock (generatedAt), so a skewed browser clock
// cannot make shared data look fresh.
function AgeHint({
  sampleAt,
  data,
}: {
  sampleAt: number;
  data: LandingMonitorData;
}) {
  if (!sampleAt) return <Pill tone="default">暂无实时数据</Pill>;
  const age = data.generatedAt - sampleAt;

  if (age <= data.ageHintMs) return null;

  return <Pill tone="warning">{formatAge(age)}</Pill>;
}

const nodeTone = (node: LandingMonitorNode): Tone => {
  if (node.deletePending) return "warning";
  switch (node.status) {
    case "active":
      return "success";
    case "error":
    case "paused_quota":
    case "paused_server_quota":
      return "danger";
    case "disabled":
      return "default";
    default:
      return "warning";
  }
};

const serverState = (
  server: LandingMonitorServer,
): { label: string; tone: Tone } => {
  if (server.status !== "active") return { label: "已停用", tone: "default" };

  return server.online
    ? { label: "在线", tone: "success" }
    : { label: "离线", tone: "danger" };
};

function RateBoxes({
  up,
  down,
}: {
  up: number | null | undefined;
  down: number | null | undefined;
}) {
  return (
    <div className="space-y-1">
      <div className="flex gap-1 px-1 text-[11px] font-bold uppercase tracking-wider text-foreground">
        <span className="flex-1 text-left">上传</span>
        <span className="flex-1 text-right">下载</span>
      </div>
      <div className="flex items-center gap-1">
        <div className="flex h-8 min-w-0 flex-1 items-center gap-1 rounded-md bg-default-100/45 px-2 dark:bg-default-50/10">
          <ArrowUp
            className="h-3 w-3 shrink-0 text-success-500"
            strokeWidth={2.5}
          />
          <span className="truncate font-mono text-xs font-semibold text-foreground">
            {formatRate(up)}
          </span>
        </div>
        <div className="flex h-8 min-w-0 flex-1 items-center justify-end gap-1 rounded-md bg-default-100/45 px-2 dark:bg-default-50/10">
          <span className="truncate font-mono text-xs font-semibold text-foreground">
            {formatRate(down)}
          </span>
          <ArrowDown
            className="h-3 w-3 shrink-0 text-primary-500"
            strokeWidth={2.5}
          />
        </div>
      </div>
    </div>
  );
}

function StatBox({
  label,
  value,
  hint,
}: {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
}) {
  return (
    <div className="min-w-0 space-y-1">
      <div className="px-1 text-[11px] font-bold uppercase tracking-wider text-default-500">
        {label}
      </div>
      <div className="flex min-h-8 items-center rounded-md bg-default-100/45 px-2 py-1 dark:bg-default-50/10">
        <span className="break-words font-mono text-xs font-semibold text-foreground">
          {value}
        </span>
      </div>
      {hint ? (
        <div className="px-1 text-xs text-default-500">{hint}</div>
      ) : null}
    </div>
  );
}

const capText = (value: number, cap: number) =>
  cap > 0 ? `${value} / ${cap}` : `${value} / 不限`;

const trafficText = (bytes: number, cap: number) =>
  `${landingGB(bytes)} / ${cap > 0 ? landingGB(cap) : "不限"} GB`;

function serverTraffic(server: LandingMonitorServer, admin: boolean) {
  if (!admin && (server.quotaLimitBytes ?? 0) > 0) {
    return {
      value: trafficText(server.quotaUsedBytes ?? 0, server.quotaLimitBytes!),
      hint: server.quotaPaused ? "服务器额度已用完" : "我的服务器额度",
    };
  }
  if (admin && server.source === "shared" && server.shareLimitBytes) {
    return {
      value: `${landingGB(server.billedBytes)} GB`,
      hint: `共享额度 ${trafficText(server.shareUsedBytes ?? 0, server.shareLimitBytes)}`,
    };
  }

  return {
    value: `${landingGB(server.billedBytes)} GB`,
    hint: admin ? "全部节点计费" : "我的节点计费",
  };
}

function ServerHeader({
  server,
  data,
}: {
  server: LandingMonitorServer;
  data: LandingMonitorData;
}) {
  const state = serverState(server);
  const traffic = serverTraffic(server, data.admin);

  return (
    <div className="space-y-3 border-b border-divider bg-default-100/40 px-4 py-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-1 items-center gap-3">
          <div className="relative flex-shrink-0">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl border border-divider bg-default-100/70 dark:bg-default-50/10">
              <Server
                className={`h-5 w-5 ${server.online ? "text-success-500" : "text-default-400"}`}
              />
            </div>
            <span
              className={`absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full border-2 border-background ${server.online && server.status === "active" ? "bg-success" : "bg-danger"}`}
            />
          </div>
          <div className="min-w-0 flex-1">
            <h3 className="break-words text-sm font-semibold text-foreground">
              {server.name}
            </h3>
            <div className="mt-1 flex flex-wrap items-center gap-1.5">
              <Pill tone={server.source === "shared" ? "secondary" : "primary"}>
                {server.source === "shared" ? "妙妙屋共享" : "自有"}
              </Pill>
              <Pill tone={state.tone}>{state.label}</Pill>
              {server.source === "shared" && server.coreState ? (
                <Pill
                  tone={server.coreState === "running" ? "success" : "default"}
                >
                  {server.coreState === "running" ? "核心运行中" : "核心未运行"}
                </Pill>
              ) : null}
              <AgeHint data={data} sampleAt={server.sampleAt} />
            </div>
          </div>
        </div>
        <Pill tone="default">
          节点 {server.activeNodeCount}/{server.nodeCount}
        </Pill>
      </div>
      {server.notice ? (
        <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10">
          {server.notice}
        </p>
      ) : null}
      {server.syncError ? (
        <p className="rounded-lg bg-warning-50 p-2 text-xs text-warning dark:bg-warning-500/10">
          同步异常 · {server.syncError}
        </p>
      ) : null}
      <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
        <StatBox
          hint={data.admin ? "当前 / 上限" : "我的节点合计"}
          label="实时连接"
          value={
            data.admin
              ? capText(server.connections, server.maxConnections)
              : server.connections
          }
        />
        <div className="order-last col-span-2 xl:order-none">
          <RateBoxes down={server.downRate} up={server.upRate} />
        </div>
        <StatBox hint={traffic.hint} label="本期流量" value={traffic.value} />
      </div>
    </div>
  );
}

function NodeStatus({ node }: { node: LandingMonitorNode }) {
  return (
    <Pill tone={nodeTone(node)}>
      {node.deletePending
        ? "删除中"
        : landingStatus[node.status] || node.status}
    </Pill>
  );
}

function NodeNotes({ node }: { node: LandingMonitorNode }) {
  if (node.syncError) {
    return <p className="text-xs text-danger">{node.syncError}</p>;
  }
  if (node.status === "pending" && node.pendingReason) {
    return <p className="text-xs text-warning">{node.pendingReason}</p>;
  }

  return null;
}

function NodeCard({
  node,
  data,
}: {
  node: LandingMonitorNode;
  data: LandingMonitorData;
}) {
  return (
    <Card className="flex h-full flex-col overflow-hidden border border-divider bg-content1 shadow-sm">
      <CardHeader className="pb-3 md:pb-3">
        <div className="flex w-full flex-col gap-2">
          <div className="flex w-full items-start justify-between gap-3">
            <div className="min-w-0 flex-1">
              <h3 className="truncate text-sm font-semibold text-foreground">
                {node.name}
              </h3>
              {data.admin ? (
                <div className="mt-0.5 truncate text-xs text-default-500">
                  {node.userName || `用户 ${node.userId}`}
                </div>
              ) : null}
            </div>
            <NodeStatus node={node} />
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <Pill tone="default">
              {landingProtocolLabel(node.protocol)} · {node.port}
            </Pill>
            <AgeHint data={data} sampleAt={node.sampleAt} />
          </div>
          <NodeNotes node={node} />
        </div>
      </CardHeader>
      <CardBody className="flex flex-1 flex-col pb-3 pt-0 md:pb-3 md:pt-0">
        <div className="flex-1 space-y-2.5 py-1">
          <RateBoxes down={node.downRate} up={node.upRate} />
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-1 border-t border-divider pt-2">
          <Pill tone="primary">
            <Activity className="h-3 w-3 shrink-0" />
            连接 {capText(node.connections, node.maxConnections)}
          </Pill>
          <Pill tone="secondary">
            <Users className="h-3 w-3 shrink-0" />
            IP 上限 {node.maxOnlineIps > 0 ? node.maxOnlineIps : "不限"}
          </Pill>
          <Pill tone="success">
            <Database className="h-3 w-3 shrink-0" />
            {trafficText(node.billedBytes, node.trafficLimitBytes)}
          </Pill>
        </div>
      </CardBody>
    </Card>
  );
}

function NodeTable({
  server,
  data,
}: {
  server: LandingMonitorServer;
  data: LandingMonitorData;
}) {
  return (
    <Table
      aria-label={`${server.name} 落地节点`}
      className="min-w-full overflow-x-auto"
      classNames={{
        th: "bg-default-100/50 text-default-600 font-semibold text-foreground text-sm border-b border-divider py-3 uppercase tracking-wider whitespace-nowrap text-center",
        td: "border-b border-divider/50 group-data-[last=true]:border-b-0 align-middle text-center p-2",
        tr: "hover:bg-default-50/50 transition-colors",
      }}
    >
      <TableHeader>
        <TableColumn align="center">状态</TableColumn>
        <TableColumn align="center">落地节点</TableColumn>
        <TableColumn align="center">协议 / 端口</TableColumn>
        <TableColumn align="center">速率</TableColumn>
        <TableColumn align="center">连接数</TableColumn>
        <TableColumn align="center">IP 上限</TableColumn>
        <TableColumn align="center">本期流量</TableColumn>
        <TableColumn align="center">数据</TableColumn>
      </TableHeader>
      <TableBody emptyContent="暂无落地节点">
        {server.nodes.map((node) => (
          <TableRow key={node.id}>
            <TableCell>
              <div className="flex w-full justify-center">
                <NodeStatus node={node} />
              </div>
            </TableCell>
            <TableCell>
              <div className="flex flex-col items-center whitespace-nowrap">
                <span className="text-sm font-semibold">{node.name}</span>
                {data.admin ? (
                  <span className="text-xs text-default-500">
                    {node.userName || `用户 ${node.userId}`}
                  </span>
                ) : null}
                <NodeNotes node={node} />
              </div>
            </TableCell>
            <TableCell>
              <span className="whitespace-nowrap text-xs text-default-600">
                {landingProtocolLabel(node.protocol)} · {node.port}
              </span>
            </TableCell>
            <TableCell>
              <div className="flex w-full flex-col items-center gap-2 whitespace-nowrap py-1 font-mono text-xs">
                <div className="flex items-center justify-center gap-1.5 text-success-500">
                  <span className="tabular-nums">
                    {formatRate(node.upRate)}
                  </span>
                  <ArrowUp className="h-3 w-3" strokeWidth={2.5} />
                </div>
                <div className="flex items-center justify-center gap-1.5 text-primary-500">
                  <span className="tabular-nums">
                    {formatRate(node.downRate)}
                  </span>
                  <ArrowDown className="h-3 w-3" strokeWidth={2.5} />
                </div>
              </div>
            </TableCell>
            <TableCell>
              <span className="whitespace-nowrap font-mono text-xs tabular-nums text-default-600">
                {capText(node.connections, node.maxConnections)}
              </span>
            </TableCell>
            <TableCell>
              <span className="whitespace-nowrap font-mono text-xs text-default-600">
                {node.maxOnlineIps > 0 ? node.maxOnlineIps : "不限"}
              </span>
            </TableCell>
            <TableCell>
              <span className="whitespace-nowrap font-mono text-xs text-default-600">
                {trafficText(node.billedBytes, node.trafficLimitBytes)}
              </span>
            </TableCell>
            <TableCell>
              <div className="flex w-full justify-center">
                <AgeHint data={data} sampleAt={node.sampleAt} />
                {node.sampleAt &&
                data.generatedAt - node.sampleAt <= data.ageHintMs ? (
                  <Pill tone="success">实时</Pill>
                ) : null}
              </div>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

interface LandingMonitorViewProps {
  active: boolean;
  viewMode?: "list" | "grid";
  refreshTrigger?: number;
  onLoadingChange?: (loading: boolean) => void;
}

export function LandingMonitorView({
  active,
  viewMode = "list",
  refreshTrigger = 0,
  onLoadingChange,
}: LandingMonitorViewProps) {
  const [data, setData] = useState<LandingMonitorData | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const inFlight = useRef(false);

  const load = useCallback(
    async (silent: boolean) => {
      if (inFlight.current) return;
      inFlight.current = true;
      if (!silent) onLoadingChange?.(true);
      try {
        const res = await landingMonitor();

        if (res.code === 0 && res.data) {
          setData(res.data);
          setError(null);
        } else {
          setError(res.msg || "加载落地监控失败");
        }
      } catch {
        setError("加载落地监控失败");
      } finally {
        inFlight.current = false;
        setLoaded(true);
        if (!silent) onLoadingChange?.(false);
      }
    },
    [onLoadingChange],
  );

  useEffect(() => {
    if (!active) return;
    void load(false);
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void load(true);
    }, REFRESH_MS);

    return () => window.clearInterval(timer);
  }, [active, load]);

  useEffect(() => {
    if (active && refreshTrigger > 0) void load(false);
  }, [refreshTrigger]);

  const summary = useMemo(() => {
    const servers = data?.servers ?? [];
    let up = 0;
    let down = 0;
    let rated = false;
    let nodes = 0;
    let activeNodes = 0;
    let connections = 0;

    for (const s of servers) {
      nodes += s.nodeCount;
      activeNodes += s.activeNodeCount;
      connections += s.connections;
      if (s.upRate !== null && s.downRate !== null) {
        up += s.upRate;
        down += s.downRate;
        rated = true;
      }
    }

    return {
      servers: servers.length,
      online: servers.filter((s) => s.online && s.status === "active").length,
      nodes,
      activeNodes,
      connections,
      up: rated ? up : null,
      down: rated ? down : null,
    };
  }, [data]);

  if (error && !data) {
    return (
      <Card>
        <CardHeader>
          <h3 className="text-sm font-semibold">落地监控</h3>
        </CardHeader>
        <CardBody>
          <div className="text-sm text-default-600">{error}</div>
        </CardBody>
      </Card>
    );
  }

  return (
    <div className="flvx-landing-text flex w-full flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2 text-xs text-default-500">
          <LiveDot ok={!error && !!data} />
          <span>
            {error ? "刷新失败，显示上次数据" : "实时数据 · 每 5 秒刷新"}
          </span>
        </div>
        <Pill tone="success">
          落地服务器 {summary.online}/{summary.servers}
        </Pill>
        <Pill tone="secondary">
          节点 {summary.activeNodes}/{summary.nodes}
        </Pill>
        <Pill tone="primary">连接 {summary.connections}</Pill>
        <Pill tone="default">
          <ArrowUp className="h-3 w-3" /> {formatRate(summary.up)}
          <ArrowDown className="ml-1 h-3 w-3" /> {formatRate(summary.down)}
        </Pill>
      </div>

      {loaded && data && data.servers.length === 0 ? (
        <Card>
          <CardBody className="py-12 text-center text-default-500">
            {data.admin
              ? "暂无落地服务器。在「节点」页「启用为落地」或接入妙妙屋共享后会显示在这里。"
              : "暂无可用的落地服务器，获授权后你的落地节点会显示在这里。"}
          </CardBody>
        </Card>
      ) : null}

      {(data?.servers ?? []).map((server) => (
        <div
          key={server.id}
          className="overflow-hidden rounded-xl border border-divider bg-content1 shadow-md"
        >
          <ServerHeader data={data!} server={server} />
          {server.nodes.length === 0 ? (
            <div className="px-4 py-6 text-center text-sm text-default-500">
              {data!.admin
                ? "该服务器暂无落地节点"
                : "你在该服务器上暂无落地节点"}
            </div>
          ) : viewMode === "grid" ? (
            <div className="p-4">
              <div className="flvx-card-grid grid gap-4">
                {server.nodes.map((node) => (
                  <NodeCard key={node.id} data={data!} node={node} />
                ))}
              </div>
            </div>
          ) : (
            <NodeTable data={data!} server={server} />
          )}
        </div>
      ))}
    </div>
  );
}
