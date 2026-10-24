import type {
  LandingMonitorData,
  LandingMonitorIps,
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
  ArrowDown,
  ArrowUp,
  CornerDownRight,
  Plug,
  Server,
  User,
} from "lucide-react";

import {
  landingGB,
  landingMonitor,
  landingMonitorIps,
  landingProtocolLabel,
  landingStatus,
} from "@/api/landing";
import { useH5Mode } from "@/hooks/useH5Mode";
import { MachineConnChip } from "@/pages/node/machine-conn";
import { connModeTag } from "@/pages/landing/conn-mode";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import {
  Dropdown,
  DropdownMenu,
  DropdownTrigger,
} from "@/shadcn-bridge/heroui/dropdown";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";
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
  | "cyan"
  | "default";

const toneClass: Record<Tone, string> = {
  success: "bg-success-500/10 text-success-600 dark:text-success-400",
  warning: "bg-warning-500/10 text-warning-600 dark:text-warning-400",
  danger: "bg-danger-500/10 text-danger-600 dark:text-danger-400",
  primary: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
  secondary: "bg-purple-500/10 text-purple-600 dark:text-purple-400",
  cyan: "bg-cyan-500/15 text-cyan-700 dark:bg-cyan-400/15 dark:text-cyan-300",
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
  ageHintMs,
  data,
}: {
  sampleAt: number;
  ageHintMs?: number;
  data: LandingMonitorData;
}) {
  if (!sampleAt) return <Pill tone="default">暂无实时数据</Pill>;
  const age = data.generatedAt - sampleAt;

  if (age <= (ageHintMs ?? data.ageHintMs)) return null;

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

// Each server family gets one accent: 自有 = cyan (same as the 「落地」 tab
// button), 妙妙屋共享 = purple (same as its chip). The server header is a
// tinted band in that accent and its nodes hang off a rail in the same accent,
// so "top = the server, below = its nodes" reads at a glance.
//
// Note: avoid `rounded-xl|2xl|lg` + `border` and the bridge Card for anything
// inside a server block. globals.css repaints those as full glass cards
// (white, shadow, 18–28px radius), which is what made nodes look like servers.
interface Accent {
  band: string;
  icon: string;
  tile: string;
  rail: string;
  tick: string;
  label: string;
}

const accents: Record<"native" | "shared", Accent> = {
  native: {
    band: "border-cyan-200/80 bg-cyan-50 dark:border-cyan-400/25 dark:bg-cyan-400/[0.12]",
    icon: "border-cyan-200 bg-white text-cyan-600 dark:border-cyan-400/35 dark:bg-cyan-400/10 dark:text-cyan-300",
    tile: "bg-white/80 dark:bg-white/[0.06]",
    rail: "border-cyan-300 dark:border-cyan-400/60",
    tick: "before:bg-cyan-300 dark:before:bg-cyan-400/60",
    label: "text-cyan-700 dark:text-cyan-300",
  },
  shared: {
    band: "border-purple-200/80 bg-purple-50 dark:border-purple-400/25 dark:bg-purple-400/[0.12]",
    icon: "border-purple-200 bg-white text-purple-600 dark:border-purple-400/35 dark:bg-purple-400/10 dark:text-purple-300",
    tile: "bg-white/80 dark:bg-white/[0.06]",
    rail: "border-purple-300 dark:border-purple-400/60",
    tick: "before:bg-purple-300 dark:before:bg-purple-400/60",
    label: "text-purple-700 dark:text-purple-300",
  },
};

const accentFor = (server: LandingMonitorServer) =>
  accents[server.source === "shared" ? "shared" : "native"];

function RateBoxes({
  up,
  down,
  tile,
}: {
  up: number | null | undefined;
  down: number | null | undefined;
  tile: string;
}) {
  return (
    <div className="grid grid-cols-2 gap-2">
      <div className="min-w-0 space-y-1">
        <div className="px-1 text-xs font-bold text-default-500">上传</div>
        <div
          className={`flex h-9 min-w-0 items-center gap-1.5 rounded-md px-2 ${tile}`}
        >
          <ArrowUp
            className="h-3.5 w-3.5 shrink-0 text-success-500"
            strokeWidth={2.5}
          />
          <span className="truncate font-mono text-sm font-semibold text-foreground">
            {formatRate(up)}
          </span>
        </div>
      </div>
      <div className="min-w-0 space-y-1">
        <div className="px-1 text-xs font-bold text-default-500">下载</div>
        <div
          className={`flex h-9 min-w-0 items-center gap-1.5 rounded-md px-2 ${tile}`}
        >
          <ArrowDown
            className="h-3.5 w-3.5 shrink-0 text-primary-500"
            strokeWidth={2.5}
          />
          <span className="truncate font-mono text-sm font-semibold text-foreground">
            {formatRate(down)}
          </span>
        </div>
      </div>
    </div>
  );
}

function StatBox({
  label,
  value,
  hint,
  tile,
}: {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
  tile: string;
}) {
  return (
    <div className="min-w-0 space-y-1">
      <div className="px-1 text-xs font-bold text-default-500">{label}</div>
      <div className={`flex min-h-9 items-center rounded-md px-2 py-1 ${tile}`}>
        <span className="break-words font-mono text-sm font-semibold text-foreground">
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
  `${value}/${cap > 0 ? cap : "∞"}`;

const unknownIpsHint = "节点程序版本过旧，暂不支持在线 IP";

// Mounted only when its popover/dialog opens, independently of the monitor's
// regular refresh. The list changes only on open or an explicit refresh.
function OnlineIpList({ nodeId }: { nodeId: number }) {
  const [data, setData] = useState<LandingMonitorIps | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const inFlight = useRef(false);

  const load = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setLoading(true);
    setError(null);
    try {
      const res = await landingMonitorIps(nodeId);

      if (res.code === 0 && res.data) {
        setData(res.data);
      } else {
        setError(res.msg || "加载在线 IP 失败");
      }
    } catch {
      setError("加载在线 IP 失败");
    } finally {
      inFlight.current = false;
      setLoading(false);
    }
  }, [nodeId]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm text-default-500">
          在线 IP {data?.onlineIps ?? "-"}
        </span>
        <Button
          isLoading={loading}
          size="sm"
          variant="light"
          onPress={() => void load()}
        >
          刷新
        </Button>
      </div>
      {loading ? (
        <p className="py-3 text-sm text-default-500">加载中…</p>
      ) : error ? (
        <p className="py-3 text-sm text-danger">{error}</p>
      ) : data?.onlineIps == null ? (
        <p className="py-3 text-sm text-default-500">{unknownIpsHint}</p>
      ) : data.ips.length === 0 ? (
        <p className="py-3 text-sm text-default-500">当前没有在线 IP</p>
      ) : (
        <div className="max-h-[45vh] overflow-y-auto">
          <div className="mb-1 flex justify-between gap-3 text-xs text-default-500">
            <span>IP 地址</span>
            <span>连接数</span>
          </div>
          {data.ips.map(({ ip, connections }) => (
            <div
              key={ip}
              className="flex items-center justify-between gap-3 border-t border-divider py-2 text-sm"
            >
              <span className="min-w-0 break-all font-mono">{ip}</span>
              <span className="shrink-0 font-mono tabular-nums">
                {connections}
              </span>
            </div>
          ))}
        </div>
      )}
      {!loading && !error && data?.onlineIps != null ? (
        <div className="space-y-1 text-xs text-default-500">
          {data.onlineIps > data.ips.length ? (
            <p>仅显示连接数最多的 {data.ips.length} 个 IP</p>
          ) : null}
          {data.ips.some(({ ip }) => ip.includes("/")) ? (
            <p>IPv6 按 /64 网段合并，一个网段计为 1 个在线 IP</p>
          ) : null}
          {data.sampleAt > 0 ? (
            <p>
              采样于{" "}
              {new Date(data.sampleAt).toLocaleTimeString("zh-CN", {
                hour12: false,
              })}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function IpCount({
  node,
  pill = false,
}: {
  node: LandingMonitorNode;
  pill?: boolean;
}) {
  const isH5 = useH5Mode();
  const [open, setOpen] = useState(false);
  const overLimit =
    node.onlineIps != null &&
    node.maxOnlineIps > 0 &&
    node.onlineIps > node.maxOnlineIps;
  const label = (
    <>
      IP{" "}
      <span
        className={
          overLimit ? "text-warning-600 dark:text-warning-400" : undefined
        }
      >
        {node.onlineIps ?? "-"}
      </span>
      /{node.maxOnlineIps > 0 ? node.maxOnlineIps : "∞"}
    </>
  );
  const trigger = (
    <button
      aria-label={`查看 ${node.name} 的在线 IP`}
      className="cursor-pointer whitespace-nowrap rounded transition-colors hover:bg-default-200/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
      title={node.onlineIps == null ? unknownIpsHint : "查看在线 IP"}
      type="button"
      onClick={isH5 ? () => setOpen(true) : undefined}
    >
      {pill ? (
        <Pill tone="secondary">
          <span>{label}</span>
        </Pill>
      ) : (
        label
      )}
    </button>
  );

  if (isH5) {
    return (
      <>
        {trigger}
        <Modal isOpen={open} size="sm" onOpenChange={setOpen}>
          <ModalContent
            aria-describedby={undefined}
            aria-label={`${node.name} 在线 IP`}
            className="flvx-landing"
          >
            <ModalHeader>
              <div>在线 IP</div>
              <div className="break-all text-sm font-normal text-default-500">
                {node.name}
              </div>
            </ModalHeader>
            <ModalBody>
              <OnlineIpList nodeId={node.id} />
            </ModalBody>
            <ModalFooter>
              <Button variant="light" onPress={() => setOpen(false)}>
                关闭
              </Button>
            </ModalFooter>
          </ModalContent>
        </Modal>
      </>
    );
  }

  return (
    <Dropdown>
      <DropdownTrigger>{trigger}</DropdownTrigger>
      <DropdownMenu>
        <div className="flvx-landing w-80 max-w-[85vw] space-y-2 p-3 text-left">
          <div className="text-base font-semibold">在线 IP</div>
          <div className="break-all text-sm text-default-500">{node.name}</div>
          <OnlineIpList nodeId={node.id} />
        </div>
      </DropdownMenu>
    </Dropdown>
  );
}

// Connection counts read as a plug icon + "当前/上限", like 妙妙屋; plan 141
// appends how they count (入站 / 入站+出站).
function ConnCount({ node }: { node: LandingMonitorNode }) {
  return (
    <span
      className="inline-flex items-center gap-1 whitespace-nowrap"
      title="连接数"
    >
      <Plug className="h-3.5 w-3.5 shrink-0" />
      {capText(node.connections, node.maxConnections)}
      {node.connMode ? (
        <span className="text-[11px] opacity-80">
          {connModeTag(node.connMode)}
        </span>
      ) : null}
    </span>
  );
}

// Plan 141: label of a server's 实时连接 (node sum; admins also see the limit).
function serverConnLabel(server: LandingMonitorServer, admin: boolean) {
  if (!server.connectionsMode) return "实时连接";
  const sum = server.connectionsMode;
  const limit = server.maxConnectionsMode;

  if (admin && server.maxConnections > 0 && limit && limit !== sum) {
    return `实时连接 ${connModeTag(sum)} · 上限 ${connModeTag(limit)}`;
  }

  return `实时连接 ${connModeTag(sum)}`;
}

const trafficText = (bytes: number, cap: number) =>
  `${landingGB(bytes)} / ${cap > 0 ? landingGB(cap) : "∞"} GB`;

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
  accent,
}: {
  server: LandingMonitorServer;
  data: LandingMonitorData;
  accent: Accent;
}) {
  const state = serverState(server);
  const traffic = serverTraffic(server, data.admin);

  return (
    <div
      className={`space-y-3 border-b px-3 py-3.5 md:px-4 md:py-4 ${accent.band}`}
    >
      <div className="flex min-w-0 items-start gap-3">
        <div className="relative flex-shrink-0">
          <div
            className={`flex h-10 w-10 items-center justify-center rounded-[12px] border shadow-sm ${accent.icon}`}
          >
            <Server className="h-5 w-5" />
          </div>
          <span
            className={`absolute -bottom-0.5 -right-0.5 h-3 w-3 rounded-full border-2 border-background ${server.online && server.status === "active" ? "bg-success" : "bg-danger"}`}
          />
        </div>
        <div className="min-w-0 flex-1">
          {/* Not an h2/h3: the landing text scope pins heading sizes. keep-all
              keeps 「自有落地」 together when a long name has to wrap. */}
          <div
            aria-level={2}
            className="text-lg font-bold leading-snug text-foreground [overflow-wrap:anywhere] [word-break:keep-all] md:text-xl"
            role="heading"
          >
            {server.name}
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            <Pill tone={server.source === "shared" ? "secondary" : "cyan"}>
              {server.source === "shared" ? "妙妙屋共享" : "自有落地"}
            </Pill>
            <Pill tone={state.tone}>{state.label}</Pill>
            {server.source === "shared" && server.coreState ? (
              <Pill
                tone={server.coreState === "running" ? "success" : "default"}
              >
                {server.coreState === "running" ? "核心运行中" : "核心未运行"}
              </Pill>
            ) : null}
            <Pill tone="default">
              节点 {server.activeNodeCount}/{server.nodeCount}
            </Pill>
            {server.source === "shared" ? (
              <MachineConnChip admin={data.admin} machine={server.machine} />
            ) : null}
            <AgeHint
              ageHintMs={server.ageHintMs}
              data={data}
              sampleAt={server.sampleAt}
            />
          </div>
        </div>
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
      <div className="grid grid-cols-2 gap-x-2 gap-y-3 xl:grid-cols-4 xl:gap-x-3">
        <StatBox
          hint={data.admin ? undefined : "我的节点合计"}
          label={serverConnLabel(server, data.admin)}
          tile={accent.tile}
          value={
            <span className="inline-flex items-center gap-1.5">
              <Plug className="h-4 w-4 shrink-0" />
              {data.admin
                ? capText(server.connections, server.maxConnections)
                : server.connections}
            </span>
          }
        />
        <div className="order-last col-span-2 xl:order-none">
          <RateBoxes
            down={server.downRate}
            tile={accent.tile}
            up={server.upRate}
          />
        </div>
        <StatBox
          hint={traffic.hint}
          label="本期流量"
          tile={accent.tile}
          value={traffic.value}
        />
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

function NodeTitle({
  node,
  data,
}: {
  node: LandingMonitorNode;
  data: LandingMonitorData;
}) {
  return (
    <div className="flex min-w-0 items-center gap-2">
      <div className="flex min-w-0 flex-1 items-baseline gap-2">
        <h4 className="min-w-0 shrink truncate text-sm font-semibold text-foreground">
          {node.name}
        </h4>
        {data.admin ? (
          <span className="flex min-w-0 shrink-[2] items-center gap-0.5 truncate text-xs text-default-500">
            <User className="h-3 w-3 shrink-0 self-center" />
            <span className="truncate">
              {node.userName || `用户 ${node.userId}`}
            </span>
          </span>
        ) : null}
      </div>
      <NodeStatus node={node} />
    </div>
  );
}

function NodeRates({ node }: { node: LandingMonitorNode }) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-0.5 font-mono text-sm font-semibold tabular-nums text-foreground">
      <span className="inline-flex items-center gap-1 whitespace-nowrap">
        <ArrowUp
          className="h-3.5 w-3.5 shrink-0 text-success-500"
          strokeWidth={2.5}
        />
        {formatRate(node.upRate)}
      </span>
      <span className="inline-flex items-center gap-1 whitespace-nowrap">
        <ArrowDown
          className="h-3.5 w-3.5 shrink-0 text-primary-500"
          strokeWidth={2.5}
        />
        {formatRate(node.downRate)}
      </span>
    </div>
  );
}

// Card mode: a compact, flat child tile (about half the r1 height on H5).
function NodeCard({
  node,
  data,
}: {
  node: LandingMonitorNode;
  data: LandingMonitorData;
}) {
  return (
    <div className="flex h-full flex-col gap-1.5 rounded-[14px] border border-default-200/90 bg-default-50/80 px-3 py-2 dark:border-white/10 dark:bg-white/[0.04]">
      <NodeTitle data={data} node={node} />
      <div className="flex flex-wrap items-center gap-1.5">
        <Pill tone="default">
          {landingProtocolLabel(node.protocol)} · {node.port}
        </Pill>
        <IpCount pill node={node} />
        <AgeHint
          ageHintMs={node.ageHintMs}
          data={data}
          sampleAt={node.sampleAt}
        />
      </div>
      <NodeNotes node={node} />
      <NodeRates node={node} />
      <div className="flex flex-wrap items-center gap-1.5">
        <Pill tone="primary">
          <ConnCount node={node} />
        </Pill>
        <Pill tone="success">
          {trafficText(node.billedBytes, node.trafficLimitBytes)}
        </Pill>
      </div>
    </div>
  );
}

// List mode on phones: dense rows instead of the wide table (which had to
// scroll sideways at 390px). Same fields as the table.
function NodeListRow({
  node,
  data,
}: {
  node: LandingMonitorNode;
  data: LandingMonitorData;
}) {
  return (
    <div className="space-y-1 px-3 py-2">
      <NodeTitle data={data} node={node} />
      <NodeNotes node={node} />
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <NodeRates node={node} />
        <AgeHint
          ageHintMs={node.ageHintMs}
          data={data}
          sampleAt={node.sampleAt}
        />
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-default-600">
        <ConnCount node={node} />
        <IpCount node={node} />
        <span className="whitespace-nowrap">
          {trafficText(node.billedBytes, node.trafficLimitBytes)}
        </span>
        <span className="whitespace-nowrap text-default-500">
          {landingProtocolLabel(node.protocol)} · {node.port}
        </span>
      </div>
    </div>
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
      className="min-w-full"
      classNames={{
        wrapper: "rounded-none",
        th: "bg-default-100/60 text-default-600 font-semibold text-foreground text-sm border-b border-divider px-2 py-2.5 whitespace-nowrap text-center",
        td: "border-b border-divider/50 group-data-[last=true]:border-b-0 align-middle text-center px-1.5 py-2",
        tr: "hover:bg-default-100/50 transition-colors",
      }}
    >
      <TableHeader>
        <TableColumn align="center">状态</TableColumn>
        <TableColumn align="center">落地节点</TableColumn>
        <TableColumn align="center">协议 / 端口</TableColumn>
        <TableColumn align="center">速率</TableColumn>
        <TableColumn align="center">
          <span className="inline-flex items-center gap-1" title="连接数">
            <Plug className="h-4 w-4" />
            {server.connectionsMode && server.connectionsMode !== "mixed" ? (
              <span className="text-[11px] font-normal">
                {connModeTag(server.connectionsMode)}
              </span>
            ) : null}
          </span>
        </TableColumn>
        <TableColumn align="center">IP</TableColumn>
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
              <div className="flex w-full flex-col items-center gap-1 whitespace-nowrap py-0.5 font-mono text-xs">
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
                {server.connectionsMode === "mixed" && node.connMode ? (
                  <span className="ml-1 font-sans text-[11px] text-default-500">
                    {connModeTag(node.connMode)}
                  </span>
                ) : null}
              </span>
            </TableCell>
            <TableCell>
              <span className="whitespace-nowrap font-mono text-xs text-default-600">
                <IpCount node={node} />
              </span>
            </TableCell>
            <TableCell>
              <span className="whitespace-nowrap font-mono text-xs text-default-600">
                {trafficText(node.billedBytes, node.trafficLimitBytes)}
              </span>
            </TableCell>
            <TableCell>
              <div className="flex w-full justify-center">
                <AgeHint
                  ageHintMs={node.ageHintMs}
                  data={data}
                  sampleAt={node.sampleAt}
                />
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

// Flat inset panel for the list views (no glass card styling).
const flatPanel =
  "overflow-hidden rounded-[14px] border border-default-200/90 bg-default-50/60 dark:border-white/10 dark:bg-white/[0.03]";

function ServerBlock({
  server,
  data,
  viewMode,
}: {
  server: LandingMonitorServer;
  data: LandingMonitorData;
  viewMode: "list" | "grid";
}) {
  const accent = accentFor(server);
  // Short connector from the rail to each node, on phones where nodes stack
  // in one column.
  const tick = `relative min-w-0 before:absolute before:-left-2.5 before:top-[22px] before:h-0.5 before:w-2.5 before:rounded-full md:before:hidden ${accent.tick}`;

  let nodes: ReactNode;

  if (server.nodes.length === 0) {
    nodes = (
      <div className="py-3 text-sm text-default-500">
        {data.admin ? "该服务器暂无落地节点" : "你在该服务器上暂无落地节点"}
      </div>
    );
  } else if (viewMode === "grid") {
    nodes = (
      <div className="grid grid-cols-[minmax(0,1fr)] gap-2 md:grid-cols-[repeat(auto-fill,minmax(300px,1fr))] md:gap-3">
        {server.nodes.map((node) => (
          <div key={node.id} className={tick}>
            <NodeCard data={data} node={node} />
          </div>
        ))}
      </div>
    );
  } else {
    nodes = (
      <>
        <div className={`hidden md:block ${flatPanel}`}>
          <NodeTable data={data} server={server} />
        </div>
        <div
          className={`divide-y divide-default-200/80 md:hidden dark:divide-white/10 ${flatPanel}`}
        >
          {server.nodes.map((node) => (
            <NodeListRow key={node.id} data={data} node={node} />
          ))}
        </div>
      </>
    );
  }

  return (
    <div className="overflow-hidden rounded-xl border border-divider bg-content1 shadow-md">
      <ServerHeader accent={accent} data={data} server={server} />
      {/* The rail sits under the server icon's centre (header px + w-10 / 2). */}
      <div
        className={`mb-3.5 ml-[31px] mr-2.5 border-l-2 pl-2.5 md:mb-4 md:ml-[35px] md:mr-4 md:pl-3 ${accent.rail}`}
      >
        <div
          className={`flex items-center gap-1.5 pb-2 pt-3 text-xs font-semibold ${accent.label}`}
        >
          <CornerDownRight className="h-3.5 w-3.5 shrink-0" />
          落地节点（{server.nodes.length}）
        </div>
        {nodes}
      </div>
    </div>
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
    // Plan 141: one label when every server's node sum counts the same way.
    const modes = new Set<string>();

    for (const s of servers) {
      nodes += s.nodeCount;
      activeNodes += s.activeNodeCount;
      connections += s.connections;
      if (s.connectionsMode && s.nodeCount > 0) modes.add(s.connectionsMode);
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
      connMode:
        modes.size === 0
          ? null
          : modes.size === 1
            ? (Array.from(modes)[0] as "total" | "inbound" | "mixed")
            : ("mixed" as const),
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
    <div className="flvx-landing flex w-full flex-col gap-4">
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
        <Pill tone="primary">
          <Plug className="h-3 w-3" /> {summary.connections}
          {summary.connMode ? (
            <span className="text-[11px] opacity-80">
              {connModeTag(summary.connMode)}
            </span>
          ) : null}
        </Pill>
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
        <ServerBlock
          key={server.id}
          data={data!}
          server={server}
          viewMode={viewMode}
        />
      ))}
    </div>
  );
}
