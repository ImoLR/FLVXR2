import type { ServerEgressView } from "./landing-egress";

import Network from "./network";

export type LandingProtocol = "ss2022" | "vless_reality_vision";
export interface SharedLandingCaps {
  status: string;
  port_min: number;
  port_max: number;
  traffic_limit_bytes: number;
  traffic_used_bytes: number;
  reset_day: number;
  max_connections: number;
  share_until: number | null;
  protocols: LandingProtocol[];
  dual_install: boolean;
}
export interface LandingIntegration {
  baseUrl: string;
  hasToken: boolean;
  lastSyncAt: number;
  syncError: string;
}
export const getLandingIntegration = () =>
  Network.post<LandingIntegration>("/landing/integration/get", {});
export const saveLandingIntegration = (data: {
  baseUrl: string;
  token: string;
}) => Network.post<LandingIntegration>("/landing/integration/save", data);
export const testLandingIntegration = (data: {
  baseUrl: string;
  token: string;
}) => Network.post<{ shareCount: number }>("/landing/integration/test", data);
export const syncLandingIntegration = () =>
  Network.post<LandingIntegration>("/landing/integration/sync", {});
export interface LandingServer {
  sharedCaps?: SharedLandingCaps;
  sharedNotice?: string;
  directUserCount?: number;
  sharedLastSyncAt?: number;
  sharedSyncError?: string;
  sharedAvailable?: boolean;
  defaultUserQuotaBytes?: number;
  id: number;
  source: "native" | "shared";
  nodeId: number;
  name: string;
  publicHost: string;
  portMin: number;
  portMax: number;
  flowType: number;
  trafficRatio: number;
  maxConnections: number;
  defaultMaxConnections: number;
  defaultMaxOnlineIps: number;
  defaultTrafficLimitBytes: number;
  defaultExpiryMode: string;
  defaultExpiryDays: number;
  defaultExpiresAt: number | null;
  maxNodesPerUser: number;
  protocols: LandingProtocol[];
  realityDest: string;
  realityServerNames: string[];
  availableUntil: number | null;
  coreMemoryMb: number;
  coreCpuPercent: number;
  status: string;
  userGroupIds: number[];
  online?: boolean;
  nodeCount?: number;
  connections?: number;
  usedBytes?: number;
  routeProfileId?: number;
  egress?: ServerEgressView;
  egressLabel?: string;
  egressWarnings?: string[];
}
export interface LandingNode {
  deletePending?: boolean;
  id: number;
  userId: number;
  userName?: string;
  landingServerId: number;
  serverName?: string;
  syncError?: string;
  sharedSyncError?: string;
  pendingReason?: string;
  name: string;
  protocol: LandingProtocol;
  port: number;
  inboundTag: string;
  maxConnections: number;
  maxOnlineIps: number;
  trafficLimitBytes: number;
  flowType: number | null;
  trafficRatio: number | null;
  expiryMode: "follow_user" | "fixed";
  expiresAt: number | null;
  effectiveExpiresAt?: number | null;
  effectiveFlowType?: number;
  effectiveTrafficRatio?: number;
  status: string;
  expiredAt?: number | null;
  expiringSoon?: boolean;
  upBytes: number;
  downBytes: number;
  billedIn: number;
  billedOut: number;
  connections?: number;
  routeProfileId?: number | null;
  effectiveRouteProfileId?: number;
  effectiveRouteProfileName?: string;
  egressLabel?: string;
}
export interface LandingLink {
  name: string;
  url: string;
  kind: "direct" | "forward";
  forwardId?: number;
  entryNodeId?: number;
}
export interface ServerQuota {
  serverKind: "node" | "landing";
  serverRefId: number;
  serverName?: string;
  limitBytes: number;
  limitOverride?: boolean;
  usedIn: number;
  usedOut: number;
  paused?: boolean;
}
export interface LandingUserSetting {
  maxNodes: number;
  quotas: ServerQuota[];
  fixedNodeCount?: number;
  serverIds?: number[];
}
export const landingServers = () =>
  Network.post<LandingServer[]>("/landing/server/list", {});
export const enableLandingServer = (nodeId: number) =>
  Network.post<LandingServer>("/landing/server/enable", { nodeId });
export const saveLandingServer = (
  server: LandingServer,
  egressRestartConfirmed = false,
) => {
  const {
    id,
    nodeId,
    name,
    publicHost,
    portMin,
    portMax,
    flowType,
    trafficRatio,
    maxConnections,
    defaultMaxConnections,
    defaultMaxOnlineIps,
    defaultTrafficLimitBytes,
    defaultUserQuotaBytes,
    defaultExpiryMode,
    defaultExpiryDays,
    defaultExpiresAt,
    maxNodesPerUser,
    protocols,
    realityDest,
    realityServerNames,
    availableUntil,
    coreMemoryMb,
    coreCpuPercent,
    status,
    userGroupIds,
  } = server;
  return Network.post<LandingServer>("/landing/server/save", {
    id,
    nodeId,
    name,
    publicHost,
    portMin,
    portMax,
    flowType,
    trafficRatio,
    maxConnections,
    defaultMaxConnections,
    defaultMaxOnlineIps,
    defaultTrafficLimitBytes,
    defaultUserQuotaBytes,
    defaultExpiryMode,
    defaultExpiryDays,
    defaultExpiresAt,
    maxNodesPerUser,
    protocols,
    realityDest,
    realityServerNames,
    availableUntil,
    coreMemoryMb,
    coreCpuPercent,
    status,
    userGroupIds,
    ...(server.routeProfileId !== undefined
      ? { routeProfileId: server.routeProfileId, egressRestartConfirmed }
      : {}),
  });
};
export const landingNodes = () =>
  Network.post<LandingNode[]>("/landing/node/list", {});
export const createLandingNode = (data: {
  landingServerId: number;
  name: string;
  protocol: LandingProtocol;
  userId?: number;
}) => Network.post<LandingNode>("/landing/node/create", data);
export const updateLandingNode = (node: LandingNode) => {
  const {
    id,
    name,
    maxConnections,
    maxOnlineIps,
    trafficLimitBytes,
    flowType,
    trafficRatio,
    expiryMode,
    expiresAt,
  } = node;
  return Network.post("/landing/node/update", {
    id,
    name,
    maxConnections,
    maxOnlineIps,
    trafficLimitBytes,
    flowType,
    trafficRatio,
    expiryMode,
    expiresAt,
    status: node.status === "disabled" ? "disabled" : "active",
    ...(node.routeProfileId !== undefined
      ? { routeProfileId: node.routeProfileId }
      : {}),
  });
};
export const deleteLandingNode = (id: number) =>
  Network.post<{ pending?: boolean }>("/landing/node/delete", { id });
export const landingLinks = (id: number) =>
  Network.post<LandingLink[]>("/landing/node/links", { id });
export const getLandingUser = (userId: number) =>
  Network.post<LandingUserSetting>("/landing/user/get", { userId });
export const saveLandingUser = (
  userId: number,
  data: LandingUserSetting & { extendFixedByMs?: number },
) =>
  Network.post("/landing/user/save", {
    userId,
    maxNodes: data.maxNodes,
    quotas: data.quotas
      .filter((quota) => quota.limitOverride !== false)
      .map(({ serverKind, serverRefId, limitBytes }) => ({
        serverKind,
        serverRefId,
        limitBytes,
      })),
    ...(data.extendFixedByMs ? { extendFixedByMs: data.extendFixedByMs } : {}),
    ...(data.serverIds ? { serverIds: data.serverIds } : {}),
  });
export const landingProtocolLabel = (value: string) =>
  value === "ss2022" ? "SS2022" : "VLESS Reality Vision";
export const landingGB = (bytes: number = 0) =>
  (bytes / 1073741824).toLocaleString("zh-CN", { maximumFractionDigits: 2 });
export const landingDate = (value?: number | null) =>
  value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "永久";
export const landingStatus: Record<string, string> = {
  active: "正常",
  error: "节点异常",
  pending: "等待同步",
  paused_quota: "流量已用完",
  paused_server_quota: "服务器额度已用完",
  paused_expired: "已到期（宽限期）",
  disabled: "已停用",
};

// 监控 「落地」 tab (plan 111). Rates are bytes per second; null = no rate yet
// (fewer than two samples, a counter reset, or data older than staleAfterMs).
export interface LandingMonitorNode {
  id: number;
  name: string;
  userId?: number;
  userName?: string;
  protocol: LandingProtocol;
  port: number;
  status: string;
  pendingReason?: string;
  syncError?: string;
  deletePending?: boolean;
  connections: number;
  maxConnections: number;
  maxOnlineIps: number;
  upRate: number | null;
  downRate: number | null;
  sampleAt: number;
  billedBytes: number;
  trafficLimitBytes: number;
}
export interface LandingMonitorServer {
  id: number;
  name: string;
  source: "native" | "shared";
  status: string;
  online: boolean;
  coreState?: string;
  notice?: string;
  syncError?: string;
  nodeCount: number;
  activeNodeCount: number;
  connections: number;
  maxConnections: number;
  upRate: number | null;
  downRate: number | null;
  sampleAt: number;
  billedBytes: number;
  shareUsedBytes?: number;
  shareLimitBytes?: number;
  quotaUsedBytes?: number;
  quotaLimitBytes?: number;
  quotaPaused?: boolean;
  nodes: LandingMonitorNode[];
}
export interface LandingMonitorData {
  generatedAt: number;
  admin: boolean;
  ageHintMs: number;
  staleAfterMs: number;
  servers: LandingMonitorServer[];
}
export const landingMonitor = () =>
  Network.post<LandingMonitorData>("/landing/monitor", {});
