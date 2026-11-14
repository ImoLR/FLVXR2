import Network from "./network";

// Plan 255 「线路监测」 (admin only). Imported only by the lazy page chunk.

/** 1 = 正常, 2 = 丢包 (first attempt failed, retry OK), 3 = 断流,
 * 4 = 端口未开启 (both attempts refused, plan 255 part 2); 0 = 无数据. */
export type LineSampleStatus = 0 | 1 | 2 | 3 | 4;

/** Plan 255 part 2: how a node-target line gets its port. */
export type LinePortMode = "auto" | "existing" | "manual";

/** Last verify of an auto check port from the line's source. */
export type LineCheckState =
  | ""
  | "ok"
  | "unreachable"
  | "refused"
  | "unverified"
  | "pending"
  | "unsupported"
  | "busy"
  | "failed";

export interface LineMonitorCheckPort {
  port: number;
  state: "open" | "closing" | "";
  agentState: "" | "open" | "busy" | "refused" | "error";
  agentMsg?: string;
  targetOnline: boolean;
  capable: boolean;
}

export interface LineMonitorLive {
  ts: number;
  status: LineSampleStatus;
  ms: number | null;
  reason?: string;
}

export interface LineMonitorLine {
  id: number;
  chartId: number;
  sourceNodeId: number;
  targetNodeId: number;
  host: string;
  port: number;
  label: string;
  color: number;
  paused: number;
  sort: number;
  createdTime: number;
  sourceName: string;
  sourceOnline: boolean;
  sourceRemote?: boolean;
  targetName?: string;
  effectiveHost: string;
  live?: LineMonitorLive;
  // plan 255 part 2
  portMode?: LinePortMode | "";
  checkWindow?: number;
  checkAllow?: number;
  checkState?: LineCheckState;
  checkMsg?: string;
  checkAt?: number;
  checkPort?: LineMonitorCheckPort;
}

export interface LineMonitorChart {
  id: number;
  name: string;
  sort: number;
  lines: LineMonitorLine[];
}

export interface LineMonitorList {
  charts: LineMonitorChart[];
  totalLines: number;
  maxPerChart: number;
  maxTotal: number;
  slotSeconds: number;
  retentionDay: number;
}

export interface LineMonitorNodeOption {
  id: number;
  name: string;
  address: string;
  online: boolean;
  remote?: boolean;
  /** the agent takes MonitorPort (known while online) */
  checkPort?: boolean;
}

export interface LineMonitorPortHint {
  port: number;
  kind: "tunnel" | "landing" | "rule";
  label: string;
}

export interface LineMonitorLineInput {
  id?: number;
  sourceNodeId: number;
  targetType: "node" | "custom";
  targetNodeId: number;
  host: string;
  port: number;
  label: string;
  paused: boolean;
  portMode?: LinePortMode;
  checkWindow?: number;
  checkAllow?: boolean;
}

export interface LineMonitorSeries {
  id: number;
  avg: (number | null)[];
  max: (number | null)[];
  ok: number[];
  loss: number[];
  down: number[];
  closed?: number[];
  exp: number[];
}

export interface LineMonitorStats {
  lineId: number;
  lastTs: number;
  lastStatus: LineSampleStatus;
  lastMs: number | null;
  avg: number | null;
  p95: number | null;
  jitter: number | null;
  availability: number | null;
  noDataPct: number | null;
  measured: number;
  expected: number;
  ok: number;
  loss: number;
  down: number;
  closed?: number;
  downEvents: number;
  longestDown: number;
}

export interface LineMonitorEvent {
  lineId: number;
  start: number;
  end: number;
  slots: number;
  ongoing?: boolean;
}

export interface LineMonitorData {
  chartId: number;
  from: number;
  to: number;
  step: number;
  now: number;
  slot: number;
  lines: LineMonitorSeries[];
  stats: LineMonitorStats[];
  events: LineMonitorEvent[];
  live: Record<string, LineMonitorLive>;
  queryMs: number;
  cached?: boolean;
}

export type LineMonitorRange = "1h" | "6h" | "12h" | "1d" | "30d" | "custom";

const base = "/node/line-monitor";

export const getLineMonitorList = () =>
  Network.post<LineMonitorList>(`${base}/list`, {});

export const getLineMonitorOptions = () =>
  Network.post<{
    nodes: LineMonitorNodeOption[];
    sshHint: string;
    checkPortUnsupported?: string;
    checkWindows?: number[];
    defaultCheckWindow?: number;
  }>(`${base}/options`, {});

export const getLineMonitorPorts = (nodeId: number) =>
  Network.post<LineMonitorPortHint[]>(`${base}/ports`, { nodeId });

export const saveLineMonitorChart = (data: {
  id?: number;
  name: string;
  lines: LineMonitorLineInput[];
}) =>
  Network.post<{ id: number; warnings: string[]; checks?: LineMonitorCheck[] }>(
    `${base}/chart/save`,
    data,
  );

export const deleteLineMonitorChart = (id: number) =>
  Network.post(`${base}/chart/delete`, { id });

export const reorderLineMonitorCharts = (ids: number[]) =>
  Network.post(`${base}/chart/reorder`, { ids });

export const pauseLineMonitorLine = (id: number, paused: boolean) =>
  Network.post(`${base}/line/pause`, { id, paused });

export const getLineMonitorData = (data: {
  chartId: number;
  range: LineMonitorRange;
  from?: number;
  to?: number;
}) => Network.post<LineMonitorData>(`${base}/data`, data);

// ─── plan 255 part 2 「检测端口」 ─────────────────────────────────────

export interface LineMonitorCheck {
  lineId: number;
  port: number;
  state: LineCheckState;
  msg?: string;
}

export interface LineMonitorRotateTry {
  port: number;
  state: LineCheckState;
  msg?: string;
}

export interface LineMonitorCheckCommand {
  port: number;
  suggested: boolean;
  window: number;
  windows: number[];
  install: string;
  undo: string;
  allow: boolean;
  allowAvailable: boolean;
  allowDefault: boolean;
  allowIps: string[];
  allowNote: string;
}

export interface LineMonitorTestResult {
  nodeId: number;
  name: string;
  ok: boolean;
  ms: number | null;
  kind: string;
  msg?: string;
}

/** Re-send the target's check-port list and verify the line. */
export const verifyLineCheckPort = (lineId: number) =>
  Network.post<{ checks: LineMonitorCheck[] }>(`${base}/check-port/verify`, {
    lineId,
  });

/** 换一个端口: ≤ 3 new ports, stops at the first the source reaches. */
export const rotateLineCheckPort = (lineId: number) =>
  Network.post<{ tries: LineMonitorRotateTry[]; ok: boolean; msg: string }>(
    `${base}/check-port/rotate`,
    { lineId },
  );

/** 开启命令 for a custom target (port 0 = suggest one). */
export const getLineCheckCommand = (data: {
  host: string;
  port: number;
  window: number;
  allow?: boolean | null;
  sourceIds: number[];
}) =>
  Network.post<LineMonitorCheckCommand>(`${base}/check-port/command`, {
    ...data,
    allow: data.allow ?? undefined,
  });

/** 测试连通: one TcpPing from every source. */
export const testLineCheckPort = (data: {
  host: string;
  port: number;
  sourceIds: number[];
}) =>
  Network.post<{ results: LineMonitorTestResult[] }>(
    `${base}/check-port/test`,
    data,
  );
