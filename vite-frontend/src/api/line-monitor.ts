import Network from "./network";

// Plan 255 「线路监测」 (admin only). Imported only by the lazy page chunk.

/** 1 = 正常, 2 = 丢包 (first attempt failed, retry OK), 3 = 断流; 0 = 无数据. */
export type LineSampleStatus = 0 | 1 | 2 | 3;

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
}

export interface LineMonitorSeries {
  id: number;
  avg: (number | null)[];
  max: (number | null)[];
  ok: number[];
  loss: number[];
  down: number[];
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
  Network.post<{ nodes: LineMonitorNodeOption[]; sshHint: string }>(
    `${base}/options`,
    {},
  );

export const getLineMonitorPorts = (nodeId: number) =>
  Network.post<LineMonitorPortHint[]>(`${base}/ports`, { nodeId });

export const saveLineMonitorChart = (data: {
  id?: number;
  name: string;
  lines: LineMonitorLineInput[];
}) =>
  Network.post<{ id: number; warnings: string[] }>(`${base}/chart/save`, data);

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
