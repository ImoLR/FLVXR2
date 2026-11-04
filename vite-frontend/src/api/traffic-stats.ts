import Network from "./network";

// Plan 212: 主页 traffic cards. Imported only by the lazy card chunks.

export interface ServerTrafficRow {
  serverKind: "node" | "landing";
  serverRefId: number;
  serverName: string;
  entry: boolean;
  landing: boolean;
  usedIn: number;
  usedOut: number;
  /** 0 = 不限 */
  limitBytes: number;
  exhausted: boolean;
  poolName?: string;
}

export interface ServerTrafficView {
  resetDay: number;
  /** YYYY-MM-DD; "" = the user's traffic never resets (one period) */
  periodStart: string;
  nextReset: string;
  servers: ServerTrafficRow[];
}

export type TrafficRole = "entry" | "relay" | "exit" | "landing";

export interface TrafficRankItem {
  userId?: number;
  user?: string;
  serverKind?: "node" | "landing";
  serverRefId?: number;
  roles?: TrafficRole[];
  name: string;
  deleted?: boolean;
  in: number;
  out: number;
}

export type TrafficWindowKey = "today" | "week" | "month";

export interface TrafficWindow {
  key: TrafficWindowKey;
  from: string;
  to: string;
  partial: boolean;
  users: TrafficRankItem[];
  servers: TrafficRankItem[];
}

export interface TrafficRankingView {
  /** unix ms when the daily statistics started */
  since: number;
  windows: TrafficWindow[];
}

export interface UserTrafficSplitView {
  userId: number;
  user: string;
  name: string;
  from: string;
  to: string;
  in: number;
  out: number;
  servers: TrafficRankItem[];
}

export const getServerTraffic = () =>
  Network.post<ServerTrafficView>("/traffic/servers", {});

export const getTrafficRanking = () =>
  Network.post<TrafficRankingView>("/traffic/ranking", {});

export const getUserTrafficSplit = (userId: number, window: TrafficWindowKey) =>
  Network.post<UserTrafficSplitView>("/traffic/ranking/user", {
    userId,
    window,
  });
